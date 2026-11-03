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

// ClaimNextBuiltInAttempt 跨 Workspace 公平领取，租户并发额度在所有后端实例之间共享。
// 进程自己的执行槽位由 Dispatcher 控制；queued 不消耗正在执行的额度。
func (s *Store) ClaimNextBuiltInAttempt(
	ctx context.Context, maxPerWorkspace int, now time.Time, leaseDuration time.Duration,
) (*AttemptClaim, error) {
	if maxPerWorkspace <= 0 {
		return nil, fmt.Errorf("workspace concurrency must be positive")
	}
	if leaseDuration <= 0 {
		leaseDuration = DefaultClaimLease
	}
	// 更久没有获得执行机会的租户先领取，避免每次都从固定的第一个 Workspace 开始。
	// 使用持久 Attempt 的开始时间，公平顺序不会因为进程重启而重置。
	rows, err := s.pool.Query(ctx, `
		SELECT cfg.workspace_id
		FROM workspace_agent_settings cfg
		WHERE EXISTS (
			SELECT 1 FROM workspace_model_routes r
			WHERE r.workspace_id=cfg.workspace_id AND r.enabled
		)
		AND (
			SELECT count(*) FROM turn_attempts a
			WHERE a.workspace_id=cfg.workspace_id AND a.runner_source_id=cfg.source_id AND a.status='running'
		) < $1
		AND (
			EXISTS (
				SELECT 1 FROM turn_attempts a
				WHERE a.workspace_id=cfg.workspace_id AND a.runner_source_id=cfg.source_id AND a.status='queued'
			) OR EXISTS (
				SELECT 1 FROM card_turns t
				WHERE t.workspace_id=cfg.workspace_id AND t.cancelled_at IS NULL
				  AND NOT EXISTS (SELECT 1 FROM turn_attempts a WHERE a.workspace_id=t.workspace_id AND a.turn_id=t.id)
			)
		)
		ORDER BY (
			SELECT max(a.started_at) FROM turn_attempts a
			WHERE a.workspace_id=cfg.workspace_id AND a.runner_source_id=cfg.source_id AND a.started_at IS NOT NULL
		) ASC NULLS FIRST, cfg.workspace_id
	`, maxPerWorkspace)
	if err != nil {
		return nil, err
	}
	workspaceIDs, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	for _, workspaceID := range workspaceIDs {
		claim, err := s.claimBuiltInWorkspaceAttempt(ctx, workspaceID, maxPerWorkspace, now.UTC(), leaseDuration)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		return claim, err
	}
	return nil, domain.ErrNotFound
}

func (s *Store) claimBuiltInWorkspaceAttempt(
	ctx context.Context, workspaceID uuid.UUID, maxConcurrent int,
	now time.Time, leaseDuration time.Duration,
) (*AttemptClaim, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 先锁租户调度入口，再以新的 READ COMMITTED 快照统计运行数。
	// 不能仅在选租户的查询中 count：并发实例可能同时看到最后一个空余名额。
	var runnerSourceID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT source_id FROM workspace_agent_settings
		WHERE workspace_id=$1 FOR UPDATE SKIP LOCKED
	`, workspaceID).Scan(&runnerSourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var eligible bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM workspace_model_routes WHERE workspace_id=$1 AND enabled
		) AND (
			SELECT count(*) FROM turn_attempts
			WHERE workspace_id=$1 AND runner_source_id=$2 AND status='running'
		) < $3
	`, workspaceID, runnerSourceID, maxConcurrent).Scan(&eligible)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return nil, domain.ErrNotFound
	}
	claim, err := claimNextTurnAttempt(ctx, tx, workspaceID, runnerSourceID, now, leaseDuration)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return claim, nil
}
