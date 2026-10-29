// Package realtime distributes temporary Agent output to authenticated browser SSE subscribers.
// Domain state and durable Timeline events still live exclusively in PostgreSQL.
package realtime

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/google/uuid"
)

type Event struct {
	Type      string         `json:"-"`
	AttemptID string         `json:"attempt_id,omitempty"`
	Text      string         `json:"text,omitempty"`
	Stage     string         `json:"stage,omitempty"`
	StartedAt string         `json:"started_at,omitempty"`
	Order     int            `json:"order,omitempty"`
	ReplyID   string         `json:"reply_id,omitempty"`
	EventID   string         `json:"event_id,omitempty"`
	Replies   []ReplyPreview `json:"replies,omitempty"`
}

type Subscriber struct {
	Events <-chan Event
	Close  func()
}

type ReplyPreview struct {
	Order   int    `json:"order"`
	ID      string `json:"id"`
	Text    string `json:"text"`
	EventID string `json:"event_id,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
}

type liveState struct {
	attemptID string
	replies   []ReplyPreview
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
			Type:      "attempt.snapshot",
			AttemptID: st.attemptID,
			Replies:   append([]ReplyPreview(nil), st.replies...),
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
	case "reply.delta", "reply.preview", "reply.committed", "reply.discarded", "reply.reset":
		st, active := h.live[cardID]
		if !active || st.attemptID != event.AttemptID {
			return
		}
		if event.Type == "reply.reset" {
			// 路由重试关闭旧预览，但不能撤回已经显示且持久化的正文。
			for i := range st.replies {
				if st.replies[i].EventID == "" {
					st.replies[i].Failed = true
				}
			}
		} else {
			if event.ReplyID == "" || len(event.ReplyID) > 512 {
				return
			}
			index := -1
			total := 0
			for i, reply := range st.replies {
				if reply.ID == event.ReplyID {
					index = i
				} else {
					total += len(reply.Text)
				}
			}
			if event.Type == "reply.delta" {
				if index >= 0 {
					event.Text = st.replies[index].Text + event.Text
				}
				// 浏览器始终接收完整快照，重连和重复通知不会重复拼接正文。
				event.Type = "reply.preview"
			}
			if event.Type == "reply.preview" {
				if total+len(event.Text) > maxLiveBytes {
					return
				}
				if index < 0 {
					if len(st.replies) >= 128 {
						return
					}
					st.replies = append(st.replies, ReplyPreview{ID: event.ReplyID})
					index = len(st.replies) - 1
				}
				// 已提交或已拒绝的调用不能被迟到参数重新打开。
				if st.replies[index].EventID != "" || st.replies[index].Failed {
					return
				}
				st.replies[index].Text = event.Text
				st.replies[index].Order = event.Order
				sort.SliceStable(st.replies, func(i, j int) bool { return st.replies[i].Order < st.replies[j].Order })
			} else {
				if index < 0 {
					return
				}
				if event.Type == "reply.committed" {
					st.replies[index].EventID = event.EventID
				} else {
					st.replies[index].Failed = true
				}
			}
		}
		h.live[cardID] = st
	case "attempt.stage":
		st, active := h.live[cardID]
		if !active || st.attemptID != event.AttemptID {
			return
		}
		st.stage = event.Stage
		h.live[cardID] = st
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
