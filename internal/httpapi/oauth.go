package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/oauth"
)

const (
	oauthConsentPagePath = "/oauth/consent"
	// 仅用于兼容旧 POST /oauth/authorize 表单提交：Chromium 会把 form-action 应用于 302 跨源回调链，
	// 因此该端点移除 form-action 限制，全局其余路由仍保留 form-action 'self'。
	oauthAuthorizeSubmitCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'"
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

type authorizedAIClientResponse struct {
	ClientID     string    `json:"client_id"`
	Name         string    `json:"name"`
	SourceID     string    `json:"source_id"`
	AuthorizedAt time.Time `json:"authorized_at"`
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
	ResourceIndicatorsSupported       bool     `json:"resource_indicators_supported"`
}

type oauthConsentResponse struct {
	ClientID      string `json:"client_id"`
	ClientName    string `json:"client_name"`
	RedirectURI   string `json:"redirect_uri"`
	Scope         string `json:"scope"`
	Resource      string `json:"resource"`
	WorkspaceName string `json:"workspace_name"`
}

type oauthConsentSubmitRequest struct {
	ResponseType        string `json:"response_type"`
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	Scope               string `json:"scope"`
	State               string `json:"state"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource"`
	Decision            string `json:"decision"`
}

type oauthConsentSubmitResponse struct {
	RedirectTo string `json:"redirect_to"`
}

func (api *API) listAuthorizedAIClients(w http.ResponseWriter, r *http.Request) {
	if api.oauth == nil {
		writeJSON(w, http.StatusOK, []authorizedAIClientResponse{})
		return
	}
	identity := identityFromRequest(r)
	clients, err := api.oauth.ListAuthorizedClients(
		r.Context(), identity.WorkspaceID, identity.HumanAccountID,
	)
	if err != nil {
		api.handleError(w, err)
		return
	}
	response := make([]authorizedAIClientResponse, 0, len(clients))
	for _, client := range clients {
		response = append(response, authorizedAIClientResponse{
			ClientID: client.ClientID, Name: client.Name,
			SourceID: client.SourceID.String(), AuthorizedAt: client.AuthorizedAt,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

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
		ResourceIndicatorsSupported:       true,
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
	grantTypes, ok := normalizeOAuthGrantTypes(request.GrantTypes)
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "grant_types must include authorization_code and only use authorization_code or refresh_token")
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
		GrantTypes:              grantTypes,
		ResponseTypes:           []string{"code"},
		ClientIDIssuedAt:        client.CreatedAt.Unix(),
	})
}

// oauthAuthorizeForm 是公开的 OAuth Authorization Endpoint（GET /oauth/authorize）。
// 校验通过后 302 到前端 SPA 同意页 /oauth/consent（未登录时携带 return_to 先去登录），
// 真正的同意提交在同源 JSON 接口 POST /api/v1/oauth/consent 完成，彻底规避浏览器 form-action 重定向链拦截。
func (api *API) oauthAuthorizeForm(w http.ResponseWriter, r *http.Request) {
	if api.oauth == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	query := r.URL.Query()
	clientID := strings.TrimSpace(query.Get("client_id"))
	redirectURI := strings.TrimSpace(query.Get("redirect_uri"))
	state := query.Get("state")

	// 第一步先确认 client_id 与 redirect_uri 已注册可信；不可信时绝不开放重定向，只落在站内错误页。
	if clientID == "" || redirectURI == "" {
		http.Redirect(w, r, oauthConsentPagePath+"?error=invalid_request", http.StatusFound)
		return
	}
	if _, err := api.oauth.ValidateClientRedirect(r.Context(), clientID, redirectURI); err != nil {
		if errors.Is(err, oauth.ErrInvalidClient) || errors.Is(err, oauth.ErrUntrustedRedirect) {
			http.Redirect(w, r, oauthConsentPagePath+"?error=invalid_request", http.StatusFound)
			return
		}
		api.logger.Error("validate oauth client redirect failed", "error", err)
		http.Redirect(w, r, oauthConsentPagePath+"?error=server_error", http.StatusFound)
		return
	}

	// client_id 与 redirect_uri 已可信，后续参数错误按 RFC 6749 §4.1.2.1 重定向回客户端回调地址。
	request, parsedState, errCode, err := api.parseOAuthAuthorizationRequest(query)
	if err != nil {
		api.redirectOAuthAuthorizationResult(w, r, redirectURI, parsedState, "", errCode)
		return
	}
	_, normalized, err := api.oauth.NormalizeAuthorizationRequest(r.Context(), request)
	if err != nil {
		api.redirectOAuthAuthorizationResult(w, r, redirectURI, state, "", oauthAuthorizeErrorCode(err))
		return
	}

	consentURL := oauthConsentPagePath + "?" + oauthConsentQueryValues(normalized, state).Encode()
	if _, err := api.webIdentityFromCookie(r); err != nil {
		http.Redirect(w, r, "/?return_to="+url.QueryEscape(consentURL), http.StatusFound)
		return
	}
	http.Redirect(w, r, consentURL, http.StatusFound)
}

// oauthConsentGet 返回 SPA 授权同意页展示所需的客户端与工作空间信息（受人类会话保护）。
func (api *API) oauthConsentGet(w http.ResponseWriter, r *http.Request) {
	if api.oauth == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	request, _, _, err := api.parseOAuthAuthorizationRequest(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, "oauth_request_invalid", err.Error())
		return
	}
	client, normalized, err := api.oauth.NormalizeAuthorizationRequest(r.Context(), request)
	if err != nil {
		api.writeConsentValidationError(w, err)
		return
	}
	identity := identityFromRequest(r)
	writeJSON(w, http.StatusOK, oauthConsentResponse{
		ClientID:      client.ID,
		ClientName:    client.Name,
		RedirectURI:   normalized.RedirectURI,
		Scope:         normalized.Scope,
		Resource:      normalized.Resource,
		WorkspaceName: identity.WorkspaceName,
	})
}

// oauthConsentSubmit 处理 SPA 授权同意页的批准/拒绝决策（受人类会话与 requireJSONWrites 保护），
// 返回客户端回调地址 redirect_to，由前端通过 window.location.assign 导航。
func (api *API) oauthConsentSubmit(w http.ResponseWriter, r *http.Request) {
	if api.oauth == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var body oauthConsentSubmitRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	values := url.Values{}
	for key, value := range map[string]string{
		"response_type":         body.ResponseType,
		"client_id":             body.ClientID,
		"redirect_uri":          body.RedirectURI,
		"scope":                 body.Scope,
		"state":                 body.State,
		"code_challenge":        body.CodeChallenge,
		"code_challenge_method": body.CodeChallengeMethod,
		"resource":              body.Resource,
	} {
		if value != "" {
			values.Set(key, value)
		}
	}
	request, state, _, err := api.parseOAuthAuthorizationRequest(values)
	if err != nil {
		writeError(w, http.StatusBadRequest, "oauth_request_invalid", err.Error())
		return
	}
	_, normalized, err := api.oauth.NormalizeAuthorizationRequest(r.Context(), request)
	if err != nil {
		api.writeConsentValidationError(w, err)
		return
	}

	decision := strings.TrimSpace(body.Decision)
	switch decision {
	case "deny":
		redirectTo, err := api.buildOAuthAuthorizationRedirect(normalized.RedirectURI, state, "", "access_denied")
		if err != nil {
			writeError(w, http.StatusBadRequest, "oauth_request_invalid", "invalid redirect_uri")
			return
		}
		writeJSON(w, http.StatusOK, oauthConsentSubmitResponse{RedirectTo: redirectTo})
	case "approve", "allow":
		identity := identityFromRequest(r)
		code, err := api.oauth.Authorize(r.Context(), identity, normalized)
		if err != nil {
			api.writeConsentValidationError(w, err)
			return
		}
		redirectTo, err := api.buildOAuthAuthorizationRedirect(code.RedirectURI, state, code.Code, "")
		if err != nil {
			writeError(w, http.StatusBadRequest, "oauth_request_invalid", "invalid redirect_uri")
			return
		}
		writeJSON(w, http.StatusOK, oauthConsentSubmitResponse{RedirectTo: redirectTo})
	default:
		writeError(w, http.StatusBadRequest, "oauth_request_invalid", "decision must be approve, allow, or deny")
	}
}

// oauthAuthorizeSubmit 保留向后兼容的表单提交端点，响应显式移除 form-action CSP 限制，
// 避免兼容客户端或旧表单提交时 302 跨源回调被浏览器拦截。
func (api *API) oauthAuthorizeSubmit(w http.ResponseWriter, r *http.Request) {
	if api.oauth == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Security-Policy", oauthAuthorizeSubmitCSP)
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
	request, state, errCode, err := api.parseOAuthAuthorizationRequest(r.PostForm)
	if err != nil {
		writeOAuthAuthorizeError(w, http.StatusBadRequest, errCode, err.Error())
		return
	}
	_, normalized, err := api.oauth.NormalizeAuthorizationRequest(r.Context(), request)
	if err != nil {
		api.handleOAuthAuthorizeValidationError(w, err)
		return
	}
	identity, err := api.webIdentityFromCookie(r)
	if err != nil {
		writeOAuthAuthorizeError(w, http.StatusUnauthorized, "login_required", "sign in to CycleDock before authorizing this client")
		return
	}
	decision := strings.TrimSpace(r.PostForm.Get("decision"))
	if decision != "approve" && decision != "allow" {
		api.redirectOAuthAuthorizationResult(w, r, normalized.RedirectURI, state, "", "access_denied")
		return
	}
	code, err := api.oauth.Authorize(r.Context(), identity, normalized)
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

func (api *API) parseOAuthAuthorizationRequest(values url.Values) (oauth.AuthorizeRequest, string, string, error) {
	state := values.Get("state")
	if values.Get("response_type") != "code" {
		return oauth.AuthorizeRequest{}, state, "unsupported_response_type", errors.New("response_type must be code")
	}
	if values.Get("code_challenge_method") != "S256" {
		return oauth.AuthorizeRequest{}, state, "invalid_request", errors.New("code_challenge_method must be S256")
	}
	request := oauth.AuthorizeRequest{
		ClientID:      strings.TrimSpace(values.Get("client_id")),
		RedirectURI:   strings.TrimSpace(values.Get("redirect_uri")),
		CodeChallenge: strings.TrimSpace(values.Get("code_challenge")),
		Scope:         strings.TrimSpace(values.Get("scope")),
		Resource:      strings.TrimSpace(values.Get("resource")),
	}
	if request.ClientID == "" || request.RedirectURI == "" || request.CodeChallenge == "" {
		return oauth.AuthorizeRequest{}, state, "invalid_request", errors.New("client_id, redirect_uri and code_challenge are required")
	}
	return request, state, "", nil
}

func oauthConsentQueryValues(request oauth.AuthorizeRequest, state string) url.Values {
	values := url.Values{
		"response_type":         {"code"},
		"client_id":             {request.ClientID},
		"redirect_uri":          {request.RedirectURI},
		"scope":                 {request.Scope},
		"code_challenge":        {request.CodeChallenge},
		"code_challenge_method": {"S256"},
		"resource":              {request.Resource},
	}
	if state != "" {
		values.Set("state", state)
	}
	return values
}

func (api *API) buildOAuthAuthorizationRedirect(redirectURI, state, code, oauthError string) (string, error) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		return "", err
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
	return target.String(), nil
}

func (api *API) redirectOAuthAuthorizationResult(w http.ResponseWriter, r *http.Request, redirectURI, state, code, oauthError string) {
	target, err := api.buildOAuthAuthorizationRedirect(redirectURI, state, code, oauthError)
	if err != nil {
		writeOAuthAuthorizeError(w, http.StatusBadRequest, "invalid_request", "invalid redirect_uri")
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func oauthAuthorizeErrorCode(err error) string {
	switch {
	case errors.Is(err, oauth.ErrInvalidScope):
		return "invalid_scope"
	case errors.Is(err, oauth.ErrInvalidResource):
		return "invalid_target"
	case errors.Is(err, oauth.ErrAuthorizationDenied):
		return "access_denied"
	case errors.Is(err, oauth.ErrInvalidRequest), errors.Is(err, oauth.ErrInvalidClient), errors.Is(err, oauth.ErrUntrustedRedirect):
		return "invalid_request"
	default:
		return "server_error"
	}
}

func (api *API) writeConsentValidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, oauth.ErrInvalidClient), errors.Is(err, oauth.ErrUntrustedRedirect):
		writeError(w, http.StatusBadRequest, "oauth_request_invalid", "授权请求无效或已失效，请回到客户端重新发起")
	case errors.Is(err, oauth.ErrInvalidScope):
		writeError(w, http.StatusBadRequest, "invalid_scope", "不支持请求的权限范围")
	case errors.Is(err, oauth.ErrInvalidResource):
		writeError(w, http.StatusBadRequest, "invalid_target", "请求的目标资源不受支持")
	case errors.Is(err, oauth.ErrAuthorizationDenied):
		writeError(w, http.StatusForbidden, "access_denied", "当前账号无权批准该工作空间授权")
	case errors.Is(err, oauth.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, "oauth_request_invalid", err.Error())
	default:
		api.logger.Error("OAuth consent error", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "OAuth 授权服务暂时不可用")
	}
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
	// RFC 7591 动态注册允许客户端携带扩展元数据字段（如 client_uri、scope、software_id），
	// 这里不拒绝未知字段，避免外部 MCP 客户端注册失败。
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
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

// normalizeOAuthGrantTypes 允许客户端缺省 grant_types，或仅传 ["authorization_code"]，
// 只要包含 authorization_code 且全部值属于受支持集合即放行。
func normalizeOAuthGrantTypes(raw []string) ([]string, bool) {
	if len(raw) == 0 {
		return []string{"authorization_code", "refresh_token"}, true
	}
	seen := make(map[string]struct{}, len(raw))
	normalized := make([]string, 0, len(raw))
	hasAuthCode := false
	for _, item := range raw {
		switch item {
		case "authorization_code":
			hasAuthCode = true
		case "refresh_token":
		default:
			return nil, false
		}
		if _, exists := seen[item]; !exists {
			seen[item] = struct{}{}
			normalized = append(normalized, item)
		}
	}
	if !hasAuthCode {
		return nil, false
	}
	return normalized, true
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
