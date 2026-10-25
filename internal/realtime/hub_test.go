package realtime

import (
	"github.com/google/uuid"
	"testing"
)

func TestHubScopesSubscribersAndRestoresSnapshot(t *testing.T) {
	hub := NewHub()
	cardA, cardB := uuid.New(), uuid.New()
	subA, snapshot := hub.Subscribe(cardA)
	defer subA.Close()
	if snapshot != nil {
		t.Fatal("fresh card has snapshot")
	}
	subB, _ := hub.Subscribe(cardB)
	defer subB.Close()
	hub.Publish(cardA, Event{Type: "attempt.started", AttemptID: "r1", StartedAt: "2026-10-21T00:00:00Z"})
	hub.Publish(cardA, Event{Type: "attempt.text.delta", AttemptID: "r1", Text: "Hello "})
	hub.Publish(cardA, Event{Type: "attempt.text.delta", AttemptID: "r1", Text: "world"})
	if got := <-subA.Events; got.Type != "attempt.started" {
		t.Fatalf("got %+v", got)
	}
	if got := <-subA.Events; got.Text != "Hello " {
		t.Fatalf("got %+v", got)
	}
	if got := <-subA.Events; got.Text != "world" {
		t.Fatalf("got %+v", got)
	}
	select {
	case ev := <-subB.Events:
		t.Fatalf("cross card leak %+v", ev)
	default:
	}
	restored, live := hub.Subscribe(cardA)
	defer restored.Close()
	if live == nil || live.Text != "Hello world" || live.AttemptID != "r1" {
		t.Fatalf("snapshot = %+v", live)
	}
	hub.Publish(cardA, Event{Type: "attempt.finished", AttemptID: "r1"})
	after, liveAfter := hub.Subscribe(cardA)
	defer after.Close()
	if liveAfter != nil {
		t.Fatalf("stale stream retained: %+v", liveAfter)
	}
}

func TestHubResetClearsOnlyCurrentRunText(t *testing.T) {
	hub := NewHub()
	card := uuid.New()
	hub.Publish(card, Event{Type: "attempt.started", AttemptID: "current"})
	hub.Publish(card, Event{Type: "attempt.text.delta", AttemptID: "current", Text: "partial"})
	hub.Publish(card, Event{Type: "attempt.text.reset", AttemptID: "old"})
	_, beforeReset := hub.Subscribe(card)
	if beforeReset == nil || beforeReset.Text != "partial" {
		t.Fatalf("stale reset changed current text: %+v", beforeReset)
	}
	hub.Publish(card, Event{Type: "attempt.text.reset", AttemptID: "current"})
	_, afterReset := hub.Subscribe(card)
	if afterReset == nil || afterReset.Text != "" || afterReset.AttemptID != "current" {
		t.Fatalf("current reset snapshot=%+v", afterReset)
	}
}
