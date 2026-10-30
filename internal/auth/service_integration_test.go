package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
)

func TestOIDCBeginUsesPKCES256NonceAndOneTimeHashedState(t *testing.T) {
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                provider.URL,
			"authorization_endpoint":                provider.URL + "/authorize",
			"token_endpoint":                        provider.URL + "/token",
			"jwks_uri":                              provider.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	}))
	defer provider.Close()

	service, pool := newAuthIntegrationService(t, Config{OIDC: OIDCConfig{
		Issuer: provider.URL, ClientID: "cycledock-test", PublicURL: "http://localhost:8080",
	}})
	start, err := service.BeginOIDCLogin(context.Background())
	if err != nil {
		t.Fatalf("begin OIDC login: %v", err)
	}
	redirect, err := url.Parse(start.URL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	query := redirect.Query()
	if query.Get("state") != start.State ||
		query.Get("code_challenge_method") != "S256" ||
		query.Get("code_challenge") == "" ||
		query.Get("nonce") == "" {
		t.Fatalf("OIDC authorize request missing state/PKCE/nonce: %s", start.URL)
	}

	var digest []byte
	var verifier, nonce string
	if err := pool.QueryRow(context.Background(),
		"SELECT state_digest, code_verifier, nonce FROM web_auth_flows",
	).Scan(&digest, &verifier, &nonce); err != nil {
		t.Fatalf("load OIDC flow: %v", err)
	}
	wantDigest := sha256.Sum256([]byte(start.State))
	if !bytes.Equal(digest, wantDigest[:]) || bytes.Equal(digest, []byte(start.State)) {
		t.Fatal("OIDC flow did not store the SHA-256 state digest")
	}
	if verifier == "" || nonce == "" || nonce != query.Get("nonce") {
		t.Fatalf("stored OIDC verifier/nonce mismatch: verifier=%q nonce=%q", verifier, nonce)
	}
	if err := service.ConsumeOIDCState(context.Background(), start.State); err != nil {
		t.Fatalf("consume OIDC state: %v", err)
	}
	if err := service.ConsumeOIDCState(context.Background(), start.State); !errors.Is(err, ErrInvalidOIDCState) {
		t.Fatalf("OIDC state was reusable: %v", err)
	}
}

func TestSelfHostedSessionStoresDigestAndAuthenticates(t *testing.T) {
	service, pool := newAuthIntegrationService(t, Config{AdminPassword: "local-admin-password"})
	ctx := context.Background()

	if _, err := service.Login(ctx, "wrong-password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password: got %v want ErrUnauthorized", err)
	}
	session, err := service.Login(ctx, "local-admin-password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if session.Token == "" || session.Identity.WorkspaceID == uuid.Nil || session.Identity.SourceID == uuid.Nil {
		t.Fatalf("invalid session: %+v", session)
	}

	var digest []byte
	if err := pool.QueryRow(ctx, "SELECT token_digest FROM web_sessions").Scan(&digest); err != nil {
		t.Fatalf("load stored session digest: %v", err)
	}
	if len(digest) != 32 || bytes.Equal(digest, []byte(session.Token)) {
		t.Fatalf("web session must store SHA-256 digest only: len=%d", len(digest))
	}

	identity, err := service.Authenticate(ctx, session.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if identity.HumanAccountID != session.Identity.HumanAccountID ||
		identity.WorkspaceID != session.Identity.WorkspaceID ||
		identity.SourceID != session.Identity.SourceID {
		t.Fatalf("authenticated identity changed: login=%+v auth=%+v", session.Identity, identity)
	}

	if err := service.Logout(ctx, session.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("session remained valid after logout: %v", err)
	}
}

func TestSelfHostedLoginReusesAdminWithoutMembership(t *testing.T) {
	for _, keepWorkspace := range []bool{false, true} {
		name := "workspace deleted"
		if keepWorkspace {
			name = "membership deleted"
		}
		t.Run(name, func(t *testing.T) {
			service, pool := newAuthIntegrationService(t, Config{AdminPassword: "local-admin-password"})
			ctx := context.Background()
			original, err := service.EnsureSelfHostedIdentity(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// 重现管理员仍在，但完整身份 JOIN 已无法查到的状态。
			if keepWorkspace {
				_, err = pool.Exec(ctx, "DELETE FROM workspace_memberships WHERE human_account_id=$1", original.HumanAccountID)
			} else {
				_, err = pool.Exec(ctx, "DELETE FROM workspaces WHERE id=$1", original.WorkspaceID)
			}
			if err != nil {
				t.Fatal(err)
			}
			session, err := service.Login(ctx, "local-admin-password")
			if err != nil {
				t.Fatalf("login with orphaned local admin: %v", err)
			}
			if session.Identity.HumanAccountID != original.HumanAccountID {
				t.Fatal("login replaced the existing admin account")
			}
			if keepWorkspace && session.Identity.WorkspaceID != original.WorkspaceID {
				t.Fatal("login replaced the existing workspace")
			}
			if _, err := service.Authenticate(ctx, session.Token); err != nil {
				t.Fatalf("authenticate repaired membership: %v", err)
			}
			repeated, err := service.EnsureSelfHostedIdentity(ctx)
			if err != nil || repeated != session.Identity {
				t.Fatalf("repeated bootstrap changed identity: %+v %v", repeated, err)
			}
			assertAuthTableCount(t, pool, "human_accounts", 1)
			assertAuthTableCount(t, pool, "workspace_memberships", 1)
			assertAuthTableCount(t, pool, "workspaces", 1)
		})
	}
}

func TestOIDCPersonalIdentityIsIdempotentAndIsolated(t *testing.T) {
	service, pool := newAuthIntegrationService(t, Config{})
	ctx := context.Background()
	issuer := "https://nexusdock.example"

	first, err := service.ensureOIDCPersonalIdentity(ctx, issuer, "user-a", "User A", "a@example.com")
	if err != nil {
		t.Fatalf("provision user A: %v", err)
	}
	repeated, err := service.ensureOIDCPersonalIdentity(ctx, issuer, "user-a", "Renamed A", "a2@example.com")
	if err != nil {
		t.Fatalf("resolve user A again: %v", err)
	}
	if repeated.HumanAccountID != first.HumanAccountID ||
		repeated.WorkspaceID != first.WorkspaceID ||
		repeated.SourceID != first.SourceID {
		t.Fatalf("same issuer+sub created a second identity: first=%+v repeated=%+v", first, repeated)
	}
	if repeated.DisplayName != "Renamed A" || repeated.SourceName != "Renamed A" || repeated.Email != "a2@example.com" {
		t.Fatalf("OIDC profile snapshot was not refreshed: %+v", repeated)
	}

	second, err := service.ensureOIDCPersonalIdentity(ctx, issuer, "user-b", "User B", "b@example.com")
	if err != nil {
		t.Fatalf("provision user B: %v", err)
	}
	if second.HumanAccountID == first.HumanAccountID ||
		second.WorkspaceID == first.WorkspaceID ||
		second.SourceID == first.SourceID {
		t.Fatalf("different subjects shared identity scope: first=%+v second=%+v", first, second)
	}

	assertAuthTableCount(t, pool, "human_accounts", 2)
	assertAuthTableCount(t, pool, "oidc_identities", 2)
	assertAuthTableCount(t, pool, "workspace_memberships", 2)
	assertAuthTableCount(t, pool, "workspaces", 2)
	assertAuthTableCount(t, pool, "sources", 2)
}

func TestOIDCPersonalIdentityConcurrentFirstLoginCreatesOneScope(t *testing.T) {
	service, pool := newAuthIntegrationService(t, Config{})
	ctx := context.Background()
	const callers = 8

	type result struct {
		identity Identity
		err      error
	}
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			identity, err := service.ensureOIDCPersonalIdentity(
				ctx, "https://nexusdock.example", "concurrent-user", "Concurrent User", "",
			)
			results <- result{identity: identity, err: err}
		}()
	}
	wg.Wait()
	close(results)

	var expected Identity
	for item := range results {
		if item.err != nil {
			t.Fatalf("concurrent first login failed: %v", item.err)
		}
		if expected.HumanAccountID == uuid.Nil {
			expected = item.identity
			continue
		}
		if item.identity.HumanAccountID != expected.HumanAccountID ||
			item.identity.WorkspaceID != expected.WorkspaceID ||
			item.identity.SourceID != expected.SourceID {
			t.Fatalf("concurrent login returned different identity: expected=%+v got=%+v", expected, item.identity)
		}
	}

	assertAuthTableCount(t, pool, "human_accounts", 1)
	assertAuthTableCount(t, pool, "oidc_identities", 1)
	assertAuthTableCount(t, pool, "workspace_memberships", 1)
	assertAuthTableCount(t, pool, "workspaces", 1)
	assertAuthTableCount(t, pool, "sources", 1)
}

func newAuthIntegrationService(t *testing.T, config Config) (*Service, *pgxpool.Pool) {
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

	schema := "auth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(context.Background(), "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create integration schema: %v", err)
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
		t.Fatalf("open migration database: %v", err)
	}
	t.Cleanup(func() { _ = migrationDB.Close() })

	_, filename, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(migrationDB, migrations); err != nil {
		t.Fatalf("migrate integration schema: %v", err)
	}

	pool, err := pgxpool.New(context.Background(), scopedURL)
	if err != nil {
		t.Fatalf("connect integration schema: %v", err)
	}
	t.Cleanup(pool.Close)

	service, err := New(pool, config)
	if err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	return service, pool
}

func assertAuthTableCount(t *testing.T, pool *pgxpool.Pool, table string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count: got %d want %d", table, got, want)
	}
}
