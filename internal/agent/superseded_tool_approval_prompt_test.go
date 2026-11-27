package agent

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/store"
)

func TestFormatSupersededToolApprovalsPrompt_Empty(t *testing.T) {
	if got := formatSupersededToolApprovalsPrompt(nil); got != "" {
		t.Fatalf("expected empty prompt for nil approvals, got %q", got)
	}
	if got := formatSupersededToolApprovalsPrompt([]store.SupersededToolApproval{}); got != "" {
		t.Fatalf("expected empty prompt for empty approvals, got %q", got)
	}
}

func TestFormatSupersededToolApprovalsPrompt_SingleAndMultiple(t *testing.T) {
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	single := formatSupersededToolApprovalsPrompt([]store.SupersededToolApproval{
		{
			ID:                id1,
			ToolName:          "exec_command",
			RiskLevel:         "high",
			ScopeLabel:        "设备 TianYi",
			Reason:            "执行变更命令",
			ResolutionComment: "已有外部事件唤醒卡片，旧审批已自动失效",
		},
	})
	for _, want := range []string{
		"自上一轮执行以来已自动失效的工具审批：",
		"- 工具 `exec_command`（approval_id=11111111-1111-1111-1111-111111111111，作用域：设备 TianYi，失效原因：已有外部事件唤醒卡片，旧审批已自动失效）",
		"已自动失效且不可点击",
		"切勿再要求用户去点击旧审批卡片确认",
		"重新发起工具调用",
	} {
		if !strings.Contains(single, want) {
			t.Fatalf("single prompt missing %q:\n%s", want, single)
		}
	}

	multi := formatSupersededToolApprovalsPrompt([]store.SupersededToolApproval{
		{
			ID:                id1,
			ToolName:          "exec_command",
			ResolutionComment: "已有后续新对话，旧审批已自动失效",
		},
		{
			ID:       id2,
			ToolName: "file_edit",
		},
	})
	idx1 := strings.Index(multi, "- 工具 `exec_command`（approval_id=11111111-1111-1111-1111-111111111111，失效原因：已有后续新对话，旧审批已自动失效）")
	idx2 := strings.Index(multi, "- 工具 `file_edit`（approval_id=22222222-2222-2222-2222-222222222222）")
	if idx1 < 0 || idx2 < 0 || idx1 >= idx2 {
		t.Fatalf("unexpected multiple approvals format or order:\n%s", multi)
	}
}

func TestTurnMessageAndBuildUserTurnContent_SupersededToolApprovals(t *testing.T) {
	clean := turnMessage(store.AgentCardContext{
		TurnContent: "继续检查构建状态",
	})
	if clean != "继续检查构建状态" {
		t.Fatalf("expected clean turnMessage without superseded approvals, got %q", clean)
	}

	approvalID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	cardCtx := store.AgentCardContext{
		TurnContent: "CI 已经完成，请继续下一步",
		StatusChanges: []store.CardStatusChange{
			{Content: "review -> working", ActorName: "Human"},
		},
		SupersededToolApprovals: []store.SupersededToolApproval{
			{
				ID:                approvalID,
				ToolName:          "exec_command",
				ScopeLabel:        "设备 TianYi",
				ResolutionComment: "已有外部事件唤醒卡片，旧审批已自动失效",
			},
		},
		DeviceNodeID: "node-1",
		DeviceName:   "TianYi",
	}

	msg1 := turnMessage(cardCtx)
	msg2 := turnMessage(cardCtx)
	if msg1 != msg2 {
		t.Fatalf("expected deterministic turnMessage across retries:\n%s\nvs\n%s", msg1, msg2)
	}

	content := buildUserTurnContent(cardCtx)
	text := textFromADKContent(content)
	for _, want := range []string{
		"CI 已经完成，请继续下一步",
		"自上一轮执行以来的卡片状态变更：",
		"自上一轮执行以来已自动失效的工具审批：",
		"approval_id=33333333-3333-3333-3333-333333333333",
		"本轮用户选择的操作设备：\"TianYi\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("buildUserTurnContent missing %q:\n%s", want, text)
		}
	}
}
