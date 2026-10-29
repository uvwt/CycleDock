package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
)

type SessionAttemptView struct {
	ID               uuid.UUID
	TurnID           uuid.UUID
	SourceEventID    uuid.NullUUID
	Sequence         int64
	Source           domain.TurnSource
	Content          string
	AttemptNumber    int
	Reason           domain.AttemptReason
	RetryOfAttemptID uuid.NullUUID
	Status           domain.AttemptStatus
	CanRetry         bool
	CurrentStage     string
	ProgressSummary  string
	ErrorSummary     string
	CreatedAt        time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
}

type PendingTurnView struct {
	ID            uuid.UUID
	SourceEventID uuid.NullUUID
	Sequence      int64
	Source        domain.TurnSource
	DispatchMode  domain.DispatchMode
	Content       string
	CreatedAt     time.Time
}

func (s *Store) GetAttemptView(
	ctx context.Context,
	workspaceID, attemptID uuid.UUID,
) (SessionAttemptView, error) {
	var out SessionAttemptView
	var startedAt, finishedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT
			a.id, a.turn_id, t.source_event_id, t.sequence, t.source, t.content,
			a.attempt_number, a.reason, a.retry_of_attempt_id, a.status,
			(
				a.status IN ('failed','expired','cancelled')
				AND NOT EXISTS (
					SELECT 1
					FROM turn_attempts successor
					WHERE successor.workspace_id=a.workspace_id
					  AND successor.retry_of_attempt_id=a.id
				)
			),
			a.current_stage, a.progress_summary,
			a.error_summary, a.created_at, a.started_at, a.finished_at
		FROM turn_attempts a
		JOIN card_turns t
		  ON t.workspace_id=a.workspace_id AND t.card_id=a.card_id AND t.id=a.turn_id
		WHERE a.workspace_id=$1 AND a.id=$2
	`, workspaceID, attemptID).Scan(
		&out.ID, &out.TurnID, &out.SourceEventID, &out.Sequence, &out.Source, &out.Content,
		&out.AttemptNumber, &out.Reason, &out.RetryOfAttemptID, &out.Status, &out.CanRetry,
		&out.CurrentStage, &out.ProgressSummary,
		&out.ErrorSummary, &out.CreatedAt, &startedAt, &finishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionAttemptView{}, domain.ErrNotFound
	}
	if err != nil {
		return SessionAttemptView{}, err
	}
	out.StartedAt = startedAt
	out.FinishedAt = finishedAt
	return out, nil
}

func (s *Store) CurrentAttemptView(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
) (*SessionAttemptView, error) {
	var out SessionAttemptView
	var startedAt, finishedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT
			a.id, a.turn_id, t.source_event_id, t.sequence, t.source, t.content,
			a.attempt_number, a.reason, a.retry_of_attempt_id, a.status,
			false,
			a.current_stage, a.progress_summary,
			a.error_summary, a.created_at, a.started_at, a.finished_at
		FROM turn_attempts a
		JOIN card_turns t
		  ON t.workspace_id=a.workspace_id AND t.card_id=a.card_id AND t.id=a.turn_id
		WHERE a.workspace_id=$1 AND a.card_id=$2
		  AND a.status IN ('queued','running','waiting_human')
		ORDER BY a.created_at DESC
		LIMIT 1
	`, workspaceID, cardID).Scan(
		&out.ID, &out.TurnID, &out.SourceEventID, &out.Sequence, &out.Source, &out.Content,
		&out.AttemptNumber, &out.Reason, &out.RetryOfAttemptID, &out.Status, &out.CanRetry,
		&out.CurrentStage, &out.ProgressSummary,
		&out.ErrorSummary, &out.CreatedAt, &startedAt, &finishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out.StartedAt = startedAt
	out.FinishedAt = finishedAt
	return &out, nil
}

func (s *Store) ListPendingTurns(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
) ([]PendingTurnView, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.source_event_id, t.sequence, t.source, t.dispatch_mode, t.content, t.created_at
		FROM card_turns t
		WHERE t.workspace_id=$1 AND t.card_id=$2
		  AND t.cancelled_at IS NULL
		  AND NOT EXISTS (
			SELECT 1
			FROM turn_attempts a
			WHERE a.workspace_id=t.workspace_id AND a.turn_id=t.id
		  )
		ORDER BY
		  CASE WHEN t.dispatch_mode='interrupt' THEN 0 ELSE 1 END,
		  t.sequence
	`, workspaceID, cardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]PendingTurnView, 0)
	for rows.Next() {
		var item PendingTurnView
		if err := rows.Scan(
			&item.ID, &item.SourceEventID, &item.Sequence, &item.Source, &item.DispatchMode,
			&item.Content, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListRecentAttempts(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	limit int,
) ([]SessionAttemptView, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
			a.id, a.turn_id, t.source_event_id, t.sequence, t.source, t.content,
			a.attempt_number, a.reason, a.retry_of_attempt_id, a.status,
			(
				a.status IN ('failed','expired','cancelled')
				AND NOT EXISTS (
					SELECT 1
					FROM turn_attempts successor
					WHERE successor.workspace_id=a.workspace_id
					  AND successor.retry_of_attempt_id=a.id
				)
			),
			a.current_stage, a.progress_summary,
			a.error_summary, a.created_at, a.started_at, a.finished_at
		FROM turn_attempts a
		JOIN card_turns t
		  ON t.workspace_id=a.workspace_id AND t.card_id=a.card_id AND t.id=a.turn_id
		WHERE a.workspace_id=$1 AND a.card_id=$2
		  AND a.status NOT IN ('queued','running','waiting_human')
		ORDER BY COALESCE(a.finished_at, a.created_at) DESC, a.created_at DESC
		LIMIT $3
	`, workspaceID, cardID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]SessionAttemptView, 0)
	for rows.Next() {
		var item SessionAttemptView
		var startedAt, finishedAt *time.Time
		if err := rows.Scan(
			&item.ID, &item.TurnID, &item.SourceEventID, &item.Sequence, &item.Source, &item.Content,
			&item.AttemptNumber, &item.Reason, &item.RetryOfAttemptID, &item.Status, &item.CanRetry,
			&item.CurrentStage,
			&item.ProgressSummary, &item.ErrorSummary,
			&item.CreatedAt, &startedAt, &finishedAt,
		); err != nil {
			return nil, err
		}
		item.StartedAt = startedAt
		item.FinishedAt = finishedAt
		out = append(out, item)
	}
	return out, rows.Err()
}
