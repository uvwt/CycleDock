package mcpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConnectDiscoverAndCallUsesBearer(t *testing.T) {
	var bearerSeen atomic.Bool
	server := mcp.NewServer(&mcp.Implementation{
		Name: "test-mcp", Version: "1.0.0",
	}, nil)
	type echoInput struct {
		Value string `json:"value"`
	}
	type echoOutput struct {
		Echo string `json:"echo"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo a value"},
		func(ctx context.Context, request *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, echoOutput, error) {
			return nil, echoOutput{Echo: input.Value}, nil
		},
	)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer secret-token" {
			bearerSeen.Store(true)
		} else {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		streamable.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	set, err := Connect(context.Background(), []Connection{{
		Name: "demo", URL: httpServer.URL, BearerToken: "secret-token", Timeout: 2 * time.Second,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	definitions := set.ToolDefinitions()
	if len(definitions) != 1 || definitions[0].Function.Name != "mcp_demo_echo" {
		t.Fatalf("definitions=%+v", definitions)
	}
	result, err := set.Call(context.Background(), "mcp_demo_echo", map[string]any{"value": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "hello") {
		t.Fatalf("result=%s", result)
	}
	if !bearerSeen.Load() {
		t.Fatal("bearer token was not sent to configured MCP origin")
	}
}

func TestFirstPartyNativeToolsHideRuntimeBootstrap(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{
		Name: "first-party-test", Version: "1.0.0",
	}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "agentdock_context", Description: "runtime context"},
		func(ctx context.Context, request *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"nodes": []any{}}, nil
		},
	)
	type echoInput struct {
		Value string `json:"value"`
	}
	type echoOutput struct {
		Echo string `json:"echo"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "exec_command", Description: "run a command"},
		func(ctx context.Context, request *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, echoOutput, error) {
			return nil, echoOutput{Echo: input.Value}, nil
		},
	)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	set, err := Connect(context.Background(), []Connection{{
		Name: "__nexusdock_first_party", URL: httpServer.URL, Timeout: 2 * time.Second,
		NativeToolNames:   true,
		HiddenRemoteTools: map[string]bool{"agentdock_context": true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	definitions := set.ToolDefinitions()
	if len(definitions) != 1 || definitions[0].Function.Name != "exec_command" {
		t.Fatalf("definitions=%+v", definitions)
	}
	if set.HasTool("agentdock_context") {
		t.Fatal("runtime-only agentdock_context was exposed to the model")
	}
	if _, err := set.Call(context.Background(), "agentdock_context", map[string]any{}); err == nil {
		t.Fatal("model-facing call reached runtime-only agentdock_context")
	}
	if _, err := set.CallInternal(context.Background(), "__nexusdock_first_party", "agentdock_context", map[string]any{}); err != nil {
		t.Fatalf("runtime bootstrap call failed: %v", err)
	}
	result, err := set.Call(context.Background(), "exec_command", map[string]any{"value": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "ok") {
		t.Fatalf("native tool result=%s", result)
	}
}

func TestMCPClientRefusesCrossOriginRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("cross-origin redirect reached target")
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	_, err := Connect(context.Background(), []Connection{{
		Name: "redirect", URL: source.URL, BearerToken: "secret-token", Timeout: time.Second,
	}})
	if err == nil || !strings.Contains(err.Error(), "cross-origin MCP redirect refused") {
		t.Fatalf("error=%v", err)
	}
}

func TestBoundedTransportRejectsLargeBody(t *testing.T) {
	transport := &boundedTransport{
		base: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"application/json"}},
				Body:          io.NopCloser(strings.NewReader(strings.Repeat("x", 65))),
				ContentLength: -1,
				Request:       request,
			}, nil
		}),
		maxBytes: 64,
	}
	response, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, "http://localhost/test", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, err = io.ReadAll(response.Body)
	if err == nil || !strings.Contains(err.Error(), "MCP response exceeds 64 bytes") {
		t.Fatalf("error=%v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestToolIDFormatAndParse(t *testing.T) {
	id := FormatToolID("github", "create_issue")
	if id != "github:create_issue" {
		t.Fatalf("expected github:create_issue, got %s", id)
	}

	server, remote, ok := ParseToolID("github:create_issue")
	if !ok || server != "github" || remote != "create_issue" {
		t.Fatalf("colon parse failed: %s %s %v", server, remote, ok)
	}

	server, remote, ok = ParseToolID("github/create_issue")
	if !ok || server != "github" || remote != "create_issue" {
		t.Fatalf("slash parse failed: %s %s %v", server, remote, ok)
	}

	if _, _, ok := ParseToolID("invalid"); ok {
		t.Fatal("expected failure for id without separator")
	}
}

func TestSessionSetExternalToolsAndRefresh(t *testing.T) {
	var toolCount atomic.Int32
	toolCount.Store(1)

	server := mcp.NewServer(&mcp.Implementation{
		Name: "dynamic-server", Version: "1.0.0",
	}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "tool_1", Description: "First tool"},
		func(ctx context.Context, request *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"ok": true}, nil
		},
	)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		if toolCount.Load() == 2 {
			// Add a second tool dynamically
			mcp.AddTool(server, &mcp.Tool{Name: "tool_2", Description: "Second tool"},
				func(ctx context.Context, request *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, map[string]any, error) {
					return nil, map[string]any{"ok": true}, nil
				},
			)
		}
		return server
	}, nil))
	defer httpServer.Close()

	set, err := Connect(context.Background(), []Connection{{
		Name: "ext", URL: httpServer.URL, Timeout: 2 * time.Second,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()

	externalTools := set.ExternalTools()
	if len(externalTools) != 1 || externalTools[0].RemoteName != "tool_1" {
		t.Fatalf("expected 1 external tool, got %+v", externalTools)
	}

	tool, found := set.GetExternalTool("ext", "tool_1")
	if !found || tool.ToolID() != "ext:tool_1" {
		t.Fatalf("GetExternalTool failed: %+v", tool)
	}

	tool, found = set.GetExternalToolByID("ext/tool_1")
	if !found || tool.RemoteName != "tool_1" {
		t.Fatalf("GetExternalToolByID with slash failed: %+v", tool)
	}

	// Dynamic tool addition and refresh
	toolCount.Store(2)
	if err := set.RefreshServer(context.Background(), "ext"); err != nil {
		t.Fatalf("RefreshServer failed: %v", err)
	}

	externalTools = set.ExternalTools()
	if len(externalTools) != 2 {
		t.Fatalf("expected 2 tools after refresh, got %d", len(externalTools))
	}

	tool, found = set.GetExternalTool("ext", "tool_2")
	if !found || tool.ToolID() != "ext:tool_2" {
		t.Fatalf("GetExternalTool for tool_2 failed: %+v", tool)
	}
}

func TestAddConnectionFailureKeepsHealthyToolsAndAllowsRecovery(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "healthy", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]string, error) {
			return nil, map[string]string{"answer": "still available"}, nil
		})
	healthy := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer healthy.Close()
	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret body must not reach users", http.StatusUnauthorized)
	}))
	defer unauthorized.Close()
	sessions, err := Connect(context.Background(), []Connection{{Name: "healthy", URL: healthy.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Close()
	err = sessions.AddConnection(context.Background(), Connection{Name: "broken", URL: unauthorized.URL})
	var connectionErr *ConnectError
	if !errors.As(err, &connectionErr) || connectionErr.ServerName != "broken" || connectionErr.Phase != ConnectPhaseInitialize {
		t.Fatalf("connection error=%v", err)
	}
	if len(sessions.ExternalTools()) != 1 {
		t.Fatalf("catalog changed after failed connection: %+v", sessions.ExternalTools())
	}
	result, err := sessions.CallExternal(context.Background(), "healthy", "echo", nil)
	if err != nil || !strings.Contains(result, "still available") {
		t.Fatalf("healthy tool result=%s err=%v", result, err)
	}
	// 故障连接名没有占住会话槽位，修复后可以正常重新接入。
	if err := sessions.AddConnection(context.Background(), Connection{Name: "broken", URL: healthy.URL}); err != nil {
		t.Fatal(err)
	}
	if len(sessions.ExternalTools()) != 2 {
		t.Fatal("recovered connection tools missing")
	}
	if err := sessions.RemoveConnection("broken"); err != nil {
		t.Fatal(err)
	}
	if len(sessions.ExternalTools()) != 1 {
		t.Fatal("removed connection retained tools")
	}
}

func TestAddConnectionListToolsFailureLeavesNoSessionOrTools(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "list-failure", Version: "1.0.0"}, nil)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body.Close()
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		if strings.Contains(string(raw), "tools/list") {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer broken.Close()
	sessions, err := Connect(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Close()
	err = sessions.AddConnection(context.Background(), Connection{Name: "broken", URL: broken.URL})
	var connectionErr *ConnectError
	if !errors.As(err, &connectionErr) || connectionErr.Phase != ConnectPhaseListTools {
		t.Fatalf("error=%v", err)
	}
	if len(sessions.sessions) != 0 || len(sessions.ToolDefinitions()) != 0 {
		t.Fatal("failed list left partial session or tools")
	}
}
