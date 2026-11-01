package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/uvwt/CycleDock/internal/auth"
)

type identityContextKey struct{}

type loginRequest struct {
	Password string `json:"password"`
}

type loginMethodsResponse struct {
	Password  bool                    `json:"password"`
	Providers []loginProviderResponse `json:"providers"`
}

type loginProviderResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	StartURL string `json:"start_url"`
}

type workspaceResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	IsPersonal bool   `json:"is_personal"`
}

type identityResponse struct {
	HumanAccountID string `json:"human_account_id"`
	DisplayName    string `json:"display_name"`
	Email          string `json:"email,omitempty"`
	Role           string `json:"role"`
	Workspace      struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"workspace"`
	Source struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"source"`
}

func (api *API) loginMethods(w http.ResponseWriter, _ *http.Request) {
	methods := api.auth.LoginMethods()
	response := loginMethodsResponse{
		Password:  methods.Password,
		Providers: make([]loginProviderResponse, 0, len(methods.Providers)),
	}
	for _, provider := range methods.Providers {
		if provider == auth.ProviderNexusDock {
			response.Providers = append(response.Providers, loginProviderResponse{
				ID: "nexusdock", Name: "NexusDock", StartURL: "/auth/nexusdock/start",
			})
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (api *API) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	session, err := api.auth.Login(r.Context(), request.Password)
	if errors.Is(err, auth.ErrUnauthorized) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
		return
	}
	if err != nil {
		api.handleError(w, err)
		return
	}
	api.setSessionCookie(w, session)
	writeJSON(w, http.StatusOK, newIdentityResponse(session.Identity))
}

func (api *API) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if err := api.auth.Logout(r.Context(), cookie.Value); err != nil {
			api.handleError(w, err)
			return
		}
	}
	api.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, newIdentityResponse(identityFromRequest(r)))
}

func (api *API) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	memberships, err := api.auth.ListWorkspaces(r.Context(), identity.HumanAccountID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	response := make([]workspaceResponse, 0, len(memberships))
	for _, membership := range memberships {
		response = append(response, workspaceResponse{
			ID: membership.ID.String(), Name: membership.Name, Role: membership.Role, IsPersonal: membership.IsPersonal,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func (api *API) switchWorkspace(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDParam(w, r, "workspaceID")
	if !ok {
		return
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	identity, err := api.auth.SwitchWorkspace(r.Context(), cookie.Value, workspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newIdentityResponse(identity))
}

func (api *API) requireHuman(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		identity, err := api.auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			api.handleError(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityContextKey{}, identity)))
	})
}

func identityFromRequest(r *http.Request) auth.Identity {
	identity, _ := r.Context().Value(identityContextKey{}).(auth.Identity)
	return identity
}

func newIdentityResponse(identity auth.Identity) identityResponse {
	var response identityResponse
	response.HumanAccountID = identity.HumanAccountID.String()
	response.DisplayName = identity.DisplayName
	response.Email = identity.Email
	response.Role = identity.Role
	response.Workspace.ID = identity.WorkspaceID.String()
	response.Workspace.Name = identity.WorkspaceName
	response.Source.ID = identity.SourceID.String()
	response.Source.Name = identity.SourceName
	response.Source.Kind = "human"
	return response
}

const (
	sessionCookieName    = "cycledock_session"
	oidcStateCookieName  = "cycledock_auth_state_oidc"
	oidcStateCookiePath  = "/auth/nexusdock/callback"
	oidcReturnCookieName = "cycledock_auth_return"
)

func (api *API) setSessionCookie(w http.ResponseWriter, session auth.Session) {
	maxAge := max(1, int(time.Until(session.ExpiresAt).Seconds()))
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: session.Token, Path: "/",
		HttpOnly: true, Secure: api.secureCookie, SameSite: http.SameSiteLaxMode,
		Expires: session.ExpiresAt, MaxAge: maxAge,
	})
}

func (api *API) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Path: "/", HttpOnly: true,
		Secure: api.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

func (api *API) oidcLoginStart(w http.ResponseWriter, r *http.Request) {
	start, err := api.auth.BeginOIDCLogin(r.Context())
	if errors.Is(err, auth.ErrOIDCDisabled) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		api.logger.Error("OIDC login start failed", "error", err)
		http.Error(w, "OIDC login is temporarily unavailable", http.StatusInternalServerError)
		return
	}
	maxAge := max(1, int(time.Until(start.ExpiresAt).Seconds()))
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookieName, Value: start.State, Path: oidcStateCookiePath,
		HttpOnly: true, Secure: api.secureCookie, SameSite: http.SameSiteLaxMode,
		Expires: start.ExpiresAt, MaxAge: maxAge,
	})
	returnTo := safeInternalReturnTo(r.URL.Query().Get("return_to"))
	if returnTo != "/" {
		http.SetCookie(w, &http.Cookie{
			Name: oidcReturnCookieName, Value: returnTo, Path: oidcStateCookiePath,
			HttpOnly: true, Secure: api.secureCookie, SameSite: http.SameSiteLaxMode,
			Expires: start.ExpiresAt, MaxAge: maxAge,
		})
	}
	http.Redirect(w, r, start.URL, http.StatusFound)
}

func (api *API) oidcLoginCallback(w http.ResponseWriter, r *http.Request) {
	returnTo := api.oidcReturnTo(r)
	state := r.URL.Query().Get("state")
	if !api.oidcStateMatches(r, state) {
		api.redirectAuthError(w, r, "invalid_state", returnTo)
		return
	}
	api.clearOIDCStateCookie(w)
	api.clearOIDCReturnCookie(w)
	if r.URL.Query().Get("error") != "" {
		if err := api.auth.ConsumeOIDCState(r.Context(), state); err != nil {
			api.redirectAuthError(w, r, "invalid_state", returnTo)
			return
		}
		api.redirectAuthError(w, r, "access_denied", returnTo)
		return
	}
	session, err := api.auth.CompleteOIDCLogin(r.Context(), state, r.URL.Query().Get("code"))
	if err != nil {
		code := "provider_error"
		if errors.Is(err, auth.ErrInvalidOIDCState) {
			code = "invalid_state"
		}
		api.logger.Warn("OIDC login callback failed", "code", code, "error", err)
		api.redirectAuthError(w, r, code, returnTo)
		return
	}
	api.setSessionCookie(w, session)
	// OIDC 登录仅创建会话；设备 MCP OAuth 在用户主动连接时另行授权。
	http.Redirect(w, r, returnTo, http.StatusFound)
}

func (api *API) oidcStateMatches(r *http.Request, state string) bool {
	cookie, err := r.Cookie(oidcStateCookieName)
	if err != nil || cookie.Value == "" || state == "" {
		return false
	}
	expected := sha256.Sum256([]byte(cookie.Value))
	actual := sha256.Sum256([]byte(state))
	return subtle.ConstantTimeCompare(expected[:], actual[:]) == 1
}

func (api *API) clearOIDCStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookieName, Path: oidcStateCookiePath, HttpOnly: true,
		Secure: api.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

func (api *API) oidcReturnTo(r *http.Request) string {
	cookie, err := r.Cookie(oidcReturnCookieName)
	if err != nil {
		return "/"
	}
	return safeInternalReturnTo(cookie.Value)
}

func (api *API) clearOIDCReturnCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: oidcReturnCookieName, Path: oidcStateCookiePath, HttpOnly: true,
		Secure: api.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

func (api *API) redirectAuthError(w http.ResponseWriter, r *http.Request, code, returnTo string) {
	values := url.Values{"auth_error": {code}, "provider": {"nexusdock"}}
	if returnTo = safeInternalReturnTo(returnTo); returnTo != "/" {
		values.Set("return_to", returnTo)
	}
	http.Redirect(w, r, "/?"+values.Encode(), http.StatusFound)
}
