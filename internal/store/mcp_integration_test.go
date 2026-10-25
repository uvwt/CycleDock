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

	if err := s.MoveTodo(ctx, f.WorkspaceID, created.Card.ID, f.AgentID, domain.TodoStateDone); !errors.Is(err, domain.ErrHumanRequired) {
		t.Fatalf("agent moved review to done: %v", err)
	}
	if err := s.MoveTodo(ctx, f.WorkspaceID, created.Card.ID, f.HumanID, domain.TodoStateDone); err != nil {
		t.Fatalf("human could not complete review: %v", err)
	}
}
