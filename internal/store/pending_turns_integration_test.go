package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestSteerPendingTurnKeepsInputAndSupersedesCurrentAttempt(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	submit := func(content string) SubmitSessionCommentResult {
		t.Helper()
		result, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
			WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
			Content: content, IdempotencyKey: content, DispatchMode: domain.DispatchModeEnqueue,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	submit("Current work")
	current, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	earlier := submit("First queued input")
	selected := submit("Selected queued input")
	if err := s.SteerPendingTurn(ctx, f.WorkspaceID, f.CardID, selected.TurnID, f.AgentID, time.Now()); err == nil {
		t.Fatal("non-human source could steer a turn")
	}
	if err := s.SteerPendingTurn(ctx, f.WorkspaceID, f.CardID, earlier.TurnID, f.HumanID, time.Now()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.SteerPendingTurn(ctx, f.WorkspaceID, f.CardID, selected.TurnID, f.HumanID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	old, err := s.GetAttempt(ctx, f.WorkspaceID, current.Attempt.ID)
	if err != nil || old.Status != domain.AttemptStatusSuperseded {
		t.Fatalf("old execution: %+v err=%v", old, err)
	}
	var turns, comments int
	if err := s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM card_turns WHERE card_id=$1),
		       (SELECT count(*) FROM events WHERE card_id=$1 AND event_type='comment')
	`, f.CardID).Scan(&turns, &comments); err != nil || turns != 3 || comments != 3 {
		t.Fatalf("steer duplicated input: turns=%d comments=%d err=%v", turns, comments, err)
	}
	claimed, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil || claimed.Turn.ID != selected.TurnID || claimed.Turn.SourceEventID.UUID != selected.EventID {
		t.Fatalf("steered input was not claimed next: %+v err=%v", claimed, err)
	}
	// 迟到的旧执行和重复操作都不能破坏已经领取的新执行。
	_, _, err = s.AddAttemptEvent(ctx, AttemptEventParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: current.Attempt.ID,
		RunnerSourceID: f.AgentID, Type: domain.EventTypeFinding,
		Content: "Late result", IdempotencyKey: "late-result",
	})
	if !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("late event accepted: %v", err)
	}
	if err := s.SteerPendingTurn(ctx, f.WorkspaceID, f.CardID, selected.TurnID, f.HumanID, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("claimed input could be steered again: %v", err)
	}
	if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, claimed.Attempt.ID, domain.AttemptStatusCompleted, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil || next.Turn.ID != earlier.TurnID {
		t.Fatalf("remaining queue lost FIFO: %+v err=%v", next, err)
	}
}

func TestCancelledQueuedInputStaysInLedgerButLeavesHumanTimeline(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	input, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "Remove this queued input", IdempotencyKey: "removed-input", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPendingTurn(ctx, f.WorkspaceID, f.CardID, input.TurnID, f.HumanID, "Removed from queue"); err != nil {
		t.Fatal(err)
	}
	if err := s.SteerPendingTurn(ctx, f.WorkspaceID, f.CardID, input.TurnID, f.HumanID, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cancelled input could be steered: %v", err)
	}
	timeline, err := s.ListCardTimeline(ctx, f.WorkspaceID, f.CardID)
	if err != nil || len(timeline) != 0 {
		t.Fatalf("cancelled input reappeared as a bubble: %+v err=%v", timeline, err)
	}
	ledger, err := s.ListCardEvents(ctx, f.WorkspaceID, f.CardID)
	if err != nil || len(ledger) != 1 || ledger[0].ID != input.EventID {
		t.Fatalf("cancelled input was deleted from ledger: %+v err=%v", ledger, err)
	}
}

func TestEditPendingTurnKeepsIdentityOrderAndCreationIdempotency(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	request := SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "Original input", IdempotencyKey: "editable-input", DispatchMode: domain.DispatchModeEnqueue,
	}
	input, err := s.SubmitSessionComment(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	var originalSequence int64
	if err := s.pool.QueryRow(ctx, `SELECT sequence FROM card_turns WHERE id=$1`, input.TurnID).Scan(&originalSequence); err != nil {
		t.Fatal(err)
	}
	if err := s.EditPendingTurn(ctx, f.WorkspaceID, f.CardID, input.TurnID, f.AgentID, "Unauthorized"); err == nil {
		t.Fatal("non-human source could edit queued input")
	}
	if err := s.EditPendingTurn(ctx, f.WorkspaceID, f.CardID, input.TurnID, f.HumanID, "  Updated input  "); err != nil {
		t.Fatal(err)
	}
	// 原发送请求重试必须复用原输入，不能将修改后的内容改回去。
	replayed, err := s.SubmitSessionComment(ctx, request)
	if err != nil || !replayed.Reused || replayed.TurnID != input.TurnID || replayed.EventID != input.EventID {
		t.Fatalf("creation retry duplicated edited input: %+v err=%v", replayed, err)
	}
	turn, err := loadTurn(ctx, s.pool, f.WorkspaceID, input.TurnID)
	if err != nil || turn.Content != "Updated input" || turn.SourceEventID.UUID != input.EventID {
		t.Fatalf("edited turn: %+v err=%v", turn, err)
	}
	var sequence int64
	if err := s.pool.QueryRow(ctx, `SELECT sequence FROM card_turns WHERE id=$1`, input.TurnID).Scan(&sequence); err != nil || sequence != originalSequence {
		t.Fatalf("editing changed queue order: sequence=%d err=%v", sequence, err)
	}
	ledger, err := s.ListCardEvents(ctx, f.WorkspaceID, f.CardID)
	if err != nil || len(ledger) != 1 || ledger[0].ID != input.EventID || ledger[0].Content != "Updated input" {
		t.Fatalf("comment was not updated in place: %+v err=%v", ledger, err)
	}
	claim, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil || claim.Turn.Content != "Updated input" {
		t.Fatalf("claim used stale content: %+v err=%v", claim, err)
	}
	if err := s.EditPendingTurn(ctx, f.WorkspaceID, f.CardID, input.TurnID, f.HumanID, "Too late"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("claimed input was editable: %v", err)
	}
	request.Content, request.IdempotencyKey = "Cancelled input", "cancelled-edit"
	cancelled, err := s.SubmitSessionComment(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPendingTurn(ctx, f.WorkspaceID, f.CardID, cancelled.TurnID, f.HumanID, "Removed"); err != nil {
		t.Fatal(err)
	}
	if err := s.EditPendingTurn(ctx, f.WorkspaceID, f.CardID, cancelled.TurnID, f.HumanID, "Too late"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cancelled input was editable: %v", err)
	}
}
