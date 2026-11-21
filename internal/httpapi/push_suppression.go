package httpapi

import (
	"context"
	"strconv"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
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

	rows, err := api.pool.Query(ctx, `
WITH target_cards AS (
  SELECT card_id, event_seq
  FROM unnest($2::uuid[], $3::bigint[]) AS u(card_id, event_seq)
)
SELECT
  c.id,
  c.kind,
  COALESCE(c.todo_state, ''),
  c.confirmation_round,
  COALESCE(ev.event_type, ''),
  COALESCE(ev.message->>'type', ''),
  tc.event_seq,
  ev.attempt_id,
  COALESCE(ap.pending, 0),
  COALESCE(ma.status, ''),
  COALESCE(mt.source, ''),
  COALESCE(mt.idempotency_key, ''),
  COALESCE(mt.content, ''),
  COALESCE(rv.sequence, 0),
  rv.attempt_id,
  sup.id,
  COALESCE(sup.notifications_muted, false),
  EXISTS (
    SELECT 1
    FROM card_turns ct
    WHERE sup.id IS NOT NULL
      AND ct.workspace_id = c.workspace_id
      AND ct.card_id = sup.id
      AND ct.source = 'system'
      AND ct.cancelled_at IS NULL
      AND ct.idempotency_key = CASE
        WHEN c.confirmation_round > 1 THEN
          'supervisor-callback:' || c.id::text || ':review:' || c.id::text || ':round:' || c.confirmation_round::text
        ELSE
          'supervisor-callback:' || c.id::text || ':review:' || c.id::text
      END
  ) AS has_review_callback,
  EXISTS (
    SELECT 1
    FROM card_turns ct
    WHERE sup.id IS NOT NULL
      AND ev.attempt_id IS NOT NULL
      AND ct.workspace_id = c.workspace_id
      AND ct.card_id = sup.id
      AND ct.source = 'system'
      AND ct.cancelled_at IS NULL
      AND ct.idempotency_key = 'supervisor-callback:' || c.id::text || ':attempt:' || ev.attempt_id::text
  ) AS has_failure_callback
FROM target_cards tc
JOIN cards c
  ON c.workspace_id = $1 AND c.id = tc.card_id
LEFT JOIN projects p
  ON p.workspace_id = c.workspace_id AND p.id = c.project_id
LEFT JOIN events ev
  ON ev.workspace_id = c.workspace_id AND ev.card_id = c.id AND ev.sequence = tc.event_seq
LEFT JOIN turn_attempts ma
  ON ma.workspace_id = c.workspace_id AND ma.card_id = c.id AND ma.id = ev.attempt_id
LEFT JOIN card_turns mt
  ON mt.workspace_id = c.workspace_id AND mt.card_id = c.id AND mt.id = ma.turn_id
LEFT JOIN LATERAL (
  SELECT COUNT(*)::int AS pending
  FROM card_tool_approvals
  WHERE workspace_id = c.workspace_id AND card_id = c.id AND status = 'pending'
) ap ON true
LEFT JOIN LATERAL (
  SELECT sequence, attempt_id
  FROM events
  WHERE workspace_id = c.workspace_id
    AND card_id = c.id
    AND event_type = 'status_change'
    AND content = 'working -> review'
  ORDER BY sequence DESC
  LIMIT 1
) rv ON c.kind = 'todo' AND c.todo_state = 'review'
LEFT JOIN LATERAL (
  -- 监督目标解析与 store.supervisorDiscussionID 保持严格一致：
  -- 优先选择活跃的 source_discussion_id，否则回退到活跃的项目 main_discussion_id。
  SELECT s.id, s.notifications_muted
  FROM (
    SELECT 1 AS priority, d.id, d.notifications_muted
    FROM cards d
    WHERE c.source_discussion_id IS NOT NULL
      AND d.workspace_id = c.workspace_id
      AND d.id = c.source_discussion_id
      AND d.kind = 'discussion'
      AND d.discussion_state = 'active'
    UNION ALL
    SELECT 2 AS priority, d.id, d.notifications_muted
    FROM cards d
    WHERE p.main_discussion_id IS NOT NULL
      AND d.workspace_id = c.workspace_id
      AND d.id = p.main_discussion_id
      AND d.kind = 'discussion'
      AND d.discussion_state = 'active'
  ) s
  ORDER BY s.priority ASC
  LIMIT 1
) sup ON c.kind IN ('todo', 'ongoing')
`, workspaceID, cardIDs, eventSeqs)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var facts cardNotificationFacts
		if err := rows.Scan(
			&facts.CardID,
			&facts.Kind,
			&facts.TodoState,
			&facts.ConfirmationRound,
			&facts.EventType,
			&facts.MessageType,
			&facts.MessageSeq,
			&facts.MessageAttemptID,
			&facts.PendingApprovalCount,
			&facts.MessageAttemptStatus,
			&facts.MessageTurnSource,
			&facts.MessageTurnIdempotencyKey,
			&facts.MessageTurnContent,
			&facts.ReviewEventSeq,
			&facts.ReviewAttemptID,
			&facts.SupervisorID,
			&facts.SupervisorMuted,
			&facts.HasReviewCallback,
			&facts.HasFailureCallback,
		); err != nil {
			return err
		}
		if idx, ok := indexByCard[facts.CardID]; ok {
			updates[idx].NotificationSuppressed = isSupervisorNotificationSuppressed(facts)
		}
	}
	return rows.Err()
}

// isSupervisorNotificationSuppressed 执行监督回调去重的 3 条核心收口规则：
// 1. 存在待工具审批或选择题（人工介入请求）时，任何卡片都不抑制推送；
// 2. 子任务（Todo / Ongoing）进入 Review 或触发失败/超时终态回调，且活跃监督讨论未静音并已成功创建对应回调 Turn 时，
//    子任务本次终态更新静默收口（防漏兜底：无活跃监督讨论或监督讨论已静音时照常推送；进 Review 后用户再追问产生的后续回复照常推送）；
// 3. 讨论卡处理“非熔断自动自愈重试”失败回调且未发出 choice / tool_approval 时静默收口；
//    Review 完成汇总、熔断终态汇报或普通对话/定时消息照常推送。
func isSupervisorNotificationSuppressed(f cardNotificationFacts) bool {
	if f.PendingApprovalCount > 0 || f.EventType == "tool_approval" || f.MessageType == "choice" {
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
