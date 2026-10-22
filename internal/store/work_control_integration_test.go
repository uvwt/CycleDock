package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestHumanCancelPendingTriggerAndRetryCreatesNewDirection(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	f := newFixture(t, s, domain.CardKindTodo)

	original, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "queued work", IdempotencyKey: "cancel-pending-original",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if original.RunID != nil {
		t.Fatalf("pending Trigger unexpectedly has Run: %+v", original)
	}

	if err := s.CancelPendingTrigger(
		ctx, f.WorkspaceID, f.CardID, original.TriggerID, f.AgentID, "agent must not cancel",
	); !errors.Is(err, domain.ErrHumanRequired) {
		t.Fatalf("agent cancel error=%v, want ErrHumanRequired", err)
	}
	if err := s.CancelPendingTrigger(
		ctx, f.WorkspaceID, f.CardID, original.TriggerID, f.HumanID, "no longer needed",
	); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPendingTrigger(
		ctx, f.WorkspaceID, f.CardID, original.TriggerID, f.HumanID, "idempotent repeat",
	); err != nil {
		t.Fatalf("repeated cancel should be idempotent: %v", err)
	}

	queue, err := s.ListPendingQueue(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 0 {
		t.Fatalf("cancelled Trigger remained pending: %+v", queue)
	}
	if _, err := s.ClaimNextTrigger(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cancelled Trigger was claimable: %v", err)
	}

	retry, err := s.RetryTrigger(
		ctx, f.WorkspaceID, f.CardID, original.TriggerID, f.HumanID, "retry-cancelled-trigger",
	)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := s.RetryTrigger(
		ctx, f.WorkspaceID, f.CardID, original.TriggerID, f.HumanID, "retry-cancelled-trigger",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Reused || replayed.TriggerID != retry.TriggerID {
		t.Fatalf("retry replay=%+v first=%+v", replayed, retry)
	}

	var cause string
	var retryOf uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		SELECT cause, retry_of_trigger_id
		FROM triggers
		WHERE id = $1
	`, retry.TriggerID).Scan(&cause, &retryOf); err != nil {
		t.Fatal(err)
	}
	if cause != string(domain.TriggerCauseManualRetry) || retryOf != original.TriggerID {
		t.Fatalf("retry cause=%q retry_of=%s", cause, retryOf)
	}

	claimed, err := s.ClaimNextTrigger(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.TriggerID != retry.TriggerID || claimed.ID == uuid.Nil {
		t.Fatalf("claimed=%+v retry=%+v", claimed, retry)
	}
}

func TestHumanCancelRunningRunBlocksLateWritesAndCanRetry(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	f := newFixture(t, s, domain.CardKindTodo)

	original, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &f.AgentID,
		Content:        "running work", IdempotencyKey: "cancel-running-original",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil || original.RunID == nil {
		t.Fatalf("create run=%+v err=%v", original, err)
	}
	if err := s.StartRun(ctx, f.WorkspaceID, *original.RunID, f.AgentID, time.Minute); err != nil {
		t.Fatal(err)
	}

	if err := s.CancelRun(
		ctx, f.WorkspaceID, f.CardID, *original.RunID, f.AgentID, "agent must not cancel",
	); !errors.Is(err, domain.ErrHumanRequired) {
		t.Fatalf("agent cancel error=%v, want ErrHumanRequired", err)
	}
	if err := s.CancelRun(
		ctx, f.WorkspaceID, f.CardID, *original.RunID, f.HumanID, "stop this attempt",
	); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelRun(
		ctx, f.WorkspaceID, f.CardID, *original.RunID, f.HumanID, "repeat",
	); err != nil {
		t.Fatalf("repeated run cancel should be idempotent: %v", err)
	}

	if _, err := s.AddRunEvent(ctx, RunEventParams{
		WorkspaceID: f.WorkspaceID, RunID: *original.RunID, RunnerSourceID: f.AgentID,
		Type: domain.EventTypeFinding, Content: "late finding", IdempotencyKey: "late-after-cancel",
	}); !errors.Is(err, domain.ErrRunNotCurrent) {
		t.Fatalf("late finding error=%v, want ErrRunNotCurrent", err)
	}
	if err := s.FinishRun(ctx, FinishRunParams{
		WorkspaceID: f.WorkspaceID, RunID: *original.RunID, RunnerSourceID: f.AgentID,
		Result: "late result", IdempotencyKey: "late-finish-after-cancel",
	}); !errors.Is(err, domain.ErrRunNotCurrent) {
		t.Fatalf("late finish error=%v, want ErrRunNotCurrent", err)
	}

	var status string
	var cancelledBy uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		SELECT status, cancelled_by_source_id
		FROM agent_runs
		WHERE id = $1
	`, *original.RunID).Scan(&status, &cancelledBy); err != nil {
		t.Fatal(err)
	}
	if status != string(domain.RunStatusCancelled) || cancelledBy != f.HumanID {
		t.Fatalf("status=%q cancelled_by=%s", status, cancelledBy)
	}

	retry, err := s.RetryTrigger(
		ctx, f.WorkspaceID, f.CardID, original.TriggerID, f.HumanID, "retry-cancelled-run",
	)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNextTrigger(ctx, f.WorkspaceID, f.CardID, f.Agent2ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.TriggerID != retry.TriggerID || claimed.ID == *original.RunID {
		t.Fatalf("retry reused old history: claimed=%+v original=%+v retry=%+v", claimed, original, retry)
	}
}

func TestBuiltInRestartRecoveryCreatesExactlyOneFollowup(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	f := newFixture(t, s, domain.CardKindTodo)

	settings, err := s.SaveAgentSettings(ctx, f.WorkspaceID, testAgentSettings(true, "http://127.0.0.1:18090/v1", "test-model", ""))
	if err != nil {
		t.Fatal(err)
	}

	queued, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &settings.SourceID,
		Content:        "resume queued after restart", IdempotencyKey: "restart-queued",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil || queued.RunID == nil {
		t.Fatalf("queued=%+v err=%v", queued, err)
	}
	resumed, err := s.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.RunID != *queued.RunID || !resumed.Reused {
		t.Fatalf("unexpired queued Run was duplicated: resumed=%+v queued=%+v", resumed, queued)
	}

	beforeEvents, err := s.ListCardEvents(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(2 * time.Minute)
	recovery, err := s.ExpireAndRecoverBuiltInLeases(ctx, f.WorkspaceID, future, 100)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Expired != 1 || recovery.RecoveryTriggers != 1 {
		t.Fatalf("recovery=%+v, want one expired and one followup", recovery)
	}
	again, err := s.ExpireAndRecoverBuiltInLeases(ctx, f.WorkspaceID, future.Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if again.Expired != 0 || again.RecoveryTriggers != 0 {
		t.Fatalf("repeated recovery was not idempotent: %+v", again)
	}

	var oldStatus string
	if err := s.pool.QueryRow(ctx, "SELECT status FROM agent_runs WHERE id=$1", resumed.RunID).Scan(&oldStatus); err != nil {
		t.Fatal(err)
	}
	if oldStatus != string(domain.RunStatusExpired) {
		t.Fatalf("old status=%q, want expired", oldStatus)
	}

	var recoveryTriggerID, retryOf uuid.UUID
	var recoveryCause string
	if err := s.pool.QueryRow(ctx, `
		SELECT id, cause, retry_of_trigger_id
		FROM triggers
		WHERE retry_of_trigger_id = $1
	`, resumed.TriggerID).Scan(&recoveryTriggerID, &recoveryCause, &retryOf); err != nil {
		t.Fatal(err)
	}
	if recoveryCause != string(domain.TriggerCauseSystemFollowup) || retryOf != resumed.TriggerID {
		t.Fatalf("recovery cause=%q retry_of=%s", recoveryCause, retryOf)
	}

	if _, err := s.AddRunEvent(ctx, RunEventParams{
		WorkspaceID: f.WorkspaceID, RunID: resumed.RunID, RunnerSourceID: settings.SourceID,
		Type: domain.EventTypeFinding, Content: "late stale finding", IdempotencyKey: "recovery-late-finding",
	}); !errors.Is(err, domain.ErrRunNotCurrent) {
		t.Fatalf("expired late finding error=%v, want ErrRunNotCurrent", err)
	}
	if err := s.FinishRun(ctx, FinishRunParams{
		WorkspaceID: f.WorkspaceID, RunID: resumed.RunID, RunnerSourceID: settings.SourceID,
		Result: "late stale result", IdempotencyKey: "recovery-late-finish",
	}); !errors.Is(err, domain.ErrRunNotCurrent) {
		t.Fatalf("expired late finish error=%v, want ErrRunNotCurrent", err)
	}
	afterEvents, err := s.ListCardEvents(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterEvents) != len(beforeEvents) {
		t.Fatalf("recovery/stale writes changed Timeline: before=%d after=%d", len(beforeEvents), len(afterEvents))
	}

	next, err := s.ClaimNextBuiltInRun(ctx, future.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if next.RunID == resumed.RunID || next.TriggerID != recoveryTriggerID {
		t.Fatalf("recovery resurrected old execution: old=%+v next=%+v", resumed, next)
	}

	if err := s.WaitForHuman(
		ctx, f.WorkspaceID, next.RunID, settings.SourceID, "need_input", "waiting survives restart",
	); err != nil {
		t.Fatal(err)
	}
	waitingRecovery, err := s.ExpireAndRecoverBuiltInLeases(
		ctx, f.WorkspaceID, future.Add(10*time.Minute), 100,
	)
	if err != nil {
		t.Fatal(err)
	}
	if waitingRecovery.Expired != 0 || waitingRecovery.RecoveryTriggers != 0 {
		t.Fatalf("waiting_human was incorrectly expired: %+v", waitingRecovery)
	}
	current, err := s.CurrentRun(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.ID != next.RunID || current.Status != string(domain.RunStatusWaitingHuman) {
		t.Fatalf("waiting_human was not preserved: %+v", current)
	}
}

func TestConcurrentLeaseRecoveryCreatesOneFollowup(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	f := newFixture(t, s, domain.CardKindTodo)

	settings, err := s.SaveAgentSettings(ctx, f.WorkspaceID, testAgentSettings(true, "http://127.0.0.1:18090/v1", "test-model", ""))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "concurrent recovery", IdempotencyKey: "concurrent-recovery-trigger",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.SourceID != settings.SourceID {
		t.Fatalf("claim source=%s settings source=%s", claim.SourceID, settings.SourceID)
	}

	future := time.Now().UTC().Add(2 * time.Minute)
	start := make(chan struct{})
	results := make(chan LeaseRecoveryResult, 2)
	errorsCh := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := s.ExpireAndRecoverBuiltInLeases(ctx, f.WorkspaceID, future, 100)
			results <- result
			errorsCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errorsCh)

	var expired, followups int
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		expired += result.Expired
		followups += result.RecoveryTriggers
	}
	if expired != 1 || followups != 1 {
		t.Fatalf("aggregate recovery expired=%d followups=%d, want 1/1", expired, followups)
	}

	var runCount, retryCount int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM agent_runs WHERE trigger_id=$1", claim.TriggerID).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM triggers WHERE retry_of_trigger_id=$1", claim.TriggerID).Scan(&retryCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 || retryCount != 1 {
		t.Fatalf("run history duplicated: runs=%d recovery_triggers=%d", runCount, retryCount)
	}
}
