package modelauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	chatGPTAuthorizeURL          = "https://auth.openai.com/api/accounts/authorize"
	chatGPTTokenURL              = "https://auth.openai.com/api/accounts/oauth/token"
	chatGPTResource              = "https://api.openai.com/v1"
	chatGPTDynamicID             = "dynamic_agent_client"
	chatGPTHostedTestRedirectURI = "http://localhost:1455/auth/callback"
	chatGPTScope                 = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"

	xaiIssuer   = "https://auth.x.ai"
	xaiClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	xaiScope    = "openid profile email offline_access grok-cli:access api:access"
)

type chatGPTFlow struct {
	CodeVerifier string `json:"code_verifier"`
	Nonce        string `json:"nonce"`
	ClientID     string `json:"client_id"`
	RedirectURI  string `json:"redirect_uri"`
}

type ChatGPTStart struct {
	AuthorizationURL string `json:"authorization_url"`
	ManualCallback   bool   `json:"manual_callback,omitempty"`
}

type chatGPTAuthorizationConfig struct {
	ClientID       string
	RedirectURI    string
	ManualCallback bool
	IncludeHostID  bool
}

type grokFlow struct {
	DeviceCode string `json:"device_code"`
	TokenURL   string `json:"token_url"`
	ClientID   string `json:"client_id"`
	Interval   int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
}

type DeviceFlow struct {
	FlowID          uuid.UUID `json:"flow_id"`
	VerificationURL string    `json:"verification_url"`
	UserCode        string    `json:"user_code"`
	ExpiresAt       time.Time `json:"expires_at"`
	IntervalSeconds int       `json:"interval_seconds"`
}

type DevicePollResult struct {
	Status          string `json:"status"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
}

func (s *Service) StartChatGPT(ctx context.Context, workspaceID uuid.UUID) (ChatGPTStart, error) {
	if s.vault == nil {
		return ChatGPTStart{}, ErrUnavailable
	}
	authConfig, err := s.chatGPTAuthorizationConfig()
	if err != nil {
		return ChatGPTStart{}, err
	}
	if err := s.deleteExpiredFlows(ctx, workspaceID, ProviderChatGPT); err != nil {
		return ChatGPTStart{}, err
	}

	state, err := randomURLToken(32)
	if err != nil {
		return ChatGPTStart{}, err
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return ChatGPTStart{}, err
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return ChatGPTStart{}, err
	}
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	flowID := uuid.New()
	expiresAt := time.Now().Add(10 * time.Minute)
	payload := chatGPTFlow{
		CodeVerifier: verifier,
		Nonce:        nonce,
		ClientID:     authConfig.ClientID,
		RedirectURI:  authConfig.RedirectURI,
	}
	encrypted, err := s.vault.seal(payload, flowAAD(flowID, workspaceID, ProviderChatGPT))
	if err != nil {
		return ChatGPTStart{}, err
	}
	stateDigest := sha256.Sum256([]byte(state))
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO model_auth_flows (
			id, workspace_id, provider, state_digest, payload_ciphertext, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, flowID, workspaceID, string(ProviderChatGPT), stateDigest[:], encrypted, expiresAt); err != nil {
		return ChatGPTStart{}, err
	}

	params := url.Values{
		"client_id":             {authConfig.ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {authConfig.RedirectURI},
		"scope":                 {chatGPTScope},
		"resource":              {chatGPTResource},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge_method": {"S256"},
		"code_challenge":        {challenge},
	}
	if authConfig.IncludeHostID {
		params.Set("ext_agent_host_id", s.hostID)
	}
	if authConfig.ClientID == chatGPTDynamicID {
		params.Set("agent_name_hint", "CycleDock")
	}
	return ChatGPTStart{
		AuthorizationURL: chatGPTAuthorizeURL + "?" + params.Encode(),
		ManualCallback:   authConfig.ManualCallback,
	}, nil
}

func (s *Service) chatGPTAuthorizationConfig() (chatGPTAuthorizationConfig, error) {
	if s.publicURL == "" {
		return chatGPTAuthorizationConfig{}, errors.New("CYCLEDOCK_PUBLIC_URL is required for ChatGPT sign-in")
	}
	publicCallback := s.publicURL + "/auth/model/chatgpt/callback"
	if s.chatGPTClientID != "" {
		return chatGPTAuthorizationConfig{
			ClientID: s.chatGPTClientID, RedirectURI: publicCallback, IncludeHostID: true,
		}, nil
	}
	if !s.allowsDynamicChatGPTRegistration() {
		return chatGPTAuthorizationConfig{}, errors.New("CYCLEDOCK_CHATGPT_CLIENT_ID is required for remotely hosted ChatGPT sign-in")
	}
	if isLoopbackURL(s.publicURL) {
		return chatGPTAuthorizationConfig{
			ClientID: chatGPTDynamicID, RedirectURI: publicCallback,
		}, nil
	}
	return chatGPTAuthorizationConfig{
		ClientID: chatGPTDynamicID, RedirectURI: chatGPTHostedTestRedirectURI, ManualCallback: true,
	}, nil
}

func (s *Service) CompleteChatGPT(
	ctx context.Context,
	expectedWorkspaceID uuid.UUID,
	state string,
	code string,
	returnedClientID string,
) (uuid.UUID, error) {
	if s.vault == nil {
		return uuid.Nil, ErrUnavailable
	}
	if strings.TrimSpace(state) == "" || strings.TrimSpace(code) == "" {
		return uuid.Nil, ErrFlowNotFound
	}
	stateDigest := sha256.Sum256([]byte(state))

	var flowID, workspaceID uuid.UUID
	var encrypted []byte
	var row pgx.Row
	if expectedWorkspaceID == uuid.Nil {
		row = s.pool.QueryRow(ctx, `
			DELETE FROM model_auth_flows
			WHERE state_digest=$1
			  AND provider=$2
			  AND expires_at > now()
			RETURNING id, workspace_id, payload_ciphertext
		`, stateDigest[:], string(ProviderChatGPT))
	} else {
		row = s.pool.QueryRow(ctx, `
			DELETE FROM model_auth_flows
			WHERE state_digest=$1
			  AND provider=$2
			  AND workspace_id=$3
			  AND expires_at > now()
			RETURNING id, workspace_id, payload_ciphertext
		`, stateDigest[:], string(ProviderChatGPT), expectedWorkspaceID)
	}
	err := row.Scan(&flowID, &workspaceID, &encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrFlowNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}

	var flow chatGPTFlow
	if err := s.vault.open(encrypted, flowAAD(flowID, workspaceID, ProviderChatGPT), &flow); err != nil {
		return uuid.Nil, err
	}
	clientID := flow.ClientID
	if clientID == chatGPTDynamicID {
		clientID = strings.TrimSpace(returnedClientID)
		if clientID == "" || clientID == chatGPTDynamicID {
			return uuid.Nil, errors.New("ChatGPT dynamic registration did not return an issued client_id")
		}
	} else if returned := strings.TrimSpace(returnedClientID); returned != "" && returned != clientID {
		return uuid.Nil, errors.New("ChatGPT returned a different client_id")
	}

	redirectURI := flow.RedirectURI
	if redirectURI == "" {
		// 兼容升级前已经创建、但仍处于十分钟有效期内的旧授权 flow。
		redirectURI = s.publicURL + "/auth/model/chatgpt/callback"
	}
	token, err := s.exchangeToken(ctx, chatGPTTokenURL, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {flow.CodeVerifier},
		"redirect_uri":  {redirectURI},
		"resource":      {chatGPTResource},
	})
	if err != nil {
		return uuid.Nil, err
	}
	if !scopeContains(token.Scope, "chatgpt.tokens.use.direct") {
		return uuid.Nil, errors.New("ChatGPT sign-in completed without plan-usage permission")
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		return uuid.Nil, errors.New("ChatGPT token response is missing credentials")
	}
	if token.IDToken == "" {
		return uuid.Nil, errors.New("ChatGPT token response is missing id_token")
	}
	if err := s.verifyChatGPTIDToken(ctx, token.IDToken, clientID, flow.Nonce); err != nil {
		return uuid.Nil, err
	}
	expiresAt := tokenExpiry(token.ExpiresIn)
	if err := s.saveCredential(ctx, workspaceID, ProviderChatGPT, credentialPayload{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		ClientID: clientID, TokenURL: chatGPTTokenURL, Scope: token.Scope,
	}, expiresAt); err != nil {
		return uuid.Nil, err
	}
	return workspaceID, nil
}

func (s *Service) StartGrok(ctx context.Context, workspaceID uuid.UUID) (DeviceFlow, error) {
	if s.vault == nil {
		return DeviceFlow{}, ErrUnavailable
	}
	if err := s.deleteExpiredFlows(ctx, workspaceID, ProviderGrok); err != nil {
		return DeviceFlow{}, err
	}
	discovery, err := s.xaiDiscovery(ctx)
	if err != nil {
		return DeviceFlow{}, err
	}
	deviceURL, err := trustedXAIEndpoint(discovery.DeviceAuthorizationEndpoint)
	if err != nil {
		return DeviceFlow{}, err
	}
	tokenURL, err := trustedXAIEndpoint(discovery.TokenEndpoint)
	if err != nil {
		return DeviceFlow{}, err
	}

	var response struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := s.postFormJSON(ctx, deviceURL, url.Values{
		"client_id": {xaiClientID},
		"scope":     {xaiScope},
		"referrer":  {"cycledock"},
	}, xaiOAuthHeaders("ui"), &response); err != nil {
		return DeviceFlow{}, err
	}
	if response.DeviceCode == "" || response.UserCode == "" || response.VerificationURI == "" {
		return DeviceFlow{}, errors.New("xAI device authorization response is incomplete")
	}
	verificationURL := response.VerificationURIComplete
	if verificationURL == "" {
		verificationURL = response.VerificationURI
	}
	if _, err := trustedXAIEndpoint(verificationURL); err != nil {
		return DeviceFlow{}, err
	}
	if response.ExpiresIn <= 0 {
		response.ExpiresIn = 600
	}
	if response.Interval <= 0 {
		response.Interval = 5
	}

	flowID := uuid.New()
	expiresAt := time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)
	encrypted, err := s.vault.seal(grokFlow{
		DeviceCode: response.DeviceCode, TokenURL: tokenURL,
		ClientID: xaiClientID, Interval: response.Interval,
	}, flowAAD(flowID, workspaceID, ProviderGrok))
	if err != nil {
		return DeviceFlow{}, err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO model_auth_flows (
			id, workspace_id, provider, state_digest, payload_ciphertext, expires_at
		) VALUES ($1, $2, $3, NULL, $4, $5)
	`, flowID, workspaceID, string(ProviderGrok), encrypted, expiresAt); err != nil {
		return DeviceFlow{}, err
	}
	return DeviceFlow{
		FlowID: flowID, VerificationURL: verificationURL, UserCode: response.UserCode,
		ExpiresAt: expiresAt, IntervalSeconds: response.Interval,
	}, nil
}

func (s *Service) PollGrok(
	ctx context.Context,
	workspaceID, flowID uuid.UUID,
) (DevicePollResult, error) {
	if s.vault == nil {
		return DevicePollResult{}, ErrUnavailable
	}
	var encrypted []byte
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT payload_ciphertext, expires_at
		FROM model_auth_flows
		WHERE id=$1 AND workspace_id=$2 AND provider=$3
	`, flowID, workspaceID, string(ProviderGrok)).Scan(&encrypted, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return DevicePollResult{}, ErrFlowNotFound
	}
	if err != nil {
		return DevicePollResult{}, err
	}
	if !expiresAt.After(time.Now()) {
		_, _ = s.pool.Exec(ctx, "DELETE FROM model_auth_flows WHERE id=$1", flowID)
		return DevicePollResult{}, ErrFlowNotFound
	}

	var flow grokFlow
	if err := s.vault.open(encrypted, flowAAD(flowID, workspaceID, ProviderGrok), &flow); err != nil {
		return DevicePollResult{}, err
	}
	token, oauthError, err := s.exchangeTokenWithOAuthErrorHeaders(ctx, flow.TokenURL, url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id":   {flow.ClientID},
		"device_code": {flow.DeviceCode},
	}, xaiOAuthHeaders("ui"))
	if err != nil {
		return DevicePollResult{}, err
	}
	switch oauthError {
	case "authorization_pending":
		return DevicePollResult{Status: "pending", IntervalSeconds: flow.Interval}, nil
	case "slow_down":
		return DevicePollResult{Status: "pending", IntervalSeconds: flow.Interval + 5}, nil
	case "access_denied", "authorization_denied", "expired_token":
		_, _ = s.pool.Exec(ctx, "DELETE FROM model_auth_flows WHERE id=$1", flowID)
		return DevicePollResult{}, errors.New("xAI device authorization was not completed")
	case "":
	default:
		return DevicePollResult{}, fmt.Errorf("xAI device authorization failed: %s", oauthError)
	}

	if token.AccessToken == "" || token.RefreshToken == "" {
		return DevicePollResult{}, errors.New("xAI token response is missing credentials")
	}
	userID, email := xaiTokenIdentity(token)
	if err := s.saveCredential(ctx, workspaceID, ProviderGrok, credentialPayload{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		ClientID: flow.ClientID, TokenURL: flow.TokenURL, Scope: token.Scope,
		UserID: userID, Email: email,
	}, tokenExpiry(token.ExpiresIn)); err != nil {
		return DevicePollResult{}, err
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM model_auth_flows WHERE id=$1", flowID); err != nil {
		return DevicePollResult{}, err
	}
	return DevicePollResult{Status: "connected"}, nil
}

func (s *Service) refreshCredential(
	ctx context.Context,
	provider Provider,
	current credentialPayload,
) (credentialPayload, time.Time, error) {
	values := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {current.ClientID},
		"refresh_token": {current.RefreshToken},
	}
	if provider == ProviderChatGPT {
		values.Set("resource", chatGPTResource)
	}
	var token tokenResponse
	var err error
	if provider == ProviderGrok {
		token, err = s.exchangeTokenHeaders(ctx, current.TokenURL, values, xaiOAuthHeaders("headless"))
	} else {
		token, err = s.exchangeToken(ctx, current.TokenURL, values)
	}
	if err != nil {
		return credentialPayload{}, time.Time{}, err
	}
	if token.AccessToken == "" {
		return credentialPayload{}, time.Time{}, ErrReauthRequired
	}
	refreshToken := token.RefreshToken
	if refreshToken == "" {
		refreshToken = current.RefreshToken
	}
	scope := token.Scope
	if scope == "" {
		scope = current.Scope
	}
	userID, email := current.UserID, current.Email
	if provider == ProviderGrok {
		if refreshedUserID, refreshedEmail := xaiTokenIdentity(token); refreshedUserID != "" || refreshedEmail != "" {
			userID, email = refreshedUserID, refreshedEmail
		}
	}
	return credentialPayload{
		AccessToken: token.AccessToken, RefreshToken: refreshToken,
		ClientID: current.ClientID, TokenURL: current.TokenURL, Scope: scope,
		UserID: userID, Email: email,
	}, tokenExpiry(token.ExpiresIn), nil
}

func (s *Service) verifyChatGPTIDToken(
	ctx context.Context,
	rawIDToken string,
	clientID string,
	expectedNonce string,
) error {
	providerCtx := oidc.ClientContext(ctx, s.httpClient)
	provider, err := oidc.NewProvider(providerCtx, "https://auth.openai.com")
	if err != nil {
		return fmt.Errorf("discover ChatGPT identity provider: %w", err)
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: clientID}).Verify(providerCtx, rawIDToken)
	if err != nil {
		return errors.New("verify ChatGPT identity token")
	}
	var claims struct {
		Nonce string `json:"nonce"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return errors.New("decode ChatGPT identity token")
	}
	if claims.Nonce == "" || claims.Nonce != expectedNonce {
		return errors.New("ChatGPT identity token nonce mismatch")
	}
	return nil
}

type oidcDiscovery struct {
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
}

func (s *Service) xaiDiscovery(ctx context.Context) (oidcDiscovery, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, xaiIssuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return oidcDiscovery{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "CycleDock")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return oidcDiscovery{}, fmt.Errorf("xAI OAuth discovery failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return oidcDiscovery{}, fmt.Errorf("xAI OAuth discovery returned HTTP %d", response.StatusCode)
	}
	var discovery oidcDiscovery
	if err := decodeBoundedJSON(response.Body, &discovery); err != nil {
		return oidcDiscovery{}, err
	}
	return discovery, nil
}

func (s *Service) exchangeToken(
	ctx context.Context,
	endpoint string,
	values url.Values,
) (tokenResponse, error) {
	return s.exchangeTokenHeaders(ctx, endpoint, values, nil)
}

func (s *Service) exchangeTokenHeaders(
	ctx context.Context,
	endpoint string,
	values url.Values,
	headers map[string]string,
) (tokenResponse, error) {
	token, oauthError, err := s.exchangeTokenWithOAuthErrorHeaders(ctx, endpoint, values, headers)
	if err != nil {
		return tokenResponse{}, err
	}
	if oauthError != "" {
		return tokenResponse{}, fmt.Errorf("OAuth token exchange failed: %s", oauthError)
	}
	return token, nil
}

func (s *Service) exchangeTokenWithOAuthError(
	ctx context.Context,
	endpoint string,
	values url.Values,
) (tokenResponse, string, error) {
	return s.exchangeTokenWithOAuthErrorHeaders(ctx, endpoint, values, nil)
}

func (s *Service) exchangeTokenWithOAuthErrorHeaders(
	ctx context.Context,
	endpoint string,
	values url.Values,
	headers map[string]string,
) (tokenResponse, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return tokenResponse{}, "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "CycleDock")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return tokenResponse{}, "", fmt.Errorf("OAuth token request failed: %w", err)
	}
	defer response.Body.Close()

	var body struct {
		tokenResponse
		Error string `json:"error"`
	}
	if err := decodeBoundedJSON(response.Body, &body); err != nil {
		return tokenResponse{}, "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if body.Error == "" {
			body.Error = fmt.Sprintf("http_%d", response.StatusCode)
		}
		return tokenResponse{}, body.Error, nil
	}
	return body.tokenResponse, "", nil
}

func (s *Service) postFormJSON(
	ctx context.Context,
	endpoint string,
	values url.Values,
	headers map[string]string,
	destination any,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "CycleDock")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OAuth request returned HTTP %d", response.StatusCode)
	}
	return decodeBoundedJSON(response.Body, destination)
}

func decodeBoundedJSON(reader io.Reader, destination any) error {
	limited := io.LimitReader(reader, 1<<20)
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode OAuth response: %w", err)
	}
	return nil
}

func randomURLToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func trustedXAIEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" {
		return "", errors.New("xAI OAuth returned an untrusted endpoint")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "x.ai" && !strings.HasSuffix(host, ".x.ai") {
		return "", errors.New("xAI OAuth returned an untrusted endpoint")
	}
	return parsed.String(), nil
}

func scopeContains(scope, expected string) bool {
	for _, item := range strings.Fields(scope) {
		if item == expected {
			return true
		}
	}
	return false
}

func xaiOAuthHeaders(surface string) map[string]string {
	return map[string]string{
		"x-grok-client-version": grokProxyClientVersion,
		"x-grok-client-surface": surface,
	}
}

// xAI 官方 device-code 客户端同样只把 id_token claims 用于展示/路由辅助；
// 服务端仍会验证真正用于鉴权的 access token。
func xaiTokenIdentity(token tokenResponse) (string, string) {
	type claims struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		PrincipalType string `json:"principal_type"`
		PrincipalID   string `json:"principal_id"`
	}
	decode := func(raw string) claims {
		parts := strings.Split(raw, ".")
		if len(parts) < 2 {
			return claims{}
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return claims{}
		}
		var value claims
		if json.Unmarshal(payload, &value) != nil {
			return claims{}
		}
		return value
	}

	idClaims := decode(token.IDToken)
	accessClaims := decode(token.AccessToken)
	if accessClaims.PrincipalID != "" &&
		(accessClaims.PrincipalType == "Team" || accessClaims.PrincipalType == "Organization") {
		return accessClaims.PrincipalID, ""
	}
	return idClaims.Sub, idClaims.Email
}

func tokenExpiry(expiresIn int) time.Time {
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return time.Now().Add(time.Duration(expiresIn) * time.Second)
}

func (s *Service) deleteExpiredFlows(ctx context.Context, workspaceID uuid.UUID, provider Provider) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM model_auth_flows
		WHERE workspace_id=$1 AND provider=$2 AND expires_at <= now()
	`, workspaceID, string(provider))
	return err
}
