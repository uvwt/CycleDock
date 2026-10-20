package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

const ScopeMCP = "cycledock:mcp"

var (
	ErrInvalidConfig       = errors.New("invalid oauth configuration")
	ErrInvalidRequest      = errors.New("invalid oauth request")
	ErrInvalidClient       = errors.New("invalid oauth client")
	ErrInvalidGrant        = errors.New("invalid oauth grant")
	ErrInvalidToken        = errors.New("invalid oauth token")
	ErrInvalidScope        = errors.New("invalid oauth scope")
	ErrInvalidResource     = errors.New("invalid oauth resource")
	ErrUnsupportedGrant    = errors.New("unsupported oauth grant type")
	ErrAuthorizationDenied = errors.New("oauth authorization denied")
)

type Config struct {
	PublicURL  string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	CodeTTL    time.Duration
}

type Service struct {
	pool       *pgxpool.Pool
	queries    *db.Queries
	issuer     string
	resource   string
	accessTTL  time.Duration
	refreshTTL time.Duration
	codeTTL    time.Duration
}

type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
	CreatedAt    time.Time
}

type AuthorizeRequest struct {
	ClientID      string
	RedirectURI   string
	CodeChallenge string
	Scope         string
	Resource      string
}

type AuthorizationCode struct {
	Code        string
	Client      Client
	Scope       string
	Resource    string
	RedirectURI string
	OAuthSource uuid.UUID
	ExpiresAt   time.Time
}

type CodeTokenRequest struct {
	Code         string
	ClientID     string
	RedirectURI  string
	CodeVerifier string
	Resource     string
}

type RefreshTokenRequest struct {
	RefreshToken string
	ClientID     string
	Resource     string
}

type TokenSet struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	Scope        string
	Resource     string
	ExpiresAt    time.Time
}

type TokenIdentity struct {
	WorkspaceID uuid.UUID
	SourceID    uuid.UUID
	ClientID    string
	ClientName  string
	Scope       string
	Resource    string
	ExpiresAt   time.Time
}

func New(pool *pgxpool.Pool, config Config) (*Service, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: database pool is required", ErrInvalidConfig)
	}
	publicURL, err := normalizeBaseURL(config.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("%w: public URL: %v", ErrInvalidConfig, err)
	}
	if config.AccessTTL <= 0 {
		config.AccessTTL = time.Hour
	}
	if config.RefreshTTL <= 0 {
		config.RefreshTTL = 30 * 24 * time.Hour
	}
	if config.CodeTTL <= 0 {
		config.CodeTTL = 5 * time.Minute
	}
	if config.RefreshTTL <= config.AccessTTL {
		return nil, fmt.Errorf("%w: refresh token lifetime must exceed access token lifetime", ErrInvalidConfig)
	}
	return &Service{
		pool:       pool,
		queries:    db.New(pool),
		issuer:     publicURL,
		resource:   publicURL + "/mcp",
		accessTTL:  config.AccessTTL,
		refreshTTL: config.RefreshTTL,
		codeTTL:    config.CodeTTL,
	}, nil
}

func (s *Service) Issuer() string   { return s.issuer }
func (s *Service) Resource() string { return s.resource }

func (s *Service) RegisterClient(ctx context.Context, name string, redirectURIs []string) (Client, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return Client{}, fmt.Errorf("%w: client_name must contain 1-100 characters", ErrInvalidRequest)
	}
	if len(redirectURIs) == 0 || len(redirectURIs) > 20 {
		return Client{}, fmt.Errorf("%w: redirect_uris must contain 1-20 URLs", ErrInvalidRequest)
	}

	normalized := make([]string, 0, len(redirectURIs))
	seen := make(map[string]struct{}, len(redirectURIs))
	for _, raw := range redirectURIs {
		redirectURI, err := validateRedirectURI(raw)
		if err != nil {
			return Client{}, err
		}
		if _, exists := seen[redirectURI]; exists {
			return Client{}, fmt.Errorf("%w: duplicate redirect_uri", ErrInvalidRequest)
		}
		seen[redirectURI] = struct{}{}
		normalized = append(normalized, redirectURI)
	}

	clientID, err := randomOpaque("client_", 24)
	if err != nil {
		return Client{}, fmt.Errorf("generate oauth client id: %w", err)
	}
	row, err := s.queries.CreateOAuthClient(ctx, db.CreateOAuthClientParams{
		ClientID: clientID, ClientName: name, RedirectUris: normalized,
	})
	if err != nil {
		return Client{}, fmt.Errorf("create oauth client: %w", err)
	}
	return Client{
		ID: row.ClientID, Name: row.ClientName,
		RedirectURIs: append([]string(nil), row.RedirectUris...),
		CreatedAt:    row.CreatedAt.Time,
	}, nil
}

func (s *Service) GetClient(ctx context.Context, clientID string) (Client, error) {
	row, err := s.queries.GetOAuthClient(ctx, strings.TrimSpace(clientID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, ErrInvalidClient
	}
	if err != nil {
		return Client{}, fmt.Errorf("load oauth client: %w", err)
	}
	return Client{
		ID: row.ClientID, Name: row.ClientName,
		RedirectURIs: append([]string(nil), row.RedirectUris...),
		CreatedAt:    row.CreatedAt.Time,
	}, nil
}

func (s *Service) ValidateAuthorizationRequest(ctx context.Context, request AuthorizeRequest) (Client, error) {
	client, err := s.GetClient(ctx, request.ClientID)
	if err != nil {
		return Client{}, err
	}
	if !contains(client.RedirectURIs, request.RedirectURI) {
		return Client{}, fmt.Errorf("%w: redirect_uri is not registered", ErrInvalidRequest)
	}
	if request.Scope == "" {
		request.Scope = ScopeMCP
	}
	if request.Scope != ScopeMCP {
		return Client{}, ErrInvalidScope
	}
	if request.Resource != s.resource {
		return Client{}, ErrInvalidResource
	}
	if err := validateCodeChallenge(request.CodeChallenge); err != nil {
		return Client{}, err
	}
	return client, nil
}

func (s *Service) Authorize(
	ctx context.Context,
	human auth.Identity,
	request AuthorizeRequest,
) (AuthorizationCode, error) {
	client, err := s.ValidateAuthorizationRequest(ctx, request)
	if err != nil {
		return AuthorizationCode{}, err
	}
	if request.Scope == "" {
		request.Scope = ScopeMCP
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AuthorizationCode{}, fmt.Errorf("begin oauth authorization: %w", err)
	}
	defer tx.Rollback(ctx)

	// 同一 Workspace + OAuth Client 的授权串行化。这样重复授权只复用一条
	// oauth_client Source，不会因为并发浏览器批准留下孤立 Source。
	lockKey := "cycledock/oauth/" + human.WorkspaceID.String() + "/" + client.ID
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return AuthorizationCode{}, fmt.Errorf("lock oauth authorization: %w", err)
	}

	q := db.New(tx)
	membership, err := q.GetOAuthHumanMembership(ctx, db.GetOAuthHumanMembershipParams{
		WorkspaceID: human.WorkspaceID, HumanAccountID: human.HumanAccountID, SourceID: human.SourceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationCode{}, ErrAuthorizationDenied
	}
	if err != nil {
		return AuthorizationCode{}, fmt.Errorf("load authorizing membership: %w", err)
	}
	if membership.SourceKind != string(domain.SourceKindHuman) {
		return AuthorizationCode{}, ErrAuthorizationDenied
	}

	grantRow, err := q.GetOAuthGrantForUpdate(ctx, db.GetOAuthGrantForUpdateParams{
		WorkspaceID: human.WorkspaceID, ClientID: client.ID, Resource: s.resource,
	})
	var grant db.OauthGrant
	var oauthSourceID uuid.UUID
	switch {
	case err == nil:
		if grantRow.SourceKind != string(domain.SourceKindOAuthClient) {
			return AuthorizationCode{}, fmt.Errorf("%w: grant source is not oauth_client", ErrInvalidGrant)
		}
		oauthSourceID = grantRow.SourceID
		if err := q.UpdateOAuthSourceName(ctx, db.UpdateOAuthSourceNameParams{
			Name: client.Name, WorkspaceID: human.WorkspaceID, ID: oauthSourceID,
		}); err != nil {
			return AuthorizationCode{}, fmt.Errorf("refresh oauth source name: %w", err)
		}
		grant, err = q.RefreshOAuthGrantApproval(ctx, db.RefreshOAuthGrantApprovalParams{
			HumanAccountID: human.HumanAccountID,
			HumanSourceID:  human.SourceID,
			Scope:          request.Scope,
			ID:             grantRow.ID,
		})
		if err != nil {
			return AuthorizationCode{}, fmt.Errorf("refresh oauth grant approval: %w", err)
		}
	case errors.Is(err, pgx.ErrNoRows):
		source, createErr := q.CreateSource(ctx, db.CreateSourceParams{
			ID: uuid.New(), WorkspaceID: human.WorkspaceID,
			Kind: string(domain.SourceKindOAuthClient), Name: client.Name,
		})
		if createErr != nil {
			return AuthorizationCode{}, fmt.Errorf("create oauth source: %w", createErr)
		}
		oauthSourceID = source.ID
		grant, err = q.CreateOAuthGrant(ctx, db.CreateOAuthGrantParams{
			ID: uuid.New(), WorkspaceID: human.WorkspaceID,
			HumanAccountID: human.HumanAccountID, HumanSourceID: human.SourceID,
			ClientID: client.ID, SourceID: source.ID, Resource: s.resource, Scope: request.Scope,
		})
		if err != nil {
			return AuthorizationCode{}, fmt.Errorf("create oauth grant: %w", err)
		}
	default:
		return AuthorizationCode{}, fmt.Errorf("load oauth grant: %w", err)
	}

	rawCode, err := randomOpaque("code_", 32)
	if err != nil {
		return AuthorizationCode{}, fmt.Errorf("generate authorization code: %w", err)
	}
	codeDigest := sha256.Sum256([]byte(rawCode))
	expiresAt := time.Now().UTC().Add(s.codeTTL)
	if err := q.CreateOAuthAuthorizationCode(ctx, db.CreateOAuthAuthorizationCodeParams{
		CodeDigest: codeDigest[:], GrantID: grant.ID,
		RedirectUri: request.RedirectURI, CodeChallenge: request.CodeChallenge,
		Scope: request.Scope, Resource: s.resource,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		return AuthorizationCode{}, fmt.Errorf("create oauth authorization code: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AuthorizationCode{}, fmt.Errorf("commit oauth authorization: %w", err)
	}
	return AuthorizationCode{
		Code: rawCode, Client: client, Scope: request.Scope, Resource: s.resource,
		RedirectURI: request.RedirectURI, OAuthSource: oauthSourceID, ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) ExchangeCode(ctx context.Context, request CodeTokenRequest) (TokenSet, error) {
	if request.Code == "" || request.ClientID == "" || request.RedirectURI == "" || request.CodeVerifier == "" {
		return TokenSet{}, ErrInvalidRequest
	}
	if request.Resource != s.resource {
		return TokenSet{}, ErrInvalidResource
	}
	if !validCodeVerifier(request.CodeVerifier) {
		return TokenSet{}, ErrInvalidGrant
	}

	codeDigest := sha256.Sum256([]byte(request.Code))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TokenSet{}, fmt.Errorf("begin authorization code exchange: %w", err)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	code, err := q.GetOAuthAuthorizationCodeForUpdate(ctx, codeDigest[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenSet{}, ErrInvalidGrant
	}
	if err != nil {
		return TokenSet{}, fmt.Errorf("load authorization code: %w", err)
	}
	now := time.Now().UTC()
	if code.ConsumedAt.Valid || code.GrantRevokedAt.Valid || !code.ExpiresAt.Valid || !code.ExpiresAt.Time.After(now) ||
		code.ClientID != request.ClientID || code.RedirectUri != request.RedirectURI ||
		code.Resource != s.resource || code.Scope != ScopeMCP {
		return TokenSet{}, ErrInvalidGrant
	}
	expectedChallenge := pkceChallenge(request.CodeVerifier)
	if subtle.ConstantTimeCompare([]byte(expectedChallenge), []byte(code.CodeChallenge)) != 1 {
		return TokenSet{}, ErrInvalidGrant
	}
	affected, err := q.ConsumeOAuthAuthorizationCode(ctx, codeDigest[:])
	if err != nil {
		return TokenSet{}, fmt.Errorf("consume authorization code: %w", err)
	}
	if affected != 1 {
		return TokenSet{}, ErrInvalidGrant
	}

	tokens, err := s.issueTokenSet(ctx, q, code.GrantID, uuid.New(), code.Scope, code.Resource, now)
	if err != nil {
		return TokenSet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TokenSet{}, fmt.Errorf("commit authorization code exchange: %w", err)
	}
	return tokens, nil
}

func (s *Service) Refresh(ctx context.Context, request RefreshTokenRequest) (TokenSet, error) {
	if request.RefreshToken == "" || request.ClientID == "" {
		return TokenSet{}, ErrInvalidRequest
	}
	if request.Resource != "" && request.Resource != s.resource {
		return TokenSet{}, ErrInvalidResource
	}

	refreshDigest := sha256.Sum256([]byte(request.RefreshToken))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TokenSet{}, fmt.Errorf("begin refresh token exchange: %w", err)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	current, err := q.GetOAuthRefreshTokenForUpdate(ctx, refreshDigest[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenSet{}, ErrInvalidGrant
	}
	if err != nil {
		return TokenSet{}, fmt.Errorf("load refresh token: %w", err)
	}
	now := time.Now().UTC()

	// 轮换后的 refresh token 被再次使用说明旧 token 已泄漏或客户端错误重放。
	// 撤销整个 token family，避免攻击者和合法客户端继续竞速换取新 access token。
	if current.TokenRevokedAt.Valid {
		if err := q.RevokeOAuthTokenFamily(ctx, current.FamilyID); err != nil {
			return TokenSet{}, fmt.Errorf("revoke replayed token family: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return TokenSet{}, fmt.Errorf("commit replay protection: %w", err)
		}
		return TokenSet{}, ErrInvalidGrant
	}
	if current.GrantRevokedAt.Valid || !current.RefreshExpiresAt.Valid || !current.RefreshExpiresAt.Time.After(now) ||
		current.ClientID != request.ClientID || current.Resource != s.resource || current.Scope != ScopeMCP {
		return TokenSet{}, ErrInvalidGrant
	}

	affected, err := q.RevokeOAuthToken(ctx, current.TokenID)
	if err != nil {
		return TokenSet{}, fmt.Errorf("revoke rotated oauth token: %w", err)
	}
	if affected != 1 {
		return TokenSet{}, ErrInvalidGrant
	}
	tokens, err := s.issueTokenSet(ctx, q, current.GrantID, current.FamilyID, current.Scope, current.Resource, now)
	if err != nil {
		return TokenSet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TokenSet{}, fmt.Errorf("commit refresh token rotation: %w", err)
	}
	return tokens, nil
}

func (s *Service) AuthenticateAccessToken(ctx context.Context, rawToken string) (TokenIdentity, error) {
	if rawToken == "" {
		return TokenIdentity{}, ErrInvalidToken
	}
	digest := sha256.Sum256([]byte(rawToken))
	row, err := s.queries.GetOAuthAccessTokenIdentity(ctx, digest[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenIdentity{}, ErrInvalidToken
	}
	if err != nil {
		return TokenIdentity{}, fmt.Errorf("load oauth access token: %w", err)
	}
	if row.Resource != s.resource || row.Scope != ScopeMCP || !row.AccessExpiresAt.Valid {
		return TokenIdentity{}, ErrInvalidToken
	}
	return TokenIdentity{
		WorkspaceID: row.WorkspaceID, SourceID: row.SourceID,
		ClientID: row.ClientID, ClientName: row.ClientName,
		Scope: row.Scope, Resource: row.Resource, ExpiresAt: row.AccessExpiresAt.Time,
	}, nil
}

func (s *Service) issueTokenSet(
	ctx context.Context,
	q *db.Queries,
	grantID, familyID uuid.UUID,
	scope, resource string,
	now time.Time,
) (TokenSet, error) {
	accessToken, err := randomOpaque("access_", 32)
	if err != nil {
		return TokenSet{}, fmt.Errorf("generate access token: %w", err)
	}
	refreshToken, err := randomOpaque("refresh_", 40)
	if err != nil {
		return TokenSet{}, fmt.Errorf("generate refresh token: %w", err)
	}
	accessDigest := sha256.Sum256([]byte(accessToken))
	refreshDigest := sha256.Sum256([]byte(refreshToken))
	accessExpiresAt := now.Add(s.accessTTL)
	refreshExpiresAt := now.Add(s.refreshTTL)
	if err := q.CreateOAuthToken(ctx, db.CreateOAuthTokenParams{
		ID: uuid.New(), GrantID: grantID, FamilyID: familyID,
		AccessTokenDigest: accessDigest[:], RefreshTokenDigest: refreshDigest[:],
		AccessExpiresAt:  pgtype.Timestamptz{Time: accessExpiresAt, Valid: true},
		RefreshExpiresAt: pgtype.Timestamptz{Time: refreshExpiresAt, Valid: true},
	}); err != nil {
		return TokenSet{}, fmt.Errorf("create oauth token: %w", err)
	}
	return TokenSet{
		AccessToken: accessToken, RefreshToken: refreshToken, TokenType: "Bearer",
		Scope: scope, Resource: resource, ExpiresAt: accessExpiresAt,
	}, nil
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return "", errors.New("must be an absolute URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("scheme must be http or https")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", errors.New("must not contain user info, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("must not contain a path")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return "", errors.New("http is only allowed for localhost or loopback addresses")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validateRedirectURI(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return "", fmt.Errorf("%w: redirect_uri must be an absolute URL", ErrInvalidRequest)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: redirect_uri cannot contain user info or fragment", ErrInvalidRequest)
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(parsed.Hostname()) {
			return "", fmt.Errorf("%w: insecure redirect_uri must use a loopback host", ErrInvalidRequest)
		}
	default:
		return "", fmt.Errorf("%w: redirect_uri must use https or loopback http", ErrInvalidRequest)
	}
	return parsed.String(), nil
}

func isLoopbackHost(raw string) bool {
	host := strings.ToLower(strings.TrimSpace(raw))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateCodeChallenge(challenge string) error {
	if len(challenge) < 43 || len(challenge) > 128 || strings.Contains(challenge, "=") {
		return fmt.Errorf("%w: code_challenge must be S256 base64url", ErrInvalidRequest)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(challenge)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("%w: code_challenge must be S256 base64url", ErrInvalidRequest)
	}
	return nil
}

func validCodeVerifier(verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, r := range verifier {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '.' || r == '_' || r == '~' {
			continue
		}
		return false
	}
	return true
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomOpaque(prefix string, bytesCount int) (string, error) {
	value := make([]byte, bytesCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(value), nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
