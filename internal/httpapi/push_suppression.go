package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/sqldb"
	"github.com/uvwt/CycleDock/internal/store"
)

// shouldSuppressCardNotification 判断本次卡片更新是否应静默推进推送快照而不发送 Web Push。
// 卡片静音或监督回调静默收口都会抑制普通消息推送，但待人工确认工具审批（tool_approval）始终穿透静音与静默收口。
func shouldSuppressCardNotification(update cardUpdate) bool {
	if update.NotificationKind == "tool_approval" {
		return false
	}
	return update.NotificationsMuted || update.NotificationSuppressed
}

type cardNotificationFacts struct {
	CardID                    uuid.UUID
	Kind                      string
	TodoState                 string
	ConfirmationRound         int64
	EventType                 string
	MessageType               string
	MessageSeq                int64
	MessageAttemptID          uuid.NullUUID
	PendingApprovalCount      int
	MessageAttemptStatus      string
	MessageTurnSource         string
	MessageTurnIdempotencyKey string
	MessageTurnContent        string
	FromRemoteChannel         bool
	ReviewEventSeq            int64
	ReviewAttemptID           uuid.NullUUID
	SupervisorID              uuid.NullUUID
	SupervisorMuted           bool
	HasReviewCallback         bool
	HasFailureCallback        bool
}

// applySupervisorNotificationSuppression 结合监督回调链路与卡片状态，
// 批量标记本次卡片最新消息是否应静默收口（Web Push 与站内浏览器桌面通知保持同一口径）。
func (api *API) applySupervisorNotificationSuppression(
	ctx context.Context,
	workspaceID uuid.UUID,
	updates []cardUpdate,
) error {
	if len(updates) == 0 {
		return nil
	}

	indexByCard := make(map[uuid.UUID]int, len(updates))
	cardIDs := make([]uuid.UUID, 0, len(updates))
	eventSeqs := make([]int64, 0, len(updates))
	for i, update := range updates {
		if update.MessagePreview == "" || update.EventSequence == "" || update.EventSequence == "0" {
			continue
		}
		cardID, err := uuid.Parse(update.ID)
		if err != nil {
			continue
		}
		seq, err := strconv.ParseInt(update.EventSequence, 10, 64)
		if err != nil || seq <= 0 {
			continue
		}
		indexByCard[cardID] = i
		cardIDs = append(cardIDs, cardID)
		eventSeqs = append(eventSeqs, seq)
	}
	if len(cardIDs) == 0 {
		return nil
	}

	// 按目标卡片逐条查询通知静默判定所需的标准 SQL 事实（PG / SQLite 公共子集，不依赖 unnest / LATERAL / jsonb ->>）。
	// 工作空间内单次更新涉及的活跃卡片数量有限，标量主键查询均走主键与复合索引。
	for i, cardID := range cardIDs {
		seq := eventSeqs[i]
		var (
			facts  cardNotificationFacts
			msgRaw []byte
		)
		err := api.pool.QueryRow(ctx, `
SELECT
  c.id,
  c.kind,
  COALESCE(c.todo_state, ''),
  c.confirmation_round,
  COALESCE(ev.event_type, ''),
  ev.message,
  ev.attempt_id,
  (
    SELECT COUNT(*)
    FROM card_tool_approvals ap
    WHERE ap.workspace_id = c.workspace_id AND ap.card_id = c.id AND ap.status = 'pending'
  ),
  COALESCE(ma.status, ''),
  COALESCE(mt.source, ''),
  COALESCE(mt.idempotency_key, ''),
  COALESCE(mt.content, ''),
  EXISTS (
    SELECT 1
    FROM remote_channel_inbound_dedup rcd
    JOIN remote_channels rc
      ON rc.id = rcd.channel_id AND rc.workspace_id = c.workspace_id
    WHERE ma.turn_id IS NOT NULL
      AND rcd.turn_id = ma.turn_id
      AND rc.enabled = true
  ),
  COALESCE((
    SELECT rv.sequence
    FROM events rv
    WHERE c.kind = 'todo' AND c.todo_state = 'review'
      AND rv.workspace_id = c.workspace_id
      AND rv.card_id = c.id
      AND rv.event_type = 'status_change'
      AND rv.content = 'working -> review'
    ORDER BY rv.sequence DESC
    LIMIT 1
  ), 0),
  (
    SELECT rv.attempt_id
    FROM events rv
    WHERE c.kind = 'todo' AND c.todo_state = 'review'
      AND rv.workspace_id = c.workspace_id
      AND rv.card_id = c.id
      AND rv.event_type = 'status_change'
      AND rv.content = 'working -> review'
    ORDER BY rv.sequence DESC
    LIMIT 1
  ),
  COALESCE(
    (
      SELECT d.id
      FROM cards d
      WHERE c.kind IN ('todo', 'ongoing')
        AND c.source_discussion_id IS NOT NULL
        AND d.workspace_id = c.workspace_id
        AND d.id = c.source_discussion_id
        AND d.kind = 'discussion'
        AND d.discussion_state = 'active'
      LIMIT 1
    ),
    (
      SELECT d.id
      FROM cards d
      WHERE c.kind IN ('todo', 'ongoing')
        AND p.main_discussion_id IS NOT NULL
        AND d.workspace_id = c.workspace_id
        AND d.id = p.main_discussion_id
        AND d.kind = 'discussion'
        AND d.discussion_state = 'active'
      LIMIT 1
    )
  )
FROM cards c
LEFT JOIN projects p
  ON p.workspace_id = c.workspace_id AND p.id = c.project_id
LEFT JOIN events ev
  ON ev.workspace_id = c.workspace_id AND ev.card_id = c.id AND ev.sequence = $3
LEFT JOIN turn_attempts ma
  ON ma.workspace_id = c.workspace_id AND ma.card_id = c.id AND ma.id = ev.attempt_id
LEFT JOIN card_turns mt
  ON mt.workspace_id = c.workspace_id AND mt.card_id = c.id AND mt.id = ma.turn_id
WHERE c.workspace_id = $1 AND c.id = $2
`, workspaceID, cardID, seq).Scan(
			&facts.CardID,
			&facts.Kind,
			&facts.TodoState,
			&facts.ConfirmationRound,
			&facts.EventType,
			&msgRaw,
			&facts.MessageAttemptID,
			&facts.PendingApprovalCount,
			&facts.MessageAttemptStatus,
			&facts.MessageTurnSource,
			&facts.MessageTurnIdempotencyKey,
			&facts.MessageTurnContent,
			&facts.FromRemoteChannel,
			&facts.ReviewEventSeq,
			&facts.ReviewAttemptID,
			&facts.SupervisorID,
		)
		if errors.Is(err, sqldb.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		facts.MessageSeq = seq
		if len(msgRaw) > 0 {
			var meta struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(msgRaw, &meta)
			facts.MessageType = meta.Type
		}
		if facts.SupervisorID.Valid {
			reviewKey := "supervisor-callback:" + facts.CardID.String() + ":review:" + facts.CardID.String()
			if facts.ConfirmationRound > 1 {
				reviewKey += ":round:" + strconv.FormatInt(facts.ConfirmationRound, 10)
			}
			failureKey := ""
			if facts.MessageAttemptID.Valid {
				failureKey = "supervisor-callback:" + facts.CardID.String() + ":attempt:" + facts.MessageAttemptID.UUID.String()
			}
			err = api.pool.QueryRow(ctx, `
SELECT
  COALESCE(sup.notifications_muted, false),
  EXISTS (
    SELECT 1 FROM card_turns ct
    WHERE ct.workspace_id = $1
      AND ct.card_id = $2
      AND ct.source = 'system'
      AND ct.cancelled_at IS NULL
      AND ct.idempotency_key = $3
  ),
  EXISTS (
    SELECT 1 FROM card_turns ct
    WHERE $4 <> ''
      AND ct.workspace_id = $1
      AND ct.card_id = $2
      AND ct.source = 'system'
      AND ct.cancelled_at IS NULL
      AND ct.idempotency_key = $4
  )
FROM cards sup
WHERE sup.workspace_id = $1 AND sup.id = $2
`, workspaceID, facts.SupervisorID.UUID, reviewKey, failureKey).Scan(
				&facts.SupervisorMuted,
				&facts.HasReviewCallback,
				&facts.HasFailureCallback,
			)
			if err != nil && !errors.Is(err, sqldb.ErrNoRows) {
				return err
			}
		}
		if idx, ok := indexByCard[facts.CardID]; ok {
			updates[idx].NotificationSuppressed = isSupervisorNotificationSuppressed(facts)
		}
	}
	return nil
}

// isSupervisorNotificationSuppressed 执行 Web Push / 桌面通知的静默收口规则：
// 1. 存在待工具审批（tool_approval）时，任何卡片都不抑制推送，确保人工审批不会漏看；
// 2. 源自已启用远程通道入站 Turn 的回复（无论普通文本还是选择题）均已由对应 IM 通道回推，
//    不再重复弹出 Web Push（但上述 tool_approval 仍穿透）；
// 3. 非远程通道消息存在选择题（choice）时不抑制推送；
// 4. 子任务（Todo / Ongoing）进入 Review 或触发失败/超时终态回调，且活跃监督讨论未静音并已成功创建对应回调 Turn 时，
//    子任务本次终态更新静默收口（防漏兜底：无活跃监督讨论或监督讨论已静音时照常推送；进 Review 后用户再追问产生的后续回复照常推送）；
// 5. 讨论卡处理“非熔断自动自愈重试”失败回调且未发出 choice / tool_approval 时静默收口；
//    Review 完成汇总、熔断终态汇报或普通对话/定时消息照常推送。
func isSupervisorNotificationSuppressed(f cardNotificationFacts) bool {
	if f.PendingApprovalCount > 0 || f.EventType == "tool_approval" {
		return false
	}
	if f.FromRemoteChannel {
		return true
	}
	if f.MessageType == "choice" {
		return false
	}

	switch f.Kind {
	case string(domain.CardKindTodo), string(domain.CardKindOngoing):
		if !f.SupervisorID.Valid || f.SupervisorID.UUID == f.CardID || f.SupervisorMuted {
			return false
		}
		if f.Kind == string(domain.CardKindTodo) &&
			f.TodoState == string(domain.TodoStateReview) &&
			f.HasReviewCallback &&
			isReviewTransitionMessage(f) {
			return true
		}
		if (f.MessageAttemptStatus == string(domain.AttemptStatusFailed) ||
			f.MessageAttemptStatus == string(domain.AttemptStatusExpired)) &&
			f.HasFailureCallback {
			return true
		}
		return false

	case string(domain.CardKindDiscussion):
		return store.IsNonCircuitSupervisorFailureCallback(
			f.MessageTurnSource,
			f.MessageTurnIdempotencyKey,
			f.MessageTurnContent,
		)

	default:
		return false
	}
}

func isReviewTransitionMessage(f cardNotificationFacts) bool {
	if f.ReviewEventSeq <= 0 {
		return false
	}
	// 先发最终答复再确认进 Review：消息序号不晚于 working -> review 状态事件。
	if f.MessageSeq <= f.ReviewEventSeq {
		return true
	}
	// 先确认进 Review 再在同一运行 Attempt 内发总结气泡：按 attempt_id 精确绑定，不受时间戳精度影响。
	if f.ReviewAttemptID.Valid && f.MessageAttemptID.Valid && f.ReviewAttemptID.UUID == f.MessageAttemptID.UUID {
		return true
	}
	return false
}
