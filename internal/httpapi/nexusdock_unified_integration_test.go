package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/nexusdock"
)

// A 登录 CycleDock，B 通过 OAuth 明确授权设备；state 必须仍绑定 A 的 Human/Workspace。
func TestNexusDockManualAuthorization_允许不同资源账号但拒绝跨用户与重放(t *testing.T) {
	handler, pool, _ := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatal(login.Body.String())
	}
	var person identityResponse
	decodeRecorder(t, login, &person)
	workspace := uuid.MustParse(person.Workspace.ID)
	human := uuid.MustParse(person.HumanAccountID)
	source := uuid.MustParse(person.Source.ID)

	subject := "account-b"
	revoked := 0
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			respondUnified(t, w, map[string]any{"resource": upstream.URL + "/mcp", "authorization_servers": []string{upstream.URL}})
		case "/.well-known/oauth-authorization-server":
			respondUnified(t, w, map[string]any{
				"issuer": upstream.URL, "authorization_endpoint": upstream.URL + "/oauth/authorize",
				"token_endpoint": upstream.URL + "/oauth/token", "registration_endpoint": upstream.URL + "/oauth/register",
				"revocation_endpoint": upstream.URL + "/oauth/revoke", "code_challenge_methods_supported": []string{"S256"},
				"token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{"mcp"},
			})
		case "/oauth/register":
			w.WriteHeader(http.StatusCreated)
			respondUnified(t, w, map[string]any{"client_id": "cycledock-test-client"})
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("grant_type") == "authorization_code" && r.Form.Get("code_verifier") == "" {
				t.Errorf("OAuth must exchange code with PKCE: %v", r.Form)
			}
			respondUnified(t, w, map[string]any{
				"access_token": "access-token", "refresh_token": "refresh-token",
				"token_type": "Bearer", "expires_in": 3600, "scope": "mcp", "sub": subject,
			})
		case "/oauth/revoke":
			revoked++
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	svc, err := nexusdock.New(pool, nexusdock.Config{
		MCPURL: upstream.URL + "/mcp", WebURL: upstream.URL, PublicURL: "http://localhost:9980",
		EncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		HTTPClient:    upstream.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	begin := func() string {
		t.Helper()
		start, err := svc.BeginAuthorization(ctx, workspace, human, source)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(start.URL)
		if err != nil {
			t.Fatal(err)
		}
		if u.Query().Get("scope") != "mcp" || u.Query().Get("code_challenge_method") != "S256" ||
			u.Query().Get("code_challenge") == "" || u.Query().Get("resource") == "" {
			t.Fatal("missing standard OAuth parameters")
		}
		return u.Query().Get("state")
	}
	state := begin()
	if err := svc.CompleteAuthorization(ctx, workspace, uuid.New(), source, state, "code"); !errors.Is(err, nexusdock.ErrInvalidState) {
		t.Fatalf("other Human must not consume state: %v", err)
	}
	if err := svc.CompleteAuthorization(ctx, workspace, human, source, state, "code"); err != nil {
		t.Fatalf("B must be permitted to authorize A's workspace: %v", err)
	}
	if err := svc.CompleteAuthorization(ctx, workspace, human, source, state, "code"); !errors.Is(err, nexusdock.ErrInvalidState) {
		t.Fatalf("replay must be rejected: %v", err)
	}
	status, err := svc.Status(ctx, workspace)
	if err != nil || !status.Connected || status.AuthorizedSubject != "account-b" {
		t.Fatalf("actual resource account not displayed: %+v %v", status, err)
	}
	connection, err := svc.Runtime(ctx, workspace)
	if err != nil || connection.AccessToken != "access-token" {
		t.Fatalf("A should use B's explicitly authorized token: %v", err)
	}
	// 旧资源 B 的 refresh token 不能被 OAuth 上游悄悄换成 C。
	if _, err := pool.Exec(ctx, "UPDATE workspace_nexusdock_connections SET access_expires_at=now()-interval '1 minute' WHERE workspace_id=$1", workspace); err != nil {
		t.Fatal(err)
	}
	subject = "account-c"
	if _, err := svc.Runtime(ctx, workspace); !errors.Is(err, nexusdock.ErrConnectionExpired) {
		t.Fatalf("resource account switch during refresh must be rejected: %v", err)
	}
	if status, _ := svc.Status(ctx, workspace); status.AuthorizedSubject != "account-b" {
		t.Fatal("failed refresh should not change stored account identity")
	}
	subject = "account-b"
	if _, err := svc.Runtime(ctx, workspace); err != nil {
		t.Fatalf("original resource account refresh should succeed: %v", err)
	}
	if err := svc.Disconnect(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if revoked != 1 {
		t.Fatalf("disconnect must revoke token: %d", revoked)
	}
	deniedState := begin()
	if err := svc.DenyAuthorization(ctx, workspace, human, source, deniedState); err != nil {
		t.Fatal(err)
	}
	if err := svc.CompleteAuthorization(ctx, workspace, human, source, deniedState, "code"); !errors.Is(err, nexusdock.ErrInvalidState) {
		t.Fatalf("denial must invalidate state: %v", err)
	}
	// 不返回 sub 的第一方 token 无法识别资源所有者，仍需拒绝并撤销。
	subject = ""
	unknownState := begin()
	if err := svc.CompleteAuthorization(ctx, workspace, human, source, unknownState, "code"); !errors.Is(err, nexusdock.ErrAuthorization) {
		t.Fatalf("unknown resource subject must not be persisted: %v", err)
	}
	if revoked != 2 {
		t.Fatalf("incomplete grant must be revoked: %d", revoked)
	}
	if status, _ := svc.Status(ctx, workspace); status.Connected {
		t.Fatal("unknown account must not become connected")
	}
}

func respondUnified(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}
