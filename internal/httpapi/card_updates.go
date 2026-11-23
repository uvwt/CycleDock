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
	ID                     string `json:"id"`
	ProjectID              string `json:"project_id"`
	Kind                   string `json:"kind"`
	Title                  string `json:"title"`
	NotificationsMuted     bool   `json:"notifications_muted"`
	NotificationSuppressed bool   `json:"notification_suppressed"`
	Revision               string `json:"revision"`
	ReadRevision           string `json:"read_revision,omitempty"`
	EventSequence          string `json:"event_sequence"`
	MessagePreview         string `json:"message_preview"`
	// PendingApprovalCount 是该卡片当前待人工审批的数量；
	// NotificationKind 区分普通模型消息（"message"）与待人工确认工具审批（"tool_approval"），
	// 用于实现“卡片静音屏蔽普通消息，但工具审批穿透静音”的产品语义。
	PendingApprovalCount int    `json:"pending_approval_count"`
	NotificationKind     string `json:"notification_kind"`
	AttemptStatus        string `json:"attempt_status"`
}

// 只比较持久业务变化，排除 token、阶段文案和 heartbeat，避免流式输出造成通知轰炸。
func (api *API) cardUpdates(ctx context.Context, workspaceID uuid.UUID) ([]cardUpdate, error) {
	return api.cardUpdatesForSource(ctx, workspaceID, uuid.Nil)
}

// cardUpdatesForSource 在工作空间级业务 Revision 之外，按 Human Source 附带只读态版本 ReadRevision，
// 使同账号多端通过 SSE 实时同步已读/标未读状态，同时保持工作空间级 Revision 与 Web Push 快照解耦。
func (api *API) cardUpdatesForSource(ctx context.Context, workspaceID, sourceID uuid.UUID) ([]cardUpdate, error) {
	rows, err := api.pool.Query(ctx, `
 SELECT c.id::text, c.project_id::text, c.kind, c.title, c.notifications_muted,
 md5(concat_ws('|', c.updated_at::text, c.notifications_muted::text, e.sequence::text, a.id::text, a.status,
   t.sequence::text, t.cancelled_at::text, s.updated_at::text, s.count::text, ap.pending::text, ap.latest_id::text)),
 CASE
   WHEN $2::uuid = '00000000-0000-0000-0000-000000000000'::uuid THEN ''
   ELSE concat_ws(':', COALESCE(rc.last_read_message_sequence, 0)::text, COALESCE(rc.marked_unread, false)::text)
 END,
 COALESCE(m.sequence::text, '0'), COALESCE(m.preview, ''), COALESCE(m.notification_kind, ''), COALESCE(ap.pending, 0), COALESCE(a.status, '')
 FROM cards c
 LEFT JOIN LATERAL (
   SELECT sequence
   FROM events WHERE workspace_id=c.workspace_id AND card_id=c.id
   ORDER BY sequence DESC LIMIT 1
 ) e ON true
 LEFT JOIN LATERAL (
   SELECT id, status FROM turn_attempts WHERE workspace_id=c.workspace_id AND card_id=c.id
   ORDER BY created_at DESC, id DESC LIMIT 1
 ) a ON true
 -- 通知只认模型回复的正式消息与待人工确认的 tool_approval 事件，排除用户评论（comment）与流式快照。
 -- 置顶、标题、进度和状态变更仍通过上面的 revision 刷新页面。
 LEFT JOIN LATERAL (
   SELECT sequence,
     CASE
       WHEN event_type = 'tool_approval' THEN
         COALESCE(NULLIF(left(btrim(content), 181), ''), '需要你审批敏感工具调用')
       ELSE
         left(COALESCE(
           NULLIF(btrim(content), ''),
           message->'widget'->>'question',
           NULLIF(message->>'alt', ''),
           CASE
             WHEN jsonb_typeof(message->'images') = 'array' AND jsonb_array_length(message->'images') > 0 THEN
               CASE
                 WHEN jsonb_array_length(message->'images') = 1 THEN '[图片]'
                 ELSE '[' || jsonb_array_length(message->'images')::text || ' 张图片]'
               END
           END,
           message->>'url',
           ''
         ), 181)
     END AS preview,
     event_type AS notification_kind
   FROM events WHERE workspace_id=c.workspace_id AND card_id=c.id
     AND (
       (
         event_type = 'message'
         AND (message->>'preview' IS DISTINCT FROM 'true')
         AND (
           (message->>'end_turn' = 'true')
           OR (message->>'type' = 'choice')
           OR (a.status IN ('completed', 'failed'))
         )
       )
       OR (
         event_type = 'tool_approval'
         AND EXISTS (
           SELECT 1 FROM card_tool_approvals cta
           WHERE cta.workspace_id = events.workspace_id
             AND cta.card_id = events.card_id
             AND cta.id = events.tool_approval_id
             AND cta.status = 'pending'
         )
       )
     )
   ORDER BY sequence DESC LIMIT 1
 ) m ON true
 LEFT JOIN LATERAL (
   SELECT MAX(sequence) AS sequence, MAX(cancelled_at) AS cancelled_at FROM card_turns WHERE workspace_id=c.workspace_id AND card_id=c.id
 ) t ON true
 LEFT JOIN LATERAL (
   SELECT MAX(updated_at) AS updated_at, COUNT(*) FILTER (WHERE archived_at IS NULL) AS count FROM card_schedules WHERE workspace_id=c.workspace_id AND card_id=c.id
 ) s ON true
 -- 待人工确认数量决定焦点列表，审批挂起与处理完成都要让其他页面刷新卡片列表。
 LEFT JOIN LATERAL (
   SELECT COUNT(*)::int AS pending, MAX(id::text) AS latest_id FROM card_tool_approvals
   WHERE workspace_id=c.workspace_id AND card_id=c.id AND status='pending'
 ) ap ON true
 LEFT JOIN card_read_cursors rc
   ON rc.workspace_id = c.workspace_id
  AND rc.card_id = c.id
  AND rc.source_id = $2
 WHERE c.workspace_id=$1 ORDER BY c.id`, workspaceID, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	updates := make([]cardUpdate, 0)
	for rows.Next() {
		var update cardUpdate
		if err := rows.Scan(
			&update.ID, &update.ProjectID, &update.Kind, &update.Title, &update.NotificationsMuted,
			&update.Revision, &update.ReadRevision, &update.EventSequence, &update.MessagePreview, &update.NotificationKind, &update.PendingApprovalCount, &update.AttemptStatus,
		); err != nil {
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := api.applySupervisorNotificationSuppression(ctx, workspaceID, updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// 工作空间级连接不随 Card 路由卸载；重连时发送快照，由客户端保留的版本基线补齐断线期间的更新。
// 优先通过内存 Hub 工作区信号零延迟唤醒，仅保留低频兜底轮询应对跨副本写入。
func (api *API) streamCardUpdates(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	workspaceID := identity.WorkspaceID
	sourceID := identity.SourceID
	updates, err := api.cardUpdatesForSource(r.Context(), workspaceID, sourceID)
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

	var wake <-chan struct{}
	if api.live != nil {
		sub := api.live.SubscribeWorkspace(workspaceID)
		defer sub.Close()
		wake = sub.Events
	}
	// 有实时事件唤醒时，兜底轮询退到 15s；无 Hub（如单测场景）时维持 3s。
	fallbackInterval := 3 * time.Second
	if wake != nil {
		fallbackInterval = 15 * time.Second
	}
	tick := time.NewTicker(fallbackInterval)
	defer tick.Stop()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	expire := time.NewTimer(15 * time.Minute)
	defer expire.Stop()

	refresh := func() bool {
		updates, err := api.cardUpdatesForSource(r.Context(), workspaceID, sourceID)
		if err != nil {
			return false
		}
		data, _ := json.Marshal(updates)
		if string(data) != string(previous) {
			if !send(updates) {
				return false
			}
			previous = data
		}
		return true
	}

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
		case _, ok := <-wake:
			if !ok {
				return
			}
			if !refresh() {
				return
			}
		case <-tick.C:
			if !refresh() {
				return
			}
		}
	}
}
