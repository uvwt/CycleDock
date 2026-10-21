package realtime

import (
	"github.com/google/uuid"
	"testing"
)

func TestSupersededRunnerCannotReplaceNewRun(t *testing.T) {
	hub := NewHub()
	card := uuid.New()
	hub.Publish(card, Event{Type: "run.started", RunID: "old"})
	hub.Publish(card, Event{Type: "run.started", RunID: "new"})
	hub.Publish(card, Event{Type: "run.text.delta", RunID: "new", Text: "new output"})
	// Interrupted old Runner can emit late deltas or a terminal state.
	hub.Publish(card, Event{Type: "run.text.delta", RunID: "old", Text: "stale"})
	hub.Publish(card, Event{Type: "run.failed", RunID: "old"})
	sub, snapshot := hub.Subscribe(card)
	defer sub.Close()
	if snapshot == nil || snapshot.RunID != "new" || snapshot.Text != "new output" {
		t.Fatalf("superseded Run displaced current output: %+v", snapshot)
	}
	// 迟到的旧 Run 事件不得使前端的当前输出消失。
	hub.Publish(card, Event{Type: "run.failed", RunID: "old"})
	select {
	case stale := <-sub.Events:
		t.Fatalf("stale terminal event leaked to browser: %+v", stale)
	default:
	}
	hub.Publish(card, Event{Type: "run.finished", RunID: "new"})
	subAfter, remaining := hub.Subscribe(card)
	defer subAfter.Close()
	if remaining != nil {
		t.Fatalf("terminal Run still in memory: %+v", remaining)
	}
}
