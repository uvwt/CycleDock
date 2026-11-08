package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/domain"
)

func TestTimelinePagesLoadTheirOwnAttempts(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	// 超过原先 50 次执行的上限，确保最早的历史页也能加载状态。
	for i := range 60 {
		_, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
			WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
			Content: fmt.Sprintf("Question %d", i), IdempotencyKey: fmt.Sprintf("input-%d", i), DispatchMode: domain.DispatchModeEnqueue,
		})
		if err != nil {
			t.Fatal(err)
		}
		claim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = s.PublishReply(ctx, PublishReplyParams{
			WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID, RunnerSourceID: f.AgentID,
			Message: ReplyMessage{Type: "text", Content: "Answer", EndTurn: true}, IdempotencyKey: fmt.Sprintf("reply-%d", i), FinishOnEnd: true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[uuid.UUID]bool)
	before := int64(1<<62 - 1)
	for {
		rows, err := s.ListCardTimelineBefore(ctx, f.WorkspaceID, f.CardID, before, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		oldest := rows[0].Sequence
		attempts, err := s.ListTimelineAttempts(ctx, f.WorkspaceID, f.CardID, oldest, before)
		if err != nil {
			t.Fatal(err)
		}
		if len(attempts) != len(rows)/2 {
			t.Fatalf("page attempts=%d want %d", len(attempts), len(rows)/2)
		}
		eventIDs := make(map[uuid.UUID]bool)
		for _, e := range rows {
			eventIDs[e.ID] = true
		}
		for _, a := range attempts {
			if !a.SourceEventID.Valid || !eventIDs[a.SourceEventID.UUID] {
				t.Fatalf("attempt from unloaded input: %+v", a)
			}
			if seen[a.ID] {
				t.Fatalf("duplicate page attempt %s", a.ID)
			}
			seen[a.ID] = true
		}
		before = oldest
	}
	if len(seen) != 60 {
		t.Fatalf("loaded attempts=%d want 60", len(seen))
	}
}

func TestEditSessionCommentAndRerun_截断后续记录并重新入队(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	first, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "First question", IdempotencyKey: "edit-test-1", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim1, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishReply(ctx, PublishReplyParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim1.Attempt.ID, RunnerSourceID: f.AgentID,
		Message: ReplyMessage{Type: "text", Content: "First answer", EndTurn: true}, IdempotencyKey: "edit-reply-1", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}

	second, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "Second question", IdempotencyKey: "edit-test-2", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim2, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveReplyPreview(ctx, ReplyPreviewParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim2.Attempt.ID, RunnerSourceID: f.AgentID,
		PreviewID: "gen:call1", Content: "Streaming second answer",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishReply(ctx, PublishReplyParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim2.Attempt.ID, RunnerSourceID: f.AgentID,
		PreviewID: "gen:call1", Message: ReplyMessage{Type: "text", Content: "Second answer", EndTurn: true}, IdempotencyKey: "edit-reply-2", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}

	edited, err := s.EditSessionCommentAndRerun(ctx, EditSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		EventID:        second.EventID,
		AuthorSourceID: f.HumanID,
		Content:        "Second question (edited)",
	})
	if err != nil {
		t.Fatalf("EditSessionCommentAndRerun: %v", err)
	}
	if edited.EventID != second.EventID || edited.TurnID != second.TurnID {
		t.Fatalf("unexpected edited result: %+v", edited)
	}

	// 编辑后原来的第二次回答已被删除，第 1 轮问答与修改后的第 2 轮提问保留
	rows, err := s.ListCardTimelineBefore(ctx, f.WorkspaceID, f.CardID, 1<<62-1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("timeline len=%d want 3 (Q1, A1, edited Q2)", len(rows))
	}
	if rows[0].ID != first.EventID || rows[2].ID != second.EventID || rows[2].Content != "Second question (edited)" {
		t.Fatalf("unexpected timeline rows: %+v", rows)
	}

	// 修改后的 Turn 可以被重新领取并回答
	rerunClaim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("ClaimCardTurnAttempt after edit: %v", err)
	}
	if rerunClaim.Turn.ID != second.TurnID || rerunClaim.Turn.Content != "Second question (edited)" {
		t.Fatalf("unexpected rerun claim: %+v", rerunClaim)
	}
}
