package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func newBlockingGuardModelServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"reversible": false, "risk_level": "high", "decision": "ask_human", "reason": "存在潜在变更风险，需人工确认"}`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestToolGuard_ScopedCardWhitelistIntegration(t *testing.T) {
	dataStore, _ := newAgentIntegrationStore(t)
	ctx := context.Background()

	workspace, err := dataStore.CreateWorkspace(ctx, "Scoped Whitelist Test")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Project", "")
	if err != nil {
		t.Fatal(err)
	}
	cardA, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Card A", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	cardB, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Card B", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	guardHTTP := newBlockingGuardModelServer(t)
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(guardHTTP.URL, "guard-model"))
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Routes) == 0 {
		t.Fatal("expected at least one model route")
	}
	routeID := settings.Routes[0].ID

	// 配置工作区全局工具守卫为 strict，再单独开启两台设备与两个 MCP 连接的开关
	if _, err := dataStore.SaveToolGuardSettings(ctx, workspace.ID, store.ToolGuardSettingsInput{
		Mode:           "strict",
		ModelRouteID:   &routeID,
		TimeoutSeconds: 5,
	}); err != nil {
		t.Fatal(err)
	}
	settings, err = dataStore.GetAgentSettings(ctx, workspace.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := dataStore.SaveDeviceToolGuard(ctx, workspace.ID, store.DeviceToolGuardInput{
		DeviceNodeID: "node-tianyi",
		DeviceName:   "TianYi",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveDeviceToolGuard(ctx, workspace.ID, store.DeviceToolGuardInput{
		DeviceNodeID: "node-rn",
		DeviceName:   "RN Server",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name:             "github",
		URL:              "https://mcp.example.com/github",
		Enabled:          true,
		TimeoutMS:        5000,
		ToolGuardEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name:             "dida365",
		URL:              "https://mcp.example.com/dida365",
		Enabled:          true,
		TimeoutMS:        5000,
		ToolGuardEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	client := modelclient.NewWithHTTPClient(guardHTTP.Client())
	deviceNames := map[string]string{
		"node-tianyi": "TianYi",
		"node-rn":     "RN Server",
	}

	// 1. 在 Card A 上临时允许设备 node-tianyi
	if err := dataStore.AddCardScopeWhitelist(ctx, workspace.ID, cardA.ID, "device", "node-tianyi", human.ID); err != nil {
		t.Fatalf("add device whitelist: %v", err)
	}
	// 重复写入应幂等成功
	if err := dataStore.AddCardScopeWhitelist(ctx, workspace.ID, cardA.ID, "device", "node-tianyi", human.ID); err != nil {
		t.Fatalf("duplicate device whitelist should be idempotent: %v", err)
	}

	// 1a. Card A 调用 node-tianyi 上的其他工具（如 file_edit、browser_act、exec_command）应自动放行
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"exec_command", map[string]any{"node_id": "node-tianyi", "cmd": "systemctl restart app"}},
		{"file_edit", map[string]any{"action": "replace", "path": "C:\\app\\main.go"}}, // 继承 cardDeviceNodeID
		{"browser_act", map[string]any{"node_id": "node-tianyi", "actions": []any{}}},
	} {
		dec := EvaluateToolGuard(
			ctx, dataStore, client, nil, settings,
			workspace.ID, cardA.ID, "node-tianyi", deviceNames,
			"Card A", "", tc.tool, tc.args,
		)
		if dec.Decision != "allow" {
			t.Fatalf("expected %s on whitelisted device node-tianyi to be allowed, got %+v", tc.tool, dec)
		}
	}

	// 1b. Card A 调用其他设备 node-rn 仍需拦截，且带出正确的设备作用域信息
	otherDeviceDec := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, cardA.ID, "node-tianyi", deviceNames,
		"Card A", "", "exec_command", map[string]any{"node_id": "node-rn", "cmd": "systemctl restart app"},
	)
	if otherDeviceDec.Decision != "ask_human" {
		t.Fatalf("expected node-rn to remain blocked, got %+v", otherDeviceDec)
	}
	if otherDeviceDec.ScopeType != "device" || otherDeviceDec.ScopeTarget != "node-rn" || otherDeviceDec.ScopeLabel != "RN Server" || !otherDeviceDec.AllowSessionWhitelist {
		t.Fatalf("unexpected scope metadata for node-rn: %+v", otherDeviceDec)
	}

	// 1c. Card A 上已被允许的设备 node-tianyi 执行极高危硬红线命令（如 rm -rf /）仍必须强制拦截，且禁止加入白名单
	critDec := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, cardA.ID, "node-tianyi", deviceNames,
		"Card A", "", "exec_command", map[string]any{"node_id": "node-tianyi", "cmd": "sudo rm -rf / --no-preserve-root"},
	)
	if critDec.Decision != "ask_human" || critDec.RiskLevel != "critical" || critDec.AllowSessionWhitelist {
		t.Fatalf("expected critical redline command on whitelisted device to still ask_human with allow_session_whitelist=false, got %+v", critDec)
	}
	critDBDec := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, cardA.ID, "node-tianyi", deviceNames,
		"Card A", "", "exec_command", map[string]any{"node_id": "node-tianyi", "cmd": `psql -c "DROP DATABASE production;"`},
	)
	if critDBDec.Decision != "ask_human" || critDBDec.RiskLevel != "critical" || critDBDec.AllowSessionWhitelist {
		t.Fatalf("expected critical drop database command on whitelisted device to still ask_human with allow_session_whitelist=false, got %+v", critDBDec)
	}

	// 2. Card A 允许 device 后不应放行 MCP 工具；为 Card A 单独允许 github MCP 后，仅放行 github 而不放行 dida365
	mcpBefore := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, cardA.ID, "node-tianyi", deviceNames,
		"Card A", "", "call_mcp_tool", map[string]any{"tool_id": "github:create_issue"},
	)
	if mcpBefore.Decision != "ask_human" || mcpBefore.ScopeType != "mcp" || mcpBefore.ScopeTarget != "github" || mcpBefore.ScopeLabel != "github" {
		t.Fatalf("expected github MCP to require approval before MCP whitelist, got %+v", mcpBefore)
	}

	if err := dataStore.AddCardScopeWhitelist(ctx, workspace.ID, cardA.ID, "mcp", "github", human.ID); err != nil {
		t.Fatalf("add mcp whitelist: %v", err)
	}

	// 2a. 同卡片同 MCP 其他工具（大小写不敏感匹配）应自动放行
	mcpSame := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, cardA.ID, "node-tianyi", deviceNames,
		"Card A", "", "call_mcp_tool", map[string]any{"tool_id": "GitHub:merge_pull_request"},
	)
	if mcpSame.Decision != "allow" {
		t.Fatalf("expected same MCP connection github to be allowed, got %+v", mcpSame)
	}

	// 2b. 同卡片其他 MCP（dida365）仍需拦截
	mcpOther := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, cardA.ID, "node-tianyi", deviceNames,
		"Card A", "", "call_mcp_tool", map[string]any{"tool_id": "dida365:create_task"},
	)
	if mcpOther.Decision != "ask_human" || mcpOther.ScopeType != "mcp" || mcpOther.ScopeTarget != "dida365" {
		t.Fatalf("expected other MCP connection dida365 to remain blocked, got %+v", mcpOther)
	}

	// 3. 跨卡片不串权：Card B 对 node-tianyi 与 github 均仍拦截
	cardBDevice := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, cardB.ID, "node-tianyi", deviceNames,
		"Card B", "", "exec_command", map[string]any{"node_id": "node-tianyi", "cmd": "systemctl restart app"},
	)
	if cardBDevice.Decision != "ask_human" {
		t.Fatalf("expected Card B device node-tianyi NOT to inherit Card A whitelist, got %+v", cardBDevice)
	}
	cardBMCP := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, cardB.ID, "node-tianyi", deviceNames,
		"Card B", "", "call_mcp_tool", map[string]any{"tool_id": "github:create_issue"},
	)
	if cardBMCP.Decision != "ask_human" {
		t.Fatalf("expected Card B MCP github NOT to inherit Card A whitelist, got %+v", cardBMCP)
	}
}

func TestToolGuard_TogglePrecedenceAndSingleCriticalApprovalIntegration(t *testing.T) {
	dataStore, _ := newAgentIntegrationStore(t)
	ctx := context.Background()

	workspace, err := dataStore.CreateWorkspace(ctx, "Guard Toggle & Single Approval Test")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Project", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Guard Flow Card", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	guardModelCalls := 0
	guardHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guardModelCalls++
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"reversible": false, "risk_level": "high", "decision": "ask_human", "reason": "模型判定需人工确认"}`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer guardHTTP.Close()

	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(guardHTTP.URL, "guard-model"))
	if err != nil {
		t.Fatal(err)
	}
	routeID := settings.Routes[0].ID
	if _, err := dataStore.SaveToolGuardSettings(ctx, workspace.ID, store.ToolGuardSettingsInput{
		Mode:           "strict",
		ModelRouteID:   &routeID,
		TimeoutSeconds: 5,
	}); err != nil {
		t.Fatal(err)
	}
	settings, err = dataStore.GetAgentSettings(ctx, workspace.ID)
	if err != nil {
		t.Fatal(err)
	}

	// 配置：一台关闭守卫的设备、一台开启守卫的设备、一个关闭守卫的 MCP 连接（另有一台从未落库配置的设备）
	if _, err := dataStore.SaveDeviceToolGuard(ctx, workspace.ID, store.DeviceToolGuardInput{
		DeviceNodeID: "node-off",
		DeviceName:   "Dev Box",
		Enabled:      false,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveDeviceToolGuard(ctx, workspace.ID, store.DeviceToolGuardInput{
		DeviceNodeID: "node-on",
		DeviceName:   "Prod Box",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name:             "dev-db-mcp",
		URL:              "https://mcp.example.com/dev-db",
		Enabled:          true,
		TimeoutMS:        5000,
		ToolGuardEnabled: false,
	}); err != nil {
		t.Fatal(err)
	}

	client := modelclient.NewWithHTTPClient(guardHTTP.Client())
	deviceNames := map[string]string{
		"node-off":     "Dev Box",
		"node-on":      "Prod Box",
		"node-default": "Default Box",
	}

	// 1. 未启用守卫的设备（显式关闭 / 从未落库）与未启用守卫的 MCP 执行 drop database、rm -rf / 均应直接放行，且不触发守卫小模型
	unguardedCases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{
			name: "disabled device rm -rf /",
			tool: "exec_command",
			args: map[string]any{"node_id": "node-off", "cmd": "sudo rm -rf / --no-preserve-root"},
		},
		{
			name: "disabled device drop database",
			tool: "exec_command",
			args: map[string]any{"node_id": "node-off", "cmd": `psql -c "DROP DATABASE dev_db;"`},
		},
		{
			name: "unconfigured device rm -rf /",
			tool: "exec_command",
			args: map[string]any{"node_id": "node-default", "cmd": "rm -rf /"},
		},
		{
			name: "unconfigured device drop database",
			tool: "exec_command",
			args: map[string]any{"node_id": "node-default", "cmd": `docker exec pg psql -c "DROP DATABASE staging;"`},
		},
		{
			name: "disabled mcp drop database",
			tool: "call_mcp_tool",
			args: map[string]any{"tool_id": "dev-db-mcp:exec_sql", "arguments": map[string]any{"sql": "DROP DATABASE dev_db;"}},
		},
	}
	for _, tc := range unguardedCases {
		dec := EvaluateToolGuard(
			ctx, dataStore, client, nil, settings,
			workspace.ID, card.ID, "node-off", deviceNames,
			"Guard Flow Card", "", tc.tool, tc.args,
		)
		if dec.Decision != "allow" {
			t.Fatalf("[%s] expected unguarded target to allow, got %+v", tc.name, dec)
		}
	}
	if guardModelCalls != 0 {
		t.Fatalf("expected unguarded targets never to call guard model, got %d calls", guardModelCalls)
	}

	// 2. 已开启守卫的设备即使加入了卡片临时白名单，执行极高危命令依然强制拦截且 AllowSessionWhitelist=false
	if err := dataStore.AddCardScopeWhitelist(ctx, workspace.ID, card.ID, "device", "node-on", human.ID); err != nil {
		t.Fatalf("add card whitelist: %v", err)
	}
	criticalArgs := map[string]any{
		"node_id": "node-on",
		"cmd":     `psql -c "DROP DATABASE staging;"`,
	}
	blockedCrit := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, card.ID, "node-on", deviceNames,
		"Guard Flow Card", "", "exec_command", criticalArgs,
	)
	if blockedCrit.Decision != "ask_human" || blockedCrit.RiskLevel != "critical" || blockedCrit.Reversible || blockedCrit.AllowSessionWhitelist {
		t.Fatalf("expected critical command on whitelisted guarded device to ask_human with AllowSessionWhitelist=false, got %+v", blockedCrit)
	}
	if guardModelCalls != 0 {
		t.Fatalf("expected static critical rule to intercept before calling guard model, got %d calls", guardModelCalls)
	}

	// 3. 用户对该极高危命令点击「允许本次执行」后，hasApprovedMatchingToolCall 优先于静态极高危规则放行，不陷入死循环
	allowSession := false
	approval, err := dataStore.CreateToolApproval(ctx, store.CreateToolApprovalParams{
		WorkspaceID:           workspace.ID,
		CardID:                card.ID,
		ToolCallID:            "call-crit-drop-db",
		ToolName:              "exec_command",
		Arguments:             criticalArgs,
		RiskLevel:             blockedCrit.RiskLevel,
		Reversible:            blockedCrit.Reversible,
		Reason:                blockedCrit.Reason,
		ScopeType:             blockedCrit.ScopeType,
		ScopeTarget:           blockedCrit.ScopeTarget,
		ScopeLabel:            blockedCrit.ScopeLabel,
		AllowSessionWhitelist: &allowSession,
	})
	if err != nil {
		t.Fatalf("create tool approval: %v", err)
	}

	// 审批仍为 pending 时，继续拦截
	stillBlocked := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, card.ID, "node-on", deviceNames,
		"Guard Flow Card", "", "exec_command", criticalArgs,
	)
	if stillBlocked.Decision != "ask_human" || stillBlocked.RiskLevel != "critical" {
		t.Fatalf("expected pending approval NOT to allow critical command, got %+v", stillBlocked)
	}

	// 人工批准本次调用后，同工具同参数重放应放行
	if _, err := dataStore.ResolveToolApproval(ctx, store.ResolveToolApprovalParams{
		WorkspaceID:        workspace.ID,
		CardID:             card.ID,
		ApprovalID:         approval.ID,
		Status:             string(domain.ToolApprovalStatusApproved),
		Comment:            "确认清理测试库",
		ResolvedBySourceID: human.ID,
	}); err != nil {
		t.Fatalf("approve tool call: %v", err)
	}

	approvedReplay := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, card.ID, "node-on", deviceNames,
		"Guard Flow Card", "", "exec_command", criticalArgs,
	)
	if approvedReplay.Decision != "allow" {
		t.Fatalf("expected single-approved critical command replay to be allowed, got %+v", approvedReplay)
	}

	// 单次放行凭证在首次放行时已被原子消费，数据库中应记录 consumed_at，且同参数第二次调用必须再次被拦截
	consumedRow, err := dataStore.GetToolApproval(ctx, workspace.ID, card.ID, approval.ID)
	if err != nil {
		t.Fatalf("get consumed tool approval: %v", err)
	}
	if !consumedRow.ConsumedAt.Valid {
		t.Fatalf("expected consumed_at to be populated after first replay")
	}
	secondReplay := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, card.ID, "node-on", deviceNames,
		"Guard Flow Card", "", "exec_command", criticalArgs,
	)
	if secondReplay.Decision != "ask_human" || secondReplay.RiskLevel != "critical" || secondReplay.AllowSessionWhitelist {
		t.Fatalf("expected second replay with same args to be intercepted after single-use consumption, got %+v", secondReplay)
	}

	// 非 critical 高危写命令在独立新卡片（无临时白名单）上 approve_once 放行一次后，同参数再次调用必须重新走守卫评估（禁止永久免检）
	freshCard, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Fresh Deploy Card", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatalf("create fresh deploy card: %v", err)
	}
	deployArgs := map[string]any{
		"node_id": "node-on",
		"cmd":     "nexusdock-backup && docker compose up -d",
	}
	deployApproval, err := dataStore.CreateToolApproval(ctx, store.CreateToolApprovalParams{
		WorkspaceID: workspace.ID,
		CardID:      freshCard.ID,
		ToolCallID:  "call-deploy-once",
		ToolName:    "exec_command",
		Arguments:   deployArgs,
		RiskLevel:   "high",
		Reversible:  false,
		Reason:      "生产发布命令需要确认",
		ScopeType:   "device",
		ScopeTarget: "node-on",
		ScopeLabel:  "Prod Box",
	})
	if err != nil {
		t.Fatalf("create deploy approval: %v", err)
	}
	if _, err := dataStore.ResolveToolApproval(ctx, store.ResolveToolApprovalParams{
		WorkspaceID:        workspace.ID,
		CardID:             freshCard.ID,
		ApprovalID:         deployApproval.ID,
		Status:             string(domain.ToolApprovalStatusApproved),
		Comment:            "仅放行本次发布",
		ResolvedBySourceID: human.ID,
	}); err != nil {
		t.Fatalf("approve deploy call: %v", err)
	}
	beforeModelCalls := guardModelCalls
	deployFirst := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, freshCard.ID, "node-on", deviceNames,
		"Fresh Deploy Card", "", "exec_command", deployArgs,
	)
	if deployFirst.Decision != "allow" || guardModelCalls != beforeModelCalls {
		t.Fatalf("expected first deploy replay to consume approval without guard model, decision=%+v calls=%d->%d", deployFirst, beforeModelCalls, guardModelCalls)
	}
	deploySecond := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, freshCard.ID, "node-on", deviceNames,
		"Fresh Deploy Card", "", "exec_command", deployArgs,
	)
	if deploySecond.Decision != "ask_human" || guardModelCalls != beforeModelCalls+1 {
		t.Fatalf("expected second deploy replay to re-trigger guard model and ask_human, decision=%+v calls=%d->%d", deploySecond, beforeModelCalls, guardModelCalls)
	}

	// 单次批准仅对完全匹配的参数生效，同一设备上的其他极高危命令仍必须拦截
	otherCritical := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, card.ID, "node-on", deviceNames,
		"Guard Flow Card", "", "exec_command", map[string]any{
			"node_id": "node-on",
			"cmd":     "sudo rm -rf / --no-preserve-root",
		},
	)
	if otherCritical.Decision != "ask_human" || otherCritical.RiskLevel != "critical" || otherCritical.AllowSessionWhitelist {
		t.Fatalf("expected different critical command to remain blocked after single approval, got %+v", otherCritical)
	}

	// 被拒绝的审批记录不应触发放行
	rejectedArgs := map[string]any{
		"node_id": "node-on",
		"cmd":     `psql -c "DROP DATABASE production;"`,
	}
	rejectedApproval, err := dataStore.CreateToolApproval(ctx, store.CreateToolApprovalParams{
		WorkspaceID:           workspace.ID,
		CardID:                card.ID,
		ToolCallID:            "call-crit-reject",
		ToolName:              "exec_command",
		Arguments:             rejectedArgs,
		RiskLevel:             "critical",
		Reversible:            false,
		Reason:                "drop database",
		ScopeType:             "device",
		ScopeTarget:           "node-on",
		ScopeLabel:            "Prod Box",
		AllowSessionWhitelist: &allowSession,
	})
	if err != nil {
		t.Fatalf("create rejected approval: %v", err)
	}
	if _, err := dataStore.ResolveToolApproval(ctx, store.ResolveToolApprovalParams{
		WorkspaceID:        workspace.ID,
		CardID:             card.ID,
		ApprovalID:         rejectedApproval.ID,
		Status:             string(domain.ToolApprovalStatusRejected),
		Comment:            "禁止删生产库",
		ResolvedBySourceID: human.ID,
	}); err != nil {
		t.Fatalf("reject tool call: %v", err)
	}
	rejectedReplay := EvaluateToolGuard(
		ctx, dataStore, client, nil, settings,
		workspace.ID, card.ID, "node-on", deviceNames,
		"Guard Flow Card", "", "exec_command", rejectedArgs,
	)
	if rejectedReplay.Decision != "ask_human" || rejectedReplay.RiskLevel != "critical" || rejectedReplay.AllowSessionWhitelist {
		t.Fatalf("expected rejected critical command to remain blocked, got %+v", rejectedReplay)
	}
}
