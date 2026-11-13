package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/uvwt/CycleDock/internal/mcpclient"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/nexusdock"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

// ToolGuardDecision 表示安全守卫的评估结论
type ToolGuardDecision struct {
	Reversible            bool   `json:"reversible"`
	RiskLevel             string `json:"risk_level"` // "low", "medium", "high", "critical"
	Decision              string `json:"decision"`   // "allow", "ask_human"
	Reason                string `json:"reason"`
	ScopeType             string `json:"scope_type,omitempty"`
	ScopeTarget           string `json:"scope_target,omitempty"`
	ScopeLabel            string `json:"scope_label,omitempty"`
	AllowSessionWhitelist bool   `json:"allow_session_whitelist"`
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

// 检查明确不可逆的高危系统破坏性命令。
// 必须按 shell 片段与命令词法解析真实执行的命令动词和目标参数，避免被 --format、字符串参数或检索关键字误伤。
func isCriticalDestructiveCommand(toolName string, args map[string]any) (bool, string) {
	if toolName == "exec_command" {
		cmd, _ := args["cmd"].(string)
		return inspectCriticalShellCommand(cmd, 0)
	}
	if toolName == "file_edit" {
		action, _ := args["action"].(string)
		targetPath, _ := args["path"].(string)
		if action == "delete" {
			clean := strings.TrimSpace(targetPath)
			if clean == "/" || clean == "/*" || strings.EqualFold(clean, "c:\\") || strings.EqualFold(clean, "c:/") {
				return true, "尝试删除系统关键根目录"
			}
		}
	}
	return false, ""
}

const maxCriticalInspectDepth = 3

func inspectCriticalShellCommand(cmd string, depth int) (bool, string) {
	if depth > maxCriticalInspectDepth || strings.TrimSpace(cmd) == "" {
		return false, ""
	}
	// 先识别明显的 fork 炸弹特征（去除空白后匹配）
	if containsForkBomb(cmd) {
		return true, "执行命令中包含高危破坏性指令 \":(){ :|:& };:\""
	}

	segments := splitShellSegments(cmd)
	hasDBCLI := false
	for _, seg := range segments {
		tokens := tokenizeShellSegment(seg)
		if len(tokens) == 0 {
			continue
		}
		verb, args := unwrapCommandTokens(tokens)
		if verb == "" {
			continue
		}
		if isDatabaseCLICommand(verb, args) {
			hasDBCLI = true
		}

		// 若为 shell 包装执行（如 sh -c / cmd /c / powershell -Command），递归检查内层脚本
		if inner, ok := extractShellWrapperPayload(verb, args); ok {
			if crit, reason := inspectCriticalShellCommand(inner, depth+1); crit {
				return true, reason
			}
		}

		if crit, reason := inspectSingleCommandSemantics(verb, args); crit {
			return true, reason
		}
	}

	// 若整条命令链路中存在数据库客户端（如 psql -c "..." 或 echo "..." | mysql），检查是否包含高危删库/清表语句
	if hasDBCLI {
		if phrase, ok := containsCriticalSQLPhrase(cmd); ok {
			return true, fmt.Sprintf("执行命令中包含高危数据库破坏性指令 %q", phrase)
		}
	}
	return false, ""
}

func containsForkBomb(cmd string) bool {
	var b strings.Builder
	b.Grow(len(cmd))
	for _, r := range cmd {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return strings.Contains(b.String(), ":(){:|:&};:")
}

// splitShellSegments 在引号外按命令分隔符切分命令片段，并额外提取双引号或无引号中的 $(...) / `...` 子命令。
func splitShellSegments(cmd string) []string {
	var segments []string
	var subshells []string
	var cur strings.Builder

	inSingle := false
	inDouble := false
	runes := []rune(cmd)

	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			segments = append(segments, s)
		}
		cur.Reset()
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		// 仅对常见引号/转义斜杠做转义，避免破坏 Windows 路径如 C:\
		if r == '\\' && !inSingle && i+1 < len(runes) {
			next := runes[i+1]
			if next == '"' || next == '\'' || next == '\\' || next == '$' || next == '`' {
				cur.WriteRune(r)
				cur.WriteRune(next)
				i++
				continue
			}
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			cur.WriteRune(r)
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			cur.WriteRune(r)
			continue
		}

		if !inSingle {
			// 提取 $(...) 子命令内容，避免 echo "$(rm -rf /)" 绕过
			if r == '$' && i+1 < len(runes) && runes[i+1] == '(' {
				if inner, endIdx, ok := extractBalancedParen(runes, i+2); ok {
					subshells = append(subshells, inner)
					cur.WriteString(string(runes[i : endIdx+1]))
					i = endIdx
					continue
				}
			}
			// 提取反引号子命令
			if r == '`' {
				if inner, endIdx, ok := extractBacktick(runes, i+1); ok {
					subshells = append(subshells, inner)
					cur.WriteString(string(runes[i : endIdx+1]))
					i = endIdx
					continue
				}
			}
		}

		if !inSingle && !inDouble {
			if r == ';' || r == '\n' || r == '\r' {
				flush()
				continue
			}
			if r == '|' || r == '&' {
				if i+1 < len(runes) && runes[i+1] == r {
					i++
				}
				flush()
				continue
			}
		}
		cur.WriteRune(r)
	}
	flush()

	for _, sub := range subshells {
		segments = append(segments, splitShellSegments(sub)...)
	}
	return segments
}

func extractBalancedParen(runes []rune, start int) (string, int, bool) {
	depth := 1
	inSingle := false
	inDouble := false
	for i := start; i < len(runes); i++ {
		r := runes[i]
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle || inDouble {
			continue
		}
		if r == '(' {
			depth++
		} else if r == ')' {
			depth--
			if depth == 0 {
				return string(runes[start:i]), i, true
			}
		}
	}
	return "", start, false
}

func extractBacktick(runes []rune, start int) (string, int, bool) {
	for i := start; i < len(runes); i++ {
		if runes[i] == '`' {
			return string(runes[start:i]), i, true
		}
	}
	return "", start, false
}

func tokenizeShellSegment(seg string) []string {
	var tokens []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	hasToken := false

	runes := []rune(seg)
	flush := func() {
		if hasToken {
			tokens = append(tokens, cur.String())
			cur.Reset()
			hasToken = false
		}
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\\' && !inSingle && i+1 < len(runes) {
			next := runes[i+1]
			if next == '"' || next == '\'' || next == '\\' {
				cur.WriteRune(next)
				hasToken = true
				i++
				continue
			}
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			hasToken = true
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			hasToken = true
			continue
		}
		if !inSingle && !inDouble && unicode.IsSpace(r) {
			flush()
			continue
		}
		cur.WriteRune(r)
		hasToken = true
	}
	flush()
	return tokens
}

func unwrapCommandTokens(tokens []string) (string, []string) {
	i := 0
	for i < len(tokens) {
		tok := tokens[i]
		if isEnvAssignmentToken(tok) {
			i++
			continue
		}
		base := normalizedCommandName(tok)
		switch base {
		case "sudo", "doas":
			i++
			for i < len(tokens) {
				flag := tokens[i]
				if flag == "--" {
					i++
					break
				}
				if !strings.HasPrefix(flag, "-") {
					break
				}
				i++
				if isSudoValueFlag(flag) && i < len(tokens) {
					i++
				}
			}
		case "env":
			i++
			for i < len(tokens) {
				arg := tokens[i]
				if arg == "--" {
					i++
					break
				}
				if isEnvAssignmentToken(arg) {
					i++
					continue
				}
				if strings.HasPrefix(arg, "-") {
					i++
					if (arg == "-u" || arg == "--unset" || arg == "-C" || arg == "--chdir") && i < len(tokens) {
						i++
					}
					continue
				}
				break
			}
		case "nice":
			i++
			for i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
				flag := tokens[i]
				i++
				if flag == "-n" && i < len(tokens) {
					i++
				}
			}
		case "nohup", "command", "builtin", "exec", "time":
			i++
			for i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
				i++
			}
		default:
			return base, tokens[i+1:]
		}
	}
	return "", nil
}

func isEnvAssignmentToken(tok string) bool {
	if tok == "" || strings.HasPrefix(tok, "-") {
		return false
	}
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	name := tok[:eq]
	return !strings.ContainsAny(name, `/\`)
}

func isSudoValueFlag(flag string) bool {
	switch flag {
	case "-u", "-g", "-C", "-h", "-p", "-r", "-t", "-U", "-T":
		return true
	default:
		return false
	}
}

func normalizedCommandName(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}
	if idx := strings.LastIndexAny(cleaned, `/\`); idx >= 0 {
		cleaned = cleaned[idx+1:]
	}
	lower := strings.ToLower(cleaned)
	for _, ext := range []string{".exe", ".com", ".cmd", ".bat"} {
		if strings.HasSuffix(lower, ext) {
			lower = strings.TrimSuffix(lower, ext)
			break
		}
	}
	return lower
}

func extractShellWrapperPayload(verb string, args []string) (string, bool) {
	switch verb {
	case "sh", "bash", "zsh", "dash", "ash", "ksh":
		for i := 0; i < len(args); i++ {
			a := args[i]
			if a == "--" {
				break
			}
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], 'c') {
				if i+1 < len(args) {
					return args[i+1], true
				}
			}
			if a == "--command" && i+1 < len(args) {
				return args[i+1], true
			}
		}
	case "cmd":
		for i := 0; i < len(args); i++ {
			lower := strings.ToLower(args[i])
			if lower == "/c" || lower == "/k" {
				if i+1 < len(args) {
					return strings.Join(args[i+1:], " "), true
				}
			}
		}
	case "powershell", "pwsh":
		for i := 0; i < len(args); i++ {
			lower := strings.ToLower(args[i])
			if lower == "-c" || lower == "-command" {
				if i+1 < len(args) {
					return strings.Join(args[i+1:], " "), true
				}
			}
		}
	}
	return "", false
}

func inspectSingleCommandSemantics(verb string, args []string) (bool, string) {
	switch verb {
	case "rm":
		if isCriticalRMCommand(args) {
			return true, "执行命令中包含高危破坏性指令 \"rm -rf /\""
		}
	case "rmdir", "rd":
		if isCriticalWindowsDirDelete(args, false) {
			return true, "执行命令中包含高危破坏性指令 \"rmdir /s /q 根目录\""
		}
	case "del", "erase":
		if isCriticalWindowsDirDelete(args, true) {
			return true, "执行命令中包含高危破坏性指令 \"del /f /s /q 根目录\""
		}
	case "format":
		// 只有作为独立命令动词且目标为磁盘盘符/卷时才视为磁盘格式化，避免误伤 --format 参数
		if isCriticalDiskFormatCommand(args) {
			return true, "执行命令中包含高危破坏性指令 \"format\""
		}
	case "dd":
		for _, a := range args {
			lower := strings.ToLower(strings.TrimSpace(a))
			if strings.HasPrefix(lower, "of=/dev/") || strings.HasPrefix(lower, `of=\\.\`) {
				return true, "执行命令中包含高危破坏性指令 \"dd of=/dev/\""
			}
		}
	case "chmod":
		if isCriticalChmodCommand(args) {
			return true, "执行命令中包含高危破坏性指令 \"chmod -R 777 /\""
		}
	case "shutdown", "reboot", "poweroff", "halt":
		return true, fmt.Sprintf("执行命令中包含高危系统控制指令 %q", verb)
	case "init":
		for _, a := range args {
			if a == "0" || a == "6" {
				return true, fmt.Sprintf("执行命令中包含高危系统控制指令 \"init %s\"", a)
			}
		}
	default:
		if verb == "mkfs" || strings.HasPrefix(verb, "mkfs.") {
			return true, fmt.Sprintf("执行命令中包含高危破坏性指令 %q", verb)
		}
	}
	return false, ""
}

func isCriticalRMCommand(args []string) bool {
	recursive := false
	force := false
	noPreserveRoot := false
	stopFlags := false
	var targets []string

	for _, a := range args {
		if !stopFlags && a == "--" {
			stopFlags = true
			continue
		}
		if !stopFlags && strings.HasPrefix(a, "-") && a != "-" {
			lower := strings.ToLower(a)
			switch lower {
			case "--recursive":
				recursive = true
			case "--force":
				force = true
			case "--no-preserve-root":
				noPreserveRoot = true
			default:
				if !strings.HasPrefix(lower, "--") {
					if strings.ContainsRune(lower[1:], 'r') {
						recursive = true
					}
					if strings.ContainsRune(lower[1:], 'f') {
						force = true
					}
				}
			}
			continue
		}
		targets = append(targets, a)
	}

	if !recursive || (!force && !noPreserveRoot) {
		return false
	}
	for _, t := range targets {
		if isPOSIXRootTarget(t) {
			return true
		}
	}
	return false
}

func isPOSIXRootTarget(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return false
	}
	cleaned := path.Clean(trimmed)
	return cleaned == "/" || cleaned == "/*"
}

func isCriticalWindowsDirDelete(args []string, requireForce bool) bool {
	recursive := false
	forceOrQuiet := false
	var targets []string

	for _, a := range args {
		lower := strings.ToLower(strings.TrimSpace(a))
		switch lower {
		case "/s", "-s":
			recursive = true
		case "/f", "-f", "/q", "-q":
			forceOrQuiet = true
		default:
			if strings.HasPrefix(lower, "/") && !strings.Contains(lower, "\\") && !strings.Contains(lower, ":") {
				continue
			}
			targets = append(targets, a)
		}
	}
	if !recursive || (requireForce && !forceOrQuiet) {
		return false
	}
	for _, t := range targets {
		if isWindowsDriveRoot(t) {
			return true
		}
	}
	return false
}

func isWindowsDriveRoot(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if len(lower) < 2 || lower[0] < 'a' || lower[0] > 'z' || lower[1] != ':' {
		return false
	}
	rest := strings.Trim(lower[2:], `/\`)
	return rest == "" || rest == "*"
}

func isCriticalDiskFormatCommand(args []string) bool {
	for _, a := range args {
		lower := strings.ToLower(strings.TrimSpace(a))
		if len(lower) >= 2 && lower[0] >= 'a' && lower[0] <= 'z' && lower[1] == ':' {
			rest := strings.Trim(lower[2:], `/\`)
			if rest == "" {
				return true
			}
		}
		if strings.HasPrefix(lower, `\\.\`) || strings.HasPrefix(lower, "/fs:") {
			return true
		}
	}
	return false
}

func isCriticalChmodCommand(args []string) bool {
	recursive := false
	mode777 := false
	var targets []string
	stopFlags := false

	for _, a := range args {
		if !stopFlags && a == "--" {
			stopFlags = true
			continue
		}
		if !stopFlags && strings.HasPrefix(a, "-") && a != "-" {
			lower := strings.ToLower(a)
			if lower == "--recursive" || (!strings.HasPrefix(lower, "--") && strings.ContainsRune(lower[1:], 'r')) {
				recursive = true
			}
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(a))
		if !mode777 && (lower == "777" || lower == "0777" || lower == "a+rwx" || lower == "ugo+rwx") {
			mode777 = true
			continue
		}
		targets = append(targets, a)
	}
	if !recursive || !mode777 {
		return false
	}
	for _, t := range targets {
		if isPOSIXRootTarget(t) {
			return true
		}
	}
	return false
}

func isDatabaseCLICommand(verb string, args []string) bool {
	switch verb {
	case "psql", "mysql", "mariadb", "sqlite3", "sqlcmd", "mongosh", "clickhouse-client", "cockroach":
		return true
	case "docker", "kubectl", "podman":
		// 例如 docker exec pg psql -c "DROP DATABASE ..."
		for _, a := range args {
			sub := normalizedCommandName(a)
			switch sub {
			case "psql", "mysql", "mariadb", "sqlite3", "sqlcmd", "mongosh", "clickhouse-client", "cockroach":
				return true
			}
		}
	}
	return false
}

func containsCriticalSQLPhrase(raw string) (string, bool) {
	lower := strings.ToLower(raw)
	phrases := []string{"drop database", "drop schema", "truncate table"}
	for _, phrase := range phrases {
		if containsWordBoundedPhrase(lower, phrase) {
			return phrase, true
		}
	}
	return "", false
}

func containsWordBoundedPhrase(haystack, phrase string) bool {
	start := 0
	for {
		idx := strings.Index(haystack[start:], phrase)
		if idx < 0 {
			return false
		}
		pos := start + idx
		end := pos + len(phrase)
		leftOK := pos == 0 || !isIdentifierChar(rune(haystack[pos-1]))
		rightOK := end == len(haystack) || !isIdentifierChar(rune(haystack[end]))
		if leftOK && rightOK {
			return true
		}
		start = pos + len(phrase)
	}
}

func isIdentifierChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
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

// ToolGuardScope 描述被守卫工具在当前卡片内的临时白名单作用域（设备 / MCP / 兜底工具名）
type ToolGuardScope struct {
	ScopeType   string // "device" | "mcp" | "tool"
	ScopeTarget string // device_node_id | MCP 连接名 | tool_name
	ScopeLabel  string // 用于前端展示的设备名 / MCP 连接名 / 工具名
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

func isDeviceScopedToolName(toolName string) bool {
	switch toolName {
	case "exec_command", "file_edit", "session_act", "file_publish",
		"skill_manage", "plugin_manage", "mcp_manage", "mcp_tool_call",
		"node_tool_call", "task_create", "task_manage", "evolve":
		return true
	default:
		return strings.HasPrefix(toolName, "browser_") || strings.HasPrefix(toolName, "acp_")
	}
}

// resolveToolGuardScope 识别工具调用归属的作用域：MCP 连接 > 设备节点 > 兜底工具名。
func resolveToolGuardScope(
	ctx context.Context,
	storeInstance *store.Store,
	workspaceID uuid.UUID,
	cardDeviceNodeID string,
	deviceNames map[string]string,
	toolName string,
	args map[string]any,
) ToolGuardScope {
	trimmedTool := strings.TrimSpace(toolName)

	// 1. 外部 MCP 工具：按 MCP 连接名归属（大小写不敏感匹配工作区连接名以保证白名单一致）
	if trimmedTool == "call_mcp_tool" {
		toolID, _ := args["tool_id"].(string)
		serverName, _, ok := mcpclient.ParseToolID(toolID)
		serverName = strings.TrimSpace(serverName)
		if ok && serverName != "" {
			canonicalName := serverName
			if storeInstance != nil {
				if conns, err := storeInstance.ListMCPConnections(ctx, workspaceID); err == nil {
					for _, c := range conns {
						if strings.EqualFold(c.Name, serverName) {
							canonicalName = c.Name
							break
						}
					}
				}
			}
			return ToolGuardScope{
				ScopeType:   "mcp",
				ScopeTarget: canonicalName,
				ScopeLabel:  canonicalName,
			}
		}
		return ToolGuardScope{
			ScopeType:   "tool",
			ScopeTarget: trimmedTool,
			ScopeLabel:  trimmedTool,
		}
	}

	// 2. 设备类工具：优先读取显式 node_id 参数，否则对设备类工具继承本轮卡片设备目标
	targetNodeID, _ := args["node_id"].(string)
	targetNodeID = strings.TrimSpace(targetNodeID)
	if targetNodeID == "" && isDeviceScopedToolName(trimmedTool) {
		targetNodeID = strings.TrimSpace(cardDeviceNodeID)
	}
	if targetNodeID != "" {
		label := strings.TrimSpace(deviceNames[targetNodeID])
		if label == "" && storeInstance != nil {
			if guard, err := storeInstance.GetDeviceToolGuard(ctx, workspaceID, targetNodeID); err == nil {
				label = strings.TrimSpace(guard.DeviceName)
			}
		}
		return ToolGuardScope{
			ScopeType:   "device",
			ScopeTarget: targetNodeID,
			ScopeLabel:  label,
		}
	}

	// 3. 无法归属到具体设备或 MCP 的工具：保留按工具名放行的安全兜底
	return ToolGuardScope{
		ScopeType:   "tool",
		ScopeTarget: trimmedTool,
		ScopeLabel:  trimmedTool,
	}
}

// resolveScopedGuardPolicyFromScope 根据已解析的作用域查找对应的设备或 MCP 守卫策略
func resolveScopedGuardPolicyFromScope(
	ctx context.Context,
	storeInstance *store.Store,
	workspaceID uuid.UUID,
	scope ToolGuardScope,
) (ScopedGuardPolicy, bool) {
	if storeInstance == nil {
		return ScopedGuardPolicy{}, false
	}

	switch scope.ScopeType {
	case "mcp":
		conns, err := storeInstance.ListMCPConnections(ctx, workspaceID)
		if err == nil {
			for _, c := range conns {
				if strings.EqualFold(c.Name, scope.ScopeTarget) {
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
	case "device":
		guard, err := storeInstance.GetDeviceToolGuard(ctx, workspaceID, scope.ScopeTarget)
		if err == nil {
			return ScopedGuardPolicy{
				ScopeType:      "device",
				ScopeTarget:    scope.ScopeTarget,
				Mode:           guard.Mode,
				ModelRouteID:   guard.ModelRouteID,
				TimeoutSeconds: guard.TimeoutSeconds,
				CustomRules:    guard.CustomRules,
			}, true
		}
		return ScopedGuardPolicy{
			ScopeType:      "device",
			ScopeTarget:    scope.ScopeTarget,
			Mode:           "disabled",
			TimeoutSeconds: 10,
		}, true
	default:
		return ScopedGuardPolicy{}, false
	}
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
	scope := resolveToolGuardScope(ctx, storeInstance, workspaceID, cardDeviceNodeID, nil, toolName, args)
	return resolveScopedGuardPolicyFromScope(ctx, storeInstance, workspaceID, scope)
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
	deviceNames map[string]string,
	cardTitle, cardDescription string,
	toolName string,
	args map[string]any,
) ToolGuardDecision {
	// ---------------- 第一层：静态规则过滤 ----------------
	// 1. 只读及系统内部协同工具放行（0 延迟、0 Token 消耗，直接放行）
	if isSafeReadOnlyTool(toolName, args) {
		return ToolGuardDecision{
			Reversible:            true,
			RiskLevel:             "low",
			Decision:              "allow",
			Reason:                "只读或系统协同工具无破坏副作用，直接安全放行",
			AllowSessionWhitelist: true,
		}
	}

	scope := resolveToolGuardScope(ctx, storeInstance, workspaceID, cardDeviceNodeID, deviceNames, toolName, args)

	// 2. 明确高危系统级破坏命令（如 rm -rf /、drop database）必须位于白名单之前，
	// 确保即便卡片已临时允许某设备，高危系统级破坏命令仍强制人工确认且不允许加入临时白名单。
	if isCrit, critReason := isCriticalDestructiveCommand(toolName, args); isCrit {
		return ToolGuardDecision{
			Reversible:            false,
			RiskLevel:             "critical",
			Decision:              "ask_human",
			Reason:                critReason,
			ScopeType:             scope.ScopeType,
			ScopeTarget:           scope.ScopeTarget,
			ScopeLabel:            scope.ScopeLabel,
			AllowSessionWhitelist: false,
		}
	}

	// 3. 卡片临时白名单检查（当前卡片 + 设备 / MCP / 兜底工具）及单次已批准防死循环检查
	if storeInstance != nil {
		if hasApprovedMatchingToolCall(ctx, storeInstance, workspaceID, cardID, toolName, args) {
			return ToolGuardDecision{
				Reversible:            true,
				RiskLevel:             "low",
				Decision:              "allow",
				Reason:                "该工具调用已获人工批准，放行执行",
				ScopeType:             scope.ScopeType,
				ScopeTarget:           scope.ScopeTarget,
				ScopeLabel:            scope.ScopeLabel,
				AllowSessionWhitelist: true,
			}
		}
		whitelisted, err := storeInstance.IsCardScopeWhitelisted(ctx, workspaceID, cardID, scope.ScopeType, scope.ScopeTarget)
		if err == nil && whitelisted {
			return ToolGuardDecision{
				Reversible:            true,
				RiskLevel:             "low",
				Decision:              "allow",
				Reason:                "本卡片已临时允许该作用域操作，自动放行",
				ScopeType:             scope.ScopeType,
				ScopeTarget:           scope.ScopeTarget,
				ScopeLabel:            scope.ScopeLabel,
				AllowSessionWhitelist: true,
			}
		}
	}

	// 4. 解析对应的具体设备或 MCP 连接级独立安全策略
	policy, ok := resolveScopedGuardPolicyFromScope(ctx, storeInstance, workspaceID, scope)
	if !ok || policy.Mode == "disabled" {
		return ToolGuardDecision{
			Reversible:            true,
			RiskLevel:             "low",
			Decision:              "allow",
			Reason:                "目标设备或 MCP 连接未启用安全守卫，予以放行",
			ScopeType:             scope.ScopeType,
			ScopeTarget:           scope.ScopeTarget,
			ScopeLabel:            scope.ScopeLabel,
			AllowSessionWhitelist: true,
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
			Reversible:            false,
			RiskLevel:             "high",
			Decision:              "ask_human",
			Reason:                fmt.Sprintf("安全守卫小模型评估异常（%v），依据安全兜底策略挂起等待人工确认", dynamicErr),
			ScopeType:             scope.ScopeType,
			ScopeTarget:           scope.ScopeTarget,
			ScopeLabel:            scope.ScopeLabel,
			AllowSessionWhitelist: true,
		}
	}

	dynamicDecision.ScopeType = scope.ScopeType
	dynamicDecision.ScopeTarget = scope.ScopeTarget
	dynamicDecision.ScopeLabel = scope.ScopeLabel
	dynamicDecision.AllowSessionWhitelist = true

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
	route store.ModelRoute,
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

	// 按设备/MCP 指定路由 -> 工作区默认路由 -> 其余启用路由排序，失败时自动顺延
	orderedRoutes := prioritizeRoutes(
		settings.Routes,
		policy.ModelRouteID,
		uuid.NullUUID{},
		settings.DefaultModelRouteID,
	)
	enabledRoutes := make([]store.ModelRoute, 0, len(orderedRoutes))
	for _, route := range orderedRoutes {
		if route.Enabled {
			enabledRoutes = append(enabledRoutes, route)
		}
	}
	if len(enabledRoutes) == 0 {
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

	var lastErr error
	for i, route := range enabledRoutes {
		if ctx.Err() != nil {
			return ToolGuardDecision{}, ctx.Err()
		}

		decision, err := evaluateGuardRoute(
			ctx,
			storeInstance,
			modelClient,
			modelAuthService,
			workspaceID,
			route,
			timeout,
			systemPrompt,
			userPrompt,
		)
		if err == nil {
			return decision, nil
		}

		lastErr = err
		if ctx.Err() != nil {
			return ToolGuardDecision{}, ctx.Err()
		}
		if i+1 < len(enabledRoutes) {
			slog.Warn("tool guard route evaluation failed, falling back to next route",
				"tool_name", toolName,
				"scope_type", policy.ScopeType,
				"scope_target", policy.ScopeTarget,
				"failed_route_id", route.ID,
				"failed_model", route.Model,
				"error", err,
			)
		}
	}

	return ToolGuardDecision{}, lastErr
}

func evaluateGuardRoute(
	ctx context.Context,
	storeInstance *store.Store,
	modelClient *modelclient.Client,
	modelAuthService *modelauth.Service,
	workspaceID uuid.UUID,
	route store.ModelRoute,
	timeout time.Duration,
	systemPrompt, userPrompt string,
) (ToolGuardDecision, error) {
	evalCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg, err := resolveGuardRouteConfig(evalCtx, storeInstance, modelAuthService, workspaceID, route, timeout)
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
	deviceNames map[string]string,
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

		// 执行安全守卫评估（支持继承 cardContext.DeviceNodeID 与设备名展示）
		decision := EvaluateToolGuard(
			ctx,
			runner.store,
			runner.model,
			runner.modelAuth,
			settings,
			cardContext.WorkspaceID,
			cardContext.CardID,
			cardContext.DeviceNodeID,
			deviceNames,
			cardContext.CardTitle,
			cardContext.CardDescription,
			toolName,
			args,
		)

		if decision.Decision == "allow" {
			// 放行：返回 nil, nil 允许 ADK 执行该工具
			return nil, nil
		}

		allowSessionWhitelist := decision.AllowSessionWhitelist
		// 判定为需要人工确认：挂起当前执行
		approval, err := runner.store.CreateToolApproval(ctx, store.CreateToolApprovalParams{
			WorkspaceID:           cardContext.WorkspaceID,
			CardID:                cardContext.CardID,
			TurnID:                &cardContext.TurnID,
			AttemptID:             &cardContext.AttemptID,
			ToolCallID:            ctx.FunctionCallID(),
			ToolName:              toolName,
			Arguments:             args,
			RiskLevel:             decision.RiskLevel,
			Reversible:            decision.Reversible,
			Reason:                decision.Reason,
			ScopeType:             decision.ScopeType,
			ScopeTarget:           decision.ScopeTarget,
			ScopeLabel:            decision.ScopeLabel,
			AllowSessionWhitelist: &allowSessionWhitelist,
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
