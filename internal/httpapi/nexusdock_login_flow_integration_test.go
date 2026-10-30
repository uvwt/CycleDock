package httpapi

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/nexusdock"
)

func TestOIDCLoginAutomaticallyContinuesNexusDockConsentAndSkipsOnRepeat(t *testing.T) {
	_, pool, dataStore := newHTTPIntegration(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuer := ""
	nonce := ""
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			respondUnified(t, w, map[string]any{
				"issuer": provider.URL, "authorization_endpoint": provider.URL + "/oidc/authorize",
				"token_endpoint": provider.URL + "/oidc/token", "jwks_uri": provider.URL + "/oidc/jwks",
				"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/oidc/jwks":
			respondUnified(t, w, map[string]any{"keys": []any{map[string]any{
				"kty": "RSA", "alg": "RS256", "kid": "test-key", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}}})
		case "/oidc/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("code_verifier") == "" {
				t.Error("OIDC PKCE verifier missing")
			}
			claims := map[string]any{
				"iss": issuer, "sub": "nexus-a", "aud": "cycle-client", "nonce": nonce,
				"iat": time.Now().Add(-time.Second).Unix(), "exp": time.Now().Add(time.Hour).Unix(),
				"email": "a@example.test", "email_verified": true,
			}
			head, _ := json.Marshal(map[string]any{"typ": "JWT", "alg": "RS256", "kid": "test-key"})
			payload, _ := json.Marshal(claims)
			text := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(payload)
			hash := sha256.Sum256([]byte(text))
			signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
			if err != nil {
				t.Error(err)
			}
			jwt := text + "." + base64.RawURLEncoding.EncodeToString(signature)
			respondUnified(t, w, map[string]any{"access_token": "oidc-access", "token_type": "Bearer", "expires_in": 3600, "id_token": jwt})
		case "/.well-known/oauth-protected-resource/mcp":
			respondUnified(t, w, map[string]any{"resource": provider.URL + "/mcp", "authorization_servers": []string{provider.URL}})
		case "/.well-known/oauth-authorization-server":
			respondUnified(t, w, map[string]any{
				"issuer": provider.URL, "authorization_endpoint": provider.URL + "/oauth/authorize",
				"token_endpoint": provider.URL + "/oauth/token", "registration_endpoint": provider.URL + "/oauth/register",
				"revocation_endpoint": provider.URL + "/oauth/revoke", "scopes_supported": []string{"mcp"},
				"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
			})
		case "/oauth/register":
			w.WriteHeader(http.StatusCreated)
			respondUnified(t, w, map[string]any{"client_id": "cycle-oauth-client"})
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			respondUnified(t, w, map[string]any{
				"access_token": "device-access", "refresh_token": "device-refresh", "token_type": "Bearer",
				"expires_in": 3600, "scope": "mcp", "sub": "nexus-a",
			})
		case "/oauth/revoke":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	issuer = provider.URL
	authService, err := auth.New(pool, auth.Config{OIDC: auth.OIDCConfig{
		Issuer: provider.URL, ClientID: "cycle-client", PublicURL: "http://localhost:9980", HTTPClient: provider.Client(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	deviceService, err := nexusdock.New(pool, nexusdock.Config{
		MCPURL: provider.URL + "/mcp", WebURL: provider.URL, PublicURL: "http://localhost:9980",
		EncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		HTTPClient:    provider.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewRouter(pool, dataStore, authService, Config{NexusDock: deviceService})
	// 登录入口只有一次；授权流程由服务端在 OIDC 回调中连续发起。
	start := httptest.NewRecorder()
	handler.ServeHTTP(start, httptest.NewRequest(http.MethodGet, "/auth/nexusdock/start?return_to=%2Fprojects", nil))
	if start.Code != http.StatusFound {
		t.Fatalf("OIDC start: %d %s", start.Code, start.Body.String())
	}
	loginURL, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	nonce = loginURL.Query().Get("nonce")
	state := loginURL.Query().Get("state")
	if nonce == "" || state == "" {
		t.Fatal("OIDC login missing nonce/state")
	}
	var stateCookie *http.Cookie
	for _, cookie := range start.Result().Cookies() {
		if cookie.Name == oidcStateCookieName {
			stateCookie = cookie
		}
	}
	if stateCookie == nil {
		t.Fatal("OIDC state cookie missing")
	}
	first := httptest.NewRequest(http.MethodGet, "/auth/nexusdock/callback?state="+url.QueryEscape(state)+"&code=oidc-code", nil)
	first.AddCookie(stateCookie)
	callback := httptest.NewRecorder()
	handler.ServeHTTP(callback, first)
	if callback.Code != http.StatusFound {
		t.Fatalf("OIDC callback: %d %s", callback.Code, callback.Body.String())
	}
	authorizationURL, err := url.Parse(callback.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if authorizationURL.Path != "/oauth/authorize" || authorizationURL.Query().Get("scope") != "mcp" {
		t.Fatalf("first login must continue to explicit device consent: %s", callback.Header().Get("Location"))
	}
	var sessionCookie *http.Cookie
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("Chat session must exist before device consent")
	}
	oauthState := authorizationURL.Query().Get("state")
	if oauthState == "" {
		t.Fatal("OAuth PKCE state missing")
	}
	approve := httptest.NewRequest(http.MethodGet, "/auth/nexusdock-device/callback?state="+url.QueryEscape(oauthState)+"&code=device-code", nil)
	approve.AddCookie(sessionCookie)
	done := httptest.NewRecorder()
	handler.ServeHTTP(done, approve)
	if done.Code != http.StatusFound || done.Header().Get("Location") != "/projects" {
		t.Fatalf("consent must resume original Chat path: %d %s", done.Code, done.Header().Get("Location"))
	}
	status := httptest.NewRequest(http.MethodGet, "/api/v1/agent/nexusdock", nil)
	status.AddCookie(sessionCookie)
	connected := httptest.NewRecorder()
	handler.ServeHTTP(connected, status)
	if connected.Code != http.StatusOK || !strings.Contains(connected.Body.String(), strings.Join([]string{string('"'), "connected", string('"'), ":true"}, "")) {
		t.Fatalf("device authorization not persisted: %d %s", connected.Code, connected.Body.String())
	}
	// 同一个 OIDC 账号再次登录，已授权设备无需第二次 consent。
	nextStart := httptest.NewRecorder()
	handler.ServeHTTP(nextStart, httptest.NewRequest(http.MethodGet, "/auth/nexusdock/start", nil))
	nextURL, _ := url.Parse(nextStart.Header().Get("Location"))
	nonce = nextURL.Query().Get("nonce")
	state = nextURL.Query().Get("state")
	for _, cookie := range nextStart.Result().Cookies() {
		if cookie.Name == oidcStateCookieName {
			stateCookie = cookie
		}
	}
	second := httptest.NewRequest(http.MethodGet, "/auth/nexusdock/callback?state="+url.QueryEscape(state)+"&code=oidc-code", nil)
	second.AddCookie(stateCookie)
	secondCallback := httptest.NewRecorder()
	handler.ServeHTTP(secondCallback, second)
	if secondCallback.Code != http.StatusFound || secondCallback.Header().Get("Location") != "/" {
		t.Fatalf("second OIDC login must skip previously consented device flow: %d %s", secondCallback.Code, secondCallback.Header().Get("Location"))
	}
}
