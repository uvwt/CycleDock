package store

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func TestModelAPIKeyEncryptionBindsWorkspaceAndCredential(t *testing.T) {
	s := &Store{}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := s.ConfigureModelEncryption(key); err != nil {
		t.Fatal(err)
	}
	workspaceID, credentialID := uuid.New(), uuid.New()
	const secret = "sk-ant-api03-model-credential-not-readable-from-postgres"
	encrypted, err := s.encryptAPICredentialKey(workspaceID.String(), credentialID.String(), secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(secret)) {
		t.Fatal("ciphertext contains plaintext")
	}

	// 多个不同 Route ID 只要引用同一个 ApiCredentialID，均能解密共享凭据
	for _, routeID := range []uuid.UUID{uuid.New(), uuid.New()} {
		route := ModelRoute{
			WorkspaceID:      workspaceID,
			ID:               routeID,
			ApiCredentialID:  uuid.NullUUID{UUID: credentialID, Valid: true},
			ApiKeyCiphertext: append([]byte(nil), encrypted...),
		}
		decoded, err := s.ModelRouteAPIKey(route)
		if err != nil || decoded != secret {
			t.Fatalf("route %s decrypt failed: %v", routeID, err)
		}
	}

	// 兼容 000032 迁移自旧 workspace_model_routes 的密文（cred.ID == 原 route.ID）
	legacyEncrypted, err := s.encryptLegacyRouteModelAPIKey(workspaceID.String(), credentialID.String(), secret)
	if err != nil {
		t.Fatal(err)
	}
	migratedCred := db.WorkspaceApiCredential{
		ID:               credentialID,
		WorkspaceID:      workspaceID,
		ApiKeyCiphertext: legacyEncrypted,
	}
	if decoded, err := s.APICredentialKey(migratedCred); err != nil || decoded != secret {
		t.Fatalf("legacy migrated credential decrypt failed: %v", err)
	}

	cred := db.WorkspaceApiCredential{
		WorkspaceID:      workspaceID,
		ID:               credentialID,
		ApiKeyCiphertext: append([]byte(nil), encrypted...),
	}
	cred.WorkspaceID = uuid.New()
	if _, err := s.APICredentialKey(cred); err == nil {
		t.Fatal("cross-workspace decryption unexpectedly succeeded")
	}
	cred.WorkspaceID = workspaceID
	cred.ID = uuid.New()
	if _, err := s.APICredentialKey(cred); err == nil {
		t.Fatal("cross-credential decryption unexpectedly succeeded")
	}
	cred.ID = credentialID
	cred.ApiKeyCiphertext[len(cred.ApiKeyCiphertext)-1] ^= 1
	if _, err := s.APICredentialKey(cred); err == nil {
		t.Fatal("tampered ciphertext unexpectedly decrypted")
	}
	if err := s.ConfigureModelEncryption(""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.APICredentialKey(cred); err == nil {
		t.Fatal("missing encryption key unexpectedly decrypted key")
	}
	if _, err := s.encryptAPICredentialKey(workspaceID.String(), credentialID.String(), secret); err == nil {
		t.Fatal("missing encryption key unexpectedly encrypted key")
	}
	if err := s.ConfigureModelEncryption("invalid"); err == nil {
		t.Fatal("invalid encryption key unexpectedly accepted")
	}
}
