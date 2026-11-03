package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func builtinDispatchFixture(t *testing.T, s *Store, inputs int) testFixture {
	t.Helper()
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	settings, err := s.SaveAgentSettings(ctx, f.WorkspaceID, testAgentSettings("https://example.com", "model", ""))
	if err != nil {
		t.Fatal(err)
	}
	f.AgentID = settings.SourceID
	for i := 0; i < inputs; i++ {
		cardID := f.CardID
		if i > 0 {
			card, err := s.CreateTodoCard(ctx, f.WorkspaceID, f.ProjectID, f.HumanID,
				fmt.Sprintf("Work %d", i), "", uuid.NullUUID{}, 1)
			if err != nil {
				t.Fatal(err)
			}
			cardID = card.ID
		}
		if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, cardID, f.HumanID,
			"Run", fmt.Sprintf("input-%d", i), domain.DispatchModeEnqueue); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestBuiltInDispatchAlternatesWorkspacesAndSkipsFullTenant(t *testing.T) {
	s := requireIntegration(t)
	a := builtinDispatchFixture(t, s, 3)
	b := builtinDispatchFixture(t, s, 3)
	ctx := context.Background()
	now := time.Now().UTC()
	claims := make([]*AttemptClaim, 0, 4)
	for i := 0; i < 4; i++ {
		claim, err := s.ClaimNextBuiltInAttempt(ctx, 2, now.Add(time.Duration(i)*time.Second), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		claims = append(claims, claim)
	}
	if claims[0].Attempt.WorkspaceID == claims[1].Attempt.WorkspaceID ||
		claims[0].Attempt.WorkspaceID != claims[2].Attempt.WorkspaceID ||
		claims[1].Attempt.WorkspaceID != claims[3].Attempt.WorkspaceID {
		t.Fatalf("workspaces did not alternate: %v %v %v %v", claims[0].Attempt.WorkspaceID,
			claims[1].Attempt.WorkspaceID, claims[2].Attempt.WorkspaceID, claims[3].Attempt.WorkspaceID)
	}
	if _, err := s.ClaimNextBuiltInAttempt(ctx, 2, now, time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("full tenants accepted another execution: %v", err)
	}
	// 本轮完成即释放运行额度，同一租户的其他卡片可以继续。
	first := claims[0].Attempt
	if err := s.CompleteAttempt(ctx, first.WorkspaceID, first.CardID, first.ID, first.RunnerSourceID, now); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimNextBuiltInAttempt(ctx, 2, now.Add(5*time.Second), time.Minute)
	if err != nil || next.Attempt.WorkspaceID != first.WorkspaceID {
		t.Fatalf("available tenant was blocked: claim=%+v err=%v (tenants %v %v)", next, err, a.WorkspaceID, b.WorkspaceID)
	}
}

func TestBuiltInDispatchConcurrentClaimersShareWorkspaceQuota(t *testing.T) {
	s := requireIntegration(t)
	f := builtinDispatchFixture(t, s, 8)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 12)
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := s.ClaimNextBuiltInAttempt(ctx, 1, time.Now(), time.Minute)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	claimed := 0
	for err := range results {
		if err == nil {
			claimed++
		} else if !errors.Is(err, domain.ErrNotFound) {
			t.Fatal(err)
		}
	}
	var running int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM turn_attempts WHERE workspace_id=$1 AND status='running'`,
		f.WorkspaceID).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if claimed != 1 || running != 1 {
		t.Fatalf("quota exceeded: claimed=%d running=%d", claimed, running)
	}
}

func TestBuiltInDispatchQueuedRetryRespectsQuotaAndResumesOnRelease(t *testing.T) {
	s := requireIntegration(t)
	builtinDispatchFixture(t, s, 3)
	ctx := context.Background()
	now := time.Now().UTC()
	first, err := s.ClaimNextBuiltInAttempt(ctx, 1, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	a := first.Attempt
	if err := s.FinishAttempt(ctx, a.WorkspaceID, a.CardID, a.ID, domain.AttemptStatusFailed, "failure", now); err != nil {
		t.Fatal(err)
	}
	active, err := s.ClaimNextBuiltInAttempt(ctx, 1, now.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.RetryAttempt(ctx, a.WorkspaceID, a.CardID, a.ID, a.RunnerSourceID, domain.AttemptReasonRetry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNextBuiltInAttempt(ctx, 1, now, time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("queued retry bypassed quota: %v", err)
	}
	a = active.Attempt
	if err := s.FinishAttempt(ctx, a.WorkspaceID, a.CardID, a.ID, domain.AttemptStatusCompleted, "", now); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.ClaimNextBuiltInAttempt(ctx, 1, now.Add(2*time.Second), time.Minute)
	if err != nil || resumed.Attempt.ID != retry.Attempt.ID || resumed.Turn.ID != first.Turn.ID {
		t.Fatalf("retry did not resume: %+v err=%v", resumed, err)
	}
}

func TestBuiltInDispatchSkipsLockedAndDisabledWorkspaces(t *testing.T) {
	s := requireIntegration(t)
	locked := builtinDispatchFixture(t, s, 1)
	disabled := builtinDispatchFixture(t, s, 1)
	ready := builtinDispatchFixture(t, s, 1)
	ctx := context.Background()
	settings := testAgentSettings("https://example.com", "model", "")
	settings.Routes[0].Enabled = false
	if _, err := s.SaveAgentSettings(ctx, disabled.WorkspaceID, settings); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM workspace_agent_settings WHERE workspace_id=$1 FOR UPDATE`, locked.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	claimCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	claim, err := s.ClaimNextBuiltInAttempt(claimCtx, 1, time.Now(), time.Minute)
	if err != nil || claim.Attempt.WorkspaceID != ready.WorkspaceID {
		t.Fatalf("available workspace blocked by other tenants: claim=%+v err=%v", claim, err)
	}
}

func TestBuiltInDispatchResumesQueuedMessageAfterModelSetup(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindDiscussion)
	ctx := context.Background()
	turn, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "保留这条消息", "before-model-setup", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNextBuiltInAttempt(ctx, 1, time.Now(), time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unconfigured workspace claim error=%v", err)
	}
	if _, err := s.SaveAgentSettings(ctx, f.WorkspaceID, testAgentSettings("https://example.com", "model", "")); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimNextBuiltInAttempt(ctx, 1, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Turn.ID != turn.Turn.ID || claim.Turn.Content != "保留这条消息" {
		t.Fatalf("queued message changed: %+v", claim.Turn)
	}
}
