package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestResolveToolApproval_EnqueuesSilentSystemTurnWithoutHumanComment(t *testing.T) {
	handler, pool, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var identity identityResponse
	decodeRecorder(t, login, &identity)
	workspaceID := uuid.MustParse(identity.Workspace.ID)
	humanSourceID := uuid.MustParse(identity.Source.ID)

	ctx := context.Background()
	project, err := dataStore.CreateProject(ctx, workspaceID, "Guard Approval Test", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, humanSourceID)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 用户发起携带设备偏好的初始轮次
	origin, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID:    workspaceID,
		CardID:         card.ID,
		AuthorSourceID: humanSourceID,
		Content:        "清理 RN Server 旧容器日志",
		IdempotencyKey: "origin-comment-1",
		DispatchMode:   domain.DispatchModeEnqueue,
		DeviceTarget:   &store.DeviceTarget{NodeID: "node-rn", Name: "RN Server"},
	})
	if err != nil {
		t.Fatalf("submit origin comment: %v", err)
	}

	// 2. 创建一条挂起的工具审批记录（携带设备作用域信息）
	approval, err := dataStore.CreateToolApproval(ctx, store.CreateToolApprovalParams{
		WorkspaceID: workspaceID,
		CardID:      card.ID,
		TurnID:      &origin.TurnID,
		ToolCallID:  "call-1",
		ToolName:    "exec_command",
		Arguments:   map[string]any{"node_id": "node-rn", "cmd": "rm -rf /var/log/old"},
		RiskLevel:   "high",
		Reversible:  false,
		Reason:      "涉及删除日志文件",
		ScopeType:   "device",
		ScopeTarget: "node-rn",
		ScopeLabel:  "RN Server",
	})
	if err != nil {
		t.Fatalf("create tool approval: %v", err)
	}

	// 挂起期间 Attempt 已结束，焦点列表只能依赖卡片列表返回的待确认数量。
	if got := pendingToolApprovalCountInList(t, handler, cookie, project.ID, card.ID); got != 1 {
		t.Fatalf("expected pending_tool_approval_count 1 before resolve, got %d", got)
	}

	var beforeCommentCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM events WHERE workspace_id = $1 AND card_id = $2 AND event_type = 'comment'
	`, workspaceID, card.ID).Scan(&beforeCommentCount); err != nil {
		t.Fatal(err)
	}

	// 3. 用户点击确认（本卡片同类不再询问）
	resolvedRec := doJSON(
		t,
		handler,
		http.MethodPost,
		"/api/v1/cards/"+card.ID.String()+"/tool-approvals/"+approval.ID.String()+"/resolve",
		map[string]any{"action": "approve_session"},
		cookie,
		nil,
	)
	if resolvedRec.Code != http.StatusOK {
		t.Fatalf("resolve tool approval: %d %s", resolvedRec.Code, resolvedRec.Body.String())
	}

	var resolvedResp struct {
		Approval toolApprovalResponse `json:"approval"`
		TurnID   string               `json:"turn_id"`
	}
	decodeRecorder(t, resolvedRec, &resolvedResp)
	if resolvedResp.Approval.Status != "approved" {
		t.Fatalf("expected status approved, got %q", resolvedResp.Approval.Status)
	}
	if resolvedResp.Approval.ScopeType != "device" || resolvedResp.Approval.ScopeTarget != "node-rn" || resolvedResp.Approval.ScopeLabel != "RN Server" || !resolvedResp.Approval.AllowSessionWhitelist {
		t.Fatalf("unexpected scoped approval response: %+v", resolvedResp.Approval)
	}
	whitelisted, err := dataStore.IsCardScopeWhitelisted(ctx, workspaceID, card.ID, "device", "node-rn")
	if err != nil || !whitelisted {
		t.Fatalf("expected device node-rn to be whitelisted on card, got whitelisted=%v err=%v", whitelisted, err)
	}
	followupTurnID := uuid.MustParse(resolvedResp.TurnID)

	if got := pendingToolApprovalCountInList(t, handler, cookie, project.ID, card.ID); got != 0 {
		t.Fatalf("expected pending_tool_approval_count 0 after resolve, got %d", got)
	}

	// 4. 断言未新增任何伪造的人类 comment 气泡事件
	var afterCommentCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM events WHERE workspace_id = $1 AND card_id = $2 AND event_type = 'comment'
	`, workspaceID, card.ID).Scan(&afterCommentCount); err != nil {
		t.Fatal(err)
	}
	if afterCommentCount != beforeCommentCount {
		t.Fatalf("expected comment count unchanged (%d), got %d", beforeCommentCount, afterCommentCount)
	}

	// 5. 断言后台唤醒 Turn 为 system 来源，且继承了原轮次的设备目标
	var turnSource, deviceNodeID, deviceName, turnContent string
	if err := pool.QueryRow(ctx, `
		SELECT source, device_node_id, device_name, content
		FROM card_turns
		WHERE workspace_id = $1 AND card_id = $2 AND id = $3
	`, workspaceID, card.ID, followupTurnID).Scan(&turnSource, &deviceNodeID, &deviceName, &turnContent); err != nil {
		t.Fatalf("load followup turn: %v", err)
	}
	if turnSource != string(domain.TurnSourceSystem) {
		t.Fatalf("expected system turn source, got %q", turnSource)
	}
	if deviceNodeID != "node-rn" || deviceName != "RN Server" {
		t.Fatalf("expected inherited device target node-rn / RN Server, got %q / %q", deviceNodeID, deviceName)
	}
	if !strings.Contains(turnContent, "exec_command") {
		t.Fatalf("expected followup prompt to mention exec_command, got %q", turnContent)
	}

	// 6. 断言卡片详情接口不会将内部唤醒 Turn 暴露到前端待执行队列
	cardRec := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID.String(), nil, cookie, nil)
	if cardRec.Code != http.StatusOK {
		t.Fatalf("get card: %d %s", cardRec.Code, cardRec.Body.String())
	}
	var detail struct {
		PendingTurns []pendingTurnResponse `json:"pending_turns"`
	}
	decodeRecorder(t, cardRec, &detail)
	for _, pt := range detail.PendingTurns {
		if pt.ID == followupTurnID.String() {
			t.Fatalf("internal tool-approval followup turn %s should be hidden from pending_turns", pt.ID)
		}
	}
}

func pendingToolApprovalCountInList(t *testing.T, handler http.Handler, cookie *http.Cookie, projectID, cardID uuid.UUID) int64 {
	t.Helper()
	rec := doJSON(t, handler, http.MethodGet, "/api/v1/projects/"+projectID.String()+"/cards", nil, cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list project cards: %d %s", rec.Code, rec.Body.String())
	}
	var cards []cardResponse
	decodeRecorder(t, rec, &cards)
	for _, item := range cards {
		if item.ID != cardID.String() {
			continue
		}
		if item.PendingToolApprovalCount == nil {
			t.Fatalf("card %s summary is missing pending_tool_approval_count", cardID)
		}
		return *item.PendingToolApprovalCount
	}
	t.Fatalf("card %s not found in project card list", cardID)
	return 0
}

func TestResolveToolApproval_CriticalRedlineRejectsSessionWhitelistAndMCPSavesScope(t *testing.T) {
	handler, _, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var identity identityResponse
	decodeRecorder(t, login, &identity)
	workspaceID := uuid.MustParse(identity.Workspace.ID)
	humanSourceID := uuid.MustParse(identity.Source.ID)

	ctx := context.Background()
	project, err := dataStore.CreateProject(ctx, workspaceID, "Guard Scope HTTP Test", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, humanSourceID)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 极高危硬红线（allow_session_whitelist=false）禁止通过 API 加入卡片白名单
	disallow := false
	critApproval, err := dataStore.CreateToolApproval(ctx, store.CreateToolApprovalParams{
		WorkspaceID:           workspaceID,
		CardID:                card.ID,
		ToolCallID:            "call-crit",
		ToolName:              "exec_command",
		Arguments:             map[string]any{"node_id": "node-rn", "cmd": "rm -rf /"},
		RiskLevel:             "critical",
		Reversible:            false,
		Reason:                "包含高危破坏性指令",
		ScopeType:             "device",
		ScopeTarget:           "node-rn",
		ScopeLabel:            "RN Server",
		AllowSessionWhitelist: &disallow,
	})
	if err != nil {
		t.Fatal(err)
	}

	critReject := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID.String()+"/tool-approvals/"+critApproval.ID.String()+"/resolve",
		map[string]any{"action": "approve_session"},
		cookie, nil,
	)
	if critReject.Code != http.StatusConflict {
		t.Fatalf("expected 409 for approve_session on critical redline, got %d %s", critReject.Code, critReject.Body.String())
	}

	// 2. MCP 审批点击 approve_session 后应写入 (mcp, github) 白名单
	mcpApproval, err := dataStore.CreateToolApproval(ctx, store.CreateToolApprovalParams{
		WorkspaceID: workspaceID,
		CardID:      card.ID,
		ToolCallID:  "call-mcp",
		ToolName:    "call_mcp_tool",
		Arguments:   map[string]any{"tool_id": "github:create_issue"},
		RiskLevel:   "medium",
		Reversible:  false,
		Reason:      "创建外部 Issue",
		ScopeType:   "mcp",
		ScopeTarget: "github",
		ScopeLabel:  "github",
	})
	if err != nil {
		t.Fatal(err)
	}

	mcpRec := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID.String()+"/tool-approvals/"+mcpApproval.ID.String()+"/resolve",
		map[string]any{"action": "approve_session"},
		cookie, nil,
	)
	if mcpRec.Code != http.StatusOK {
		t.Fatalf("resolve mcp tool approval: %d %s", mcpRec.Code, mcpRec.Body.String())
	}
	whitelisted, err := dataStore.IsCardScopeWhitelisted(ctx, workspaceID, card.ID, "mcp", "github")
	if err != nil || !whitelisted {
		t.Fatalf("expected mcp github to be whitelisted on card, got whitelisted=%v err=%v", whitelisted, err)
	}
}
