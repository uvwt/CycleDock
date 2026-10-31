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

type CardTurn struct {
	DeviceNodeID      string
	DeviceName        string
	ID                uuid.UUID
	WorkspaceID       uuid.UUID
	CardID            uuid.UUID
	Source            domain.TurnSource
	Content           string
	CreatedBySourceID uuid.NullUUID
	SourceEventID     uuid.NullUUID
	DispatchMode      domain.DispatchMode
	ScheduleID        uuid.NullUUID
	ScheduledFor      *time.Time
	CreatedAt         time.Time
}

type CreateTurnResult struct {
	Turn   CardTurn
	Reused bool
}

type TurnAttempt struct {
	ID               uuid.UUID
	WorkspaceID      uuid.UUID
	CardID           uuid.UUID
	TurnID           uuid.UUID
	AttemptNumber    int
	Reason           domain.AttemptReason
	RetryOfAttemptID uuid.NullUUID
	RunnerSourceID   uuid.UUID
	InvocationID     string
	Status           domain.AttemptStatus
	ErrorSummary     string
	StartedAt        *time.Time
	LeaseExpiresAt   *time.Time
	FinishedAt       *time.Time
}

type AttemptClaim struct {
	Attempt TurnAttempt
	Turn    CardTurn
}

type RetryAttemptResult struct {
	Attempt TurnAttempt
	Reused  bool
}

func (s *Store) CreateHumanTurn(
	ctx context.Context,
	workspaceID, cardID, humanSourceID uuid.UUID,
	content, idempotencyKey string,
	dispatchMode domain.DispatchMode,
) (CreateTurnResult, error) {
	content = strings.TrimSpace(content)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if content == "" {
		return CreateTurnResult{}, errors.New("turn content is required")
	}
	if idempotencyKey == "" {
		return CreateTurnResult{}, errors.New("idempotency key is required")
	}
	if dispatchMode != domain.DispatchModeEnqueue && dispatchMode != domain.DispatchModeInterrupt {
		return CreateTurnResult{}, fmt.Errorf("invalid dispatch mode %q", dispatchMode)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateTurnResult{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return CreateTurnResult{}, err
	}
	if err := requireHumanSource(ctx, tx, workspaceID, humanSourceID); err != nil {
		return CreateTurnResult{}, err
	}
	if err := ensureDiscussionAcceptsWork(ctx, tx, workspaceID, cardID); err != nil {
		return CreateTurnResult{}, err
	}

	requestHash := stableRequestHash(
		"human_turn", workspaceID.String(), cardID.String(), humanSourceID.String(),
		string(dispatchMode), content,
	)
	if existing, found, err := findTurnByIdempotency(
		ctx, tx, workspaceID, humanSourceID, idempotencyKey,
	); err != nil {
		return CreateTurnResult{}, err
	} else if found {
		if existing.requestHash != requestHash {
			return CreateTurnResult{}, domain.ErrIdempotencyMismatch
		}
		if err := tx.Commit(ctx); err != nil {
			return CreateTurnResult{}, err
		}
		return CreateTurnResult{Turn: existing.turn, Reused: true}, nil
	}

	turn, err := insertTurn(ctx, tx, insertTurnParams{
		WorkspaceID:       workspaceID,
		CardID:            cardID,
		Source:            domain.TurnSourceHuman,
		Content:           content,
		CreatedBySourceID: uuid.NullUUID{UUID: humanSourceID, Valid: true},
		DispatchMode:      dispatchMode,
		IdempotencyKey:    idempotencyKey,
		RequestHash:       requestHash,
	})
	if err != nil {
		return CreateTurnResult{}, err
	}
	if err := supersedeAttemptForHumanTurn(
		ctx, tx, workspaceID, cardID, dispatchMode, time.Now().UTC(),
	); err != nil {
		return CreateTurnResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateTurnResult{}, err
	}
	s.notifyWorkReady()
	return CreateTurnResult{Turn: turn}, nil
}

func (s *Store) CreateSystemTurn(
	ctx context.Context,
	workspaceID, cardID, cycleAgentSourceID uuid.UUID,
	content, idempotencyKey string,
) (CreateTurnResult, error) {
	content = strings.TrimSpace(content)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if content == "" {
		return CreateTurnResult{}, errors.New("turn content is required")
	}
	if idempotencyKey == "" {
		return CreateTurnResult{}, errors.New("idempotency key is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateTurnResult{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return CreateTurnResult{}, err
	}
	var kind string
	err = tx.QueryRow(ctx, `
		SELECT kind
		FROM sources
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, cycleAgentSourceID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return CreateTurnResult{}, domain.ErrNotFound
	}
	if err != nil {
		return CreateTurnResult{}, err
	}
	if kind != string(domain.SourceKindCycleAgent) {
		return CreateTurnResult{}, fmt.Errorf("%w: system turn requires cycle_agent source", domain.ErrConflict)
	}

	requestHash := stableRequestHash(
		"system_turn", workspaceID.String(), cardID.String(), cycleAgentSourceID.String(), content,
	)
	if existing, found, err := findTurnByIdempotency(
		ctx, tx, workspaceID, cycleAgentSourceID, idempotencyKey,
	); err != nil {
		return CreateTurnResult{}, err
	} else if found {
		if existing.requestHash != requestHash {
			return CreateTurnResult{}, domain.ErrIdempotencyMismatch
		}
		if err := tx.Commit(ctx); err != nil {
			return CreateTurnResult{}, err
		}
		return CreateTurnResult{Turn: existing.turn, Reused: true}, nil
	}

	turn, err := insertTurn(ctx, tx, insertTurnParams{
		WorkspaceID:       workspaceID,
		CardID:            cardID,
		Source:            domain.TurnSourceSystem,
		Content:           content,
		CreatedBySourceID: uuid.NullUUID{UUID: cycleAgentSourceID, Valid: true},
		DispatchMode:      domain.DispatchModeEnqueue,
		IdempotencyKey:    idempotencyKey,
		RequestHash:       requestHash,
	})
	if err != nil {
		return CreateTurnResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateTurnResult{}, err
	}
	s.notifyWorkReady()
	return CreateTurnResult{Turn: turn}, nil
}

func (s *Store) CreateScheduleTurn(
	ctx context.Context,
	workspaceID, scheduleID uuid.UUID,
	scheduledFor time.Time,
	content string,
) (CreateTurnResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return CreateTurnResult{}, errors.New("turn content is required")
	}
	scheduledFor = scheduledFor.UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateTurnResult{}, err
	}
	defer tx.Rollback(ctx)

	var cardID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT card_id
		FROM card_schedules
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, scheduleID).Scan(&cardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CreateTurnResult{}, domain.ErrNotFound
	}
	if err != nil {
		return CreateTurnResult{}, err
	}
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return CreateTurnResult{}, err
	}

	var existingID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM card_turns
		WHERE schedule_id=$1 AND scheduled_for=$2
	`, scheduleID, scheduledFor).Scan(&existingID)
	if err == nil {
		turn, err := loadTurn(ctx, tx, workspaceID, existingID)
		if err != nil {
			return CreateTurnResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CreateTurnResult{}, err
		}
		return CreateTurnResult{Turn: turn, Reused: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CreateTurnResult{}, err
	}

	turn, err := insertTurn(ctx, tx, insertTurnParams{
		WorkspaceID:  workspaceID,
		CardID:       cardID,
		Source:       domain.TurnSourceSchedule,
		Content:      content,
		DispatchMode: domain.DispatchModeEnqueue,
		ScheduleID:   uuid.NullUUID{UUID: scheduleID, Valid: true},
		ScheduledFor: &scheduledFor,
	})
	if err != nil {
		return CreateTurnResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateTurnResult{}, err
	}
	return CreateTurnResult{Turn: turn}, nil
}

func (s *Store) ClaimNextTurnAttempt(
	ctx context.Context,
	workspaceID, runnerSourceID uuid.UUID,
	now time.Time,
	leaseDuration time.Duration,
) (*AttemptClaim, error) {
	if leaseDuration <= 0 {
		leaseDuration = DefaultClaimLease
	}
	now = now.UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := requireRunnableSource(ctx, tx, workspaceID, runnerSourceID); err != nil {
		return nil, err
	}

	// Retry/recovery/interrupt can reserve the Card with a queued Attempt before
	// a worker is available. Always start that reserved execution first.
	claim, found, err := claimQueuedAttempt(
		ctx, tx, workspaceID, runnerSourceID, now, leaseDuration,
	)
	if err != nil {
		return nil, err
	}
	if found {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return claim, nil
	}

	var turn CardTurn
	err = tx.QueryRow(ctx, `
		SELECT
			t.id, t.workspace_id, t.card_id, t.source, t.content, t.device_node_id, t.device_name,
			t.created_by_source_id, t.source_event_id, t.dispatch_mode, t.schedule_id,
			t.scheduled_for, t.created_at
		FROM card_turns t
		JOIN cards c
		  ON c.workspace_id=t.workspace_id
		 AND c.id=t.card_id
		WHERE t.workspace_id=$1
		  AND t.cancelled_at IS NULL
		  AND NOT EXISTS (
			SELECT 1
			FROM turn_attempts attempted
			WHERE attempted.workspace_id=t.workspace_id
			  AND attempted.turn_id=t.id
		  )
		  AND NOT EXISTS (
			SELECT 1
			FROM turn_attempts current_attempt
			WHERE current_attempt.workspace_id=t.workspace_id
			  AND current_attempt.card_id=t.card_id
			  AND current_attempt.status IN ('queued','running','waiting_human')
		  )
		  AND (
			(c.kind='discussion' AND c.discussion_state='active')
			OR (c.kind='todo' AND c.todo_state<>'done')
			OR (c.kind='ongoing' AND c.ongoing_state='active')
		  )
		ORDER BY
		  CASE WHEN t.dispatch_mode='interrupt' THEN 0 ELSE 1 END,
		  t.sequence
		LIMIT 1
		FOR UPDATE OF c SKIP LOCKED
	`, workspaceID).Scan(
		&turn.ID, &turn.WorkspaceID, &turn.CardID, &turn.Source, &turn.Content, &turn.DeviceNodeID, &turn.DeviceName,
		&turn.CreatedBySourceID, &turn.SourceEventID, &turn.DispatchMode, &turn.ScheduleID,
		&turn.ScheduledFor, &turn.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	attempt, err := insertRunningAttempt(
		ctx, tx, turn, runnerSourceID, 1, domain.AttemptReasonInitial,
		uuid.NullUUID{}, now, leaseDuration,
	)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			// SELECT 的快照建立后，另一个 claimant 仍可能先提交同一 Turn 的 Attempt。
			// 唯一约束负责最终仲裁；失败方必须停止本次 Claim，不能顺手领取同一卡片的后续 Turn。
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &AttemptClaim{Attempt: attempt, Turn: turn}, nil
}

func supersedeAttemptForHumanTurn(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID uuid.UUID,
	dispatchMode domain.DispatchMode,
	now time.Time,
) error {
	interrupt := dispatchMode == domain.DispatchModeInterrupt
	_, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status='superseded',
		    error_summary=CASE
		        WHEN $3::boolean THEN 'superseded by interrupting human turn'
		        ELSE 'superseded by human reply'
		    END,
		    heartbeat_at=NULL,
		    lease_expires_at=NULL,
		    finished_at=$4,
		    updated_at=$4
		WHERE workspace_id=$1
		  AND card_id=$2
		  AND status IN ('queued','running','waiting_human')
		  AND ($3::boolean OR status='waiting_human')
	`, workspaceID, cardID, interrupt, now)
	if err != nil {
		return fmt.Errorf("supersede current attempt for human turn: %w", err)
	}
	return nil
}

func (s *Store) RetryAttempt(
	ctx context.Context,
	workspaceID, cardID, attemptID, runnerSourceID uuid.UUID,
	reason domain.AttemptReason,
) (RetryAttemptResult, error) {
	if reason != domain.AttemptReasonRetry && reason != domain.AttemptReasonRecovery {
		return RetryAttemptResult{}, fmt.Errorf("invalid retry reason %q", reason)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RetryAttemptResult{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return RetryAttemptResult{}, err
	}
	if err := requireRunnableSource(ctx, tx, workspaceID, runnerSourceID); err != nil {
		return RetryAttemptResult{}, err
	}

	var target TurnAttempt
	err = scanAttempt(tx.QueryRow(ctx, `
		SELECT
			id, workspace_id, card_id, turn_id, attempt_number, reason,
			retry_of_attempt_id, runner_source_id, invocation_id, status,
			error_summary, started_at, lease_expires_at, finished_at
		FROM turn_attempts
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		FOR UPDATE
	`, workspaceID, cardID, attemptID), &target)
	if errors.Is(err, pgx.ErrNoRows) {
		return RetryAttemptResult{}, domain.ErrNotFound
	}
	if err != nil {
		return RetryAttemptResult{}, err
	}

	var successor TurnAttempt
	err = scanAttempt(tx.QueryRow(ctx, `
		SELECT
			id, workspace_id, card_id, turn_id, attempt_number, reason,
			retry_of_attempt_id, runner_source_id, invocation_id, status,
			error_summary, started_at, lease_expires_at, finished_at
		FROM turn_attempts
		WHERE workspace_id=$1 AND card_id=$2 AND retry_of_attempt_id=$3
	`, workspaceID, cardID, attemptID), &successor)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return RetryAttemptResult{}, err
		}
		s.notifyWorkReady()
		return RetryAttemptResult{Attempt: successor, Reused: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RetryAttemptResult{}, err
	}

	switch target.Status {
	case domain.AttemptStatusFailed, domain.AttemptStatusExpired, domain.AttemptStatusCancelled:
	default:
		return RetryAttemptResult{}, fmt.Errorf("%w: attempt is not retryable", domain.ErrConflict)
	}

	var latestID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM turn_attempts
		WHERE workspace_id=$1 AND turn_id=$2
		ORDER BY attempt_number DESC
		LIMIT 1
		FOR UPDATE
	`, workspaceID, target.TurnID).Scan(&latestID)
	if err != nil {
		return RetryAttemptResult{}, err
	}
	if latestID != target.ID {
		return RetryAttemptResult{}, fmt.Errorf("%w: only the latest attempt can be retried", domain.ErrConflict)
	}

	attemptIDNew := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO turn_attempts (
			id, workspace_id, card_id, turn_id, attempt_number, reason,
			retry_of_attempt_id, runner_source_id, status
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'queued')
	`, attemptIDNew, workspaceID, cardID, target.TurnID, target.AttemptNumber+1,
		string(reason), target.ID, runnerSourceID)
	if err != nil {
		return RetryAttemptResult{}, mapConstraintError(err)
	}

	attempt := TurnAttempt{
		ID:               attemptIDNew,
		WorkspaceID:      workspaceID,
		CardID:           cardID,
		TurnID:           target.TurnID,
		AttemptNumber:    target.AttemptNumber + 1,
		Reason:           reason,
		RetryOfAttemptID: uuid.NullUUID{UUID: target.ID, Valid: true},
		RunnerSourceID:   runnerSourceID,
		Status:           domain.AttemptStatusQueued,
	}
	if err := tx.Commit(ctx); err != nil {
		return RetryAttemptResult{}, err
	}
	s.notifyWorkReady()
	return RetryAttemptResult{Attempt: attempt}, nil
}

func (s *Store) FinishAttempt(
	ctx context.Context,
	workspaceID, cardID, attemptID uuid.UUID,
	status domain.AttemptStatus,
	errorSummary string,
	now time.Time,
) error {
	switch status {
	case domain.AttemptStatusCompleted,
		domain.AttemptStatusFailed,
		domain.AttemptStatusSuperseded,
		domain.AttemptStatusExpired,
		domain.AttemptStatusCancelled:
	default:
		return fmt.Errorf("invalid terminal attempt status %q", status)
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
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status=$4,
		    error_summary=$5,
		    heartbeat_at=NULL,
		    lease_expires_at=NULL,
		    finished_at=$6,
		    updated_at=$6
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND status IN ('queued','running','waiting_human')
	`, workspaceID, cardID, attemptID, string(status), strings.TrimSpace(errorSummary), now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrAttemptNotCurrent
	}
	return tx.Commit(ctx)
}

type AttemptLeaseRecoveryResult struct {
	Expired          int
	RecoveryAttempts int
}

// EditPendingTurn 只修改尚未被领取的人工输入，并同步其原 Comment Event。
// 创建请求的幂等凭据保持不变，发送重试不能覆盖编辑或制造新的输入。
func (s *Store) EditPendingTurn(
	ctx context.Context,
	workspaceID, cardID, turnID, humanSourceID uuid.UUID,
	content string,
) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("%w: turn content is required", domain.ErrConflict)
	}
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
	if err := ensureDiscussionAcceptsWork(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	var eventID uuid.NullUUID
	var source domain.TurnSource
	var cancelledAt pgtype.Timestamptz
	var attempted bool
	err = tx.QueryRow(ctx, `
		SELECT t.source_event_id, t.source, t.cancelled_at, EXISTS (
			SELECT 1 FROM turn_attempts a
			WHERE a.workspace_id=t.workspace_id AND a.turn_id=t.id
		)
		FROM card_turns t
		WHERE t.workspace_id=$1 AND t.card_id=$2 AND t.id=$3
		FOR UPDATE
	`, workspaceID, cardID, turnID).Scan(&eventID, &source, &cancelledAt, &attempted)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if source != domain.TurnSourceHuman || cancelledAt.Valid || attempted {
		return fmt.Errorf("%w: only pending human turns can be edited", domain.ErrConflict)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE card_turns SET content=$4 WHERE workspace_id=$1 AND card_id=$2 AND id=$3
	`, workspaceID, cardID, turnID, content); err != nil {
		return err
	}
	if eventID.Valid {
		if _, err := tx.Exec(ctx, `
			UPDATE events SET content=$4 WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		`, workspaceID, cardID, eventID.UUID, content); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SteerPendingTurn 将已有排队输入转为 interrupt，保留原 Turn/Event 和幂等键。
// 与 Claim 共用 Card 行锁：已经被领取的输入不能再中断后续执行。
func (s *Store) SteerPendingTurn(
	ctx context.Context,
	workspaceID, cardID, turnID, humanSourceID uuid.UUID,
	now time.Time,
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
	if err := ensureDiscussionAcceptsWork(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}

	var source domain.TurnSource
	var mode domain.DispatchMode
	var cancelledAt pgtype.Timestamptz
	var attempted bool
	err = tx.QueryRow(ctx, `
		SELECT t.source, t.dispatch_mode, t.cancelled_at, EXISTS (
			SELECT 1 FROM turn_attempts a
			WHERE a.workspace_id=t.workspace_id AND a.turn_id=t.id
		)
		FROM card_turns t
		WHERE t.workspace_id=$1 AND t.card_id=$2 AND t.id=$3
		FOR UPDATE
	`, workspaceID, cardID, turnID).Scan(&source, &mode, &cancelledAt, &attempted)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if source != domain.TurnSourceHuman || cancelledAt.Valid || attempted {
		return fmt.Errorf("%w: only pending human turns can be steered", domain.ErrConflict)
	}
	if mode == domain.DispatchModeInterrupt {
		return tx.Commit(ctx)
	}
	// 新选择的引导成为唯一优先输入，其他未领取输入继续按原 FIFO 排队。
	if _, err := tx.Exec(ctx, `
		UPDATE card_turns t SET dispatch_mode='enqueue'
		WHERE t.workspace_id=$1 AND t.card_id=$2 AND t.id<>$3
		  AND t.dispatch_mode='interrupt' AND t.cancelled_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM turn_attempts a
			WHERE a.workspace_id=t.workspace_id AND a.turn_id=t.id
		  )
	`, workspaceID, cardID, turnID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE card_turns SET dispatch_mode='interrupt'
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
	`, workspaceID, cardID, turnID); err != nil {
		return err
	}
	if err := supersedeAttemptForHumanTurn(ctx, tx, workspaceID, cardID, domain.DispatchModeInterrupt, now.UTC()); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.notifyWorkReady()
	return nil
}

func (s *Store) CancelPendingTurn(
	ctx context.Context,
	workspaceID, cardID, turnID, humanSourceID uuid.UUID,
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
	var attempted bool
	err = tx.QueryRow(ctx, `
		SELECT t.cancelled_at, EXISTS (
			SELECT 1
			FROM turn_attempts a
			WHERE a.workspace_id=t.workspace_id AND a.turn_id=t.id
		)
		FROM card_turns t
		WHERE t.workspace_id=$1 AND t.card_id=$2 AND t.id=$3
		FOR UPDATE
	`, workspaceID, cardID, turnID).Scan(&cancelledAt, &attempted)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if cancelledAt.Valid {
		return tx.Commit(ctx)
	}
	if attempted {
		return fmt.Errorf("%w: turn already has an attempt; cancel the attempt instead", domain.ErrConflict)
	}

	_, err = tx.Exec(ctx, `
		UPDATE card_turns
		SET cancelled_at=now(),
		    cancelled_by_source_id=$4,
		    cancel_reason=$5
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
	`, workspaceID, cardID, turnID, humanSourceID, strings.TrimSpace(reason))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CancelAttempt(
	ctx context.Context,
	workspaceID, cardID, attemptID, humanSourceID uuid.UUID,
	reason string,
	now time.Time,
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

	var status domain.AttemptStatus
	err = tx.QueryRow(ctx, `
		SELECT status
		FROM turn_attempts
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		FOR UPDATE
	`, workspaceID, cardID, attemptID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == domain.AttemptStatusCancelled {
		return tx.Commit(ctx)
	}
	switch status {
	case domain.AttemptStatusQueued, domain.AttemptStatusRunning, domain.AttemptStatusWaitingHuman:
	default:
		return fmt.Errorf("%w: only current attempt can be cancelled", domain.ErrConflict)
	}

	now = now.UTC()
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status='cancelled',
		    error_summary=$4,
		    heartbeat_at=NULL,
		    lease_expires_at=NULL,
		    finished_at=$5,
		    updated_at=$5
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND status IN ('queued','running','waiting_human')
	`, workspaceID, cardID, attemptID, strings.TrimSpace(reason), now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrAttemptNotCurrent
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.notifyWorkReady()
	return nil
}

func (s *Store) HeartbeatAttempt(
	ctx context.Context,
	workspaceID, cardID, attemptID uuid.UUID,
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
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET heartbeat_at=$4,
		    lease_expires_at=$5,
		    updated_at=$4
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND status='running'
	`, workspaceID, cardID, attemptID, now, now.Add(leaseDuration))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrAttemptNotCurrent
	}
	return tx.Commit(ctx)
}

func (s *Store) WaitAttemptForHuman(
	ctx context.Context,
	workspaceID, cardID, attemptID uuid.UUID,
	now time.Time,
) error {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status='waiting_human',
		    heartbeat_at=NULL,
		    lease_expires_at=NULL,
		    updated_at=$4
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND status='running'
	`, workspaceID, cardID, attemptID, now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrAttemptNotCurrent
	}
	return tx.Commit(ctx)
}

func (s *Store) ExpireAndRecoverBuiltInAttempts(
	ctx context.Context,
	workspaceID uuid.UUID,
	now time.Time,
	limit int,
) (AttemptLeaseRecoveryResult, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	now = now.UTC()

	rows, err := s.pool.Query(ctx, `
		SELECT id, card_id
		FROM turn_attempts
		WHERE workspace_id=$1
		  AND status='running'
		  AND lease_expires_at IS NOT NULL
		  AND lease_expires_at <= $2
		ORDER BY lease_expires_at, created_at, id
		LIMIT $3
	`, workspaceID, now, limit)
	if err != nil {
		return AttemptLeaseRecoveryResult{}, err
	}
	type candidate struct {
		attemptID uuid.UUID
		cardID    uuid.UUID
	}
	candidates := make([]candidate, 0, limit)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.attemptID, &item.cardID); err != nil {
			rows.Close()
			return AttemptLeaseRecoveryResult{}, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return AttemptLeaseRecoveryResult{}, err
	}
	rows.Close()

	var result AttemptLeaseRecoveryResult
	for _, item := range candidates {
		expired, recovered, err := s.expireAndRecoverBuiltInAttempt(
			ctx, workspaceID, item.cardID, item.attemptID, now,
		)
		if err != nil {
			return result, err
		}
		if expired {
			result.Expired++
		}
		if recovered {
			result.RecoveryAttempts++
		}
	}
	if result.RecoveryAttempts > 0 {
		s.notifyWorkReady()
	}
	return result, nil
}

func (s *Store) expireAndRecoverBuiltInAttempt(
	ctx context.Context,
	workspaceID, cardID, attemptID uuid.UUID,
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

	var attempt TurnAttempt
	var invocationID pgtype.Text
	var sourceKind, cardKind string
	var routeEnabled bool
	var discussionState, todoState, ongoingState pgtype.Text
	err = tx.QueryRow(ctx, `
		SELECT
			a.id, a.workspace_id, a.card_id, a.turn_id, a.attempt_number, a.reason,
			a.retry_of_attempt_id, a.runner_source_id, a.invocation_id, a.status,
			a.started_at, a.lease_expires_at, a.finished_at,
			src.kind,
			EXISTS (
				SELECT 1
				FROM workspace_model_routes route
				WHERE route.workspace_id=a.workspace_id AND route.enabled
			),
			c.kind, c.discussion_state, c.todo_state, c.ongoing_state
		FROM turn_attempts a
		JOIN sources src
		  ON src.workspace_id=a.workspace_id AND src.id=a.runner_source_id
		JOIN cards c
		  ON c.workspace_id=a.workspace_id AND c.id=a.card_id
		WHERE a.workspace_id=$1 AND a.card_id=$2 AND a.id=$3
		FOR UPDATE OF a
	`, workspaceID, cardID, attemptID).Scan(
		&attempt.ID, &attempt.WorkspaceID, &attempt.CardID, &attempt.TurnID,
		&attempt.AttemptNumber, &attempt.Reason, &attempt.RetryOfAttemptID,
		&attempt.RunnerSourceID, &invocationID, &attempt.Status,
		&attempt.StartedAt, &attempt.LeaseExpiresAt, &attempt.FinishedAt,
		&sourceKind, &routeEnabled,
		&cardKind, &discussionState, &todoState, &ongoingState,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if invocationID.Valid {
		attempt.InvocationID = invocationID.String
	}
	if attempt.Status != domain.AttemptStatusRunning ||
		attempt.LeaseExpiresAt == nil || attempt.LeaseExpiresAt.After(now) {
		return false, false, tx.Commit(ctx)
	}

	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status='expired',
		    error_summary='execution lease expired',
		    heartbeat_at=NULL,
		    lease_expires_at=NULL,
		    finished_at=$4,
		    updated_at=$4
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND status='running'
	`, workspaceID, cardID, attemptID, now)
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
	if sourceKind != string(domain.SourceKindCycleAgent) || !routeEnabled || !executable {
		if err := tx.Commit(ctx); err != nil {
			return false, false, err
		}
		return true, false, nil
	}

	var existingID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM turn_attempts
		WHERE workspace_id=$1 AND retry_of_attempt_id=$2
	`, workspaceID, attemptID).Scan(&existingID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return false, false, err
		}
		return true, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, false, err
	}

	recoveryID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO turn_attempts (
			id, workspace_id, card_id, turn_id, attempt_number, reason,
			retry_of_attempt_id, runner_source_id, status
		)
		VALUES ($1,$2,$3,$4,$5,'recovery',$6,$7,'queued')
	`, recoveryID, workspaceID, cardID, attempt.TurnID,
		attempt.AttemptNumber+1, attempt.ID, attempt.RunnerSourceID)
	if err != nil {
		return false, false, mapConstraintError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, err
	}
	return true, true, nil
}

func (s *Store) GetAttempt(
	ctx context.Context,
	workspaceID, attemptID uuid.UUID,
) (TurnAttempt, error) {
	var attempt TurnAttempt
	err := scanAttempt(s.pool.QueryRow(ctx, `
		SELECT
			id, workspace_id, card_id, turn_id, attempt_number, reason,
			retry_of_attempt_id, runner_source_id, invocation_id, status,
			error_summary, started_at, lease_expires_at, finished_at
		FROM turn_attempts
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, attemptID), &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return TurnAttempt{}, domain.ErrNotFound
	}
	return attempt, err
}

type insertTurnParams struct {
	DeviceNodeID      string
	DeviceName        string
	WorkspaceID       uuid.UUID
	CardID            uuid.UUID
	Source            domain.TurnSource
	Content           string
	CreatedBySourceID uuid.NullUUID
	SourceEventID     uuid.NullUUID
	DispatchMode      domain.DispatchMode
	ScheduleID        uuid.NullUUID
	ScheduledFor      *time.Time
	IdempotencyKey    string
	RequestHash       string
}

func insertTurn(ctx context.Context, tx pgx.Tx, p insertTurnParams) (CardTurn, error) {
	turnID := uuid.New()
	var scheduledFor any
	if p.ScheduledFor != nil {
		scheduledFor = *p.ScheduledFor
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO card_turns (
			id, workspace_id, card_id, source, content, created_by_source_id,
			source_event_id, dispatch_mode, schedule_id, scheduled_for, idempotency_key, request_hash, device_node_id, device_name
		)
		VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),NULLIF($12,''),$13,$14
		)
	`, turnID, p.WorkspaceID, p.CardID, string(p.Source), p.Content,
		nullUUIDValue(p.CreatedBySourceID), nullUUIDValue(p.SourceEventID), string(p.DispatchMode),
		nullUUIDValue(p.ScheduleID), scheduledFor, p.IdempotencyKey, p.RequestHash, p.DeviceNodeID, p.DeviceName)
	if err != nil {
		return CardTurn{}, mapConstraintError(err)
	}
	return loadTurn(ctx, tx, p.WorkspaceID, turnID)
}

type existingTurnRequest struct {
	turn        CardTurn
	requestHash string
}

func findTurnByIdempotency(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sourceID uuid.UUID,
	key string,
) (existingTurnRequest, bool, error) {
	var turnID uuid.UUID
	var hash string
	err := tx.QueryRow(ctx, `
		SELECT id, COALESCE(request_hash,'')
		FROM card_turns
		WHERE workspace_id=$1
		  AND created_by_source_id=$2
		  AND idempotency_key=$3
	`, workspaceID, sourceID, key).Scan(&turnID, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return existingTurnRequest{}, false, nil
	}
	if err != nil {
		return existingTurnRequest{}, false, err
	}
	turn, err := loadTurn(ctx, tx, workspaceID, turnID)
	if err != nil {
		return existingTurnRequest{}, false, err
	}
	return existingTurnRequest{turn: turn, requestHash: hash}, true, nil
}

func loadTurn(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, workspaceID, turnID uuid.UUID) (CardTurn, error) {
	var turn CardTurn
	err := q.QueryRow(ctx, `
		SELECT
			id, workspace_id, card_id, source, content, device_node_id, device_name, created_by_source_id,
			source_event_id, dispatch_mode, schedule_id, scheduled_for, created_at
		FROM card_turns
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, turnID).Scan(
		&turn.ID, &turn.WorkspaceID, &turn.CardID, &turn.Source, &turn.Content, &turn.DeviceNodeID, &turn.DeviceName,
		&turn.CreatedBySourceID, &turn.SourceEventID, &turn.DispatchMode, &turn.ScheduleID,
		&turn.ScheduledFor, &turn.CreatedAt,
	)
	return turn, err
}

func claimQueuedAttempt(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, runnerSourceID uuid.UUID,
	now time.Time,
	leaseDuration time.Duration,
) (*AttemptClaim, bool, error) {
	var attempt TurnAttempt
	var turn CardTurn
	var invocationID pgtype.Text
	err := tx.QueryRow(ctx, `
		SELECT
			a.id, a.workspace_id, a.card_id, a.turn_id, a.attempt_number, a.reason,
			a.retry_of_attempt_id, a.runner_source_id, a.invocation_id, a.status,
			a.started_at, a.lease_expires_at, a.finished_at,
			t.id, t.workspace_id, t.card_id, t.source, t.content, t.device_node_id, t.device_name,
			t.created_by_source_id, t.source_event_id, t.dispatch_mode, t.schedule_id, t.scheduled_for, t.created_at
		FROM turn_attempts a
		JOIN card_turns t
		  ON t.workspace_id=a.workspace_id
		 AND t.card_id=a.card_id
		 AND t.id=a.turn_id
		JOIN cards c
		  ON c.workspace_id=a.workspace_id
		 AND c.id=a.card_id
		WHERE a.workspace_id=$1
		  AND a.runner_source_id=$2
		  AND a.status='queued'
		  AND t.cancelled_at IS NULL
		  AND (
			(c.kind='discussion' AND c.discussion_state='active')
			OR (c.kind='todo' AND c.todo_state<>'done')
			OR (c.kind='ongoing' AND c.ongoing_state='active')
		  )
		ORDER BY t.sequence, a.attempt_number
		LIMIT 1
		FOR UPDATE OF c, a SKIP LOCKED
	`, workspaceID, runnerSourceID).Scan(
		&attempt.ID, &attempt.WorkspaceID, &attempt.CardID, &attempt.TurnID,
		&attempt.AttemptNumber, &attempt.Reason, &attempt.RetryOfAttemptID,
		&attempt.RunnerSourceID, &invocationID, &attempt.Status,
		&attempt.StartedAt, &attempt.LeaseExpiresAt, &attempt.FinishedAt,
		&turn.ID, &turn.WorkspaceID, &turn.CardID, &turn.Source, &turn.Content, &turn.DeviceNodeID, &turn.DeviceName,
		&turn.CreatedBySourceID, &turn.SourceEventID, &turn.DispatchMode, &turn.ScheduleID,
		&turn.ScheduledFor, &turn.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if invocationID.Valid {
		attempt.InvocationID = invocationID.String
	}

	leaseExpires := now.Add(leaseDuration)
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status='running',
		    started_at=$4,
		    heartbeat_at=$4,
		    lease_expires_at=$5,
		    updated_at=$4
		WHERE workspace_id=$1 AND id=$2 AND runner_source_id=$3 AND status='queued'
	`, workspaceID, attempt.ID, runnerSourceID, now, leaseExpires)
	if err != nil {
		return nil, false, err
	}
	if command.RowsAffected() != 1 {
		return nil, false, domain.ErrAttemptNotCurrent
	}
	attempt.Status = domain.AttemptStatusRunning
	attempt.StartedAt = &now
	attempt.LeaseExpiresAt = &leaseExpires
	return &AttemptClaim{Attempt: attempt, Turn: turn}, true, nil
}

func insertRunningAttempt(
	ctx context.Context,
	tx pgx.Tx,
	turn CardTurn,
	runnerSourceID uuid.UUID,
	attemptNumber int,
	reason domain.AttemptReason,
	retryOf uuid.NullUUID,
	now time.Time,
	leaseDuration time.Duration,
) (TurnAttempt, error) {
	attemptID := uuid.New()
	leaseExpires := now.Add(leaseDuration)
	_, err := tx.Exec(ctx, `
		INSERT INTO turn_attempts (
			id, workspace_id, card_id, turn_id, attempt_number, reason,
			retry_of_attempt_id, runner_source_id, status,
			started_at, heartbeat_at, lease_expires_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'running',$9,$9,$10)
	`, attemptID, turn.WorkspaceID, turn.CardID, turn.ID, attemptNumber,
		string(reason), nullUUIDValue(retryOf), runnerSourceID, now, leaseExpires)
	if err != nil {
		return TurnAttempt{}, mapConstraintError(err)
	}
	return TurnAttempt{
		ID:               attemptID,
		WorkspaceID:      turn.WorkspaceID,
		CardID:           turn.CardID,
		TurnID:           turn.ID,
		AttemptNumber:    attemptNumber,
		Reason:           reason,
		RetryOfAttemptID: retryOf,
		RunnerSourceID:   runnerSourceID,
		Status:           domain.AttemptStatusRunning,
		StartedAt:        &now,
		LeaseExpiresAt:   &leaseExpires,
	}, nil
}

func requireRunnableSource(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sourceID uuid.UUID,
) error {
	var kind string
	err := tx.QueryRow(ctx, `
		SELECT kind
		FROM sources
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, sourceID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if kind == string(domain.SourceKindHuman) {
		return fmt.Errorf("%w: human source cannot own an attempt", domain.ErrConflict)
	}
	return nil
}

func scanAttempt(row pgx.Row, attempt *TurnAttempt) error {
	var invocationID pgtype.Text
	var startedAt, leaseExpiresAt, finishedAt pgtype.Timestamptz
	err := row.Scan(
		&attempt.ID, &attempt.WorkspaceID, &attempt.CardID, &attempt.TurnID,
		&attempt.AttemptNumber, &attempt.Reason, &attempt.RetryOfAttemptID,
		&attempt.RunnerSourceID, &invocationID, &attempt.Status,
		&attempt.ErrorSummary, &startedAt, &leaseExpiresAt, &finishedAt,
	)
	if err != nil {
		return err
	}
	if invocationID.Valid {
		attempt.InvocationID = invocationID.String
	}
	if startedAt.Valid {
		value := startedAt.Time
		attempt.StartedAt = &value
	}
	if leaseExpiresAt.Valid {
		value := leaseExpiresAt.Time
		attempt.LeaseExpiresAt = &value
	}
	if finishedAt.Valid {
		value := finishedAt.Time
		attempt.FinishedAt = &value
	}
	return nil
}

func nullUUIDValue(value uuid.NullUUID) any {
	if !value.Valid {
		return nil
	}
	return value.UUID
}
