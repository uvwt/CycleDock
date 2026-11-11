package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/uvwt/CycleDock/internal/mcpclient"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/nexusdock"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

// ToolGuardDecision 表示安全守卫的评估结论
type ToolGuardDecision struct {
	Reversible bool   `json:"reversible"`
	RiskLevel  string `json:"risk_level"` // "low", "medium", "high", "critical"
	Decision   string `json:"decision"`   // "allow", "ask_human"
	Reason     string `json:"reason"`
}

// 只读及系统内部协同工具清单（0 延迟、0 Token 消耗直接放行）
var safeReadOnlyToolNames = map[string]bool{
	"reply_message":                    true,
	"search_mcp_tools":                 true,
	"cycledock_list_schedules":         true,
	"cycledock_create_schedule":        true,
	"cycledock_update_schedule":        true,
	"cycledock_delete_schedule":        true,
	"cycledock_check_in":               true,
	"cycledock_generate_work":          true,
	"cycledock_confirm_card":           true,
	"cycledock_send_card_message":      true,
	"cycledock_inspect_card_session":   true,
	"cycledock_read_artifact":          true,
	"cycledock_search_session_history": true,
	"read_file":                        true,
	"list_dir":                         true,
	"search_text":                      true,
	"recall_search":                    true,
	"recall_read":                      true,
	"session_observe":                  true,
	"browser_snapshot":                 true,
	"view_image":                       true,
	"node_tool_search":                 true,
	"mcp_tool_search":                  true,
	"mcp_tool_inspect":                 true,
	"workspace_context":                true,
}

// 快速只读或系统内部工具判断（包含外部 MCP 只读启发式）
func isSafeReadOnlyTool(toolName string, args map[string]any) bool {
	if safeReadOnlyToolNames[toolName] || strings.HasPrefix(toolName, "cycledock_") {
		return true
	}
	// 针对 call_mcp_tool，如果 remote tool name 是明显只读的
	if toolName == "call_mcp_tool" {
		toolID, _ := args["tool_id"].(string)
		parts := strings.Split(toolID, ":")
		if len(parts) == 2 {
			remoteName := strings.ToLower(parts[1])
			if strings.HasPrefix(remoteName, "get_") ||
				strings.HasPrefix(remoteName, "list_") ||
				strings.HasPrefix(remoteName, "read_") ||
				strings.HasPrefix(remoteName, "search_") ||
				strings.HasPrefix(remoteName, "fetch_") ||
				strings.HasPrefix(remoteName, "inspect_") ||
				strings.HasPrefix(remoteName, "query_") ||
				strings.HasPrefix(remoteName, "view_") {
				return true
			}
		}
	}
	return false
}

// 检查明确不可逆的高危系统破坏性命令
func isCriticalDestructiveCommand(toolName string, args map[string]any) (bool, string) {
	if toolName == "exec_command" {
		cmd, _ := args["cmd"].(string)
		lower := strings.ToLower(cmd)
		criticalPatterns := []string{
			"rm -rf /", "rm -rf /*", "rmdir /s /q c:\\", "del /f /s /q c:\\",
			"format ", "mkfs.", "dd if=", "drop database", "drop schema",
			"truncate table", "shutdown", "reboot", "init 0", "init 6",
			":(){ :|:& };:", "chmod -r 777 /",
		}
		for _, pat := range criticalPatterns {
			if strings.Contains(lower, pat) {
				return true, fmt.Sprintf("执行命令中包含高危破坏性指令 %q", pat)
			}
		}
	}
	if toolName == "file_edit" {
		action, _ := args["action"].(string)
		path, _ := args["path"].(string)
		if action == "delete" {
			clean := strings.TrimSpace(path)
			if clean == "/" || clean == "/*" || strings.EqualFold(clean, "c:\\") || strings.EqualFold(clean, "c:/") {
				return true, "尝试删除系统关键根目录"
			}
		}
	}
	return false, ""
}

// hasApprovedMatchingToolCall 检查当前卡片中是否已经有相同工具名与参数的已批准记录（防止 approve_once 后模型重试死循环）
func hasApprovedMatchingToolCall(
	ctx context.Context,
	storeInstance *store.Store,
	workspaceID, cardID uuid.UUID,
	toolName string,
	args map[string]any,
) bool {
	if storeInstance == nil {
		return false
	}
	approvals, err := storeInstance.ListCardToolApprovals(ctx, workspaceID, cardID)
	if err != nil || len(approvals) == 0 {
		return false
	}
	targetRaw, err := json.Marshal(args)
	if err != nil {
		targetRaw = []byte("{}")
	}
	for _, app := range approvals {
		if app.Status == "approved" && app.ToolName == toolName {
			if bytes.Equal(bytes.TrimSpace(app.Arguments), bytes.TrimSpace(targetRaw)) {
				return true
			}
		}
	}
	return false
}

// ScopedGuardPolicy 描述具体执行目标（设备或具体 MCP 连接）的独立守卫策略
type ScopedGuardPolicy struct {
	ScopeType      string // "device" | "mcp"
	ScopeTarget    string // device_node_id 或 connection_name
	Mode           string // "disabled" | "block_high_risk" | "strict"
	ModelRouteID   uuid.NullUUID
	TimeoutSeconds int32
	CustomRules    string
}

// resolveScopedGuardPolicy 根据即将调用的工具类型与入参，精准解析对应的设备级或 MCP 级策略
func resolveScopedGuardPolicy(
	ctx context.Context,
	storeInstance *store.Store,
	workspaceID uuid.UUID,
	cardDeviceNodeID string,
	toolName string,
	args map[string]any,
) (ScopedGuardPolicy, bool) {
	if storeInstance == nil {
		return ScopedGuardPolicy{}, false
	}

	// 1. 如果是 call_mcp_tool
	if toolName == "call_mcp_tool" {
		toolID, _ := args["tool_id"].(string)
		serverName, _, ok := mcpclient.ParseToolID(toolID)
		if !ok || serverName == "" {
			return ScopedGuardPolicy{}, false
		}
		conns, err := storeInstance.ListMCPConnections(ctx, workspaceID)
		if err == nil {
			for _, c := range conns {
				if strings.EqualFold(c.Name, serverName) {
					return ScopedGuardPolicy{
						ScopeType:      "mcp",
						ScopeTarget:    c.Name,
						Mode:           c.ToolGuardMode,
						ModelRouteID:   c.ToolGuardModelRouteID,
						TimeoutSeconds: c.ToolGuardTimeoutSeconds,
						CustomRules:    c.ToolGuardCustomRules,
					}, true
				}
			}
		}
		return ScopedGuardPolicy{}, false
	}

	// 2. 如果是针对设备的工具（例如包含 node_id 参数，或者继承本轮 cardDeviceNodeID）
	targetNodeID, _ := args["node_id"].(string)
	targetNodeID = strings.TrimSpace(targetNodeID)
	if targetNodeID == "" {
		targetNodeID = strings.TrimSpace(cardDeviceNodeID)
	}

	// 设备级工具判断：具备 node_id 或者是常见的设备执行/文件/进程类工具
	isDeviceTool := targetNodeID != "" || toolName == "exec_command" || toolName == "file_edit" || toolName == "session_act" || strings.HasPrefix(toolName, "browser_")

	if isDeviceTool && targetNodeID != "" {
		guard, err := storeInstance.GetDeviceToolGuard(ctx, workspaceID, targetNodeID)
		if err == nil {
			return ScopedGuardPolicy{
				ScopeType:      "device",
				ScopeTarget:    targetNodeID,
				Mode:           guard.Mode,
				ModelRouteID:   guard.ModelRouteID,
				TimeoutSeconds: guard.TimeoutSeconds,
				CustomRules:    guard.CustomRules,
			}, true
		}
		// 若该设备尚未单独配置，默认使用推荐的 block_high_risk 兜底
		return ScopedGuardPolicy{
			ScopeType:      "device",
			ScopeTarget:    targetNodeID,
			Mode:           "block_high_risk",
			TimeoutSeconds: 10,
		}, true
	}

	return ScopedGuardPolicy{}, false
}

// EvaluateToolGuard 执行三层守卫流水线评估（基于设备与 MCP 连接精准独立判定）
func EvaluateToolGuard(
	ctx context.Context,
	storeInstance *store.Store,
	modelClient *modelclient.Client,
	modelAuthService *modelauth.Service,
	settings store.AgentSettings,
	workspaceID, cardID uuid.UUID,
	cardDeviceNodeID string,
	cardTitle, cardDescription string,
	toolName string,
	args map[string]any,
) ToolGuardDecision {
	// ---------------- 第一层：静态规则过滤 ----------------
	// 1. 只读及系统内部协同工具放行（0 延迟、0 Token 消耗，直接放行）
	if isSafeReadOnlyTool(toolName, args) {
		return ToolGuardDecision{
			Reversible: true,
			RiskLevel:  "low",
			Decision:   "allow",
			Reason:     "只读或系统协同工具无破坏副作用，直接安全放行",
		}
	}

	// 2. 卡片内白名单规则检查（用户交互“同类操作不再询问”）或已批准的完全相同操作重试
	if storeInstance != nil {
		whitelisted, err := storeInstance.IsCardToolWhitelisted(ctx, workspaceID, cardID, toolName)
		if err == nil && whitelisted {
			return ToolGuardDecision{
				Reversible: true,
				RiskLevel:  "low",
				Decision:   "allow",
				Reason:     "本卡片已将同类工具操作加入临时白名单，自动放行",
			}
		}
		if hasApprovedMatchingToolCall(ctx, storeInstance, workspaceID, cardID, toolName, args) {
			return ToolGuardDecision{
				Reversible: true,
				RiskLevel:  "low",
				Decision:   "allow",
				Reason:     "该工具调用已获人工批准，放行执行",
			}
		}
	}

	// 3. 明确高危系统级破坏命令（如 rm -rf /、drop database）无论设备配置如何直接必审
	if isCrit, critReason := isCriticalDestructiveCommand(toolName, args); isCrit {
		return ToolGuardDecision{
			Reversible: false,
			RiskLevel:  "critical",
			Decision:   "ask_human",
			Reason:     critReason,
		}
	}

	// 4. 解析对应的具体设备或 MCP 连接级独立安全策略
	policy, ok := resolveScopedGuardPolicy(ctx, storeInstance, workspaceID, cardDeviceNodeID, toolName, args)
	if !ok || policy.Mode == "disabled" {
		return ToolGuardDecision{
			Reversible: true,
			RiskLevel:  "low",
			Decision:   "allow",
			Reason:     "目标设备或 MCP 连接未启用安全守卫，予以放行",
		}
	}

	// ---------------- 第二层：小模型动态语义风险评估 ----------------
	dynamicDecision, dynamicErr := evaluateDynamicModel(
		ctx,
		storeInstance,
		modelClient,
		modelAuthService,
		settings,
		policy,
		workspaceID,
		cardTitle,
		cardDescription,
		toolName,
		args,
	)

	// ---------------- 第三层：降级兜底 ----------------
	if dynamicErr != nil {
		slog.Warn("tool guard dynamic evaluation failed, falling back to ask_human",
			"tool_name", toolName,
			"scope_type", policy.ScopeType,
			"scope_target", policy.ScopeTarget,
			"error", dynamicErr,
		)
		return ToolGuardDecision{
			Reversible: false,
			RiskLevel:  "high",
			Decision:   "ask_human",
			Reason:     fmt.Sprintf("安全守卫小模型评估异常（%v），依据安全兜底策略挂起等待人工确认", dynamicErr),
		}
	}

	// 根据目标设备或 MCP 的具体模式最终确定决策
	if policy.Mode == "block_high_risk" {
		// 仅拦截高危且不可逆操作或显式要求人工确认
		if dynamicDecision.Decision == "ask_human" {
			if dynamicDecision.RiskLevel == "high" || dynamicDecision.RiskLevel == "critical" || !dynamicDecision.Reversible {
				return dynamicDecision
			}
			dynamicDecision.Decision = "allow"
			dynamicDecision.Reason = "在仅拦截高危不可逆模式下，评估为中低风险或可逆操作，予以放行"
			return dynamicDecision
		}
		return dynamicDecision
	}

	// strict 模式：只要小模型认为是 ask_human 或者风险为 medium 及以上，均拦截
	if dynamicDecision.RiskLevel == "high" || dynamicDecision.RiskLevel == "critical" || dynamicDecision.RiskLevel == "medium" {
		dynamicDecision.Decision = "ask_human"
	}

	return dynamicDecision
}

// resolveGuardRouteConfig 解析评估小模型的请求配置（支持 openai_compatible 以及 chatgpt/grok OAuth 路由）
func resolveGuardRouteConfig(
	ctx context.Context,
	storeInstance *store.Store,
	modelAuthService *modelauth.Service,
	workspaceID uuid.UUID,
	route db.WorkspaceModelRoute,
	timeout time.Duration,
) (modelclient.Config, error) {
	switch route.ProviderType {
	case "", "openai_compatible":
		var apiKey string
		if len(route.ApiKeyCiphertext) > 0 && storeInstance != nil {
			var err error
			apiKey, err = storeInstance.ModelRouteAPIKey(route)
			if err != nil {
				return modelclient.Config{}, fmt.Errorf("decrypt guard model API key: %w", err)
			}
		} else if route.ApiKeyEnv != "" {
			var err error
			apiKey, err = secretFromEnv(route.ApiKeyEnv, "model API key")
			if err != nil {
				return modelclient.Config{}, err
			}
		}
		return modelclient.Config{
			BaseURL:     route.ProviderBaseUrl,
			Model:       route.Model,
			APIKey:      apiKey,
			Timeout:     timeout,
			Temperature: 0.1,
		}, nil
	case string(modelauth.ProviderChatGPT):
		if modelAuthService == nil || !route.CredentialID.Valid {
			return modelclient.Config{}, modelauth.ErrNotConnected
		}
		cfg, err := modelAuthService.ModelConfigForCredential(
			ctx, workspaceID, modelauth.ProviderChatGPT,
			route.CredentialID.UUID, route.Model, timeout,
		)
		if err != nil {
			return modelclient.Config{}, err
		}
		cfg.Temperature = 0.1
		return cfg, nil
	case string(modelauth.ProviderGrok):
		if modelAuthService == nil || !route.CredentialID.Valid {
			return modelclient.Config{}, modelauth.ErrNotConnected
		}
		cfg, err := modelAuthService.ModelConfigForCredential(
			ctx, workspaceID, modelauth.ProviderGrok,
			route.CredentialID.UUID, route.Model, timeout,
		)
		if err != nil {
			return modelclient.Config{}, err
		}
		cfg.Temperature = 0.1
		return cfg, nil
	default:
		return modelclient.Config{}, fmt.Errorf("unsupported model provider %q", route.ProviderType)
	}
}

// evaluateDynamicModel 负责组装并调用小模型进行语义分析
func evaluateDynamicModel(
	ctx context.Context,
	storeInstance *store.Store,
	modelClient *modelclient.Client,
	modelAuthService *modelauth.Service,
	settings store.AgentSettings,
	policy ScopedGuardPolicy,
	workspaceID uuid.UUID,
	cardTitle, cardDescription string,
	toolName string,
	args map[string]any,
) (ToolGuardDecision, error) {
	if modelClient == nil {
		return ToolGuardDecision{}, errors.New("model client is not available")
	}

	// 确定评估使用的模型路由：优先使用该设备或 MCP 绑定的模型路由
	var selectedRoute *db.WorkspaceModelRoute
	if policy.ModelRouteID.Valid {
		for i := range settings.Routes {
			if settings.Routes[i].ID == policy.ModelRouteID.UUID && settings.Routes[i].Enabled {
				selectedRoute = &settings.Routes[i]
				break
			}
		}
	}
	if selectedRoute == nil {
		// 回退到默认启用的第一条路由
		for i := range settings.Routes {
			if settings.Routes[i].Enabled {
				selectedRoute = &settings.Routes[i]
				break
			}
		}
	}
	if selectedRoute == nil {
		return ToolGuardDecision{}, errors.New("no enabled model route configured for tool guard")
	}

	timeoutSec := int(policy.TimeoutSeconds)
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	timeout := time.Duration(timeoutSec) * time.Second

	argsJSON, _ := json.MarshalIndent(args, "", "  ")

	scopeDesc := fmt.Sprintf("设备节点: %s", policy.ScopeTarget)
	if policy.ScopeType == "mcp" {
		scopeDesc = fmt.Sprintf("MCP 外部服务: %s", policy.ScopeTarget)
	}

	systemPrompt := fmt.Sprintf(`你是 CycleDock 的轻量级「工具调用安全守卫（Tool Guard）」。
当前审查目标作用域：%s
你的核心任务是审查 Agent 即将针对该目标调用的工具与其入参，评估操作安全风险，决定是否允许直接执行或必须挂起等待人工确认。

【评估维度】
1. reversible（布尔值）：操作是否可安全回滚/撤销（例如读取或添加临时文件为 true；删除数据、覆写关键代码、执行外部有副作用命令为 false）。
2. risk_level（字符串）：风险级别，必须是 "low" | "medium" | "high" | "critical" 之一。
3. decision（字符串）：决策结果，必须是 "allow"（放行）或 "ask_human"（拦截等待人工确认）。
4. reason（字符串）：简明扼要的一两句中文解释，说明为什么做出该决策与潜在风险点。

【该目标独立自定义守卫规则】
%s

【输出规范】
必须仅输出标准 JSON 格式，不要输出任何额外的 Markdown 或解释文字：
{
  "reversible": false,
  "risk_level": "medium",
  "decision": "ask_human",
  "reason": "..."
}`, scopeDesc, policy.CustomRules)

	userPrompt := fmt.Sprintf(`请评估以下工具调用：
当前卡片目标：
标题：%s
描述：%s

即将调用的工具：
工具名：%s
参数：
%s`, cardTitle, cardDescription, toolName, string(argsJSON))

	evalCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg, err := resolveGuardRouteConfig(evalCtx, storeInstance, modelAuthService, workspaceID, *selectedRoute, timeout)
	if err != nil {
		return ToolGuardDecision{}, fmt.Errorf("resolve guard model route config: %w", err)
	}

	completion, err := modelClient.Complete(evalCtx, cfg, []modelclient.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, nil)
	if err != nil {
		return ToolGuardDecision{}, err
	}

	// 提取 JSON 并解析
	cleanJSON := extractJSONContent(completion.Content)
	var decision ToolGuardDecision
	if err := json.Unmarshal([]byte(cleanJSON), &decision); err != nil {
		return ToolGuardDecision{}, fmt.Errorf("parse model decision JSON: %w (raw: %s)", err, completion.Content)
	}

	decision.Decision = strings.ToLower(strings.TrimSpace(decision.Decision))
	decision.RiskLevel = strings.ToLower(strings.TrimSpace(decision.RiskLevel))
	decision.Reason = strings.TrimSpace(decision.Reason)

	if decision.Decision != "allow" && decision.Decision != "ask_human" {
		decision.Decision = "ask_human"
	}
	switch decision.RiskLevel {
	case "low", "medium", "high", "critical":
	default:
		decision.RiskLevel = "medium"
	}

	return decision, nil
}

// 辅助清洗提取 JSON 内容
func extractJSONContent(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 2 {
			end := len(lines) - 1
			if strings.HasPrefix(strings.TrimSpace(lines[end]), "```") {
				return strings.Join(lines[1:end], "\n")
			}
			return strings.Join(lines[1:], "\n")
		}
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		return trimmed[start : end+1]
	}
	return trimmed
}

// ToolGuardCallback 返回可直接注入 ADK BeforeToolCallbacks 的安全拦截钩子
func ToolGuardCallback(
	runner *Runner,
	live *realtime.Hub,
	cardContext store.AgentCardContext,
	settings store.AgentSettings,
	replies *replyState,
) func(ctx agent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
	return func(ctx agent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		replies.mu.Lock()
		ended := replies.ended
		replies.mu.Unlock()
		if ended {
			ctx.Actions().SkipSummarization = true
			return map[string]any{"error": "turn already ended"}, nil
		}

		toolName := t.Name()

		// 执行安全守卫评估（支持继承 cardContext.DeviceNodeID）
		decision := EvaluateToolGuard(
			ctx,
			runner.store,
			runner.model,
			runner.modelAuth,
			settings,
			cardContext.WorkspaceID,
			cardContext.CardID,
			cardContext.DeviceNodeID,
			cardContext.CardTitle,
			cardContext.CardDescription,
			toolName,
			args,
		)

		if decision.Decision == "allow" {
			// 放行：返回 nil, nil 允许 ADK 执行该工具
			return nil, nil
		}

		// 判定为需要人工确认：挂起当前执行
		approval, err := runner.store.CreateToolApproval(ctx, store.CreateToolApprovalParams{
			WorkspaceID: cardContext.WorkspaceID,
			CardID:      cardContext.CardID,
			TurnID:      &cardContext.TurnID,
			AttemptID:   &cardContext.AttemptID,
			ToolCallID:  ctx.FunctionCallID(),
			ToolName:    toolName,
			Arguments:   args,
			RiskLevel:   decision.RiskLevel,
			Reversible:  decision.Reversible,
			Reason:      decision.Reason,
		})
		if err != nil {
			return nil, fmt.Errorf("create tool approval record: %w", err)
		}

		// 发布 tool_approval 事件到会话 Timeline
		eventSummary := fmt.Sprintf("工具「%s」调用已被安全守卫拦截并挂起（风险等级：%s，原因：%s）。等待人工确认后恢复执行。",
			toolName, decision.RiskLevel, decision.Reason)

		eventID, err := runner.store.PublishToolApprovalEvent(ctx, store.PublishToolApprovalEventParams{
			WorkspaceID:    cardContext.WorkspaceID,
			CardID:         cardContext.CardID,
			AttemptID:      cardContext.AttemptID,
			RunnerSourceID: cardContext.RunnerSourceID,
			ApprovalID:     approval.ID,
			Content:        eventSummary,
		})
		if err != nil {
			runner.logger.Error("failed to publish tool approval event", "error", err)
		}

		// 实时通知前端刷新卡片
		if live != nil {
			live.Publish(cardContext.CardID, realtime.Event{
				Type:      "card.updated",
				AttemptID: cardContext.AttemptID.String(),
				EventID:   eventID.String(),
			})
		}

		// 标记本轮结束，阻止后续同批工具产生副作用
		replies.mu.Lock()
		replies.ended = true
		replies.mu.Unlock()
		ctx.Actions().SkipSummarization = true

		// 返回拦截响应给 ADK 记录 FunctionResponse，安全完成本轮 Attempt
		return map[string]any{
			"status":      "suspended",
			"approval_id": approval.ID.String(),
			"message":     "此工具调用已被安全守卫拦截并挂起，等待人工审核确认后恢复执行。",
		}, nil
	}
}

// ExecuteApprovedTool 执行被审批通过的工具调用（与 openADKMCPSessions 保持一致的鉴权与刷新能力）
func ExecuteApprovedTool(
	ctx context.Context,
	storeInstance *store.Store,
	nexusDock *nexusdock.Service,
	workspaceID uuid.UUID,
	toolName string,
	arguments map[string]any,
) (string, error) {
	connections, err := storeInstance.ListEnabledMCPConnections(ctx, workspaceID)
	if err != nil {
		return "", fmt.Errorf("load MCP connections: %w", err)
	}
	sessions, err := mcpclient.Connect(ctx, nil)
	if err != nil {
		return "", err
	}
	defer sessions.Close()

	for _, connection := range connections {
		token, err := storeInstance.MCPBearerToken(connection)
		if err == nil {
			_ = sessions.AddConnection(ctx, mcpclient.Connection{
				Name: connection.Name, URL: connection.Url, BearerToken: token,
				Timeout: time.Duration(connection.TimeoutMs) * time.Millisecond,
			})
		}
	}

	if nexusDock != nil {
		if connection, err := nexusDock.Runtime(ctx, workspaceID); err == nil {
			_ = sessions.AddConnection(ctx, mcpclient.Connection{
				Name:        firstPartyNexusDockServerName,
				URL:         connection.URL,
				BearerToken: connection.AccessToken,
				BearerTokenFunc: func(reqCtx context.Context) (string, error) {
					conn, err := nexusDock.Runtime(reqCtx, workspaceID)
					if err != nil {
						return "", err
					}
					return conn.AccessToken, nil
				},
				RefreshBearerFunc: func(reqCtx context.Context) (string, error) {
					conn, err := nexusDock.ForceRefresh(reqCtx, workspaceID)
					if err != nil {
						return "", err
					}
					return conn.AccessToken, nil
				},
				Timeout:           30 * time.Second,
				NativeToolNames:   true,
				HiddenRemoteTools: map[string]bool{"agentdock_context": true},
			})
		}
	}

	if toolName == "call_mcp_tool" {
		toolID, _ := arguments["tool_id"].(string)
		toolArgs, _ := arguments["arguments"].(map[string]any)
		if toolArgs == nil {
			toolArgs = map[string]any{}
		}
		serverName, remoteName, ok := mcpclient.ParseToolID(toolID)
		if !ok {
			return "", fmt.Errorf("invalid tool_id %q", toolID)
		}
		res, err := sessions.CallExternal(ctx, serverName, remoteName, toolArgs)
		if err != nil {
			return "", err
		}
		resJSON, _ := json.MarshalIndent(res, "", "  ")
		return string(resJSON), nil
	}

	res, err := sessions.Call(ctx, toolName, arguments)
	if err == nil {
		resJSON, _ := json.MarshalIndent(res, "", "  ")
		return string(resJSON), nil
	}

	return "", fmt.Errorf("tool %q execution failed: %w", toolName, err)
}
