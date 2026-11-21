package store

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestFinalBaselineUsesOnlyTurnAttemptExecutionSchema(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()

	var cardTurns, turnAttempts, triggers, agentRuns *string
	if err := s.pool.QueryRow(ctx, `
		SELECT
			to_regclass('card_turns')::text,
			to_regclass('turn_attempts')::text,
			to_regclass('triggers')::text,
			to_regclass('agent_runs')::text
	`).Scan(&cardTurns, &turnAttempts, &triggers, &agentRuns); err != nil {
		t.Fatal(err)
	}
	if cardTurns == nil || *cardTurns != "card_turns" {
		t.Fatalf("card_turns missing: %v", cardTurns)
	}
	if turnAttempts == nil || *turnAttempts != "turn_attempts" {
		t.Fatalf("turn_attempts missing: %v", turnAttempts)
	}
	if triggers != nil || agentRuns != nil {
		t.Fatalf("legacy execution tables still exist: triggers=%v agent_runs=%v", triggers, agentRuns)
	}

	var legacyRunColumnCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema=current_schema()
		  AND table_name='events'
		  AND column_name='run_id'
	`).Scan(&legacyRunColumnCount); err != nil {
		t.Fatal(err)
	}
	if legacyRunColumnCount != 0 {
		t.Fatalf("events.run_id still exists")
	}
	var attemptColumnCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema=current_schema()
		  AND table_name='events'
		  AND column_name='attempt_id'
	`).Scan(&attemptColumnCount); err != nil {
		t.Fatal(err)
	}
	if attemptColumnCount != 1 {
		t.Fatalf("events.attempt_id count=%d want 1", attemptColumnCount)
	}

	var resultColumnCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='turn_attempts'
		  AND column_name='result_summary'
	`).Scan(&resultColumnCount); err != nil {
		t.Fatal(err)
	}
	if resultColumnCount != 0 {
		t.Fatal("turn_attempts still stores a duplicate reply summary")
	}

	var migrationVersion int64
	if err := s.pool.QueryRow(ctx, `
		SELECT version_id
		FROM goose_db_version
		WHERE is_applied
		ORDER BY id DESC
		LIMIT 1
	`).Scan(&migrationVersion); err != nil {
		t.Fatal(err)
	}
	if migrationVersion != 47 {
		t.Fatalf("migration version=%d want 47", migrationVersion)
	}
}

func TestMigration000047MarkedUnreadUpDownUp(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()

	schema := "mig47_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := s.pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})

	cfg := s.pool.Config().ConnConfig.Copy()
	cfg.RuntimeParams["search_path"] = schema
	sqlDB := stdlib.OpenDB(*cfg)
	defer sqlDB.Close()

	_, filename, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(sqlDB, migrations); err != nil {
		t.Fatalf("initial goose up: %v", err)
	}
	if err := goose.DownTo(sqlDB, migrations, 46); err != nil {
		t.Fatalf("goose down-to 46: %v", err)
	}
	var colCount int
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = $1
		  AND table_name = 'card_read_cursors'
		  AND column_name = 'marked_unread'
	`, schema).Scan(&colCount); err != nil {
		t.Fatal(err)
	}
	if colCount != 0 {
		t.Fatalf("marked_unread column still exists after down-to 46: count=%d", colCount)
	}
	if err := goose.Up(sqlDB, migrations); err != nil {
		t.Fatalf("goose re-up to 47: %v", err)
	}
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = $1
		  AND table_name = 'card_read_cursors'
		  AND column_name = 'marked_unread'
	`, schema).Scan(&colCount); err != nil {
		t.Fatal(err)
	}
	if colCount != 1 {
		t.Fatalf("marked_unread column missing after re-up to 47: count=%d", colCount)
	}
}
