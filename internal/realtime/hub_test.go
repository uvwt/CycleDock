package realtime

import (
	"github.com/google/uuid"
	"strings"
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
	hub.Publish(cardA, Event{Type: "reply.preview", AttemptID: "r1", ReplyID: "reply", Text: "Hello "})
	hub.Publish(cardA, Event{Type: "reply.preview", AttemptID: "r1", ReplyID: "reply", Text: "Hello world"})
	if got := <-subA.Events; got.Type != "attempt.started" {
		t.Fatalf("got %+v", got)
	}
	if got := <-subA.Events; got.Text != "Hello " {
		t.Fatalf("got %+v", got)
	}
	if got := <-subA.Events; got.Text != "Hello world" {
		t.Fatalf("got %+v", got)
	}
	select {
	case ev := <-subB.Events:
		t.Fatalf("cross card leak %+v", ev)
	default:
	}
	restored, live := hub.Subscribe(cardA)
	defer restored.Close()
	if live == nil || (len(live.Replies) != 1 || live.Replies[0].Text != "Hello world") || live.AttemptID != "r1" {
		t.Fatalf("snapshot = %+v", live)
	}
	hub.Publish(cardA, Event{Type: "attempt.finished", AttemptID: "r1"})
	after, liveAfter := hub.Subscribe(cardA)
	defer after.Close()
	if liveAfter != nil {
		t.Fatalf("stale stream retained: %+v", liveAfter)
	}
}

func TestHubResetClosesCurrentPreviewWithoutRemovingText(t *testing.T) {
	hub := NewHub()
	card := uuid.New()
	hub.Publish(card, Event{Type: "attempt.started", AttemptID: "current"})
	hub.Publish(card, Event{Type: "reply.preview", AttemptID: "current", ReplyID: "reply", Text: "partial"})
	hub.Publish(card, Event{Type: "reply.reset", AttemptID: "old"})
	_, beforeReset := hub.Subscribe(card)
	if beforeReset == nil || (len(beforeReset.Replies) != 1 || beforeReset.Replies[0].Text != "partial") {
		t.Fatalf("stale reset changed current text: %+v", beforeReset)
	}
	hub.Publish(card, Event{Type: "reply.reset", AttemptID: "current"})
	_, afterReset := hub.Subscribe(card)
	if afterReset == nil || len(afterReset.Replies) != 1 || !afterReset.Replies[0].Failed || afterReset.AttemptID != "current" {
		t.Fatalf("current reset snapshot=%+v", afterReset)
	}
}

func TestReplyPreviewsRestoreInCallOrderAndSurviveCommit(t *testing.T) {
	hub := NewHub()
	card := uuid.New()
	hub.Publish(card, Event{Type: "attempt.started", AttemptID: "a"})
	hub.Publish(card, Event{Type: "reply.preview", AttemptID: "a", ReplyID: "second", Order: 2, Text: "second"})
	hub.Publish(card, Event{Type: "reply.preview", AttemptID: "a", ReplyID: "first", Order: 1, Text: "first"})
	sub, snapshot := hub.Subscribe(card)
	defer sub.Close()
	if len(snapshot.Replies) != 2 || snapshot.Replies[0].ID != "first" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	hub.Publish(card, Event{Type: "reply.committed", AttemptID: "a", ReplyID: "first", EventID: "persisted"})
	hub.Publish(card, Event{Type: "reply.reset", AttemptID: "a"})
	restored, after := hub.Subscribe(card)
	defer restored.Close()
	if len(after.Replies) != 2 || after.Replies[0].EventID != "persisted" || !after.Replies[1].Failed {
		t.Fatalf("after reset=%+v", after)
	}
	hub.Publish(card, Event{Type: "reply.preview", AttemptID: "a", ReplyID: "first", Text: "late"})
	final, last := hub.Subscribe(card)
	defer final.Close()
	if last.Replies[0].Text != "first" {
		t.Fatal("late parameters changed committed preview")
	}
}

func TestOversizedPreviewIsNeitherStoredNorBroadcast(t *testing.T) {
	hub := NewHub()
	card := uuid.New()
	hub.Publish(card, Event{Type: "attempt.started", AttemptID: "a"})
	sub, _ := hub.Subscribe(card)
	defer sub.Close()
	hub.Publish(card, Event{Type: "reply.preview", AttemptID: "a", ReplyID: "large", Text: strings.Repeat("x", maxLiveBytes+1)})
	select {
	case event := <-sub.Events:
		t.Fatalf("oversized event=%s", event.Type)
	default:
	}
}

func TestADKReplyDeltasPublishFullBrowserSnapshots(t *testing.T) {
	hub := NewHub()
	card := uuid.New()
	hub.Publish(card, Event{Type: "attempt.started", AttemptID: "a"})
	sub, _ := hub.Subscribe(card)
	defer sub.Close()
	hub.Publish(card, Event{Type: "reply.delta", AttemptID: "a", ReplyID: "reply", Text: "你", Order: 1})
	hub.Publish(card, Event{Type: "reply.delta", AttemptID: "a", ReplyID: "reply", Text: "好", Order: 1})
	for _, want := range []string{"你", "你好"} {
		event := <-sub.Events
		if event.Type != "reply.preview" || event.Text != want {
			t.Fatalf("event=%+v want text=%s", event, want)
		}
	}
	restored, snapshot := hub.Subscribe(card)
	defer restored.Close()
	if len(snapshot.Replies) != 1 || snapshot.Replies[0].Text != "你好" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	hub.Publish(card, Event{Type: "reply.committed", AttemptID: "a", ReplyID: "reply", EventID: "persisted"})
	<-sub.Events
	hub.Publish(card, Event{Type: "reply.delta", AttemptID: "a", ReplyID: "reply", Text: "late"})
	select {
	case event := <-sub.Events:
		t.Fatalf("late delta=%+v", event)
	default:
	}
}
