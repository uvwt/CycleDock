package store

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestCardReadCursorMigrationSeedsExistingMessagesAsRead(t *testing.T) {
	requireIntegration(t)
	ctx := context.Background()
	schema := pgx.Identifier{"read_cursor_migration_" + uuid.NewString()}.Sanitize()

	connectionURL, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	basePool, err := pgxpool.New(ctx, connectionURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := basePool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		basePool.Close()
		t.Fatal(err)
	}
	basePool.Close()
	defer func() {
		cleanupPool, err := pgxpool.New(ctx, connectionURL.String())
		if err == nil {
			_, _ = cleanupPool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
			cleanupPool.Close()
		}
	}()

	query := connectionURL.Query()
	query.Set("search_path", schema)
	connectionURL.RawQuery = query.Encode()
	database, err := sql.Open("pgx", connectionURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	const migrations = "../../db/migrations"
	if err := goose.UpTo(database, migrations, 11); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, connectionURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	isolated := New(pool)
	f := newFixture(t, isolated, domain.CardKindTodo)

	if _, err := isolated.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"existing request", "existing-request", domain.DispatchModeEnqueue,
	); err != nil {
		t.Fatal(err)
	}
	claim, err := isolated.ClaimCardTurnAttempt(
		ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := isolated.PublishReply(ctx, PublishReplyParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: f.AgentID, IdempotencyKey: "existing-reply",
		Message: ReplyMessage{Type: "text", Content: "existing answer", EndTurn: true},
		FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	var latestMessageSequence int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(sequence), 0)
		FROM events
		WHERE workspace_id=$1 AND card_id=$2
		  AND event_type IN ('comment','message')
	`, f.WorkspaceID, f.CardID).Scan(&latestMessageSequence); err != nil {
		t.Fatal(err)
	}

	if err := goose.UpTo(database, migrations, 12); err != nil {
		t.Fatal(err)
	}
	rows, err := isolated.ListProjectCardSummaries(
		ctx, f.WorkspaceID, f.ProjectID, f.HumanID,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID != f.CardID {
			continue
		}
		if row.Unread ||
			row.LatestMessageSequence != latestMessageSequence ||
			row.LastReadMessageSequence != latestMessageSequence {
			t.Fatalf("migration did not seed existing history as read: %+v", row)
		}
		return
	}
	t.Fatalf("card %s missing after migration", f.CardID)
}
