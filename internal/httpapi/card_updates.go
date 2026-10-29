package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type cardUpdate struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	Revision       string `json:"revision"`
	EventSequence  string `json:"event_sequence"`
	MessagePreview string `json:"message_preview"`
	AttemptStatus  string `json:"attempt_status"`
}

// 只比较持久业务变化，排除 token、阶段文案和 heartbeat，避免流式输出造成通知轰炸。
func (api *API) cardUpdates(ctx context.Context, workspaceID uuid.UUID) ([]cardUpdate, error) {
	rows, err := api.pool.Query(ctx, `
 SELECT c.id::text, c.project_id::text, c.kind, c.title,
 md5(concat_ws('|', c.updated_at::text, e.sequence::text, a.id::text, a.status,
   t.sequence::text, t.cancelled_at::text, s.updated_at::text, s.count::text)),
 COALESCE(e.sequence::text, '0'), COALESCE(e.preview, ''), COALESCE(a.status, '')
 FROM cards c
 LEFT JOIN LATERAL (
   SELECT sequence, left(COALESCE(NULLIF(btrim(content), ''), message->'widget'->>'question',
     NULLIF(message->>'alt', ''), message->>'url', ''), 181) AS preview
   FROM events WHERE workspace_id=c.workspace_id AND card_id=c.id
   ORDER BY sequence DESC LIMIT 1
 ) e ON true
 LEFT JOIN LATERAL (
   SELECT id, status FROM turn_attempts WHERE workspace_id=c.workspace_id AND card_id=c.id
   ORDER BY created_at DESC, id DESC LIMIT 1
 ) a ON true
 LEFT JOIN LATERAL (
   SELECT MAX(sequence) AS sequence, MAX(cancelled_at) AS cancelled_at FROM card_turns WHERE workspace_id=c.workspace_id AND card_id=c.id
 ) t ON true
 LEFT JOIN LATERAL (
   SELECT MAX(updated_at) AS updated_at, COUNT(*) AS count FROM card_schedules WHERE workspace_id=c.workspace_id AND card_id=c.id
 ) s ON true
 WHERE c.workspace_id=$1 ORDER BY c.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	updates := make([]cardUpdate, 0)
	for rows.Next() {
		var update cardUpdate
		if err := rows.Scan(&update.ID, &update.ProjectID, &update.Kind, &update.Title, &update.Revision, &update.EventSequence, &update.MessagePreview, &update.AttemptStatus); err != nil {
			return nil, err
		}
		// 通知只取产品时间线中的消息摘要，不读取模型内部思考或原始工具上下文。
		preview := []rune(strings.Join(strings.Fields(update.MessagePreview), " "))
		if len(preview) > 180 {
			update.MessagePreview = string(preview[:180]) + "…"
		} else {
			update.MessagePreview = string(preview)
		}
		updates = append(updates, update)
	}
	return updates, rows.Err()
}

// 工作空间级连接不随 Card 路由卸载；重连时发送快照，由客户端保留的版本基线补齐断线期间的更新。
func (api *API) streamCardUpdates(w http.ResponseWriter, r *http.Request) {
	workspaceID := identityFromRequest(r).WorkspaceID
	updates, err := api.cardUpdates(r.Context(), workspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unsupported", "stream unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(updates []cardUpdate) bool {
		data, err := json.Marshal(updates)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: cards.snapshot\ndata: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send(updates) {
		return
	}
	previous, _ := json.Marshal(updates)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	expire := time.NewTimer(15 * time.Minute)
	defer expire.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-expire.C:
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-tick.C:
			updates, err := api.cardUpdates(r.Context(), workspaceID)
			if err != nil {
				return
			}
			data, _ := json.Marshal(updates)
			if string(data) != string(previous) {
				if !send(updates) {
					return
				}
				previous = data
			}
		}
	}
}
