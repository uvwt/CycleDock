package httpapi

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/sqldb"
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
	// 1. 查询工作空间内所有卡片的聚合状态事实（使用 PG / SQLite 公共标量子查询，避免 LATERAL / FILTER / md5 / jsonb 专属语法）。
	rows, err := api.pool.Query(ctx, `
SELECT
  c.id::text,
  c.project_id::text,
  c.kind,
  c.title,
  c.notifications_muted,
  c.updated_at,
  (SELECT sequence FROM events e WHERE e.workspace_id = c.workspace_id AND e.card_id = c.id ORDER BY sequence DESC LIMIT 1) AS latest_event_seq,
  (SELECT id::text FROM turn_attempts a WHERE a.workspace_id = c.workspace_id AND a.card_id = c.id ORDER BY created_at DESC, id DESC LIMIT 1) AS latest_attempt_id,
  (SELECT status FROM turn_attempts a WHERE a.workspace_id = c.workspace_id AND a.card_id = c.id ORDER BY created_at DESC, id DESC LIMIT 1) AS latest_attempt_status,
  (SELECT MAX(sequence) FROM card_turns t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id) AS max_turn_seq,
  (SELECT MAX(cancelled_at) FROM card_turns t WHERE t.workspace_id = c.workspace_id AND t.card_id = c.id) AS max_turn_cancelled_at,
  (SELECT MAX(updated_at) FROM card_schedules s WHERE s.workspace_id = c.workspace_id AND s.card_id = c.id) AS max_schedule_updated_at,
  (SELECT COUNT(*) FROM card_schedules s WHERE s.workspace_id = c.workspace_id AND s.card_id = c.id AND s.archived_at IS NULL) AS active_schedule_count,
  (SELECT COUNT(*) FROM card_tool_approvals ap WHERE ap.workspace_id = c.workspace_id AND ap.card_id = c.id AND ap.status = 'pending') AS pending_approval_count,
  (SELECT MAX(id::text) FROM card_tool_approvals ap WHERE ap.workspace_id = c.workspace_id AND ap.card_id = c.id AND ap.status = 'pending') AS latest_approval_id,
  COALESCE(rc.last_read_message_sequence, 0),
  COALESCE(rc.marked_unread, false)
FROM cards c
LEFT JOIN card_read_cursors rc
  ON rc.workspace_id = c.workspace_id
  AND rc.card_id = c.id
  AND rc.source_id = $2
WHERE c.workspace_id = $1
ORDER BY c.id`, workspaceID, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	updates := make([]cardUpdate, 0)
	attemptStatusByCard := make(map[string]string)
	for rows.Next() {
		var (
			update               cardUpdate
			cardUpdatedAt        sqldb.Timestamptz
			latestEventSeq       sqldb.Int8
			latestAttemptID      sqldb.Text
			latestAttemptStatus  sqldb.Text
			maxTurnSeq           sqldb.Int8
			maxTurnCancelledAt   sqldb.Timestamptz
			maxScheduleUpdatedAt sqldb.Timestamptz
			activeScheduleCount  int64
			pendingApprovalCount int
			latestApprovalID     sqldb.Text
			lastReadSeq          int64
			markedUnread         bool
		)
		if err := rows.Scan(
			&update.ID, &update.ProjectID, &update.Kind, &update.Title, &update.NotificationsMuted,
			&cardUpdatedAt, &latestEventSeq, &latestAttemptID, &latestAttemptStatus,
			&maxTurnSeq, &maxTurnCancelledAt, &maxScheduleUpdatedAt, &activeScheduleCount,
			&pendingApprovalCount, &latestApprovalID, &lastReadSeq, &markedUnread,
		); err != nil {
			return nil, err
		}
		update.PendingApprovalCount = pendingApprovalCount
		if latestAttemptStatus.Valid {
			update.AttemptStatus = latestAttemptStatus.String
		}
		attemptStatusByCard[update.ID] = update.AttemptStatus

		// 在 Go 侧计算确定性的 MD5 Revision，避免 PostgreSQL md5() / concat_ws() 在 SQLite 侧不可用。
		revParts := make([]string, 0, 11)
		if cardUpdatedAt.Valid {
			revParts = append(revParts, cardUpdatedAt.Time.UTC().Format(sqldb.SQLiteTimeLayout))
		}
		revParts = append(revParts, strconv.FormatBool(update.NotificationsMuted))
		if latestEventSeq.Valid {
			revParts = append(revParts, strconv.FormatInt(latestEventSeq.Int64, 10))
		}
		if latestAttemptID.Valid {
			revParts = append(revParts, latestAttemptID.String)
		}
		if latestAttemptStatus.Valid {
			revParts = append(revParts, latestAttemptStatus.String)
		}
		if maxTurnSeq.Valid {
			revParts = append(revParts, strconv.FormatInt(maxTurnSeq.Int64, 10))
		}
		if maxTurnCancelledAt.Valid {
			revParts = append(revParts, maxTurnCancelledAt.Time.UTC().Format(sqldb.SQLiteTimeLayout))
		}
		if maxScheduleUpdatedAt.Valid {
			revParts = append(revParts, maxScheduleUpdatedAt.Time.UTC().Format(sqldb.SQLiteTimeLayout))
		}
		revParts = append(revParts, strconv.FormatInt(activeScheduleCount, 10))
		revParts = append(revParts, strconv.Itoa(pendingApprovalCount))
		if latestApprovalID.Valid {
			revParts = append(revParts, latestApprovalID.String)
		}
		sum := md5.Sum([]byte(strings.Join(revParts, "|")))
		update.Revision = hex.EncodeToString(sum[:])

		if sourceID != uuid.Nil {
			update.ReadRevision = strconv.FormatInt(lastReadSeq, 10) + ":" + strconv.FormatBool(markedUnread)
		}
		update.EventSequence = "0"
		updates = append(updates, update)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(updates) == 0 {
		return updates, nil
	}

	// 2. 拉取本工作空间最近的可通知事件（message 或仍处于 pending 的 tool_approval），
	// 在 Go 侧解析 JSON payload 生成预览，彻底消除 PG 专属的 jsonb_typeof / jsonb_array_length / ->> 语法。
	evRows, err := api.pool.Query(ctx, `
SELECT e.card_id::text, e.sequence, e.event_type, e.content, e.message
FROM events e
WHERE e.workspace_id = $1
  AND (
    e.event_type = 'message'
    OR (
      e.event_type = 'tool_approval'
      AND EXISTS (
        SELECT 1 FROM card_tool_approvals cta
        WHERE cta.workspace_id = e.workspace_id
          AND cta.card_id = e.card_id
          AND cta.id = e.tool_approval_id
          AND cta.status = 'pending'
      )
    )
  )
ORDER BY e.card_id, e.sequence DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer evRows.Close()

	type latestNotif struct {
		seq     int64
		preview string
		kind    string
	}
	notifByCard := make(map[string]latestNotif, len(updates))
	for evRows.Next() {
		var (
			cardIDStr string
			seq       int64
			eventType string
			content   string
			msgRaw    []byte
		)
		if err := evRows.Scan(&cardIDStr, &seq, &eventType, &content, &msgRaw); err != nil {
			return nil, err
		}
		if _, done := notifByCard[cardIDStr]; done {
			continue
		}
		if eventType == "tool_approval" {
			preview := strings.TrimSpace(content)
			if preview == "" {
				preview = "需要你审批敏感工具调用"
			}
			notifByCard[cardIDStr] = latestNotif{seq: seq, preview: preview, kind: eventType}
			continue
		}
		var meta struct {
			Preview bool            `json:"preview"`
			EndTurn bool            `json:"end_turn"`
			Type    string          `json:"type"`
			Alt     string          `json:"alt"`
			URL     string          `json:"url"`
			Images  json.RawMessage `json:"images"`
			Widget  *struct {
				Question string `json:"question"`
			} `json:"widget"`
		}
		if len(msgRaw) > 0 {
			_ = json.Unmarshal(msgRaw, &meta)
		}
		if meta.Preview {
			continue
		}
		attemptStatus := attemptStatusByCard[cardIDStr]
		if !meta.EndTurn && meta.Type != "choice" && attemptStatus != "completed" && attemptStatus != "failed" {
			continue
		}
		preview := strings.TrimSpace(content)
		if preview == "" && meta.Widget != nil {
			preview = strings.TrimSpace(meta.Widget.Question)
		}
		if preview == "" {
			preview = strings.TrimSpace(meta.Alt)
		}
		if preview == "" && len(meta.Images) > 0 {
			var imgs []json.RawMessage
			if err := json.Unmarshal(meta.Images, &imgs); err == nil && len(imgs) > 0 {
				if len(imgs) == 1 {
					preview = "[图片]"
				} else {
					preview = fmt.Sprintf("[%d 张图片]", len(imgs))
				}
			}
		}
		if preview == "" {
			preview = strings.TrimSpace(meta.URL)
		}
		notifByCard[cardIDStr] = latestNotif{seq: seq, preview: preview, kind: eventType}
	}
	if err := evRows.Err(); err != nil {
		return nil, err
	}

	for i := range updates {
		if n, ok := notifByCard[updates[i].ID]; ok {
			updates[i].EventSequence = strconv.FormatInt(n.seq, 10)
			updates[i].NotificationKind = n.kind
			// 通知只取产品时间线中的消息摘要，不读取模型内部思考或原始工具上下文。
			preview := []rune(strings.Join(strings.Fields(n.preview), " "))
			if len(preview) > 180 {
				updates[i].MessagePreview = string(preview[:180]) + "…"
			} else {
				updates[i].MessagePreview = string(preview)
			}
		}
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
