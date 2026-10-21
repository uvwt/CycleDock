package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	if err := moveTodoTx(ctx, tx, workspaceID, cardID, actorSourceID, to, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func moveTodoTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID, actorSourceID uuid.UUID,
	to domain.TodoState,
	runID *uuid.UUID,
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

	if _, err := tx.Exec(ctx, `
		UPDATE cards
		SET todo_state = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID, string(to)); err != nil {
		return err
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id, run_id, content
		)
		VALUES ($1, $2, $3, 'status_change', $4, $5, $6)
	`, uuid.New(), workspaceID, cardID, actorSourceID, runID, fmt.Sprintf("%s -> %s", from, to))
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
	if state == string(domain.DiscussionStateArchived) {
		return tx.Commit(ctx)
	}
	if state != string(domain.DiscussionStateActive) {
		return domain.ErrInvalidTransition
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'cancelled',
			finished_at = now(),
			heartbeat_at = NULL,
			lease_expires_at = NULL,
			cancelled_by_source_id = $3,
			cancel_reason = 'discussion archived',
			updated_at = now()
		WHERE workspace_id = $1
		  AND card_id = $2
		  AND status IN ('queued', 'running', 'waiting_human')
	`, workspaceID, cardID, actorSourceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE triggers pending
		SET cancelled_at = now(),
			cancelled_by_source_id = $3,
			cancel_reason = 'discussion archived'
		WHERE pending.workspace_id = $1
		  AND pending.card_id = $2
		  AND pending.cancelled_at IS NULL
		  AND NOT EXISTS (
			  SELECT 1
			  FROM agent_runs existing
			  WHERE existing.workspace_id = pending.workspace_id
			    AND existing.trigger_id = pending.id
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

func (s *Store) DeleteDiscussion(
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
	command, err := tx.Exec(ctx, `
		DELETE FROM cards
		WHERE workspace_id = $1 AND id = $2 AND kind = 'discussion'
	`, workspaceID, cardID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("%w: discussion card required", domain.ErrInvalidTransition)
	}
	return tx.Commit(ctx)
}

// SetDiscussionMetadataForRun writes the generated title/description only while the
// same Run still owns an active Discussion. A superseded Run must never rename it.
func (s *Store) SetDiscussionMetadataForRun(
	ctx context.Context,
	workspaceID, runID, runnerSourceID uuid.UUID,
	title, description string,
) error {
	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)
	if title == "" {
		return fmt.Errorf("%w: discussion title is required", domain.ErrConflict)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cardID, status, actualRunnerSourceID, err := lockRunCard(ctx, tx, workspaceID, runID)
	if err != nil {
		return err
	}
	if actualRunnerSourceID != runnerSourceID || status != domain.RunStatusRunning {
		return domain.ErrRunNotCurrent
	}
	var kind, state, existingTitle string
	if err := tx.QueryRow(ctx, `
		SELECT kind, COALESCE(discussion_state, ''), title
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID).Scan(&kind, &state, &existingTitle); err != nil {
		return err
	}
	if kind != string(domain.CardKindDiscussion) || state != string(domain.DiscussionStateActive) {
		return fmt.Errorf("%w: active discussion required", domain.ErrInvalidTransition)
	}
	if strings.TrimSpace(existingTitle) != "" {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE cards
		SET title = $3, description = $4, updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID, title, description); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
	return domain.CheckinStatus(event.CheckinStatus.String), nil
}
