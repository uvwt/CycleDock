package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestToolGuard_StaticReadOnlyAndDestructive(t *testing.T) {
	ctx := context.Background()
	workspaceID := uuid.New()
	cardID := uuid.New()

	settings := store.AgentSettings{}

	// 1. 第一层：只读工具与内置 cycledock_* 协同工具应直接放行（0 延迟、0 Token 消耗）
	safeTools := []string{
		"read_file", "list_dir", "search_text", "recall_search", "session_observe", "reply_message",
		"cycledock_confirm_card", "cycledock_check_in", "cycledock_create_schedule", "cycledock_send_card_message",
	}
	for _, name := range safeTools {
		if !isSafeReadOnlyTool(name, nil) {
			t.Fatalf("expected %q to be classified as safe read-only/internal tool", name)
		}
		dec := EvaluateToolGuard(
			ctx, nil, nil, nil,
			settings, workspaceID, cardID, "",
			"测试卡片", "测试描述",
			name, map[string]any{},
		)
		if dec.Decision != "allow" {
			t.Fatalf("expected %q to be allowed, got %+v", name, dec)
		}
	}

	// 2. 第一层：MCP 只读前缀启发式识别
	if !isSafeReadOnlyTool("call_mcp_tool", map[string]any{"tool_id": "github:list_pull_requests"}) {
		t.Fatalf("expected github:list_pull_requests to be classified as safe read-only")
	}
	if isSafeReadOnlyTool("call_mcp_tool", map[string]any{"tool_id": "github:delete_repository"}) {
		t.Fatalf("expected github:delete_repository NOT to be classified as safe read-only")
	}

	// 3. 第一层：高危破坏性命令直接必审
	isCrit, reason := isCriticalDestructiveCommand("exec_command", map[string]any{
		"cmd": "sudo rm -rf / --no-preserve-root",
	})
	if !isCrit || reason == "" {
		t.Fatalf("expected rm -rf / to be flagged as critical destructive command")
	}
	critDec := EvaluateToolGuard(
		ctx, nil, nil, nil,
		settings, workspaceID, cardID, "node-1",
		"测试卡片", "测试描述",
		"exec_command", map[string]any{"cmd": "sudo rm -rf /"},
	)
	if critDec.Decision != "ask_human" || critDec.RiskLevel != "critical" || critDec.Reversible {
		t.Fatalf("expected critical destructive command to ask_human immediately, got %+v", critDec)
	}
}

func TestToolGuard_ScopedDynamicSmallModelAndFallback(t *testing.T) {
	ctx := context.Background()
	workspaceID := uuid.New()
	cardID := uuid.New()
	routeID := uuid.New()

	// 模拟小模型服务返回高风险不可逆判定（带 Markdown 代码块包裹以测试 extractJSONContent）
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "```json\n{\"reversible\": false, \"risk_level\": \"high\", \"decision\": \"ask_human\", \"reason\": \"尝试对目标设备修改生产环境配置并重启容器\"}\n```",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := modelclient.NewWithHTTPClient(server.Client())
	settings := store.AgentSettings{
		Routes: []store.ModelRoute{
			{
				ID:              routeID,
				Enabled:         true,
				ProviderType:    "openai_compatible",
				ProviderBaseUrl: server.URL,
				Model:           "gpt-4o-mini-guard",
			},
		},
	}

	policy := ScopedGuardPolicy{
		ScopeType:      "device",
		ScopeTarget:    "node-prod-server",
		Mode:           "block_high_risk",
		ModelRouteID:   uuid.NullUUID{UUID: routeID, Valid: true},
		TimeoutSeconds: 5,
		CustomRules:    "禁止未经确认重启线上生产容器",
	}

	// 明确高危系统指令必审测试
	critDec := EvaluateToolGuard(
		ctx, nil, client, nil, settings, workspaceID, cardID, "node-prod-server",
		"测试卡片", "测试描述",
		"exec_command", map[string]any{"cmd": "sudo rm -rf / --no-preserve-root"},
	)
	if critDec.Decision != "ask_human" || critDec.RiskLevel != "critical" {
		t.Fatalf("expected critical command to ask_human, got %+v", critDec)
	}

	dec, err := evaluateDynamicModel(
		ctx, nil, client, nil, settings, policy, workspaceID,
		"部署服务", "更新线上配置",
		"exec_command", map[string]any{"cmd": "docker compose up -d --force-recreate"},
	)
	if err != nil {
		t.Fatalf("unexpected error from evaluateDynamicModel: %v", err)
	}
	if dec.Decision != "ask_human" || dec.RiskLevel != "high" || dec.Reversible {
		t.Fatalf("unexpected decision from small model: %+v", dec)
	}

	// 测试针对 MCP 独立作用域的动态评估与超时降级
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		http.Error(w, "upstream timeout", http.StatusGatewayTimeout)
	}))
	defer errServer.Close()

	errSettings := settings
	errSettings.Routes = []store.ModelRoute{
		{
			ID:              routeID,
			Enabled:         true,
			ProviderType:    "openai_compatible",
			ProviderBaseUrl: errServer.URL,
			Model:           "gpt-4o-mini-guard",
		},
	}
	mcpPolicy := ScopedGuardPolicy{
		ScopeType:      "mcp",
		ScopeTarget:    "production-db",
		Mode:           "strict",
		ModelRouteID:   uuid.NullUUID{UUID: routeID, Valid: true},
		TimeoutSeconds: 5,
	}
	_, err = evaluateDynamicModel(
		ctx, nil, modelclient.NewWithHTTPClient(errServer.Client()), nil,
		errSettings, mcpPolicy, workspaceID,
		"数据库运维", "变更生产数据",
		"call_mcp_tool", map[string]any{"tool_id": "production-db:drop_table"},
	)
	if err == nil {
		t.Fatalf("expected error when small model returns 504")
	}
}
