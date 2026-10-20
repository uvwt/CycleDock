package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
)

type SubmitCommentParams struct {
	WorkspaceID    uuid.UUID
	CardID         uuid.UUID
	AuthorSourceID uuid.UUID
	RunnerSourceID *uuid.UUID
	Content        string
	IdempotencyKey string
	DispatchMode   domain.DispatchMode
	LeaseDuration  time.Duration
}

type SubmitCommentResult struct {
	EventID   uuid.UUID
	TriggerID uuid.UUID
	RunID     *uuid.UUID
	Reused    bool
}

func (s *Store) SubmitComment(ctx context.Context, p SubmitCommentParams) (SubmitCommentResult, error) {
	return s.submitComment(ctx, p, false)
}

// ReplyAndContinue 只接受 waiting_human 的当前 Run。用户回复形成新的 Trigger，
// 旧 Run 进入 superseded，避免把旧 Run 的执行上下文偷偷“复活”。
func (s *Store) ReplyAndContinue(ctx context.Context, p SubmitCommentParams) (SubmitCommentResult, error) {
	p.DispatchMode = domain.DispatchModeInterrupt
	return s.submitComment(ctx, p, true)
}

func (s *Store) submitComment(ctx context.Context, p SubmitCommentParams, requireWaitingHuman bool) (SubmitCommentResult, error) {
	if p.DispatchMode != domain.DispatchModeEnqueue && p.DispatchMode != domain.DispatchModeInterrupt {
		return SubmitCommentResult{}, fmt.Errorf("invalid dispatch mode %q", p.DispatchMode)
	}
	if p.LeaseDuration <= 0 {
		p.LeaseDuration = DefaultClaimLease
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SubmitCommentResult{}, err
	}
	defer tx.Rollback(ctx)

	// 会改变 current Run 的事务统一先锁 Card。interrupt、finish、waiting_human 接续都遵守同一锁顺序。
	var cardKind string
	if err := tx.QueryRow(ctx,
		`SELECT kind FROM cards WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
		p.WorkspaceID, p.CardID,
	).Scan(&cardKind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SubmitCommentResult{}, domain.ErrNotFound
		}
		return SubmitCommentResult{}, err
	}

	requestHash := commentRequestHash(p)
	if p.IdempotencyKey != "" {
		existing, found, err := findCommentRequest(ctx, tx, p.WorkspaceID, p.AuthorSourceID, p.IdempotencyKey)
		if err != nil {
			return SubmitCommentResult{}, err
		}
		if found {
			if existing.RequestHash != requestHash {
				return SubmitCommentResult{}, domain.ErrIdempotencyMismatch
			}
			existing.Result.Reused = true
			if err := tx.Commit(ctx); err != nil {
				return SubmitCommentResult{}, err
			}
			return existing.Result, nil
		}
	}

	var currentRunID uuid.UUID
	var currentStatus string
	var currentRunnerSourceID uuid.UUID
	currentErr := tx.QueryRow(ctx, `
		SELECT id, status, runner_source_id
		FROM agent_runs
		WHERE workspace_id = $1
		  AND card_id = $2
		  AND status IN ('queued', 'running', 'waiting_human')
		FOR UPDATE
	`, p.WorkspaceID, p.CardID).Scan(&currentRunID, &currentStatus, &currentRunnerSourceID)
	if currentErr != nil && !errors.Is(currentErr, pgx.ErrNoRows) {
		return SubmitCommentResult{}, currentErr
	}
	hasCurrent := currentErr == nil
	if requireWaitingHuman && (!hasCurrent || currentStatus != string(domain.RunStatusWaitingHuman)) {
		return SubmitCommentResult{}, fmt.Errorf("%w: card is not waiting for human", domain.ErrConflict)
	}
	if p.DispatchMode == domain.DispatchModeInterrupt && !hasCurrent && p.RunnerSourceID == nil {
		return SubmitCommentResult{}, fmt.Errorf("%w: interrupt without a current run requires an explicit runner", domain.ErrConflict)
	}
	if p.DispatchMode == domain.DispatchModeInterrupt && hasCurrent && p.RunnerSourceID == nil {
		// Human Web 不应提交 runner_source_id。转向当前执行时默认沿用当前 Runner，
		// 并且在 Card 锁内读取，避免 Handler 先查后写产生 TOCTOU。
		p.RunnerSourceID = &currentRunnerSourceID
	}

	eventID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id, content, idempotency_key, request_hash
		)
		VALUES ($1, $2, $3, 'comment', $4, $5, NULLIF($6, ''), $7)
	`, eventID, p.WorkspaceID, p.CardID, p.AuthorSourceID, p.Content, p.IdempotencyKey, requestHash)
	if err != nil {
		return SubmitCommentResult{}, mapConstraintError(err)
	}

	triggerID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO triggers (
			id, workspace_id, card_id, cause, cause_event_id, created_by_source_id,
			dispatch_mode, idempotency_key, request_hash
		)
		VALUES ($1, $2, $3, 'human_comment', $4, $5, $6, NULLIF($7, ''), $8)
	`, triggerID, p.WorkspaceID, p.CardID, eventID, p.AuthorSourceID, string(p.DispatchMode), p.IdempotencyKey, requestHash)
	if err != nil {
		return SubmitCommentResult{}, mapConstraintError(err)
	}

	result := SubmitCommentResult{EventID: eventID, TriggerID: triggerID}
	if p.DispatchMode == domain.DispatchModeInterrupt {
		if hasCurrent {
			if _, err := tx.Exec(ctx, `
				UPDATE agent_runs
				SET status = 'superseded',
					finished_at = now(),
					heartbeat_at = NULL,
					lease_expires_at = NULL,
					updated_at = now()
				WHERE workspace_id = $1 AND id = $2
			`, p.WorkspaceID, currentRunID); err != nil {
				return SubmitCommentResult{}, err
			}
		}
		runID, err := insertQueuedRun(ctx, tx, p.WorkspaceID, p.CardID, triggerID, *p.RunnerSourceID, p.LeaseDuration)
		if err != nil {
			return SubmitCommentResult{}, err
		}
		result.RunID = &runID
	} else if !hasCurrent && p.RunnerSourceID != nil {
		runID, err := insertQueuedRun(ctx, tx, p.WorkspaceID, p.CardID, triggerID, *p.RunnerSourceID, p.LeaseDuration)
		if err != nil {
			return SubmitCommentResult{}, err
		}
		result.RunID = &runID
	}

	if err := tx.Commit(ctx); err != nil {
		return SubmitCommentResult{}, err
	}
	return result, nil
}

func commentRequestHash(p SubmitCommentParams) string {
	runner := ""
	if p.RunnerSourceID != nil {
		runner = p.RunnerSourceID.String()
	}
	sum := sha256.Sum256([]byte(
		p.WorkspaceID.String() + "\x00" +
			p.CardID.String() + "\x00" +
			p.AuthorSourceID.String() + "\x00" +
			string(p.DispatchMode) + "\x00" +
			runner + "\x00" +
			p.Content,
	))
	return hex.EncodeToString(sum[:])
}

type existingCommentRequest struct {
	Result      SubmitCommentResult
	RequestHash string
}

func findCommentRequest(ctx context.Context, tx pgx.Tx, workspaceID, sourceID uuid.UUID, key string) (existingCommentRequest, bool, error) {
	var existing existingCommentRequest
	var runID uuid.NullUUID
	err := tx.QueryRow(ctx, `
		SELECT e.id, t.id, r.id, t.request_hash
		FROM events e
		JOIN triggers t ON t.workspace_id = e.workspace_id AND t.cause_event_id = e.id
		LEFT JOIN agent_runs r ON r.workspace_id = t.workspace_id AND r.trigger_id = t.id
		WHERE e.workspace_id = $1
		  AND e.actor_source_id = $2
		  AND e.event_type = 'comment'
		  AND e.idempotency_key = $3
	`, workspaceID, sourceID, key).Scan(
		&existing.Result.EventID,
		&existing.Result.TriggerID,
		&runID,
		&existing.RequestHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return existingCommentRequest{}, false, nil
	}
	if err != nil {
		return existingCommentRequest{}, false, err
	}
	if runID.Valid {
		existing.Result.RunID = &runID.UUID
	}
	return existing, true, nil
}
