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

func TestExternalOAuthAttemptOwnershipAndCardScopedClaim(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	oauthA, err := s.CreateSource(ctx, f.WorkspaceID, domain.SourceKindOAuthClient, "External A")
	if err != nil {
		t.Fatal(err)
	}
	oauthB, err := s.CreateSource(ctx, f.WorkspaceID, domain.SourceKindOAuthClient, "External B")
	if err != nil {
		t.Fatal(err)
	}

	first, err := s.SubmitAgentTurn(
		ctx, f.WorkspaceID, f.CardID, oauthA.ID,
		"execute external work", "external-turn-1",
	)
	if err != nil || first.Reused || first.EventID == uuid.Nil || first.TurnID == uuid.Nil {
		t.Fatalf("submit agent turn: %+v err=%v", first, err)
	}
	replayed, err := s.SubmitAgentTurn(
		ctx, f.WorkspaceID, f.CardID, oauthA.ID,
		"execute external work", "external-turn-1",
	)
	if err != nil || !replayed.Reused || replayed.TurnID != first.TurnID || replayed.EventID != first.EventID {
		t.Fatalf("replay agent turn: %+v err=%v", replayed, err)
	}
	if _, err := s.SubmitAgentTurn(
		ctx, f.WorkspaceID, f.CardID, oauthA.ID,
		"changed work", "external-turn-1",
	); !errors.Is(err, domain.ErrIdempotencyMismatch) {
		t.Fatalf("mismatched turn replay error=%v", err)
	}

	otherCard, err := s.CreateTodoCard(
		ctx, f.WorkspaceID, f.ProjectID, f.HumanID,
		"Other card", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	otherTurn, err := s.SubmitAgentTurn(
		ctx, f.WorkspaceID, otherCard.ID, oauthA.ID,
		"must stay on the other card", "external-turn-other",
	)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	type result struct {
		source uuid.UUID
		claim  CardAttemptClaimResult
		err    error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, sourceID := range []uuid.UUID{oauthA.ID, oauthB.ID} {
		wg.Add(1)
		go func(sourceID uuid.UUID) {
			defer wg.Done()
			<-start
			claim, err := s.ClaimCardTurnAttempt(
				ctx, f.WorkspaceID, f.CardID, sourceID,
				time.Now().UTC(), time.Minute,
			)
			results <- result{source: sourceID, claim: claim, err: err}
		}(sourceID)
	}
	close(start)
	wg.Wait()
	close(results)

	var winner result
	successes := 0
	for item := range results {
		if item.err == nil {
			successes++
			winner = item
			continue
		}
		if !errors.Is(item.err, domain.ErrConflict) {
			t.Fatalf("unexpected concurrent claim error=%v", item.err)
		}
	}
	if successes != 1 {
		t.Fatalf("claim successes=%d want 1", successes)
	}
	if winner.claim.Turn.ID != first.TurnID || winner.claim.Attempt.CardID != f.CardID {
		t.Fatalf("claim escaped target card: %+v first=%+v", winner.claim, first)
	}

	retry, err := s.ClaimCardTurnAttempt(
		ctx, f.WorkspaceID, f.CardID, winner.source,
		time.Now().UTC(), time.Minute,
	)
	if err != nil || !retry.Reused || retry.Attempt.ID != winner.claim.Attempt.ID {
		t.Fatalf("claim retry=%+v err=%v", retry, err)
	}

	other := oauthA.ID
	if winner.source == oauthA.ID {
		other = oauthB.ID
	}
	if err := s.HeartbeatAttemptOwned(
		ctx, f.WorkspaceID, f.CardID, winner.claim.Attempt.ID, other,
		time.Now().UTC(), time.Minute,
	); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("other source heartbeat error=%v", err)
	}
	if _, _, err := s.ReportAttemptProgress(ctx, AttemptProgressParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID,
		AttemptID: winner.claim.Attempt.ID, RunnerSourceID: other,
		Stage: "steal", Summary: "must fail", LeaseDuration: time.Minute,
	}); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("other source progress error=%v", err)
	}
	if err := s.CompleteAttempt(
		ctx, f.WorkspaceID, f.CardID, winner.claim.Attempt.ID, other,
		time.Now().UTC(),
	); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("other source complete error=%v", err)
	}

	progressID, reusedProgress, err := s.ReportAttemptProgress(ctx, AttemptProgressParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID,
		AttemptID: winner.claim.Attempt.ID, RunnerSourceID: winner.source,
		Stage: "verify", Summary: "ownership verified",
		EventContent:   "external attempt ownership verified",
		IdempotencyKey: "external-progress-1", LeaseDuration: time.Minute,
	})
	if err != nil || reusedProgress || progressID == uuid.Nil {
		t.Fatalf("winner progress id=%s reused=%v err=%v", progressID, reusedProgress, err)
	}
	replayedProgressID, reusedProgress, err := s.ReportAttemptProgress(ctx, AttemptProgressParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID,
		AttemptID: winner.claim.Attempt.ID, RunnerSourceID: winner.source,
		Stage: "verify", Summary: "ownership verified",
		EventContent:   "external attempt ownership verified",
		IdempotencyKey: "external-progress-1", LeaseDuration: time.Minute,
	})
	if err != nil || !reusedProgress || replayedProgressID != progressID {
		t.Fatalf("progress replay id=%s reused=%v err=%v", replayedProgressID, reusedProgress, err)
	}

	if err := s.CompleteAttempt(
		ctx, f.WorkspaceID, f.CardID, winner.claim.Attempt.ID, winner.source,
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAttempt(
		ctx, f.WorkspaceID, f.CardID, winner.claim.Attempt.ID, winner.source,
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("completed retry: %v", err)
	}

	var resultCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM events
		WHERE workspace_id=$1 AND card_id=$2
		  AND event_type='result'
	`, f.WorkspaceID, f.CardID).Scan(&resultCount); err != nil {
		t.Fatal(err)
	}
	if resultCount != 0 {
		t.Fatalf("result event count=%d want 0", resultCount)
	}

	otherClaim, err := s.ClaimCardTurnAttempt(
		ctx, f.WorkspaceID, otherCard.ID, oauthA.ID,
		time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if otherClaim.Turn.ID != otherTurn.TurnID || otherClaim.Attempt.CardID != otherCard.ID {
		t.Fatalf("other card claim=%+v turn=%+v", otherClaim, otherTurn)
	}
}

func TestExternalOAuthClaimsHumanReplyAndScheduleTurns(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()

	todo := newFixture(t, s, domain.CardKindTodo)
	external, err := s.CreateSource(
		ctx, todo.WorkspaceID, domain.SourceKindOAuthClient, "External Session Agent",
	)
	if err != nil {
		t.Fatal(err)
	}
	humanTurn, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: todo.WorkspaceID, CardID: todo.CardID,
		AuthorSourceID: todo.HumanID, Content: "inspect target A",
		IdempotencyKey: "external-human-turn", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimCardTurnAttempt(
		ctx, todo.WorkspaceID, todo.CardID, external.ID,
		time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Turn.ID != humanTurn.TurnID || first.Turn.Source != domain.TurnSourceHuman {
		t.Fatalf("external claim human turn=%+v human=%+v", first, humanTurn)
	}
	if err := s.WaitAttemptForHumanWithPrompt(
		ctx, todo.WorkspaceID, todo.CardID, first.Attempt.ID, external.ID,
		"need_target", "which target?", time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitAttemptForHumanWithPrompt(
		ctx, todo.WorkspaceID, todo.CardID, first.Attempt.ID, external.ID,
		"need_target", "which target?", time.Now().UTC(),
	); err != nil {
		t.Fatalf("waiting_human retry should be idempotent: %v", err)
	}
	reply, err := s.ReplySessionAndContinue(ctx, SubmitSessionCommentParams{
		WorkspaceID: todo.WorkspaceID, CardID: todo.CardID,
		AuthorSourceID: todo.HumanID, Content: "use target B",
		IdempotencyKey: "external-human-reply", ReplyToAttemptID: first.Attempt.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldAttempt, err := s.GetAttempt(ctx, todo.WorkspaceID, first.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldAttempt.Status != domain.AttemptStatusSuperseded {
		t.Fatalf("waiting attempt status=%s want superseded", oldAttempt.Status)
	}
	continued, err := s.ClaimCardTurnAttempt(
		ctx, todo.WorkspaceID, todo.CardID, external.ID,
		time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if continued.Turn.ID != reply.TurnID || continued.Turn.Source != domain.TurnSourceHuman {
		t.Fatalf("external claim reply=%+v reply=%+v", continued, reply)
	}
	if err := s.CompleteAttempt(
		ctx, todo.WorkspaceID, todo.CardID, continued.Attempt.ID, external.ID,
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}

	ongoing := newFixture(t, s, domain.CardKindOngoing)
	scheduledExternal, err := s.CreateSource(
		ctx, ongoing.WorkspaceID, domain.SourceKindOAuthClient, "External Scheduler Agent",
	)
	if err != nil {
		t.Fatal(err)
	}
	rule := domain.ScheduleRule{
		Frequency: domain.ScheduleDaily, Hour: 8, Minute: 30, Timezone: "UTC",
	}
	schedule, err := s.CreateSchedule(
		ctx, ongoing.WorkspaceID, ongoing.CardID, rule,
		time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := time.Date(2026, 10, 26, 8, 30, 0, 0, time.UTC)
	scheduled, err := s.CreateScheduleTurn(
		ctx, ongoing.WorkspaceID, schedule.ID, occurrence, "daily external check",
	)
	if err != nil {
		t.Fatal(err)
	}
	scheduledClaim, err := s.ClaimCardTurnAttempt(
		ctx, ongoing.WorkspaceID, ongoing.CardID, scheduledExternal.ID,
		time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if scheduledClaim.Turn.ID != scheduled.Turn.ID ||
		scheduledClaim.Turn.Source != domain.TurnSourceSchedule {
		t.Fatalf("external schedule claim=%+v scheduled=%+v", scheduledClaim, scheduled)
	}
}
