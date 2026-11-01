package nexusdock

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestTokenEncryptionIsBoundToWorkspaceAndPurpose(t *testing.T) {
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{aead: aead}
	workspaceA := uuid.New()
	workspaceB := uuid.New()

	ciphertext, err := service.encryptToken(workspaceA, "access", "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.decryptToken(workspaceA, "access", ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret-token" {
		t.Fatalf("decrypted token=%q", got)
	}
	if _, err := service.decryptToken(workspaceB, "access", ciphertext); !errors.Is(err, ErrConnectionExpired) {
		t.Fatalf("cross-workspace decrypt error=%v", err)
	}
	if _, err := service.decryptToken(workspaceA, "refresh", ciphertext); !errors.Is(err, ErrConnectionExpired) {
		t.Fatalf("cross-purpose decrypt error=%v", err)
	}
}

func TestParseEncryptionKeyRequiresExactly32Bytes(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := parseEncryptionKey(valid); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if _, err := parseEncryptionKey(base64.StdEncoding.EncodeToString(make([]byte, 31))); err == nil {
		t.Fatal("31-byte key accepted")
	}
}

func TestDiscoverAcceptsNexusDockMetadataAndRejectsCrossOriginEndpoint(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			writeTestJSON(w, map[string]any{
				"resource":              server.URL + "/mcp",
				"authorization_servers": []string{server.URL},
			})
		case "/.well-known/oauth-authorization-server":
			writeTestJSON(w, map[string]any{
				"issuer":                                server.URL,
				"authorization_endpoint":                server.URL + "/oauth/authorize",
				"token_endpoint":                        server.URL + "/oauth/token",
				"registration_endpoint":                 server.URL + "/oauth/register",
				"revocation_endpoint":                   server.URL + "/oauth/revoke",
				"code_challenge_methods_supported":      []string{"S256"},
				"token_endpoint_auth_methods_supported": []string{"none"},
				"scopes_supported":                      []string{"mcp"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := &Service{mcpURL: server.URL + "/mcp", client: server.Client()}
	metadata, err := service.discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Resource != server.URL+"/mcp" {
		t.Fatalf("resource=%q", metadata.Resource)
	}

	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			writeTestJSON(w, map[string]any{
				"resource":              "http://" + r.Host + "/mcp",
				"authorization_servers": []string{"http://" + r.Host},
			})
		case "/.well-known/oauth-authorization-server":
			writeTestJSON(w, map[string]any{
				"issuer":                                "http://" + r.Host,
				"authorization_endpoint":                "https://example.invalid/oauth/authorize",
				"token_endpoint":                        "http://" + r.Host + "/oauth/token",
				"registration_endpoint":                 "http://" + r.Host + "/oauth/register",
				"revocation_endpoint":                   "http://" + r.Host + "/oauth/revoke",
				"code_challenge_methods_supported":      []string{"S256"},
				"token_endpoint_auth_methods_supported": []string{"none"},
				"scopes_supported":                      []string{"mcp"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer badServer.Close()

	service = &Service{mcpURL: badServer.URL + "/mcp", client: badServer.Client()}
	if _, err := service.discover(t.Context()); err == nil || !strings.Contains(err.Error(), "unsafe OAuth endpoint") {
		t.Fatalf("cross-origin authorization endpoint error=%v", err)
	}
}

func TestExchangeTokenDistinguishesAuthorizationFailureFromUpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("mode") {
		case "auth":
			http.Error(w, "invalid grant", http.StatusBadRequest)
		default:
			http.Error(w, "temporary", http.StatusBadGateway)
		}
	}))
	defer server.Close()
	service := &Service{client: server.Client()}

	if _, err := service.exchangeToken(t.Context(), server.URL+"?mode=auth", nil); !errors.Is(err, ErrAuthorization) {
		t.Fatalf("authorization error=%v", err)
	}
	if _, err := service.exchangeToken(t.Context(), server.URL+"?mode=upstream", nil); err == nil || errors.Is(err, ErrAuthorization) {
		t.Fatalf("upstream error=%v", err)
	}
}

func TestExchangeRefreshMatchesNexusDockTokenContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type=%q", got)
		}
		if got := r.Form.Get("client_id"); got != "client-1" {
			t.Fatalf("client_id=%q", got)
		}
		if got := r.Form.Get("refresh_token"); got != "refresh-1" {
			t.Fatalf("refresh_token=%q", got)
		}
		if got := r.Form.Get("resource"); got != "https://mcp.example/mcp" {
			t.Fatalf("resource=%q", got)
		}
		if _, exists := r.Form["scope"]; exists {
			t.Fatal("refresh request must not send scope to NexusDock")
		}
		writeTestJSON(w, tokenResponse{
			AccessToken: "access-2", TokenType: "Bearer", ExpiresIn: 3600, RefreshToken: "refresh-2",
		})
	}))
	defer server.Close()

	service := &Service{client: server.Client()}
	tokens, err := service.exchangeRefresh(t.Context(), oauthMetadata{
		Resource: "https://mcp.example/mcp",
		Server:   authorizationServerMetadata{TokenEndpoint: server.URL},
	}, "client-1", "refresh-1")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "access-2" || tokens.RefreshToken != "refresh-2" {
		t.Fatalf("tokens=%+v", tokens)
	}
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(err)
	}
}
