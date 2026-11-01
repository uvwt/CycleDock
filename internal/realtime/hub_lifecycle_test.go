package realtime

import (
	"github.com/google/uuid"
	"testing"
)

func TestStagePollDoesNotClearOperatingDevice(t *testing.T) {
	hub := NewHub()
	card := uuid.New()
	hub.Publish(card, Event{Type: "attempt.started", AttemptID: "run"})
	hub.Publish(card, Event{Type: "attempt.device", AttemptID: "run", DeviceName: "RN"})
	hub.Publish(card, Event{Type: "attempt.stage", AttemptID: "run", Stage: "正在执行"})
	_, snapshot := hub.Subscribe(card)
	if snapshot == nil || snapshot.DeviceName != "RN" || snapshot.Stage != "正在执行" {
		t.Fatalf("stage poll cleared the operating device: %+v", snapshot)
	}
	hub.Publish(card, Event{Type: "attempt.device", AttemptID: "run"})
	_, cleared := hub.Subscribe(card)
	if cleared == nil || cleared.DeviceName != "" || cleared.Stage != "正在执行" {
		t.Fatalf("tool completion should clear only the device: %+v", cleared)
	}
}

func TestSupersededRunnerCannotReplaceNewRun(t *testing.T) {
	hub := NewHub()
	card := uuid.New()
	hub.Publish(card, Event{Type: "attempt.started", AttemptID: "old"})
	hub.Publish(card, Event{Type: "attempt.started", AttemptID: "new"})
	hub.Publish(card, Event{Type: "attempt.device", AttemptID: "new", DeviceName: "RN"})
	hub.Publish(card, Event{Type: "reply.preview", AttemptID: "new", ReplyID: "reply", Text: "new output"})
	// Interrupted old Runner can emit late deltas or a terminal state.
	for _, event := range []Event{
		{Type: "reply.delta", AttemptID: "old", ReplyID: "reply", Text: "stale"},
		{Type: "reply.preview", AttemptID: "old", ReplyID: "reply", Text: "stale"},
		{Type: "reply.reset", AttemptID: "old"},
		{Type: "reply.committed", AttemptID: "old", ReplyID: "reply", EventID: "stale"},
		{Type: "reply.discarded", AttemptID: "old", ReplyID: "reply"},
		{Type: "attempt.stage", AttemptID: "old", Stage: "stale"},
		{Type: "attempt.device", AttemptID: "old", DeviceName: "stale"},
		{Type: "attempt.task", AttemptID: "old", Task: &TaskProgress{ID: "old", Title: "旧任务"}},
	} {
		hub.Publish(card, event)
	}
	hub.Publish(card, Event{Type: "attempt.failed", AttemptID: "old"})
	sub, snapshot := hub.Subscribe(card)
	defer sub.Close()
	if snapshot == nil || snapshot.AttemptID != "new" || snapshot.DeviceName != "RN" || snapshot.Task != nil || (len(snapshot.Replies) != 1 || snapshot.Replies[0].Text != "new output") {
		t.Fatalf("superseded Run displaced current output: %+v", snapshot)
	}
	// 迟到的旧 Run 事件不得使前端的当前输出消失。
	hub.Publish(card, Event{Type: "attempt.failed", AttemptID: "old"})
	select {
	case stale := <-sub.Events:
		t.Fatalf("stale terminal event leaked to browser: %+v", stale)
	default:
	}
	hub.Publish(card, Event{Type: "attempt.finished", AttemptID: "new"})
	// A finished newer Run must not be replaced by an older delayed delta.
	hub.Publish(card, Event{Type: "reply.preview", AttemptID: "old", ReplyID: "reply", Text: "late"})
	subAfter, remaining := hub.Subscribe(card)
	defer subAfter.Close()
	if remaining != nil {
		t.Fatalf("terminal Run still in memory: %+v", remaining)
	}
}
