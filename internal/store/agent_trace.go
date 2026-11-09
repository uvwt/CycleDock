package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type RecordModelCallParams struct {
	ID                    uuid.UUID
	WorkspaceID           uuid.UUID
	CardID                uuid.UUID
	TurnID                uuid.UUID
	AttemptID             uuid.UUID
	InvocationID          string
	CallIndex             int
	Purpose               string
	ModelRouteID          uuid.NullUUID
	ModelID               string
	Provider              string
	StartedAt             time.Time
	FinishedAt            time.Time
	DurationMS            int
	Status                string
	InputTokens           int
	OutputTokens          int
	CachedInputTokens     int
	ReasoningOutputTokens int
	UsageSource           string
	ErrorCode             string
	ErrorSummary          string
}

// RecordModelCall 记录一次独立的模型调用执行事实，用于 Agent Trace 诊断分析。
func (s *Store) RecordModelCall(ctx context.Context, params RecordModelCallParams) (db.AgentModelCall, error) {
	if params.ID == uuid.Nil {
		params.ID = uuid.New()
	}
	if params.StartedAt.IsZero() {
		params.StartedAt = time.Now().UTC()
	}
	if params.FinishedAt.IsZero() {
		params.FinishedAt = params.StartedAt
	}
	if params.DurationMS < 0 {
		params.DurationMS = 0
	}
	if params.UsageSource == "" {
		params.UsageSource = "unknown"
	}
	if params.Status == "" {
		params.Status = "success"
	}

	return s.queries.RecordAgentModelCall(ctx, db.RecordAgentModelCallParams{
		ID:                    params.ID,
		WorkspaceID:           params.WorkspaceID,
		CardID:                params.CardID,
		TurnID:                params.TurnID,
		AttemptID:             params.AttemptID,
		InvocationID:          params.InvocationID,
		CallIndex:             int32(params.CallIndex),
		Purpose:               params.Purpose,
		ModelRouteID:          params.ModelRouteID,
		ModelID:               params.ModelID,
		Provider:              params.Provider,
		StartedAt:             pgtype.Timestamptz{Time: params.StartedAt.UTC(), Valid: true},
		FinishedAt:            pgtype.Timestamptz{Time: params.FinishedAt.UTC(), Valid: true},
		DurationMs:            int32(params.DurationMS),
		Status:                params.Status,
		InputTokens:           int32(params.InputTokens),
		OutputTokens:          int32(params.OutputTokens),
		CachedInputTokens:     int32(params.CachedInputTokens),
		ReasoningOutputTokens: int32(params.ReasoningOutputTokens),
		UsageSource:           params.UsageSource,
		ErrorCode:             params.ErrorCode,
		ErrorSummary:          params.ErrorSummary,
	})
}

// ListAttemptModelCalls 按时间顺序获取一次 Attempt 关联的所有模型调用记录。
func (s *Store) ListAttemptModelCalls(
	ctx context.Context,
	workspaceID, cardID, attemptID uuid.UUID,
) ([]db.AgentModelCall, error) {
	// 先检查 card 与 attempt 归属
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM turn_attempts
			WHERE workspace_id = $1 AND card_id = $2 AND id = $3
		)
	`, workspaceID, cardID, attemptID).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check attempt existence: %w", err)
	}
	if !exists {
		return nil, domain.ErrNotFound
	}

	return s.queries.ListAttemptModelCalls(ctx, db.ListAttemptModelCallsParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
		AttemptID:   attemptID,
	})
}

// GetAttemptWithCard 校验并获取 Attempt 及其对应的 Card 基础信息。
func (s *Store) GetAttemptWithCard(
	ctx context.Context,
	workspaceID, cardID, attemptID uuid.UUID,
) (TurnAttempt, error) {
	attempt, err := s.GetAttempt(ctx, workspaceID, attemptID)
	if err != nil {
		return TurnAttempt{}, err
	}
	if attempt.CardID != cardID {
		return TurnAttempt{}, domain.ErrNotFound
	}
	return attempt, nil
}

