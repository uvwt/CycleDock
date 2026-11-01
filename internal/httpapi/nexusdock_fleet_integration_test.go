package httpapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/nexusdock"
)

func TestNexusDockFleet_未登录拒绝且未连接返回ConnectedFalse(t *testing.T) {
	handler, pool, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]

	requestFleet := func(c *http.Cookie) *httptest.ResponseRecorder {
		return doJSON(t, handler, http.MethodGet, "/api/v1/agent/nexusdock/fleet", nil, c, nil)
	}

	// 1. 未登录访问返回 401
	if got := requestFleet(nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问 fleet 应返回 401，实际: %d", got.Code)
	}

	// 2. 已登录但未连接 NexusDock，返回 connected: false
	got := requestFleet(cookie)
	if got.Code != http.StatusOK {
		t.Fatalf("已登录但未连接应返回 200，实际: %d", got.Code)
	}
	var resp nexusdock.FleetResponse
	decodeRecorder(t, got, &resp)
	if resp.Connected {
		t.Errorf("未连接时 Connected 必须为 false")
	}
	if len(resp.Devices) != 0 {
		t.Errorf("未连接时设备列表必须为空")
	}

	// 3. 连接后平滑降级（MCP 离线时走 REST 提取设备并正确返回）
	var person identityResponse
	decodeRecorder(t, login, &person)
	workspaceID := uuid.MustParse(person.Workspace.ID)
	humanID := uuid.MustParse(person.HumanAccountID)
	sourceID := uuid.MustParse(person.Source.ID)

	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			respondUnified(t, w, map[string]any{"resource": upstream.URL + "/mcp", "authorization_servers": []string{upstream.URL}})
		case "/.well-known/oauth-authorization-server":
			respondUnified(t, w, map[string]any{"issuer": upstream.URL, "authorization_endpoint": upstream.URL + "/oauth/authorize", "token_endpoint": upstream.URL + "/oauth/token", "registration_endpoint": upstream.URL + "/oauth/register", "revocation_endpoint": upstream.URL + "/oauth/revoke", "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{"mcp"}})
		case "/oauth/register":
			w.WriteHeader(http.StatusCreated)
			respondUnified(t, w, map[string]any{"client_id": "fleet-test"})
		case "/oauth/token":
			respondUnified(t, w, map[string]any{"access_token": "fleet-secret", "refresh_token": "refresh-secret", "token_type": "Bearer", "expires_in": 3600, "sub": "fleet-account", "scope": "mcp"})
		case "/v1/integrations/devices":
			respondUnified(t, w, map[string]any{"devices": []any{
				map[string]any{"id": "node-fleet-1", "name": "Fleet Device 1", "online": true, "os": "darwin", "arch": "arm64", "agent_version": "1.1.1"},
			}})
		default:
			w.WriteHeader(http.StatusOK)
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

	fleetGot := doJSON(t, handler, http.MethodGet, "/api/v1/agent/nexusdock/fleet", nil, cookie, nil)
	if fleetGot.Code != http.StatusOK {
		t.Fatalf("连接后请求 fleet 应返回 200，实际: %d %s", fleetGot.Code, fleetGot.Body.String())
	}
	var fleetResp nexusdock.FleetResponse
	decodeRecorder(t, fleetGot, &fleetResp)
	if !fleetResp.Connected || len(fleetResp.Devices) != 1 || fleetResp.Devices[0].ID != "node-fleet-1" {
		t.Fatalf("降级返回应包含 node-fleet-1: got=%+v", fleetResp)
	}
	if strings.Contains(fleetGot.Body.String(), "fleet-secret") {
		t.Fatal("响应不得泄露凭据 token")
	}
}
