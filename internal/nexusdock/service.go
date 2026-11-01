package nexusdock

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/uvwt/CycleDock/internal/store/db"
)

const (
	authFlowTTL       = 10 * time.Minute
	accessRefreshSkew = 2 * time.Minute
	maxResponseBytes  = 256 << 10
)

var (
	ErrDisabled          = errors.New("NexusDock first-party connection is disabled")
	ErrNotConnected      = errors.New("NexusDock is not connected")
	ErrInvalidState      = errors.New("invalid NexusDock authorization state")
	ErrAuthorization     = errors.New("NexusDock authorization failed")
	ErrInvalidConfig     = errors.New("invalid NexusDock connection configuration")
	ErrConnectionExpired = errors.New("NexusDock authorization expired")
)

type Config struct {
	MCPURL        string
	WebURL        string
	PublicURL     string
	EncryptionKey string
	HTTPClient    *http.Client
}

type Service struct {
	pool        *pgxpool.Pool
	queries     *db.Queries
	mcpURL      string
	webURL      string
	callbackURL string
	aead        cipher.AEAD
	client      *http.Client
	fleetMu     sync.RWMutex
	fleetCache  map[uuid.UUID]fleetCacheEntry
}

type Status struct {
	Available         bool
	Connected         bool
	ManageURL         string
	ConnectedAt       time.Time
	AuthorizedSubject string
}

type AuthorizationStart struct {
	URL       string
	ExpiresAt time.Time
}

type RuntimeConnection struct {
	URL         string
	AccessToken string
}

type protectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

type authorizationServerMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	RevocationEndpoint            string   `json:"revocation_endpoint"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported               []string `json:"scopes_supported"`
}

type oauthMetadata struct {
	Resource string
	Server   authorizationServerMetadata
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Subject      string `json:"sub"`
	Scope        string `json:"scope"`
}

func New(pool *pgxpool.Pool, config Config) (*Service, error) {
	config.MCPURL = strings.TrimRight(strings.TrimSpace(config.MCPURL), "/")
	config.WebURL = strings.TrimRight(strings.TrimSpace(config.WebURL), "/")
	config.PublicURL = strings.TrimRight(strings.TrimSpace(config.PublicURL), "/")
	config.EncryptionKey = strings.TrimSpace(config.EncryptionKey)

	if config.MCPURL == "" && config.WebURL == "" && config.EncryptionKey == "" {
		return nil, nil
	}
	if pool == nil || config.MCPURL == "" || config.WebURL == "" || config.PublicURL == "" || config.EncryptionKey == "" {
		return nil, fmt.Errorf("%w: MCP URL, Web URL, Public URL and encryption key are required together", ErrInvalidConfig)
	}
	if err := validateServiceURL(config.MCPURL); err != nil {
		return nil, fmt.Errorf("%w: MCP URL: %v", ErrInvalidConfig, err)
	}
	if err := validateServiceURL(config.WebURL); err != nil {
		return nil, fmt.Errorf("%w: Web URL: %v", ErrInvalidConfig, err)
	}
	if err := validateServiceURL(config.PublicURL); err != nil {
		return nil, fmt.Errorf("%w: Public URL: %v", ErrInvalidConfig, err)
	}

	key, err := parseEncryptionKey(config.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("%w: encryption key: %v", ErrInvalidConfig, err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: create cipher: %v", ErrInvalidConfig, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: create GCM: %v", ErrInvalidConfig, err)
	}

	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Service{
		pool: pool, queries: db.New(pool), mcpURL: config.MCPURL, webURL: config.WebURL,
		callbackURL: config.PublicURL + "/auth/nexusdock-device/callback",
		aead:        aead, client: client,
		fleetCache:  make(map[uuid.UUID]fleetCacheEntry),
	}, nil
}

func (s *Service) Enabled() bool {
	return s != nil
}

func (s *Service) Status(ctx context.Context, workspaceID uuid.UUID) (Status, error) {
	if s == nil {
		return Status{Available: false}, nil
	}
	status := Status{
		Available: true,
		ManageURL: s.webURL + "/workspace/devices",
	}
	connection, err := s.queries.GetWorkspaceNexusDockConnection(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return Status{}, fmt.Errorf("load NexusDock connection: %w", err)
	}
	status.Connected = true
	status.AuthorizedSubject = connection.AuthorizedSubject
	if connection.ConnectedAt.Valid {
		status.ConnectedAt = connection.ConnectedAt.Time
	}
	return status, nil
}

func (s *Service) BeginAuthorization(
	ctx context.Context,
	workspaceID, humanAccountID, sourceID uuid.UUID,
) (AuthorizationStart, error) {
	if s == nil {
		return AuthorizationStart{}, ErrDisabled
	}
	metadata, err := s.discover(ctx)
	if err != nil {
		return AuthorizationStart{}, err
	}
	clientID, err := s.ensureOAuthClient(ctx, metadata)
	if err != nil {
		return AuthorizationStart{}, err
	}
	if err := s.queries.DeleteExpiredNexusDockAuthFlows(ctx); err != nil {
		return AuthorizationStart{}, fmt.Errorf("clean NexusDock auth flows: %w", err)
	}

	stateRaw := make([]byte, 32)
	if _, err := rand.Read(stateRaw); err != nil {
		return AuthorizationStart{}, fmt.Errorf("generate NexusDock state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateRaw)
	stateDigest := sha256.Sum256([]byte(state))
	verifier := oauth2.GenerateVerifier()
	expiresAt := time.Now().UTC().Add(authFlowTTL)
	if err := s.queries.CreateNexusDockAuthFlow(ctx, db.CreateNexusDockAuthFlowParams{
		StateDigest: stateDigest[:], WorkspaceID: workspaceID, HumanAccountID: humanAccountID,
		SourceID: sourceID, ClientID: clientID, CodeVerifier: verifier,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		return AuthorizationStart{}, fmt.Errorf("save NexusDock auth flow: %w", err)
	}

	challengeDigest := sha256.Sum256([]byte(verifier))
	values := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {s.callbackURL},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challengeDigest[:])},
		"code_challenge_method": {"S256"},
		"resource":              {metadata.Resource},
		"scope":                 {"mcp"},
		"state":                 {state},
	}
	return AuthorizationStart{
		URL:       metadata.Server.AuthorizationEndpoint + "?" + values.Encode(),
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) consumeAuthorization(ctx context.Context, workspaceID, humanAccountID, sourceID uuid.UUID, state string) (db.NexusdockAuthFlow, error) {
	if s == nil {
		return db.NexusdockAuthFlow{}, ErrDisabled
	}
	if strings.TrimSpace(state) == "" {
		return db.NexusdockAuthFlow{}, ErrInvalidState
	}
	digest := sha256.Sum256([]byte(state))
	flow, err := s.queries.ConsumeNexusDockAuthFlow(ctx, db.ConsumeNexusDockAuthFlowParams{
		StateDigest: digest[:], WorkspaceID: workspaceID, HumanAccountID: humanAccountID, SourceID: sourceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.NexusdockAuthFlow{}, ErrInvalidState
	}
	if err != nil {
		return db.NexusdockAuthFlow{}, fmt.Errorf("consume NexusDock auth flow: %w", err)
	}
	return flow, nil
}

func (s *Service) DenyAuthorization(ctx context.Context, workspaceID, humanAccountID, sourceID uuid.UUID, state string) error {
	// 用户拒绝设备授权时只消耗该次 state；已经建立的登录 Session 不受影响。
	_, err := s.consumeAuthorization(ctx, workspaceID, humanAccountID, sourceID, state)
	return err
}

// 设备 OAuth 授权属于资源所有者 B，允许与 CycleDock 的 OIDC 登录身份 A 不同。
// 但 state 必须绑定当前 A 的 Human、Source 和 Workspace，绝不能跨工作空间使用。
func (s *Service) CompleteAuthorization(ctx context.Context, workspaceID, humanAccountID, sourceID uuid.UUID, state, code string) error {
	if strings.TrimSpace(code) == "" {
		return ErrInvalidState
	}
	flow, err := s.consumeAuthorization(ctx, workspaceID, humanAccountID, sourceID, state)
	if err != nil {
		return err
	}
	metadata, err := s.discover(ctx)
	if err != nil {
		return err
	}
	tokens, err := s.exchangeCode(ctx, metadata, flow.ClientID, flow.CodeVerifier, code)
	if err != nil {
		return err
	}
	if strings.TrimSpace(tokens.Subject) == "" {
		// NexusDock 第一方返回资源账号 sub；不接受无法识别所有者的 Token。
		if tokens.RefreshToken != "" {
			_ = s.postFormNoContent(ctx, metadata.Server.RevocationEndpoint, url.Values{
				"client_id": {flow.ClientID}, "token": {tokens.RefreshToken}, "token_type_hint": {"refresh_token"},
			})
		}
		return ErrAuthorization
	}
	return s.storeTokens(ctx, workspaceID, metadata.Resource, flow.ClientID, tokens)
}

func (s *Service) Runtime(ctx context.Context, workspaceID uuid.UUID) (RuntimeConnection, error) {
	if s == nil {
		return RuntimeConnection{}, ErrDisabled
	}
	row, err := s.queries.GetWorkspaceNexusDockConnection(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeConnection{}, ErrNotConnected
	}
	if err != nil {
		return RuntimeConnection{}, fmt.Errorf("load NexusDock connection: %w", err)
	}
	if row.Resource != s.mcpURL {
		return RuntimeConnection{}, fmt.Errorf("%w: configured MCP resource changed", ErrConnectionExpired)
	}
	if row.AccessExpiresAt.Valid && time.Until(row.AccessExpiresAt.Time) > accessRefreshSkew {
		token, err := s.decryptToken(workspaceID, "access", row.AccessTokenCiphertext)
		if err != nil {
			return RuntimeConnection{}, err
		}
		return RuntimeConnection{URL: row.Resource, AccessToken: token}, nil
	}
	return s.refresh(ctx, workspaceID)
}

func (s *Service) Disconnect(ctx context.Context, workspaceID uuid.UUID) error {
	if s == nil {
		return ErrDisabled
	}
	row, err := s.queries.GetWorkspaceNexusDockConnection(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load NexusDock connection: %w", err)
	}
	refreshToken, err := s.decryptToken(workspaceID, "refresh", row.RefreshTokenCiphertext)
	if err != nil {
		return err
	}
	metadata, err := s.discover(ctx)
	if err != nil {
		return err
	}
	form := url.Values{
		"token":           {refreshToken},
		"token_type_hint": {"refresh_token"},
		"client_id":       {row.ClientID},
	}
	if err := s.postFormNoContent(ctx, metadata.Server.RevocationEndpoint, form); err != nil {
		return fmt.Errorf("revoke NexusDock authorization: %w", err)
	}
	if _, err := s.queries.DeleteWorkspaceNexusDockConnection(ctx, workspaceID); err != nil {
		return fmt.Errorf("delete NexusDock connection: %w", err)
	}
	return nil
}

func (s *Service) refresh(ctx context.Context, workspaceID uuid.UUID) (RuntimeConnection, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return RuntimeConnection{}, fmt.Errorf("acquire NexusDock refresh lock connection: %w", err)
	}
	defer conn.Release()

	lockKey := "cycledock/nexusdock/refresh/" + workspaceID.String()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return RuntimeConnection{}, fmt.Errorf("lock NexusDock token refresh: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockKey)
	}()

	q := db.New(conn)
	row, err := q.GetWorkspaceNexusDockConnection(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeConnection{}, ErrNotConnected
	}
	if err != nil {
		return RuntimeConnection{}, fmt.Errorf("reload NexusDock connection: %w", err)
	}
	if row.AccessExpiresAt.Valid && time.Until(row.AccessExpiresAt.Time) > accessRefreshSkew {
		token, err := s.decryptToken(workspaceID, "access", row.AccessTokenCiphertext)
		if err != nil {
			return RuntimeConnection{}, err
		}
		return RuntimeConnection{URL: row.Resource, AccessToken: token}, nil
	}

	refreshToken, err := s.decryptToken(workspaceID, "refresh", row.RefreshTokenCiphertext)
	if err != nil {
		return RuntimeConnection{}, err
	}
	metadata, err := s.discover(ctx)
	if err != nil {
		return RuntimeConnection{}, err
	}
	tokens, err := s.exchangeRefresh(ctx, metadata, row.ClientID, refreshToken)
	if err != nil {
		if errors.Is(err, ErrAuthorization) {
			return RuntimeConnection{}, ErrConnectionExpired
		}
		return RuntimeConnection{}, err
	}
	// 刷新仅能续期原有资源账号，不能无声替换成另一位账号的设备权限。
	if row.AuthorizedSubject != "" && tokens.Subject != row.AuthorizedSubject {
		return RuntimeConnection{}, ErrConnectionExpired
	}
	updated, err := s.storeTokensWithQueries(ctx, q, workspaceID, metadata.Resource, row.ClientID, tokens)
	if err != nil {
		return RuntimeConnection{}, err
	}
	access, err := s.decryptToken(workspaceID, "access", updated.AccessTokenCiphertext)
	if err != nil {
		return RuntimeConnection{}, err
	}
	return RuntimeConnection{URL: updated.Resource, AccessToken: access}, nil
}

func (s *Service) storeTokens(
	ctx context.Context,
	workspaceID uuid.UUID,
	resource, clientID string,
	tokens tokenResponse,
) error {
	_, err := s.storeTokensWithQueries(ctx, s.queries, workspaceID, resource, clientID, tokens)
	return err
}

func (s *Service) storeTokensWithQueries(
	ctx context.Context,
	q *db.Queries,
	workspaceID uuid.UUID,
	resource, clientID string,
	tokens tokenResponse,
) (db.WorkspaceNexusdockConnection, error) {
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || strings.TrimSpace(tokens.Subject) == "" || !strings.EqualFold(tokens.TokenType, "Bearer") || tokens.ExpiresIn <= 0 {
		return db.WorkspaceNexusdockConnection{}, fmt.Errorf("%w: incomplete token response", ErrAuthorization)
	}
	accessCiphertext, err := s.encryptToken(workspaceID, "access", tokens.AccessToken)
	if err != nil {
		return db.WorkspaceNexusdockConnection{}, err
	}
	refreshCiphertext, err := s.encryptToken(workspaceID, "refresh", tokens.RefreshToken)
	if err != nil {
		return db.WorkspaceNexusdockConnection{}, err
	}
	expiresAt := time.Now().UTC().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	row, err := q.UpsertWorkspaceNexusDockConnection(ctx, db.UpsertWorkspaceNexusDockConnectionParams{
		WorkspaceID: workspaceID, Resource: resource, ClientID: clientID,
		AccessTokenCiphertext: accessCiphertext, RefreshTokenCiphertext: refreshCiphertext,
		AccessExpiresAt:   pgtype.Timestamptz{Time: expiresAt, Valid: true},
		AuthorizedSubject: tokens.Subject,
	})
	if err != nil {
		return db.WorkspaceNexusdockConnection{}, fmt.Errorf("save NexusDock connection: %w", err)
	}
	return row, nil
}

func (s *Service) ensureOAuthClient(ctx context.Context, metadata oauthMetadata) (string, error) {
	row, err := s.queries.GetNexusDockOAuthClient(ctx, metadata.Resource)
	if err == nil {
		return row.ClientID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("load NexusDock OAuth client: %w", err)
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire NexusDock client lock connection: %w", err)
	}
	defer conn.Release()
	lockKey := "cycledock/nexusdock/oauth-client/" + metadata.Resource
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return "", fmt.Errorf("lock NexusDock OAuth client registration: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockKey)
	}()

	q := db.New(conn)
	row, err = q.GetNexusDockOAuthClient(ctx, metadata.Resource)
	if err == nil {
		return row.ClientID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("reload NexusDock OAuth client: %w", err)
	}

	request := map[string]any{
		"client_name":                "CycleDock",
		"redirect_uris":              []string{s.callbackURL},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	var response struct {
		ClientID string `json:"client_id"`
	}
	if err := s.postJSON(ctx, metadata.Server.RegistrationEndpoint, request, &response, http.StatusCreated); err != nil {
		return "", fmt.Errorf("register NexusDock OAuth client: %w", err)
	}
	if response.ClientID == "" {
		return "", fmt.Errorf("%w: NexusDock registration omitted client_id", ErrAuthorization)
	}
	saved, err := q.CreateNexusDockOAuthClient(ctx, db.CreateNexusDockOAuthClientParams{
		Resource: metadata.Resource, ClientID: response.ClientID,
	})
	if err != nil {
		return "", fmt.Errorf("save NexusDock OAuth client: %w", err)
	}
	return saved.ClientID, nil
}

func (s *Service) exchangeCode(
	ctx context.Context,
	metadata oauthMetadata,
	clientID, verifier, code string,
) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {s.callbackURL},
		"code_verifier": {verifier},
		"resource":      {metadata.Resource},
	}
	return s.exchangeToken(ctx, metadata.Server.TokenEndpoint, form)
}

func (s *Service) exchangeRefresh(
	ctx context.Context,
	metadata oauthMetadata,
	clientID, refreshToken string,
) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refreshToken},
		"resource":      {metadata.Resource},
	}
	return s.exchangeToken(ctx, metadata.Server.TokenEndpoint, form)
}

func (s *Service) exchangeToken(ctx context.Context, endpoint string, form url.Values) (tokenResponse, error) {
	var response tokenResponse
	status, body, err := s.doForm(ctx, endpoint, form)
	if err != nil {
		return response, err
	}
	if status != http.StatusOK {
		if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
			return response, ErrAuthorization
		}
		return response, fmt.Errorf("NexusDock token endpoint returned HTTP %d", status)
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return response, fmt.Errorf("%w: invalid token response", ErrAuthorization)
	}
	return response, nil
}

func (s *Service) discover(ctx context.Context) (oauthMetadata, error) {
	resourceURL, err := url.Parse(s.mcpURL)
	if err != nil {
		return oauthMetadata{}, fmt.Errorf("%w: parse MCP URL", ErrInvalidConfig)
	}
	metadataPath := "/.well-known/oauth-protected-resource"
	if resourceURL.Path != "" && resourceURL.Path != "/" {
		metadataPath += "/" + strings.TrimPrefix(strings.TrimRight(resourceURL.EscapedPath(), "/"), "/")
	}
	resourceMetadataURL := resourceURL.Scheme + "://" + resourceURL.Host + metadataPath

	var resourceMeta protectedResourceMetadata
	if err := s.getJSON(ctx, resourceMetadataURL, &resourceMeta); err != nil {
		return oauthMetadata{}, fmt.Errorf("discover NexusDock protected resource: %w", err)
	}
	if strings.TrimRight(resourceMeta.Resource, "/") != s.mcpURL || len(resourceMeta.AuthorizationServers) == 0 {
		return oauthMetadata{}, fmt.Errorf("%w: protected resource metadata mismatch", ErrAuthorization)
	}
	issuer := strings.TrimRight(strings.TrimSpace(resourceMeta.AuthorizationServers[0]), "/")
	if err := validateServiceURL(issuer); err != nil {
		return oauthMetadata{}, fmt.Errorf("%w: invalid authorization server", ErrAuthorization)
	}

	var serverMeta authorizationServerMetadata
	if err := s.getJSON(ctx, issuer+"/.well-known/oauth-authorization-server", &serverMeta); err != nil {
		return oauthMetadata{}, fmt.Errorf("discover NexusDock authorization server: %w", err)
	}
	if strings.TrimRight(serverMeta.Issuer, "/") != issuer ||
		serverMeta.AuthorizationEndpoint == "" || serverMeta.TokenEndpoint == "" ||
		serverMeta.RegistrationEndpoint == "" || serverMeta.RevocationEndpoint == "" ||
		!contains(serverMeta.CodeChallengeMethodsSupported, "S256") ||
		!contains(serverMeta.TokenEndpointAuthMethods, "none") ||
		!contains(serverMeta.ScopesSupported, "mcp") {
		return oauthMetadata{}, fmt.Errorf("%w: unsupported authorization server metadata", ErrAuthorization)
	}
	for _, endpoint := range []string{
		serverMeta.AuthorizationEndpoint, serverMeta.TokenEndpoint,
		serverMeta.RegistrationEndpoint, serverMeta.RevocationEndpoint,
	} {
		if err := validateEndpointForIssuer(endpoint, issuer); err != nil {
			return oauthMetadata{}, fmt.Errorf("%w: unsafe OAuth endpoint", ErrAuthorization)
		}
	}
	return oauthMetadata{Resource: resourceMeta.Resource, Server: serverMeta}, nil
}

func (s *Service) getJSON(ctx context.Context, endpoint string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxResponseBytes {
		return errors.New("upstream response too large")
	}
	if err := json.Unmarshal(body, output); err != nil {
		return errors.New("upstream returned invalid JSON")
	}
	return nil
}

func (s *Service) postJSON(ctx context.Context, endpoint string, input, output any, expectedStatus int) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		return fmt.Errorf("upstream status %d", response.StatusCode)
	}
	limited, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(limited) > maxResponseBytes {
		return errors.New("upstream response too large")
	}
	if err := json.Unmarshal(limited, output); err != nil {
		return errors.New("upstream returned invalid JSON")
	}
	return nil
}

func (s *Service) doForm(ctx context.Context, endpoint string, form url.Values) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return 0, nil, err
	}
	if len(body) > maxResponseBytes {
		return 0, nil, errors.New("upstream response too large")
	}
	return response.StatusCode, body, nil
}

func (s *Service) postFormNoContent(ctx context.Context, endpoint string, form url.Values) error {
	status, _, err := s.doForm(ctx, endpoint, form)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("upstream status %d", status)
	}
	return nil
}

func (s *Service) encryptToken(workspaceID uuid.UUID, purpose, plaintext string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate NexusDock token nonce: %w", err)
	}
	aad := []byte("cycledock/nexusdock/" + workspaceID.String() + "/" + purpose)
	return s.aead.Seal(nonce, nonce, []byte(plaintext), aad), nil
}

func (s *Service) decryptToken(workspaceID uuid.UUID, purpose string, ciphertext []byte) (string, error) {
	nonceSize := s.aead.NonceSize()
	if len(ciphertext) < nonceSize+s.aead.Overhead() {
		return "", fmt.Errorf("%w: invalid encrypted token", ErrConnectionExpired)
	}
	nonce, encrypted := ciphertext[:nonceSize], ciphertext[nonceSize:]
	aad := []byte("cycledock/nexusdock/" + workspaceID.String() + "/" + purpose)
	plaintext, err := s.aead.Open(nil, nonce, encrypted, aad)
	if err != nil {
		return "", fmt.Errorf("%w: token decryption failed", ErrConnectionExpired)
	}
	return string(plaintext), nil
}

func parseEncryptionKey(raw string) ([]byte, error) {
	decoders := []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
		hex.DecodeString,
	}
	for _, decode := range decoders {
		value, err := decode(raw)
		if err == nil && len(value) == 32 {
			return value, nil
		}
	}
	return nil, errors.New("must encode exactly 32 bytes as base64 or hex")
}

func validateServiceURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return errors.New("must be an absolute URL without credentials, query or fragment")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return nil
	}
	return errors.New("remote URLs must use HTTPS")
}

func validateEndpointForIssuer(endpoint, issuer string) error {
	if err := validateServiceURL(endpoint); err != nil {
		return err
	}
	endpointURL, _ := url.Parse(endpoint)
	issuerURL, _ := url.Parse(issuer)
	if !strings.EqualFold(endpointURL.Scheme, issuerURL.Scheme) || !strings.EqualFold(endpointURL.Host, issuerURL.Host) {
		return errors.New("OAuth endpoint origin differs from issuer")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
