package modelauth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestModelOAuthCredentialsAreEncryptedScopedAndRefreshable(t *testing.T) {
	var refreshCalls int
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		if got := r.Header.Get("x-grok-client-version"); got != grokProxyClientVersion {
			t.Errorf("x-grok-client-version = %q", got)
		}
		if got := r.Header.Get("x-grok-client-surface"); got != "headless" {
			t.Errorf("x-grok-client-surface = %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse refresh form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" {
			t.Errorf("unexpected refresh form: %#v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
			"id_token":      testJWT(`{"sub":"user-2","email":"new@example.com"}`),
		})
	}))
	defer refreshServer.Close()

	service, pool := newModelAuthIntegrationService(t, Config{HTTPClient: refreshServer.Client()})
	ctx := context.Background()
	workspaceID := uuid.New()
	otherWorkspaceID := uuid.New()
	for id, name := range map[uuid.UUID]string{
		workspaceID:      "Primary",
		otherWorkspaceID: "Other",
	} {
		if _, err := pool.Exec(ctx, "INSERT INTO workspaces (id, name) VALUES ($1, $2)", id, name); err != nil {
			t.Fatalf("create workspace %s: %v", name, err)
		}
	}

	if _, err := service.saveCredential(ctx, workspaceID, ProviderGrok, credentialPayload{
		AccessToken: "old-access", RefreshToken: "old-refresh",
		ClientID: xaiClientID, TokenURL: refreshServer.URL,
		UserID: "user-1", Email: "old@example.com",
	}, time.Now().Add(-time.Minute), "user-1", "old@example.com"); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	var ciphertext []byte
	if err := pool.QueryRow(ctx, `
		SELECT credential_ciphertext
		FROM model_oauth_credentials
		WHERE workspace_id=$1 AND provider=$2
	`, workspaceID, string(ProviderGrok)).Scan(&ciphertext); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	for _, secret := range []string{"old-access", "old-refresh"} {
		if bytes.Contains(ciphertext, []byte(secret)) {
			t.Fatalf("ciphertext contains plaintext %q", secret)
		}
	}

	config, err := service.ModelConfig(ctx, workspaceID, ProviderGrok, "grok-model", 10*time.Second)
	if err != nil {
		t.Fatalf("ModelConfig: %v", err)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	if config.APIKey != "new-access" {
		t.Fatalf("refreshed API key = %q", config.APIKey)
	}
	if config.Headers["x-userid"] != "user-2" || config.Headers["x-email"] != "new@example.com" {
		t.Fatalf("refreshed identity headers = %#v", config.Headers)
	}
	if config.Headers["X-XAI-Token-Auth"] != "xai-grok-cli" {
		t.Fatalf("missing Grok proxy auth header: %#v", config.Headers)
	}

	_, err = service.ModelConfig(ctx, otherWorkspaceID, ProviderGrok, "grok-model", 10*time.Second)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("cross-workspace credential lookup = %v, want ErrNotConnected", err)
	}

	var refreshedCiphertext []byte
	if err := pool.QueryRow(ctx, `
		SELECT credential_ciphertext
		FROM model_oauth_credentials
		WHERE workspace_id=$1 AND provider=$2
	`, workspaceID, string(ProviderGrok)).Scan(&refreshedCiphertext); err != nil {
		t.Fatalf("read refreshed ciphertext: %v", err)
	}
	for _, secret := range []string{"new-access", "new-refresh"} {
		if bytes.Contains(refreshedCiphertext, []byte(secret)) {
			t.Fatalf("refreshed ciphertext contains plaintext %q", secret)
		}
	}
}

func TestModelOAuthCredentialsSupportMultipleAccountsAndReconnectSameAccount(t *testing.T) {
	service, pool := newModelAuthIntegrationService(t, Config{})
	ctx := context.Background()
	workspaceID := uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO workspaces (id, name) VALUES ($1, $2)", workspaceID, "Multi account"); err != nil {
		t.Fatal(err)
	}

	firstID, err := service.saveCredential(ctx, workspaceID, ProviderGrok, credentialPayload{
		AccessToken: "first-access", RefreshToken: "first-refresh",
		ClientID: xaiClientID, TokenURL: "https://example.invalid/token",
		UserID: "user-a", Email: "a@example.com",
	}, time.Now().Add(time.Hour), "user-a", "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := service.saveCredential(ctx, workspaceID, ProviderGrok, credentialPayload{
		AccessToken: "second-access", RefreshToken: "second-refresh",
		ClientID: xaiClientID, TokenURL: "https://example.invalid/token",
		UserID: "user-b", Email: "b@example.com",
	}, time.Now().Add(time.Hour), "user-b", "b@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID {
		t.Fatal("different accounts reused one credential id")
	}

	reconnectedID, err := service.saveCredential(ctx, workspaceID, ProviderGrok, credentialPayload{
		AccessToken: "first-access-new", RefreshToken: "first-refresh-new",
		ClientID: xaiClientID, TokenURL: "https://example.invalid/token",
		UserID: "user-a", Email: "a-new@example.com",
	}, time.Now().Add(2*time.Hour), "user-a", "a-new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if reconnectedID != firstID {
		t.Fatalf("reconnect id=%s, want original %s", reconnectedID, firstID)
	}

	status, err := service.Status(ctx, workspaceID, ProviderGrok)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Connected || len(status.Accounts) != 2 {
		t.Fatalf("status=%+v, want two connected accounts", status)
	}
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM model_oauth_credentials WHERE workspace_id=$1 AND provider=$2",
		workspaceID, string(ProviderGrok),
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("credential count=%d, want 2", count)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO workspace_model_routes (
			id, workspace_id, position, enabled, provider_type, credential_id, model
		)
		VALUES ($1, $2, 0, true, 'grok', $3, 'grok-test')
	`, uuid.New(), workspaceID, firstID); err != nil {
		t.Fatal(err)
	}
	if err := service.Disconnect(ctx, workspaceID, ProviderGrok, firstID); !errors.Is(err, ErrCredentialInUse) {
		t.Fatalf("disconnect routed account error=%v, want ErrCredentialInUse", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM workspace_model_routes WHERE workspace_id=$1", workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := service.Disconnect(ctx, workspaceID, ProviderGrok, firstID); err != nil {
		t.Fatalf("disconnect unused account: %v", err)
	}
}

func newModelAuthIntegrationService(t *testing.T, config Config) (*Service, *pgxpool.Pool) {
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

	schema := "modelauth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

	if config.SecretKey == "" {
		config.SecretKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x73}, 32))
	}
	service, err := New(pool, config)
	if err != nil {
		t.Fatalf("create model auth service: %v", err)
	}
	return service, pool
}

func testJWT(payload string) string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".sig"
}
