package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type CreateToolApprovalParams struct {
	WorkspaceID uuid.UUID
	CardID      uuid.UUID
	TurnID      *uuid.UUID
	AttemptID   *uuid.UUID
	ToolCallID  string
	ToolName    string
	Arguments   map[string]any
	RiskLevel   string // "low", "medium", "high", "critical"
	Reversible  bool
	Reason      string
}

type ToolApproval struct {
	db.CardToolApproval
}

// CreateToolApproval 创建一条持久化的工具待确认记录
func (s *Store) CreateToolApproval(ctx context.Context, p CreateToolApprovalParams) (db.CardToolApproval, error) {
	if p.WorkspaceID == uuid.Nil || p.CardID == uuid.Nil {
		return db.CardToolApproval{}, fmt.Errorf("%w: workspace_id and card_id are required", domain.ErrConflict)
	}
	if strings.TrimSpace(p.ToolName) == "" {
		return db.CardToolApproval{}, fmt.Errorf("%w: tool_name is required", domain.ErrConflict)
	}
	riskLevel := strings.ToLower(strings.TrimSpace(p.RiskLevel))
	switch riskLevel {
	case "low", "medium", "high", "critical":
	default:
		riskLevel = "high"
	}
	argsRaw, err := json.Marshal(p.Arguments)
	if err != nil {
		argsRaw = []byte("{}")
	}

	id := uuid.New()
	var turnID uuid.NullUUID
	if p.TurnID != nil && *p.TurnID != uuid.Nil {
		turnID = uuid.NullUUID{UUID: *p.TurnID, Valid: true}
	}
	var attemptID uuid.NullUUID
	if p.AttemptID != nil && *p.AttemptID != uuid.Nil {
		attemptID = uuid.NullUUID{UUID: *p.AttemptID, Valid: true}
	}

	return s.queries.CreateCardToolApproval(ctx, db.CreateCardToolApprovalParams{
		ID:          id,
		WorkspaceID: p.WorkspaceID,
		CardID:      p.CardID,
		TurnID:      turnID,
		AttemptID:   attemptID,
		ToolCallID:  strings.TrimSpace(p.ToolCallID),
		ToolName:    strings.TrimSpace(p.ToolName),
		Arguments:   argsRaw,
		RiskLevel:   riskLevel,
		Reversible:  p.Reversible,
		Reason:      strings.TrimSpace(p.Reason),
	})
}

// GetToolApproval 查询单个工具审批记录
func (s *Store) GetToolApproval(ctx context.Context, workspaceID, cardID, approvalID uuid.UUID) (db.CardToolApproval, error) {
	approval, err := s.queries.GetCardToolApproval(ctx, db.GetCardToolApprovalParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
		ID:          approvalID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.CardToolApproval{}, domain.ErrNotFound
	}
	return approval, err
}

// ListCardToolApprovals 查询卡片下的所有工具审批记录
func (s *Store) ListCardToolApprovals(ctx context.Context, workspaceID, cardID uuid.UUID) ([]db.CardToolApproval, error) {
	return s.queries.ListCardToolApprovals(ctx, db.ListCardToolApprovalsParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
	})
}

// ResolveToolApprovalParams 解决审批参数
type ResolveToolApprovalParams struct {
	WorkspaceID        uuid.UUID
	CardID             uuid.UUID
	ApprovalID         uuid.UUID
	Status             string // "approved" | "rejected"
	Comment            string
	ResolvedBySourceID uuid.UUID
}

// ResolveToolApproval 解决工具审批（允许或拒绝）
func (s *Store) ResolveToolApproval(ctx context.Context, p ResolveToolApprovalParams) (db.CardToolApproval, error) {
	if p.Status != "approved" && p.Status != "rejected" {
		return db.CardToolApproval{}, fmt.Errorf("%w: status must be approved or rejected", domain.ErrConflict)
	}

	var sourceID uuid.NullUUID
	if p.ResolvedBySourceID != uuid.Nil {
		sourceID = uuid.NullUUID{UUID: p.ResolvedBySourceID, Valid: true}
	}

	approval, err := s.queries.ResolveCardToolApproval(ctx, db.ResolveCardToolApprovalParams{
		WorkspaceID:        p.WorkspaceID,
		CardID:             p.CardID,
		ID:                 p.ApprovalID,
		Status:             p.Status,
		ResolutionComment:  strings.TrimSpace(p.Comment),
		ResolvedBySourceID: sourceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.CardToolApproval{}, domain.ErrNotFound
	}
	return approval, err
}

// AddCardToolWhitelist 为卡片添加同类工具白名单规则
func (s *Store) AddCardToolWhitelist(ctx context.Context, workspaceID, cardID uuid.UUID, toolName string, sourceID uuid.UUID) error {
	var src uuid.NullUUID
	if sourceID != uuid.Nil {
		src = uuid.NullUUID{UUID: sourceID, Valid: true}
	}
	_, err := s.queries.CreateCardToolWhitelist(ctx, db.CreateCardToolWhitelistParams{
		ID:                 uuid.New(),
		WorkspaceID:        workspaceID,
		CardID:             cardID,
		ToolName:           strings.TrimSpace(toolName),
		Pattern:            "*",
		CreatedBySourceID: src,
	})
	return err
}

// IsCardToolWhitelisted 检查工具是否在该卡片的白名单中
func (s *Store) IsCardToolWhitelisted(ctx context.Context, workspaceID, cardID uuid.UUID, toolName string) (bool, error) {
	return s.queries.CheckCardToolWhitelisted(ctx, db.CheckCardToolWhitelistedParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
		ToolName:    strings.TrimSpace(toolName),
	})
}

type PublishToolApprovalEventParams struct {
	WorkspaceID    uuid.UUID
	CardID         uuid.UUID
	AttemptID      uuid.UUID
	RunnerSourceID uuid.UUID
	ApprovalID     uuid.UUID
	Content        string
}

// PublishToolApprovalEvent 发布一条 tool_approval 类型的卡片事件
func (s *Store) PublishToolApprovalEvent(ctx context.Context, p PublishToolApprovalEventParams) (uuid.UUID, error) {
	if p.WorkspaceID == uuid.Nil || p.CardID == uuid.Nil || p.ApprovalID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: workspace_id, card_id and approval_id are required", domain.ErrConflict)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return uuid.Nil, err
	}

	eventID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id,
			attempt_id, content, tool_approval_id
		)
		VALUES ($1, $2, $3, 'tool_approval', $4, $5, $6, $7)
	`, eventID, p.WorkspaceID, p.CardID, p.RunnerSourceID, p.AttemptID, p.Content, p.ApprovalID)
	if err != nil {
		return uuid.Nil, mapConstraintError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	s.notifyWorkReady()
	return eventID, nil
}
