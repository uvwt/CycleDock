package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
)

type RunSnapshot struct {
	ID             uuid.UUID
	CardID         uuid.UUID
	TriggerID      uuid.UUID
	RunnerSourceID uuid.UUID
	Status         domain.RunStatus
}

func insertQueuedRun(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID, triggerID, runnerSourceID uuid.UUID,
	leaseDuration time.Duration,
) (uuid.UUID, error) {
	if leaseDuration <= 0 {
		leaseDuration = DefaultClaimLease
	}
	runID := uuid.New()
	leaseExpiresAt := time.Now().UTC().Add(leaseDuration)
	_, err := tx.Exec(ctx, `
		INSERT INTO agent_runs (
			id, workspace_id, card_id, trigger_id, runner_source_id, status, lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, 'queued', $6)
	`, runID, workspaceID, cardID, triggerID, runnerSourceID, leaseExpiresAt)
	if err != nil {
		return uuid.Nil, mapConstraintError(err)
	}
	return runID, nil
}

// ClaimNextTrigger 把最早的 pending Trigger 转换为 queued Run。
// Card 行锁负责调度顺序，数据库 unique 约束仍是 Trigger/current Run 的最终一致性兜底。
func (s *Store) ClaimNextTrigger(
	ctx context.Context,
	workspaceID, cardID, runnerSourceID uuid.UUID,
	leaseDuration time.Duration,
) (RunSnapshot, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RunSnapshot{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return RunSnapshot{}, err
	}

	var current bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM agent_runs
			WHERE workspace_id = $1 AND card_id = $2
			  AND status IN ('queued', 'running', 'waiting_human')
		)
	`, workspaceID, cardID).Scan(&current); err != nil {
		return RunSnapshot{}, err
	}
	if current {
		return RunSnapshot{}, fmt.Errorf("%w: card already has a current run", domain.ErrConflict)
	}

	var triggerID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT t.id
		FROM triggers t
		WHERE t.workspace_id = $1
		  AND t.card_id = $2
		  AND NOT EXISTS (
			SELECT 1 FROM agent_runs r
			WHERE r.workspace_id = t.workspace_id AND r.trigger_id = t.id
		  )
		ORDER BY t.sequence
		LIMIT 1
		FOR UPDATE
	`, workspaceID, cardID).Scan(&triggerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunSnapshot{}, domain.ErrNotFound
	}
	if err != nil {
		return RunSnapshot{}, err
	}

	runID, err := insertQueuedRun(ctx, tx, workspaceID, cardID, triggerID, runnerSourceID, leaseDuration)
	if err != nil {
		return RunSnapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RunSnapshot{}, err
	}
	return RunSnapshot{
		ID:             runID,
		CardID:         cardID,
		TriggerID:      triggerID,
		RunnerSourceID: runnerSourceID,
		Status:         domain.RunStatusQueued,
	}, nil
}

func (s *Store) StartRun(ctx context.Context, workspaceID, runID uuid.UUID, leaseDuration time.Duration) error {
	if leaseDuration <= 0 {
		leaseDuration = DefaultClaimLease
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, status, _, err := lockRunCard(ctx, tx, workspaceID, runID)
	if err != nil {
		return err
	}
	if status != domain.RunStatusQueued {
		return fmt.Errorf("%w: queued run required", domain.ErrRunNotCurrent)
	}
	now := time.Now().UTC()
	command, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'running',
			started_at = $3,
			heartbeat_at = $3,
			lease_expires_at = $4,
			updated_at = $3
		WHERE workspace_id = $1 AND id = $2 AND status = 'queued'
	`, workspaceID, runID, now, now.Add(leaseDuration))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrRunNotCurrent
	}
	return tx.Commit(ctx)
}

// Heartbeat 只延长 lease，不写 Event/Timeline。
func (s *Store) Heartbeat(ctx context.Context, workspaceID, runID uuid.UUID, leaseDuration time.Duration) error {
	if leaseDuration <= 0 {
		leaseDuration = DefaultClaimLease
	}
	now := time.Now().UTC()
	command, err := s.pool.Exec(ctx, `
		UPDATE agent_runs
		SET heartbeat_at = $3, lease_expires_at = $4, updated_at = $3
		WHERE workspace_id = $1 AND id = $2 AND status = 'running'
	`, workspaceID, runID, now, now.Add(leaseDuration))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrRunNotCurrent
	}
	return nil
}

func (s *Store) WaitForHuman(ctx context.Context, workspaceID, runID uuid.UUID, stage, summary string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, status, _, err := lockRunCard(ctx, tx, workspaceID, runID)
	if err != nil {
		return err
	}
	if status != domain.RunStatusRunning {
		return domain.ErrRunNotCurrent
	}
	command, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'waiting_human',
			current_stage = $3,
			progress_summary = $4,
			heartbeat_at = NULL,
			lease_expires_at = NULL,
			updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status = 'running'
	`, workspaceID, runID, stage, summary)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrRunNotCurrent
	}
	return tx.Commit(ctx)
}

type RunEventParams struct {
	WorkspaceID uuid.UUID
	RunID       uuid.UUID
	Type        domain.EventType
	Content     string
	Checkin     *domain.CheckinStatus
}

func (s *Store) AddRunEvent(ctx context.Context, p RunEventParams) (uuid.UUID, error) {
	switch p.Type {
	case domain.EventTypeProgress, domain.EventTypeFinding, domain.EventTypeCheckin:
	default:
		return uuid.Nil, fmt.Errorf("unsupported run event type %q", p.Type)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	cardID, status, runnerSourceID, err := lockRunCard(ctx, tx, p.WorkspaceID, p.RunID)
	if err != nil {
		return uuid.Nil, err
	}
	if status != domain.RunStatusQueued && status != domain.RunStatusRunning && status != domain.RunStatusWaitingHuman {
		return uuid.Nil, domain.ErrRunNotCurrent
	}

	var checkin any
	if p.Type == domain.EventTypeCheckin {
		if p.Checkin == nil || (*p.Checkin != domain.CheckinHealthy && *p.Checkin != domain.CheckinWarning && *p.Checkin != domain.CheckinCritical) {
			return uuid.Nil, fmt.Errorf("valid check-in status required")
		}
		var kind string
		if err := tx.QueryRow(ctx,
			`SELECT kind FROM cards WHERE workspace_id = $1 AND id = $2`,
			p.WorkspaceID, cardID,
		).Scan(&kind); err != nil {
			return uuid.Nil, err
		}
		if kind != string(domain.CardKindOngoing) {
			return uuid.Nil, fmt.Errorf("%w: check-in requires ongoing card", domain.ErrInvalidTransition)
		}
		checkin = string(*p.Checkin)
	}

	eventID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id, run_id, content, checkin_status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, eventID, p.WorkspaceID, cardID, string(p.Type), runnerSourceID, p.RunID, p.Content, checkin)
	if err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return eventID, nil
}

type FinishRunParams struct {
	WorkspaceID uuid.UUID
	RunID       uuid.UUID
	Result      string
	MoveTo      *domain.NormalColumn
}

// FinishRun 与 interrupt 使用相同的 Card 行锁。谁先拿到锁谁先完成状态转换；
// 如果 interrupt 已 supersede 旧 Run，旧 Run 的迟到 finish 会稳定返回 ErrRunNotCurrent。
func (s *Store) FinishRun(ctx context.Context, p FinishRunParams) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cardID, status, runnerSourceID, err := lockRunCard(ctx, tx, p.WorkspaceID, p.RunID)
	if err != nil {
		return err
	}
	if status != domain.RunStatusQueued && status != domain.RunStatusRunning && status != domain.RunStatusWaitingHuman {
		return domain.ErrRunNotCurrent
	}

	if p.MoveTo != nil {
		if err := moveNormalCardTx(ctx, tx, p.WorkspaceID, cardID, runnerSourceID, *p.MoveTo, &p.RunID); err != nil {
			return err
		}
	}
	if p.Result != "" {
		_, err = tx.Exec(ctx, `
			INSERT INTO events (id, workspace_id, card_id, event_type, actor_source_id, run_id, content)
			VALUES ($1, $2, $3, 'result', $4, $5, $6)
		`, uuid.New(), p.WorkspaceID, cardID, runnerSourceID, p.RunID, p.Result)
		if err != nil {
			return err
		}
	}

	command, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'completed',
			result_summary = $3,
			finished_at = now(),
			heartbeat_at = NULL,
			lease_expires_at = NULL,
			updated_at = now()
		WHERE workspace_id = $1 AND id = $2
		  AND status IN ('queued', 'running', 'waiting_human')
	`, p.WorkspaceID, p.RunID, p.Result)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrRunNotCurrent
	}
	return tx.Commit(ctx)
}

func (s *Store) FailRun(ctx context.Context, workspaceID, runID uuid.UUID, summary string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, status, _, err := lockRunCard(ctx, tx, workspaceID, runID)
	if err != nil {
		return err
	}
	if status != domain.RunStatusQueued && status != domain.RunStatusRunning && status != domain.RunStatusWaitingHuman {
		return domain.ErrRunNotCurrent
	}
	_, err = tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'failed',
			error_summary = $3,
			finished_at = now(),
			heartbeat_at = NULL,
			lease_expires_at = NULL,
			updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, runID, summary)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ExpireLeases(ctx context.Context, workspaceID uuid.UUID, now time.Time) (int64, error) {
	command, err := s.pool.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'expired',
			finished_at = $2,
			heartbeat_at = NULL,
			lease_expires_at = NULL,
			updated_at = $2
		WHERE workspace_id = $1
		  AND status IN ('queued', 'running')
		  AND lease_expires_at IS NOT NULL
		  AND lease_expires_at <= $2
	`, workspaceID, now)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

func lockCard(ctx context.Context, tx pgx.Tx, workspaceID, cardID uuid.UUID) error {
	var id uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM cards WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
		workspaceID, cardID,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

func lockRunCard(ctx context.Context, tx pgx.Tx, workspaceID, runID uuid.UUID) (uuid.UUID, domain.RunStatus, uuid.UUID, error) {
	var cardID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT card_id FROM agent_runs WHERE workspace_id = $1 AND id = $2`,
		workspaceID, runID,
	).Scan(&cardID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, "", uuid.Nil, domain.ErrNotFound
		}
		return uuid.Nil, "", uuid.Nil, err
	}
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return uuid.Nil, "", uuid.Nil, err
	}

	var status string
	var runnerSourceID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT status, runner_source_id
		FROM agent_runs
		WHERE workspace_id = $1 AND id = $2
		FOR UPDATE
	`, workspaceID, runID).Scan(&status, &runnerSourceID); err != nil {
		return uuid.Nil, "", uuid.Nil, err
	}
	return cardID, domain.RunStatus(status), runnerSourceID, nil
}
