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

func TestAgentFollowupConcurrentClaimOwnershipAndIdempotentFinish(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindNormal)
	ctx := context.Background()

	commentID, reused, err := s.AddAgentComment(ctx, f.WorkspaceID, f.CardID, f.AgentID, "durable context", "agent-comment-1")
	if err != nil || reused || commentID == uuid.Nil {
		t.Fatalf("add agent comment: id=%s reused=%v err=%v", commentID, reused, err)
	}
	replayedCommentID, reused, err := s.AddAgentComment(ctx, f.WorkspaceID, f.CardID, f.AgentID, "durable context", "agent-comment-1")
	if err != nil || !reused || replayedCommentID != commentID {
		t.Fatalf("replay agent comment: id=%s reused=%v err=%v", replayedCommentID, reused, err)
	}
	if _, _, err := s.AddAgentComment(ctx, f.WorkspaceID, f.CardID, f.AgentID, "changed", "agent-comment-1"); !errors.Is(err, domain.ErrIdempotencyMismatch) {
		t.Fatalf("mismatched comment replay error=%v", err)
	}

	followup, err := s.CreateAgentFollowup(ctx, f.WorkspaceID, f.CardID, f.AgentID, "process this next", "followup-1")
	if err != nil || followup.Reused {
		t.Fatalf("create followup: %+v err=%v", followup, err)
	}
	replayedFollowup, err := s.CreateAgentFollowup(ctx, f.WorkspaceID, f.CardID, f.AgentID, "process this next", "followup-1")
	if err != nil || !replayedFollowup.Reused || replayedFollowup.TriggerID != followup.TriggerID {
		t.Fatalf("replay followup: %+v err=%v", replayedFollowup, err)
	}

	start := make(chan struct{})
	type claimResult struct {
		agent uuid.UUID
		run   ClaimWorkResult
		err   error
	}
	results := make(chan claimResult, 2)
	var wg sync.WaitGroup
	for _, agentID := range []uuid.UUID{f.AgentID, f.Agent2ID} {
		wg.Add(1)
		go func(sourceID uuid.UUID) {
			defer wg.Done()
			<-start
			run, err := s.ClaimNextTriggerAndStart(ctx, f.WorkspaceID, f.CardID, sourceID, time.Minute)
			results <- claimResult{agent: sourceID, run: run, err: err}
		}(agentID)
	}
	close(start)
	wg.Wait()
	close(results)

	var winner claimResult
	var successCount int
	for result := range results {
		if result.err == nil {
			winner = result
			successCount++
			continue
		}
		if !errors.Is(result.err, domain.ErrConflict) {
			t.Fatalf("unexpected concurrent claim error: %v", result.err)
		}
	}
	if successCount != 1 || winner.run.Run.ID == uuid.Nil {
		t.Fatalf("claim success count=%d winner=%+v", successCount, winner)
	}

	retry, err := s.ClaimNextTriggerAndStart(ctx, f.WorkspaceID, f.CardID, winner.agent, time.Minute)
	if err != nil || !retry.Reused || retry.Run.ID != winner.run.Run.ID {
		t.Fatalf("claim retry did not recover current run: %+v err=%v", retry, err)
	}

	other := f.AgentID
	if winner.agent == f.AgentID {
		other = f.Agent2ID
	}
	if err := s.Heartbeat(ctx, f.WorkspaceID, winner.run.Run.ID, other, time.Minute); !errors.Is(err, domain.ErrRunNotCurrent) {
		t.Fatalf("other source heartbeat error=%v", err)
	}
	if _, err := s.AddRunEvent(ctx, RunEventParams{
		WorkspaceID: f.WorkspaceID, RunID: winner.run.Run.ID, RunnerSourceID: other,
		Type: domain.EventTypeFinding, Content: "stolen write", IdempotencyKey: "finding-other",
	}); !errors.Is(err, domain.ErrRunNotCurrent) {
		t.Fatalf("other source run event error=%v", err)
	}

	eventID, reused, err := s.ReportProgress(ctx, ProgressParams{
		WorkspaceID: f.WorkspaceID, RunID: winner.run.Run.ID, RunnerSourceID: winner.agent,
		Stage: "inspect", Summary: "checked inputs", EventContent: "input contract verified",
		IdempotencyKey: "progress-1", LeaseDuration: time.Minute,
	})
	if err != nil || reused || eventID == uuid.Nil {
		t.Fatalf("progress: id=%s reused=%v err=%v", eventID, reused, err)
	}
	replayedEventID, reused, err := s.ReportProgress(ctx, ProgressParams{
		WorkspaceID: f.WorkspaceID, RunID: winner.run.Run.ID, RunnerSourceID: winner.agent,
		Stage: "inspect", Summary: "checked inputs", EventContent: "input contract verified",
		IdempotencyKey: "progress-1", LeaseDuration: time.Minute,
	})
	if err != nil || !reused || replayedEventID != eventID {
		t.Fatalf("progress replay: id=%s reused=%v err=%v", replayedEventID, reused, err)
	}

	findingID, err := s.AddRunEvent(ctx, RunEventParams{
		WorkspaceID: f.WorkspaceID, RunID: winner.run.Run.ID, RunnerSourceID: winner.agent,
		Type: domain.EventTypeFinding, Content: "durable finding", IdempotencyKey: "finding-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	replayedFindingID, err := s.AddRunEvent(ctx, RunEventParams{
		WorkspaceID: f.WorkspaceID, RunID: winner.run.Run.ID, RunnerSourceID: winner.agent,
		Type: domain.EventTypeFinding, Content: "durable finding", IdempotencyKey: "finding-1",
	})
	if err != nil || replayedFindingID != findingID {
		t.Fatalf("finding replay: id=%s want=%s err=%v", replayedFindingID, findingID, err)
	}

	if err := s.FinishRun(ctx, FinishRunParams{
		WorkspaceID: f.WorkspaceID, RunID: winner.run.Run.ID, RunnerSourceID: winner.agent,
		Result: "completed work", IdempotencyKey: "finish-1",
	}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := s.FinishRun(ctx, FinishRunParams{
		WorkspaceID: f.WorkspaceID, RunID: winner.run.Run.ID, RunnerSourceID: winner.agent,
		Result: "completed work", IdempotencyKey: "finish-1",
	}); err != nil {
		t.Fatalf("finish retry: %v", err)
	}
	if err := s.FinishRun(ctx, FinishRunParams{
		WorkspaceID: f.WorkspaceID, RunID: winner.run.Run.ID, RunnerSourceID: winner.agent,
		Result: "changed result", IdempotencyKey: "finish-1",
	}); !errors.Is(err, domain.ErrIdempotencyMismatch) {
		t.Fatalf("mismatched finish retry error=%v", err)
	}
}

func TestAgentCardCreateConfirmThresholdAndHumanDone(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindNormal)
	ctx := context.Background()

	created, err := s.CreateAgentCard(ctx, AgentCardCreateParams{
		WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, SourceID: f.AgentID,
		Kind: domain.CardKindNormal, Title: "Agent created", Description: "needs two confirmations",
		RequiredConfirmations: 2, IdempotencyKey: "create-agent-card-1",
	})
	if err != nil || created.Reused {
		t.Fatalf("create agent card: %+v err=%v", created, err)
	}
	replayed, err := s.CreateAgentCard(ctx, AgentCardCreateParams{
		WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, SourceID: f.AgentID,
		Kind: domain.CardKindNormal, Title: "Agent created", Description: "needs two confirmations",
		RequiredConfirmations: 2, IdempotencyKey: "create-agent-card-1",
	})
	if err != nil || !replayed.Reused || replayed.Card.ID != created.Card.ID {
		t.Fatalf("create card replay: %+v err=%v", replayed, err)
	}
	if err := s.MoveNormalCard(ctx, f.WorkspaceID, created.Card.ID, f.AgentID, domain.NormalColumnWorking); err != nil {
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

	if err := s.MoveNormalCard(ctx, f.WorkspaceID, created.Card.ID, f.AgentID, domain.NormalColumnDone); !errors.Is(err, domain.ErrHumanRequired) {
		t.Fatalf("agent moved review to done: %v", err)
	}
	if err := s.MoveNormalCard(ctx, f.WorkspaceID, created.Card.ID, f.HumanID, domain.NormalColumnDone); err != nil {
		t.Fatalf("human could not complete review: %v", err)
	}
}

func TestSupersededRunCannotFinishThroughOriginalRunner(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindNormal)
	ctx := context.Background()

	first, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &f.AgentID, Content: "old direction", IdempotencyKey: "supersede-old",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil || first.RunID == nil {
		t.Fatalf("create old run: %+v err=%v", first, err)
	}
	if err := s.StartRun(ctx, f.WorkspaceID, *first.RunID, f.AgentID, time.Minute); err != nil {
		t.Fatal(err)
	}
	interrupt, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &f.Agent2ID, Content: "new direction", IdempotencyKey: "supersede-new",
		DispatchMode: domain.DispatchModeInterrupt,
	})
	if err != nil || interrupt.RunID == nil {
		t.Fatalf("interrupt: %+v err=%v", interrupt, err)
	}

	if err := s.FinishRun(ctx, FinishRunParams{
		WorkspaceID: f.WorkspaceID, RunID: *first.RunID, RunnerSourceID: f.AgentID,
		Result: "late old result", IdempotencyKey: "late-finish",
	}); !errors.Is(err, domain.ErrRunNotCurrent) {
		t.Fatalf("superseded old run finish error=%v", err)
	}
}
