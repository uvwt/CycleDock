package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestMainChatLifecycleAndArchiveProtection(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	f := newFixture(t, s, domain.CardKindTodo)

	first, created, err := s.EnsureMainDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil || !created || first.Title != "主聊天" || !first.PinnedAt.Valid {
		t.Fatalf("create main chat: card=%+v created=%v err=%v", first, created, err)
	}
	second, created, err := s.EnsureMainDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("reopen main chat: card=%s created=%v err=%v", second.ID, created, err)
	}

	if _, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "Inspect", IdempotencyKey: "start", DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.PublishReply(ctx, PublishReplyParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AttemptID:      attempt.Attempt.ID,
		RunnerSourceID: f.AgentID,
		Message:        ReplyMessage{Type: "text", Content: "服务器巡检完成"},
		IdempotencyKey: "todo-reply-notice",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: first.ID, AuthorSourceID: f.HumanID,
		Content: "主聊天里直接说", IdempotencyKey: "main-note", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}

	timeline, err := s.ListCardTimeline(ctx, f.WorkspaceID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 1 || timeline[0].EventType != "comment" || timeline[0].Content != "主聊天里直接说" {
		t.Fatalf("main chat timeline should only contain its own events, got: %+v", timeline)
	}

	var turns int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM card_turns
		WHERE workspace_id = $1 AND card_id = $2 AND source <> 'human'
	`, f.WorkspaceID, first.ID).Scan(&turns); err != nil {
		t.Fatal(err)
	}
	if turns != 0 {
		t.Fatalf("main chat gained %d non-human turns", turns)
	}

	if err := s.ArchiveDiscussion(ctx, f.WorkspaceID, first.ID, f.HumanID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("archive main chat error=%v, want ErrConflict", err)
	}
	if err := s.DeleteDiscussion(ctx, f.WorkspaceID, first.ID, f.HumanID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("delete main chat error=%v, want ErrConflict", err)
	}
}

func TestAgentContextIdentifiesMainChatByProjectReference(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	f := newFixture(t, s, domain.CardKindTodo)
	discussion, err := s.CreateDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE cards SET title='主聊天' WHERE workspace_id=$1 AND id=$2`, f.WorkspaceID, discussion.ID); err != nil {
		t.Fatal(err)
	}
	main, _, err := s.EnsureMainDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		cardID   uuid.UUID
		wantMain bool
	}{
		{cardID: f.CardID}, {cardID: discussion.ID}, {cardID: main.ID, wantMain: true},
	} {
		if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, test.cardID, f.HumanID, "检查上下文", test.cardID.String(), domain.DispatchModeEnqueue); err != nil {
			t.Fatal(err)
		}
		claim, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim.Attempt.ID, f.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.CardID != test.cardID || loaded.IsMainDiscussion != test.wantMain {
			t.Fatalf("card=%s main=%v, want card=%s main=%v", loaded.CardID, loaded.IsMainDiscussion, test.cardID, test.wantMain)
		}
		if err := s.FinishAttempt(ctx, f.WorkspaceID, test.cardID, claim.Attempt.ID, domain.AttemptStatusCompleted, "", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
}
