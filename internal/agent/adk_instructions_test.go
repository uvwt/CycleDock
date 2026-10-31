package agent

import (
	"strings"
	"testing"

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
