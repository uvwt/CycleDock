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
)

type CardAttemptClaimResult struct {
	Attempt TurnAttempt
	Turn    CardTurn
	Reused  bool
}

func (s *Store) ClaimCardTurnAttempt(
	ctx context.Context,
	workspaceID, cardID, runnerSourceID uuid.UUID,
	now time.Time,
	leaseDuration time.Duration,
) (CardAttemptClaimResult, error) {
	if leaseDuration <= 0 {
		leaseDuration = DefaultClaimLease
	}
	now = now.UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CardAttemptClaimResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return CardAttemptClaimResult{}, err
	}
	if err := requireRunnableSource(ctx, tx, workspaceID, runnerSourceID); err != nil {
		return CardAttemptClaimResult{}, err
	}

	var current TurnAttempt
	err = scanAttempt(tx.QueryRow(ctx, `
		SELECT
			id, workspace_id, card_id, turn_id, attempt_number, reason,
			retry_of_attempt_id, runner_source_id, invocation_id, status,
			error_summary, started_at, lease_expires_at, finished_at
		FROM turn_attempts
		WHERE workspace_id=$1 AND card_id=$2
		  AND status IN ('queued','running','waiting_human')
		FOR UPDATE
	`, workspaceID, cardID), &current)
	if err == nil {
		if current.RunnerSourceID != runnerSourceID {
			return CardAttemptClaimResult{}, fmt.Errorf("%w: card attempt is owned by another source", domain.ErrConflict)
		}
		turn, loadErr := loadTurn(ctx, tx, workspaceID, current.TurnID)
		if loadErr != nil {
			return CardAttemptClaimResult{}, loadErr
		}
		if current.Status == domain.AttemptStatusQueued {
			leaseExpires := now.Add(leaseDuration)
			command, updateErr := tx.Exec(ctx, `
				UPDATE turn_attempts
				SET status='running',
				    started_at=$4,
				    heartbeat_at=$4,
				    lease_expires_at=$5,
				    updated_at=$4
				WHERE workspace_id=$1 AND id=$2 AND runner_source_id=$3 AND status='queued'
			`, workspaceID, current.ID, runnerSourceID, now, leaseExpires)
			if updateErr != nil {
				return CardAttemptClaimResult{}, updateErr
			}
			if command.RowsAffected() != 1 {
				return CardAttemptClaimResult{}, domain.ErrAttemptNotCurrent
			}
			current.Status = domain.AttemptStatusRunning
			current.StartedAt = &now
			current.LeaseExpiresAt = &leaseExpires
		}
		if err := tx.Commit(ctx); err != nil {
			return CardAttemptClaimResult{}, err
		}
		return CardAttemptClaimResult{Attempt: current, Turn: turn, Reused: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CardAttemptClaimResult{}, err
	}

	var turnID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT t.id
		FROM card_turns t
		JOIN cards c
		  ON c.workspace_id=t.workspace_id AND c.id=t.card_id
		WHERE t.workspace_id=$1 AND t.card_id=$2
		  AND t.cancelled_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM turn_attempts a
			WHERE a.workspace_id=t.workspace_id AND a.turn_id=t.id
		  )
		  AND (
			(c.kind='discussion' AND c.discussion_state='active')
			OR (c.kind='todo' AND c.todo_state<>'done')
			OR (c.kind='ongoing' AND c.ongoing_state='active')
		  )
		ORDER BY CASE WHEN t.dispatch_mode='interrupt' THEN 0 ELSE 1 END, t.sequence
		LIMIT 1
		FOR UPDATE OF t
	`, workspaceID, cardID).Scan(&turnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CardAttemptClaimResult{}, domain.ErrNotFound
	}
	if err != nil {
		return CardAttemptClaimResult{}, err
	}
	turn, err := loadTurn(ctx, tx, workspaceID, turnID)
	if err != nil {
		return CardAttemptClaimResult{}, err
	}
	attempt, err := insertRunningAttempt(
		ctx, tx, turn, runnerSourceID, 1, domain.AttemptReasonInitial,
		uuid.NullUUID{}, now, leaseDuration,
	)
	if err != nil {
		return CardAttemptClaimResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CardAttemptClaimResult{}, err
	}
	return CardAttemptClaimResult{Attempt: attempt, Turn: turn}, nil
}

func (s *Store) HeartbeatAttemptOwned(
	ctx context.Context,
	workspaceID, cardID, attemptID, runnerSourceID uuid.UUID,
	now time.Time,
	leaseDuration time.Duration,
) error {
	if leaseDuration <= 0 {
		leaseDuration = DefaultClaimLease
	}
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	if err := requireCurrentAttempt(
		ctx, tx, workspaceID, cardID, attemptID, runnerSourceID,
		domain.AttemptStatusRunning,
	); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET heartbeat_at=$5, lease_expires_at=$6, updated_at=$5
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND runner_source_id=$4 AND status='running'
	`, workspaceID, cardID, attemptID, runnerSourceID, now, now.Add(leaseDuration))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrAttemptNotCurrent
	}
	return tx.Commit(ctx)
}

func (s *Store) CompleteAttemptOwned(
	ctx context.Context,
	workspaceID, cardID, attemptID, runnerSourceID uuid.UUID,
	result string,
	now time.Time,
) error {
	result = strings.TrimSpace(result)
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}

	var actualRunner uuid.UUID
	var status domain.AttemptStatus
	var existing string
	err = tx.QueryRow(ctx, `
		SELECT runner_source_id, status, result_summary
		FROM turn_attempts
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		FOR UPDATE
	`, workspaceID, cardID, attemptID).Scan(&actualRunner, &status, &existing)
	if errors.Is(err, pgx.ErrNoRows) || actualRunner != runnerSourceID {
		return domain.ErrAttemptNotCurrent
	}
	if err != nil {
		return err
	}
	if status == domain.AttemptStatusCompleted && existing == result {
		return tx.Commit(ctx)
	}
	if status != domain.AttemptStatusRunning {
		return domain.ErrAttemptNotCurrent
	}
	if result != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO events (
				id, workspace_id, card_id, event_type, actor_source_id, attempt_id, content
			)
			VALUES ($1,$2,$3,'result',$4,$5,$6)
		`, uuid.New(), workspaceID, cardID, runnerSourceID, attemptID, result); err != nil {
			return err
		}
	}
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status='completed',
		    result_summary=$5,
		    heartbeat_at=NULL,
		    lease_expires_at=NULL,
		    finished_at=$6,
		    updated_at=$6
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND runner_source_id=$4 AND status='running'
	`, workspaceID, cardID, attemptID, runnerSourceID, result, now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrAttemptNotCurrent
	}
	return tx.Commit(ctx)
}

func (s *Store) FailAttemptOwned(
	ctx context.Context,
	workspaceID, cardID, attemptID, runnerSourceID uuid.UUID,
	summary string,
	now time.Time,
) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return fmt.Errorf("%w: failure summary is required", domain.ErrConflict)
	}
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}

	var actualRunner uuid.UUID
	var status domain.AttemptStatus
	var existing string
	err = tx.QueryRow(ctx, `
		SELECT runner_source_id, status, error_summary
		FROM turn_attempts
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		FOR UPDATE
	`, workspaceID, cardID, attemptID).Scan(&actualRunner, &status, &existing)
	if errors.Is(err, pgx.ErrNoRows) || actualRunner != runnerSourceID {
		return domain.ErrAttemptNotCurrent
	}
	if err != nil {
		return err
	}
	if status == domain.AttemptStatusFailed && existing == summary {
		return tx.Commit(ctx)
	}
	if status != domain.AttemptStatusRunning {
		return domain.ErrAttemptNotCurrent
	}
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status='failed',
		    error_summary=$5,
		    heartbeat_at=NULL,
		    lease_expires_at=NULL,
		    finished_at=$6,
		    updated_at=$6
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND runner_source_id=$4 AND status='running'
	`, workspaceID, cardID, attemptID, runnerSourceID, summary, now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrAttemptNotCurrent
	}
	return tx.Commit(ctx)
}
