package httpapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/nexusdock"
)

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
	var directoryCalls, refreshCalls atomic.Int32
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
		case "/v1/integrations/devices":
			directoryCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer device-secret" {
				t.Error("未复用当前 Workspace 授权")
				w.WriteHeader(401)
				return
			}
			respondUnified(t, w, map[string]any{"devices": []any{map[string]any{"id": "node-a", "name": "RN", "online": true, "os": "linux", "arch": "amd64"}}})
		case "/mcp":
			// 后台 Attempt Runner 建立 MCP 连接探测
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("页面设备列表不应调用 MCP 或其他接口: %s", r.URL.Path)
			w.WriteHeader(404)
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
	if got := requestDevices(cookie, "/api/v1/agent/nexusdock/devices"); got.Code != 200 || !strings.Contains(got.Body.String(), `"connected":false`) || directoryCalls.Load() != 0 {
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
	if got.Code != 200 || !response.Available || !response.Connected || len(response.Devices) != 1 || response.Devices[0].ID != "node-a" || refreshCalls.Load() != 1 || strings.Contains(got.Body.String(), "secret") {
		t.Fatalf("应刷新授权并只返回摘要: %d %s refresh=%d", got.Code, got.Body.String(), refreshCalls.Load())
	}
	project, err := dataStore.CreateProject(ctx, workspaceID, "Device selection", "", "")
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
	before := directoryCalls.Load()
	got = requestDevices(cookie, "/api/v1/agent/nexusdock/devices?workspace_id="+workspaceID.String())
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"connected":false`) || directoryCalls.Load() != before {
		t.Fatalf("请求参数不能切换设备授权 Workspace: %d %s", got.Code, got.Body.String())
	}
}
