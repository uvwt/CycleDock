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
)

type RetryTriggerResult struct {
	TriggerID uuid.UUID
	Reused    bool
}

type LeaseRecoveryResult struct {
	Expired          int
	RecoveryTriggers int
}

// CancelPendingTrigger 取消尚未被 Claim 的 Trigger。取消只改变调度生命周期，
// 不修改 Trigger 的原始意图，也不会伪造一个 cancelled Run。
func (s *Store) CancelPendingTrigger(
	ctx context.Context,
	workspaceID, cardID, triggerID, humanSourceID uuid.UUID,
	reason string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	if err := requireHumanSource(ctx, tx, workspaceID, humanSourceID); err != nil {
		return err
	}

	var cancelledAt pgtype.Timestamptz
	var claimed bool
	err = tx.QueryRow(ctx, `
		SELECT t.cancelled_at, EXISTS (
			SELECT 1 FROM agent_runs r
			WHERE r.workspace_id = t.workspace_id AND r.trigger_id = t.id
		)
		FROM triggers t
		WHERE t.workspace_id = $1 AND t.card_id = $2 AND t.id = $3
		FOR UPDATE
	`, workspaceID, cardID, triggerID).Scan(&cancelledAt, &claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if cancelledAt.Valid {
		return tx.Commit(ctx)
	}
	if claimed {
		return fmt.Errorf("%w: trigger is already claimed; cancel its run instead", domain.ErrConflict)
	}

	_, err = tx.Exec(ctx, `
		UPDATE triggers
		SET cancelled_at = now(),
			cancelled_by_source_id = $4,
			cancel_reason = $5
		WHERE workspace_id = $1 AND card_id = $2 AND id = $3
	`, workspaceID, cardID, triggerID, humanSourceID, strings.TrimSpace(reason))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CancelRun 终结当前 Run，但不会创建新 Trigger。它与 interrupt 的区别是：
// interrupt 表达“换方向并继续”，cancel 只表达“停止这次工作”。
func (s *Store) CancelRun(
	ctx context.Context,
	workspaceID, cardID, runID, humanSourceID uuid.UUID,
	reason string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	actualCardID, status, _, err := lockRunCard(ctx, tx, workspaceID, runID)
	if err != nil {
		return err
	}
	if actualCardID != cardID {
		return domain.ErrNotFound
	}
	if err := requireHumanSource(ctx, tx, workspaceID, humanSourceID); err != nil {
		return err
	}
	if status == domain.RunStatusCancelled {
		return tx.Commit(ctx)
	}
	if status != domain.RunStatusQueued &&
		status != domain.RunStatusRunning &&
		status != domain.RunStatusWaitingHuman {
		return fmt.Errorf("%w: only current work can be cancelled", domain.ErrConflict)
	}

	now := time.Now().UTC()
	command, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'cancelled',
			cancelled_by_source_id = $4,
			cancel_reason = $5,
			finished_at = $6,
			heartbeat_at = NULL,
			lease_expires_at = NULL,
			updated_at = $6
		WHERE workspace_id = $1 AND card_id = $2 AND id = $3
		  AND status IN ('queued', 'running', 'waiting_human')
	`, workspaceID, cardID, runID, humanSourceID, strings.TrimSpace(reason), now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrRunNotCurrent
	}
	return tx.Commit(ctx)
}

// RetryTrigger 创建一个新的 manual_retry Trigger。旧 Trigger/Run 永远不会复活；
// retry_of_trigger_id 同时提供历史链路与数据库级去重。
func (s *Store) RetryTrigger(
	ctx context.Context,
	workspaceID, cardID, triggerID, humanSourceID uuid.UUID,
	idempotencyKey string,
) (RetryTriggerResult, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return RetryTriggerResult{}, fmt.Errorf("idempotency key is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RetryTriggerResult{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return RetryTriggerResult{}, err
	}
	if err := requireHumanSource(ctx, tx, workspaceID, humanSourceID); err != nil {
		return RetryTriggerResult{}, err
	}

	var cancelledAt pgtype.Timestamptz
	var runStatus pgtype.Text
	err = tx.QueryRow(ctx, `
		SELECT t.cancelled_at, r.status
		FROM triggers t
		LEFT JOIN agent_runs r
		  ON r.workspace_id = t.workspace_id
		 AND r.trigger_id = t.id
		WHERE t.workspace_id = $1 AND t.card_id = $2 AND t.id = $3
		FOR UPDATE OF t
	`, workspaceID, cardID, triggerID).Scan(&cancelledAt, &runStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return RetryTriggerResult{}, domain.ErrNotFound
	}
	if err != nil {
		return RetryTriggerResult{}, err
	}

	var existingRetryID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM triggers
		WHERE workspace_id = $1 AND card_id = $2 AND retry_of_trigger_id = $3
		LIMIT 1
	`, workspaceID, cardID, triggerID).Scan(&existingRetryID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return RetryTriggerResult{}, err
		}
		s.notifyWorkReady()
		return RetryTriggerResult{TriggerID: existingRetryID, Reused: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RetryTriggerResult{}, err
	}

	retryable := cancelledAt.Valid && !runStatus.Valid
	if runStatus.Valid {
		switch domain.RunStatus(runStatus.String) {
		case domain.RunStatusFailed, domain.RunStatusExpired, domain.RunStatusCancelled:
			retryable = true
		}
	}
	if !retryable {
		return RetryTriggerResult{}, fmt.Errorf("%w: trigger is not retryable", domain.ErrConflict)
	}

	requestHash := stableRequestHash(
		"manual_retry", workspaceID.String(), cardID.String(), triggerID.String(), humanSourceID.String(),
	)
	var keyedID, keyedRetryOf uuid.UUID
	var keyedHash string
	err = tx.QueryRow(ctx, `
		SELECT id, retry_of_trigger_id, COALESCE(request_hash, '')
		FROM triggers
		WHERE workspace_id = $1
		  AND created_by_source_id = $2
		  AND idempotency_key = $3
		  AND cause IN ('manual_retry', 'system_followup')
	`, workspaceID, humanSourceID, idempotencyKey).Scan(&keyedID, &keyedRetryOf, &keyedHash)
	if err == nil {
		if keyedRetryOf != triggerID || keyedHash != requestHash {
			return RetryTriggerResult{}, domain.ErrIdempotencyMismatch
		}
		if err := tx.Commit(ctx); err != nil {
			return RetryTriggerResult{}, err
		}
		s.notifyWorkReady()
		return RetryTriggerResult{TriggerID: keyedID, Reused: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RetryTriggerResult{}, err
	}

	retryID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO triggers (
			id, workspace_id, card_id, cause, created_by_source_id,
			dispatch_mode, idempotency_key, request_hash, retry_of_trigger_id
		)
		VALUES ($1, $2, $3, 'manual_retry', $4, 'enqueue', $5, $6, $7)
	`, retryID, workspaceID, cardID, humanSourceID, idempotencyKey, requestHash, triggerID)
	if err != nil {
		return RetryTriggerResult{}, mapConstraintError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RetryTriggerResult{}, err
	}
	s.notifyWorkReady()
	return RetryTriggerResult{TriggerID: retryID}, nil
}

// ExpireAndRecoverBuiltInLeases 用于 Dispatcher maintenance。
// 每个候选 Run 都遵循“先锁 Card，再锁 Run”的统一顺序；只有启用的 cycle_agent
// 才会获得新的 system_followup Trigger，外部 Agent 过期后只保留 expired 历史。
func (s *Store) ExpireAndRecoverBuiltInLeases(
	ctx context.Context,
	workspaceID uuid.UUID,
	now time.Time,
	limit int,
) (LeaseRecoveryResult, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, card_id
		FROM agent_runs
		WHERE workspace_id = $1
		  AND status IN ('queued', 'running')
		  AND lease_expires_at IS NOT NULL
		  AND lease_expires_at <= $2
		ORDER BY lease_expires_at, created_at, id
		LIMIT $3
	`, workspaceID, now, limit)
	if err != nil {
		return LeaseRecoveryResult{}, err
	}
	type candidate struct {
		runID  uuid.UUID
		cardID uuid.UUID
	}
	candidates := make([]candidate, 0, limit)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.runID, &item.cardID); err != nil {
			rows.Close()
			return LeaseRecoveryResult{}, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return LeaseRecoveryResult{}, err
	}
	rows.Close()

	var result LeaseRecoveryResult
	for _, item := range candidates {
		expired, recovered, err := s.expireAndRecoverBuiltInRun(ctx, workspaceID, item.cardID, item.runID, now)
		if err != nil {
			return result, err
		}
		if expired {
			result.Expired++
		}
		if recovered {
			result.RecoveryTriggers++
		}
	}
	return result, nil
}

func (s *Store) expireAndRecoverBuiltInRun(
	ctx context.Context,
	workspaceID, cardID, runID uuid.UUID,
	now time.Time,
) (bool, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, false, nil
		}
		return false, false, err
	}

	var triggerID, runnerSourceID uuid.UUID
	var status, sourceKind, cardKind string
	var leaseExpiresAt pgtype.Timestamptz
	var agentRunnable bool
	var discussionState, todoState, ongoingState pgtype.Text
	err = tx.QueryRow(ctx, `
		SELECT r.trigger_id, r.runner_source_id, r.status, r.lease_expires_at,
		       src.kind,
		       EXISTS (
		           SELECT 1
		           FROM workspace_model_routes route
		           WHERE route.workspace_id = settings.workspace_id
		             AND route.enabled
		       ),
		       c.kind, c.discussion_state, c.todo_state, c.ongoing_state
		FROM agent_runs r
		JOIN sources src
		  ON src.workspace_id = r.workspace_id AND src.id = r.runner_source_id
		JOIN cards c
		  ON c.workspace_id = r.workspace_id AND c.id = r.card_id
		LEFT JOIN workspace_agent_settings settings
		  ON settings.workspace_id = r.workspace_id
		 AND settings.source_id = r.runner_source_id
		WHERE r.workspace_id = $1 AND r.card_id = $2 AND r.id = $3
		FOR UPDATE OF r
	`, workspaceID, cardID, runID).Scan(
		&triggerID, &runnerSourceID, &status, &leaseExpiresAt,
		&sourceKind, &agentRunnable, &cardKind, &discussionState, &todoState, &ongoingState,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if (status != string(domain.RunStatusQueued) && status != string(domain.RunStatusRunning)) ||
		!leaseExpiresAt.Valid || leaseExpiresAt.Time.After(now) {
		return false, false, tx.Commit(ctx)
	}

	command, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'expired',
			finished_at = $4,
			heartbeat_at = NULL,
			lease_expires_at = NULL,
			updated_at = $4
		WHERE workspace_id = $1 AND card_id = $2 AND id = $3
		  AND status IN ('queued', 'running')
	`, workspaceID, cardID, runID, now)
	if err != nil {
		return false, false, err
	}
	if command.RowsAffected() != 1 {
		return false, false, tx.Commit(ctx)
	}

	executable := cardKind == string(domain.CardKindDiscussion) &&
		discussionState.Valid && discussionState.String == string(domain.DiscussionStateActive)
	if cardKind == string(domain.CardKindTodo) {
		executable = todoState.Valid && todoState.String != string(domain.TodoStateDone)
	} else if cardKind == string(domain.CardKindOngoing) {
		executable = ongoingState.Valid && ongoingState.String == string(domain.OngoingStateActive)
	}
	if sourceKind != string(domain.SourceKindCycleAgent) || !agentRunnable || !executable {
		if err := tx.Commit(ctx); err != nil {
			return false, false, err
		}
		return true, false, nil
	}

	recoveryID := uuid.New()
	idempotencyKey := "builtin-recovery:" + runID.String()
	requestHash := stableRequestHash(
		"system_recovery", workspaceID.String(), cardID.String(), triggerID.String(), runID.String(),
	)
	command, err = tx.Exec(ctx, `
		INSERT INTO triggers (
			id, workspace_id, card_id, cause, created_by_source_id,
			dispatch_mode, idempotency_key, request_hash, retry_of_trigger_id
		)
		VALUES ($1, $2, $3, 'system_followup', $4, 'enqueue', $5, $6, $7)
		ON CONFLICT (retry_of_trigger_id)
		WHERE retry_of_trigger_id IS NOT NULL
		DO NOTHING
	`, recoveryID, workspaceID, cardID, runnerSourceID, idempotencyKey, requestHash, triggerID)
	if err != nil {
		return false, false, err
	}
	recovered := command.RowsAffected() == 1
	if err := tx.Commit(ctx); err != nil {
		return false, false, err
	}
	return true, recovered, nil
}

func requireHumanSource(ctx context.Context, tx pgx.Tx, workspaceID, sourceID uuid.UUID) error {
	var kind string
	err := tx.QueryRow(ctx, `
		SELECT kind
		FROM sources
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, sourceID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if kind != string(domain.SourceKindHuman) {
		return domain.ErrHumanRequired
	}
	return nil
}
