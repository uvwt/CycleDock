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

func TestTurnAttemptConcurrentClaimKeepsFutureTurnPending(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	first, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"inspect the server", "turn-1", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"inspect the server", "turn-1", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Reused || replayed.Turn.ID != first.Turn.ID {
		t.Fatalf("idempotent replay=%+v first=%+v", replayed, first)
	}
	if _, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"different content", "turn-1", domain.DispatchModeEnqueue,
	); !errors.Is(err, domain.ErrIdempotencyMismatch) {
		t.Fatalf("mismatched replay error=%v, want ErrIdempotencyMismatch", err)
	}

	second, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"then inspect the logs", "turn-2", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	type result struct {
		claim *AttemptClaim
		err   error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claim, err := s.ClaimNextTurnAttempt(
				ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute,
			)
			results <- result{claim: claim, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var claimed *AttemptClaim
	var successes int
	for result := range results {
		if result.err == nil {
			successes++
			claimed = result.claim
			continue
		}
		if !errors.Is(result.err, domain.ErrNotFound) {
			t.Fatalf("concurrent claim error=%v", result.err)
		}
	}
	if successes != 1 || claimed == nil {
		t.Fatalf("successes=%d claim=%+v", successes, claimed)
	}
	if claimed.Turn.ID != first.Turn.ID {
		t.Fatalf("claimed turn=%s want first=%s", claimed.Turn.ID, first.Turn.ID)
	}
	if claimed.Attempt.AttemptNumber != 1 ||
		claimed.Attempt.Reason != domain.AttemptReasonInitial ||
		claimed.Attempt.Status != domain.AttemptStatusRunning {
		t.Fatalf("initial attempt=%+v", claimed.Attempt)
	}

	var secondAttempts int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM turn_attempts
		WHERE workspace_id=$1 AND turn_id=$2
	`, f.WorkspaceID, second.Turn.ID).Scan(&secondAttempts); err != nil {
		t.Fatal(err)
	}
	if secondAttempts != 0 {
		t.Fatalf("future queued Turn already has %d attempts", secondAttempts)
	}

	if err := s.FinishAttempt(
		ctx, f.WorkspaceID, f.CardID, claimed.Attempt.ID,
		domain.AttemptStatusCompleted, "", time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if next.Turn.ID != second.Turn.ID {
		t.Fatalf("next turn=%s want=%s", next.Turn.ID, second.Turn.ID)
	}
}

func TestRetryAttemptCreatesOneSuccessorOnSameTurn(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	turn, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"build it", "retry-turn", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(
		ctx, f.WorkspaceID, f.CardID, initial.Attempt.ID,
		domain.AttemptStatusFailed, "provider failed", time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	type retryResult struct {
		result RetryAttemptResult
		err    error
	}
	results := make(chan retryResult, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := s.RetryAttempt(
				ctx, f.WorkspaceID, f.CardID, initial.Attempt.ID, f.AgentID,
				domain.AttemptReasonRetry,
			)
			results <- retryResult{result: result, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var successorID uuid.UUID
	var reusedCount int
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if successorID == uuid.Nil {
			successorID = result.result.Attempt.ID
		} else if successorID != result.result.Attempt.ID {
			t.Fatalf("concurrent retry created two successors: %s and %s", successorID, result.result.Attempt.ID)
		}
		if result.result.Reused {
			reusedCount++
		}
	}
	if reusedCount != 1 {
		t.Fatalf("reused retries=%d, want exactly one replay", reusedCount)
	}

	var turnCount, attemptCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM card_turns WHERE workspace_id=$1 AND id=$2),
			(SELECT count(*) FROM turn_attempts WHERE workspace_id=$1 AND turn_id=$2)
	`, f.WorkspaceID, turn.Turn.ID).Scan(&turnCount, &attemptCount); err != nil {
		t.Fatal(err)
	}
	if turnCount != 1 || attemptCount != 2 {
		t.Fatalf("turns=%d attempts=%d, want one Turn with two Attempts", turnCount, attemptCount)
	}

	retryClaim, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retryClaim.Attempt.ID != successorID ||
		retryClaim.Attempt.TurnID != initial.Attempt.TurnID ||
		retryClaim.Attempt.AttemptNumber != 2 ||
		retryClaim.Attempt.Reason != domain.AttemptReasonRetry {
		t.Fatalf("retry claim=%+v initial=%+v", retryClaim, initial)
	}
}

func TestScheduleAndSystemTurnsHaveExplicitSources(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()

	ongoing := newFixture(t, s, domain.CardKindOngoing)
	rule := domain.ScheduleRule{
		Frequency: domain.ScheduleDaily,
		Hour:      8,
		Minute:    30,
		Timezone:  "UTC",
	}
	schedule, err := s.CreateSchedule(
		ctx, ongoing.WorkspaceID, ongoing.CardID, rule,
		time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := time.Date(2026, 10, 26, 8, 30, 0, 0, time.UTC)
	first, err := s.CreateScheduleTurn(
		ctx, ongoing.WorkspaceID, schedule.ID, occurrence, "daily health check",
	)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := s.CreateScheduleTurn(
		ctx, ongoing.WorkspaceID, schedule.ID, occurrence, "daily health check",
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Turn.Source != domain.TurnSourceSchedule ||
		!replayed.Reused ||
		replayed.Turn.ID != first.Turn.ID {
		t.Fatalf("schedule first=%+v replay=%+v", first, replayed)
	}

	systemFixture := newFixture(t, s, domain.CardKindTodo)
	system, err := s.CreateSystemTurn(
		ctx, systemFixture.WorkspaceID, systemFixture.CardID, systemFixture.AgentID,
		"continue after controlled recovery", "system-recovery-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if system.Turn.Source != domain.TurnSourceSystem ||
		system.Turn.DispatchMode != domain.DispatchModeEnqueue {
		t.Fatalf("system turn=%+v", system.Turn)
	}
	if _, err := s.CreateSystemTurn(
		ctx, systemFixture.WorkspaceID, systemFixture.CardID, systemFixture.HumanID,
		"invalid system turn", "system-invalid",
	); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("human-created system turn error=%v, want ErrConflict", err)
	}
}

func TestInterruptSupersedesCurrentAttemptAndRunsBeforeQueuedTurn(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	firstTurn, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"first direction", "interrupt-first", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Turn.ID != firstTurn.Turn.ID {
		t.Fatalf("claimed first turn=%s want=%s", first.Turn.ID, firstTurn.Turn.ID)
	}

	queued, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"queued direction", "interrupt-queued", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	interrupting, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"urgent direction", "interrupt-urgent", domain.DispatchModeInterrupt,
	)
	if err != nil {
		t.Fatal(err)
	}

	oldAttempt, err := s.GetAttempt(ctx, f.WorkspaceID, first.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldAttempt.Status != domain.AttemptStatusSuperseded {
		t.Fatalf("old attempt status=%s want superseded", oldAttempt.Status)
	}

	urgentClaim, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if urgentClaim.Turn.ID != interrupting.Turn.ID {
		t.Fatalf("interrupt claim=%s want=%s (queued=%s)", urgentClaim.Turn.ID, interrupting.Turn.ID, queued.Turn.ID)
	}

	if err := s.FinishAttempt(
		ctx, f.WorkspaceID, f.CardID, urgentClaim.Attempt.ID,
		domain.AttemptStatusCompleted, "", time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	nextClaim, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if nextClaim.Turn.ID != queued.Turn.ID {
		t.Fatalf("post-interrupt claim=%s want queued=%s", nextClaim.Turn.ID, queued.Turn.ID)
	}
}

func TestHumanReplySupersedesWaitingAttemptEvenWhenEnqueued(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	turn, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"need a decision", "waiting-first", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Turn.ID != turn.Turn.ID {
		t.Fatalf("claim turn=%s want=%s", claim.Turn.ID, turn.Turn.ID)
	}
	if err := s.WaitAttemptForHuman(
		ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}

	reply, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"use option B", "waiting-reply", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	oldAttempt, err := s.GetAttempt(ctx, f.WorkspaceID, claim.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldAttempt.Status != domain.AttemptStatusSuperseded {
		t.Fatalf("waiting attempt status=%s want superseded", oldAttempt.Status)
	}

	replyClaim, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replyClaim.Turn.ID != reply.Turn.ID {
		t.Fatalf("reply claim=%s want=%s", replyClaim.Turn.ID, reply.Turn.ID)
	}
}

func TestCancelPendingTurnAndCurrentAttempt(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	pending, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"do not run this", "cancel-pending", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPendingTurn(
		ctx, f.WorkspaceID, f.CardID, pending.Turn.ID, f.HumanID, "no longer needed",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute,
	); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("claim cancelled pending turn error=%v want ErrNotFound", err)
	}

	active, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"run then cancel", "cancel-active", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Turn.ID != active.Turn.ID {
		t.Fatalf("active claim=%s want=%s", claim.Turn.ID, active.Turn.ID)
	}
	if err := s.CancelAttempt(
		ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID, f.HumanID,
		"stop execution", time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAttempt(ctx, f.WorkspaceID, claim.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.AttemptStatusCancelled {
		t.Fatalf("cancelled attempt status=%s want cancelled", got.Status)
	}
	if err := s.HeartbeatAttempt(
		ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID, time.Now().UTC(), time.Minute,
	); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("heartbeat cancelled attempt error=%v want ErrAttemptNotCurrent", err)
	}
}

func TestHeartbeatAndConcurrentLeaseRecoveryStayOnSameTurn(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	if _, err := s.SaveAgentSettings(
		ctx, f.WorkspaceID, testAgentSettings("http://127.0.0.1:1/v1", "test-model", ""),
	); err != nil {
		t.Fatal(err)
	}

	turn, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"long running work", "lease-first", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)
	claim, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, started, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatAttempt(
		ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID,
		started.Add(30*time.Second), 2*time.Minute,
	); err != nil {
		t.Fatal(err)
	}
	notExpired, err := s.ExpireAndRecoverBuiltInAttempts(
		ctx, f.WorkspaceID, started.Add(90*time.Second), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if notExpired.Expired != 0 || notExpired.RecoveryAttempts != 0 {
		t.Fatalf("premature recovery=%+v", notExpired)
	}

	checkAt := started.Add(3 * time.Minute)
	start := make(chan struct{})
	results := make(chan AttemptLeaseRecoveryResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := s.ExpireAndRecoverBuiltInAttempts(
				ctx, f.WorkspaceID, checkAt, 10,
			)
			results <- result
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	var expired, recovered int
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		expired += result.Expired
		recovered += result.RecoveryAttempts
	}
	if expired != 1 || recovered != 1 {
		t.Fatalf("concurrent recovery expired=%d recovered=%d want 1/1", expired, recovered)
	}

	oldAttempt, err := s.GetAttempt(ctx, f.WorkspaceID, claim.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldAttempt.Status != domain.AttemptStatusExpired {
		t.Fatalf("old attempt status=%s want expired", oldAttempt.Status)
	}

	recovery, err := s.ClaimNextTurnAttempt(
		ctx, f.WorkspaceID, f.AgentID, checkAt.Add(time.Second), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Turn.ID != turn.Turn.ID ||
		recovery.Attempt.TurnID != claim.Attempt.TurnID ||
		recovery.Attempt.AttemptNumber != 2 ||
		recovery.Attempt.Reason != domain.AttemptReasonRecovery ||
		!recovery.Attempt.RetryOfAttemptID.Valid ||
		recovery.Attempt.RetryOfAttemptID.UUID != claim.Attempt.ID {
		t.Fatalf("recovery claim=%+v original=%+v", recovery, claim)
	}
}
