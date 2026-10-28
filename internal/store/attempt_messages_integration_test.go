package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestAttemptMessagesAreIdempotentAndRejectStaleWrites(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"inspect", "message-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	params := AttemptEventParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: f.AgentID, Type: domain.EventTypeComment,
		Content: "I will inspect the card.", IdempotencyKey: "message-1",
	}
	id, reused, err := s.AddAttemptEvent(ctx, params)
	if err != nil || reused {
		t.Fatalf("first message: reused=%v err=%v", reused, err)
	}
	again, reused, err := s.AddAttemptEvent(ctx, params)
	if err != nil || !reused || again != id {
		t.Fatalf("message replay: id=%s reused=%v err=%v", again, reused, err)
	}
	// 模型在调用等待工具前已经问过同一个问题，工具不应再生成一条重复消息。
	params.Content, params.IdempotencyKey = "Which target?", "message-question"
	if _, _, err := s.AddAttemptEvent(ctx, params); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.WaitAttemptForHumanWithPrompt(ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID,
			f.AgentID, "confirm", "Which target?", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID,
		domain.AttemptStatusSuperseded, "superseded by human reply", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	params.Content, params.IdempotencyKey = "late message", "message-late"
	if _, _, err := s.AddAttemptEvent(ctx, params); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("stale message error=%v", err)
	}
	timeline, err := s.ListCardTimeline(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, event := range timeline {
		if event.AttemptID.Valid && event.AttemptID.UUID == claim.Attempt.ID && event.EventType == "comment" {
			messages = append(messages, event.Content)
		}
	}
	if len(messages) != 2 || messages[0] != "I will inspect the card." || messages[1] != "Which target?" {
		t.Fatalf("persisted messages=%v", messages)
	}
	var turns int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM card_turns WHERE card_id=$1", f.CardID).Scan(&turns); err != nil {
		t.Fatal(err)
	}
	if turns != 1 {
		t.Fatalf("agent messages created new turns: count=%d", turns)
	}
}
