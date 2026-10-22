package store

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestModelRoutesMigrationBackfillsLegacySettingsAndCredential(t *testing.T) {
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

	schema := "modelroutes_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	if err := goose.UpTo(migrationDB, migrations, 7); err != nil {
		t.Fatalf("migrate v7 schema: %v", err)
	}

	workspaceID := uuid.New()
	sourceID := uuid.New()
	if _, err := migrationDB.Exec(
		"INSERT INTO workspaces (id, name) VALUES ($1, 'Upgrade')",
		workspaceID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := migrationDB.Exec(
		"INSERT INTO sources (id, workspace_id, kind, name) VALUES ($1, $2, 'cycle_agent', 'Cycle Agent')",
		sourceID, workspaceID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := migrationDB.Exec(
		"INSERT INTO workspace_agent_settings (workspace_id, source_id, enabled, provider_type, model) VALUES ($1, $2, true, 'chatgpt', 'gpt-upgrade')",
		workspaceID, sourceID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := migrationDB.Exec(
		"INSERT INTO model_oauth_credentials (workspace_id, provider, credential_ciphertext, expires_at, display_label) VALUES ($1, 'chatgpt', decode(repeat('aa', 29), 'hex'), now() + interval '1 hour', 'Legacy ChatGPT')",
		workspaceID,
	); err != nil {
		t.Fatal(err)
	}

	if err := goose.Up(migrationDB, migrations); err != nil {
		t.Fatalf("migrate v9 schema: %v", err)
	}

	var position int
	var enabled bool
	var provider, model, label string
	var credentialID uuid.UUID
	if err := migrationDB.QueryRow(
		`SELECT r.position, r.enabled, r.provider_type, r.credential_id, r.model, c.display_label
		   FROM workspace_model_routes r
		   JOIN model_oauth_credentials c
		     ON c.workspace_id = r.workspace_id AND c.id = r.credential_id
		  WHERE r.workspace_id = $1`,
		workspaceID,
	).Scan(&position, &enabled, &provider, &credentialID, &model, &label); err != nil {
		t.Fatalf("read migrated route: %v", err)
	}
	if position != 0 || !enabled || provider != "chatgpt" || credentialID == uuid.Nil ||
		model != "gpt-upgrade" || label != "Legacy ChatGPT" {
		t.Fatalf(
			"migrated route position=%d enabled=%v provider=%q credential=%s model=%q label=%q",
			position, enabled, provider, credentialID, model, label,
		)
	}

	var legacyProviderColumn int
	if err := migrationDB.QueryRow(
		`SELECT count(*)
		   FROM information_schema.columns
		  WHERE table_schema = $1
		    AND table_name = 'workspace_agent_settings'
		    AND column_name = 'provider_type'`,
		schema,
	).Scan(&legacyProviderColumn); err != nil {
		t.Fatal(err)
	}
	if legacyProviderColumn != 0 {
		t.Fatalf("workspace_agent_settings still has provider_type after v9 migration")
	}

	if err := goose.Down(migrationDB, migrations); err != nil {
		t.Fatalf("downgrade v9 to v8: %v", err)
	}
	var downgradedProvider, downgradedModel string
	if err := migrationDB.QueryRow(
		"SELECT provider_type, model FROM workspace_agent_settings WHERE workspace_id=$1",
		workspaceID,
	).Scan(&downgradedProvider, &downgradedModel); err != nil {
		t.Fatalf("read downgraded settings: %v", err)
	}
	if downgradedProvider != "chatgpt" || downgradedModel != "gpt-upgrade" {
		t.Fatalf("downgraded settings provider=%q model=%q", downgradedProvider, downgradedModel)
	}
	if err := goose.Up(migrationDB, migrations); err != nil {
		t.Fatalf("re-upgrade v8 to v9: %v", err)
	}
	var routeCount int
	if err := migrationDB.QueryRow(
		"SELECT count(*) FROM workspace_model_routes WHERE workspace_id=$1 AND provider_type='chatgpt' AND model='gpt-upgrade'",
		workspaceID,
	).Scan(&routeCount); err != nil {
		t.Fatal(err)
	}
	if routeCount != 1 {
		t.Fatalf("route count after data down/up=%d, want 1", routeCount)
	}
}
