package store

import (
	"context"
	"testing"
	"time"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestStatusChangeImplicitContext_上一轮执行以来的状态流转与重试一致性(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	now := time.Now().UTC()

	// 1. 卡片初始创建为 Todo 状态，发起第 1 轮 Turn
	turn1, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "Turn 1 request", "turn-1", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}

	claim1, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// 第 1 轮执行时，前序无任何已执行 Turn，不产生状态变更提示
	cardCtx1, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim1.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cardCtx1.StatusChanges) != 0 {
		t.Fatalf("first turn should have no status changes, got %d", len(cardCtx1.StatusChanges))
	}

	// 第 1 轮尝试成功完成
	if err := s.CompleteAttempt(ctx, f.WorkspaceID, f.CardID, claim1.Attempt.ID, f.AgentID, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	// 2. 用户在两轮之间更改卡片状态：todo -> working，然后 working -> review，最后 review -> working
	if err := s.MoveTodo(ctx, f.WorkspaceID, f.CardID, f.HumanID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTodo(ctx, f.WorkspaceID, f.CardID, f.HumanID, domain.TodoStateReview); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTodo(ctx, f.WorkspaceID, f.CardID, f.HumanID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}

	// 3. 用户发送第 2 轮 Turn
	turn2, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "Turn 2 request", "turn-2", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	_ = turn2

	claim2, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// 校验第 2 轮 Attempt 1 能够读取到这 3 次状态变更
	cardCtx2, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim2.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cardCtx2.StatusChanges) != 3 {
		t.Fatalf("turn 2 attempt 1 expected 3 status changes, got %d: %+v", len(cardCtx2.StatusChanges), cardCtx2.StatusChanges)
	}
	if cardCtx2.StatusChanges[0].Content != "todo -> working" ||
		cardCtx2.StatusChanges[1].Content != "working -> review" ||
		cardCtx2.StatusChanges[2].Content != "review -> working" {
		t.Fatalf("unexpected status changes order or content: %+v", cardCtx2.StatusChanges)
	}
	if cardCtx2.StatusChanges[0].ActorName != "Human" {
		t.Fatalf("expected actor name 'Human', got %q", cardCtx2.StatusChanges[0].ActorName)
	}

	// 4. 模拟第 2 轮 Attempt 1 技术故障失败并触发重试（Attempt 2）
	if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, claim2.Attempt.ID, domain.AttemptStatusFailed, "network timeout", now.Add(70*time.Second)); err != nil {
		t.Fatal(err)
	}

	retryResult, err := s.RetryAttempt(ctx, f.WorkspaceID, f.CardID, claim2.Attempt.ID, f.AgentID, domain.AttemptReasonRetry)
	if err != nil {
		t.Fatal(err)
	}

	claim2Retry, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(80*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim2Retry.Attempt.ID != retryResult.Attempt.ID {
		t.Fatalf("expected claimed attempt %v, got %v", retryResult.Attempt.ID, claim2Retry.Attempt.ID)
	}

	// 验证重试轮次 Attempt 2 获取的状态变更与 Attempt 1 完全一致且确定
	cardCtx2Retry, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim2Retry.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cardCtx2Retry.StatusChanges) != 3 {
		t.Fatalf("turn 2 retry expected 3 status changes, got %d: %+v", len(cardCtx2Retry.StatusChanges), cardCtx2Retry.StatusChanges)
	}
	for i := range cardCtx2.StatusChanges {
		if cardCtx2Retry.StatusChanges[i].Content != cardCtx2.StatusChanges[i].Content {
			t.Fatalf("status change mismatch between attempt 1 and retry attempt 2 at index %d: %s vs %s",
				i, cardCtx2.StatusChanges[i].Content, cardCtx2Retry.StatusChanges[i].Content)
		}
	}

	// 重试成功完成
	if err := s.CompleteAttempt(ctx, f.WorkspaceID, f.CardID, claim2Retry.Attempt.ID, f.AgentID, now.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}

	// 5. 用户未做任何状态变更，发起第 3 轮 Turn
	_, err = s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "Turn 3 request", "turn-3", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}

	claim3, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(100*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// 第 3 轮执行时，旧的状态变更已被消费，不应再重复出现
	cardCtx3, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim3.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cardCtx3.StatusChanges) != 0 {
		t.Fatalf("turn 3 should have no new status changes, got %d: %+v", len(cardCtx3.StatusChanges), cardCtx3.StatusChanges)
	}

	_ = turn1
}
