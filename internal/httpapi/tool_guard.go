package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agent"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/mcpclient"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type toolApprovalResponse struct {
	ID                    string          `json:"id"`
	ToolName              string          `json:"tool_name"`
	Arguments             json.RawMessage `json:"arguments"`
	RiskLevel             string          `json:"risk_level"`
	Reversible            bool            `json:"reversible"`
	Reason                string          `json:"reason"`
	ScopeType             string          `json:"scope_type,omitempty"`
	ScopeTarget           string          `json:"scope_target,omitempty"`
	ScopeLabel            string          `json:"scope_label,omitempty"`
	AllowSessionWhitelist bool            `json:"allow_session_whitelist"`
	Status                string          `json:"status"`
	ResolutionComment     string          `json:"resolution_comment,omitempty"`
	ResolvedAt            *time.Time      `json:"resolved_at,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
}

func toolApprovalFromDB(row db.CardToolApproval) *toolApprovalResponse {
	resp := &toolApprovalResponse{
		ID:                    row.ID.String(),
		ToolName:              row.ToolName,
		Arguments:             json.RawMessage(row.Arguments),
		RiskLevel:             row.RiskLevel,
		Reversible:            row.Reversible,
		Reason:                row.Reason,
		ScopeType:             row.ScopeType,
		ScopeTarget:           row.ScopeTarget,
		ScopeLabel:            row.ScopeLabel,
		AllowSessionWhitelist: row.AllowSessionWhitelist,
		Status:                row.Status,
		ResolutionComment:     row.ResolutionComment,
		CreatedAt:             row.CreatedAt.Time,
	}
	if row.ResolvedAt.Valid {
		ts := row.ResolvedAt.Time
		resp.ResolvedAt = &ts
	}
	return resp
}

// resolveApprovalScope 解析审批记录对应的白名单作用域；若为迁移前的历史记录则按参数兜底推导。
func resolveApprovalScope(approval db.CardToolApproval) (string, string) {
	scopeType := strings.TrimSpace(approval.ScopeType)
	scopeTarget := strings.TrimSpace(approval.ScopeTarget)
	if scopeType != "" && scopeTarget != "" {
		return scopeType, scopeTarget
	}

	var args map[string]any
	_ = json.Unmarshal(approval.Arguments, &args)
	if approval.ToolName == "call_mcp_tool" {
		toolID, _ := args["tool_id"].(string)
		if serverName, _, ok := mcpclient.ParseToolID(toolID); ok && strings.TrimSpace(serverName) != "" {
			return "mcp", strings.TrimSpace(serverName)
		}
	}
	if nodeID, _ := args["node_id"].(string); strings.TrimSpace(nodeID) != "" {
		return "device", strings.TrimSpace(nodeID)
	}
	return "tool", strings.TrimSpace(approval.ToolName)
}

func (api *API) attachToolApprovals(ctx context.Context, workspaceID, cardID uuid.UUID, events []eventResponse) {
	hasToolApproval := false
	for _, ev := range events {
		if ev.Type == "tool_approval" && ev.ToolApprovalID != "" {
			hasToolApproval = true
			break
		}
	}
	if !hasToolApproval {
		return
	}

	approvals, err := api.store.ListCardToolApprovals(ctx, workspaceID, cardID)
	if err != nil {
		return
	}
	byID := make(map[string]db.CardToolApproval, len(approvals))
	for _, app := range approvals {
		byID[app.ID.String()] = app
	}
	for i := range events {
		if events[i].Type == "tool_approval" && events[i].ToolApprovalID != "" {
			if row, ok := byID[events[i].ToolApprovalID]; ok {
				events[i].ToolApproval = toolApprovalFromDB(row)
			}
		}
	}
}

// resolveToolApproval 处理用户在风险确认卡片上的三种交互操作：
// 1. approve_once: 允许本次执行并推进后续轮次
// 2. approve_session: 本卡片内同类操作不再询问（加入白名单）并执行
// 3. reject: 拒绝并补充说明反馈给主模型
func (api *API) resolveToolApproval(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	approvalID, ok := parseUUIDParam(w, r, "approvalID")
	if !ok {
		return
	}

	var request struct {
		Action  string `json:"action"`  // "approve_once", "approve_session", "reject"
		Comment string `json:"comment"` // 拒绝原因或修改建议
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Action = strings.TrimSpace(request.Action)
	request.Comment = strings.TrimSpace(request.Comment)

	switch request.Action {
	case "approve_once", "approve_session", "reject":
	default:
		writeError(w, http.StatusBadRequest, "invalid_action", "action must be approve_once, approve_session, or reject")
		return
	}

	identity := identityFromRequest(r)
	approval, err := api.store.GetToolApproval(r.Context(), identity.WorkspaceID, cardID, approvalID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	if approval.Status != "pending" {
		writeError(w, http.StatusConflict, "already_resolved", "tool approval has already been resolved")
		return
	}
	if request.Action == "approve_session" && !approval.AllowSessionWhitelist {
		writeError(w, http.StatusConflict, "session_whitelist_not_allowed", "critical destructive tool call cannot be added to card whitelist")
		return
	}

	status := "approved"
	if request.Action == "reject" {
		status = "rejected"
	}

	resolved, err := api.store.ResolveToolApproval(r.Context(), store.ResolveToolApprovalParams{
		WorkspaceID:        identity.WorkspaceID,
		CardID:             cardID,
		ApprovalID:         approvalID,
		Status:             status,
		Comment:            request.Comment,
		ResolvedBySourceID: identity.SourceID,
	})
	if err != nil {
		api.handleError(w, err)
		return
	}

	scopeType, scopeTarget := resolveApprovalScope(approval)
	if request.Action == "approve_session" {
		if err := api.store.AddCardScopeWhitelist(r.Context(), identity.WorkspaceID, cardID, scopeType, scopeTarget, identity.SourceID); err != nil {
			api.handleError(w, err)
			return
		}
	}

	var followupContent string
	if status == "rejected" {
		commentText := request.Comment
		if commentText == "" {
			commentText = "操作风险过高，请换用更安全或只读的方式推进。"
		}
		followupContent = fmt.Sprintf("【工具审批拒绝反馈】\n用户拒绝了工具 `%s` 的调用。\n拒绝原因与补充说明：%s\n请根据上述反馈调整方案继续推进。", approval.ToolName, commentText)
	} else {
		var parsedArgs map[string]any
		_ = json.Unmarshal(approval.Arguments, &parsedArgs)
		execCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		toolOutput, execErr := agent.ExecuteApprovedTool(
			execCtx,
			api.store,
			api.nexusDock,
			identity.WorkspaceID,
			approval.ToolName,
			parsedArgs,
		)
		cancel()

		modeLabel := "允许本次执行"
		if request.Action == "approve_session" {
			switch scopeType {
			case "device":
				modeLabel = "允许执行（本卡片已临时允许该设备操作）"
			case "mcp":
				modeLabel = "允许执行（本卡片已临时允许该 MCP 连接操作）"
			default:
				modeLabel = "允许执行（本卡片内同类操作不再询问）"
			}
		}
		if execErr != nil {
			followupContent = fmt.Sprintf("【工具审批通过（%s）】\n你请求调用的工具 `%s` 已获人工批准，已自动加入放行白名单或允许执行。请重新调用该工具或根据当前状态继续推进后续步骤。", modeLabel, approval.ToolName)
		} else {
			if len(toolOutput) > 6000 {
				toolOutput = toolOutput[:6000] + "\n...(truncated)"
			}
			followupContent = fmt.Sprintf("【工具审批通过并已执行（%s）】\n工具 `%s` 已获人工批准并成功执行，返回结果如下：\n```json\n%s\n```\n请基于该执行结果继续推进任务。", modeLabel, approval.ToolName, toolOutput)
		}
	}

	// 仅入队后台 System Turn 唤醒主模型继续推进，不再伪造人类 comment 聊天事件污染时间线。
	idempotencyKey := "tool-approval-resolve-" + approvalID.String()
	turnResult, err := api.store.EnqueueToolApprovalTurn(r.Context(), store.EnqueueToolApprovalTurnParams{
		WorkspaceID:    identity.WorkspaceID,
		CardID:         cardID,
		OriginTurnID:   approval.TurnID,
		Content:        followupContent,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		api.handleError(w, err)
		return
	}

	if api.live != nil {
		api.live.Publish(cardID, realtime.Event{
			Type: "card.updated",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"approval": toolApprovalFromDB(resolved),
		"turn_id":  turnResult.Turn.ID.String(),
	})
}

type deviceToolGuardResponse struct {
	ID             string    `json:"id"`
	DeviceNodeID   string    `json:"device_node_id"`
	DeviceName     string    `json:"device_name"`
	Mode           string    `json:"mode"`
	ModelRouteID   string    `json:"model_route_id,omitempty"`
	TimeoutSeconds int32     `json:"timeout_seconds"`
	CustomRules    string    `json:"custom_rules"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func deviceToolGuardFromDB(row db.WorkspaceDeviceToolGuard) deviceToolGuardResponse {
	routeID := ""
	if row.ModelRouteID.Valid {
		routeID = row.ModelRouteID.UUID.String()
	}
	return deviceToolGuardResponse{
		ID:             row.ID.String(),
		DeviceNodeID:   row.DeviceNodeID,
		DeviceName:     row.DeviceName,
		Mode:           row.Mode,
		ModelRouteID:   routeID,
		TimeoutSeconds: row.TimeoutSeconds,
		CustomRules:    row.CustomRules,
		CreatedAt:      row.CreatedAt.Time,
		UpdatedAt:      row.UpdatedAt.Time,
	}
}

func (api *API) listDeviceToolGuards(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	guards, err := api.store.ListDeviceToolGuards(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	res := make([]deviceToolGuardResponse, 0, len(guards))
	for _, g := range guards {
		res = append(res, deviceToolGuardFromDB(g))
	}
	writeJSON(w, http.StatusOK, res)
}

func (api *API) getDeviceToolGuard(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	nodeID := strings.TrimSpace(r.PathValue("nodeID"))
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "invalid_node_id", "device node_id is required")
		return
	}
	guard, err := api.store.GetDeviceToolGuard(r.Context(), identity.WorkspaceID, nodeID)
	if errors.Is(err, domain.ErrNotFound) {
		writeJSON(w, http.StatusOK, deviceToolGuardResponse{
			DeviceNodeID:   nodeID,
			Mode:           "block_high_risk",
			TimeoutSeconds: 10,
		})
		return
	}
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deviceToolGuardFromDB(guard))
}

func (api *API) saveDeviceToolGuard(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	nodeID := strings.TrimSpace(r.PathValue("nodeID"))
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "invalid_node_id", "device node_id is required")
		return
	}
	var request struct {
		DeviceNodeID   *string `json:"device_node_id"`
		DeviceName     *string `json:"device_name"`
		Mode           *string `json:"mode"`
		ModelRouteID   *string `json:"model_route_id"`
		TimeoutSeconds *int32  `json:"timeout_seconds"`
		CustomRules    *string `json:"custom_rules"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	// 前端按 DeviceToolGuard 类型契约提交时会在 body 中携带 device_node_id；
	// decodeJSON 开启了 DisallowUnknownFields，因此必须接收该字段并校验与路径一致。
	if request.DeviceNodeID != nil {
		bodyNodeID := strings.TrimSpace(*request.DeviceNodeID)
		if bodyNodeID != "" && bodyNodeID != nodeID {
			writeError(w, http.StatusBadRequest, "invalid_node_id", "device_node_id must match URL nodeID")
			return
		}
	}
	deviceName := ""
	if request.DeviceName != nil {
		deviceName = strings.TrimSpace(*request.DeviceName)
	}
	mode := "disabled"
	if request.Mode != nil && strings.TrimSpace(*request.Mode) != "" {
		mode = strings.TrimSpace(*request.Mode)
	}
	switch mode {
	case "disabled", "block_high_risk", "strict":
	default:
		writeError(w, http.StatusBadRequest, "invalid_mode", "mode must be disabled, block_high_risk, or strict")
		return
	}
	var routeID *uuid.UUID
	if request.ModelRouteID != nil {
		rawRouteID := strings.TrimSpace(*request.ModelRouteID)
		if rawRouteID != "" {
			id, err := uuid.Parse(rawRouteID)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_model_route_id", "model_route_id must be a valid UUID")
				return
			}
			routeID = &id
		}
	}
	var timeoutSeconds int32
	if request.TimeoutSeconds != nil {
		timeoutSeconds = *request.TimeoutSeconds
	}
	customRules := ""
	if request.CustomRules != nil {
		customRules = *request.CustomRules
	}
	guard, err := api.store.SaveDeviceToolGuard(r.Context(), identity.WorkspaceID, store.DeviceToolGuardInput{
		DeviceNodeID:   nodeID,
		DeviceName:     deviceName,
		Mode:           mode,
		ModelRouteID:   routeID,
		TimeoutSeconds: timeoutSeconds,
		CustomRules:    customRules,
	})
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deviceToolGuardFromDB(guard))
}

func (api *API) deleteDeviceToolGuard(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	nodeID := strings.TrimSpace(r.PathValue("nodeID"))
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "invalid_node_id", "device node_id is required")
		return
	}
	err := api.store.DeleteDeviceToolGuard(r.Context(), identity.WorkspaceID, nodeID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
