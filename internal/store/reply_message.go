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
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
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

type PublishReplyParams struct {
	WorkspaceID, CardID, AttemptID, RunnerSourceID uuid.UUID
	Message                                        ReplyMessage
	IdempotencyKey                                 string
	// 外部 MCP 没有 ADK Runner 善后，最后一条消息同时结算 Attempt。
	FinishOnEnd bool
}

func validReplyURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" &&
		u.User == nil && u.Fragment == "" && !strings.ContainsAny(raw, "\r\n")
}

func (m *ReplyMessage) Validate() error {
	m.Type = strings.TrimSpace(m.Type)
	m.Content = strings.TrimSpace(m.Content)
	m.URL = strings.TrimSpace(m.URL)
	m.Alt = strings.TrimSpace(m.Alt)
	if len(m.Content) > 20000 || len(m.Alt) > 500 || len(m.Images) > 8 {
		return fmt.Errorf("%w: reply exceeds size limit", domain.ErrConflict)
	}
	for i := range m.Images {
		m.Images[i] = strings.TrimSpace(m.Images[i])
		if len(m.Images[i]) > 2048 || !validReplyURL(m.Images[i]) {
			return fmt.Errorf("%w: invalid image URL", domain.ErrConflict)
		}
	}
	switch m.Type {
	case "text":
		if m.Content == "" || m.URL != "" || m.Widget != nil {
			return fmt.Errorf("%w: text reply requires content", domain.ErrConflict)
		}
	case "attachment":
		if len(m.URL) > 2048 || !validReplyURL(m.URL) || m.Widget != nil {
			return fmt.Errorf("%w: attachment requires HTTPS URL", domain.ErrConflict)
		}
	case "choice":
		if !m.EndTurn || m.Widget == nil || m.URL != "" || len(m.Images) != 0 {
			return fmt.Errorf("%w: choice requires widget and end_turn", domain.ErrConflict)
		}
		m.Widget.Question = strings.TrimSpace(m.Widget.Question)
		if m.Widget.Question == "" || len(m.Widget.Question) > 1000 ||
			len(m.Widget.Options) < 2 || len(m.Widget.Options) > 8 {
			return fmt.Errorf("%w: invalid choice question or options", domain.ErrConflict)
		}
		values := map[string]bool{}
		for i := range m.Widget.Options {
			o := &m.Widget.Options[i]
			o.Value = strings.TrimSpace(o.Value)
			o.Label = strings.TrimSpace(o.Label)
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
		return fmt.Errorf("%w: unexpected widget", domain.ErrConflict)
	}
	return nil
}

// PublishReply 与 Attempt 状态变化共用 Card 行锁；一次调用只追加一条 Message。
// 选择题在同一事务中进入 waiting_human，避免页面可点而 Attempt 尚未等待。
func (s *Store) PublishReply(ctx context.Context, p PublishReplyParams) (uuid.UUID, bool, error) {
	if err := p.Message.Validate(); err != nil {
		return uuid.Nil, false, err
	}
	p.IdempotencyKey = strings.TrimSpace(p.IdempotencyKey)
	if p.IdempotencyKey == "" || len(p.IdempotencyKey) > 200 {
		return uuid.Nil, false, fmt.Errorf("%w: idempotency key required", domain.ErrConflict)
	}
	payload, err := json.Marshal(p.Message)
	if err != nil {
		return uuid.Nil, false, err
	}
	hash := stableRequestHash("reply_message", p.WorkspaceID.String(), p.CardID.String(),
		p.AttemptID.String(), p.RunnerSourceID.String(), string(payload))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return uuid.Nil, false, err
	}
	// 幂等读取放在状态检查之前：成功发送后 end_turn 可能已关闭 Attempt。
	var oldID, oldCard, oldAttempt uuid.UUID
	var oldHash string
	err = tx.QueryRow(ctx, `SELECT id,card_id,attempt_id,COALESCE(request_hash,'')
		FROM events WHERE workspace_id=$1 AND actor_source_id=$2
		AND event_type='message' AND idempotency_key=$3`,
		p.WorkspaceID, p.RunnerSourceID, p.IdempotencyKey).
		Scan(&oldID, &oldCard, &oldAttempt, &oldHash)
	if err == nil {
		if oldCard != p.CardID || oldAttempt != p.AttemptID || oldHash != hash {
			return uuid.Nil, false, domain.ErrIdempotencyMismatch
		}
		return oldID, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}
	if err := requireCurrentAttempt(ctx, tx, p.WorkspaceID, p.CardID,
		p.AttemptID, p.RunnerSourceID, domain.AttemptStatusRunning); err != nil {
		return uuid.Nil, false, err
	}
	id := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO events
		(id,workspace_id,card_id,event_type,actor_source_id,attempt_id,content,message,idempotency_key,request_hash)
		VALUES ($1,$2,$3,'message',$4,$5,$6,$7,$8,$9)`,
		id, p.WorkspaceID, p.CardID, p.RunnerSourceID, p.AttemptID,
		p.Message.Content, payload, p.IdempotencyKey, hash)
	if err != nil {
		return uuid.Nil, false, mapConstraintError(err)
	}
	if p.Message.Type == "choice" {
		command, err := tx.Exec(ctx, `UPDATE turn_attempts
			SET status='waiting_human', current_stage='等待用户选择', progress_summary=$5,
			heartbeat_at=NULL,lease_expires_at=NULL,updated_at=$6
			WHERE workspace_id=$1 AND card_id=$2 AND id=$3
				AND runner_source_id=$4 AND status='running'`,
			p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID,
			p.Message.Widget.Question, time.Now().UTC())
		if err != nil {
			return uuid.Nil, false, err
		}
		if command.RowsAffected() != 1 {
			return uuid.Nil, false, domain.ErrAttemptNotCurrent
		}
	} else if p.Message.EndTurn && p.FinishOnEnd {
		now := time.Now().UTC()
		command, err := tx.Exec(ctx, `UPDATE turn_attempts
			SET status='completed', result_summary=$5, heartbeat_at=NULL,
				lease_expires_at=NULL, finished_at=$6, updated_at=$6
			WHERE workspace_id=$1 AND card_id=$2 AND id=$3
				AND runner_source_id=$4 AND status='running'`,
			p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID, p.Message.Content, now)
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
	return id, false, nil
}
