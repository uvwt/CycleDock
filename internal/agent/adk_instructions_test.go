package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestBuildADKInstructions_连上NexusDock才写明记忆和工作流(t *testing.T) {
	card := store.AgentCardContext{WorkspaceName: "ws", ProjectName: "proj", CardTitle: "card"}
	without := buildADKInstructions(card, "", "")
	if strings.Contains(without, "recall_search") || strings.Contains(without, "workflow_template_manage") {
		t.Fatal("未连接 NexusDock 时不应写入记忆或工作流工具")
	}

	with := buildADKInstructions(card, "", `{"nodes":[]}`)
	for _, name := range []string{
		"recall_search", "recall_read", "recall_write", "recall_maintain",
		"workflow_template_manage", "task_create", "task_manage",
	} {
		if !strings.Contains(with, name) {
			t.Errorf("已连接 NexusDock 的提示词缺少 %s", name)
		}
	}
	if strings.Contains(with, "已连接") {
		t.Fatal("不应向模型说明 NexusDock 连接状态")
	}
	if !strings.Contains(with, "不要等用户开口") {
		t.Fatal("应要求主动写入长期记忆")
	}
}

func TestBuildADKInstructions_主聊天场景不影响其他卡片(t *testing.T) {
	for _, test := range []struct {
		name     string
		card     store.AgentCardContext
		wantMain bool
	}{
		{name: "project main chat", card: store.AgentCardContext{CardKind: domain.CardKindDiscussion, IsMainDiscussion: true}, wantMain: true},
		{name: "discussion named main chat", card: store.AgentCardContext{CardKind: domain.CardKindDiscussion, CardTitle: "主聊天"}},
		{name: "todo", card: store.AgentCardContext{CardKind: domain.CardKindTodo}},
		{name: "ongoing", card: store.AgentCardContext{CardKind: domain.CardKindOngoing}},
	} {
		t.Run(test.name, func(t *testing.T) {
			instructions := buildADKInstructions(test.card, "", `{"nodes":[]}`)
			at := strings.Index(instructions, "当前场景：项目主聊天")
			if (at >= 0) != test.wantMain {
				t.Fatalf("main chat role present=%v, want %v", at >= 0, test.wantMain)
			}
			if test.wantMain && at < strings.Index(instructions, nexusMemoryAndWorkflow) {
				t.Fatal("主聊天约束必须放在通用长任务执行指引之后")
			}
		})
	}
}

func TestBuildADKInstructions_包含卡片引用跳转规范(t *testing.T) {
	card := store.AgentCardContext{CardKind: domain.CardKindDiscussion, CardTitle: "普通讨论"}
	instructions := buildADKInstructions(card, "", "")
	if !strings.Contains(instructions, "[卡片标题](cycledock://card/<card_id>)") {
		t.Fatal("通用指令必须包含标准卡片引用标记约束，以支持前端卡片跳转渲染")
	}

	mainCard := store.AgentCardContext{CardKind: domain.CardKindDiscussion, IsMainDiscussion: true, CardTitle: "主聊天"}
	mainInstructions := buildADKInstructions(mainCard, "", "")
	if !strings.Contains(mainInstructions, "必须使用标准卡片引用标记 [卡片标题](cycledock://card/<card_id>)") {
		t.Fatal("主聊天指令必须明确硬约束使用标准卡片引用标记")
	}
}

func TestBuildADKInstructions_项目说明包含背景和工作要求(t *testing.T) {
	card := store.AgentCardContext{ProjectDescription: "项目背景：维护服务\n工作要求：修改后验证"}
	instructions := buildADKInstructions(card, "", "")
	if !strings.Contains(instructions, "项目说明："+card.ProjectDescription) {
		t.Fatal("项目说明应完整传入 Agent 上下文")
	}
}

func TestMCPConnectionWarningUsesSafeActionableReasons(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("Unauthorized: private-token"), "401"},
		{errors.New("Forbidden: private-token"), "403"},
		{context.DeadlineExceeded, "超时"},
		{errors.New("https://private-endpoint/?key=private-token"), "连接失败"},
	} {
		warning := mcpConnectionWarning("连接12", tc.err)
		if !strings.Contains(warning, "连接12") || !strings.Contains(warning, tc.want) {
			t.Errorf("warning=%s", warning)
		}
		if strings.Contains(warning, "private-") {
			t.Fatalf("remote error leaked: %s", warning)
		}
	}
}
