package store

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func TestMCPTokenEncryptionBindsWorkspaceAndConnection(t *testing.T) {
	s := &Store{}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := s.ConfigureMCPEncryption(key); err != nil {
		t.Fatal(err)
	}
	workspaceID, connectionID := uuid.New(), uuid.New()
	const secret = "credential-not-readable-from-postgres"
	encrypted, err := s.encryptMCPBearerToken(workspaceID.String(), connectionID.String(), secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(secret)) {
		t.Fatal("ciphertext contains plaintext")
	}
	connection := db.WorkspaceMcpConnection{WorkspaceID: workspaceID, ID: connectionID, BearerTokenCiphertext: encrypted}
	decoded, err := s.MCPBearerToken(connection)
	if err != nil || decoded != secret {
		t.Fatalf("decrypt failed: %v", err)
	}
	connection.WorkspaceID = uuid.New()
	if _, err := s.MCPBearerToken(connection); err == nil {
		t.Fatal("cross-workspace decryption unexpectedly succeeded")
	}
	connection.WorkspaceID = workspaceID
	connection.ID = uuid.New()
	if _, err := s.MCPBearerToken(connection); err == nil {
		t.Fatal("cross-connection decryption unexpectedly succeeded")
	}
	connection.ID = connectionID
	connection.BearerTokenCiphertext[len(connection.BearerTokenCiphertext)-1] ^= 1
	if _, err := s.MCPBearerToken(connection); err == nil {
		t.Fatal("tampered ciphertext unexpectedly decrypted")
	}
	if err := s.ConfigureMCPEncryption(""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MCPBearerToken(connection); err == nil {
		t.Fatal("missing encryption key unexpectedly decrypted token")
	}
	if _, err := s.encryptMCPBearerToken(workspaceID.String(), connectionID.String(), secret); err == nil {
		t.Fatal("missing encryption key unexpectedly encrypted token")
	}
	if err := s.ConfigureMCPEncryption("invalid"); err == nil {
		t.Fatal("invalid encryption key unexpectedly accepted")
	}
}
