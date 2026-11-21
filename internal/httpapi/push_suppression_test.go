package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestIsNonCircuitSupervisorFailureCallback(t *testing.T) {
	childID := uuid.New()
	attemptID := uuid.New()

	cases := []struct {
		name           string
		source         string
		idempotencyKey string
		content        string
		want           bool
	}{
		{
			name:           "非熔断失败回调（带结构化 circuit_open=false）",
			source:         string(domain.TurnSourceSystem),
			idempotencyKey: store.SupervisorFailureCallbackKey(childID, attemptID),
			content:        "子任务终态回调：测试任务执行失败。\noutcome=failed\nattempt=1/5\nidentical_error_count=1\ncircuit_open=false\nerror_summary=上游接口触发熔断\n错误与上次不同，可调整方案后用 cycledock_send_card_message 让子任务继续。",
			want:           true,
		},
		{
			name:           "熔断失败回调（带结构化 circuit_open=true）",
			source:         string(domain.TurnSourceSystem),
			idempotencyKey: store.SupervisorFailureCallbackKey(childID, attemptID),
			content:        "子任务终态回调：测试任务执行失败。\noutcome=failed\nattempt=2/5\nidentical_error_count=2\ncircuit_open=true\nerror_summary=port refused\n熔断：连续两次错误摘要相同，停止自动追聊，改为向用户总结。",
			want:           false,
		},
		{
			name:           "历史格式非熔断失败回调（无 circuit_open 行）",
			source:         string(domain.TurnSourceSystem),
			idempotencyKey: store.SupervisorFailureCallbackKey(childID, attemptID),
			content:        "子任务终态回调：测试任务执行失败。\noutcome=expired\nattempt=1/5\nidentical_error_count=1\nerror_summary=execution lease expired\n错误与上次不同，可调整方案后用 cycledock_send_card_message 让子任务继续。",
			want:           true,
		},
		{
			name:           "历史格式熔断失败回调（无 circuit_open 行）",
			source:         string(domain.TurnSourceSystem),
			idempotencyKey: store.SupervisorFailureCallbackKey(childID, attemptID),
			content:        "子任务终态回调：测试任务执行失败。\noutcome=failed\nattempt=5/5\nidentical_error_count=1\nerror_summary=timeout\n熔断：已达到 5 次动态自愈预算，停止自动追聊，改为向用户总结。",
			want:           false,
		},
		{
			name:           "Review 完成回调不得静默",
			source:         string(domain.TurnSourceSystem),
			idempotencyKey: store.SupervisorReviewCallbackKey(childID, 1),
			content:        "子任务终态回调：测试任务已进入 Review。\noutcome=review\n请向用户总结结果。",
			want:           false,
		},
		{
			name:           "普通人工对话不得静默",
			source:         string(domain.TurnSourceHuman),
			idempotencyKey: "human-turn-1",
			content:        "帮我看一下进展",
			want:           false,
		},
		{
			name:           "定时主动消息不得静默",
			source:         string(domain.TurnSourceSchedule),
			idempotencyKey: "schedule:1",
			content:        "定时检查",
			want:           false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := store.IsNonCircuitSupervisorFailureCallback(tc.source, tc.idempotencyKey, tc.content)
			if got != tc.want {
				t.Fatalf("IsNonCircuitSupervisorFailureCallback = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsSupervisorNotificationSuppressedRules(t *testing.T) {
	cardID := uuid.New()
	supID := uuid.New()
	attemptID := uuid.New()
	otherAttemptID := uuid.New()

	baseReviewFacts := cardNotificationFacts{
		CardID:             cardID,
		Kind:               string(domain.CardKindTodo),
		TodoState:          string(domain.TodoStateReview),
		ConfirmationRound:  1,
		EventType:          "message",
		MessageType:        "text",
		MessageSeq:         11,
		MessageAttemptID:   uuid.NullUUID{UUID: attemptID, Valid: true},
		ReviewEventSeq:     10,
		ReviewAttemptID:    uuid.NullUUID{UUID: attemptID, Valid: true},
		SupervisorID:       uuid.NullUUID{UUID: supID, Valid: true},
		SupervisorMuted:    false,
		HasReviewCallback:  true,
		HasFailureCallback: false,
	}

	if !isSupervisorNotificationSuppressed(baseReviewFacts) {
		t.Fatal("expected Todo review with active unmuted supervisor and matching attempt to be suppressed")
	}

	// 规则 1：choice 和待工具审批永远不抑制
	choiceFacts := baseReviewFacts
	choiceFacts.MessageType = "choice"
	if isSupervisorNotificationSuppressed(choiceFacts) {
		t.Fatal("choice message must never be suppressed")
	}

	approvalFacts := baseReviewFacts
	approvalFacts.PendingApprovalCount = 1
	if isSupervisorNotificationSuppressed(approvalFacts) {
		t.Fatal("pending tool approval must never be suppressed")
	}

	toolApprovalEventFacts := baseReviewFacts
	toolApprovalEventFacts.EventType = "tool_approval"
	if isSupervisorNotificationSuppressed(toolApprovalEventFacts) {
		t.Fatal("tool_approval event must never be suppressed")
	}

	// 规则 2 防漏兜底：监督讨论静音、缺失或缺少回调 Turn 时不得抑制
	mutedSup := baseReviewFacts
	mutedSup.SupervisorMuted = true
	if isSupervisorNotificationSuppressed(mutedSup) {
		t.Fatal("muted supervisor must fall back to notifying child card")
	}

	noSup := baseReviewFacts
	noSup.SupervisorID = uuid.NullUUID{}
	if isSupervisorNotificationSuppressed(noSup) {
		t.Fatal("missing supervisor must fall back to notifying child card")
	}

	noCallback := baseReviewFacts
	noCallback.HasReviewCallback = false
	if isSupervisorNotificationSuppressed(noCallback) {
		t.Fatal("missing review callback turn must fall back to notifying child card")
	}

	// 规则 2 边界：进入 Review 后由新 Attempt 产生的追问回复不得抑制
	followUpFacts := baseReviewFacts
	followUpFacts.MessageSeq = 20
	followUpFacts.MessageAttemptID = uuid.NullUUID{UUID: otherAttemptID, Valid: true}
	if isSupervisorNotificationSuppressed(followUpFacts) {
		t.Fatal("post-review follow-up reply must not be suppressed")
	}

	// 规则 2B：Ongoing 失败且回调成功时静默
	ongoingFailed := cardNotificationFacts{
		CardID:               cardID,
		Kind:                 string(domain.CardKindOngoing),
		EventType:            "message",
		MessageType:          "text",
		MessageSeq:           15,
		MessageAttemptID:     uuid.NullUUID{UUID: attemptID, Valid: true},
		MessageAttemptStatus: string(domain.AttemptStatusExpired),
		SupervisorID:         uuid.NullUUID{UUID: supID, Valid: true},
		SupervisorMuted:      false,
		HasFailureCallback:   true,
	}
	if !isSupervisorNotificationSuppressed(ongoingFailed) {
		t.Fatal("expired ongoing card with active unmuted supervisor callback should be suppressed")
	}

	// 规则 3：讨论卡处理非熔断失败回调，无 choice / 审批时静默，有 choice 时不静默
	discRetry := cardNotificationFacts{
		CardID:                    supID,
		Kind:                      string(domain.CardKindDiscussion),
		EventType:                 "message",
		MessageType:               "text",
		MessageTurnSource:         string(domain.TurnSourceSystem),
		MessageTurnIdempotencyKey: store.SupervisorFailureCallbackKey(cardID, attemptID),
		MessageTurnContent:        "outcome=failed\ncircuit_open=false\nerror_summary=temporary",
	}
	if !isSupervisorNotificationSuppressed(discRetry) {
		t.Fatal("discussion auto-retry reply without choice should be suppressed")
	}
	discRetry.MessageType = "choice"
	if isSupervisorNotificationSuppressed(discRetry) {
		t.Fatal("discussion auto-retry reply with choice must not be suppressed")
	}
}
