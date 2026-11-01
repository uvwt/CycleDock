package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestReplyMigrationRollbackPreservesMessagesAndAttemptLinks(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	schema := pgx.Identifier{"reply_rollback_" + uuid.NewString()}.Sanitize()
	if _, err := s.pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer s.pool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	connectionURL, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := connectionURL.Query()
	query.Set("search_path", schema)
	connectionURL.RawQuery = query.Encode()
	database, err := sql.Open("pgx", connectionURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	const migrations = "../../db/migrations"
	if err := goose.Up(database, migrations); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, connectionURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	isolated := New(pool)
	f := newFixture(t, isolated, domain.CardKindTodo)
	if _, err := isolated.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"Check rollback", "rollback-start", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := isolated.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	messages := []ReplyMessage{
		{Type: "text", Content: "Hello", Images: []string{"https://example.com/image.png"}},
		{Type: "attachment", URL: "https://example.com/report.pdf", Alt: "Report"},
		{Type: "choice", EndTurn: true, Widget: &ChoiceWidget{Question: "Continue?", Options: []ChoiceOption{
			{Value: "yes", Label: "Yes"}, {Value: "no", Label: "No"},
		}}},
	}
	ids := make([]uuid.UUID, len(messages))
	sequences := make([]int64, len(messages))
	for i, message := range messages {
		id, _, err := isolated.PublishReply(ctx, PublishReplyParams{
			WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID,
			RunnerSourceID: f.AgentID, Message: message, IdempotencyKey: uuid.NewString(),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
		if err := pool.QueryRow(ctx, "SELECT sequence FROM events WHERE id=$1", id).Scan(&sequences[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := goose.DownTo(database, migrations, 2); err != nil {
		t.Fatalf("rollback with messages: %v", err)
	}
	for i, id := range ids {
		var eventType, content string
		var attemptID uuid.UUID
		var sequence int64
		if err := pool.QueryRow(ctx, "SELECT event_type,content,attempt_id,sequence FROM events WHERE id=$1", id).
			Scan(&eventType, &content, &attemptID, &sequence); err != nil {
			t.Fatal(err)
		}
		var restored ReplyMessage
		if err := json.Unmarshal([]byte(content), &restored); err != nil {
			t.Fatalf("rollback lost structured content: %v", err)
		}
		if eventType != "progress" || attemptID != claim.Attempt.ID || sequence != sequences[i] || !reflect.DeepEqual(restored, messages[i]) {
			t.Fatalf("rollback changed message/linkage: type=%s attempt=%s sequence=%d message=%+v", eventType, attemptID, sequence, restored)
		}
	}
	if err := goose.Up(database, migrations); err != nil {
		t.Fatalf("reapply after rollback: %v", err)
	}
	if rows, err := isolated.ListCardTimeline(ctx, f.WorkspaceID, f.CardID); err != nil || len(rows) != len(messages) {
		t.Fatalf("reapplied timeline rows=%d err=%v", len(rows), err)
	}
}
