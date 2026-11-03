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
	DeviceNodeID     string
	DeviceName       string
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
	DeviceNodeID  string
	DeviceName    string
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
			a.id, a.turn_id, t.source_event_id, t.sequence, t.source, t.content, t.device_node_id, t.device_name,
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
		&out.ID, &out.TurnID, &out.SourceEventID, &out.Sequence, &out.Source, &out.Content, &out.DeviceNodeID, &out.DeviceName,
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
			a.id, a.turn_id, t.source_event_id, t.sequence, t.source, t.content, t.device_node_id, t.device_name,
			a.attempt_number, a.reason, a.retry_of_attempt_id, a.status,
			false,
			a.current_stage, a.progress_summary,
			a.error_summary, a.created_at, a.started_at, a.finished_at
		FROM turn_attempts a
		JOIN card_turns t
		  ON t.workspace_id=a.workspace_id AND t.card_id=a.card_id AND t.id=a.turn_id
		WHERE a.workspace_id=$1 AND a.card_id=$2
		  AND a.status IN ('queued','running')
		  AND COALESCE(t.idempotency_key, '') NOT LIKE 'builtin-proactive:%'
		ORDER BY a.created_at DESC
		LIMIT 1
	`, workspaceID, cardID).Scan(
		&out.ID, &out.TurnID, &out.SourceEventID, &out.Sequence, &out.Source, &out.Content, &out.DeviceNodeID, &out.DeviceName,
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
		SELECT t.id, t.source_event_id, t.sequence, t.source, t.dispatch_mode, t.content, t.device_node_id, t.device_name, t.created_at
		FROM card_turns t
		WHERE t.workspace_id=$1 AND t.card_id=$2
		  AND t.cancelled_at IS NULL
		  AND COALESCE(t.idempotency_key, '') NOT LIKE 'builtin-proactive:%'
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
			&item.Content, &item.DeviceNodeID, &item.DeviceName, &item.CreatedAt,
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
	return s.listAttempts(ctx, workspaceID, cardID, limit, 0, 1<<62-1)
}

// ListTimelineAttempts 与消息共用分页范围，避免把未加载消息的执行误列为孤立历史。
func (s *Store) ListTimelineAttempts(
	ctx context.Context, workspaceID, cardID uuid.UUID, oldestSequence, beforeSequence int64,
) ([]SessionAttemptView, error) {
	return s.listAttempts(ctx, workspaceID, cardID, 0, oldestSequence, beforeSequence)
}

func (s *Store) listAttempts(
	ctx context.Context, workspaceID, cardID uuid.UUID, limit int, oldestSequence, beforeSequence int64,
) ([]SessionAttemptView, error) {
	if limit < 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
			a.id, a.turn_id, t.source_event_id, t.sequence, t.source, t.content, t.device_node_id, t.device_name,
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
		  AND a.status NOT IN ('queued','running')
		  AND (
			EXISTS (
				SELECT 1 FROM events e
				WHERE e.workspace_id=a.workspace_id AND e.card_id=a.card_id
				  AND (e.attempt_id=a.id OR e.id=t.source_event_id)
				  AND e.sequence >= $4 AND e.sequence < $5
			)
			OR (
				t.source_event_id IS NULL
				AND NOT EXISTS (SELECT 1 FROM events e WHERE e.workspace_id=a.workspace_id AND e.attempt_id=a.id)
				AND a.created_at >= COALESCE((SELECT created_at FROM events WHERE workspace_id=$1 AND card_id=$2 AND sequence=$4), '-infinity'::timestamptz)
				AND a.created_at < COALESCE((SELECT created_at FROM events WHERE workspace_id=$1 AND card_id=$2 AND sequence=$5), 'infinity'::timestamptz)
			)
		  )
		  AND (
			COALESCE(t.idempotency_key, '') NOT LIKE 'builtin-proactive:%'
			OR (
				a.status = 'completed'
				AND EXISTS (
					SELECT 1
					FROM events e
					WHERE e.workspace_id=a.workspace_id
					  AND e.card_id=a.card_id
					  AND e.attempt_id=a.id
					  AND e.event_type='message'
				)
			)
		  )
		ORDER BY COALESCE(a.finished_at, a.created_at) DESC, a.created_at DESC
		LIMIT NULLIF($3, 0)
	`, workspaceID, cardID, limit, oldestSequence, beforeSequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]SessionAttemptView, 0)
	for rows.Next() {
		var item SessionAttemptView
		var startedAt, finishedAt *time.Time
		if err := rows.Scan(
			&item.ID, &item.TurnID, &item.SourceEventID, &item.Sequence, &item.Source, &item.Content, &item.DeviceNodeID, &item.DeviceName,
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
