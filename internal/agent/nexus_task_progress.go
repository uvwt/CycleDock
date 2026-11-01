package agent

import (
	"github.com/uvwt/CycleDock/internal/realtime"
)

// nexusTaskProgress 只读取 task_create / task_manage 单次任务结果。
// list 和 get 不带 task_summary，不能把清单或完整任务体当成当前进度。
func nexusTaskProgress(toolName string, result map[string]any) (realtime.TaskProgress, bool) {
	if toolName != "task_create" && toolName != "task_manage" || result == nil || boolValue(result["isError"]) {
		return realtime.TaskProgress{}, false
	}
	payload := result
	if structured, ok := result["structuredContent"].(map[string]any); ok {
		payload = structured
	}
	summary, ok := payload["task_summary"].(map[string]any)
	if !ok {
		return realtime.TaskProgress{}, false
	}
	progress := realtime.TaskProgress{
		ID:          metadataString(summary["id"], 80),
		Title:       metadataString(summary["title"], 120),
		Status:      metadataString(summary["status"], 32),
		Completed:   boundedCount(summary["completed_step_count"]),
		Total:       boundedCount(summary["step_count"]),
		Summary:     metadataString(summary["summary"], 240),
		Blocker:     metadataString(summary["blocker"], 240),
		CurrentStep: taskStepTitle(summary["current_step"]),
	}
	if steps, ok := summary["steps"].([]any); ok {
		for _, raw := range steps {
			if len(progress.Steps) >= 12 {
				break
			}
			step, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			title := metadataString(step["title"], 120)
			if title == "" {
				continue
			}
			progress.Steps = append(progress.Steps, realtime.TaskStep{
				Title:  title,
				Status: metadataString(step["status"], 32),
			})
		}
	}
	if progress.ID == "" || progress.Title == "" {
		return realtime.TaskProgress{}, false
	}
	return progress, true
}

func (r attemptToolRuntime) publishNexusTask(toolName string, result map[string]any) {
	if r.live == nil {
		return
	}
	progress, ok := nexusTaskProgress(toolName, result)
	if !ok {
		return
	}
	r.live.Publish(r.context.CardID, realtime.Event{
		Type: "attempt.task", AttemptID: r.context.AttemptID.String(), Task: &progress,
	})
}

func taskStepTitle(value any) string {
	step, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	return metadataString(step["title"], 120)
}

func boundedCount(value any) int {
	number, ok := value.(float64)
	if !ok || number < 0 {
		return 0
	}
	if number > 12 {
		return 12
	}
	return int(number)
}
