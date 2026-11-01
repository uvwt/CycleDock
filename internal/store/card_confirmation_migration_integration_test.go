package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestConfirmationMigrationRestoresReopenedRound(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	if err := s.MoveTodo(ctx, f.WorkspaceID, f.CardID, f.AgentID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	first, err := s.ConfirmCard(ctx, f.WorkspaceID, f.CardID, f.AgentID, "verified", "first-round")
	if err != nil || !first.MovedToReview {
		t.Fatalf("first round: %+v err=%v", first, err)
	}
	if err := s.MoveTodo(ctx, f.WorkspaceID, f.CardID, f.HumanID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}

	database := stdlib.OpenDB(*s.pool.Config().ConnConfig)
	defer database.Close()
	const migrations = "../../db/migrations"
	// 退回旧表结构，再验证升级会从状态事件中还原轮次，而非把旧确认带入本轮。
	defer func() {
		if err := goose.Up(database, migrations); err != nil {
			t.Errorf("restore latest schema: %v", err)
		}
	}()
	if err := goose.DownTo(database, migrations, 15); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(database, migrations); err != nil {
		t.Fatal(err)
	}
	card, err := s.GetCard(ctx, f.WorkspaceID, f.CardID)
	if err != nil || card.ConfirmationRound != 2 {
		t.Fatalf("restored card round=%d err=%v", card.ConfirmationRound, err)
	}
	var oldRound int64
	if err := s.pool.QueryRow(ctx, "SELECT confirmation_round FROM events WHERE id=$1", first.EventID).Scan(&oldRound); err != nil || oldRound != 1 {
		t.Fatalf("restored confirmation round=%d err=%v", oldRound, err)
	}
	second, err := s.ConfirmCard(ctx, f.WorkspaceID, f.CardID, f.AgentID, "reverified", "second-round")
	if err != nil || !second.MovedToReview || second.ConfirmationCount != 1 {
		t.Fatalf("reopened card after migration: %+v err=%v", second, err)
	}
}
