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
