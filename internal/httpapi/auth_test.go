package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOIDCStateCookieSecurityAndBinding(t *testing.T) {
	api := &API{secureCookie: true}
	expiresAt := time.Now().Add(10 * time.Minute)

	response := httptest.NewRecorder()
	maxAge := max(1, int(time.Until(expiresAt).Seconds()))
	http.SetCookie(response, &http.Cookie{
		Name: oidcStateCookieName, Value: "state-value", Path: oidcStateCookiePath,
		HttpOnly: true, Secure: api.secureCookie, SameSite: http.SameSiteLaxMode,
		Expires: expiresAt, MaxAge: maxAge,
	})

	var stateCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == oidcStateCookieName {
			stateCookie = cookie
			break
		}
	}
	if stateCookie == nil {
		t.Fatal("OIDC state cookie was not set")
	}
	if !stateCookie.HttpOnly || !stateCookie.Secure || stateCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected OIDC state cookie flags: %+v", stateCookie)
	}
	if stateCookie.Path != oidcStateCookiePath {
		t.Fatalf("unexpected OIDC state cookie path: %q", stateCookie.Path)
	}

	request := httptest.NewRequest(http.MethodGet, oidcStateCookiePath+"?state=state-value", nil)
	request.AddCookie(stateCookie)
	if !api.oidcStateMatches(request, "state-value") {
		t.Fatal("matching OIDC browser state cookie was rejected")
	}
	if api.oidcStateMatches(request, "other-state") {
		t.Fatal("mismatched OIDC browser state cookie was accepted")
	}
}
