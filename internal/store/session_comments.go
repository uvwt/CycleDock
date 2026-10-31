package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
)

// DeviceTarget 是用户的执行偏好，不授予设备权限；实际调用始终由 MCP 校验授权。
type DeviceTarget struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
}

type SubmitSessionCommentParams struct {
	DeviceTarget   *DeviceTarget
	WorkspaceID    uuid.UUID
	CardID         uuid.UUID
	AuthorSourceID uuid.UUID
	Content        string
	IdempotencyKey string
	DispatchMode   domain.DispatchMode
	// 回复绑定正在回答的执行；选择按钮还须绑定具体消息，防止旧页面误答。
	ReplyToAttemptID uuid.UUID
	ChoiceMessageID  uuid.UUID
}

type SubmitSessionCommentResult struct {
	EventID uuid.UUID
	TurnID  uuid.UUID
	Reused  bool
}

func (s *Store) SubmitSessionComment(
	ctx context.Context,
	p SubmitSessionCommentParams,
) (SubmitSessionCommentResult, error) {
	return s.submitSessionComment(ctx, p, false)
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
	requireWaitingHuman bool,
) (SubmitSessionCommentResult, error) {
	p.Content = strings.TrimSpace(p.Content)
	p.IdempotencyKey = strings.TrimSpace(p.IdempotencyKey)
	if p.Content == "" {
		return SubmitSessionCommentResult{}, fmt.Errorf("%w: comment content is required", domain.ErrConflict)
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
		deviceNodeID = strings.TrimSpace(p.DeviceTarget.NodeID)
		deviceName = strings.TrimSpace(p.DeviceTarget.Name)
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

	if deviceNodeID != "" {
		requestHash = stableRequestHash(requestHash, "device_target", deviceNodeID, deviceName)
	}

	if requireWaitingHuman {
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

	if requireWaitingHuman {
		var status string
		err := tx.QueryRow(ctx, `
			SELECT status FROM turn_attempts
			WHERE workspace_id=$1 AND card_id=$2 AND id=$3
			FOR UPDATE
		`, p.WorkspaceID, p.CardID, p.ReplyToAttemptID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return SubmitSessionCommentResult{}, domain.ErrAttemptNotCurrent
		}
		if err != nil {
			return SubmitSessionCommentResult{}, err
		}
		if status != string(domain.AttemptStatusWaitingHuman) {
			return SubmitSessionCommentResult{}, domain.ErrAttemptNotCurrent
		}
		if p.ChoiceMessageID != uuid.Nil {
			// 与创建下一轮共用 Card 行锁，旧页面的选择不能回答新的等待问题。
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

	eventID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id,
			content, idempotency_key, request_hash
		)
		VALUES ($1,$2,$3,'comment',$4,$5,$6,$7)
	`, eventID, p.WorkspaceID, p.CardID, p.AuthorSourceID,
		p.Content, p.IdempotencyKey, requestHash)
	if err != nil {
		return SubmitSessionCommentResult{}, mapConstraintError(err)
	}

	turn, err := insertTurn(ctx, tx, insertTurnParams{
		DeviceNodeID: deviceNodeID, DeviceName: deviceName,
		WorkspaceID:       p.WorkspaceID,
		CardID:            p.CardID,
		Source:            domain.TurnSourceHuman,
		Content:           p.Content,
		CreatedBySourceID: uuid.NullUUID{UUID: p.AuthorSourceID, Valid: true},
		SourceEventID:     uuid.NullUUID{UUID: eventID, Valid: true},
		DispatchMode:      p.DispatchMode,
		IdempotencyKey:    p.IdempotencyKey,
		RequestHash:       requestHash,
	})
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if err := supersedeAttemptForHumanTurn(
		ctx, tx, p.WorkspaceID, p.CardID, p.DispatchMode, turn.CreatedAt,
	); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if err := relayVisibleMessage(ctx, tx, p.WorkspaceID, p.CardID, eventID, p.Content); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	s.notifyWorkReady()
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
