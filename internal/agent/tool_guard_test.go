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
			settings, workspaceID, cardID, "", nil,
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
		settings, workspaceID, cardID, "node-1", map[string]string{"node-1": "TianYi"},
		"测试卡片", "测试描述",
		"exec_command", map[string]any{"cmd": "sudo rm -rf /"},
	)
	if critDec.Decision != "ask_human" || critDec.RiskLevel != "critical" || critDec.Reversible {
		t.Fatalf("expected critical destructive command to ask_human immediately, got %+v", critDec)
	}
	if critDec.ScopeType != "device" || critDec.ScopeTarget != "node-1" || critDec.ScopeLabel != "TianYi" || critDec.AllowSessionWhitelist {
		t.Fatalf("expected critical destructive command scope=device:node-1(TianYi) and allow_session_whitelist=false, got %+v", critDec)
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
		ctx, nil, client, nil, settings, workspaceID, cardID, "node-prod-server", nil,
		"测试卡片", "测试描述",
		"exec_command", map[string]any{"cmd": "sudo rm -rf / --no-preserve-root"},
	)
	if critDec.Decision != "ask_human" || critDec.RiskLevel != "critical" || critDec.AllowSessionWhitelist {
		t.Fatalf("expected critical command to ask_human with allow_session_whitelist=false, got %+v", critDec)
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

func TestToolGuard_CommandSemanticsAvoidsFalsePositives(t *testing.T) {
	// 非破坏性命令（即使包含 format/shutdown/drop database/rm -rf /tmp 等字眼）不应被静态高危规则误杀
	safeOrNonCriticalCmds := []string{
		`docker ps --format "table {{.Names}}\t{{.Image}}\t{{.Status}}"`,
		`git log --format="%H %s" -n 5`,
		`rm -rf /tmp/build-cache`,
		`rm -rf ./dist`,
		`rmdir /s /q C:\Temp\build`,
		`del /f /s /q D:\workspace\cache`,
		`grep -rn "shutdown" /var/log`,
		`rg "drop database" ./db/migrations`,
		`echo "format C: and reboot"`,
		`dd if=/dev/zero of=./swap.img bs=1M count=10`,
		`chmod -R 777 /tmp/shared`,
	}
	for _, cmd := range safeOrNonCriticalCmds {
		isCrit, reason := isCriticalDestructiveCommand("exec_command", map[string]any{"cmd": cmd})
		if isCrit {
			t.Fatalf("expected command %q NOT to be flagged as critical destructive, got reason=%q", cmd, reason)
		}
	}

	// 真正高危系统破坏性命令仍应触发必审
	criticalCmds := []string{
		`sudo rm -rf / --no-preserve-root`,
		`rm -rf /*`,
		`env FOO=bar sh -c "rm -rf /"`,
		`echo "$(rm -rf /)"`,
		`format C: /q`,
		`cmd /c "format D:"`,
		`rmdir /s /q C:\`,
		`del /f /s /q C:\`,
		`mkfs.ext4 /dev/sda1`,
		`dd if=/dev/zero of=/dev/sda bs=4M`,
		`psql -c "DROP DATABASE production;"`,
		`echo "TRUNCATE TABLE users;" | mysql app`,
		`docker exec pg psql -U postgres -c "DROP SCHEMA public CASCADE;"`,
		`:(){ :|:& };:`,
		`chmod -R 777 /`,
		`shutdown -h now`,
		`reboot`,
		`init 0`,
	}
	for _, cmd := range criticalCmds {
		isCrit, reason := isCriticalDestructiveCommand("exec_command", map[string]any{"cmd": cmd})
		if !isCrit || reason == "" {
			t.Fatalf("expected command %q to be flagged as critical destructive", cmd)
		}
	}
}

func TestToolGuard_DynamicModelRouteFailover(t *testing.T) {
	ctx := context.Background()
	workspaceID := uuid.New()

	primaryID := uuid.New()
	disabledID := uuid.New()
	backupID := uuid.New()

	primaryCalls := 0
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer primaryServer.Close()

	disabledCalls := 0
	disabledServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		disabledCalls++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer disabledServer.Close()

	backupCalls := 0
	backupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls++
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"reversible": true, "risk_level": "low", "decision": "allow", "reason": "docker ps 仅查询容器运行状态，无副作用"}`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer backupServer.Close()

	settings := store.AgentSettings{
		Routes: []store.ModelRoute{
			{
				ID:              backupID,
				Enabled:         true,
				ProviderType:    "openai_compatible",
				ProviderBaseUrl: backupServer.URL,
				Model:           "backup-model",
			},
			{
				ID:              disabledID,
				Enabled:         false,
				ProviderType:    "openai_compatible",
				ProviderBaseUrl: disabledServer.URL,
				Model:           "disabled-model",
			},
			{
				ID:              primaryID,
				Enabled:         true,
				ProviderType:    "openai_compatible",
				ProviderBaseUrl: primaryServer.URL,
				Model:           "primary-guard-model",
			},
		},
	}
	policy := ScopedGuardPolicy{
		ScopeType:      "device",
		ScopeTarget:    "node-linux",
		Mode:           "block_high_risk",
		ModelRouteID:   uuid.NullUUID{UUID: primaryID, Valid: true},
		TimeoutSeconds: 5,
	}

	client := modelclient.NewWithHTTPClient(backupServer.Client())
	dec, err := evaluateDynamicModel(
		ctx, nil, client, nil, settings, policy, workspaceID,
		"巡检容器", "查看容器运行状态",
		"exec_command", map[string]any{"cmd": `docker ps --format "table {{.Names}}\t{{.Image}}\t{{.Status}}"`},
	)
	if err != nil {
		t.Fatalf("expected backup route to succeed after primary failed, got error: %v", err)
	}
	if dec.Decision != "allow" || dec.RiskLevel != "low" || !dec.Reversible {
		t.Fatalf("unexpected decision from backup route: %+v", dec)
	}
	if primaryCalls != 1 {
		t.Fatalf("expected primary route to be tried once, got %d", primaryCalls)
	}
	if disabledCalls != 0 {
		t.Fatalf("expected disabled route to be skipped, got %d calls", disabledCalls)
	}
	if backupCalls != 1 {
		t.Fatalf("expected backup route to be tried once, got %d", backupCalls)
	}

	// 父上下文已取消时，不得继续顺延请求后续路由
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = evaluateDynamicModel(
		canceledCtx, nil, client, nil, settings, policy, workspaceID,
		"巡检容器", "查看容器运行状态",
		"exec_command", map[string]any{"cmd": "docker ps"},
	)
	if err == nil {
		t.Fatalf("expected error when parent context is canceled")
	}
	if primaryCalls != 1 || backupCalls != 1 {
		t.Fatalf("expected no additional route requests on canceled context, got primary=%d backup=%d", primaryCalls, backupCalls)
	}
}

func TestToolGuard_ResolveScope(t *testing.T) {
	ctx := context.Background()
	workspaceID := uuid.New()
	deviceNames := map[string]string{"node-tianyi": "TianYi"}

	// 1. 外部 MCP 工具优先归属到 mcp 连接名
	mcpScope := resolveToolGuardScope(ctx, nil, workspaceID, "node-tianyi", deviceNames, "call_mcp_tool", map[string]any{
		"tool_id": "github:create_issue",
	})
	if mcpScope.ScopeType != "mcp" || mcpScope.ScopeTarget != "github" || mcpScope.ScopeLabel != "github" {
		t.Fatalf("unexpected mcp scope: %+v", mcpScope)
	}

	// 2. 显式携带 node_id 的设备工具应归属到目标设备节点
	explicitDeviceScope := resolveToolGuardScope(ctx, nil, workspaceID, "node-tianyi", deviceNames, "exec_command", map[string]any{
		"node_id": "node-rn",
		"cmd":     "systemctl restart app",
	})
	if explicitDeviceScope.ScopeType != "device" || explicitDeviceScope.ScopeTarget != "node-rn" || explicitDeviceScope.ScopeLabel != "" {
		t.Fatalf("unexpected explicit device scope: %+v", explicitDeviceScope)
	}

	// 3. 缺省 node_id 的设备类工具继承卡片设备上下文与设备名
	inheritedDeviceScope := resolveToolGuardScope(ctx, nil, workspaceID, "node-tianyi", deviceNames, "file_edit", map[string]any{
		"path": "C:\\temp\\a.txt",
	})
	if inheritedDeviceScope.ScopeType != "device" || inheritedDeviceScope.ScopeTarget != "node-tianyi" || inheritedDeviceScope.ScopeLabel != "TianYi" {
		t.Fatalf("unexpected inherited device scope: %+v", inheritedDeviceScope)
	}

	// 4. 非设备类工具（如 recall_write）不误继承卡片设备，兜底到 tool 作用域
	recallScope := resolveToolGuardScope(ctx, nil, workspaceID, "node-tianyi", deviceNames, "recall_write", map[string]any{
		"action": "create",
	})
	if recallScope.ScopeType != "tool" || recallScope.ScopeTarget != "recall_write" || recallScope.ScopeLabel != "recall_write" {
		t.Fatalf("unexpected recall fallback scope: %+v", recallScope)
	}

	// 5. 无任何设备目标时兜底到 tool 作用域
	noTargetDeviceScope := resolveToolGuardScope(ctx, nil, workspaceID, "", nil, "exec_command", map[string]any{
		"cmd": "ls",
	})
	if noTargetDeviceScope.ScopeType != "tool" || noTargetDeviceScope.ScopeTarget != "exec_command" {
		t.Fatalf("unexpected fallback scope: %+v", noTargetDeviceScope)
	}
}
