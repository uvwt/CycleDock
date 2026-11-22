package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
)

const (
	MaxCommentAttachments      = 8
	MaxCommentQuoteSenderRunes = 100
	MaxCommentQuoteTextRunes   = 1000
)

// CommentAttachmentRef 是写入 events.message 的轻量附件引用元数据（图片或纯文本/日志/代码），严禁包含原始文件二进制或 base64。
type CommentAttachmentRef struct {
	ID              uuid.UUID `json:"id"`
	URL             string    `json:"url"`
	Kind            string    `json:"kind,omitempty"`
	FileName        string    `json:"file_name,omitempty"`
	MimeType        string    `json:"mime_type"`
	Width           int       `json:"width"`
	Height          int       `json:"height"`
	SizeBytes       int64     `json:"size_bytes"`
	StoredSizeBytes int64     `json:"stored_size_bytes,omitempty"`
	LineCount       int       `json:"line_count,omitempty"`
	Status          string    `json:"status,omitempty"`
}

// CommentQuote 是人类消息引用某条历史消息（整条或划选片段）的结构化元数据。
// 与 Attachments 一并序列化到 events.message，零数据库迁移，同时供前端点击回跳和 Agent 理解引用上下文。
type CommentQuote struct {
	EventID uuid.UUID `json:"event_id"`
	Sender  string    `json:"sender"`
	Text    string    `json:"text"`
}

// CommentMessagePayload 是人类 comment 事件可选的轻量结构化载荷（附件引用与消息引用元数据）。
type CommentMessagePayload struct {
	Attachments []CommentAttachmentRef `json:"attachments,omitempty"`
	Quote       *CommentQuote          `json:"quote,omitempty"`
}

// truncateRunesWithEllipsis 按 Unicode rune 截断文本，超过上限时保留末尾省略号，避免切坏多字节字符。
func truncateRunesWithEllipsis(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func normalizeCommentQuote(raw *CommentQuote) (*CommentQuote, error) {
	if raw == nil {
		return nil, nil
	}
	if raw.EventID == uuid.Nil {
		return nil, fmt.Errorf("%w: quote event_id is required", domain.ErrConflict)
	}
	text := strings.ReplaceAll(raw.Text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text, _ = agentdata.NormalizeText(strings.TrimSpace(text))
	if text == "" {
		return nil, fmt.Errorf("%w: quote text is required", domain.ErrConflict)
	}
	text = truncateRunesWithEllipsis(text, MaxCommentQuoteTextRunes)

	sender, _ := agentdata.NormalizeText(strings.TrimSpace(raw.Sender))
	sender = truncateRunesWithEllipsis(sender, MaxCommentQuoteSenderRunes)

	return &CommentQuote{
		EventID: raw.EventID,
		Sender:  sender,
		Text:    text,
	}, nil
}

func formatAttachmentOnlyTurnContent(refs []CommentAttachmentRef) string {
	if len(refs) == 0 {
		return ""
	}
	var imageCount, textCount int
	for _, ref := range refs {
		if ref.Kind == "text" {
			textCount++
		} else {
			imageCount++
		}
	}
	if textCount == 0 {
		if imageCount <= 1 {
			return "[图片]"
		}
		return fmt.Sprintf("[图片 x%d]", imageCount)
	}
	if imageCount == 0 {
		if textCount == 1 && strings.TrimSpace(refs[0].FileName) != "" {
			return fmt.Sprintf("[文件: %s]", strings.TrimSpace(refs[0].FileName))
		}
		if textCount == 1 {
			return "[文件]"
		}
		return fmt.Sprintf("[文件 x%d]", textCount)
	}
	return fmt.Sprintf("[附件 x%d]", len(refs))
}

func normalizeCommentAttachmentIDs(ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return nil, fmt.Errorf("%w: invalid attachment id", domain.ErrConflict)
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) > MaxCommentAttachments {
		return nil, fmt.Errorf("%w: at most %d attachments are allowed per message", domain.ErrConflict, MaxCommentAttachments)
	}
	return out, nil
}

// DeviceTarget 是用户的执行偏好，不授予设备权限；实际调用始终由 MCP 校验授权。
type DeviceTarget struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
}

type SubmitSessionCommentParams struct {
	DeviceTarget          *DeviceTarget
	PreferredModelRouteID *uuid.UUID
	WorkspaceID           uuid.UUID
	CardID                uuid.UUID
	AuthorSourceID        uuid.UUID
	Content               string
	AttachmentIDs         []uuid.UUID
	Quote                 *CommentQuote
	IdempotencyKey        string
	DispatchMode          domain.DispatchMode
	// 回复绑定已完成的执行；选择回复还须绑定具体消息，防止旧页面误答。
	ReplyToAttemptID uuid.UUID
	ChoiceMessageID  uuid.UUID
}

type SubmitSessionCommentResult struct {
	EventID uuid.UUID
	TurnID  uuid.UUID
	Reused  bool
}

type EditSessionCommentParams struct {
	WorkspaceID    uuid.UUID
	CardID         uuid.UUID
	EventID        uuid.UUID
	AuthorSourceID uuid.UUID
	Content        string
}

// RegenerateSessionMessageParams 描述从某条对话气泡（AI 回复气泡或用户消息气泡）
// 截断后续记录并使用可选模型路由重新回答的请求参数。
type RegenerateSessionMessageParams struct {
	WorkspaceID           uuid.UUID
	CardID                uuid.UUID
	EventID               uuid.UUID
	AuthorSourceID        uuid.UUID
	PreferredModelRouteID *uuid.UUID
}

// validateEnabledWorkspaceModelRouteTx 校验可选的模型路由 ID 属于当前工作区且处于启用状态。
// 传入 nil 或 uuid.Nil 表示不指定单次覆盖，继续沿用卡片/工作区默认路由。
func validateEnabledWorkspaceModelRouteTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID uuid.UUID,
	routeID *uuid.UUID,
) (uuid.NullUUID, error) {
	if routeID == nil || *routeID == uuid.Nil {
		return uuid.NullUUID{}, nil
	}
	var enabled bool
	err := tx.QueryRow(ctx, `
		SELECT enabled
		FROM workspace_model_routes
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, *routeID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.NullUUID{}, fmt.Errorf("%w: model route not found in workspace", domain.ErrNotFound)
	}
	if err != nil {
		return uuid.NullUUID{}, err
	}
	if !enabled {
		return uuid.NullUUID{}, fmt.Errorf("%w: model route is disabled", domain.ErrConflict)
	}
	return uuid.NullUUID{UUID: *routeID, Valid: true}, nil
}

// ensureCardAllowsRerunTx 校验目标卡片当前生命周期是否允许截断历史并重新执行。
func ensureCardAllowsRerunTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID uuid.UUID,
) (string, error) {
	var cardKind, discussionState, todoState, ongoingState string
	if err := tx.QueryRow(ctx, `
		SELECT kind, COALESCE(discussion_state, ''), COALESCE(todo_state, ''), COALESCE(ongoing_state, '')
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID).Scan(&cardKind, &discussionState, &todoState, &ongoingState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrNotFound
		}
		return "", err
	}
	switch cardKind {
	case string(domain.CardKindDiscussion):
		if discussionState != string(domain.DiscussionStateActive) {
			return "", fmt.Errorf("%w: discussion is archived", domain.ErrInvalidTransition)
		}
	case string(domain.CardKindTodo):
		if todoState == string(domain.TodoStateDone) {
			return "", fmt.Errorf("%w: done todo card cannot rerun turns", domain.ErrInvalidTransition)
		}
	case string(domain.CardKindOngoing):
		if ongoingState != string(domain.OngoingStateActive) {
			return "", fmt.Errorf("%w: ongoing card is not active", domain.ErrInvalidTransition)
		}
	}
	return cardKind, nil
}

// truncateCardHistoryFromTurnTx 截断指定轮次（含该轮次自身旧执行）及其后的全部
// 业务事件、ADK 会话事件、turn_attempts 与后续 card_turns，保留源触发事件（若有）。
func truncateCardHistoryFromTurnTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID uuid.UUID,
	cardKind string,
	turnSequence int64,
	keepEventID uuid.NullUUID,
	cutoffEventSequence int64,
	approvalReason string,
) error {
	rows, err := tx.Query(ctx, `
		SELECT a.id, COALESCE(a.invocation_id, '')
		FROM turn_attempts a
		JOIN card_turns ct
		  ON ct.workspace_id = a.workspace_id
		 AND ct.card_id = a.card_id
		 AND ct.id = a.turn_id
		WHERE a.workspace_id = $1
		  AND a.card_id = $2
		  AND ct.sequence >= $3
		FOR UPDATE OF a
	`, workspaceID, cardID, turnSequence)
	if err != nil {
		return err
	}
	attemptIDs := make([]uuid.UUID, 0)
	invocationIDs := make([]string, 0)
	for rows.Next() {
		var attemptID uuid.UUID
		var invocationID string
		if err := rows.Scan(&attemptID, &invocationID); err != nil {
			rows.Close()
			return err
		}
		attemptIDs = append(attemptIDs, attemptID)
		if strings.TrimSpace(invocationID) != "" {
			invocationIDs = append(invocationIDs, invocationID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	// 先解除后续被删轮次的 source_event_id 引用再删 events，避免复合外键 ON DELETE SET NULL 误伤保留行。
	if _, err := tx.Exec(ctx, `
		UPDATE card_turns
		SET source_event_id = NULL
		WHERE workspace_id = $1
		  AND card_id = $2
		  AND sequence > $3
		  AND source_event_id IS NOT NULL
	`, workspaceID, cardID, turnSequence); err != nil {
		return err
	}

	// 先删除关联的业务 Events，避免先删 turn_attempts 触发 ON DELETE SET NULL (attempt_id)
	// 从而违反 events_reply_preview_check（带 preview_id 的消息要求 attempt_id IS NOT NULL）。
	if _, err := tx.Exec(ctx, `
		DELETE FROM events e
		WHERE e.workspace_id = $1
		  AND e.card_id = $2
		  AND ($3::uuid IS NULL OR e.id <> $3)
		  AND (
		    e.attempt_id = ANY($4::uuid[])
		    OR (
		      e.sequence > $5
		      AND NOT EXISTS (
		        SELECT 1
		        FROM turn_attempts prev_a
		        JOIN card_turns prev_t
		          ON prev_t.workspace_id = prev_a.workspace_id
		         AND prev_t.card_id = prev_a.card_id
		         AND prev_t.id = prev_a.turn_id
		        WHERE prev_a.workspace_id = e.workspace_id
		          AND prev_a.card_id = e.card_id
		          AND prev_a.id = e.attempt_id
		          AND prev_t.sequence < $6
		      )
		      AND NOT EXISTS (
		        SELECT 1
		        FROM card_turns prev_t
		        WHERE prev_t.workspace_id = e.workspace_id
		          AND prev_t.card_id = e.card_id
		          AND prev_t.source_event_id = e.id
		          AND prev_t.sequence < $6
		      )
		    )
		  )
	`, workspaceID, cardID, keepEventID, attemptIDs, cutoffEventSequence, turnSequence); err != nil {
		return err
	}

	if len(invocationIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			DELETE FROM agent_session_events
			WHERE app_name = 'cycledock'
			  AND user_id = $1
			  AND session_id = $2
			  AND (
			    invocation_id = ANY($3::text[])
			    OR sequence >= COALESCE(
			      (
			        SELECT MIN(ase.sequence)
			        FROM agent_session_events ase
			        WHERE ase.app_name = 'cycledock'
			          AND ase.user_id = $1
			          AND ase.session_id = $2
			          AND ase.invocation_id = ANY($3::text[])
			      ),
			      9223372036854775807
			    )
			  )
		`, workspaceID.String(), cardID.String(), invocationIDs); err != nil {
			return err
		}
	}

	if len(attemptIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE turn_attempts
			SET retry_of_attempt_id = NULL
			WHERE workspace_id = $1
			  AND card_id = $2
			  AND id = ANY($3::uuid[])
			  AND retry_of_attempt_id IS NOT NULL
		`, workspaceID, cardID, attemptIDs); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM turn_attempts
			WHERE workspace_id = $1
			  AND card_id = $2
			  AND id = ANY($3::uuid[])
		`, workspaceID, cardID, attemptIDs); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM card_turns
		WHERE workspace_id = $1
		  AND card_id = $2
		  AND sequence > $3
	`, workspaceID, cardID, turnSequence); err != nil {
		return err
	}

	if cardKind == string(domain.CardKindTodo) {
		if _, err := tx.Exec(ctx, `
			UPDATE cards c
			SET todo_state = COALESCE(
			      (
			        SELECT CASE
			          WHEN split_part(transition.content, ' -> ', 2) IN ('todo', 'working', 'review')
			            THEN split_part(transition.content, ' -> ', 2)
			          ELSE 'working'
			        END
			        FROM events transition
			        WHERE transition.workspace_id = c.workspace_id
			          AND transition.card_id = c.id
			          AND transition.event_type = 'status_change'
			        ORDER BY transition.sequence DESC
			        LIMIT 1
			      ),
			      'todo'
			    ),
			    confirmation_round = (
			      SELECT count(*)
			      FROM events transition
			      WHERE transition.workspace_id = c.workspace_id
			        AND transition.card_id = c.id
			        AND transition.event_type = 'status_change'
			        AND transition.content IN ('todo -> working', 'review -> working', 'done -> working')
			    ),
			    updated_at = now()
			WHERE c.workspace_id = $1 AND c.id = $2
		`, workspaceID, cardID); err != nil {
			return err
		}
	}

	return supersedePendingToolApprovalsTx(ctx, tx, workspaceID, cardID, approvalReason)
}

func (s *Store) SubmitSessionComment(
	ctx context.Context,
	p SubmitSessionCommentParams,
) (SubmitSessionCommentResult, error) {
	return s.submitSessionComment(ctx, p, false)
}

// EditSessionCommentAndRerun 编辑已发送的人类消息气泡，截断该消息之后的全部回复、后续轮次与 ADK Session 上下文，
// 并将该轮次重置为待领取状态，唤醒 Built-in Agent 重新回答。
func (s *Store) EditSessionCommentAndRerun(
	ctx context.Context,
	p EditSessionCommentParams,
) (SubmitSessionCommentResult, error) {
	p.Content, _ = agentdata.NormalizeText(strings.TrimSpace(p.Content))
	if p.Content == "" {
		return SubmitSessionCommentResult{}, fmt.Errorf("%w: comment content is required", domain.ErrConflict)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if err := requireHumanSource(ctx, tx, p.WorkspaceID, p.AuthorSourceID); err != nil {
		return SubmitSessionCommentResult{}, err
	}

	cardKind, err := ensureCardAllowsRerunTx(ctx, tx, p.WorkspaceID, p.CardID)
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}

	var eventSequence, turnSequence int64
	var turnID uuid.UUID
	var turnSource string
	err = tx.QueryRow(ctx, `
		SELECT e.sequence, t.id, t.sequence, t.source
		FROM events e
		JOIN card_turns t
		  ON t.workspace_id = e.workspace_id
		 AND t.card_id = e.card_id
		 AND t.source_event_id = e.id
		WHERE e.workspace_id = $1
		  AND e.card_id = $2
		  AND e.id = $3
		  AND e.event_type = 'comment'
		FOR UPDATE OF e, t
	`, p.WorkspaceID, p.CardID, p.EventID).Scan(&eventSequence, &turnID, &turnSequence, &turnSource)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubmitSessionCommentResult{}, domain.ErrNotFound
	}
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if turnSource != string(domain.TurnSourceHuman) {
		return SubmitSessionCommentResult{}, fmt.Errorf("%w: only human comments can be edited", domain.ErrConflict)
	}

	requestHash := stableRequestHash(
		"session_comment",
		p.WorkspaceID.String(),
		p.CardID.String(),
		p.AuthorSourceID.String(),
		string(domain.DispatchModeEnqueue),
		p.Content,
	)
	if err := truncateCardHistoryFromTurnTx(
		ctx, tx, p.WorkspaceID, p.CardID, cardKind, turnSequence,
		uuid.NullUUID{UUID: p.EventID, Valid: true}, eventSequence,
		"消息已编辑并重新执行，旧审批已自动失效",
	); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	// 仅更新正文与请求哈希，保留 events.message 中的既有附件引用与结构化 quote 元数据，
	// 以便前端继续展示原引用块，且后续重新执行时模型仍能读到同一引用上下文。
	if _, err := tx.Exec(ctx, `
		UPDATE events
		SET content = $4,
		    request_hash = $5
		WHERE workspace_id = $1 AND card_id = $2 AND id = $3
	`, p.WorkspaceID, p.CardID, p.EventID, p.Content, requestHash); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE card_turns
		SET content = $4,
		    dispatch_mode = 'enqueue',
		    cancelled_at = NULL,
		    cancelled_by_source_id = NULL,
		    cancel_reason = '',
		    request_hash = $5
		WHERE workspace_id = $1 AND card_id = $2 AND id = $3
	`, p.WorkspaceID, p.CardID, turnID, p.Content, requestHash); err != nil {
		return SubmitSessionCommentResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	s.notifyWorkReady()
	s.notifyWorkspaceChanged(p.WorkspaceID)
	return SubmitSessionCommentResult{EventID: p.EventID, TurnID: turnID}, nil
}

// RegenerateSessionMessage 从指定的 AI 回复气泡或用户消息气泡重新生成回答：
// 定位到对应 Turn，截断该 Turn 自身的旧回复及其之后的所有记录和 ADK Session 历史，
// 将该 Turn 的单次 preferred_model_route_id 覆盖为所选路由（传 nil 则沿用卡片/工作区默认），
// 并重新排队唤醒 Built-in Agent 执行（不修改卡片默认模型路由与工作区默认模型路由）。
func (s *Store) RegenerateSessionMessage(
	ctx context.Context,
	p RegenerateSessionMessageParams,
) (SubmitSessionCommentResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if err := requireHumanSource(ctx, tx, p.WorkspaceID, p.AuthorSourceID); err != nil {
		return SubmitSessionCommentResult{}, err
	}

	cardKind, err := ensureCardAllowsRerunTx(ctx, tx, p.WorkspaceID, p.CardID)
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}

	preferredRouteID, err := validateEnabledWorkspaceModelRouteTx(
		ctx, tx, p.WorkspaceID, p.PreferredModelRouteID,
	)
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}

	var eventType string
	var eventSequence int64
	var eventAttemptID uuid.NullUUID
	err = tx.QueryRow(ctx, `
		SELECT event_type, sequence, attempt_id
		FROM events
		WHERE workspace_id = $1 AND card_id = $2 AND id = $3
		FOR UPDATE
	`, p.WorkspaceID, p.CardID, p.EventID).Scan(&eventType, &eventSequence, &eventAttemptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubmitSessionCommentResult{}, domain.ErrNotFound
	}
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}

	var turnID uuid.UUID
	var turnSequence int64
	var sourceEventID uuid.NullUUID
	var cutoffEventSequence int64

	switch eventType {
	case "message":
		if !eventAttemptID.Valid {
			return SubmitSessionCommentResult{}, fmt.Errorf("%w: reply message has no associated turn attempt", domain.ErrConflict)
		}
		err = tx.QueryRow(ctx, `
			SELECT t.id, t.sequence, t.source_event_id
			FROM turn_attempts a
			JOIN card_turns t
			  ON t.workspace_id = a.workspace_id
			 AND t.card_id = a.card_id
			 AND t.id = a.turn_id
			WHERE a.workspace_id = $1
			  AND a.card_id = $2
			  AND a.id = $3
			FOR UPDATE OF t
		`, p.WorkspaceID, p.CardID, eventAttemptID.UUID).Scan(&turnID, &turnSequence, &sourceEventID)
		if errors.Is(err, pgx.ErrNoRows) {
			return SubmitSessionCommentResult{}, domain.ErrNotFound
		}
		if err != nil {
			return SubmitSessionCommentResult{}, err
		}
		if sourceEventID.Valid {
			err = tx.QueryRow(ctx, `
				SELECT sequence
				FROM events
				WHERE workspace_id = $1 AND card_id = $2 AND id = $3
			`, p.WorkspaceID, p.CardID, sourceEventID.UUID).Scan(&cutoffEventSequence)
			if errors.Is(err, pgx.ErrNoRows) {
				sourceEventID = uuid.NullUUID{}
				cutoffEventSequence = eventSequence
			} else if err != nil {
				return SubmitSessionCommentResult{}, err
			}
		} else {
			// 定时/系统触发等无 source_event_id 的轮次：以本轮最早产出的事件前一位作为截断点，
			// 保留本轮之前的所有历史事件，截断本轮及之后产生的全部事件。
			err = tx.QueryRow(ctx, `
				SELECT COALESCE(MIN(e.sequence) - 1, $4 - 1)
				FROM events e
				JOIN turn_attempts a
				  ON a.workspace_id = e.workspace_id
				 AND a.card_id = e.card_id
				 AND a.id = e.attempt_id
				WHERE e.workspace_id = $1
				  AND e.card_id = $2
				  AND a.turn_id = $3
			`, p.WorkspaceID, p.CardID, turnID, eventSequence).Scan(&cutoffEventSequence)
			if err != nil {
				return SubmitSessionCommentResult{}, err
			}
		}
	case "comment":
		err = tx.QueryRow(ctx, `
			SELECT t.id, t.sequence
			FROM card_turns t
			WHERE t.workspace_id = $1
			  AND t.card_id = $2
			  AND t.source_event_id = $3
			FOR UPDATE OF t
		`, p.WorkspaceID, p.CardID, p.EventID).Scan(&turnID, &turnSequence)
		if errors.Is(err, pgx.ErrNoRows) {
			return SubmitSessionCommentResult{}, domain.ErrNotFound
		}
		if err != nil {
			return SubmitSessionCommentResult{}, err
		}
		sourceEventID = uuid.NullUUID{UUID: p.EventID, Valid: true}
		cutoffEventSequence = eventSequence
	default:
		return SubmitSessionCommentResult{}, fmt.Errorf("%w: only reply or comment bubbles can be regenerated", domain.ErrConflict)
	}

	if err := truncateCardHistoryFromTurnTx(
		ctx, tx, p.WorkspaceID, p.CardID, cardKind, turnSequence,
		sourceEventID, cutoffEventSequence,
		"已发起重新回答，旧审批已自动失效",
	); err != nil {
		return SubmitSessionCommentResult{}, err
	}

	// 覆盖本轮的单次优先模型路由并重置为待执行状态（不篡改卡片默认路由）。
	if _, err := tx.Exec(ctx, `
		UPDATE card_turns
		SET preferred_model_route_id = $4,
		    dispatch_mode = 'enqueue',
		    cancelled_at = NULL,
		    cancelled_by_source_id = NULL,
		    cancel_reason = ''
		WHERE workspace_id = $1 AND card_id = $2 AND id = $3
	`, p.WorkspaceID, p.CardID, turnID, preferredRouteID); err != nil {
		return SubmitSessionCommentResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	s.notifyWorkReady()

	resultEventID := p.EventID
	if sourceEventID.Valid {
		resultEventID = sourceEventID.UUID
	}
	return SubmitSessionCommentResult{EventID: resultEventID, TurnID: turnID}, nil
}

func (s *Store) ReplySessionAndContinue(
	ctx context.Context,
	p SubmitSessionCommentParams,
) (SubmitSessionCommentResult, error) {
	if p.ReplyToAttemptID == uuid.Nil {
		return SubmitSessionCommentResult{}, fmt.Errorf("%w: reply attempt is required", domain.ErrConflict)
	}
	p.DispatchMode = domain.DispatchModeEnqueue
	return s.submitSessionComment(ctx, p, true)
}

func (s *Store) submitSessionComment(
	ctx context.Context,
	p SubmitSessionCommentParams,
	replyToMessage bool,
) (SubmitSessionCommentResult, error) {
	p.Content, _ = agentdata.NormalizeText(strings.TrimSpace(p.Content))
	p.IdempotencyKey, _ = agentdata.NormalizeText(strings.TrimSpace(p.IdempotencyKey))
	attachmentIDs, err := normalizeCommentAttachmentIDs(p.AttachmentIDs)
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}
	quote, err := normalizeCommentQuote(p.Quote)
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if p.Content == "" && len(attachmentIDs) == 0 {
		return SubmitSessionCommentResult{}, fmt.Errorf("%w: comment content or image attachment is required", domain.ErrConflict)
	}
	if p.IdempotencyKey == "" {
		return SubmitSessionCommentResult{}, fmt.Errorf("%w: idempotency key is required", domain.ErrConflict)
	}
	if p.DispatchMode != domain.DispatchModeEnqueue &&
		p.DispatchMode != domain.DispatchModeInterrupt {
		return SubmitSessionCommentResult{}, fmt.Errorf("invalid dispatch mode %q", p.DispatchMode)
	}

	var deviceNodeID, deviceName string
	if p.DeviceTarget != nil {
		deviceNodeID, _ = agentdata.NormalizeText(strings.TrimSpace(p.DeviceTarget.NodeID))
		deviceName, _ = agentdata.NormalizeText(strings.TrimSpace(p.DeviceTarget.Name))
		if deviceNodeID == "" || len([]rune(deviceNodeID)) > 200 || len([]rune(deviceName)) > 200 {
			return SubmitSessionCommentResult{}, fmt.Errorf("%w: invalid device target", domain.ErrConflict)
		}
	}

	requestHash := stableRequestHash(
		"session_comment",
		p.WorkspaceID.String(),
		p.CardID.String(),
		p.AuthorSourceID.String(),
		string(p.DispatchMode),
		p.Content,
	)

	if len(attachmentIDs) > 0 {
		attHashParts := make([]string, 0, len(attachmentIDs)+2)
		attHashParts = append(attHashParts, requestHash, "attachments")
		for _, id := range attachmentIDs {
			attHashParts = append(attHashParts, id.String())
		}
		requestHash = stableRequestHash(attHashParts...)
	}

	if deviceNodeID != "" {
		requestHash = stableRequestHash(requestHash, "device_target", deviceNodeID, deviceName)
	}

	if p.PreferredModelRouteID != nil && *p.PreferredModelRouteID != uuid.Nil {
		requestHash = stableRequestHash(requestHash, "preferred_model_route", p.PreferredModelRouteID.String())
	}

	if quote != nil {
		requestHash = stableRequestHash(requestHash, "quote", quote.EventID.String(), quote.Sender, quote.Text)
	}

	if replyToMessage {
		requestHash = stableRequestHash(requestHash, "reply", p.ReplyToAttemptID.String(), p.ChoiceMessageID.String())
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if err := requireHumanSource(ctx, tx, p.WorkspaceID, p.AuthorSourceID); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if err := ensureDiscussionAcceptsWork(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return SubmitSessionCommentResult{}, err
	}

	if existing, found, err := findSessionComment(
		ctx, tx, p.WorkspaceID, p.AuthorSourceID, p.IdempotencyKey,
	); err != nil {
		return SubmitSessionCommentResult{}, err
	} else if found {
		if existing.RequestHash != requestHash || existing.CardID != p.CardID {
			return SubmitSessionCommentResult{}, domain.ErrIdempotencyMismatch
		}
		if err := tx.Commit(ctx); err != nil {
			return SubmitSessionCommentResult{}, err
		}
		s.notifyWorkReady()
		return SubmitSessionCommentResult{
			EventID: existing.EventID,
			TurnID:  existing.TurnID,
			Reused:  true,
		}, nil
	}

	if replyToMessage {
		var status string
		err := tx.QueryRow(ctx, `
			SELECT a.status FROM turn_attempts a
			JOIN card_turns t ON t.workspace_id=a.workspace_id AND t.id=a.turn_id
			WHERE a.workspace_id=$1 AND a.card_id=$2 AND a.id=$3
			  AND NOT EXISTS (
				SELECT 1 FROM card_turns newer
				WHERE newer.workspace_id=t.workspace_id AND newer.card_id=t.card_id
				  AND newer.sequence>t.sequence AND newer.cancelled_at IS NULL
			  )
			FOR UPDATE OF a
		`, p.WorkspaceID, p.CardID, p.ReplyToAttemptID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return SubmitSessionCommentResult{}, domain.ErrAttemptNotCurrent
		}
		if err != nil {
			return SubmitSessionCommentResult{}, err
		}
		if status != string(domain.AttemptStatusCompleted) {
			return SubmitSessionCommentResult{}, domain.ErrAttemptNotCurrent
		}
		if p.ChoiceMessageID != uuid.Nil {
			// 与创建下一轮共用 Card 行锁，同一个问题只能创建一轮回答，后续输入使旧选择失效。
			var payload []byte
			err := tx.QueryRow(ctx, `
				SELECT message FROM events
				WHERE workspace_id=$1 AND card_id=$2 AND attempt_id=$3
				  AND id=$4 AND event_type='message'
			`, p.WorkspaceID, p.CardID, p.ReplyToAttemptID, p.ChoiceMessageID).Scan(&payload)
			if errors.Is(err, pgx.ErrNoRows) {
				return SubmitSessionCommentResult{}, fmt.Errorf("%w: choice message does not match attempt", domain.ErrConflict)
			}
			if err != nil {
				return SubmitSessionCommentResult{}, err
			}
			var message ReplyMessage
			if err := json.Unmarshal(payload, &message); err != nil {
				return SubmitSessionCommentResult{}, err
			}
			validOption := false
			if message.Type == "choice" && message.Widget != nil {
				for _, option := range message.Widget.Options {
					if option.Value == p.Content {
						validOption = true
						break
					}
				}
			}
			if !validOption {
				return SubmitSessionCommentResult{}, fmt.Errorf("%w: invalid choice option", domain.ErrConflict)
			}
		}
	}

	if quote != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM events
				WHERE workspace_id = $1 AND card_id = $2 AND id = $3
			)
		`, p.WorkspaceID, p.CardID, quote.EventID).Scan(&exists); err != nil {
			return SubmitSessionCommentResult{}, err
		}
		if !exists {
			return SubmitSessionCommentResult{}, fmt.Errorf("%w: quoted event not found in card", domain.ErrNotFound)
		}
	}

	var rawMessage []byte
	var attachmentRefs []CommentAttachmentRef
	var payload CommentMessagePayload
	if len(attachmentIDs) > 0 {
		attachmentRefs, err = resolveAndBindCommentAttachmentsTx(
			ctx, tx, p.WorkspaceID, p.CardID, attachmentIDs,
		)
		if err != nil {
			return SubmitSessionCommentResult{}, err
		}
		payload.Attachments = attachmentRefs
	}
	if quote != nil {
		payload.Quote = quote
	}
	if len(payload.Attachments) > 0 || payload.Quote != nil {
		rawMessage, err = json.Marshal(payload)
		if err != nil {
			return SubmitSessionCommentResult{}, fmt.Errorf("marshal comment message payload: %w", err)
		}
	}

	eventID := uuid.New()
	var messageArg any
	if len(rawMessage) > 0 {
		messageArg = rawMessage
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id,
			content, message, idempotency_key, request_hash
		)
		VALUES ($1,$2,$3,'comment',$4,$5,$6,$7,$8)
	`, eventID, p.WorkspaceID, p.CardID, p.AuthorSourceID,
		p.Content, messageArg, p.IdempotencyKey, requestHash)
	if err != nil {
		return SubmitSessionCommentResult{}, mapConstraintError(err)
	}

	var preferredRouteID uuid.NullUUID
	if p.PreferredModelRouteID != nil && *p.PreferredModelRouteID != uuid.Nil {
		preferredRouteID, err = validateEnabledWorkspaceModelRouteTx(
			ctx, tx, p.WorkspaceID, p.PreferredModelRouteID,
		)
		if err != nil {
			return SubmitSessionCommentResult{}, err
		}
	}

	turnContent := p.Content
	if turnContent == "" {
		turnContent = formatAttachmentOnlyTurnContent(attachmentRefs)
	}

	turn, err := insertTurn(ctx, tx, insertTurnParams{
		DeviceNodeID: deviceNodeID, DeviceName: deviceName,
		PreferredModelRouteID: preferredRouteID,
		WorkspaceID:           p.WorkspaceID,
		CardID:                p.CardID,
		Source:                domain.TurnSourceHuman,
		Content:               turnContent,
		CreatedBySourceID:     uuid.NullUUID{UUID: p.AuthorSourceID, Valid: true},
		SourceEventID:         uuid.NullUUID{UUID: eventID, Valid: true},
		DispatchMode:          p.DispatchMode,
		IdempotencyKey:        p.IdempotencyKey,
		RequestHash:           requestHash,
	})
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if err := supersedeAttemptForHumanTurn(
		ctx, tx, p.WorkspaceID, p.CardID, p.DispatchMode, turn.CreatedAt,
	); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if err := supersedePendingToolApprovalsTx(
		ctx, tx, p.WorkspaceID, p.CardID, "已有后续新对话，旧审批已自动失效",
	); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	s.notifyWorkReady()
	s.notifyWorkspaceChanged(p.WorkspaceID)
	return SubmitSessionCommentResult{EventID: eventID, TurnID: turn.ID}, nil
}

type existingSessionComment struct {
	EventID     uuid.UUID
	TurnID      uuid.UUID
	CardID      uuid.UUID
	RequestHash string
}

func findSessionComment(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sourceID uuid.UUID,
	idempotencyKey string,
) (existingSessionComment, bool, error) {
	var out existingSessionComment
	err := tx.QueryRow(ctx, `
		SELECT e.id, e.card_id, t.id, COALESCE(e.request_hash,'')
		FROM events e
		JOIN card_turns t
		  ON t.workspace_id=e.workspace_id
		 AND t.card_id=e.card_id
		 AND t.created_by_source_id=e.actor_source_id
		 AND t.idempotency_key=e.idempotency_key
		WHERE e.workspace_id=$1
		  AND e.actor_source_id=$2
		  AND e.event_type='comment'
		  AND e.idempotency_key=$3
	`, workspaceID, sourceID, idempotencyKey).Scan(
		&out.EventID, &out.CardID, &out.TurnID, &out.RequestHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return existingSessionComment{}, false, nil
	}
	if err != nil {
		return existingSessionComment{}, false, err
	}
	return out, true, nil
}

func resolveAndBindCommentAttachmentsTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID uuid.UUID,
	attachmentIDs []uuid.UUID,
) ([]CommentAttachmentRef, error) {
	now := time.Now().UTC()
	rows, err := tx.Query(ctx, `
		SELECT id, card_id, kind, file_name, mime_type, size_bytes, stored_size_bytes, line_count, width, height, expires_at, purged_at
		FROM workspace_attachments
		WHERE workspace_id = $1 AND id = ANY($2::uuid[])
		FOR UPDATE
	`, workspaceID, attachmentIDs)
	if err != nil {
		return nil, fmt.Errorf("load comment attachments: %w", err)
	}
	defer rows.Close()

	byID := make(map[uuid.UUID]CommentAttachmentRef, len(attachmentIDs))
	unbindableIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		var boundCardID uuid.NullUUID
		var kind string
		var fileName string
		var mimeType string
		var sizeBytes, storedSizeBytes int64
		var lineCount, width, height int
		var expiresAt time.Time
		var purgedAt *time.Time
		if err := rows.Scan(&id, &boundCardID, &kind, &fileName, &mimeType, &sizeBytes, &storedSizeBytes, &lineCount, &width, &height, &expiresAt, &purgedAt); err != nil {
			return nil, fmt.Errorf("scan comment attachment: %w", err)
		}
		if purgedAt != nil || !expiresAt.After(now) {
			return nil, fmt.Errorf("%w: attachment has expired", domain.ErrConflict)
		}
		if boundCardID.Valid && boundCardID.UUID != cardID {
			return nil, fmt.Errorf("%w: attachment belongs to another card", domain.ErrConflict)
		}
		if !boundCardID.Valid {
			unbindableIDs = append(unbindableIDs, id)
		}
		byID[id] = CommentAttachmentRef{
			ID:              id,
			URL:             fmt.Sprintf("/api/v1/workspaces/%s/attachments/%s", workspaceID.String(), id.String()),
			Kind:            kind,
			FileName:        fileName,
			MimeType:        mimeType,
			Width:           width,
			Height:          height,
			SizeBytes:       sizeBytes,
			StoredSizeBytes: storedSizeBytes,
			LineCount:       lineCount,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(byID) != len(attachmentIDs) {
		return nil, fmt.Errorf("%w: one or more attachments were not found", domain.ErrConflict)
	}

	if len(unbindableIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE workspace_attachments
			SET card_id = $3
			WHERE workspace_id = $1 AND id = ANY($2::uuid[]) AND card_id IS NULL
		`, workspaceID, unbindableIDs, cardID); err != nil {
			return nil, fmt.Errorf("bind comment attachments to card: %w", err)
		}
	}

	ordered := make([]CommentAttachmentRef, 0, len(attachmentIDs))
	for _, id := range attachmentIDs {
		ordered = append(ordered, byID[id])
	}
	return ordered, nil
}

