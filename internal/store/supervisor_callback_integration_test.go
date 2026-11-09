package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestSupervisorCallbackOnReviewUsesSourceDiscussion(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	discussion, err := s.CreateAgentDiscussion(ctx, AgentDiscussionCreateParams{
		WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, SourceID: f.AgentID,
		IdempotencyKey: "supervisor-source-discussion",
	})
	if err != nil {
		t.Fatal(err)
	}
	mainChat, _, err := s.EnsureMainDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil {
		t.Fatal(err)
	}
	card, err := s.CreateTodoCard(
		ctx, f.WorkspaceID, f.ProjectID, f.HumanID,
		"完成回调", "", uuid.NullUUID{UUID: discussion.Card.ID, Valid: true}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTodo(ctx, f.WorkspaceID, card.ID, f.AgentID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	confirmed, err := s.ConfirmCard(ctx, f.WorkspaceID, card.ID, f.AgentID, "verified", "supervisor-review")
	if err != nil || !confirmed.MovedToReview {
		t.Fatalf("confirm=%+v err=%v", confirmed, err)
	}
	again, err := s.ConfirmCard(ctx, f.WorkspaceID, card.ID, f.AgentID, "verified", "supervisor-review")
	if err != nil || !again.Reused {
		t.Fatalf("confirm replay=%+v err=%v", again, err)
	}

	content := supervisorTurnContent(t, s, discussion.Card.ID)
	if !strings.Contains(content, "outcome=review") || !strings.Contains(content, card.ID.String()) {
		t.Fatalf("source callback=%q", content)
	}
	if count := supervisorTurnCount(t, s, mainChat.ID); count != 0 {
		t.Fatalf("main chat callbacks=%d, want 0", count)
	}
	if count := supervisorTurnCount(t, s, discussion.Card.ID); count != 1 {
		t.Fatalf("source callbacks=%d, want 1", count)
	}
}

func TestSupervisorCallbackFallsBackToMainDiscussion(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	mainChat, _, err := s.EnsureMainDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTodo(ctx, f.WorkspaceID, f.CardID, f.AgentID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmCard(ctx, f.WorkspaceID, f.CardID, f.AgentID, "verified", "fallback-review"); err != nil {
		t.Fatal(err)
	}
	content := supervisorTurnContent(t, s, mainChat.ID)
	if !strings.Contains(content, f.CardID.String()) || !strings.Contains(content, "outcome=review") {
		t.Fatalf("fallback callback=%q", content)
	}
}

func TestSupervisorFailureRetriesUntilIdenticalErrorOpensCircuit(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	mainChat, _, err := s.EnsureMainDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"run the task", "supervisor-failure", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 26, 2, 0, 0, 0, time.UTC)
	first, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(
		ctx, f.WorkspaceID, f.CardID, first.Attempt.ID,
		domain.AttemptStatusFailed, "missing config", now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	firstCallback := supervisorTurnContent(t, s, mainChat.ID)
	if !strings.Contains(firstCallback, "attempt=1/5") || strings.Contains(firstCallback, "熔断") {
		t.Fatalf("first callback=%q", firstCallback)
	}

	retried, err := s.RetryAttempt(
		ctx, f.WorkspaceID, f.CardID, first.Attempt.ID, f.AgentID, domain.AttemptReasonRetry,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondClaim, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(2*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if secondClaim.Attempt.ID != retried.Attempt.ID {
		t.Fatalf("claimed %s, retried %s", secondClaim.Attempt.ID, retried.Attempt.ID)
	}
	if err := s.FinishAttempt(
		ctx, f.WorkspaceID, f.CardID, secondClaim.Attempt.ID,
		domain.AttemptStatusFailed, "port refused", now.Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if count := supervisorTurnCount(t, s, mainChat.ID); count != 2 {
		t.Fatalf("callbacks after changed error=%d, want 2", count)
	}

	secondRetry, err := s.RetryAttempt(
		ctx, f.WorkspaceID, f.CardID, secondClaim.Attempt.ID, f.AgentID, domain.AttemptReasonRetry,
	)
	if err != nil {
		t.Fatal(err)
	}
	thirdClaim, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(4*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(
		ctx, f.WorkspaceID, f.CardID, thirdClaim.Attempt.ID,
		domain.AttemptStatusFailed, "port refused", now.Add(5*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	latest := latestSupervisorTurn(t, s, mainChat.ID)
	if !strings.Contains(latest, "熔断") || !strings.Contains(latest, "identical_error_count=2") {
		t.Fatalf("circuit callback=%q", latest)
	}
	if latestAttempt := latestAttemptNumber(t, s, turn.Turn.ID); latestAttempt != secondRetry.Attempt.AttemptNumber {
		t.Fatalf("attempt number=%d, want terminal %d", latestAttempt, secondRetry.Attempt.AttemptNumber)
	}
}

func TestSupervisorTimeoutDoesNotAutoRecoverWhenCircuitOpens(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	mainChat, _, err := s.EnsureMainDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAgentSettings(
		ctx, f.WorkspaceID, testAgentSettings("http://127.0.0.1:1/v1", "test-model", ""),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateHumanTurn(
		ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"long work", "supervisor-timeout", domain.DispatchModeEnqueue,
	); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	first, err := s.ClaimNextBuiltInAttempt(ctx, 1, started, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := s.ExpireAndRecoverBuiltInAttempts(ctx, f.WorkspaceID, started.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Expired != 1 || expired.RecoveryAttempts != 1 {
		t.Fatalf("first timeout=%+v", expired)
	}
	second, err := s.ClaimNextBuiltInAttempt(ctx, 1, started.Add(3*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Attempt.RetryOfAttemptID.Valid || second.Attempt.RetryOfAttemptID.UUID != first.Attempt.ID {
		t.Fatalf("recovery attempt=%+v", second.Attempt)
	}
	repeated, err := s.ExpireAndRecoverBuiltInAttempts(ctx, f.WorkspaceID, started.Add(5*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Expired != 1 || repeated.RecoveryAttempts != 0 {
		t.Fatalf("repeated timeout=%+v", repeated)
	}
	content := latestSupervisorTurn(t, s, mainChat.ID)
	if !strings.Contains(content, "outcome=expired") || !strings.Contains(content, "熔断") {
		t.Fatalf("timeout callback=%q", content)
	}
	if _, err := s.ClaimNextBuiltInAttempt(ctx, 1, started.Add(6*time.Minute), time.Minute); err == nil {
		t.Fatal("circuit opened but another attempt was queued")
	}
}

func supervisorTurnContent(t *testing.T, s *Store, cardID uuid.UUID) string {
	t.Helper()
	var content string
	if err := s.pool.QueryRow(context.Background(), `
		SELECT content FROM card_turns
		WHERE card_id=$1 AND idempotency_key LIKE 'supervisor-callback:%'
		ORDER BY sequence DESC LIMIT 1
	`, cardID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	return content
}

func latestSupervisorTurn(t *testing.T, s *Store, cardID uuid.UUID) string {
	return supervisorTurnContent(t, s, cardID)
}

func supervisorTurnCount(t *testing.T, s *Store, cardID uuid.UUID) int {
	t.Helper()
	var count int
	if err := s.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM card_turns
		WHERE card_id=$1 AND idempotency_key LIKE 'supervisor-callback:%'
	`, cardID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func latestAttemptNumber(t *testing.T, s *Store, turnID uuid.UUID) int {
	t.Helper()
	var number int
	if err := s.pool.QueryRow(context.Background(), `
		SELECT attempt_number FROM turn_attempts
		WHERE turn_id=$1
		ORDER BY attempt_number DESC LIMIT 1
	`, turnID).Scan(&number); err != nil {
		t.Fatal(err)
	}
	return number
}
