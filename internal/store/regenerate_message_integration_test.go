package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestRegenerateSessionMessage_AI气泡指定模型重跑并截断后续轮次(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindDiscussion)
	ctx := context.Background()

	savedSettings, err := s.SaveAgentSettings(ctx, f.WorkspaceID, AgentSettingsInput{
		Routes: []ModelRouteInput{
			{
				ProviderType:    "openai_compatible",
				ProviderBaseURL: "https://api.openai.com/v1",
				Model:           "default-model",
				Enabled:         true,
			},
			{
				ProviderType:    "openai_compatible",
				ProviderBaseURL: "https://api.openai.com/v1",
				Model:           "override-model",
				Enabled:         true,
			},
			{
				ProviderType:    "openai_compatible",
				ProviderBaseURL: "https://api.openai.com/v1",
				Model:           "disabled-model",
				Enabled:         false,
			},
		},
	})
	if err != nil {
		t.Fatalf("save routes: %v", err)
	}
	defaultRoute := savedSettings.Routes[0]
	overrideRoute := savedSettings.Routes[1]
	disabledRoute := savedSettings.Routes[2]

	// 设置卡片默认路由为 defaultRoute，验证重跑不会篡改卡片默认路由。
	if _, err := s.SetCardPreferredModelRoute(ctx, f.WorkspaceID, f.CardID, uuid.NullUUID{UUID: defaultRoute.ID, Valid: true}); err != nil {
		t.Fatalf("set card route: %v", err)
	}

	// 第 1 轮对话
	q1, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "Question 1", IdempotencyKey: "regen-q1", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim1, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	a1EventID, _, err := s.PublishReply(ctx, PublishReplyParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim1.Attempt.ID, RunnerSourceID: f.AgentID,
		Model:   "default-model",
		Message: ReplyMessage{Type: "text", Content: "Answer 1", EndTurn: true},
		IdempotencyKey: "regen-a1", FinishOnEnd: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 第 2 轮对话（后续轮次，在从第 1 轮 AI 气泡重跑时应被截断）
	_, err = s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "Question 2", IdempotencyKey: "regen-q2", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim2, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// 故意让第 2 轮保持 running 状态，验证卡片正在运行时重跑旧气泡能安全清理旧 Attempt。
	if err := s.SaveReplyPreview(ctx, ReplyPreviewParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim2.Attempt.ID, RunnerSourceID: f.AgentID,
		PreviewID: "gen:streaming", Content: "Partial answer 2",
	}); err != nil {
		t.Fatal(err)
	}

	// 校验非法路由被拒绝：禁用路由 -> ErrConflict
	if _, err := s.RegenerateSessionMessage(ctx, RegenerateSessionMessageParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, EventID: a1EventID, AuthorSourceID: f.HumanID,
		PreferredModelRouteID: &disabledRoute.ID,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict for disabled route, got %v", err)
	}

	// 校验跨工作区路由被拒绝 -> ErrNotFound
	otherFixture := newFixture(t, s, domain.CardKindDiscussion)
	otherSettings, err := s.SaveAgentSettings(ctx, otherFixture.WorkspaceID, AgentSettingsInput{
		Routes: []ModelRouteInput{{
			ProviderType:    "openai_compatible",
			ProviderBaseURL: "https://api.openai.com/v1",
			Model:           "other-ws-model",
			Enabled:         true,
		}},
	})
	if err != nil {
		t.Fatalf("save other ws route: %v", err)
	}
	otherRouteID := otherSettings.Routes[0].ID
	if _, err := s.RegenerateSessionMessage(ctx, RegenerateSessionMessageParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, EventID: a1EventID, AuthorSourceID: f.HumanID,
		PreferredModelRouteID: &otherRouteID,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-workspace route, got %v", err)
	}

	// 从第 1 轮 AI 回复气泡触发指定模型重跑
	res, err := s.RegenerateSessionMessage(ctx, RegenerateSessionMessageParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, EventID: a1EventID, AuthorSourceID: f.HumanID,
		PreferredModelRouteID: &overrideRoute.ID,
	})
	if err != nil {
		t.Fatalf("RegenerateSessionMessage: %v", err)
	}
	if res.TurnID != q1.TurnID || res.EventID != q1.EventID {
		t.Fatalf("unexpected regenerate result: %+v want turn=%s event=%s", res, q1.TurnID, q1.EventID)
	}

	// 时间线仅保留第 1 轮用户消息，A1、Q2 以及正在流式输出的预览消息均已截断
	timeline, err := s.ListCardTimelineBefore(ctx, f.WorkspaceID, f.CardID, 1<<62-1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 1 || timeline[0].ID != q1.EventID {
		t.Fatalf("unexpected timeline after regenerate: len=%d rows=%+v", len(timeline), timeline)
	}

	// 卡片默认模型路由保持不变，未被单次覆盖污染
	cardAfter, err := s.GetCard(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	if !cardAfter.PreferredModelRouteID.Valid || cardAfter.PreferredModelRouteID.UUID != defaultRoute.ID {
		t.Fatalf("card preferred_model_route_id mutated: %+v want %s", cardAfter.PreferredModelRouteID, defaultRoute.ID)
	}

	// 重新领取第 1 轮，验证 LoadAgentCardContext 命中单次指定的 overrideRoute
	rerunClaim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("ClaimCardTurnAttempt after regenerate: %v", err)
	}
	if rerunClaim.Turn.ID != q1.TurnID {
		t.Fatalf("claimed turn=%s want %s", rerunClaim.Turn.ID, q1.TurnID)
	}
	cardCtx, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, rerunClaim.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatalf("LoadAgentCardContext: %v", err)
	}
	if !cardCtx.PreferredModelRouteID.Valid || cardCtx.PreferredModelRouteID.UUID != overrideRoute.ID {
		t.Fatalf("context preferred route=%+v want %s", cardCtx.PreferredModelRouteID, overrideRoute.ID)
	}

	// 从用户气泡以默认模型（PreferredModelRouteID=nil）再次重跑，应清除单次覆盖并回退到卡片默认路由
	if _, _, err := s.PublishReply(ctx, PublishReplyParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: rerunClaim.Attempt.ID, RunnerSourceID: f.AgentID,
		Model:   "override-model",
		Message: ReplyMessage{Type: "text", Content: "Answer 1 (override)", EndTurn: true},
		IdempotencyKey: "regen-a1-v2", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegenerateSessionMessage(ctx, RegenerateSessionMessageParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, EventID: q1.EventID, AuthorSourceID: f.HumanID,
		PreferredModelRouteID: nil,
	}); err != nil {
		t.Fatalf("RegenerateSessionMessage from user bubble with default model: %v", err)
	}
	rerunClaim2, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cardCtx2, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, rerunClaim2.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if !cardCtx2.PreferredModelRouteID.Valid || cardCtx2.PreferredModelRouteID.UUID != defaultRoute.ID {
		t.Fatalf("expected fallback to card default route %s, got %+v", defaultRoute.ID, cardCtx2.PreferredModelRouteID)
	}
}

func TestRegenerateSessionMessage_无人类源事件的系统触发轮次也可安全重跑(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindOngoing)
	ctx := context.Background()

	savedSettings, err := s.SaveAgentSettings(ctx, f.WorkspaceID, AgentSettingsInput{
		Routes: []ModelRouteInput{
			{
				ProviderType:    "openai_compatible",
				ProviderBaseURL: "https://api.openai.com/v1",
				Model:           "model-a",
				Enabled:         true,
			},
			{
				ProviderType:    "openai_compatible",
				ProviderBaseURL: "https://api.openai.com/v1",
				Model:           "model-b",
				Enabled:         true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	routeB := savedSettings.Routes[1]

	// 通过真实 schedule 创建并触发一条无 source_event_id 的定时轮次
	now := time.Now().UTC()
	rule := domain.ScheduleRule{Frequency: domain.ScheduleDaily, Hour: 9, Minute: 0, Timezone: "Asia/Shanghai"}
	schedule, err := s.CreateSchedule(ctx, f.WorkspaceID, f.CardID, rule, now)
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE card_schedules SET next_trigger_at = $2 WHERE id = $1
	`, schedule.ID, now.Add(-time.Minute)); err != nil {
		t.Fatalf("backdate schedule: %v", err)
	}
	_, created, err := s.ProcessDueScheduleTurn(ctx, f.WorkspaceID, schedule.ID, now)
	if err != nil {
		t.Fatalf("process due schedule turn: %v", err)
	}
	if !created {
		t.Fatal("expected scheduled turn to be created")
	}

	claim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	scheduleTurnID := claim.Turn.ID
	if claim.Turn.Source != domain.TurnSourceSchedule {
		t.Fatalf("claimed turn source=%s want schedule", claim.Turn.Source)
	}
	replyEventID, _, err := s.PublishReply(ctx, PublishReplyParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID, RunnerSourceID: f.AgentID,
		Model:   "model-a",
		Message: ReplyMessage{Type: "text", Content: "Scheduled report v1", EndTurn: true},
		IdempotencyKey: "sched-reply-1", FinishOnEnd: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 从该无人类源事件的 AI 回复气泡重跑并切换到 routeB
	res, err := s.RegenerateSessionMessage(ctx, RegenerateSessionMessageParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, EventID: replyEventID, AuthorSourceID: f.HumanID,
		PreferredModelRouteID: &routeB.ID,
	})
	if err != nil {
		t.Fatalf("RegenerateSessionMessage on schedule turn: %v", err)
	}
	if res.TurnID != scheduleTurnID {
		t.Fatalf("turn=%s want %s", res.TurnID, scheduleTurnID)
	}

	rerunClaim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("ClaimCardTurnAttempt after schedule regenerate: %v", err)
	}
	cardCtx, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, rerunClaim.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if !cardCtx.PreferredModelRouteID.Valid || cardCtx.PreferredModelRouteID.UUID != routeB.ID {
		t.Fatalf("expected routeB %s, got %+v", routeB.ID, cardCtx.PreferredModelRouteID)
	}
}

func TestRetryAttemptWithModelRoute_覆盖本轮优先模型路由(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindDiscussion)
	ctx := context.Background()

	savedSettings, err := s.SaveAgentSettings(ctx, f.WorkspaceID, AgentSettingsInput{
		Routes: []ModelRouteInput{
			{
				ProviderType:    "openai_compatible",
				ProviderBaseURL: "https://api.openai.com/v1",
				Model:           "model-1",
				Enabled:         true,
			},
			{
				ProviderType:    "openai_compatible",
				ProviderBaseURL: "https://api.openai.com/v1",
				Model:           "model-2",
				Enabled:         true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	route2 := savedSettings.Routes[1]

	if _, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "Retry with route2", IdempotencyKey: "retry-route-1", DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID, domain.AttemptStatusFailed, "timeout", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	retryRes, err := s.RetryAttemptWithModelRoute(ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID, f.AgentID, domain.AttemptReasonRetry, &route2.ID)
	if err != nil {
		t.Fatalf("RetryAttemptWithModelRoute: %v", err)
	}
	if retryRes.Attempt.AttemptNumber != 2 {
		t.Fatalf("attempt_number=%d want 2", retryRes.Attempt.AttemptNumber)
	}

	claim2, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cardCtx, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim2.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if !cardCtx.PreferredModelRouteID.Valid || cardCtx.PreferredModelRouteID.UUID != route2.ID {
		t.Fatalf("expected route2 %s, got %+v", route2.ID, cardCtx.PreferredModelRouteID)
	}
}
