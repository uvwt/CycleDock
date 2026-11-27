package httpapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/nexusdock"
)

func newTestNexusMCPHandler(payload func() string, toolCalls *atomic.Int32) http.Handler {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "nexus-http-test", Version: "1.0.0"}, nil)
	type emptyIn struct{}
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "agentdock_context", Description: "test"},
		func(context.Context, *mcp.CallToolRequest, emptyIn) (*mcp.CallToolResult, map[string]any, error) {
			if toolCalls != nil {
				toolCalls.Add(1)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: payload()}},
			}, nil, nil
		},
	)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
}

func TestNexusDockDevices_按认证Workspace读取并允许成员查看(t *testing.T) {
	handler, pool, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	var person identityResponse
	decodeRecorder(t, login, &person)
	workspaceID := uuid.MustParse(person.Workspace.ID)
	humanID := uuid.MustParse(person.HumanAccountID)
	sourceID := uuid.MustParse(person.Source.ID)

	var mcpCalls, refreshCalls atomic.Int32
	var nodeOnline atomic.Bool
	nodeOnline.Store(true)
	mcpHandler := newTestNexusMCPHandler(func() string {
		online := "true"
		if !nodeOnline.Load() {
			online = "false"
		}
		return `{"nodes":[{"node_id":"node-a","name":"RN","online":` + online + `,"os":"linux","arch":"amd64","version":"1.1.1"}]}`
	}, &mcpCalls)

	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			respondUnified(t, w, map[string]any{"resource": upstream.URL + "/mcp", "authorization_servers": []string{upstream.URL}})
		case "/.well-known/oauth-authorization-server":
			respondUnified(t, w, map[string]any{"issuer": upstream.URL, "authorization_endpoint": upstream.URL + "/oauth/authorize", "token_endpoint": upstream.URL + "/oauth/token", "registration_endpoint": upstream.URL + "/oauth/register", "revocation_endpoint": upstream.URL + "/oauth/revoke", "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{"mcp"}})
		case "/oauth/register":
			w.WriteHeader(http.StatusCreated)
			respondUnified(t, w, map[string]any{"client_id": "device-test"})
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshCalls.Add(1)
			}
			respondUnified(t, w, map[string]any{"access_token": "device-secret", "refresh_token": "refresh-secret", "token_type": "Bearer", "expires_in": 3600, "sub": "device-account", "scope": "mcp"})
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer device-secret" {
				t.Error("未复用当前 Workspace 授权")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			mcpHandler.ServeHTTP(w, r)
		case "/v1/integrations/devices":
			t.Errorf("mcp token 不应请求 REST /v1/integrations/devices")
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Errorf("未预期的上游请求: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	svc, err := nexusdock.New(pool, nexusdock.Config{MCPURL: upstream.URL + "/mcp", WebURL: upstream.URL, PublicURL: "http://localhost:9980", EncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	authService, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	handler = NewRouter(pool, dataStore, authService, Config{NexusDock: svc})
	requestDevices := func(cookie *http.Cookie, path string) *httptest.ResponseRecorder {
		return doJSON(t, handler, http.MethodGet, path, nil, cookie, nil)
	}
	if got := requestDevices(nil, "/api/v1/agent/nexusdock/devices"); got.Code != 401 {
		t.Fatalf("未登录应拒绝: %d", got.Code)
	}
	if got := requestDevices(nil, "/api/v1/agent/nexusdock/fleet"); got.Code != 401 {
		t.Fatalf("未登录查看 fleet 应拒绝: %d", got.Code)
	}
	if got := requestDevices(cookie, "/api/v1/agent/nexusdock/devices"); got.Code != 200 || !strings.Contains(got.Body.String(), `"connected":false`) || mcpCalls.Load() != 0 {
		t.Fatalf("未连接不应请求 Cloud: %d %s", got.Code, got.Body.String())
	}
	if got := requestDevices(cookie, "/api/v1/agent/nexusdock/fleet"); got.Code != 200 || !strings.Contains(got.Body.String(), `"connected":false`) {
		t.Fatalf("未连接 fleet 应返回 connected:false: %d %s", got.Code, got.Body.String())
	}
	ctx := context.Background()
	start, err := svc.BeginAuthorization(ctx, workspaceID, humanID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	authorizeURL, err := url.Parse(start.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CompleteAuthorization(ctx, workspaceID, humanID, sourceID, authorizeURL.Query().Get("state"), "code"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE workspace_nexusdock_connections SET access_expires_at=now()-interval '1 minute' WHERE workspace_id=$1", workspaceID); err != nil {
		t.Fatal(err)
	}
	got := requestDevices(cookie, "/api/v1/agent/nexusdock/devices")
	var response nexusDockDevicesResponse
	decodeRecorder(t, got, &response)
	if got.Code != 200 || !response.Available || !response.Connected || len(response.Devices) != 1 || response.Devices[0].ID != "node-a" || !response.Devices[0].Online || refreshCalls.Load() != 1 || mcpCalls.Load() != 1 || strings.Contains(got.Body.String(), "secret") {
		t.Fatalf("应刷新授权并只返回摘要: %d %s refresh=%d mcp=%d", got.Code, got.Body.String(), refreshCalls.Load(), mcpCalls.Load())
	}
	// 短缓存复用：连续查询 fleet 与 devices 不重复打上游 MCP
	if got := requestDevices(cookie, "/api/v1/agent/nexusdock/fleet"); got.Code != 200 || mcpCalls.Load() != 1 {
		t.Fatalf("fleet 应复用 devices 刚写入的短缓存: code=%d mcpCalls=%d", got.Code, mcpCalls.Load())
	}
	// 穿透刷新：携带 ?refresh=1 应绕过短缓存拿到最新设备状态
	nodeOnline.Store(false)
	got = requestDevices(cookie, "/api/v1/agent/nexusdock/devices?refresh=1")
	decodeRecorder(t, got, &response)
	if got.Code != 200 || mcpCalls.Load() != 2 || len(response.Devices) != 1 || response.Devices[0].Online {
		t.Fatalf("?refresh=1 应穿透缓存获取最新状态: code=%d mcpCalls=%d resp=%+v", got.Code, mcpCalls.Load(), response)
	}

	project, err := dataStore.CreateProject(ctx, workspaceID, "Device selection", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	comment := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID.String()+"/comments",
		map[string]any{"content": "Inspect", "dispatch_mode": "enqueue", "device_target": map[string]string{"node_id": "node-a", "name": "RN"}},
		cookie, map[string]string{"Idempotency-Key": "selected-device"})
	if comment.Code != http.StatusCreated {
		t.Fatal(comment.Body.String())
	}
	var selectedNode string
	if err := pool.QueryRow(ctx, "SELECT device_node_id FROM card_turns WHERE card_id=$1", card.ID).Scan(&selectedNode); err != nil || selectedNode != "node-a" {
		t.Fatalf("HTTP 选择未持久化: %s %v", selectedNode, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE workspace_memberships SET role='member' WHERE workspace_id=$1 AND human_account_id=$2", workspaceID, humanID); err != nil {
		t.Fatal(err)
	}
	if got := requestDevices(cookie, "/api/v1/agent/nexusdock/devices"); got.Code != 200 {
		t.Fatalf("成员应能查看设备: %d %s", got.Code, got.Body.String())
	}
	if got := requestDevices(cookie, "/api/v1/agent/nexusdock/fleet"); got.Code != 200 {
		t.Fatalf("成员应能查看 fleet: %d %s", got.Code, got.Body.String())
	}
	if got := requestDevices(cookie, "/api/v1/agent/nexusdock"); got.Code != 403 {
		t.Fatalf("成员仍不能读取连接管理: %d", got.Code)
	}
	other, err := dataStore.CreateWorkspace(ctx, "Other Workspace")
	if err != nil {
		t.Fatal(err)
	}
	member, err := dataStore.CreateSource(ctx, other.ID, domain.SourceKindHuman, "Member")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO workspace_memberships (workspace_id,human_account_id,source_id,role,is_personal) VALUES ($1,$2,$3,'member',false)", other.ID, humanID, member.ID); err != nil {
		t.Fatal(err)
	}
	switched := doJSON(t, handler, http.MethodPost, "/api/v1/workspaces/"+other.ID.String()+"/switch", map[string]any{}, cookie, nil)
	if switched.Code != 200 {
		t.Fatal(switched.Body.String())
	}
	before := mcpCalls.Load()
	got = requestDevices(cookie, "/api/v1/agent/nexusdock/devices?workspace_id="+workspaceID.String())
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"connected":false`) || mcpCalls.Load() != before {
		t.Fatalf("请求参数不能切换设备授权 Workspace: %d %s", got.Code, got.Body.String())
	}
}

func TestNexusDockDevicesAndFleet_上游401自动刷新重试与错误区分(t *testing.T) {
	handler, pool, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	var person identityResponse
	decodeRecorder(t, login, &person)
	workspaceID := uuid.MustParse(person.Workspace.ID)
	humanID := uuid.MustParse(person.HumanAccountID)
	sourceID := uuid.MustParse(person.Source.ID)

	var (
		mu             sync.Mutex
		acceptedToken  = "token-v2"
		nextToken      = "token-v2"
		refreshStatus  = http.StatusOK
		mcpForceStatus = 0
		mcpDelay       time.Duration
		refreshCalls   atomic.Int32
		mcpToolCalls   atomic.Int32
	)
	mcpHandler := newTestNexusMCPHandler(func() string {
		mu.Lock()
		delay := mcpDelay
		mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		return `{"nodes":[{"node_id":"node-tianyi","name":"TianYi","online":true,"os":"windows","arch":"amd64","version":"1.1.1"}]}`
	}, &mcpToolCalls)

	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			respondUnified(t, w, map[string]any{"resource": upstream.URL + "/mcp", "authorization_servers": []string{upstream.URL}})
		case "/.well-known/oauth-authorization-server":
			respondUnified(t, w, map[string]any{"issuer": upstream.URL, "authorization_endpoint": upstream.URL + "/oauth/authorize", "token_endpoint": upstream.URL + "/oauth/token", "registration_endpoint": upstream.URL + "/oauth/register", "revocation_endpoint": upstream.URL + "/oauth/revoke", "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{"mcp"}})
		case "/oauth/register":
			w.WriteHeader(http.StatusCreated)
			respondUnified(t, w, map[string]any{"client_id": "auth-retry-test"})
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("grant_type") == "authorization_code" {
				respondUnified(t, w, map[string]any{"access_token": "token-v1", "refresh_token": "refresh-v1", "token_type": "Bearer", "expires_in": 3600, "sub": "acct-1", "scope": "mcp"})
				return
			}
			refreshCalls.Add(1)
			mu.Lock()
			st := refreshStatus
			tok := nextToken
			mu.Unlock()
			if st != http.StatusOK {
				w.WriteHeader(st)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			respondUnified(t, w, map[string]any{"access_token": tok, "refresh_token": "refresh-v2", "token_type": "Bearer", "expires_in": 3600, "sub": "acct-1", "scope": "mcp"})
		case "/mcp":
			mu.Lock()
			forced := mcpForceStatus
			want := "Bearer " + acceptedToken
			mu.Unlock()
			if forced != 0 {
				w.WriteHeader(forced)
				_, _ = w.Write([]byte("upstream-secret"))
				return
			}
			if r.Header.Get("Authorization") != want {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
				return
			}
			mcpHandler.ServeHTTP(w, r)
		case "/v1/integrations/devices":
			t.Errorf("任何场景都不应回退调用 REST /v1/integrations/devices")
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	svc, err := nexusdock.New(pool, nexusdock.Config{
		MCPURL:        upstream.URL + "/mcp",
		WebURL:        upstream.URL,
		PublicURL:     "http://localhost:9980",
		EncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		HTTPClient:    upstream.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	authService, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	handler = NewRouter(pool, dataStore, authService, Config{NexusDock: svc})

	ctx := context.Background()
	start, err := svc.BeginAuthorization(ctx, workspaceID, humanID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	authorizeURL, err := url.Parse(start.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CompleteAuthorization(ctx, workspaceID, humanID, sourceID, authorizeURL.Query().Get("state"), "code"); err != nil {
		t.Fatal(err)
	}

	// 1. 并发冷缓存请求：devices 与 fleet 同时触发，且初始 token-v1 被 MCP 拒 401 -> 自动 ForceRefresh 换 token-v2 重试成功，且上游仅打一次
	mu.Lock()
	mcpDelay = 60 * time.Millisecond
	mu.Unlock()
	var wg sync.WaitGroup
	var devRec, fleetRec *httptest.ResponseRecorder
	wg.Add(2)
	go func() {
		defer wg.Done()
		devRec = doJSON(t, handler, http.MethodGet, "/api/v1/agent/nexusdock/devices", nil, cookie, nil)
	}()
	go func() {
		defer wg.Done()
		fleetRec = doJSON(t, handler, http.MethodGet, "/api/v1/agent/nexusdock/fleet", nil, cookie, nil)
	}()
	wg.Wait()
	if devRec.Code != http.StatusOK || fleetRec.Code != http.StatusOK {
		t.Fatalf("401 自动刷新重试后应成功: dev=%d %s fleet=%d %s", devRec.Code, devRec.Body.String(), fleetRec.Code, fleetRec.Body.String())
	}
	if refreshCalls.Load() != 1 || mcpToolCalls.Load() != 1 {
		t.Fatalf("并发冷缓存应合并为单次刷新与单次 MCP 调用: refresh=%d mcp=%d", refreshCalls.Load(), mcpToolCalls.Load())
	}

	mu.Lock()
	mcpDelay = 0
	// 2. 刷新成功但新 token 仍被上游 MCP 拒 401 -> 返回 409 nexusdock_reconnect_required
	acceptedToken = "token-v999"
	nextToken = "token-v3"
	mu.Unlock()
	got := doJSON(t, handler, http.MethodGet, "/api/v1/agent/nexusdock/devices?refresh=true", nil, cookie, nil)
	if got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "nexusdock_reconnect_required") {
		t.Fatalf("刷新后再次 401 应返回 409 nexusdock_reconnect_required: %d %s", got.Code, got.Body.String())
	}
	got = doJSON(t, handler, http.MethodGet, "/api/v1/agent/nexusdock/fleet?refresh=true", nil, cookie, nil)
	if got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "nexusdock_reconnect_required") {
		t.Fatalf("fleet 刷新后再次 401 应返回 409 nexusdock_reconnect_required: %d %s", got.Code, got.Body.String())
	}

	// 3. 401 后刷新本身被授权服务器拒 400 (refresh_token 失效) -> 返回 409 nexusdock_reconnect_required
	mu.Lock()
	refreshStatus = http.StatusBadRequest
	mu.Unlock()
	got = doJSON(t, handler, http.MethodGet, "/api/v1/agent/nexusdock/devices?refresh=true", nil, cookie, nil)
	if got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "nexusdock_reconnect_required") {
		t.Fatalf("refresh 失败应返回 409 nexusdock_reconnect_required: %d %s", got.Code, got.Body.String())
	}

	// 4. 上游 MCP 临时 503 故障 -> 返回 502 nexusdock_unavailable，绝不误报授权过期
	mu.Lock()
	mcpForceStatus = http.StatusServiceUnavailable
	mu.Unlock()
	got = doJSON(t, handler, http.MethodGet, "/api/v1/agent/nexusdock/devices?refresh=true", nil, cookie, nil)
	if got.Code != http.StatusBadGateway || !strings.Contains(got.Body.String(), "nexusdock_unavailable") {
		t.Fatalf("上游 503 应返回 502 nexusdock_unavailable: %d %s", got.Code, got.Body.String())
	}
	got = doJSON(t, handler, http.MethodGet, "/api/v1/agent/nexusdock/fleet?refresh=true", nil, cookie, nil)
	if got.Code != http.StatusBadGateway || !strings.Contains(got.Body.String(), "nexusdock_unavailable") {
		t.Fatalf("fleet 上游 503 应返回 502 nexusdock_unavailable: %d %s", got.Code, got.Body.String())
	}
}
