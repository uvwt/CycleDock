package httpapi

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/oauth"
)

type oauthClientRegistrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
}

type oauthClientRegistrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type oauthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type oauthAuthorizationServerMetadataResponse struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []string `json:"scopes_supported"`
}

var oauthConsentTemplate = template.Must(template.New("oauth-consent").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Authorize CycleDock MCP</title>
<style>
body{font-family:system-ui,sans-serif;background:#020617;color:#e2e8f0;margin:0;min-height:100vh;display:grid;place-items:center}
main{width:min(92vw,520px);border:1px solid #1e293b;border-radius:20px;padding:28px;background:#0f172a}
h1{font-size:22px;margin:0 0 10px}p{color:#94a3b8;line-height:1.6}strong{color:#e2e8f0}
.actions{display:flex;gap:10px;margin-top:22px}button{border:0;border-radius:10px;padding:10px 16px;font-weight:600;cursor:pointer}
.approve{background:#22d3ee;color:#082f49}.deny{background:#1e293b;color:#cbd5e1}
</style>
</head>
<body><main>
<h1>Authorize CycleDock MCP</h1>
<p><strong>{{.ClientName}}</strong> wants to work in <strong>{{.WorkspaceName}}</strong>.</p>
<p>It will receive its own OAuth Client Source. It can read and update CycleDock work through the MCP tools, but it cannot complete Review → Done as a human.</p>
<form method="post" action="/oauth/authorize">
<input type="hidden" name="response_type" value="code">
<input type="hidden" name="client_id" value="{{.ClientID}}">
<input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
<input type="hidden" name="scope" value="{{.Scope}}">
<input type="hidden" name="state" value="{{.State}}">
<input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
<input type="hidden" name="code_challenge_method" value="S256">
<input type="hidden" name="resource" value="{{.Resource}}">
<div class="actions"><button class="approve" name="decision" value="approve">Authorize</button><button class="deny" name="decision" value="deny">Deny</button></div>
</form>
</main></body></html>`))

func (api *API) oauthAuthorizationServerMetadata(w http.ResponseWriter, _ *http.Request) {
	if api.oauth == nil {
		http.NotFound(w, nil)
		return
	}
	issuer := api.oauth.Issuer()
	writeJSON(w, http.StatusOK, oauthAuthorizationServerMetadataResponse{
		Issuer:                            issuer,
		AuthorizationEndpoint:             issuer + "/oauth/authorize",
		TokenEndpoint:                     issuer + "/oauth/token",
		RegistrationEndpoint:              issuer + "/oauth/register",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
		ScopesSupported:                   []string{oauth.ScopeMCP},
	})
}

func (api *API) oauthRegisterClient(w http.ResponseWriter, r *http.Request) {
	if api.oauth == nil {
		http.NotFound(w, r)
		return
	}
	if !requireOAuthMediaType(w, r, "application/json") {
		return
	}
	var request oauthClientRegistrationRequest
	if !decodeOAuthJSON(w, r, &request) {
		return
	}
	if request.TokenEndpointAuthMethod != "" && request.TokenEndpointAuthMethod != "none" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "only public clients with token_endpoint_auth_method=none are supported")
		return
	}
	if len(request.GrantTypes) > 0 && !sameStringSet(request.GrantTypes, []string{"authorization_code", "refresh_token"}) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "grant_types must be authorization_code and refresh_token")
		return
	}
	if len(request.ResponseTypes) > 0 && !sameStringSet(request.ResponseTypes, []string{"code"}) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "response_types must contain only code")
		return
	}
	client, err := api.oauth.RegisterClient(r.Context(), request.ClientName, request.RedirectURIs)
	if err != nil {
		api.handleOAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, oauthClientRegistrationResponse{
		ClientID: client.ID, ClientName: client.Name, RedirectURIs: client.RedirectURIs,
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientIDIssuedAt:        client.CreatedAt.Unix(),
	})
}

func (api *API) oauthAuthorizeForm(w http.ResponseWriter, r *http.Request) {
	if api.oauth == nil {
		http.NotFound(w, r)
		return
	}
	request, state, err := api.parseOAuthAuthorizationRequest(r.URL.Query())
	if err != nil {
		writeOAuthAuthorizeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	client, err := api.oauth.ValidateAuthorizationRequest(r.Context(), request)
	if err != nil {
		api.handleOAuthAuthorizeValidationError(w, err)
		return
	}
	identity, err := api.webIdentityFromCookie(r)
	if err != nil {
		returnTo := r.URL.RequestURI()
		http.Redirect(w, r, "/?return_to="+url.QueryEscape(returnTo), http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = oauthConsentTemplate.Execute(w, struct {
		ClientID      string
		ClientName    string
		RedirectURI   string
		Scope         string
		State         string
		CodeChallenge string
		Resource      string
		WorkspaceName string
	}{
		ClientID: client.ID, ClientName: client.Name, RedirectURI: request.RedirectURI,
		Scope: request.Scope, State: state, CodeChallenge: request.CodeChallenge,
		Resource: request.Resource, WorkspaceName: identity.WorkspaceName,
	})
}

func (api *API) oauthAuthorizeSubmit(w http.ResponseWriter, r *http.Request) {
	if api.oauth == nil {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeOAuthAuthorizeError(w, http.StatusForbidden, "invalid_request", "cross-site authorization submission is not allowed")
		return
	}
	if !requireOAuthMediaType(w, r, "application/x-www-form-urlencoded") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		writeOAuthAuthorizeError(w, http.StatusBadRequest, "invalid_request", "invalid authorization form")
		return
	}
	request, state, err := api.parseOAuthAuthorizationRequest(r.PostForm)
	if err != nil {
		writeOAuthAuthorizeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if _, err := api.oauth.ValidateAuthorizationRequest(r.Context(), request); err != nil {
		api.handleOAuthAuthorizeValidationError(w, err)
		return
	}
	identity, err := api.webIdentityFromCookie(r)
	if err != nil {
		writeOAuthAuthorizeError(w, http.StatusUnauthorized, "login_required", "sign in to CycleDock before authorizing this client")
		return
	}
	if r.PostForm.Get("decision") != "approve" {
		api.redirectOAuthAuthorizationResult(w, r, request.RedirectURI, state, "", "access_denied")
		return
	}
	code, err := api.oauth.Authorize(r.Context(), identity, request)
	if err != nil {
		api.handleOAuthAuthorizeValidationError(w, err)
		return
	}
	api.redirectOAuthAuthorizationResult(w, r, code.RedirectURI, state, code.Code, "")
}

func (api *API) oauthToken(w http.ResponseWriter, r *http.Request) {
	if api.oauth == nil {
		http.NotFound(w, r)
		return
	}
	if !requireOAuthMediaType(w, r, "application/x-www-form-urlencoded") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid token request")
		return
	}

	var tokens oauth.TokenSet
	var err error
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		tokens, err = api.oauth.ExchangeCode(r.Context(), oauth.CodeTokenRequest{
			Code: r.PostForm.Get("code"), ClientID: r.PostForm.Get("client_id"),
			RedirectURI: r.PostForm.Get("redirect_uri"), CodeVerifier: r.PostForm.Get("code_verifier"),
			Resource: r.PostForm.Get("resource"),
		})
	case "refresh_token":
		tokens, err = api.oauth.Refresh(r.Context(), oauth.RefreshTokenRequest{
			RefreshToken: r.PostForm.Get("refresh_token"), ClientID: r.PostForm.Get("client_id"),
			Resource: r.PostForm.Get("resource"),
		})
	default:
		err = oauth.ErrUnsupportedGrant
	}
	if err != nil {
		api.handleOAuthError(w, err)
		return
	}
	expiresIn := int64(time.Until(tokens.ExpiresAt).Seconds())
	if expiresIn < 0 {
		expiresIn = 0
	}
	writeJSON(w, http.StatusOK, oauthTokenResponse{
		AccessToken: tokens.AccessToken, TokenType: tokens.TokenType,
		ExpiresIn: expiresIn, RefreshToken: tokens.RefreshToken, Scope: tokens.Scope,
	})
}

func (api *API) parseOAuthAuthorizationRequest(values url.Values) (oauth.AuthorizeRequest, string, error) {
	if values.Get("response_type") != "code" {
		return oauth.AuthorizeRequest{}, "", errors.New("response_type must be code")
	}
	if values.Get("code_challenge_method") != "S256" {
		return oauth.AuthorizeRequest{}, "", errors.New("code_challenge_method must be S256")
	}
	scope := values.Get("scope")
	if scope == "" {
		scope = oauth.ScopeMCP
	}
	request := oauth.AuthorizeRequest{
		ClientID: values.Get("client_id"), RedirectURI: values.Get("redirect_uri"),
		CodeChallenge: values.Get("code_challenge"), Scope: scope, Resource: values.Get("resource"),
	}
	if request.ClientID == "" || request.RedirectURI == "" || request.CodeChallenge == "" || request.Resource == "" {
		return oauth.AuthorizeRequest{}, "", errors.New("client_id, redirect_uri, code_challenge and resource are required")
	}
	return request, values.Get("state"), nil
}

func (api *API) redirectOAuthAuthorizationResult(w http.ResponseWriter, r *http.Request, redirectURI, state, code, oauthError string) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		writeOAuthAuthorizeError(w, http.StatusBadRequest, "invalid_request", "invalid redirect_uri")
		return
	}
	query := target.Query()
	if code != "" {
		query.Set("code", code)
	}
	if oauthError != "" {
		query.Set("error", oauthError)
	}
	if state != "" {
		query.Set("state", state)
	}
	query.Set("iss", api.oauth.Issuer())
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (api *API) webIdentityFromCookie(r *http.Request) (auth.Identity, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return auth.Identity{}, auth.ErrUnauthorized
	}
	return api.auth.Authenticate(r.Context(), cookie.Value)
}

func (api *API) handleOAuthAuthorizeValidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, oauth.ErrInvalidClient):
		writeOAuthAuthorizeError(w, http.StatusBadRequest, "invalid_request", "unknown client")
	case errors.Is(err, oauth.ErrInvalidScope):
		writeOAuthAuthorizeError(w, http.StatusBadRequest, "invalid_scope", "unsupported scope")
	case errors.Is(err, oauth.ErrInvalidResource):
		writeOAuthAuthorizeError(w, http.StatusBadRequest, "invalid_target", "unsupported resource")
	case errors.Is(err, oauth.ErrAuthorizationDenied):
		writeOAuthAuthorizeError(w, http.StatusForbidden, "access_denied", "authorization denied")
	case errors.Is(err, oauth.ErrInvalidRequest):
		writeOAuthAuthorizeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		api.logger.Error("OAuth authorization error", "error", err)
		writeOAuthAuthorizeError(w, http.StatusInternalServerError, "server_error", "OAuth server error")
	}
}

func (api *API) handleOAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, oauth.ErrInvalidClient):
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client")
	case errors.Is(err, oauth.ErrInvalidGrant):
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid, expired, or replayed grant")
	case errors.Is(err, oauth.ErrInvalidScope):
		writeOAuthError(w, http.StatusBadRequest, "invalid_scope", "unsupported scope")
	case errors.Is(err, oauth.ErrInvalidResource):
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "unsupported resource")
	case errors.Is(err, oauth.ErrUnsupportedGrant):
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant_type")
	case errors.Is(err, oauth.ErrInvalidRequest):
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		api.logger.Error("OAuth endpoint error", "error", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "OAuth server error")
	}
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, oauthErrorResponse{Error: code, ErrorDescription: description})
}

func writeOAuthAuthorizeError(w http.ResponseWriter, status int, code, description string) {
	writeOAuthError(w, status, code, description)
}

func requireOAuthMediaType(w http.ResponseWriter, r *http.Request, expected string) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != expected {
		writeOAuthError(w, http.StatusUnsupportedMediaType, "invalid_request", "Content-Type must be "+expected)
		return false
	}
	return true
}

func decodeOAuthJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "request must contain one JSON object")
		return false
	}
	return true
}

func sameStringSet(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	counts := make(map[string]int, len(actual))
	for _, value := range actual {
		counts[value]++
	}
	for _, value := range expected {
		if counts[value] == 0 {
			return false
		}
		counts[value]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func safeInternalReturnTo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, "\\") {
		return "/"
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "/"
	}
	return raw
}
