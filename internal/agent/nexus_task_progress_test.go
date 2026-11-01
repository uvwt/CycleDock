package agent

import (
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestNexusTaskProgressReadsCheckpointSummary(t *testing.T) {
	progress, ok := nexusTaskProgress("task_manage", map[string]any{
		"structuredContent": map[string]any{
			"action": "checkpoint",
			"task_summary": map[string]any{
				"id": "task-1", "title": "部署服务", "status": "active",
				"completed_step_count": float64(2), "step_count": float64(5),
				"current_step": map[string]any{"title": "检查健康检查", "status": "in_progress"},
				"summary":      "服务已启动",
				"steps": []any{
					map[string]any{"title": "构建", "status": "completed"},
					map[string]any{"title": "检查健康检查", "status": "in_progress"},
				},
			},
		},
	})
	if !ok || progress.Title != "部署服务" || progress.Completed != 2 || progress.Total != 5 || progress.CurrentStep != "检查健康检查" || len(progress.Steps) != 2 {
		t.Fatalf("progress = %+v ok=%v", progress, ok)
	}
	if _, ok := nexusTaskProgress("task_manage", map[string]any{"structuredContent": map[string]any{"action": "list", "tasks": []any{}}}); ok {
		t.Fatal("list must not become the current task")
	}
	if _, ok := nexusTaskProgress("exec_command", map[string]any{"structuredContent": map[string]any{"task_summary": map[string]any{"id": "task-1", "title": "部署服务"}}}); ok {
		t.Fatal("device tools must not publish task progress")
	}
}

func TestPublishNexusTaskSurvivesStagePoll(t *testing.T) {
	cardID := uuid.New()
	attemptID := uuid.New()
	hub := realtime.NewHub()
	hub.Publish(cardID, realtime.Event{Type: "attempt.started", AttemptID: attemptID.String()})
	runtime := attemptToolRuntime{live: hub, context: store.AgentCardContext{CardID: cardID, AttemptID: attemptID}}
	runtime.publishNexusTask("task_create", map[string]any{
		"structuredContent": map[string]any{
			"task_summary": map[string]any{"id": "task-1", "title": "部署服务", "status": "active", "step_count": float64(3)},
		},
	})
	hub.Publish(cardID, realtime.Event{Type: "attempt.stage", AttemptID: attemptID.String(), Stage: "正在执行"})
	sub, snapshot := hub.Subscribe(cardID)
	sub.Close()
	if snapshot == nil || snapshot.Task == nil || snapshot.Task.Title != "部署服务" || snapshot.Task.Total != 3 || snapshot.Stage != "正在执行" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
