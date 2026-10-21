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
	RunID     string `json:"run_id,omitempty"`
	Text      string `json:"text,omitempty"`
	Stage     string `json:"stage,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}
type Subscriber struct {
	Events <-chan Event
	Close  func()
}
type liveState struct {
	runID     string
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
	return &Hub{subscribers: make(map[uuid.UUID]map[chan Event]struct{}), live: make(map[uuid.UUID]liveState)}
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
		snapshot = &Event{Type: "run.text.snapshot", RunID: st.runID, Text: st.text, Stage: st.stage, StartedAt: st.startedAt}
	}
	return Subscriber{Events: ch, Close: func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subscribers[cardID][ch]; ok {
			delete(h.subscribers[cardID], ch)
			close(ch)
			if len(h.subscribers[cardID]) == 0 {
				delete(h.subscribers, cardID)
			}
		}
	}}, snapshot
}

// Publish never blocks Runner on a slow browser. A lagging subscriber is closed
// so the browser reconnects and receives the bounded live snapshot.
func (h *Hub) Publish(cardID uuid.UUID, event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if event.Type == "run.started" {
		h.live[cardID] = liveState{runID: event.RunID, startedAt: event.StartedAt, stage: "正在执行"}
	} else if event.Type == "run.text.delta" {
		st := h.live[cardID]
		if st.runID != "" && st.runID != event.RunID {
			// Superseded Runner 的迟到增量不得覆盖当前 Run 的实时输出。
			return
		}
		if st.runID == "" {
			st = liveState{runID: event.RunID, startedAt: event.StartedAt}
		}
		if len(st.text)+len(event.Text) <= maxLiveBytes {
			st.text += event.Text
		}
		h.live[cardID] = st
	} else if event.Type == "run.stage" {
		st := h.live[cardID]
		if st.runID == event.RunID {
			st.stage = event.Stage
			h.live[cardID] = st
		}
	} else if event.Type == "run.finished" || event.Type == "run.failed" {
		// 旧 Run 的终止事件不能删除新 Run 的快照，也不能推送给浏览器清空新流。
		if st, ok := h.live[cardID]; ok {
			if st.runID != event.RunID {
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
