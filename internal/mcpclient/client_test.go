package mcpclient

import (
	"context"
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
	metadata, ok := set.Tool("exec_command")
	if !ok || metadata.ServerName != "__nexusdock_first_party" || metadata.RemoteName != "exec_command" {
		t.Fatalf("first-party tool metadata=%+v ok=%v", metadata, ok)
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
