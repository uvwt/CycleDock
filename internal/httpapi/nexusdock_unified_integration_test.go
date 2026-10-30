package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/nexusdock"
)

// 真实 PostgreSQL 下验证一体登录使用独立 OAuth 授权，绝不将 B 账号设备授给 A。
func TestNexusDockUnifiedAuthorization_拒绝跨账号授权并记住用户拒绝(t *testing.T) {
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
	var subject = "account-b"
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
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code_verifier") == "" {
				t.Errorf("invalid oauth exchange: %v", r.Form)
			}
			respondUnified(t, w, map[string]any{"access_token": "access-token", "refresh_token": "refresh-token", "token_type": "Bearer", "expires_in": 3600, "scope": "mcp", "sub": subject})
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
	expected := nexusdock.AuthorizationOptions{Automatic: true, ExpectedSubject: "account-a", ReturnTo: "/chat/project"}
	should, err := svc.ShouldAutoAuthorize(ctx, workspace)
	if err != nil || !should {
		t.Fatalf("first sign in should prompt consent: %v %v", should, err)
	}
	first, err := svc.BeginAuthorization(ctx, workspace, human, source, expected)
	if err != nil {
		t.Fatal(err)
	}
	firstURL, err := url.Parse(first.URL)
	if err != nil {
		t.Fatal(err)
	}
	firstState := firstURL.Query().Get("state")
	if firstState == "" || firstURL.Query().Get("scope") != "mcp" {
		t.Fatal("invalid OAuth authorization start")
	}
	// 另一个 Human 即使得到 state 也不能消费此授权流程。
	if _, err := svc.CompleteAuthorization(ctx, workspace, uuid.New(), source, firstState, "code"); !errors.Is(err, nexusdock.ErrInvalidState) {
		t.Fatalf("cross-human callback: %v", err)
	}
	if _, err := svc.CompleteAuthorization(ctx, workspace, human, source, firstState, "code"); !errors.Is(err, nexusdock.ErrIdentityMismatch) {
		t.Fatalf("cross-account OAuth grant must be rejected: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("cross-account grant should be revoked; got=%d", revoked)
	}
	if status, _ := svc.Status(ctx, workspace); status.Connected {
		t.Fatal("cross-account token must never persist")
	}

	subject = "account-a"
	second, err := svc.BeginAuthorization(ctx, workspace, human, source, expected)
	if err != nil {
		t.Fatal(err)
	}
	secondURL, _ := url.Parse(second.URL)
	result, err := svc.CompleteAuthorization(ctx, workspace, human, source, secondURL.Query().Get("state"), "code")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Automatic || result.ReturnTo != "/chat/project" {
		t.Fatalf("incorrect return context %+v", result)
	}
	if _, err := svc.CompleteAuthorization(ctx, workspace, human, source, secondURL.Query().Get("state"), "code"); !errors.Is(err, nexusdock.ErrInvalidState) {
		t.Fatalf("replay should fail: %v", err)
	}
	should, err = svc.ShouldAutoAuthorize(ctx, workspace)
	if err != nil || should {
		t.Fatal("connected user should never be prompted twice")
	}
	connection, err := svc.Runtime(ctx, workspace)
	if err != nil || connection.AccessToken != "access-token" {
		t.Fatalf("authorized connection missing: %v", err)
	}

	if err := svc.Disconnect(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	should, err = svc.ShouldAutoAuthorize(ctx, workspace)
	if err != nil || should {
		t.Fatal("manual disconnect must suppress next-login consent")
	}
	manual, err := svc.BeginAuthorization(ctx, workspace, human, source, nexusdock.AuthorizationOptions{ExpectedSubject: "account-a", ReturnTo: "/"})
	if err != nil {
		t.Fatal(err)
	}
	manualURL, _ := url.Parse(manual.URL)
	resumed, err := svc.CompleteAuthorization(ctx, workspace, human, source, manualURL.Query().Get("state"), "code")
	if err != nil || resumed.Automatic {
		t.Fatalf("manual reauthorization failed: %v %+v", err, resumed)
	}
	if err := svc.Disconnect(ctx, workspace); err != nil {
		t.Fatal(err)
	}

	third, err := svc.BeginAuthorization(ctx, workspace, human, source, expected)
	if err != nil {
		t.Fatal(err)
	}
	thirdURL, _ := url.Parse(third.URL)
	denied, err := svc.DenyAuthorization(ctx, workspace, human, source, thirdURL.Query().Get("state"))
	if err != nil || !denied.Automatic {
		t.Fatalf("consent denial failed: %v %+v", err, denied)
	}
	should, err = svc.ShouldAutoAuthorize(ctx, workspace)
	if err != nil || should {
		t.Fatal("explicit denial must persist across future logins")
	}
	if strings.TrimSpace(denied.ReturnTo) != "/chat/project" {
		t.Fatal("denied consent should resume chat")
	}
}

func respondUnified(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}
