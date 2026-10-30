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

type ConnectPhase string

const (
	ConnectPhaseInitialize ConnectPhase = "initialize"
	ConnectPhaseListTools  ConnectPhase = "list_tools"
)

type ConnectError struct {
	Phase ConnectPhase
	Err   error
}

func (e *ConnectError) Error() string {
	return fmt.Sprintf("MCP %s failed: %v", e.Phase, e.Err)
}

func (e *ConnectError) Unwrap() error {
	return e.Err
}

type SessionSet struct {
	sessions map[string]*serverSession
	tools    map[string]Tool
	ordered  []Tool
	mu       sync.Mutex
	closed   bool
}

type serverSession struct {
	config  Connection
	session *mcp.ClientSession
}

func Connect(ctx context.Context, connections []Connection) (*SessionSet, error) {
	set := &SessionSet{
		sessions: make(map[string]*serverSession, len(connections)),
		tools:    make(map[string]Tool),
	}
	for _, connection := range connections {
		connection.Name = strings.TrimSpace(connection.Name)
		connection.URL = strings.TrimSpace(connection.URL)
		if connection.Name == "" || connection.URL == "" {
			set.Close()
			return nil, errors.New("MCP connection name and URL are required")
		}
		if connection.Timeout <= 0 {
			connection.Timeout = 30 * time.Second
		}
		client := mcp.NewClient(&mcp.Implementation{
			Name: "cycledock-agent", Title: "CycleDock Built-in Agent", Version: "0.4.0",
		}, nil)
		transport := &mcp.StreamableClientTransport{
			Endpoint:             connection.URL,
			HTTPClient:           mcpHTTPClient(connection),
			MaxRetries:           1,
			DisableStandaloneSSE: true,
		}
		connectCtx, cancel := context.WithTimeout(ctx, connection.Timeout)
		session, err := client.Connect(connectCtx, transport, nil)
		cancel()
		if err != nil {
			set.Close()
			return nil, &ConnectError{Phase: ConnectPhaseInitialize, Err: err}
		}
		set.sessions[connection.Name] = &serverSession{config: connection, session: session}

		listCtx, cancel := context.WithTimeout(ctx, connection.Timeout)
		result, err := session.ListTools(listCtx, nil)
		cancel()
		if err != nil {
			set.Close()
			return nil, &ConnectError{Phase: ConnectPhaseListTools, Err: err}
		}
		for _, remote := range result.Tools {
			if remote == nil {
				continue
			}
			name := modelToolName(connection.Name, remote.Name)
			if connection.NativeToolNames {
				name = nativeToolName(remote.Name)
			}
			if previous, exists := set.tools[name]; exists {
				set.Close()
				return nil, fmt.Errorf(
					"MCP tool name collision %q between %s/%s and %s/%s",
					name, previous.ServerName, previous.RemoteName, connection.Name, remote.Name,
				)
			}
			tool := Tool{
				ModelName: name, ServerName: connection.Name, RemoteName: remote.Name,
				Description: strings.TrimSpace(remote.Description),
				InputSchema: jsonMap(remote.InputSchema),
				Hidden:      connection.HiddenRemoteTools[remote.Name],
			}
			if tool.InputSchema == nil {
				tool.InputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			set.tools[name] = tool
			set.ordered = append(set.ordered, tool)
		}
	}
	sort.Slice(set.ordered, func(i, j int) bool { return set.ordered[i].ModelName < set.ordered[j].ModelName })
	return set, nil
}

func (s *SessionSet) ToolDefinitions() []modelclient.ToolDefinition {
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

func (s *SessionSet) HasTool(modelName string) bool {
	tool, ok := s.tools[modelName]
	return ok && !tool.Hidden
}

// Tool 返回模型工具背后的真实 MCP server/name 元数据。
// Built-in Agent 用它识别 NexusDock 第一方设备工具，并在不重做 MCP Tool Schema 合并的前提下
// 把真实执行切到 NexusDock Execution API；普通 MCP 调用继续走 SessionSet.Call。
func (s *SessionSet) Tool(modelName string) (Tool, bool) {
	tool, ok := s.tools[modelName]
	if !ok || tool.Hidden {
		return Tool{}, false
	}
	return tool, true
}

func (s *SessionSet) Call(ctx context.Context, modelName string, arguments map[string]any) (string, error) {
	tool, ok := s.tools[modelName]
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

// CallInternal invokes a discovered tool without exposing it to the model.
func (s *SessionSet) CallInternal(
	ctx context.Context,
	serverName, remoteName string,
	arguments map[string]any,
) (map[string]any, error) {
	found := false
	for _, tool := range s.ordered {
		if tool.ServerName == serverName && tool.RemoteName == remoteName {
			found = true
			break
		}
	}
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
	connected := s.sessions[serverName]
	if connected == nil {
		return nil, fmt.Errorf("MCP session %q is unavailable", serverName)
	}
	callCtx, cancel := context.WithTimeout(ctx, connected.config.Timeout)
	defer cancel()
	result, err := connected.session.CallTool(callCtx, &mcp.CallToolParams{
		Name: remoteName, Arguments: arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("call MCP %s/%s: %w", serverName, remoteName, err)
	}
	return result, nil
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
	sessions := make([]*mcp.ClientSession, 0, len(s.sessions))
	for _, connected := range s.sessions {
		if connected != nil && connected.session != nil {
			sessions = append(sessions, connected.session)
		}
	}
	s.mu.Unlock()

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
	if connection.BearerToken != "" {
		transport = &bearerTransport{
			base: transport, token: connection.BearerToken, allowedOrigin: origin,
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
	allowedOrigin string
}

func (t *bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || t.allowedOrigin == "" || endpointOrigin(request.URL.String()) != t.allowedOrigin {
		return t.base.RoundTrip(request)
	}
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
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
