package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestOIDCSubjectForHumanIsBoundToIssuerAndHuman(t *testing.T) {
	service, _ := newAuthIntegrationService(t, Config{OIDC: OIDCConfig{
		Issuer: "https://nexusdock.example", ClientID: "cycledock-client", PublicURL: "https://cycledock.example",
	}})
	ctx := context.Background()
	alice, err := service.ensureOIDCPersonalIdentity(ctx, "https://nexusdock.example", "nexus-a", "Alice", "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := service.ensureOIDCPersonalIdentity(ctx, "https://nexusdock.example", "nexus-b", "Bob", "")
	if err != nil {
		t.Fatal(err)
	}
	subject, err := service.SubjectForHuman(ctx, alice.HumanAccountID)
	if err != nil || subject != "nexus-a" {
		t.Fatalf("wrong issuer-scoped subject: %q %v", subject, err)
	}
	other, err := service.SubjectForHuman(ctx, bob.HumanAccountID)
	if err != nil || other != "nexus-b" {
		t.Fatalf("other user's subject: %q %v", other, err)
	}
	if _, err = service.SubjectForHuman(ctx, uuid.New()); err == nil {
		t.Fatal("unknown human must not receive a NexusDock subject")
	}
}
