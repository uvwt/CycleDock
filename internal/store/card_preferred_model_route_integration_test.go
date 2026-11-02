package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func TestCardPreferredModelRouteIntegration(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindDiscussion)
	ctx := context.Background()

	// 1. 创建模型路由
	route, err := s.queries.CreateWorkspaceModelRoute(ctx, db.CreateWorkspaceModelRouteParams{
		ID:              uuid.New(),
		WorkspaceID:     f.WorkspaceID,
		Position:        1,
		ProviderType:    "openai_compatible",
		ProviderBaseUrl: "https://api.openai.com/v1",
		Model:           "gpt-4o",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("create model route: %v", err)
	}

	// 2. 为卡片绑定优先模型路由
	updated, err := s.SetCardPreferredModelRoute(ctx, f.WorkspaceID, f.CardID, uuid.NullUUID{UUID: route.ID, Valid: true})
	if err != nil {
		t.Fatalf("set card preferred model route: %v", err)
	}
	if !updated.PreferredModelRouteID.Valid || updated.PreferredModelRouteID.UUID != route.ID {
		t.Fatalf("preferred route not set: %+v", updated.PreferredModelRouteID)
	}

	// 校验读取
	got, err := s.GetCard(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if !got.PreferredModelRouteID.Valid || got.PreferredModelRouteID.UUID != route.ID {
		t.Fatalf("got card route mismatch: %+v", got.PreferredModelRouteID)
	}

	// 3. 测试通过 CreateDiscussion 直接指定模型路由
	newCard, err := s.CreateDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID, uuid.NullUUID{UUID: route.ID, Valid: true})
	if err != nil {
		t.Fatalf("create discussion with route: %v", err)
	}
	if !newCard.PreferredModelRouteID.Valid || newCard.PreferredModelRouteID.UUID != route.ID {
		t.Fatalf("new discussion route mismatch: %+v", newCard.PreferredModelRouteID)
	}

	// 4. 清除卡片模型路由（恢复跟随默认）
	cleared, err := s.SetCardPreferredModelRoute(ctx, f.WorkspaceID, f.CardID, uuid.NullUUID{Valid: false})
	if err != nil {
		t.Fatalf("clear card preferred model route: %v", err)
	}
	if cleared.PreferredModelRouteID.Valid {
		t.Fatalf("expected cleared route, got valid: %+v", cleared.PreferredModelRouteID)
	}

	// 5. 跨工作区模型路由拒绝绑定
	otherWorkspaceID := uuid.New()
	if _, err := s.SetCardPreferredModelRoute(ctx, otherWorkspaceID, f.CardID, uuid.NullUUID{UUID: route.ID, Valid: true}); err == nil {
		t.Fatal("expected error for wrong workspace, got nil")
	}
}

func TestLoadAgentCardContextInheritsCardPreferredModelRoute(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindDiscussion)
	ctx := context.Background()

	cardRoute, err := s.queries.CreateWorkspaceModelRoute(ctx, db.CreateWorkspaceModelRouteParams{
		ID:              uuid.New(),
		WorkspaceID:     f.WorkspaceID,
		Position:        1,
		ProviderType:    "openai_compatible",
		ProviderBaseUrl: "https://api.openai.com/v1",
		Model:           "card-model",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("create card route: %v", err)
	}

	turnOverrideRoute, err := s.queries.CreateWorkspaceModelRoute(ctx, db.CreateWorkspaceModelRouteParams{
		ID:              uuid.New(),
		WorkspaceID:     f.WorkspaceID,
		Position:        2,
		ProviderType:    "openai_compatible",
		ProviderBaseUrl: "https://api.openai.com/v1",
		Model:           "turn-override-model",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("create turn override route: %v", err)
	}

	// 设置卡片优先模型
	if _, err := s.SetCardPreferredModelRoute(ctx, f.WorkspaceID, f.CardID, uuid.NullUUID{UUID: cardRoute.ID, Valid: true}); err != nil {
		t.Fatalf("set card route: %v", err)
	}

	// 提交一个没有指定模型的 turn（应该继承 cardRoute）
	res1, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AuthorSourceID: f.HumanID,
		Content:        "Turn 1 without override",
		IdempotencyKey: uuid.NewString(),
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatalf("submit turn 1: %v", err)
	}

	claim1, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim turn 1: %v", err)
	}
	if claim1.Turn.ID != res1.TurnID {
		t.Fatalf("claimed wrong turn: got %v, want %v", claim1.Turn.ID, res1.TurnID)
	}

	cardContext1, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim1.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatalf("load card context 1: %v", err)
	}
	if !cardContext1.PreferredModelRouteID.Valid || cardContext1.PreferredModelRouteID.UUID != cardRoute.ID {
		t.Fatalf("turn 1 should inherit cardRoute: got %+v, want %v", cardContext1.PreferredModelRouteID, cardRoute.ID)
	}

	// 结束 attempt 1
	if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, claim1.Attempt.ID, domain.AttemptStatusCompleted, "", time.Now().UTC()); err != nil {
		t.Fatalf("finish attempt 1: %v", err)
	}

	// 提交一个带显式覆盖的 turn（应该覆盖 cardRoute）
	res2, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:           f.WorkspaceID,
		CardID:                f.CardID,
		AuthorSourceID:        f.HumanID,
		Content:               "Turn 2 with override",
		IdempotencyKey:        uuid.NewString(),
		DispatchMode:          domain.DispatchModeEnqueue,
		PreferredModelRouteID: &turnOverrideRoute.ID,
	})
	if err != nil {
		t.Fatalf("submit turn 2: %v", err)
	}

	claim2, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim turn 2: %v", err)
	}
	if claim2.Turn.ID != res2.TurnID {
		t.Fatalf("claimed wrong turn: got %v, want %v", claim2.Turn.ID, res2.TurnID)
	}

	cardContext2, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim2.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatalf("load card context 2: %v", err)
	}
	if !cardContext2.PreferredModelRouteID.Valid || cardContext2.PreferredModelRouteID.UUID != turnOverrideRoute.ID {
		t.Fatalf("turn 2 should use turnOverrideRoute: got %+v, want %v", cardContext2.PreferredModelRouteID, turnOverrideRoute.ID)
	}
}
