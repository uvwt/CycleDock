package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func (s *Store) MoveTodo(
	ctx context.Context,
	workspaceID, cardID, actorSourceID uuid.UUID,
	to domain.TodoState,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	if err := moveTodoTx(ctx, tx, workspaceID, cardID, actorSourceID, to); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func moveTodoTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID, actorSourceID uuid.UUID,
	to domain.TodoState,
) error {
	var kind string
	var from string
	if err := tx.QueryRow(ctx, `
		SELECT kind, todo_state
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID).Scan(&kind, &from); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return err
	}
	if kind != string(domain.CardKindTodo) {
		return fmt.Errorf("%w: todo card required", domain.ErrInvalidTransition)
	}

	var actorKind string
	if err := tx.QueryRow(ctx, `
		SELECT kind FROM sources WHERE workspace_id = $1 AND id = $2
	`, workspaceID, actorSourceID).Scan(&actorKind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return err
	}
	if err := domain.CanMoveTodo(domain.TodoState(from), to, domain.SourceKind(actorKind)); err != nil {
		return err
	}
	if from == string(to) {
		return nil
	}

	// 每次真正进入 Working 开启新确认轮次，旧确认仅作为历史证据保留。
	if _, err := tx.Exec(ctx, `
		UPDATE cards
		SET todo_state = $3,
		    confirmation_round = confirmation_round + CASE WHEN $3 = 'working' THEN 1 ELSE 0 END,
		    updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID, string(to)); err != nil {
		return err
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id, content
		)
		VALUES ($1, $2, $3, 'status_change', $4, $5)
	`, uuid.New(), workspaceID, cardID, actorSourceID, fmt.Sprintf("%s -> %s", from, to))
	return err
}

func (s *Store) ArchiveDiscussion(
	ctx context.Context,
	workspaceID, cardID, actorSourceID uuid.UUID,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	if err := requireHumanSource(ctx, tx, workspaceID, actorSourceID); err != nil {
		return err
	}
	var kind, state string
	if err := tx.QueryRow(ctx, `
		SELECT kind, COALESCE(discussion_state, '')
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID).Scan(&kind, &state); err != nil {
		return err
	}
	if kind != string(domain.CardKindDiscussion) {
		return fmt.Errorf("%w: discussion card required", domain.ErrInvalidTransition)
	}
	if err := rejectMainDiscussionMutation(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	if state == string(domain.DiscussionStateArchived) {
		return tx.Commit(ctx)
	}
	if state != string(domain.DiscussionStateActive) {
		return domain.ErrInvalidTransition
	}
	if _, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status='cancelled',
		    error_summary='discussion archived',
		    heartbeat_at=NULL,
		    lease_expires_at=NULL,
		    finished_at=now(),
		    updated_at=now()
		WHERE workspace_id=$1 AND card_id=$2
		  AND status IN ('queued','running')
	`, workspaceID, cardID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE card_turns pending
		SET cancelled_at=now(),
		    cancelled_by_source_id=$3,
		    cancel_reason='discussion archived'
		WHERE pending.workspace_id=$1
		  AND pending.card_id=$2
		  AND pending.cancelled_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM turn_attempts existing
			WHERE existing.workspace_id=pending.workspace_id
			  AND existing.turn_id=pending.id
		  )
	`, workspaceID, cardID, actorSourceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE cards
		SET discussion_state = 'archived', updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (id, workspace_id, card_id, event_type, actor_source_id, content)
		VALUES ($1, $2, $3, 'status_change', $4, 'active -> archived')
	`, uuid.New(), workspaceID, cardID, actorSourceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ensureDiscussionAcceptsWork(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID uuid.UUID,
) error {
	var kind, discussionState string
	if err := tx.QueryRow(ctx, `
		SELECT kind, COALESCE(discussion_state, '')
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID).Scan(&kind, &discussionState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return err
	}
	if kind == string(domain.CardKindDiscussion) &&
		discussionState != string(domain.DiscussionStateActive) {
		return fmt.Errorf("%w: discussion is archived", domain.ErrInvalidTransition)
	}
	return nil
}

func (s *Store) DeleteCard(
	ctx context.Context,
	workspaceID, cardID, actorSourceID uuid.UUID,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	if err := requireHumanSource(ctx, tx, workspaceID, actorSourceID); err != nil {
		return err
	}
	if err := rejectMainDiscussionMutation(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		DELETE FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("%w: card not found", domain.ErrNotFound)
	}
	// Card ID 即 ADK Session ID，删除卡片时同步清理对应的持久 Session、Event 与 Artifact。
	if _, err := tx.Exec(ctx, `
		DELETE FROM agent_sessions
		WHERE user_id = $1 AND id = $2
	`, workspaceID.String(), cardID.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteDiscussion(
	ctx context.Context,
	workspaceID, cardID, actorSourceID uuid.UUID,
) error {
	return s.DeleteCard(ctx, workspaceID, cardID, actorSourceID)
}

// SetCardPreferredModelRoute 为卡片（持久 Agent Session）绑定或清除优先模型路由。
// 传入 Valid=false 的 routeID 表示恢复为跟随工作区与卡片类型默认模型。
func (s *Store) SetCardPreferredModelRoute(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	preferredModelRouteID uuid.NullUUID,
) (db.Card, error) {
	if preferredModelRouteID.Valid && preferredModelRouteID.UUID != uuid.Nil {
		var exists bool
		err := s.pool.QueryRow(ctx, `
			SELECT true FROM workspace_model_routes
			WHERE workspace_id = $1 AND id = $2 AND enabled = true
		`, workspaceID, preferredModelRouteID.UUID).Scan(&exists)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return db.Card{}, fmt.Errorf("%w: model route not found or disabled", domain.ErrNotFound)
			}
			return db.Card{}, err
		}
	}

	card, err := s.queries.SetCardPreferredModelRoute(ctx, db.SetCardPreferredModelRouteParams{
		WorkspaceID:           workspaceID,
		ID:                    cardID,
		PreferredModelRouteID: preferredModelRouteID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Card{}, domain.ErrNotFound
	}
	return card, err
}

func (s *Store) SetOngoingState(
	ctx context.Context,
	workspaceID, cardID, actorSourceID uuid.UUID,
	to domain.OngoingState,
	now time.Time,
) error {
	if to != domain.OngoingStateActive && to != domain.OngoingStatePaused && to != domain.OngoingStateArchived {
		return fmt.Errorf("%w: invalid ongoing state %q", domain.ErrInvalidTransition, to)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	var kind, from string
	if err := tx.QueryRow(ctx, `
		SELECT kind, ongoing_state
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID).Scan(&kind, &from); err != nil {
		return err
	}
	if kind != string(domain.CardKindOngoing) {
		return fmt.Errorf("%w: ongoing card required", domain.ErrInvalidTransition)
	}
	if from == string(to) {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE cards SET ongoing_state = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID, string(to)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (id, workspace_id, card_id, event_type, actor_source_id, content)
		VALUES ($1, $2, $3, 'status_change', $4, $5)
	`, uuid.New(), workspaceID, cardID, actorSourceID, fmt.Sprintf("%s -> %s", from, to)); err != nil {
		return err
	}

	// Paused/Archived 期间不补跑。重新 Active 时从“现在”重新计算所有 enabled schedule 的未来 occurrence。
	if to == domain.OngoingStateActive {
		if err := recalculateCardSchedulesTx(ctx, tx, workspaceID, cardID, now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) Health(ctx context.Context, workspaceID, cardID uuid.UUID) (domain.CheckinStatus, error) {
	event, err := s.queries.LatestCheckin(ctx, db.LatestCheckinParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CheckinUnknown, nil
	}
	if err != nil {
		return "", err
	}
	if !event.CheckinStatus.Valid {
		return domain.CheckinUnknown, nil
	}
	status := domain.CheckinStatus(event.CheckinStatus.String)
	if status == domain.CheckinWarning || status == domain.CheckinCritical {
		var acked bool
		err := s.pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM events
				WHERE workspace_id = $1
				  AND card_id = $2
				  AND event_type = 'checkin_ack'
				  AND sequence > $3
			)
		`, workspaceID, cardID, event.Sequence).Scan(&acked)
		if err != nil {
			return "", err
		}
		if acked {
			return domain.CheckinAcknowledged, nil
		}
	}
	return status, nil
}

func (s *Store) AcknowledgeCheckin(
	ctx context.Context,
	workspaceID, cardID, actorSourceID uuid.UUID,
	content string,
) error {
	content = strings.TrimSpace(content)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	if err := requireHumanSource(ctx, tx, workspaceID, actorSourceID); err != nil {
		return err
	}

	var kind string
	if err := tx.QueryRow(ctx, `
		SELECT kind
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID).Scan(&kind); err != nil {
		return err
	}
	if domain.CardKind(kind) != domain.CardKindOngoing {
		return domain.ErrInvalidTransition
	}

	var latestSeq int64
	var latestStatus pgtype.Text
	err = tx.QueryRow(ctx, `
		SELECT sequence, checkin_status
		FROM events
		WHERE workspace_id = $1
		  AND card_id = $2
		  AND event_type = 'checkin'
		ORDER BY sequence DESC
		LIMIT 1
	`, workspaceID, cardID).Scan(&latestSeq, &latestStatus)
	if errors.Is(err, pgx.ErrNoRows) || !latestStatus.Valid {
		return domain.ErrInvalidTransition
	}
	if err != nil {
		return err
	}

	status := domain.CheckinStatus(latestStatus.String)
	if status != domain.CheckinWarning && status != domain.CheckinCritical {
		return domain.ErrInvalidTransition
	}

	var alreadyAcked bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM events
			WHERE workspace_id = $1
			  AND card_id = $2
			  AND event_type = 'checkin_ack'
			  AND sequence > $3
		)
	`, workspaceID, cardID, latestSeq).Scan(&alreadyAcked); err != nil {
		return err
	}
	if alreadyAcked {
		return domain.ErrInvalidTransition
	}

	if content == "" {
		content = fmt.Sprintf("%s -> %s", status, domain.CheckinAcknowledged)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE cards
		SET updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO events (id, workspace_id, card_id, event_type, actor_source_id, content)
		VALUES ($1, $2, $3, 'checkin_ack', $4, $5)
	`, uuid.New(), workspaceID, cardID, actorSourceID, content); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
