// Package realtime distributes temporary Agent output to authenticated browser SSE subscribers.
// Domain state and durable Timeline events still live exclusively in PostgreSQL.
package realtime

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"
)

type Event struct {
	Type      string `json:"-"`
	AttemptID string `json:"attempt_id,omitempty"`
	Text      string `json:"text,omitempty"`
	Stage     string `json:"stage,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

type Subscriber struct {
	Events <-chan Event
	Close  func()
}

type liveState struct {
	attemptID string
	text      string
	stage     string
	startedAt string
}

type Hub struct {
	mu          sync.Mutex
	subscribers map[uuid.UUID]map[chan Event]struct{}
	live        map[uuid.UUID]liveState
}

func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[uuid.UUID]map[chan Event]struct{}),
		live:        make(map[uuid.UUID]liveState),
	}
}

const maxLiveBytes = 256 << 10

func (h *Hub) Subscribe(cardID uuid.UUID) (Subscriber, *Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan Event, 256)
	if h.subscribers[cardID] == nil {
		h.subscribers[cardID] = make(map[chan Event]struct{})
	}
	h.subscribers[cardID][ch] = struct{}{}

	var snapshot *Event
	if st, ok := h.live[cardID]; ok {
		snapshot = &Event{
			Type:      "attempt.text.snapshot",
			AttemptID: st.attemptID,
			Text:      st.text,
			Stage:     st.stage,
			StartedAt: st.startedAt,
		}
	}
	return Subscriber{
		Events: ch,
		Close: func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if _, ok := h.subscribers[cardID][ch]; ok {
				delete(h.subscribers[cardID], ch)
				close(ch)
				if len(h.subscribers[cardID]) == 0 {
					delete(h.subscribers, cardID)
				}
			}
		},
	}, snapshot
}

// Publish never blocks Agent execution on a slow browser. A lagging subscriber
// is closed so the browser reconnects and receives the bounded live snapshot.
func (h *Hub) Publish(cardID uuid.UUID, event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch event.Type {
	case "attempt.started":
		h.live[cardID] = liveState{
			attemptID: event.AttemptID,
			startedAt: event.StartedAt,
			stage:     "正在执行",
		}
	case "attempt.text.delta":
		st, active := h.live[cardID]
		if !active || st.attemptID != event.AttemptID {
			// Every Attempt must publish attempt.started first. A stale Attempt
			// can never resurrect itself after a newer Attempt becomes current.
			return
		}
		if len(st.text)+len(event.Text) <= maxLiveBytes {
			st.text += event.Text
		}
		h.live[cardID] = st
	case "attempt.text.reset":
		st, active := h.live[cardID]
		if !active || st.attemptID != event.AttemptID {
			return
		}
		st.text = ""
		h.live[cardID] = st
	case "attempt.stage":
		st := h.live[cardID]
		if st.attemptID == event.AttemptID {
			st.stage = event.Stage
			h.live[cardID] = st
		}
	case "attempt.finished", "attempt.failed":
		// A stale Attempt terminal event cannot delete or clear a newer Attempt.
		if st, ok := h.live[cardID]; ok {
			if st.attemptID != event.AttemptID {
				return
			}
			delete(h.live, cardID)
		}
	}

	for ch := range h.subscribers[cardID] {
		select {
		case ch <- event:
		default:
			delete(h.subscribers[cardID], ch)
			close(ch)
		}
	}
}

func Encode(event Event) ([]byte, error) { return json.Marshal(event) }
