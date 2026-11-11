package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agent"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type toolApprovalResponse struct {
	ID                string          `json:"id"`
	ToolName          string          `json:"tool_name"`
	Arguments         json.RawMessage `json:"arguments"`
	RiskLevel         string          `json:"risk_level"`
	Reversible        bool            `json:"reversible"`
	Reason            string          `json:"reason"`
	Status            string          `json:"status"`
	ResolutionComment string          `json:"resolution_comment,omitempty"`
	ResolvedAt        *time.Time      `json:"resolved_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
}

func toolApprovalFromDB(row db.CardToolApproval) *toolApprovalResponse {
	resp := &toolApprovalResponse{
		ID:                row.ID.String(),
		ToolName:          row.ToolName,
		Arguments:         json.RawMessage(row.Arguments),
		RiskLevel:         row.RiskLevel,
		Reversible:        row.Reversible,
		Reason:            row.Reason,
		Status:            row.Status,
		ResolutionComment: row.ResolutionComment,
		CreatedAt:         row.CreatedAt.Time,
	}
	if row.ResolvedAt.Valid {
		ts := row.ResolvedAt.Time
		resp.ResolvedAt = &ts
	}
	return resp
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

	if request.Action == "approve_session" {
		_ = api.store.AddCardToolWhitelist(r.Context(), identity.WorkspaceID, cardID, approval.ToolName, identity.SourceID)
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
			modeLabel = "允许执行（本卡片内同类操作不再询问）"
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

	// 自动入队新的跟进轮次，唤醒主模型继续推进
	idempotencyKey := "tool-approval-resolve-" + approvalID.String()
	commentResult, err := api.store.SubmitSessionComment(r.Context(), store.SubmitSessionCommentParams{
		WorkspaceID:    identity.WorkspaceID,
		CardID:         cardID,
		AuthorSourceID: identity.SourceID,
		Content:        followupContent,
		IdempotencyKey: idempotencyKey,
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		api.handleError(w, err)
		return
	}

	if api.live != nil {
		api.live.Publish(cardID, realtime.Event{
			Type:    "card.updated",
			EventID: commentResult.EventID.String(),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"approval": toolApprovalFromDB(resolved),
		"turn_id":  commentResult.TurnID.String(),
		"event_id": commentResult.EventID.String(),
	})
}
