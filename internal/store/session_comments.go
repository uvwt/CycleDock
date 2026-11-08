package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
)

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

	var cardKind, discussionState, todoState, ongoingState string
	if err := tx.QueryRow(ctx, `
		SELECT kind, COALESCE(discussion_state, ''), COALESCE(todo_state, ''), COALESCE(ongoing_state, '')
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, p.WorkspaceID, p.CardID).Scan(&cardKind, &discussionState, &todoState, &ongoingState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SubmitSessionCommentResult{}, domain.ErrNotFound
		}
		return SubmitSessionCommentResult{}, err
	}
	switch cardKind {
	case string(domain.CardKindDiscussion):
		if discussionState != string(domain.DiscussionStateActive) {
			return SubmitSessionCommentResult{}, fmt.Errorf("%w: discussion is archived", domain.ErrInvalidTransition)
		}
	case string(domain.CardKindTodo):
		if todoState == string(domain.TodoStateDone) {
			return SubmitSessionCommentResult{}, fmt.Errorf("%w: done todo card cannot rerun turns", domain.ErrInvalidTransition)
		}
	case string(domain.CardKindOngoing):
		if ongoingState != string(domain.OngoingStateActive) {
			return SubmitSessionCommentResult{}, fmt.Errorf("%w: ongoing card is not active", domain.ErrInvalidTransition)
		}
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
	`, p.WorkspaceID, p.CardID, turnSequence)
	if err != nil {
		return SubmitSessionCommentResult{}, err
	}
	attemptIDs := make([]uuid.UUID, 0)
	invocationIDs := make([]string, 0)
	for rows.Next() {
		var attemptID uuid.UUID
		var invocationID string
		if err := rows.Scan(&attemptID, &invocationID); err != nil {
			rows.Close()
			return SubmitSessionCommentResult{}, err
		}
		attemptIDs = append(attemptIDs, attemptID)
		if strings.TrimSpace(invocationID) != "" {
			invocationIDs = append(invocationIDs, invocationID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SubmitSessionCommentResult{}, err
	}
	rows.Close()

	// 先删除关联的业务 Events，避免先删 turn_attempts 触发 ON DELETE SET NULL (attempt_id)
	// 从而违反 events_reply_preview_check（带 preview_id 的消息要求 attempt_id IS NOT NULL）。
	if _, err := tx.Exec(ctx, `
		DELETE FROM events e
		WHERE e.workspace_id = $1
		  AND e.card_id = $2
		  AND e.id <> $3
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
	`, p.WorkspaceID, p.CardID, p.EventID, attemptIDs, eventSequence, turnSequence); err != nil {
		return SubmitSessionCommentResult{}, err
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
		`, p.WorkspaceID.String(), p.CardID.String(), invocationIDs); err != nil {
			return SubmitSessionCommentResult{}, err
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
		`, p.WorkspaceID, p.CardID, attemptIDs); err != nil {
			return SubmitSessionCommentResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM turn_attempts
			WHERE workspace_id = $1
			  AND card_id = $2
			  AND id = ANY($3::uuid[])
		`, p.WorkspaceID, p.CardID, attemptIDs); err != nil {
			return SubmitSessionCommentResult{}, err
		}
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM card_turns
		WHERE workspace_id = $1
		  AND card_id = $2
		  AND sequence > $3
	`, p.WorkspaceID, p.CardID, turnSequence); err != nil {
		return SubmitSessionCommentResult{}, err
	}

	requestHash := stableRequestHash(
		"session_comment",
		p.WorkspaceID.String(),
		p.CardID.String(),
		p.AuthorSourceID.String(),
		string(domain.DispatchModeEnqueue),
		p.Content,
	)
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
		`, p.WorkspaceID, p.CardID); err != nil {
			return SubmitSessionCommentResult{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return SubmitSessionCommentResult{}, err
	}
	s.notifyWorkReady()
	return SubmitSessionCommentResult{EventID: p.EventID, TurnID: turnID}, nil
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

	if deviceNodeID != "" {
		requestHash = stableRequestHash(requestHash, "device_target", deviceNodeID, deviceName)
	}

	if p.PreferredModelRouteID != nil && *p.PreferredModelRouteID != uuid.Nil {
		requestHash = stableRequestHash(requestHash, "preferred_model_route", p.PreferredModelRouteID.String())
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

	var preferredRouteID uuid.NullUUID
	if p.PreferredModelRouteID != nil && *p.PreferredModelRouteID != uuid.Nil {
		preferredRouteID = uuid.NullUUID{UUID: *p.PreferredModelRouteID, Valid: true}
	}

	turn, err := insertTurn(ctx, tx, insertTurnParams{
		DeviceNodeID: deviceNodeID, DeviceName: deviceName,
		PreferredModelRouteID: preferredRouteID,
		WorkspaceID:           p.WorkspaceID,
		CardID:                p.CardID,
		Source:                domain.TurnSourceHuman,
		Content:               p.Content,
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
