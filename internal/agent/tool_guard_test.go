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
	"github.com/uvwt/CycleDock/internal/store/db"
)

func TestToolGuard_StaticReadOnlyAndDestructive(t *testing.T) {
	ctx := context.Background()
	workspaceID := uuid.New()
	cardID := uuid.New()

	settings := store.AgentSettings{
		WorkspaceAgentSetting: db.WorkspaceAgentSetting{
			ToolGuardMode:           "strict",
			ToolGuardTimeoutSeconds: 5,
		},
	}

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
			settings, workspaceID, cardID,
			"测试卡片", "测试描述",
			name, map[string]any{},
		)
		if dec.Decision != "allow" {
			t.Fatalf("expected %q to be allowed in strict mode, got %+v", name, dec)
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
		settings, workspaceID, cardID,
		"测试卡片", "测试描述",
		"exec_command", map[string]any{"cmd": "sudo rm -rf /"},
	)
	if critDec.Decision != "ask_human" || critDec.RiskLevel != "critical" || critDec.Reversible {
		t.Fatalf("expected critical destructive command to ask_human immediately, got %+v", critDec)
	}

	// 4. 关闭模式下所有工具直接放行
	disabledSettings := settings
	disabledSettings.ToolGuardMode = "disabled"
	dec := EvaluateToolGuard(
		ctx, nil, nil, nil,
		disabledSettings, workspaceID, cardID,
		"测试卡片", "测试描述",
		"exec_command", map[string]any{"cmd": "rm -rf /"},
	)
	if dec.Decision != "allow" {
		t.Fatalf("expected disabled mode to allow, got %+v", dec)
	}
}

func TestToolGuard_DynamicSmallModelAndFallback(t *testing.T) {
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
						"content": "```json\n{\"reversible\": false, \"risk_level\": \"high\", \"decision\": \"ask_human\", \"reason\": \"尝试修改生产环境配置并重启容器\"}\n```",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := modelclient.NewWithHTTPClient(server.Client())
	settings := store.AgentSettings{
		WorkspaceAgentSetting: db.WorkspaceAgentSetting{
			ToolGuardMode:           "block_high_risk",
			ToolGuardModelRouteID:   uuid.NullUUID{UUID: routeID, Valid: true},
			ToolGuardTimeoutSeconds: 5,
			ToolGuardCustomRules:    "禁止未经确认修改生产配置",
		},
		Routes: []db.WorkspaceModelRoute{
			{
				ID:              routeID,
				Enabled:         true,
				ProviderType:    "openai_compatible",
				ProviderBaseUrl: server.URL,
				Model:           "gpt-4o-mini-guard",
			},
		},
	}

	dec, err := evaluateDynamicModel(
		ctx, nil, client, nil, settings, workspaceID,
		"部署服务", "更新线上配置",
		"exec_command", map[string]any{"cmd": "docker compose up -d --force-recreate"},
	)
	if err != nil {
		t.Fatalf("unexpected error from evaluateDynamicModel: %v", err)
	}
	if dec.Decision != "ask_human" || dec.RiskLevel != "high" || dec.Reversible {
		t.Fatalf("unexpected decision from small model: %+v", dec)
	}

	// 测试第三层降级兜底：当小模型服务超时或报错时，EvaluateToolGuard 对未分类写操作回退为 ask_human
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		http.Error(w, "upstream timeout", http.StatusGatewayTimeout)
	}))
	defer errServer.Close()

	errSettings := settings
	errSettings.Routes = []db.WorkspaceModelRoute{
		{
			ID:              routeID,
			Enabled:         true,
			ProviderType:    "openai_compatible",
			ProviderBaseUrl: errServer.URL,
			Model:           "gpt-4o-mini-guard",
		},
	}
	fallbackDec := EvaluateToolGuard(
		ctx, nil, modelclient.NewWithHTTPClient(errServer.Client()), nil,
		errSettings, workspaceID, cardID,
		"部署服务", "更新线上配置",
		"file_edit", map[string]any{"action": "replace", "path": "config.yaml"},
	)
	if fallbackDec.Decision != "ask_human" || fallbackDec.RiskLevel != "high" {
		t.Fatalf("expected fallback decision to be ask_human with high risk, got %+v", fallbackDec)
	}
}
