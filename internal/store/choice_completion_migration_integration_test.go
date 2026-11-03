package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/uvwt/CycleDock/internal/domain"
)

func TestWaitingMigrationCompletesAttemptAndPreservesQuestion(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "Choose a target", "migration-question", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	question, _, err := s.PublishReply(ctx, PublishReplyParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID, RunnerSourceID: f.AgentID,
		Message:        ReplyMessage{Type: "choice", EndTurn: true, Widget: &ChoiceWidget{Question: "Which target?", Options: []ChoiceOption{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}}},
		IdempotencyKey: "migration-choice",
	})
	if err != nil {
		t.Fatal(err)
	}
	database := stdlib.OpenDB(*s.pool.Config().ConnConfig)
	defer database.Close()
	const migrations = "../../db/migrations"
	defer func() {
		if err := goose.Up(database, migrations); err != nil {
			t.Errorf("restore latest schema: %v", err)
		}
	}()
	if err := goose.DownTo(database, migrations, 21); err != nil {
		t.Fatal(err)
	}
	// 还原旧版选择消息发布后的状态，再真实执行升级。
	if _, err := s.pool.Exec(ctx, `UPDATE turn_attempts SET status='waiting_human',heartbeat_at=NULL,lease_expires_at=NULL,current_stage='等待用户选择',progress_summary='Which target?' WHERE id=$1`, claim.Attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(database, migrations); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.GetAttempt(ctx, f.WorkspaceID, claim.Attempt.ID)
	if err != nil || attempt.Status != domain.AttemptStatusCompleted || attempt.FinishedAt == nil {
		t.Fatalf("migrated attempt=%+v err=%v", attempt, err)
	}
	current, err := s.CurrentAttemptView(ctx, f.WorkspaceID, f.CardID)
	if err != nil || current != nil {
		t.Fatalf("migration retained current attempt=%+v err=%v", current, err)
	}
	var preserved bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id=$1 AND message->'widget'->>'question'='Which target?')`, question).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("question preserved=%v err=%v", preserved, err)
	}
	// 数据库也必须拒绝旧状态，而不只是 Go 类型不再定义。
	if _, err := s.pool.Exec(ctx, `UPDATE turn_attempts SET status='waiting_human',finished_at=NULL WHERE id=$1`, attempt.ID); err == nil {
		t.Fatal("database still accepts removed waiting status")
	}
	if _, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID, Content: "Target B", IdempotencyKey: "migration-answer", DispatchMode: domain.DispatchModeEnqueue}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute); err != nil {
		t.Fatalf("migration blocked next turn: %v", err)
	}
}
