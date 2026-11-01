package modelauth

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
)

func TestVaultRoundTripBindsCiphertextToAAD(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	vault, _, err := newVault(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("newVault: %v", err)
	}

	workspaceID := uuid.New()
	payload := credentialPayload{
		AccessToken:  "access-secret",
		RefreshToken: "refresh-secret",
		ClientID:     "client",
		TokenURL:     "https://example.com/token",
	}
	aad := credentialAAD(workspaceID, ProviderChatGPT)
	ciphertext, err := vault.seal(payload, aad)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(ciphertext, []byte(payload.AccessToken)) || bytes.Contains(ciphertext, []byte(payload.RefreshToken)) {
		t.Fatal("ciphertext contains plaintext credential material")
	}

	var decoded credentialPayload
	if err := vault.open(ciphertext, aad, &decoded); err != nil {
		t.Fatalf("open: %v", err)
	}
	if decoded != payload {
		t.Fatalf("decoded payload mismatch: %#v", decoded)
	}

	if err := vault.open(ciphertext, credentialAAD(uuid.New(), ProviderChatGPT), &credentialPayload{}); err == nil {
		t.Fatal("ciphertext decrypted under a different workspace AAD")
	}
	if err := vault.open(ciphertext, credentialAAD(workspaceID, ProviderGrok), &credentialPayload{}); err == nil {
		t.Fatal("ciphertext decrypted under a different provider AAD")
	}
}

func TestDecodeKeyRequiresExactly32Bytes(t *testing.T) {
	for _, raw := range [][]byte{
		bytes.Repeat([]byte{1}, 16),
		bytes.Repeat([]byte{1}, 31),
		bytes.Repeat([]byte{1}, 33),
	} {
		if _, err := decodeKey(base64.StdEncoding.EncodeToString(raw)); err == nil {
			t.Fatalf("decodeKey accepted %d-byte key", len(raw))
		}
	}
	if _, err := decodeKey("not-base64"); err == nil {
		t.Fatal("decodeKey accepted invalid base64")
	}
}

func TestTrustedXAIEndpoint(t *testing.T) {
	for _, raw := range []string{
		"https://auth.x.ai/oauth2/token",
		"https://accounts.x.ai/oauth/token",
		"https://x.ai/oauth/token",
	} {
		if _, err := trustedXAIEndpoint(raw); err != nil {
			t.Fatalf("trusted endpoint %q rejected: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://auth.x.ai/oauth2/token",
		"https://x.ai.example.com/oauth2/token",
		"https://example.com/oauth2/token",
		"file:///tmp/token",
	} {
		if _, err := trustedXAIEndpoint(raw); err == nil {
			t.Fatalf("untrusted endpoint %q accepted", raw)
		}
	}
}

func TestScopeContainsExactScope(t *testing.T) {
	scope := "openid profile chatgpt.tokens.use.direct offline_access"
	if !scopeContains(scope, "chatgpt.tokens.use.direct") {
		t.Fatal("expected exact scope to be found")
	}
	if scopeContains(scope, "chatgpt.tokens.use") {
		t.Fatal("scope prefix must not be accepted")
	}
}

func TestGrokProxyHeadersMatchAuthenticatedProxyContract(t *testing.T) {
	headers := grokProxyHeaders("grok-test", "user-123", "user@example.com")
	want := map[string]string{
		"X-XAI-Token-Auth":         "xai-grok-cli",
		"x-authenticateresponse":   "authenticate-response",
		"x-grok-client-version":    grokProxyClientVersion,
		"x-grok-client-identifier": "cycledock",
		"x-grok-client-mode":       "headless",
		"x-grok-model-override":    "grok-test",
		"x-userid":                 "user-123",
		"x-email":                  "user@example.com",
	}
	for name, value := range want {
		if headers[name] != value {
			t.Fatalf("%s = %q, want %q", name, headers[name], value)
		}
	}
}

func TestXAITokenIdentityUsesPersonalAndTeamClaims(t *testing.T) {
	encode := func(payload string) string {
		return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".sig"
	}
	userID, email := xaiTokenIdentity(tokenResponse{
		IDToken: encode(`{"sub":"user-1","email":"user@example.com"}`),
	})
	if userID != "user-1" || email != "user@example.com" {
		t.Fatalf("personal identity = %q %q", userID, email)
	}

	userID, email = xaiTokenIdentity(tokenResponse{
		AccessToken: encode(`{"principal_type":"Team","principal_id":"team-7"}`),
		IDToken:     encode(`{"sub":"user-1","email":"user@example.com"}`),
	})
	if userID != "team-7" || email != "" {
		t.Fatalf("team identity = %q %q", userID, email)
	}
}
