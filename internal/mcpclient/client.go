package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/CycleDock/internal/modelclient"
)

const (
	maxMCPResponseBytes     int64 = 4 << 20
	maxModelToolResultBytes       = 256 << 10
)

type Connection struct {
	Name              string
	URL               string
	BearerToken       string
	// BearerTokenFunc 在每次发出 MCP HTTP 请求前按需获取最新 Token（用于 OAuth 长会话自动续期）。
	BearerTokenFunc   func(ctx context.Context) (string, error)
	// RefreshBearerFunc 在收到 HTTP 401 时强制刷新 Token 并重放一次请求。
	RefreshBearerFunc func(ctx context.Context) (string, error)
	Timeout           time.Duration
	NativeToolNames   bool
	HiddenRemoteTools map[string]bool
}

type Tool struct {
	ModelName   string
	ServerName  string
	RemoteName  string
	Description string
	InputSchema map[string]any
	Hidden      bool
}

// FormatToolID returns the stable tool identifier for external MCP tools.
func FormatToolID(serverName, remoteName string) string {
	return strings.TrimSpace(serverName) + ":" + strings.TrimSpace(remoteName)
}

// ParseToolID parses a stable tool identifier, supporting both colon (server:tool)
// and slash (server/tool) separators.
func ParseToolID(id string) (serverName, remoteName string, ok bool) {
	id = strings.TrimSpace(id)
	if s, r, found := strings.Cut(id, ":"); found && s != "" && r != "" {
		return s, r, true
	}
	if s, r, found := strings.Cut(id, "/"); found && s != "" && r != "" {
		return s, r, true
	}
	return "", "", false
}

// ToolID returns the stable identifier of this tool.
func (t Tool) ToolID() string {
	return FormatToolID(t.ServerName, t.RemoteName)
}

type ConnectPhase string

const (
	ConnectPhaseInitialize ConnectPhase = "initialize"
	ConnectPhaseListTools  ConnectPhase = "list_tools"
)

type ConnectError struct {
	ServerName string
	Phase      ConnectPhase
	Err        error
}

func (e *ConnectError) Error() string {
	return fmt.Sprintf("MCP %q %s failed: %v", e.ServerName, e.Phase, e.Err)
}

func (e *ConnectError) Unwrap() error {
	return e.Err
}

// SessionSet 管理一组已接入的 MCP 服务会话与统一工具目录。
//
// 锁约束：
// 1. s.mu 保护 sessions、tools、ordered 与 closed。
// 2. serverSession.mu 保护单个服务的 session 指针读写及重连串行化；config 在创建后只读。
// 3. 严禁在持有 s.mu 的同时获取 serverSession.mu，避免与重连路径形成 AB-BA 死锁。
type SessionSet struct {
	sessions map[string]*serverSession
	tools    map[string]Tool
	ordered  []Tool
	mu       sync.Mutex
	closed   bool
}

type serverSession struct {
	mu      sync.Mutex
	config  Connection
	session *mcp.ClientSession
}

func Connect(ctx context.Context, connections []Connection) (*SessionSet, error) {
	set := &SessionSet{
		sessions: make(map[string]*serverSession, len(connections)),
		tools:    make(map[string]Tool),
	}
	for _, connection := range connections {
		if err := set.AddConnection(ctx, connection); err != nil {
			set.Close()
			return nil, err
		}
	}
	return set, nil
}

func dialSession(ctx context.Context, connection Connection) (*mcp.ClientSession, error) {
	timeout := connection.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name: "cycledock-agent", Title: "CycleDock Built-in Agent", Version: "0.4.0",
	}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint: connection.URL, HTTPClient: mcpHTTPClient(connection),
		MaxRetries: 1, DisableStandaloneSSE: true,
	}
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, &ConnectError{ServerName: connection.Name, Phase: ConnectPhaseInitialize, Err: err}
	}
	return session, nil
}

func buildServerTools(connection Connection, existing map[string]Tool, remoteTools []*mcp.Tool) (map[string]Tool, error) {
	additions := make(map[string]Tool, len(remoteTools))
	for _, remote := range remoteTools {
		if remote == nil {
			continue
		}
		name := modelToolName(connection.Name, remote.Name)
		if connection.NativeToolNames {
			name = nativeToolName(remote.Name)
		}
		if prev, exists := existing[name]; exists && prev.ServerName != connection.Name {
			return nil, fmt.Errorf("MCP tool name collision %q between %s/%s and %s/%s", name, prev.ServerName, prev.RemoteName, connection.Name, remote.Name)
		}
		if prev, exists := additions[name]; exists {
			return nil, fmt.Errorf("MCP tool name collision %q between %s/%s and %s/%s", name, prev.ServerName, prev.RemoteName, connection.Name, remote.Name)
		}
		tool := Tool{
			ModelName:   name,
			ServerName:  connection.Name,
			RemoteName:  remote.Name,
			Description: strings.TrimSpace(remote.Description),
			InputSchema: jsonMap(remote.InputSchema),
			Hidden:      connection.HiddenRemoteTools[remote.Name],
		}
		if tool.InputSchema == nil {
			tool.InputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		additions[name] = tool
	}
	return additions, nil
}

func (s *SessionSet) replaceServerToolsLocked(serverName string, additions map[string]Tool) {
	newOrdered := make([]Tool, 0, len(s.ordered)+len(additions))
	for _, t := range s.ordered {
		if t.ServerName != serverName {
			newOrdered = append(newOrdered, t)
		} else {
			delete(s.tools, t.ModelName)
		}
	}
	for name, tool := range additions {
		s.tools[name] = tool
		newOrdered = append(newOrdered, tool)
	}
	sort.Slice(newOrdered, func(i, j int) bool { return newOrdered[i].ModelName < newOrdered[j].ModelName })
	s.ordered = newOrdered
}

// AddConnection 独立接入一个服务；失败时不影响已连接服务，也不留下半份工具目录。
// Connect 保持全量成功语义，运行时则可逐个接入并向用户报告不可用的服务。
func (s *SessionSet) AddConnection(ctx context.Context, connection Connection) error {
	connection.Name = strings.TrimSpace(connection.Name)
	connection.URL = strings.TrimSpace(connection.URL)
	if connection.Name == "" || connection.URL == "" {
		return errors.New("MCP connection name and URL are required")
	}
	if connection.Timeout <= 0 {
		connection.Timeout = 30 * time.Second
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("MCP session set is closed")
	}
	if _, exists := s.sessions[connection.Name]; exists {
		s.mu.Unlock()
		return fmt.Errorf("MCP connection %q already exists", connection.Name)
	}
	s.mu.Unlock()

	session, err := dialSession(ctx, connection)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = session.Close()
		}
	}()

	listCtx, cancel := context.WithTimeout(ctx, connection.Timeout)
	result, err := session.ListTools(listCtx, nil)
	cancel()
	if err != nil {
		return &ConnectError{ServerName: connection.Name, Phase: ConnectPhaseListTools, Err: err}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("MCP session set is closed")
	}
	if _, exists := s.sessions[connection.Name]; exists {
		return fmt.Errorf("MCP connection %q already exists", connection.Name)
	}
	additions, err := buildServerTools(connection, s.tools, result.Tools)
	if err != nil {
		return err
	}
	s.sessions[connection.Name] = &serverSession{config: connection, session: session}
	s.replaceServerToolsLocked(connection.Name, additions)
	committed = true
	return nil
}

// RemoveConnection 同时撤下工具并关闭会话，避免接入后的 bootstrap 失败留下可调用工具。
func (s *SessionSet) RemoveConnection(name string) error {
	s.mu.Lock()
	server := s.sessions[name]
	delete(s.sessions, name)
	kept := s.ordered[:0]
	for _, tool := range s.ordered {
		if tool.ServerName == name {
			delete(s.tools, tool.ModelName)
		} else {
			kept = append(kept, tool)
		}
	}
	s.ordered = kept
	s.mu.Unlock()

	if server == nil {
		return nil
	}
	server.mu.Lock()
	sess := server.session
	server.session = nil
	server.mu.Unlock()
	if sess != nil {
		return sess.Close()
	}
	return nil
}

func (s *SessionSet) ToolDefinitions() []modelclient.ToolDefinition {
	s.mu.Lock()
	defer s.mu.Unlock()
	definitions := make([]modelclient.ToolDefinition, 0, len(s.ordered))
	for _, tool := range s.ordered {
		if tool.Hidden {
			continue
		}
		description := tool.Description
		if description == "" {
			description = "MCP tool " + tool.RemoteName + " from " + tool.ServerName
		}
		definitions = append(definitions, modelclient.ToolDefinition{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name: tool.ModelName, Description: description, Parameters: tool.InputSchema,
			},
		})
	}
	return definitions
}

// NativeToolDefinitions returns tool definitions only for first-party/native tools
// (e.g. NexusDock tools) that should be mounted directly onto the agent.
func (s *SessionSet) NativeToolDefinitions() []modelclient.ToolDefinition {
	s.mu.Lock()
	defer s.mu.Unlock()
	definitions := make([]modelclient.ToolDefinition, 0, len(s.ordered))
	for _, tool := range s.ordered {
		if tool.Hidden {
			continue
		}
		session, ok := s.sessions[tool.ServerName]
		if !ok || !session.config.NativeToolNames {
			continue
		}
		description := tool.Description
		if description == "" {
			description = "MCP tool " + tool.RemoteName
		}
		definitions = append(definitions, modelclient.ToolDefinition{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name: tool.ModelName, Description: description, Parameters: tool.InputSchema,
			},
		})
	}
	return definitions
}

// ExternalTools returns all visible tools from external/third-party MCP connections.
func (s *SessionSet) ExternalTools() []Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Tool, 0, len(s.ordered))
	for _, tool := range s.ordered {
		if tool.Hidden {
			continue
		}
		session, ok := s.sessions[tool.ServerName]
		if ok && session.config.NativeToolNames {
			continue
		}
		out = append(out, tool)
	}
	return out
}

// GetExternalTool retrieves an external tool by connection name and remote tool name.
func (s *SessionSet) GetExternalTool(serverName, remoteName string) (Tool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tool := range s.ordered {
		if !tool.Hidden && strings.EqualFold(tool.ServerName, serverName) && tool.RemoteName == remoteName {
			session, ok := s.sessions[tool.ServerName]
			if ok && session.config.NativeToolNames {
				continue
			}
			return tool, true
		}
	}
	return Tool{}, false
}

// GetExternalToolByID retrieves an external tool using a stable tool_id (supporting server:tool or server/tool).
func (s *SessionSet) GetExternalToolByID(toolID string) (Tool, bool) {
	serverName, remoteName, ok := ParseToolID(toolID)
	if !ok {
		return Tool{}, false
	}
	return s.GetExternalTool(serverName, remoteName)
}

func (s *SessionSet) HasTool(modelName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	tool, ok := s.tools[modelName]
	return ok && !tool.Hidden
}

func (s *SessionSet) Call(ctx context.Context, modelName string, arguments map[string]any) (string, error) {
	s.mu.Lock()
	tool, ok := s.tools[modelName]
	s.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("unknown MCP tool %q", modelName)
	}
	if tool.Hidden {
		return "", fmt.Errorf("MCP tool %q is runtime-only", modelName)
	}
	result, err := s.callRemote(ctx, tool.ServerName, tool.RemoteName, arguments)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode MCP result %s/%s: %w", tool.ServerName, tool.RemoteName, err)
	}
	return boundedModelResult(raw), nil
}

// CallExternal invokes an external MCP tool by its server and remote name.
func (s *SessionSet) CallExternal(
	ctx context.Context,
	serverName, remoteName string,
	arguments map[string]any,
) (string, error) {
	tool, found := s.GetExternalTool(serverName, remoteName)
	if !found {
		return "", fmt.Errorf("external MCP tool %s/%s not found", serverName, remoteName)
	}
	result, err := s.callRemote(ctx, tool.ServerName, tool.RemoteName, arguments)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode MCP result %s/%s: %w", tool.ServerName, tool.RemoteName, err)
	}
	return boundedModelResult(raw), nil
}

// CallInternal invokes a discovered tool without exposing it to the model.
func (s *SessionSet) CallInternal(
	ctx context.Context,
	serverName, remoteName string,
	arguments map[string]any,
) (map[string]any, error) {
	s.mu.Lock()
	found := false
	for _, tool := range s.ordered {
		if tool.ServerName == serverName && tool.RemoteName == remoteName {
			found = true
			break
		}
	}
	s.mu.Unlock()
	if !found {
		return nil, fmt.Errorf("MCP runtime tool %s/%s is unavailable", serverName, remoteName)
	}
	result, err := s.callRemote(ctx, serverName, remoteName, arguments)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode MCP runtime result %s/%s: %w", serverName, remoteName, err)
	}
	var output map[string]any
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil, fmt.Errorf("decode MCP runtime result %s/%s: %w", serverName, remoteName, err)
	}
	return output, nil
}

func (s *SessionSet) callRemote(
	ctx context.Context,
	serverName, remoteName string,
	arguments map[string]any,
) (*mcp.CallToolResult, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("MCP session set is closed")
	}
	server := s.sessions[serverName]
	s.mu.Unlock()
	if server == nil {
		return nil, fmt.Errorf("MCP session %q is unavailable", serverName)
	}

	timeout := server.config.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	const maxAttempts = 3
	reconnected := false
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		server.mu.Lock()
		currentSession := server.session
		server.mu.Unlock()

		if currentSession == nil {
			if err := s.reconnectServer(ctx, serverName, nil); err != nil {
				return nil, fmt.Errorf("reconnect MCP %s before call: %w", serverName, err)
			}
			reconnected = true
			server.mu.Lock()
			currentSession = server.session
			server.mu.Unlock()
			if currentSession == nil {
				return nil, fmt.Errorf("MCP session %q unavailable after reconnect", serverName)
			}
		}

		callCtx, cancel := context.WithTimeout(ctx, timeout)
		result, err := currentSession.CallTool(callCtx, &mcp.CallToolParams{
			Name: remoteName, Arguments: arguments,
		})
		category := classifyCallError(ctx, callCtx, err)
		cancel()

		if err == nil {
			return result, nil
		}

		switch category {
		case CategoryCallerCanceled:
			return nil, err

		case CategoryTimeout:
			// 单次工具超时不主动拆毁会话，也不重试慢工具；
			// 若此次超时发生在读取响应体阶段导致 go-sdk 将连接置为 failed，下一轮调用会在入口自动识别并重连。
			return nil, fmt.Errorf("call MCP %s/%s timed out: %w", serverName, remoteName, err)

		case CategoryConnectionClosed:
			// 请求尚未被服务端执行，最多触发一次重连并重发。
			if !reconnected && attempt+1 < maxAttempts && ctx.Err() == nil {
				if reconnErr := s.reconnectServer(ctx, serverName, currentSession); reconnErr == nil {
					reconnected = true
					continue
				}
			}
			return nil, fmt.Errorf("call MCP %s/%s connection closed: %w", serverName, remoteName, err)

		case CategoryConnectionDropped:
			// 响应读取中途中断，服务端可能已执行副作用；淘汰已损坏的底层会话，但禁止静默重发本次调用。
			s.invalidateSession(serverName, currentSession)
			return nil, fmt.Errorf("call MCP %s/%s connection dropped during response: %w", serverName, remoteName, err)

		case CategoryTransient:
			if attempt+1 < maxAttempts && ctx.Err() == nil {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(100*(1<<attempt)) * time.Millisecond):
					continue
				}
			}
			return nil, fmt.Errorf("call MCP %s/%s transient error: %w", serverName, remoteName, err)

		default:
			if strings.Contains(strings.ToLower(err.Error()), "mcp response exceeds ") {
				s.invalidateSession(serverName, currentSession)
			}
			return nil, fmt.Errorf("call MCP %s/%s: %w", serverName, remoteName, err)
		}
	}

	return nil, fmt.Errorf("call MCP %s/%s exceeded max attempts", serverName, remoteName)
}

func (s *SessionSet) invalidateSession(serverName string, broken *mcp.ClientSession) {
	if broken == nil {
		return
	}
	s.mu.Lock()
	server := s.sessions[serverName]
	s.mu.Unlock()
	if server == nil {
		return
	}

	server.mu.Lock()
	if server.session != broken {
		server.mu.Unlock()
		return
	}
	server.session = nil
	server.mu.Unlock()

	go func(old *mcp.ClientSession) {
		_ = old.Close()
	}(broken)
}

// reconnectServer 串行化单个 MCP 服务的重连过程，传入触发失败的旧 session 以合并并发重连请求。
func (s *SessionSet) reconnectServer(ctx context.Context, serverName string, failed *mcp.ClientSession) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("MCP session set is closed")
	}
	server := s.sessions[serverName]
	s.mu.Unlock()
	if server == nil {
		return fmt.Errorf("MCP session %q is unavailable", serverName)
	}

	server.mu.Lock()
	defer server.mu.Unlock()

	// 若排在前面的并发协程已经换上了新会话，直接复用，避免惊群重连并误杀健康会话。
	if server.session != nil && server.session != failed {
		return nil
	}

	s.mu.Lock()
	closedOrRemoved := s.closed || s.sessions[serverName] != server
	s.mu.Unlock()
	if closedOrRemoved {
		return errors.New("MCP session set is closed")
	}

	newSession, err := dialSession(ctx, server.config)
	if err != nil {
		return err
	}

	// 趁新会话建立同步刷新工具目录；若刷新失败不阻断本次重连，后续按需搜索/校验仍可再刷新。
	listCtx, cancel := context.WithTimeout(ctx, server.config.Timeout)
	listResult, listErr := newSession.ListTools(listCtx, nil)
	cancel()

	s.mu.Lock()
	if s.closed || s.sessions[serverName] != server {
		s.mu.Unlock()
		_ = newSession.Close()
		return errors.New("MCP session set is closed")
	}
	if listErr == nil && listResult != nil {
		if additions, err := buildServerTools(server.config, s.tools, listResult.Tools); err == nil {
			s.replaceServerToolsLocked(server.config.Name, additions)
		}
	}
	s.mu.Unlock()

	oldSession := server.session
	server.session = newSession
	if oldSession != nil {
		go func(old *mcp.ClientSession) {
			_ = old.Close()
		}(oldSession)
	}
	return nil
}

// RefreshServer 重新拉取指定服务的工具列表并原子更新目录（含跨服务工具名冲突校验）。
func (s *SessionSet) RefreshServer(ctx context.Context, serverName string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("MCP session set is closed")
	}
	server := s.sessions[serverName]
	s.mu.Unlock()
	if server == nil {
		return fmt.Errorf("MCP session %q is unavailable", serverName)
	}

	server.mu.Lock()
	currentSession := server.session
	server.mu.Unlock()
	if currentSession == nil {
		if err := s.reconnectServer(ctx, serverName, nil); err != nil {
			return fmt.Errorf("refresh MCP tools for %q: %w", serverName, err)
		}
		server.mu.Lock()
		currentSession = server.session
		server.mu.Unlock()
		if currentSession == nil {
			return fmt.Errorf("MCP session %q is unavailable", serverName)
		}
	}

	listCtx, cancel := context.WithTimeout(ctx, server.config.Timeout)
	result, err := currentSession.ListTools(listCtx, nil)
	cancel()
	if err != nil {
		return fmt.Errorf("refresh MCP tools for %q: %w", serverName, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.sessions[serverName] != server {
		return errors.New("MCP session set is closed")
	}
	additions, err := buildServerTools(server.config, s.tools, result.Tools)
	if err != nil {
		return err
	}
	s.replaceServerToolsLocked(server.config.Name, additions)
	return nil
}

// RefreshTools re-lists tools from all active server sessions.
func (s *SessionSet) RefreshTools(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("MCP session set is closed")
	}
	names := make([]string, 0, len(s.sessions))
	for name := range s.sessions {
		names = append(names, name)
	}
	s.mu.Unlock()

	var errs []error
	for _, name := range names {
		if err := s.RefreshServer(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func boundedModelResult(raw []byte) string {
	if len(raw) <= maxModelToolResultBytes {
		return string(raw)
	}
	preview := raw[:maxModelToolResultBytes]
	for len(preview) > 0 && !utf8.Valid(preview) {
		preview = preview[:len(preview)-1]
	}
	truncated, _ := json.Marshal(map[string]any{
		"truncated": true,
		"message":   "MCP result exceeded CycleDock's per-tool model-context limit.",
		"preview":   string(preview),
	})
	return string(truncated)
}

func (s *SessionSet) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	servers := make([]*serverSession, 0, len(s.sessions))
	for _, connected := range s.sessions {
		if connected != nil {
			servers = append(servers, connected)
		}
	}
	s.mu.Unlock()

	sessions := make([]*mcp.ClientSession, 0, len(servers))
	for _, srv := range servers {
		srv.mu.Lock()
		if srv.session != nil {
			sessions = append(sessions, srv.session)
			srv.session = nil
		}
		srv.mu.Unlock()
	}

	var combined error
	for _, session := range sessions {
		combined = errors.Join(combined, session.Close())
	}
	return combined
}

func modelToolName(serverName, remoteName string) string {
	base := "mcp_" + safeName(serverName) + "_" + safeName(remoteName)
	if len(base) <= 64 {
		return base
	}
	sum := sha256.Sum256([]byte(serverName + "\x00" + remoteName))
	suffix := "_" + hex.EncodeToString(sum[:4])
	return strings.TrimRight(base[:64-len(suffix)], "_") + suffix
}

func nativeToolName(remoteName string) string {
	base := safeName(remoteName)
	if len(base) <= 64 {
		return base
	}
	sum := sha256.Sum256([]byte(remoteName))
	suffix := "_" + hex.EncodeToString(sum[:4])
	return strings.TrimRight(base[:64-len(suffix)], "_") + suffix
}

func safeName(value string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.TrimSpace(value) {
		valid := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
		if valid && r <= unicode.MaxASCII {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	result := strings.Trim(b.String(), "_")
	if result == "" {
		return "tool"
	}
	return result
}

func mcpHTTPClient(connection Connection) *http.Client {
	origin := endpointOrigin(connection.URL)
	var transport http.RoundTripper = &boundedTransport{
		base: http.DefaultTransport, maxBytes: maxMCPResponseBytes,
	}
	if connection.BearerToken != "" || connection.BearerTokenFunc != nil {
		transport = &bearerTransport{
			base:          transport,
			token:         connection.BearerToken,
			tokenFunc:     connection.BearerTokenFunc,
			refreshFunc:   connection.RefreshBearerFunc,
			allowedOrigin: origin,
		}
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many MCP redirects")
			}
			if endpointOrigin(request.URL.String()) != origin {
				return errors.New("cross-origin MCP redirect refused")
			}
			return nil
		},
	}
}

func endpointOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}

type bearerTransport struct {
	base          http.RoundTripper
	token         string
	tokenFunc     func(ctx context.Context) (string, error)
	refreshFunc   func(ctx context.Context) (string, error)
	allowedOrigin string
}

func (t *bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || t.allowedOrigin == "" || endpointOrigin(request.URL.String()) != t.allowedOrigin {
		return t.base.RoundTrip(request)
	}
	token := t.token
	if t.tokenFunc != nil {
		var err error
		token, err = t.tokenFunc(request.Context())
		if err != nil {
			return nil, fmt.Errorf("resolve MCP bearer token: %w", err)
		}
	}
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+token)
	resp, err := t.base.RoundTrip(clone)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || t.refreshFunc == nil {
		return resp, err
	}
	if request.Body != nil && request.GetBody == nil {
		return resp, nil
	}
	refreshedToken, refreshErr := t.refreshFunc(request.Context())
	if refreshErr != nil || refreshedToken == "" {
		return resp, nil
	}
	_ = resp.Body.Close()
	retryReq := request.Clone(request.Context())
	retryReq.Header = request.Header.Clone()
	if request.GetBody != nil {
		bodyCopy, bodyErr := request.GetBody()
		if bodyErr != nil {
			return nil, bodyErr
		}
		retryReq.Body = bodyCopy
	}
	retryReq.Header.Set("Authorization", "Bearer "+refreshedToken)
	return t.base.RoundTrip(retryReq)
}

type boundedTransport struct {
	base     http.RoundTripper
	maxBytes int64
}

func (t *boundedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if strings.EqualFold(mediaType, "text/event-stream") {
		response.Body = &boundedSSEReadCloser{ReadCloser: response.Body, maxEventBytes: t.maxBytes}
		return response, nil
	}
	if response.ContentLength > t.maxBytes {
		_ = response.Body.Close()
		return nil, fmt.Errorf("MCP response exceeds %d bytes", t.maxBytes)
	}
	response.Body = &boundedReadCloser{ReadCloser: response.Body, remaining: t.maxBytes, limit: t.maxBytes}
	return response, nil
}

type boundedReadCloser struct {
	io.ReadCloser
	remaining int64
	limit     int64
}

func (r *boundedReadCloser) Read(p []byte) (int, error) {
	if r.remaining < 0 {
		return 0, fmt.Errorf("MCP response exceeds %d bytes", r.limit)
	}
	if int64(len(p)) > r.remaining+1 {
		p = p[:r.remaining+1]
	}
	n, err := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	if r.remaining < 0 {
		return 0, fmt.Errorf("MCP response exceeds %d bytes", r.limit)
	}
	return n, err
}

type boundedSSEReadCloser struct {
	io.ReadCloser
	maxEventBytes int64
	eventBytes    int64
	lineHasData   bool
}

func (r *boundedSSEReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	for _, b := range p[:n] {
		r.eventBytes++
		if b == '\n' {
			if !r.lineHasData {
				r.eventBytes = 0
			}
			r.lineHasData = false
		} else if b != '\r' {
			r.lineHasData = true
		}
		if r.eventBytes > r.maxEventBytes {
			return 0, fmt.Errorf("MCP SSE event exceeds %d bytes", r.maxEventBytes)
		}
	}
	return n, err
}

func jsonMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var output map[string]any
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil
	}
	return output
}
