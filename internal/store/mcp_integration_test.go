package store

import (
	"context"
	"errors"
	"testing"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestAgentGenerateWorkConfirmThresholdAndHumanDone(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	discussion, err := s.CreateAgentDiscussion(ctx, AgentDiscussionCreateParams{
		WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, SourceID: f.AgentID,
		IdempotencyKey: "create-discussion-1",
	})
	if err != nil || discussion.Reused {
		t.Fatalf("create discussion: %+v err=%v", discussion, err)
	}
	created, err := s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID: f.WorkspaceID, DiscussionID: discussion.Card.ID, SourceID: f.AgentID,
		Kind: domain.CardKindTodo, Title: "Agent created", Description: "needs two confirmations",
		RequiredConfirmations: 2, IdempotencyKey: "generate-agent-card-1",
	})
	if err != nil || created.Reused {
		t.Fatalf("generate agent card: %+v err=%v", created, err)
	}
	if !created.Card.SourceDiscussionID.Valid || created.Card.SourceDiscussionID.UUID != discussion.Card.ID {
		t.Fatalf("generated work source=%v, want discussion %s", created.Card.SourceDiscussionID, discussion.Card.ID)
	}
	replayed, err := s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID: f.WorkspaceID, DiscussionID: discussion.Card.ID, SourceID: f.AgentID,
		Kind: domain.CardKindTodo, Title: "Agent created", Description: "needs two confirmations",
		RequiredConfirmations: 2, IdempotencyKey: "generate-agent-card-1",
	})
	if err != nil || !replayed.Reused || replayed.Card.ID != created.Card.ID {
		t.Fatalf("generate card replay: %+v err=%v", replayed, err)
	}
	if err := s.MoveTodo(ctx, f.WorkspaceID, created.Card.ID, f.AgentID, domain.TodoStateWorking); err != nil {
		t.Fatalf("agent move to working: %v", err)
	}

	first, err := s.ConfirmCard(ctx, f.WorkspaceID, created.Card.ID, f.AgentID, "agent A verified", "confirm-a")
	if err != nil || first.MovedToReview || first.ConfirmationCount != 1 {
		t.Fatalf("first confirm: %+v err=%v", first, err)
	}
	firstReplay, err := s.ConfirmCard(ctx, f.WorkspaceID, created.Card.ID, f.AgentID, "agent A verified", "confirm-a")
	if err != nil || !firstReplay.Reused || firstReplay.ConfirmationCount != 1 {
		t.Fatalf("first confirm replay: %+v err=%v", firstReplay, err)
	}
	second, err := s.ConfirmCard(ctx, f.WorkspaceID, created.Card.ID, f.Agent2ID, "agent B verified", "confirm-b")
	if err != nil || !second.MovedToReview || second.ConfirmationCount != 2 {
		t.Fatalf("second confirm: %+v err=%v", second, err)
	}

	// Review 退回 Working 后，历史确认不能计入新一轮，也不能阻止原 Agent 再次确认。
	if err := s.MoveTodo(ctx, f.WorkspaceID, created.Card.ID, f.HumanID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	oldReplay, err := s.ConfirmCard(ctx, f.WorkspaceID, created.Card.ID, f.AgentID, "agent A verified", "confirm-a")
	if err != nil || !oldReplay.Reused || oldReplay.MovedToReview || oldReplay.ConfirmationCount != 0 {
		t.Fatalf("old request must not confirm the new round: %+v err=%v", oldReplay, err)
	}
	newFirst, err := s.ConfirmCard(ctx, f.WorkspaceID, created.Card.ID, f.AgentID, "agent A reverified", "confirm-a-round-2")
	if err != nil || newFirst.Reused || newFirst.MovedToReview || newFirst.ConfirmationCount != 1 {
		t.Fatalf("first confirmation in reopened round: %+v err=%v", newFirst, err)
	}
	if _, err := s.ConfirmCard(ctx, f.WorkspaceID, created.Card.ID, f.AgentID, "duplicate vote", "confirm-a-duplicate"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("same agent voted twice in one round: %v", err)
	}
	// Working -> Working 是幂等状态请求，不能重置本轮已经收到的确认。
	if err := s.MoveTodo(ctx, f.WorkspaceID, created.Card.ID, f.HumanID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	newSecond, err := s.ConfirmCard(ctx, f.WorkspaceID, created.Card.ID, f.Agent2ID, "agent B reverified", "confirm-b-round-2")
	if err != nil || !newSecond.MovedToReview || newSecond.ConfirmationCount != 2 {
		t.Fatalf("second confirmation in reopened round: %+v err=%v", newSecond, err)
	}
	var historicalCount int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE workspace_id=$1 AND card_id=$2 AND event_type='confirm'`, f.WorkspaceID, created.Card.ID).Scan(&historicalCount); err != nil || historicalCount != 4 {
		t.Fatalf("confirmation history was lost: count=%d err=%v", historicalCount, err)
	}

	if err := s.MoveTodo(ctx, f.WorkspaceID, created.Card.ID, f.AgentID, domain.TodoStateDone); !errors.Is(err, domain.ErrHumanRequired) {
		t.Fatalf("agent moved review to done: %v", err)
	}
	if err := s.MoveTodo(ctx, f.WorkspaceID, created.Card.ID, f.HumanID, domain.TodoStateDone); err != nil {
		t.Fatalf("human could not complete review: %v", err)
	}
}

func TestConcurrentConfirmationsFromSameAgentCountOnce(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, "UPDATE cards SET required_confirmations=2 WHERE workspace_id=$1 AND id=$2", f.WorkspaceID, f.CardID); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTodo(ctx, f.WorkspaceID, f.CardID, f.AgentID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, key := range []string{"concurrent-a", "concurrent-b"} {
		go func() {
			<-start
			_, err := s.ConfirmCard(ctx, f.WorkspaceID, f.CardID, f.AgentID, "verified", key)
			results <- err
		}()
	}
	close(start)
	var accepted, rejected int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, domain.ErrConflict):
			rejected++
		default:
			t.Fatalf("unexpected concurrent confirmation error: %v", err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d; want one vote and one conflict", accepted, rejected)
	}
	second, err := s.ConfirmCard(ctx, f.WorkspaceID, f.CardID, f.Agent2ID, "independently verified", "concurrent-other-agent")
	if err != nil || !second.MovedToReview || second.ConfirmationCount != 2 {
		t.Fatalf("distinct agents must still meet threshold: %+v err=%v", second, err)
	}
}
