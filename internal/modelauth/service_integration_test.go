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

	if err := service.saveCredential(ctx, workspaceID, ProviderGrok, credentialPayload{
		AccessToken: "old-access", RefreshToken: "old-refresh",
		ClientID: xaiClientID, TokenURL: refreshServer.URL,
		UserID: "user-1", Email: "old@example.com",
	}, time.Now().Add(-time.Minute)); err != nil {
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
