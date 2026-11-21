package store

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

	var turnCount int
	var turnContent string
	if err := s.pool.QueryRow(ctx, `SELECT count(*), coalesce(max(content), '') FROM card_turns WHERE card_id=$1`, created.Card.ID).Scan(&turnCount, &turnContent); err != nil {
		t.Fatalf("query card turns: %v", err)
	}
	if turnCount != 1 || !strings.Contains(turnContent, "needs two confirmations") {
		t.Fatalf("expected initial turn created, count=%d, content=%q", turnCount, turnContent)
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

func TestAgentGenerateWorkAutoStartOptionAndManualStart(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	var notified atomic.Int32
	s.SetWorkReadyNotifier(func() {
		notified.Add(1)
	})
	t.Cleanup(func() {
		s.SetWorkReadyNotifier(nil)
	})

	discussion, err := s.CreateAgentDiscussion(ctx, AgentDiscussionCreateParams{
		WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, SourceID: f.AgentID,
		IdempotencyKey: "create-discussion-autostart",
	})
	if err != nil {
		t.Fatalf("create discussion: %v", err)
	}

	autoStartFalse := false
	autoStartTrue := true

	// 1) auto_start=false：仅建卡留档，不插入 initial_turn，不唤醒调度器，不能被内置调度器 claim。
	silentTodo, err := s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID:    f.WorkspaceID,
		DiscussionID:   discussion.Card.ID,
		SourceID:       f.AgentID,
		Kind:           domain.CardKindTodo,
		Title:          "暂缓执行的待办",
		Description:    "先建卡排期，暂不启动 Agent",
		AutoStart:      &autoStartFalse,
		IdempotencyKey: "gen-silent-todo-1",
	})
	if err != nil || silentTodo.Reused {
		t.Fatalf("generate silent todo: %+v err=%v", silentTodo, err)
	}
	if !silentTodo.Card.TodoState.Valid || silentTodo.Card.TodoState.String != string(domain.TodoStateTodo) {
		t.Fatalf("silent todo state=%v, want todo", silentTodo.Card.TodoState)
	}
	if notified.Load() != 0 {
		t.Fatalf("auto_start=false must not notify workReady, got notified=%d", notified.Load())
	}
	var turnCount int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM card_turns WHERE card_id=$1`, silentTodo.Card.ID).Scan(&turnCount); err != nil {
		t.Fatalf("query silent todo turns: %v", err)
	}
	if turnCount != 0 {
		t.Fatalf("auto_start=false must not create initial_turn, count=%d", turnCount)
	}
	if claim, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), DefaultClaimLease); !errors.Is(err, domain.ErrNotFound) || claim != nil {
		t.Fatalf("auto_start=false card must not be claimed, claim=%+v err=%v", claim, err)
	}

	// 同幂等键 + 同 auto_start=false 重试应复用。
	replayedSilent, err := s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID:    f.WorkspaceID,
		DiscussionID:   discussion.Card.ID,
		SourceID:       f.AgentID,
		Kind:           domain.CardKindTodo,
		Title:          "暂缓执行的待办",
		Description:    "先建卡排期，暂不启动 Agent",
		AutoStart:      &autoStartFalse,
		IdempotencyKey: "gen-silent-todo-1",
	})
	if err != nil || !replayedSilent.Reused || replayedSilent.Card.ID != silentTodo.Card.ID {
		t.Fatalf("silent todo replay: %+v err=%v", replayedSilent, err)
	}

	// 同幂等键但改传 auto_start=true / nil 应报幂等冲突。
	if _, err := s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID:    f.WorkspaceID,
		DiscussionID:   discussion.Card.ID,
		SourceID:       f.AgentID,
		Kind:           domain.CardKindTodo,
		Title:          "暂缓执行的待办",
		Description:    "先建卡排期，暂不启动 Agent",
		AutoStart:      &autoStartTrue,
		IdempotencyKey: "gen-silent-todo-1",
	}); !errors.Is(err, domain.ErrIdempotencyMismatch) {
		t.Fatalf("same key with different auto_start must mismatch, got err=%v", err)
	}

	// 2) auto_start=false 对 Ongoing 卡片同样不创建 initial_turn。
	silentOngoing, err := s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID:    f.WorkspaceID,
		DiscussionID:   discussion.Card.ID,
		SourceID:       f.AgentID,
		Kind:           domain.CardKindOngoing,
		Title:          "静默创建的巡检",
		Description:    "等后续再排班",
		AutoStart:      &autoStartFalse,
		IdempotencyKey: "gen-silent-ongoing-1",
	})
	if err != nil {
		t.Fatalf("generate silent ongoing: %v", err)
	}
	if !silentOngoing.Card.OngoingState.Valid || silentOngoing.Card.OngoingState.String != string(domain.OngoingStateActive) {
		t.Fatalf("silent ongoing state=%v, want active", silentOngoing.Card.OngoingState)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM card_turns WHERE card_id=$1`, silentOngoing.Card.ID).Scan(&turnCount); err != nil {
		t.Fatalf("query silent ongoing turns: %v", err)
	}
	if turnCount != 0 || notified.Load() != 0 {
		t.Fatalf("silent ongoing turnCount=%d notified=%d, want 0/0", turnCount, notified.Load())
	}

	// 3) 后续通过 StartCardExecution 一键启动静默待办，应插入系统 Turn、唤醒调度器并可被 claim。
	started, err := s.StartCardExecution(ctx, StartCardExecutionParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         silentTodo.Card.ID,
		AuthorSourceID: f.HumanID,
		IdempotencyKey: "start-silent-todo-1",
	})
	if err != nil || started.Reused {
		t.Fatalf("start silent todo: %+v err=%v", started, err)
	}
	if started.Turn.Source != domain.TurnSourceSystem || !strings.Contains(started.Turn.Content, "暂缓执行的待办") {
		t.Fatalf("unexpected started turn: %+v", started.Turn)
	}
	if notified.Load() != 1 {
		t.Fatalf("manual start must notify workReady once, got %d", notified.Load())
	}
	// 同幂等键重试应复用。
	startedReplay, err := s.StartCardExecution(ctx, StartCardExecutionParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         silentTodo.Card.ID,
		AuthorSourceID: f.HumanID,
		IdempotencyKey: "start-silent-todo-1",
	})
	if err != nil || !startedReplay.Reused || startedReplay.Turn.ID != started.Turn.ID {
		t.Fatalf("start replay: %+v err=%v", startedReplay, err)
	}
	// 换新幂等键重复启动已启动卡片应报冲突。
	if _, err := s.StartCardExecution(ctx, StartCardExecutionParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         silentTodo.Card.ID,
		AuthorSourceID: f.HumanID,
		IdempotencyKey: "start-silent-todo-2",
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate start must conflict, got %v", err)
	}
	// 手动启动后应能被调度器正常领取执行。
	claimed, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), DefaultClaimLease)
	if err != nil || claimed == nil || claimed.Attempt.CardID != silentTodo.Card.ID {
		t.Fatalf("manually started card should be claimed, claimed=%+v err=%v", claimed, err)
	}

	// 4) auto_start=true 与默认（nil）一致，幂等哈希互相兼容。
	autoCard, err := s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID:    f.WorkspaceID,
		DiscussionID:   discussion.Card.ID,
		SourceID:       f.AgentID,
		Kind:           domain.CardKindTodo,
		Title:          "默认立即执行",
		Description:    "立即启动",
		AutoStart:      &autoStartTrue,
		IdempotencyKey: "gen-auto-todo-1",
	})
	if err != nil || autoCard.Reused {
		t.Fatalf("generate auto todo: %+v err=%v", autoCard, err)
	}
	autoReplayNil, err := s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID:    f.WorkspaceID,
		DiscussionID:   discussion.Card.ID,
		SourceID:       f.AgentID,
		Kind:           domain.CardKindTodo,
		Title:          "默认立即执行",
		Description:    "立即启动",
		AutoStart:      nil,
		IdempotencyKey: "gen-auto-todo-1",
	})
	if err != nil || !autoReplayNil.Reused || autoReplayNil.Card.ID != autoCard.Card.ID {
		t.Fatalf("auto_start=true and nil must share idempotency hash: %+v err=%v", autoReplayNil, err)
	}
	if notified.Load() != 2 {
		t.Fatalf("auto_start=true must notify workReady, got %d", notified.Load())
	}
}
