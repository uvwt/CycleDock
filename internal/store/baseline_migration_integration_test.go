package store

import (
	"context"
	"testing"
)

func TestFinalBaselineUsesOnlyTurnAttemptExecutionSchema(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()

	var cardTurns, turnAttempts, triggers, agentRuns *string
	if err := s.pool.QueryRow(ctx, `
		SELECT
			to_regclass('public.card_turns')::text,
			to_regclass('public.turn_attempts')::text,
			to_regclass('public.triggers')::text,
			to_regclass('public.agent_runs')::text
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
		WHERE table_schema='public'
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
		WHERE table_schema='public'
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
		WHERE table_schema='public' AND table_name='turn_attempts'
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
	if migrationVersion != 4 {
		t.Fatalf("migration version=%d want 4", migrationVersion)
	}
}
