package realtime

import (
	"github.com/google/uuid"
	"testing"
)

func TestSupersededRunnerCannotReplaceNewRun(t *testing.T) {
	hub := NewHub()
	card := uuid.New()
	hub.Publish(card, Event{Type: "attempt.started", AttemptID: "old"})
	hub.Publish(card, Event{Type: "attempt.started", AttemptID: "new"})
	hub.Publish(card, Event{Type: "reply.preview", AttemptID: "new", ReplyID: "reply", Text: "new output"})
	// Interrupted old Runner can emit late deltas or a terminal state.
	for _, event := range []Event{
		{Type: "reply.delta", AttemptID: "old", ReplyID: "reply", Text: "stale"},
		{Type: "reply.preview", AttemptID: "old", ReplyID: "reply", Text: "stale"},
		{Type: "reply.reset", AttemptID: "old"},
		{Type: "reply.committed", AttemptID: "old", ReplyID: "reply", EventID: "stale"},
		{Type: "reply.discarded", AttemptID: "old", ReplyID: "reply"},
		{Type: "attempt.stage", AttemptID: "old", Stage: "stale"},
	} {
		hub.Publish(card, event)
	}
	hub.Publish(card, Event{Type: "attempt.failed", AttemptID: "old"})
	sub, snapshot := hub.Subscribe(card)
	defer sub.Close()
	if snapshot == nil || snapshot.AttemptID != "new" || (len(snapshot.Replies) != 1 || snapshot.Replies[0].Text != "new output") {
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
