package store

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func TestModelAPIKeyEncryptionBindsWorkspaceAndRoute(t *testing.T) {
	s := &Store{}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := s.ConfigureModelEncryption(key); err != nil {
		t.Fatal(err)
	}
	workspaceID, routeID := uuid.New(), uuid.New()
	const secret = "sk-ant-api03-model-credential-not-readable-from-postgres"
	encrypted, err := s.encryptModelAPIKey(workspaceID.String(), routeID.String(), secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(secret)) {
		t.Fatal("ciphertext contains plaintext")
	}
	route := db.WorkspaceModelRoute{
		WorkspaceID:      workspaceID,
		ID:               routeID,
		ApiKeyCiphertext: encrypted,
	}
	decoded, err := s.ModelRouteAPIKey(route)
	if err != nil || decoded != secret {
		t.Fatalf("decrypt failed: %v", err)
	}
	route.WorkspaceID = uuid.New()
	if _, err := s.ModelRouteAPIKey(route); err == nil {
		t.Fatal("cross-workspace decryption unexpectedly succeeded")
	}
	route.WorkspaceID = workspaceID
	route.ID = uuid.New()
	if _, err := s.ModelRouteAPIKey(route); err == nil {
		t.Fatal("cross-route decryption unexpectedly succeeded")
	}
	route.ID = routeID
	route.ApiKeyCiphertext[len(route.ApiKeyCiphertext)-1] ^= 1
	if _, err := s.ModelRouteAPIKey(route); err == nil {
		t.Fatal("tampered ciphertext unexpectedly decrypted")
	}
	if err := s.ConfigureModelEncryption(""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ModelRouteAPIKey(route); err == nil {
		t.Fatal("missing encryption key unexpectedly decrypted key")
	}
	if _, err := s.encryptModelAPIKey(workspaceID.String(), routeID.String(), secret); err == nil {
		t.Fatal("missing encryption key unexpectedly encrypted key")
	}
	if err := s.ConfigureModelEncryption("invalid"); err == nil {
		t.Fatal("invalid encryption key unexpectedly accepted")
	}
}

