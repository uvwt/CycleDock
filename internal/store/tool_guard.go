package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/agentdata"
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

type DeviceToolGuardInput struct {
	DeviceNodeID   string
	DeviceName     string
	Mode           string
	ModelRouteID   *uuid.UUID
	TimeoutSeconds int32
	CustomRules    string
}

func toNullableUUID(u *uuid.UUID) uuid.NullUUID {
	if u == nil || *u == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *u, Valid: true}
}

// GetDeviceToolGuard 获取指定设备的 Tool Guard 配置
func (s *Store) GetDeviceToolGuard(ctx context.Context, workspaceID uuid.UUID, nodeID string) (db.WorkspaceDeviceToolGuard, error) {
	guard, err := s.queries.GetWorkspaceDeviceToolGuard(ctx, db.GetWorkspaceDeviceToolGuardParams{
		WorkspaceID:  workspaceID,
		DeviceNodeID: strings.TrimSpace(nodeID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WorkspaceDeviceToolGuard{}, domain.ErrNotFound
	}
	return guard, err
}

// ListDeviceToolGuards 列出工作区所有设备的 Tool Guard 配置
func (s *Store) ListDeviceToolGuards(ctx context.Context, workspaceID uuid.UUID) ([]db.WorkspaceDeviceToolGuard, error) {
	return s.queries.ListWorkspaceDeviceToolGuards(ctx, workspaceID)
}

// SaveDeviceToolGuard 保存或更新指定设备的 Tool Guard 配置
func (s *Store) SaveDeviceToolGuard(ctx context.Context, workspaceID uuid.UUID, input DeviceToolGuardInput) (db.WorkspaceDeviceToolGuard, error) {
	nodeID := strings.TrimSpace(input.DeviceNodeID)
	if nodeID == "" {
		return db.WorkspaceDeviceToolGuard{}, fmt.Errorf("%w: device_node_id is required", domain.ErrConflict)
	}
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = "disabled"
	}
	switch mode {
	case "disabled", "block_high_risk", "strict":
	default:
		return db.WorkspaceDeviceToolGuard{}, fmt.Errorf("%w: invalid mode %q", domain.ErrConflict, mode)
	}
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 10
	} else if timeout > 60 {
		timeout = 60
	}
	routeID := toNullableUUID(input.ModelRouteID)

	return s.queries.UpsertWorkspaceDeviceToolGuard(ctx, db.UpsertWorkspaceDeviceToolGuardParams{
		ID:             uuid.New(),
		WorkspaceID:    workspaceID,
		DeviceNodeID:   nodeID,
		DeviceName:     strings.TrimSpace(input.DeviceName),
		Mode:           mode,
		ModelRouteID:   routeID,
		TimeoutSeconds: timeout,
		CustomRules:    strings.TrimSpace(input.CustomRules),
	})
}

// DeleteDeviceToolGuard 删除指定设备的 Tool Guard 配置
func (s *Store) DeleteDeviceToolGuard(ctx context.Context, workspaceID uuid.UUID, nodeID string) error {
	_, err := s.queries.DeleteWorkspaceDeviceToolGuard(ctx, db.DeleteWorkspaceDeviceToolGuardParams{
		WorkspaceID:  workspaceID,
		DeviceNodeID: strings.TrimSpace(nodeID),
	})
	return err
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

type EnqueueToolApprovalTurnParams struct {
	WorkspaceID    uuid.UUID
	CardID         uuid.UUID
	OriginTurnID   uuid.NullUUID
	Content        string
	IdempotencyKey string
}

// EnqueueToolApprovalTurn 在工具人工审批完成后入队后台 System Turn 唤醒主模型继续推进。
// 该轮次继承原挂起轮次的设备目标与优先模型路由，但不生成人类 comment 事件，避免污染聊天时间线。
func (s *Store) EnqueueToolApprovalTurn(
	ctx context.Context,
	p EnqueueToolApprovalTurnParams,
) (CreateTurnResult, error) {
	p.Content, _ = agentdata.NormalizeText(strings.TrimSpace(p.Content))
	p.IdempotencyKey, _ = agentdata.NormalizeText(strings.TrimSpace(p.IdempotencyKey))
	if p.WorkspaceID == uuid.Nil || p.CardID == uuid.Nil {
		return CreateTurnResult{}, fmt.Errorf("%w: workspace_id and card_id are required", domain.ErrConflict)
	}
	if p.Content == "" {
		return CreateTurnResult{}, fmt.Errorf("%w: turn content is required", domain.ErrConflict)
	}
	if p.IdempotencyKey == "" {
		return CreateTurnResult{}, fmt.Errorf("%w: idempotency_key is required", domain.ErrConflict)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateTurnResult{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return CreateTurnResult{}, err
	}
	if err := ensureDiscussionAcceptsWork(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return CreateTurnResult{}, err
	}

	cycleAgentID, err := ensureCycleAgentSource(ctx, tx, p.WorkspaceID)
	if err != nil {
		return CreateTurnResult{}, err
	}

	requestHash := stableRequestHash(
		"tool_approval_turn",
		p.WorkspaceID.String(),
		p.CardID.String(),
		cycleAgentID.String(),
		p.IdempotencyKey,
		p.Content,
	)
	if existing, found, err := findTurnByIdempotency(ctx, tx, p.WorkspaceID, cycleAgentID, p.IdempotencyKey); err != nil {
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

	var deviceNodeID, deviceName string
	var preferredRouteID uuid.NullUUID
	if p.OriginTurnID.Valid {
		err := tx.QueryRow(ctx, `
			SELECT device_node_id, device_name, preferred_model_route_id
			FROM card_turns
			WHERE workspace_id = $1 AND card_id = $2 AND id = $3
		`, p.WorkspaceID, p.CardID, p.OriginTurnID.UUID).Scan(&deviceNodeID, &deviceName, &preferredRouteID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return CreateTurnResult{}, err
		}
	}

	turn, err := insertTurn(ctx, tx, insertTurnParams{
		DeviceNodeID:          deviceNodeID,
		DeviceName:            deviceName,
		PreferredModelRouteID: preferredRouteID,
		WorkspaceID:           p.WorkspaceID,
		CardID:                p.CardID,
		Source:                domain.TurnSourceSystem,
		Content:               p.Content,
		CreatedBySourceID:     uuid.NullUUID{UUID: cycleAgentID, Valid: true},
		DispatchMode:          domain.DispatchModeEnqueue,
		IdempotencyKey:        p.IdempotencyKey,
		RequestHash:           requestHash,
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
