package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	adkagent "google.golang.org/adk/v2/agent"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestBuildADKTools_ListCards权限边界(t *testing.T) {
	hasTool := func(t *testing.T, ctx store.AgentCardContext, name string) bool {
		t.Helper()
		tools, err := buildADKTools(attemptToolRuntime{context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range tools {
			if item.Name() == name {
				return true
			}
		}
		return false
	}

	// 1. 普通 Discussion 卡片：可见 cycledock_list_cards
	if !hasTool(t, store.AgentCardContext{CardKind: domain.CardKindDiscussion}, "cycledock_list_cards") {
		t.Fatal("expected cycledock_list_cards to be mounted on discussion cards")
	}

	// 2. IsBuiltInProactive 主聊天：放行只读 cycledock_list_cards，且不暴露写工具
	proactiveCtx := store.AgentCardContext{
		CardKind:           domain.CardKindDiscussion,
		IsMainDiscussion:   true,
		IsBuiltInProactive: true,
	}
	if !hasTool(t, proactiveCtx, "cycledock_list_cards") {
		t.Fatal("expected cycledock_list_cards to be mounted on built-in proactive main chat turns")
	}
	for _, forbidden := range []string{
		"cycledock_generate_work",
		"cycledock_update_card",
		"cycledock_send_card_message",
		"cycledock_create_schedule",
		"cycledock_inspect_card_session",
	} {
		if hasTool(t, proactiveCtx, forbidden) {
			t.Fatalf("built-in proactive mode must not expose %s", forbidden)
		}
	}

	// 3. Todo / Ongoing 卡片：不可见 cycledock_list_cards
	if hasTool(t, store.AgentCardContext{CardKind: domain.CardKindTodo}, "cycledock_list_cards") {
		t.Fatal("cycledock_list_cards must not be mounted on todo cards")
	}
	if hasTool(t, store.AgentCardContext{CardKind: domain.CardKindOngoing}, "cycledock_list_cards") {
		t.Fatal("cycledock_list_cards must not be mounted on ongoing cards")
	}
}

func TestListCardsTool_过滤参数与严格Workspace隔离(t *testing.T) {
	dataStore, _ := newAgentIntegrationStore(t)
	ctx := context.Background()

	wsA, err := dataStore.CreateWorkspace(ctx, "List Tool WS A")
	if err != nil {
		t.Fatal(err)
	}
	humanA, err := dataStore.CreateSource(ctx, wsA.ID, domain.SourceKindHuman, "Human A")
	if err != nil {
		t.Fatal(err)
	}
	agentA, err := dataStore.CreateSource(ctx, wsA.ID, domain.SourceKindCycleAgent, "Agent A")
	if err != nil {
		t.Fatal(err)
	}
	projA1, err := dataStore.CreateProject(ctx, wsA.ID, "主项目", "")
	if err != nil {
		t.Fatal(err)
	}
	projA2, err := dataStore.CreateProject(ctx, wsA.ID, "副项目", "")
	if err != nil {
		t.Fatal(err)
	}
	mainChat, _, err := dataStore.EnsureMainDiscussion(ctx, wsA.ID, projA1.ID, humanA.ID)
	if err != nil {
		t.Fatal(err)
	}

	// 其他工作区数据，用于验证严格隔离
	wsB, err := dataStore.CreateWorkspace(ctx, "List Tool WS B")
	if err != nil {
		t.Fatal(err)
	}
	humanB, err := dataStore.CreateSource(ctx, wsB.ID, domain.SourceKindHuman, "Human B")
	if err != nil {
		t.Fatal(err)
	}
	projB, err := dataStore.CreateProject(ctx, wsB.ID, "隔离项目", "")
	if err != nil {
		t.Fatal(err)
	}
	wsBCard, err := dataStore.CreateTodoCard(ctx, wsB.ID, projB.ID, humanB.ID, "Secret Cross-WS Card", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	// 准备 Project A1 / A2 卡片
	todo1, err := dataStore.CreateTodoCard(ctx, wsA.ID, projA1.ID, humanA.ID, "实现 API 限流", "给登录接口增加限流", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	todoDone, err := dataStore.CreateTodoCard(ctx, wsA.ID, projA1.ID, humanA.ID, "已完成的限流调研", "done", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MoveTodo(ctx, wsA.ID, todoDone.ID, humanA.ID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MoveTodo(ctx, wsA.ID, todoDone.ID, humanA.ID, domain.TodoStateReview); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MoveTodo(ctx, wsA.ID, todoDone.ID, humanA.ID, domain.TodoStateDone); err != nil {
		t.Fatal(err)
	}

	ongoingWarn, err := dataStore.CreateOngoingCard(ctx, wsA.ID, projA1.ID, humanA.ID, "网关延迟监控", "监控延迟", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, wsA.ID, ongoingWarn.ID, humanA.ID, "巡检", "warn-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	warnClaim, err := dataStore.ClaimCardTurnAttempt(ctx, wsA.ID, ongoingWarn.ID, agentA.ID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	warnStatus := domain.CheckinWarning
	if _, _, err := dataStore.AddAttemptEvent(ctx, store.AttemptEventParams{
		WorkspaceID:    wsA.ID,
		CardID:         ongoingWarn.ID,
		AttemptID:      warnClaim.Attempt.ID,
		RunnerSourceID: agentA.ID,
		Type:           domain.EventTypeCheckin,
		Content:        "P99 延迟升高",
		Checkin:        &warnStatus,
		IdempotencyKey: "warn-checkin",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.CompleteAttempt(ctx, wsA.ID, ongoingWarn.ID, warnClaim.Attempt.ID, agentA.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	ongoingHealthy, err := dataStore.CreateOngoingCard(ctx, wsA.ID, projA1.ID, humanA.ID, "健康备份监控", "备份监控", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, wsA.ID, ongoingHealthy.ID, humanA.ID, "巡检", "ok-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	okClaim, err := dataStore.ClaimCardTurnAttempt(ctx, wsA.ID, ongoingHealthy.ID, agentA.ID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	healthyStatus := domain.CheckinHealthy
	if _, _, err := dataStore.AddAttemptEvent(ctx, store.AttemptEventParams{
		WorkspaceID:    wsA.ID,
		CardID:         ongoingHealthy.ID,
		AttemptID:      okClaim.Attempt.ID,
		RunnerSourceID: agentA.ID,
		Type:           domain.EventTypeCheckin,
		Content:        "正常",
		Checkin:        &healthyStatus,
		IdempotencyKey: "ok-checkin",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.CompleteAttempt(ctx, wsA.ID, ongoingHealthy.ID, okClaim.Attempt.ID, agentA.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	projA2Card, err := dataStore.CreateTodoCard(ctx, wsA.ID, projA2.ID, humanA.ID, "副项目待办", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	toolCtx := adkagent.NewStrictContextMock(ctx)

	// Todo / Ongoing 卡片即使直接调用 handler 也必须被拒绝
	for _, kind := range []domain.CardKind{domain.CardKindTodo, domain.CardKindOngoing} {
		workRuntime := attemptToolRuntime{
			store: dataStore,
			context: store.AgentCardContext{
				WorkspaceID: wsA.ID,
				ProjectID:   projA1.ID,
				CardID:      todo1.ID,
				CardKind:    kind,
			},
		}
		workHandler, err := workRuntime.internalHandler("cycledock_list_cards")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := workHandler(&toolCtx, map[string]any{}); err == nil {
			t.Fatalf("expected error when %s card invokes cycledock_list_cards", kind)
		}
	}

	// Discussion 卡片（同时在普通与 IsBuiltInProactive 模式下）正常调用
	discRuntime := attemptToolRuntime{
		store: dataStore,
		context: store.AgentCardContext{
			WorkspaceID:        wsA.ID,
			ProjectID:          projA1.ID,
			CardID:             mainChat.ID,
			CardKind:           domain.CardKindDiscussion,
			IsMainDiscussion:   true,
			IsBuiltInProactive: true,
		},
	}
	handler, err := discRuntime.internalHandler("cycledock_list_cards")
	if err != nil {
		t.Fatal(err)
	}

	// 1. 默认查当前项目，排除 done，异常卡片排首位
	out, err := handler(&toolCtx, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	cards := out["cards"].([]map[string]any)
	if out["count"].(int) != len(cards) || out["truncated"].(bool) {
		t.Fatalf("unexpected metadata: %+v", out)
	}
	if len(cards) < 3 || cards[0]["card_id"] != ongoingWarn.ID.String() {
		t.Fatalf("expected abnormal ongoing card first, got %+v", cards)
	}
	for _, c := range cards {
		if c["card_id"] == todoDone.ID.String() || c["card_id"] == projA2Card.ID.String() || c["card_id"] == wsBCard.ID.String() {
			t.Fatalf("unexpected card in default project scope: %+v", c)
		}
	}

	// 2. health=abnormal 只返回 warning/critical
	abnOut, err := handler(&toolCtx, map[string]any{"health": "abnormal"})
	if err != nil {
		t.Fatal(err)
	}
	abnCards := abnOut["cards"].([]map[string]any)
	if len(abnCards) != 1 || abnCards[0]["card_id"] != ongoingWarn.ID.String() || abnCards[0]["health"] != "warning" {
		t.Fatalf("health=abnormal mismatch: %+v", abnCards)
	}

	// 3. scope=workspace + query + include_done
	wsOut, err := handler(&toolCtx, map[string]any{
		"scope":        "workspace",
		"query":        "限流",
		"include_done": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wsCards := wsOut["cards"].([]map[string]any)
	if len(wsCards) != 2 {
		t.Fatalf("expected 2 cards matching query with include_done=true, got %+v", wsCards)
	}

	// scope=workspace 必须能看到副项目，但绝不能看到 Workspace B
	allWsOut, err := handler(&toolCtx, map[string]any{"scope": "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	var sawProjA2 bool
	for _, c := range allWsOut["cards"].([]map[string]any) {
		if c["card_id"] == projA2Card.ID.String() {
			sawProjA2 = true
		}
		if c["card_id"] == wsBCard.ID.String() {
			t.Fatalf("cross-workspace card leaked in cycledock_list_cards: %+v", c)
		}
	}
	if !sawProjA2 {
		t.Fatal("expected projA2Card in scope=workspace")
	}

	// 4. limit 截断 + 非法参数校验
	limOut, err := handler(&toolCtx, map[string]any{"limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if limOut["count"].(int) != 1 || limOut["total_matched"].(int) < 2 || !limOut["truncated"].(bool) {
		t.Fatalf("unexpected limit truncation result: %+v", limOut)
	}
	if _, err := handler(&toolCtx, map[string]any{"limit": 0}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected limit validation error, got %v", err)
	}
}
