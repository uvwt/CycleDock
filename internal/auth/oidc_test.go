package auth

import "testing"

func TestOIDCConfigDisablesPasswordLoginAndAdvertisesNexusDock(t *testing.T) {
	service := &Service{adminPassword: []byte("local-admin-password")}
	err := service.configureOIDC(OIDCConfig{
		Issuer:    "https://nexusdock.example",
		ClientID:  "cycledock",
		PublicURL: "https://cycle.example/",
	})
	if err != nil {
		t.Fatalf("configure OIDC: %v", err)
	}
	if service.SelfHostedLoginEnabled() {
		t.Fatal("hosted OIDC mode must disable local administrator password login")
	}
	methods := service.LoginMethods()
	if methods.Password || len(methods.Providers) != 1 || methods.Providers[0] != ProviderNexusDock {
		t.Fatalf("unexpected login methods: %+v", methods)
	}
}

func TestOIDCConfigKeepsSelfHostedPasswordWithoutOIDC(t *testing.T) {
	service := &Service{adminPassword: []byte("local-admin-password")}
	if err := service.configureOIDC(OIDCConfig{}); err != nil {
		t.Fatal(err)
	}
	methods := service.LoginMethods()
	if !methods.Password || len(methods.Providers) != 0 {
		t.Fatalf("unexpected self-hosted methods: %+v", methods)
	}
}

func TestOIDCConfigRejectsInsecureRemoteIssuer(t *testing.T) {
	service := &Service{}
	if err := service.configureOIDC(OIDCConfig{
		Issuer:    "http://nexusdock.example",
		ClientID:  "cycledock",
		PublicURL: "https://cycle.example",
	}); err == nil {
		t.Fatal("remote HTTP issuer must be rejected")
	}
}
