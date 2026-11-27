package store

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/trigger"
)

func TestSupersededToolApprovalContext_HumanFollowupRetryAndDeduplication(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	// 刻意使用带时间偏移的 Go 侧模拟时钟，验证查询边界不依赖 Go 侧 started_at，
	// 而是与数据库 now() 写入的 resolved_at 保持时钟同源。
	now := time.Now().UTC()

	// 1. 第 1 轮 Turn 执行中，工具被安全守卫挂起并创建 pending 审批。
	turn1, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "执行高风险命令", "superseded-ctx-turn-1", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	claim1, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cardCtx1, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim1.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cardCtx1.SupersededToolApprovals) != 0 {
		t.Fatalf("first turn should have no superseded approvals, got %+v", cardCtx1.SupersededToolApprovals)
	}

	approval1, err := s.CreateToolApproval(ctx, CreateToolApprovalParams{
		WorkspaceID: f.WorkspaceID,
		CardID:      f.CardID,
		TurnID:      &turn1.Turn.ID,
		AttemptID:   &claim1.Attempt.ID,
		ToolCallID:  "call-1",
		ToolName:    "exec_command",
		Arguments:   map[string]any{"cmd": "git push origin main"},
		RiskLevel:   "high",
		Reversible:  false,
		Reason:      "推送受保护分支需要确认",
		ScopeType:   "device",
		ScopeTarget: "node-tianyi",
		ScopeLabel:  "设备 TianYi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAttempt(ctx, f.WorkspaceID, f.CardID, claim1.Attempt.ID, f.AgentID, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	// 同时创建另一张卡片并产生一条失效审批，验证查询严格按 card_id 隔离。
	otherCard, err := s.CreateTodoCard(ctx, f.WorkspaceID, f.ProjectID, f.HumanID, "其他卡片", "隔离校验", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	otherTurn, err := s.CreateHumanTurn(ctx, f.WorkspaceID, otherCard.ID, f.HumanID, "其他卡片第 1 轮", "other-turn-1", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	otherClaim, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(10*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateToolApproval(ctx, CreateToolApprovalParams{
		WorkspaceID: f.WorkspaceID,
		CardID:      otherCard.ID,
		TurnID:      &otherTurn.Turn.ID,
		AttemptID:   &otherClaim.Attempt.ID,
		ToolCallID:  "other-call-1",
		ToolName:    "file_edit",
		RiskLevel:   "medium",
		Reason:      "其他卡片审批",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAttempt(ctx, f.WorkspaceID, otherCard.ID, otherClaim.Attempt.ID, f.AgentID, now.Add(12*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, otherCard.ID, f.HumanID, "作废其他卡片审批", "other-turn-2", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	// 先消费掉其他卡片的第 2 轮，避免影响下面主卡片的 Claim 顺序。
	otherClaim2, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(15*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAttempt(ctx, f.WorkspaceID, otherCard.ID, otherClaim2.Attempt.ID, f.AgentID, now.Add(16*time.Second)); err != nil {
		t.Fatal(err)
	}

	// 2. 人类发送第 2 轮消息，同事务内自动将 approval1 标记为 superseded。
	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "换一种方案继续", "superseded-ctx-turn-2", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim2, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim2.Turn.CardID != f.CardID {
		t.Fatalf("expected claimed card %s, got %s", f.CardID, claim2.Turn.CardID)
	}

	// 第 2 轮 Attempt #1 必须准确拿到 approval1（且不包含其他卡片的审批）。
	cardCtx2, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim2.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cardCtx2.SupersededToolApprovals) != 1 {
		t.Fatalf("turn 2 attempt 1 expected 1 superseded approval, got %d: %+v", len(cardCtx2.SupersededToolApprovals), cardCtx2.SupersededToolApprovals)
	}
	got := cardCtx2.SupersededToolApprovals[0]
	if got.ID != approval1.ID || got.ToolName != "exec_command" || got.RiskLevel != "high" || got.ScopeLabel != "设备 TianYi" || got.Reason != "推送受保护分支需要确认" {
		t.Fatalf("unexpected superseded approval metadata: %+v", got)
	}
	if !strings.Contains(got.ResolutionComment, "旧审批已自动失效") || got.ResolvedAt.IsZero() {
		t.Fatalf("expected non-empty resolution_comment and resolved_at, got %+v", got)
	}

	// 3. 模拟第 2 轮 Attempt #1 技术故障失败并触发重试（Attempt #2），上下文快照必须保持一致。
	if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, claim2.Attempt.ID, domain.AttemptStatusFailed, "upstream timeout", now.Add(70*time.Second)); err != nil {
		t.Fatal(err)
	}
	retryRes, err := s.RetryAttempt(ctx, f.WorkspaceID, f.CardID, claim2.Attempt.ID, f.AgentID, domain.AttemptReasonRetry)
	if err != nil {
		t.Fatal(err)
	}
	claim2Retry, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(80*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim2Retry.Attempt.ID != retryRes.Attempt.ID {
		t.Fatalf("expected retry attempt %s, got %s", retryRes.Attempt.ID, claim2Retry.Attempt.ID)
	}
	cardCtx2Retry, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim2Retry.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cardCtx2Retry.SupersededToolApprovals) != 1 || cardCtx2Retry.SupersededToolApprovals[0].ID != approval1.ID {
		t.Fatalf("turn 2 retry expected identical superseded approval snapshot, got %+v", cardCtx2Retry.SupersededToolApprovals)
	}
	if err := s.CompleteAttempt(ctx, f.WorkspaceID, f.CardID, claim2Retry.Attempt.ID, f.AgentID, now.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}

	// 4. 进入第 3 轮：第 2 轮已经同步过的旧失效审批不得再次重复注入。
	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "确认最终结果", "superseded-ctx-turn-3", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim3, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(2*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cardCtx3, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim3.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cardCtx3.SupersededToolApprovals) != 0 {
		t.Fatalf("turn 3 should not repeat previously delivered superseded approvals, got %+v", cardCtx3.SupersededToolApprovals)
	}
}

func TestSupersededToolApprovalContext_ExternalWebhookWatcherWakeup(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	testKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := s.ConfigureTriggerEncryption(testKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.ConfigureTriggerEncryption("")
	})

	now := time.Now().UTC()

	// 1. 第 1 轮挂起产生 pending 审批，同时注册外部事件 Watcher。
	turn1, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "部署并等待 CI", "watcher-turn-1", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	claim1, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := s.CreateToolApproval(ctx, CreateToolApprovalParams{
		WorkspaceID: f.WorkspaceID,
		CardID:      f.CardID,
		TurnID:      &turn1.Turn.ID,
		AttemptID:   &claim1.Attempt.ID,
		ToolCallID:  "call-deploy",
		ToolName:    "exec_command",
		RiskLevel:   "high",
		Reason:      "执行发布脚本",
		ScopeType:   "device",
		ScopeTarget: "node-prod",
		ScopeLabel:  "设备 Prod",
	})
	if err != nil {
		t.Fatal(err)
	}

	matchKey := "github:uvwt/cycledock:pr:112:ci_completed"
	if _, err := s.CreateCardWatcher(ctx, CreateCardWatcherParams{
		WorkspaceID:        f.WorkspaceID,
		CardID:             f.CardID,
		Kind:               domain.WatcherKindEvent,
		MatchKey:           matchKey,
		Note:               "检查 CI 结果",
		DispatchMode:       domain.DispatchModeEnqueue,
		ExpiresAt:          now.Add(time.Hour),
		CreatedBySourceID:  uuid.NullUUID{UUID: f.AgentID, Valid: true},
		CreatedByAttemptID: uuid.NullUUID{UUID: claim1.Attempt.ID, Valid: true},
		IdempotencyKey:     "watch-ci-112",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAttempt(ctx, f.WorkspaceID, f.CardID, claim1.Attempt.ID, f.AgentID, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	// 2. 外部 Webhook 事件到达匹配 Watcher，触发第 2 轮并自动将旧审批标记为 superseded。
	trig, _, err := s.CreateTrigger(ctx, f.WorkspaceID, f.HumanID, CreateTriggerInput{
		Name:        "GitHub CI",
		Provider:    domain.TriggerProviderGitHub,
		RoutingMode: domain.TriggerRoutingMatchWatchers,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.ProcessVerifiedWebhookDelivery(ctx, trig, trigger.ParsedEvent{
		Provider:   domain.TriggerProviderGitHub,
		DeliveryID: "delivery-" + uuid.NewString(),
		EventType:  "workflow_run",
		Action:     "completed",
		Conclusion: "success",
		Repository: "uvwt/CycleDock",
		Summary:    "CI passed",
		MatchKeys:  []string{matchKey},
	}, now.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.WokenCardIDs) != 1 || res.WokenCardIDs[0] != f.CardID {
		t.Fatalf("expected watcher to wake card %s, got %+v", f.CardID, res)
	}

	// 3. 第 2 轮加载上下文时必须拿到这条被外部事件作废的审批。
	claim2, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(30*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cardCtx2, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim2.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cardCtx2.SupersededToolApprovals) != 1 {
		t.Fatalf("expected 1 superseded approval on webhook wakeup turn, got %+v", cardCtx2.SupersededToolApprovals)
	}
	if cardCtx2.SupersededToolApprovals[0].ID != approval.ID {
		t.Fatalf("expected approval %s, got %s", approval.ID, cardCtx2.SupersededToolApprovals[0].ID)
	}
	if !strings.Contains(cardCtx2.SupersededToolApprovals[0].ResolutionComment, "外部事件唤醒") {
		t.Fatalf("unexpected resolution_comment: %q", cardCtx2.SupersededToolApprovals[0].ResolutionComment)
	}
}

func TestSupersededToolApprovalContext_ExcludesApprovedAndRejected(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	now := time.Now().UTC()

	// 第 1 轮产生两条审批：一条被人工批准，一条被人工拒绝（都不是 superseded）。
	turn1, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "测试已审批和已拒绝状态", "resolved-turn-1", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	claim1, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approvedRec, err := s.CreateToolApproval(ctx, CreateToolApprovalParams{
		WorkspaceID: f.WorkspaceID,
		CardID:      f.CardID,
		TurnID:      &turn1.Turn.ID,
		AttemptID:   &claim1.Attempt.ID,
		ToolCallID:  "call-approved",
		ToolName:    "exec_command",
		RiskLevel:   "medium",
		Reason:      "将被批准",
	})
	if err != nil {
		t.Fatal(err)
	}
	rejectedRec, err := s.CreateToolApproval(ctx, CreateToolApprovalParams{
		WorkspaceID: f.WorkspaceID,
		CardID:      f.CardID,
		TurnID:      &turn1.Turn.ID,
		AttemptID:   &claim1.Attempt.ID,
		ToolCallID:  "call-rejected",
		ToolName:    "file_edit",
		RiskLevel:   "high",
		Reason:      "将被拒绝",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAttempt(ctx, f.WorkspaceID, f.CardID, claim1.Attempt.ID, f.AgentID, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ResolveToolApproval(ctx, ResolveToolApprovalParams{
		WorkspaceID:        f.WorkspaceID,
		CardID:             f.CardID,
		ApprovalID:         approvedRec.ID,
		Status:             string(domain.ToolApprovalStatusApproved),
		Comment:            "同意执行",
		ResolvedBySourceID: f.HumanID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveToolApproval(ctx, ResolveToolApprovalParams{
		WorkspaceID:        f.WorkspaceID,
		CardID:             f.CardID,
		ApprovalID:         rejectedRec.ID,
		Status:             string(domain.ToolApprovalStatusRejected),
		Comment:            "不要修改此文件",
		ResolvedBySourceID: f.HumanID,
	}); err != nil {
		t.Fatal(err)
	}

	// 进入第 2 轮：approved 与 rejected 均不得被误当成 superseded 注入。
	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "继续推进", "resolved-turn-2", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim2, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now.Add(30*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cardCtx2, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim2.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cardCtx2.SupersededToolApprovals) != 0 {
		t.Fatalf("approved/rejected approvals must not be injected as superseded, got %+v", cardCtx2.SupersededToolApprovals)
	}
}
