package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestGlobalToolGuardMigration000035_BackfillsSwitchesAndGlobalPolicy(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	database := stdlib.OpenDB(*s.pool.Config().ConnConfig)
	defer database.Close()
	const migrations = "../../db/migrations"
	defer func() {
		if err := goose.Up(database, migrations); err != nil {
			t.Errorf("restore latest schema: %v", err)
		}
	}()

	if err := goose.DownTo(database, migrations, 34); err != nil {
		t.Fatalf("downgrade to migration 34: %v", err)
	}

	route1ID := uuid.New()
	route2ID := uuid.New()
	cred1ID := uuid.New()
	cred2ID := uuid.New()
	if _, err := database.Exec(`
		INSERT INTO workspace_agent_settings (workspace_id, source_id, system_instructions)
		VALUES ($1, $2, '')
	`, f.WorkspaceID, f.AgentID); err != nil {
		t.Fatalf("insert pre-000035 agent settings: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO workspace_api_credentials (
			id, workspace_id, name, base_url, api_key_env
		) VALUES
			($1, $3, 'guard-a', 'https://guard-a.example.com/v1', ''),
			($2, $3, 'guard-b', 'https://guard-b.example.com/v1', '')
	`, cred1ID, cred2ID, f.WorkspaceID); err != nil {
		t.Fatalf("insert api credentials: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO workspace_model_routes (
			id, workspace_id, position, enabled, provider_type,
			api_credential_id, model
		) VALUES
			($1, $3, 0, true, 'openai_compatible', $4, 'guard-a'),
			($2, $3, 1, true, 'openai_compatible', $5, 'guard-b')
	`, route1ID, route2ID, f.WorkspaceID, cred1ID, cred2ID); err != nil {
		t.Fatalf("insert model routes: %v", err)
	}

	// 插入存量设备守卫：一个 block_high_risk（带自定义规则和超时），一个 strict（带模型路由和自定义规则），一个 disabled。
	if _, err := database.Exec(`
		INSERT INTO workspace_device_tool_guards (
			id, workspace_id, device_node_id, device_name, mode, model_route_id, timeout_seconds, custom_rules
		) VALUES
			($1, $4, 'node_tianyi', 'TianYi', 'block_high_risk', NULL, 20, 'allow git status'),
			($2, $4, 'node_prod', 'ProdBox', 'strict', $5, 30, 'block rm and chmod'),
			($3, $4, 'node_disabled', 'DisabledBox', 'disabled', NULL, 10, 'ignored rule')
	`, uuid.New(), uuid.New(), uuid.New(), f.WorkspaceID, route1ID); err != nil {
		t.Fatalf("insert legacy device tool guards: %v", err)
	}

	// 插入存量 MCP 连接：一个启用 block_high_risk 并带规则，一个 disabled。
	if _, err := database.Exec(`
		INSERT INTO workspace_mcp_connections (
			id, workspace_id, name, url, enabled, timeout_ms,
			tool_guard_mode, tool_guard_model_route_id, tool_guard_timeout_seconds, tool_guard_custom_rules
		) VALUES
			($1, $3, 'github', 'https://mcp.example.com/github', true, 5000, 'block_high_risk', $4, 18, 'require review on force push'),
			($2, $3, 'readonly', 'https://mcp.example.com/ro', true, 5000, 'disabled', NULL, 10, '')
	`, uuid.New(), uuid.New(), f.WorkspaceID, route2ID); err != nil {
		t.Fatalf("insert legacy mcp connections: %v", err)
	}

	if err := goose.Up(database, migrations); err != nil {
		t.Fatalf("apply migration 000035: %v", err)
	}

	// 验证工作区全局工具守卫择优回填结果：模式 strict、超时取非默认优先值、非空规则带目标名称前缀合并保留（避免丢失已有规则）。
	globalGuard, err := s.GetToolGuardSettings(ctx, f.WorkspaceID)
	if err != nil {
		t.Fatalf("get migrated global tool guard: %v", err)
	}
	if globalGuard.Mode != "strict" {
		t.Fatalf("expected migrated global mode=strict, got %q", globalGuard.Mode)
	}
	if globalGuard.ModelRouteID == nil {
		t.Fatalf("expected migrated global model_route_id to be populated")
	}
	if globalGuard.TimeoutSeconds == 10 {
		t.Fatalf("expected migrated global timeout_seconds to preserve non-default value, got %d", globalGuard.TimeoutSeconds)
	}
	if !strings.Contains(globalGuard.CustomRules, "[TianYi] allow git status") ||
		!strings.Contains(globalGuard.CustomRules, "[ProdBox] block rm and chmod") ||
		!strings.Contains(globalGuard.CustomRules, "[github] require review on force push") ||
		!strings.Contains(globalGuard.CustomRules, "[DisabledBox] ignored rule") {
		t.Fatalf("expected merged custom_rules from targets with prefixes, got %q", globalGuard.CustomRules)
	}

	// 验证设备开关回填
	deviceGuards, err := s.ListDeviceToolGuards(ctx, f.WorkspaceID)
	if err != nil {
		t.Fatalf("list migrated device guards: %v", err)
	}
	enabledByNode := make(map[string]bool, len(deviceGuards))
	for _, g := range deviceGuards {
		enabledByNode[g.DeviceNodeID] = g.Enabled
	}
	if !enabledByNode["node_tianyi"] || !enabledByNode["node_prod"] || enabledByNode["node_disabled"] {
		t.Fatalf("unexpected migrated device enabled flags: %+v", enabledByNode)
	}

	// 验证 MCP 开关回填
	mcpConns, err := s.ListMCPConnections(ctx, f.WorkspaceID)
	if err != nil {
		t.Fatalf("list migrated mcp connections: %v", err)
	}
	mcpEnabledByName := make(map[string]bool, len(mcpConns))
	for _, conn := range mcpConns {
		mcpEnabledByName[conn.Name] = conn.ToolGuardEnabled
	}
	if !mcpEnabledByName["github"] || mcpEnabledByName["readonly"] {
		t.Fatalf("unexpected migrated mcp tool_guard_enabled flags: %+v", mcpEnabledByName)
	}
}
