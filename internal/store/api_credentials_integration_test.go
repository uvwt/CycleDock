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

func TestWorkspaceAPICredentialsMigration000032_ConsolidatesLegacyRoutesAndHealsMissingKeys(t *testing.T) {
	s := requireIntegration(t)
	if err := s.ConfigureModelEncryption(strings.Repeat("ab", 32)); err != nil {
		t.Fatalf("configure model encryption: %v", err)
	}
	t.Cleanup(func() {
		_ = s.ConfigureModelEncryption("")
	})

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

	if err := goose.DownTo(database, migrations, 31); err != nil {
		t.Fatalf("downgrade to migration 31: %v", err)
	}

	routeWithKeyID := uuid.New()
	routeMissingKeyID := uuid.New()
	legacyCiphertext, err := s.encryptLegacyRouteModelAPIKey(
		f.WorkspaceID.String(),
		routeWithKeyID.String(),
		"sk-cpa-legacy-prod-key",
	)
	if err != nil {
		t.Fatalf("encrypt legacy route key: %v", err)
	}

	// 模拟 000032 之前的真实线上状态：
	// 同一 CPA 服务地址下，第一条路由保存了加密 API Key（带末尾斜杠），第二条新增路由因未继承密文而为空。
	if _, err := database.Exec(`
		INSERT INTO workspace_agent_settings (workspace_id, source_id, system_instructions)
		VALUES ($1, $2, '')
	`, f.WorkspaceID, f.AgentID); err != nil {
		t.Fatalf("insert legacy agent settings: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO workspace_model_routes (
			id, workspace_id, position, enabled, provider_type,
			provider_base_url, model, api_key_env, api_key_ciphertext
		) VALUES
			($1, $3, 0, true, 'openai_compatible', 'https://cpa.example.com/v1/', 'claude-sonnet-4-6', '', $4),
			($2, $3, 1, true, 'openai_compatible', 'https://cpa.example.com/v1', 'gpt-5.4', '', NULL)
	`, routeWithKeyID, routeMissingKeyID, f.WorkspaceID, legacyCiphertext); err != nil {
		t.Fatalf("insert legacy model routes: %v", err)
	}

	if err := goose.Up(database, migrations); err != nil {
		t.Fatalf("apply migration 000032: %v", err)
	}

	creds, err := s.ListAPICredentials(ctx, f.WorkspaceID)
	if err != nil {
		t.Fatalf("list API credentials: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("expected 1 consolidated API credential, got %d: %+v", len(creds), creds)
	}
	if creds[0].ID != routeWithKeyID {
		t.Fatalf("expected credential id=%s to preserve ciphertext route id, got %s", routeWithKeyID, creds[0].ID)
	}
	if creds[0].BaseUrl != "https://cpa.example.com/v1" || creds[0].Name != "cpa.example.com" {
		t.Fatalf("unexpected migrated credential metadata: %+v", creds[0])
	}
	credSecret, err := s.APICredentialKey(creds[0])
	if err != nil || credSecret != "sk-cpa-legacy-prod-key" {
		t.Fatalf("decrypt migrated credential key: got=%q err=%v", credSecret, err)
	}

	settings, err := s.GetAgentSettings(ctx, f.WorkspaceID)
	if err != nil {
		t.Fatalf("get migrated agent settings: %v", err)
	}
	if len(settings.Routes) != 2 {
		t.Fatalf("expected 2 migrated routes, got %d", len(settings.Routes))
	}
	for _, route := range settings.Routes {
		if !route.ApiCredentialID.Valid || route.ApiCredentialID.UUID != creds[0].ID {
			t.Fatalf("route %s was not linked to consolidated credential %s: %+v", route.ID, creds[0].ID, route)
		}
		if route.ProviderBaseUrl != "https://cpa.example.com/v1" {
			t.Fatalf("route %s base_url=%q, want normalized URL", route.ID, route.ProviderBaseUrl)
		}
		decrypted, err := s.ModelRouteAPIKey(route)
		if err != nil || decrypted != "sk-cpa-legacy-prod-key" {
			t.Fatalf("route %s (%s) failed to decrypt shared key: got=%q err=%v", route.ID, route.Model, decrypted, err)
		}
	}

	// 验证回滚到 31 后再升级到 32 幂等无损。
	if err := goose.DownTo(database, migrations, 31); err != nil {
		t.Fatalf("rollback migration 000032: %v", err)
	}
	if err := goose.Up(database, migrations); err != nil {
		t.Fatalf("re-apply migration 000032: %v", err)
	}
}

func TestSaveAgentSettings_PreservesRetainedRouteForeignKeysAndSharesCredentials(t *testing.T) {
	s := requireIntegration(t)
	if err := s.ConfigureModelEncryption(strings.Repeat("cd", 32)); err != nil {
		t.Fatalf("configure model encryption: %v", err)
	}
	t.Cleanup(func() {
		_ = s.ConfigureModelEncryption("")
	})

	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	route1ID := uuid.New()
	initial, err := s.SaveAgentSettings(ctx, f.WorkspaceID, AgentSettingsInput{
		Routes: []ModelRouteInput{{
			ID:              route1ID,
			Enabled:         true,
			ProviderType:    "openai_compatible",
			ProviderBaseURL: "https://cpa.example.com/v1/",
			Model:           "claude-sonnet-4-6",
			APIKey:          "sk-initial-shared-key",
		}},
	})
	if err != nil {
		t.Fatalf("save initial settings: %v", err)
	}
	if len(initial.APICredentials) != 1 || len(initial.Routes) != 1 {
		t.Fatalf("unexpected initial settings: %+v", initial)
	}
	credID := initial.APICredentials[0].ID

	if _, err := s.SetCardPreferredModelRoute(ctx, f.WorkspaceID, f.CardID, uuid.NullUUID{UUID: route1ID, Valid: true}); err != nil {
		t.Fatalf("set card preferred model route: %v", err)
	}
	submitted, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:           f.WorkspaceID,
		CardID:                f.CardID,
		AuthorSourceID:        f.HumanID,
		Content:               "check deployment",
		IdempotencyKey:        "preserve-fk-turn",
		DispatchMode:          domain.DispatchModeEnqueue,
		PreferredModelRouteID: &route1ID,
	})
	if err != nil {
		t.Fatalf("submit turn with preferred route: %v", err)
	}
	mcpConn, err := s.SaveMCPConnection(ctx, f.WorkspaceID, MCPConnectionInput{
		Name:             "ops-mcp",
		URL:              "https://mcp.example.com/sse",
		Enabled:          true,
		TimeoutMS:        5000,
		ToolGuardEnabled: true,
	})
	if err != nil {
		t.Fatalf("save MCP connection with tool guard route: %v", err)
	}
	if _, err := s.SaveToolGuardSettings(ctx, f.WorkspaceID, ToolGuardSettingsInput{
		Mode:           "strict",
		ModelRouteID:   &route1ID,
		TimeoutSeconds: 15,
		CustomRules:    "protect prod",
	}); err != nil {
		t.Fatalf("save global tool guard settings: %v", err)
	}

	// 新增第二个模型（api_key 为空）并把顺序调整到 route1 之前：
	// 1. 新路由必须自动共享同一凭据；
	// 2. 保留的 route1 不得因保存触发 ON DELETE SET NULL 清空卡片、Turn 与全局守卫引用，且模型保存不能冲掉全局守卫配置。
	route2ID := uuid.New()
	updated, err := s.SaveAgentSettings(ctx, f.WorkspaceID, AgentSettingsInput{
		Routes: []ModelRouteInput{
			{
				ID:              route2ID,
				Enabled:         true,
				ProviderType:    "openai_compatible",
				APICredentialID: &credID,
				Model:           "gpt-5.4",
			},
			{
				ID:              route1ID,
				Enabled:         true,
				ProviderType:    "openai_compatible",
				APICredentialID: &credID,
				Model:           "claude-sonnet-4-6",
			},
		},
	})
	if err != nil {
		t.Fatalf("save reordered multi-route settings: %v", err)
	}
	if len(updated.Routes) != 2 || updated.Routes[0].ID != route2ID || updated.Routes[1].ID != route1ID {
		t.Fatalf("unexpected reordered routes: %+v", updated.Routes)
	}
	for _, r := range updated.Routes {
		gotKey, err := s.ModelRouteAPIKey(r)
		if err != nil || gotKey != "sk-initial-shared-key" {
			t.Fatalf("route %s (%s) key=%q err=%v", r.ID, r.Model, gotKey, err)
		}
	}

	card, err := s.GetCard(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatalf("get card after route save: %v", err)
	}
	if !card.PreferredModelRouteID.Valid || card.PreferredModelRouteID.UUID != route1ID {
		t.Fatalf("card preferred_model_route_id was wiped on route save: %+v", card.PreferredModelRouteID)
	}
	var turnPreferred uuid.NullUUID
	if err := s.pool.QueryRow(ctx,
		"SELECT preferred_model_route_id FROM card_turns WHERE workspace_id=$1 AND id=$2",
		f.WorkspaceID, submitted.TurnID,
	).Scan(&turnPreferred); err != nil {
		t.Fatalf("query turn preferred_model_route_id: %v", err)
	}
	if !turnPreferred.Valid || turnPreferred.UUID != route1ID {
		t.Fatalf("turn preferred_model_route_id was wiped on route save: %+v", turnPreferred)
	}
	reloadedMCP, err := s.GetMCPConnection(ctx, f.WorkspaceID, mcpConn.ID)
	if err != nil {
		t.Fatalf("get MCP connection after route save: %v", err)
	}
	if !reloadedMCP.ToolGuardEnabled {
		t.Fatalf("MCP tool_guard_enabled should remain true after route save")
	}
	reloadedGuard, err := s.GetToolGuardSettings(ctx, f.WorkspaceID)
	if err != nil {
		t.Fatalf("get global tool guard settings after route save: %v", err)
	}
	if reloadedGuard.Mode != "strict" || reloadedGuard.ModelRouteID == nil || *reloadedGuard.ModelRouteID != route1ID || reloadedGuard.TimeoutSeconds != 15 || reloadedGuard.CustomRules != "protect prod" {
		t.Fatalf("global tool guard settings were wiped or altered on route save: %+v", reloadedGuard)
	}

	// 在服务商凭据级轮换密钥后，两条路由同步生效；随后删除 route1 时，route2 的凭据不受影响，而原外键被安全置空。
	if _, err := s.SaveAPICredential(ctx, f.WorkspaceID, APICredentialInput{
		ID:      credID,
		Name:    "CPA 主账号",
		BaseURL: "https://cpa.example.com/v1",
		APIKey:  "sk-rotated-shared-key",
	}); err != nil {
		t.Fatalf("rotate API credential key: %v", err)
	}
	afterDeleteRoute1, err := s.SaveAgentSettings(ctx, f.WorkspaceID, AgentSettingsInput{
		Routes: []ModelRouteInput{{
			ID:              route2ID,
			Enabled:         true,
			ProviderType:    "openai_compatible",
			APICredentialID: &credID,
			Model:           "gpt-5.4",
		}},
	})
	if err != nil {
		t.Fatalf("remove route1 while keeping route2: %v", err)
	}
	if len(afterDeleteRoute1.Routes) != 1 || afterDeleteRoute1.Routes[0].ID != route2ID {
		t.Fatalf("unexpected remaining routes: %+v", afterDeleteRoute1.Routes)
	}
	gotKey, err := s.ModelRouteAPIKey(afterDeleteRoute1.Routes[0])
	if err != nil || gotKey != "sk-rotated-shared-key" {
		t.Fatalf("route2 lost rotated key after deleting route1: key=%q err=%v", gotKey, err)
	}
	cardAfterRemove, err := s.GetCard(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	if cardAfterRemove.PreferredModelRouteID.Valid {
		t.Fatalf("expected removed route1 FK to become NULL, got %+v", cardAfterRemove.PreferredModelRouteID)
	}
}
