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
