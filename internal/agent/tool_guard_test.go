package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if !safeReadOnlyToolNames["cycledock_list_cards"] {
		t.Fatal("expected cycledock_list_cards to be explicitly registered in safeReadOnlyToolNames")
	}
	safeTools := []string{
		"read_file", "list_dir", "search_text", "recall_search", "session_observe", "reply_message",
		"cycledock_confirm_card", "cycledock_update_card", "cycledock_list_cards", "cycledock_check_in", "cycledock_create_schedule", "cycledock_send_card_message",
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

	// 3. 静态高危破坏性命令词法检测依然能准确识别 rm -rf / 与 drop database
	isCrit, reason := isCriticalDestructiveCommand("exec_command", map[string]any{
		"cmd": "sudo rm -rf / --no-preserve-root",
	})
	if !isCrit || reason == "" {
		t.Fatalf("expected rm -rf / to be flagged as critical destructive command")
	}
	if critDB, dbReason := isCriticalDestructiveCommand("exec_command", map[string]any{
		"cmd": `psql -c "DROP DATABASE production;"`,
	}); !critDB || dbReason == "" {
		t.Fatalf("expected drop database to be flagged as critical destructive command")
	}

	// 4. 目标设备/MCP 未开启守卫（无守卫策略时）直接放行，不再被静态高危规则硬拦截
	for _, tc := range []struct {
		tool        string
		args        map[string]any
		scopeType   string
		scopeTarget string
		scopeLabel  string
	}{
		{
			tool:        "exec_command",
			args:        map[string]any{"cmd": "sudo rm -rf /"},
			scopeType:   "device",
			scopeTarget: "node-1",
			scopeLabel:  "TianYi",
		},
		{
			tool:        "exec_command",
			args:        map[string]any{"cmd": `psql -c "DROP DATABASE production;"`},
			scopeType:   "device",
			scopeTarget: "node-1",
			scopeLabel:  "TianYi",
		},
		{
			tool:        "call_mcp_tool",
			args:        map[string]any{"tool_id": "github:delete_repository", "arguments": map[string]any{"repo": "demo"}},
			scopeType:   "mcp",
			scopeTarget: "github",
			scopeLabel:  "github",
		},
	} {
		dec := EvaluateToolGuard(
			ctx, nil, nil, nil,
			settings, workspaceID, cardID, "node-1", map[string]string{"node-1": "TianYi"},
			"测试卡片", "测试描述",
			tc.tool, tc.args,
		)
		if dec.Decision != "allow" {
			t.Fatalf("expected unguarded %s (%v) to be allowed, got %+v", tc.tool, tc.args, dec)
		}
		if dec.ScopeType != tc.scopeType || dec.ScopeTarget != tc.scopeTarget || dec.ScopeLabel != tc.scopeLabel {
			t.Fatalf("unexpected scope metadata for unguarded %s: got %+v", tc.tool, dec)
		}
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

	// 未配置/未开启守卫时，EvaluateToolGuard 直接放行且不触发小模型评估
	unguardedDec := EvaluateToolGuard(
		ctx, nil, client, nil, settings, workspaceID, cardID, "node-prod-server", nil,
		"测试卡片", "测试描述",
		"exec_command", map[string]any{"cmd": "sudo rm -rf / --no-preserve-root"},
	)
	if unguardedDec.Decision != "allow" {
		t.Fatalf("expected unguarded device to allow without store policy, got %+v", unguardedDec)
	}

	dec, err := evaluateDynamicModel(
		ctx, nil, client, nil, settings, policy, workspaceID,
		"部署服务", "更新线上配置",
		nil,
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
		nil,
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
		nil,
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
		nil,
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

func TestToolGuard_CanonicalizeToolArguments(t *testing.T) {
	// PostgreSQL JSONB 会按 key 长度排序并插入空格，归一化后应与 Go json.Marshal 结果一致
	pgJSONB := []byte(`{"cmd": "sudo rm -rf /", "node_id": "node-tianyi", "nested": {"z": 1, "aa": true}}`)
	goRaw, err := json.Marshal(map[string]any{
		"node_id": "node-tianyi",
		"cmd":     "sudo rm -rf /",
		"nested":  map[string]any{"aa": true, "z": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonPG, ok1 := canonicalizeToolArguments(pgJSONB)
	canonGo, ok2 := canonicalizeToolArguments(goRaw)
	if !ok1 || !ok2 {
		t.Fatalf("expected both payloads to canonicalize cleanly, got ok1=%v ok2=%v", ok1, ok2)
	}
	if string(canonPG) != string(canonGo) {
		t.Fatalf("expected canonical JSON to match:\npg=%s\ngo=%s", string(canonPG), string(canonGo))
	}
}

func TestToolGuard_ReadOnlySubActionsStaticRules(t *testing.T) {
	allowed := []struct {
		tool string
		args map[string]any
	}{
		{"task_manage", map[string]any{"action": "list"}},
		{"task_manage", map[string]any{"action": "get"}},
		{"workflow_template_manage", map[string]any{"action": "list"}},
		{"workflow_template_manage", map[string]any{"action": "match"}},
		{"skill_manage", map[string]any{"action": "env_list"}},
		{"plugin_manage", map[string]any{"action": "inspect"}},
		{"acp_session", map[string]any{"action": "info"}},
		{"acp_session", map[string]any{"action": "list"}},
		{"acp_session", map[string]any{"action": "inspect"}},
		{"acp_interaction", map[string]any{"action": "list"}},
		{"mcp_tool_call", map[string]any{"name": "github:get_pull_request"}},
	}
	for _, tc := range allowed {
		if !isSafeReadOnlyTool(tc.tool, tc.args) {
			t.Fatalf("expected %s(%v) to be classified as safe read-only", tc.tool, tc.args)
		}
	}

	guarded := []struct {
		tool string
		args map[string]any
	}{
		{"task_manage", map[string]any{"action": "create"}},
		{"task_manage", map[string]any{"action": "complete"}},
		{"workflow_template_manage", map[string]any{"action": "publish"}},
		{"skill_manage", map[string]any{"action": "install"}},
		{"skill_manage", map[string]any{"action": "env_set"}},
		{"plugin_manage", map[string]any{"action": "install"}},
		{"acp_session", map[string]any{"action": "new"}},
		{"acp_session", map[string]any{"action": "delete"}},
		{"acp_interaction", map[string]any{"action": "respond"}},
		{"mcp_tool_call", map[string]any{"name": "github:merge_pull_request"}},
		{"cycledock_create_trigger", map[string]any{"provider": "github"}},
	}
	for _, tc := range guarded {
		if isSafeReadOnlyTool(tc.tool, tc.args) {
			t.Fatalf("expected mutating tool %s(%v) NOT to bypass guard as read-only", tc.tool, tc.args)
		}
	}
}

func TestToolGuard_SelectTrustedHumanInstructionsFilteringAndTruncation(t *testing.T) {
	longOld := strings.Repeat("旧", 700)
	longNewest := "请重启测试环境容器：" + strings.Repeat("新", 600)

	raw := []string{
		"更早的一条普通指令",
		"[系统终态回调] 子任务已完成",
		"<external_event_envelope>{\"event\":\"push\"}</external_event_envelope>",
		"[系统自动续跑] 安全守卫审批已通过，请继续执行",
		"任务「部署后端」已自动进入 Review",
		longOld,
		"  第二条有效指令\r\n\r\n\r\n请先看容器状态  ",
		"延时自唤醒已触发，请复查结果",
		longNewest,
	}

	got := selectTrustedHumanInstructions(raw, 3, 500, 1200)
	if len(got) != 3 {
		t.Fatalf("expected 3 filtered human instructions, got %d: %#v", len(got), got)
	}
	// 时间正序（从早到晚）：longOld -> 第二条有效指令 -> longNewest
	if !strings.HasPrefix(got[0], "旧") || !strings.HasSuffix(got[0], "…") {
		t.Fatalf("expected oldest kept item to be truncated longOld, got %q", got[0])
	}
	if got[1] != "第二条有效指令\n\n请先看容器状态" {
		t.Fatalf("unexpected normalized middle instruction: %q", got[1])
	}
	if !strings.HasPrefix(got[2], "请重启测试环境容器：") || !strings.HasSuffix(got[2], "…") {
		t.Fatalf("expected newest instruction to be preserved and truncated at single limit, got %q", got[2])
	}

	totalRunes := 0
	for i, item := range got {
		r := len([]rune(item))
		if r > 500 {
			t.Fatalf("item[%d] exceeds single rune limit: %d", i, r)
		}
		totalRunes += r
	}
	if totalRunes > 1200 {
		t.Fatalf("total runes %d exceeds budget 1200", totalRunes)
	}

	// 验证总预算很紧时优先保留最新一条
	tight := selectTrustedHumanInstructions([]string{
		strings.Repeat("A", 300),
		strings.Repeat("B", 300),
		"最新一条短指令",
	}, 3, 500, 100)
	if len(tight) != 2 {
		t.Fatalf("expected 2 items under tight budget, got %d: %#v", len(tight), tight)
	}
	if tight[1] != "最新一条短指令" {
		t.Fatalf("expected newest instruction intact under tight budget, got %q", tight[1])
	}
	if len([]rune(tight[0]))+len([]rune(tight[1])) > 100 {
		t.Fatalf("expected total runes <= 100, got %d", len([]rune(tight[0]))+len([]rune(tight[1])))
	}
}

func TestToolGuard_DynamicModelPromptIncludesTrustedHumanInstructionsAndFallbackSummary(t *testing.T) {
	ctx := context.Background()
	workspaceID := uuid.New()

	// 1. 验证守卫 prompt 包含最近人工原始指令与授权边界说明
	var capturedSystem, capturedUser string
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, m := range req.Messages {
			if m.Role == "system" {
				capturedSystem = m.Content
			}
			if m.Role == "user" {
				capturedUser = m.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"reversible": true, "risk_level": "low", "decision": "allow", "reason": "符合用户最近明确授权"}`,
					},
				},
			},
		})
	}))
	defer okServer.Close()

	routeOK := uuid.New()
	settings := store.AgentSettings{
		Routes: []store.ModelRoute{
			{
				ID:                routeOK,
				ApiCredentialName: "guard-ok",
				Enabled:           true,
				ProviderType:      "openai_compatible",
				ProviderBaseUrl:   okServer.URL,
				Model:             "gpt-4o-mini",
			},
		},
	}
	settings.DefaultModelRouteID = uuid.NullUUID{UUID: routeOK, Valid: true}
	policy := ScopedGuardPolicy{
		ScopeType:      "device",
		ScopeTarget:    "node-tianyi",
		Mode:           "strict",
		ModelRouteID:   uuid.NullUUID{UUID: routeOK, Valid: true},
		TimeoutSeconds: 5,
	}

	dec, err := evaluateDynamicModel(
		ctx, nil, modelclient.NewWithHTTPClient(okServer.Client()), nil,
		settings, policy, workspaceID,
		"主聊天", "项目的主聊天",
		[]string{"先帮我看一下 Docker 服务", "把 staging-api 容器重启一下"},
		"exec_command", map[string]any{"cmd": "docker restart staging-api"},
	)
	if err != nil {
		t.Fatalf("evaluateDynamicModel: %v", err)
	}
	if dec.Decision != "allow" {
		t.Fatalf("expected allow decision, got %+v", dec)
	}
	if !strings.Contains(capturedSystem, "最近人工原始指令") {
		t.Fatalf("expected systemPrompt to explain trusted human instructions, got:\n%s", capturedSystem)
	}
	if !strings.Contains(capturedUser, "最近人工原始指令（可信授权依据") ||
		!strings.Contains(capturedUser, "1. 先帮我看一下 Docker 服务") ||
		!strings.Contains(capturedUser, "2. 把 staging-api 容器重启一下") {
		t.Fatalf("expected userPrompt to contain recent human instructions in order, got:\n%s", capturedUser)
	}

	// 2. 多条启用路由全部失败时，汇总展示尝试路由数与各路由失败原因
	failRoute1Calls := 0
	failServer1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failRoute1Calls++
		http.Error(w, "route1 timeout", http.StatusGatewayTimeout)
	}))
	defer failServer1.Close()

	failRoute2Calls := 0
	failServer2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failRoute2Calls++
		http.Error(w, "route2 overloaded", http.StatusServiceUnavailable)
	}))
	defer failServer2.Close()

	route1ID := uuid.New()
	route2ID := uuid.New()
	allFailSettings := store.AgentSettings{
		Routes: []store.ModelRoute{
			{
				ID:                route1ID,
				ApiCredentialName: "PrimaryGuard",
				Enabled:           true,
				ProviderType:      "openai_compatible",
				ProviderBaseUrl:   failServer1.URL,
				Model:             "guard-fast-1",
			},
			{
				ID:                route2ID,
				ApiCredentialName: "BackupGuard",
				Enabled:           true,
				ProviderType:      "openai_compatible",
				ProviderBaseUrl:   failServer2.URL,
				Model:             "guard-backup-2",
			},
		},
	}
	allFailSettings.DefaultModelRouteID = uuid.NullUUID{UUID: route1ID, Valid: true}
	allFailPolicy := ScopedGuardPolicy{
		ScopeType:      "device",
		ScopeTarget:    "node-tianyi",
		Mode:           "strict",
		ModelRouteID:   uuid.NullUUID{UUID: route1ID, Valid: true},
		TimeoutSeconds: 5,
	}

	_, err = evaluateDynamicModel(
		ctx, nil, modelclient.NewWithHTTPClient(failServer1.Client()), nil,
		allFailSettings, allFailPolicy, workspaceID,
		"主聊天", "项目的主聊天",
		[]string{"重启一下 staging-api"},
		"exec_command", map[string]any{"cmd": "docker restart staging-api"},
	)
	if err == nil {
		t.Fatal("expected aggregated error when all guard routes fail")
	}
	if failRoute1Calls != 1 || failRoute2Calls != 1 {
		t.Fatalf("expected both routes tried once, got route1=%d route2=%d", failRoute1Calls, failRoute2Calls)
	}
	errMsg := err.Error()
	for _, want := range []string{"2 条守卫路由", "PrimaryGuard/guard-fast-1", "BackupGuard/guard-backup-2", "504", "503"} {
		if !strings.Contains(errMsg, want) {
			t.Fatalf("expected aggregated error %q to contain %q", errMsg, want)
		}
	}
}

