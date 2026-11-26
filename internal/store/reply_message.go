package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/sqldb"
)

// ReplyMessage 是一条独立的用户可见气泡。业务状态仍由 Card/Attempt 决定。
type ReplyMessage struct {
	Type    string        `json:"type"`
	Content string        `json:"content,omitempty"`
	URL     string        `json:"url,omitempty"`
	Alt     string        `json:"alt,omitempty"`
	Images  []string      `json:"images,omitempty"`
	Widget  *ChoiceWidget `json:"widget,omitempty"`
	EndTurn bool          `json:"end_turn"`
}

type ChoiceWidget struct {
	Question string         `json:"question"`
	Options  []ChoiceOption `json:"options"`
}

type ChoiceOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// replyPayload 的预览身份仅由 Runner 设置，不接受模型或外部 MCP 指定。
type replyPayload struct {
	ReplyMessage
	PreviewID string `json:"preview_id,omitempty"`
	Preview   bool   `json:"preview,omitempty"`
	Model     string `json:"model,omitempty"`
}

type PublishReplyParams struct {
	WorkspaceID, CardID, AttemptID, RunnerSourceID uuid.UUID
	Message                                        ReplyMessage
	IdempotencyKey                                 string
	PreviewID                                      string
	Model                                          string
	// 外部 MCP 没有 ADK Runner 善后，最后一条消息同时结算 Attempt。
	FinishOnEnd bool
}

func validReplyURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" &&
		u.User == nil && u.Fragment == "" && !strings.ContainsAny(raw, "\r\n")
}

func (m *ReplyMessage) Validate() error {
	m.Type, _ = agentdata.NormalizeText(strings.TrimSpace(m.Type))
	m.Content, _ = agentdata.NormalizeText(strings.TrimSpace(m.Content))
	m.URL, _ = agentdata.NormalizeText(strings.TrimSpace(m.URL))
	m.Alt, _ = agentdata.NormalizeText(strings.TrimSpace(m.Alt))
	if len(m.Content) > 20000 || len(m.Alt) > 500 || len(m.Images) > 8 {
		return fmt.Errorf("%w: reply exceeds size limit", domain.ErrConflict)
	}
	for i := range m.Images {
		m.Images[i], _ = agentdata.NormalizeText(strings.TrimSpace(m.Images[i]))
		if len(m.Images[i]) > 2048 || !validReplyURL(m.Images[i]) {
			return fmt.Errorf("%w: invalid image URL", domain.ErrConflict)
		}
	}
	switch m.Type {
	case "text":
		// 允许发送纯图片气泡（images 非空时 content 可留空）；未附图片时仍强制要求正文非空。
		if m.Content == "" && len(m.Images) == 0 {
			return fmt.Errorf("%w: text reply requires content or images", domain.ErrConflict)
		}
		if m.URL != "" {
			return fmt.Errorf("%w: text reply must omit url; use type=attachment for attachments", domain.ErrConflict)
		}
	case "attachment":
		if len(m.URL) > 2048 || !validReplyURL(m.URL) {
			return fmt.Errorf("%w: attachment requires HTTPS URL", domain.ErrConflict)
		}
	case "choice":
		if !m.EndTurn || m.Widget == nil || m.URL != "" || len(m.Images) != 0 {
			return fmt.Errorf("%w: choice requires widget and end_turn", domain.ErrConflict)
		}
		m.Widget.Question, _ = agentdata.NormalizeText(strings.TrimSpace(m.Widget.Question))
		if m.Widget.Question == "" || len(m.Widget.Question) > 1000 ||
			len(m.Widget.Options) < 2 || len(m.Widget.Options) > 8 {
			return fmt.Errorf("%w: invalid choice question or options", domain.ErrConflict)
		}
		values := map[string]bool{}
		for i := range m.Widget.Options {
			o := &m.Widget.Options[i]
			o.Value, _ = agentdata.NormalizeText(strings.TrimSpace(o.Value))
			o.Label, _ = agentdata.NormalizeText(strings.TrimSpace(o.Label))
			if o.Value == "" || o.Label == "" || len(o.Value) > 200 ||
				len(o.Label) > 200 || values[o.Value] {
				return fmt.Errorf("%w: invalid or duplicate option", domain.ErrConflict)
			}
			values[o.Value] = true
		}
	default:
		return fmt.Errorf("%w: unsupported reply type", domain.ErrConflict)
	}
	if m.Type != "choice" && m.Widget != nil {
		return fmt.Errorf("%w: widget is only allowed for type=choice; omit widget for text and attachment replies", domain.ErrConflict)
	}
	return nil
}

// PublishReply 与 Attempt 状态变化共用 Card 行锁；一次调用只提交一条 Message。
// Built-in Runner 在 ADK 尾部事件保存后统一结算；外部调用在消息事务内结束执行。
func (s *Store) PublishReply(ctx context.Context, p PublishReplyParams) (uuid.UUID, bool, error) {
	if err := p.Message.Validate(); err != nil {
		return uuid.Nil, false, err
	}
	p.IdempotencyKey, _ = agentdata.NormalizeText(strings.TrimSpace(p.IdempotencyKey))
	p.PreviewID, _ = agentdata.NormalizeText(p.PreviewID)
	p.Model, _ = agentdata.NormalizeText(strings.TrimSpace(p.Model))
	if p.IdempotencyKey == "" || len(p.IdempotencyKey) > 200 {
		return uuid.Nil, false, fmt.Errorf("%w: idempotency key required", domain.ErrConflict)
	}
	payload, _, err := agentdata.MarshalJSONB(replyPayload{
		ReplyMessage: p.Message,
		PreviewID:    p.PreviewID,
		Model:        p.Model,
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	// 预览身份不改变业务幂等语义；模型路由重试可能为同一回复创建新预览。
	requestPayload, _, err := agentdata.MarshalJSONB(p.Message)
	if err != nil {
		return uuid.Nil, false, err
	}
	hash := stableRequestHash("reply_message", p.WorkspaceID.String(), p.CardID.String(),
		p.AttemptID.String(), p.RunnerSourceID.String(), string(requestPayload))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return uuid.Nil, false, err
	}
	// 幂等读取放在状态检查之前：成功发送后 end_turn 可能已关闭 Attempt。
	// reply_message 幂等键限定在当前卡片与当前 Attempt 内，避免跨卡或跨轮次误撞冲突。
	var oldID uuid.UUID
	var oldHash string
	err = tx.QueryRow(ctx, `SELECT id,COALESCE(request_hash,'')
		FROM events WHERE workspace_id=$1 AND card_id=$2 AND attempt_id=$3
		AND actor_source_id=$4 AND event_type='message' AND idempotency_key=$5`,
		p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID, p.IdempotencyKey).
		Scan(&oldID, &oldHash)
	if err == nil {
		if oldHash != hash {
			return uuid.Nil, false, domain.ErrIdempotencyMismatch
		}
		if p.PreviewID != "" {
			if _, err := deleteReplyPreviewEventsTx(ctx, tx, p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID, p.PreviewID, oldID); err != nil {
				return uuid.Nil, false, err
			}
		}
		return oldID, true, tx.Commit(ctx)
	}
	if !errors.Is(err, sqldb.ErrNoRows) {
		return uuid.Nil, false, err
	}
	if err := requireCurrentAttempt(ctx, tx, p.WorkspaceID, p.CardID,
		p.AttemptID, p.RunnerSourceID, domain.AttemptStatusRunning); err != nil {
		return uuid.Nil, false, err
	}
	// 正式发送原地接替已持久化预览，气泡 ID、顺序与创建时间保持稳定。
	id := uuid.Nil
	if p.PreviewID != "" {
		previewEventID, _, found, findErr := findReplyPreviewEventTx(ctx, tx, p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID, p.PreviewID)
		if findErr != nil {
			return uuid.Nil, false, findErr
		}
		if found {
			err = tx.QueryRow(ctx, `UPDATE events SET content=$3,message=$4,idempotency_key=$5,request_hash=$6
				WHERE workspace_id=$1 AND id=$2 AND idempotency_key IS NULL
				RETURNING id`, p.WorkspaceID, previewEventID,
				p.Message.Content, payload, p.IdempotencyKey, hash).Scan(&id)
			if err != nil && !errors.Is(err, sqldb.ErrNoRows) {
				return uuid.Nil, false, err
			}
		}
	}
	if id == uuid.Nil {
		id = uuid.New()
		_, err = tx.Exec(ctx, `INSERT INTO events
			(id,workspace_id,card_id,event_type,actor_source_id,attempt_id,content,message,idempotency_key,request_hash)
			VALUES ($1,$2,$3,'message',$4,$5,$6,$7,$8,$9)`,
			id, p.WorkspaceID, p.CardID, p.RunnerSourceID, p.AttemptID,
			p.Message.Content, payload, p.IdempotencyKey, hash)
		if err != nil {
			return uuid.Nil, false, mapConstraintError(err)
		}
	}
	if p.Message.EndTurn && p.FinishOnEnd {
		now := time.Now().UTC()
		command, err := tx.Exec(ctx, `UPDATE turn_attempts
			SET status='completed', heartbeat_at=NULL,
				lease_expires_at=NULL, finished_at=$5, updated_at=$5
			WHERE workspace_id=$1 AND card_id=$2 AND id=$3
				AND runner_source_id=$4 AND status='running'`,
			p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID, now)
		if err != nil {
			return uuid.Nil, false, err
		}
		if command.RowsAffected() != 1 {
			return uuid.Nil, false, domain.ErrAttemptNotCurrent
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	s.notifyWorkspaceChanged(p.WorkspaceID)
	return id, false, nil
}

type ReplyPreviewParams struct {
	WorkspaceID, CardID, AttemptID, RunnerSourceID uuid.UUID
	PreviewID, Content                             string
}

// SaveReplyPreview 先保存已经可以展示的正文，再允许 SSE 对外发布。
// 与取消/替代共用 Card 行锁：停止后的迟到片段既不能落库，也不能重新打开气泡。
func (s *Store) SaveReplyPreview(ctx context.Context, p ReplyPreviewParams) error {
	p.PreviewID, _ = agentdata.NormalizeText(p.PreviewID)
	p.Content, _ = agentdata.NormalizeText(p.Content)
	if p.PreviewID == "" || len(p.PreviewID) > 512 || p.Content == "" || len(p.Content) > 20000 {
		return fmt.Errorf("%w: invalid reply preview", domain.ErrConflict)
	}
	payload, _, err := agentdata.MarshalJSONB(replyPayload{
		ReplyMessage: ReplyMessage{Type: "text", Content: p.Content}, PreviewID: p.PreviewID, Preview: true,
	})
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return err
	}
	if err := requireCurrentAttempt(ctx, tx, p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID, domain.AttemptStatusRunning); err != nil {
		return err
	}
	// 已持有 Card 行锁（PG）或单写事务（SQLite），先查找同 attempt + preview_id 的既有事件，
	// 避免使用 PG 专属表达式索引 ON CONFLICT ((message->>'preview_id')) 与 left() 函数。
	existingID, existingContent, existingIdem, found, err := findAnyReplyPreviewEventTx(ctx, tx, p.WorkspaceID, p.CardID, p.AttemptID, p.PreviewID)
	if err != nil {
		return err
	}
	if found {
		if existingIdem || !strings.HasPrefix(p.Content, existingContent) {
			return fmt.Errorf("%w: reply preview already sent or rewritten", domain.ErrConflict)
		}
		command, err := tx.Exec(ctx, `UPDATE events SET content=$3,message=$4
			WHERE workspace_id=$1 AND id=$2 AND idempotency_key IS NULL`,
			p.WorkspaceID, existingID, p.Content, payload)
		if err != nil {
			return mapConstraintError(err)
		}
		if command.RowsAffected() != 1 {
			return fmt.Errorf("%w: reply preview already sent or rewritten", domain.ErrConflict)
		}
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO events
			(id,workspace_id,card_id,event_type,actor_source_id,attempt_id,content,message)
			VALUES ($1,$2,$3,'message',$4,$5,$6,$7)`,
			uuid.New(), p.WorkspaceID, p.CardID, p.RunnerSourceID, p.AttemptID, p.Content, payload)
		if err != nil {
			return mapConstraintError(err)
		}
	}
	return tx.Commit(ctx)
}

type DiscardReplyPreviewParams struct {
	WorkspaceID, CardID, AttemptID, RunnerSourceID uuid.UUID
	PreviewID                                      string
}

// DiscardReplyPreview 删除单个被拒绝或调用失败的未转正预览气泡。
// 仅在 Attempt 仍处于 running 状态且该预览尚未写入 idempotency_key 时删除，
// 不影响已正式提交的消息，也不会误删已取消/已替代 Attempt 保留的流式正文。
func (s *Store) DiscardReplyPreview(ctx context.Context, p DiscardReplyPreviewParams) error {
	p.PreviewID, _ = agentdata.NormalizeText(p.PreviewID)
	if p.PreviewID == "" || len(p.PreviewID) > 512 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return err
	}
	if err := requireCurrentAttempt(ctx, tx, p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID, domain.AttemptStatusRunning); err != nil {
		return err
	}
	deleted, err := deleteReplyPreviewEventsTx(ctx, tx, p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID, p.PreviewID, uuid.Nil)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if deleted > 0 {
		s.notifyWorkspaceChanged(p.WorkspaceID)
	}
	return nil
}

// findAnyReplyPreviewEventTx 在单次 Attempt 内按 preview_id 查找已落库的事件（含已转正与未转正）。
func findAnyReplyPreviewEventTx(
	ctx context.Context, tx *sqldb.Tx,
	workspaceID, cardID, attemptID uuid.UUID,
	previewID string,
) (eventID uuid.UUID, content string, hasIdempotencyKey bool, found bool, err error) {
	rows, err := tx.Query(ctx, `
		SELECT id, content, message, idempotency_key
		FROM events
		WHERE workspace_id=$1 AND card_id=$2 AND attempt_id=$3 AND event_type='message'
		ORDER BY sequence ASC
	`, workspaceID, cardID, attemptID)
	if err != nil {
		return uuid.Nil, "", false, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var body string
		var raw []byte
		var idem sqldb.Text
		if err := rows.Scan(&id, &body, &raw, &idem); err != nil {
			return uuid.Nil, "", false, false, err
		}
		if extractPreviewID(raw) == previewID {
			return id, body, idem.Valid, true, nil
		}
	}
	return uuid.Nil, "", false, false, rows.Err()
}

// findReplyPreviewEventTx 查找可被正式 reply_message 原地接替的未转正预览气泡。
func findReplyPreviewEventTx(
	ctx context.Context, tx *sqldb.Tx,
	workspaceID, cardID, attemptID, actorSourceID uuid.UUID,
	previewID string,
) (eventID uuid.UUID, content string, found bool, err error) {
	rows, err := tx.Query(ctx, `
		SELECT id, content, message
		FROM events
		WHERE workspace_id=$1 AND card_id=$2 AND attempt_id=$3
		  AND actor_source_id=$4 AND event_type='message' AND idempotency_key IS NULL
		ORDER BY sequence ASC
	`, workspaceID, cardID, attemptID, actorSourceID)
	if err != nil {
		return uuid.Nil, "", false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var body string
		var raw []byte
		if err := rows.Scan(&id, &body, &raw); err != nil {
			return uuid.Nil, "", false, err
		}
		if extractPreviewID(raw) == previewID {
			return id, body, true, nil
		}
	}
	return uuid.Nil, "", false, rows.Err()
}

// deleteReplyPreviewEventsTx 删除匹配 preview_id 的未转正草稿气泡（可选排除 excludeID）。
func deleteReplyPreviewEventsTx(
	ctx context.Context, tx *sqldb.Tx,
	workspaceID, cardID, attemptID, actorSourceID uuid.UUID,
	previewID string, excludeID uuid.UUID,
) (int64, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, message
		FROM events
		WHERE workspace_id=$1 AND card_id=$2 AND attempt_id=$3
		  AND actor_source_id=$4 AND event_type='message' AND idempotency_key IS NULL
	`, workspaceID, cardID, attemptID, actorSourceID)
	if err != nil {
		return 0, err
	}
	var toDelete []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		if excludeID != uuid.Nil && id == excludeID {
			continue
		}
		if extractPreviewID(raw) == previewID {
			toDelete = append(toDelete, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(toDelete) == 0 {
		return 0, nil
	}
	args := []any{workspaceID}
	inClause := sqldb.AppendInPlaceholders(&args, toDelete)
	res, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM events WHERE workspace_id=$1 AND id IN (%s)`, inClause), args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}

func extractPreviewID(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var meta struct {
		PreviewID string `json:"preview_id"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ""
	}
	return meta.PreviewID
}
