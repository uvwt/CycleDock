package oauth

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/auth"
)

func TestOAuthAuthorizationCodePKCEAndRefreshReplayProtection(t *testing.T) {
	service, humanAuth, pool := newOAuthIntegration(t)
	ctx := context.Background()
	session, err := humanAuth.Login(ctx, "phase3-local-admin-password")
	if err != nil {
		t.Fatalf("human login: %v", err)
	}
	client, err := service.RegisterClient(ctx, "External Agent", []string{"http://127.0.0.1:9876/callback"})
	if err != nil {
		t.Fatalf("register client: %v", err)
	}

	verifier := strings.Repeat("a", 64)
	request := AuthorizeRequest{
		ClientID: client.ID, RedirectURI: client.RedirectURIs[0],
		CodeChallenge: pkceChallenge(verifier), Scope: ScopeMCP, Resource: service.Resource(),
	}
	code, err := service.Authorize(ctx, session.Identity, request)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if code.Code == "" || code.OAuthSource == uuid.Nil {
		t.Fatalf("invalid authorization code result: %+v", code)
	}

	var storedCodeDigest []byte
	if err := pool.QueryRow(ctx, "SELECT code_digest FROM oauth_authorization_codes").Scan(&storedCodeDigest); err != nil {
		t.Fatalf("read code digest: %v", err)
	}
	if len(storedCodeDigest) != 32 || bytes.Equal(storedCodeDigest, []byte(code.Code)) {
		t.Fatal("authorization code was not stored as SHA-256 digest")
	}

	if _, err := service.ExchangeCode(ctx, CodeTokenRequest{
		Code: code.Code, ClientID: client.ID, RedirectURI: client.RedirectURIs[0],
		CodeVerifier: strings.Repeat("b", 64), Resource: service.Resource(),
	}); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("wrong PKCE verifier: %v", err)
	}

	tokens, err := service.ExchangeCode(ctx, CodeTokenRequest{
		Code: code.Code, ClientID: client.ID, RedirectURI: client.RedirectURIs[0],
		CodeVerifier: verifier, Resource: service.Resource(),
	})
	if err != nil {
		t.Fatalf("exchange code: %v", err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("missing OAuth tokens: %+v", tokens)
	}
	if _, err := service.ExchangeCode(ctx, CodeTokenRequest{
		Code: code.Code, ClientID: client.ID, RedirectURI: client.RedirectURIs[0],
		CodeVerifier: verifier, Resource: service.Resource(),
	}); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("authorization code was reusable: %v", err)
	}

	var accessDigest, refreshDigest []byte
	if err := pool.QueryRow(ctx, "SELECT access_token_digest, refresh_token_digest FROM oauth_tokens").Scan(&accessDigest, &refreshDigest); err != nil {
		t.Fatalf("read token digests: %v", err)
	}
	if len(accessDigest) != 32 || len(refreshDigest) != 32 ||
		bytes.Equal(accessDigest, []byte(tokens.AccessToken)) || bytes.Equal(refreshDigest, []byte(tokens.RefreshToken)) {
		t.Fatal("OAuth tokens were not stored as SHA-256 digests")
	}

	identity, err := service.AuthenticateAccessToken(ctx, tokens.AccessToken)
	if err != nil {
		t.Fatalf("authenticate access token: %v", err)
	}
	if identity.WorkspaceID != session.Identity.WorkspaceID || identity.SourceID != code.OAuthSource {
		t.Fatalf("token identity scope changed: %+v", identity)
	}

	rotated, err := service.Refresh(ctx, RefreshTokenRequest{
		RefreshToken: tokens.RefreshToken, ClientID: client.ID, Resource: service.Resource(),
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rotated.AccessToken == tokens.AccessToken || rotated.RefreshToken == tokens.RefreshToken {
		t.Fatal("refresh did not rotate both tokens")
	}
	if _, err := service.AuthenticateAccessToken(ctx, tokens.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("old access token remained valid after rotation: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(ctx, rotated.AccessToken); err != nil {
		t.Fatalf("rotated access token invalid: %v", err)
	}

	if _, err := service.Refresh(ctx, RefreshTokenRequest{
		RefreshToken: tokens.RefreshToken, ClientID: client.ID, Resource: service.Resource(),
	}); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("replayed refresh token was accepted: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(ctx, rotated.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("refresh replay did not revoke token family: %v", err)
	}
}

func TestConcurrentOAuthApprovalReusesOneSource(t *testing.T) {
	service, humanAuth, pool := newOAuthIntegration(t)
	ctx := context.Background()
	session, err := humanAuth.Login(ctx, "phase3-local-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	client, err := service.RegisterClient(ctx, "Concurrent Agent", []string{"http://localhost:9988/callback"})
	if err != nil {
		t.Fatal(err)
	}
	request := AuthorizeRequest{
		ClientID: client.ID, RedirectURI: client.RedirectURIs[0],
		CodeChallenge: pkceChallenge(strings.Repeat("c", 64)),
		Scope:         ScopeMCP, Resource: service.Resource(),
	}

	const callers = 8
	type result struct {
		code AuthorizationCode
		err  error
	}
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, err := service.Authorize(ctx, session.Identity, request)
			results <- result{code: code, err: err}
		}()
	}
	wg.Wait()
	close(results)

	var sourceID uuid.UUID
	for item := range results {
		if item.err != nil {
			t.Fatalf("concurrent authorization failed: %v", item.err)
		}
		if sourceID == uuid.Nil {
			sourceID = item.code.OAuthSource
		} else if item.code.OAuthSource != sourceID {
			t.Fatalf("concurrent approval created multiple OAuth sources: %s vs %s", sourceID, item.code.OAuthSource)
		}
	}
	var sourceCount, grantCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sources WHERE kind='oauth_client'").Scan(&sourceCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM oauth_grants").Scan(&grantCount); err != nil {
		t.Fatal(err)
	}
	if sourceCount != 1 || grantCount != 1 {
		t.Fatalf("approval scope duplication: sources=%d grants=%d", sourceCount, grantCount)
	}
}

func TestOAuthRejectsUnsafeRedirectURI(t *testing.T) {
	service, _, _ := newOAuthIntegration(t)
	if _, err := service.RegisterClient(context.Background(), "Unsafe", []string{"http://example.com/callback"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsafe redirect accepted: %v", err)
	}
}

func newOAuthIntegration(t *testing.T) (*Service, *auth.Service, *pgxpool.Pool) {
	t.Helper()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	adminPool, err := pgxpool.New(context.Background(), baseURL)
	if err != nil {
		t.Fatalf("connect integration database: %v", err)
	}
	t.Cleanup(adminPool.Close)

	schema := "oauth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(context.Background(), "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
	})

	query := parsedURL.Query()
	query.Set("search_path", schema)
	parsedURL.RawQuery = query.Encode()
	scopedURL := parsedURL.String()

	migrationDB, err := sql.Open("pgx", scopedURL)
	if err != nil {
		t.Fatalf("open migration db: %v", err)
	}
	t.Cleanup(func() { _ = migrationDB.Close() })
	_, filename, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(migrationDB, migrations); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	pool, err := pgxpool.New(context.Background(), scopedURL)
	if err != nil {
		t.Fatalf("connect scoped db: %v", err)
	}
	t.Cleanup(pool.Close)

	humanAuth, err := auth.New(pool, auth.Config{AdminPassword: "phase3-local-admin-password"})
	if err != nil {
		t.Fatalf("create human auth: %v", err)
	}
	service, err := New(pool, Config{PublicURL: "http://127.0.0.1:18083"})
	if err != nil {
		t.Fatalf("create oauth service: %v", err)
	}
	return service, humanAuth, pool
}
