package agentcheck

import (
	"bytes"
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

func TestProviderSuccessUsesConfiguredSecretWithoutReturningIt(t *testing.T) {
	const secret = "provider-test-secret"
	t.Setenv("CYCLEDOCK_TEST_PROVIDER_KEY", secret)
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Fatalf("authorization=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer server.Close()

	result := New().Provider(context.Background(), ProviderInput{
		BaseURL: server.URL, Model: "test-model",
		APIKeyEnv: "CYCLEDOCK_TEST_PROVIDER_KEY", Timeout: time.Second,
	})
	if !result.OK || result.Code != "ok" || requests.Load() != 1 {
		t.Fatalf("result=%+v requests=%d", result, requests.Load())
	}
	if strings.Contains(result.Message, secret) {
		t.Fatal("provider secret leaked in diagnostic result")
	}
}

func TestProviderDiagnosticsAreSafeAndCategorized(t *testing.T) {
	const secret = "never-return-this-provider-secret"
	t.Setenv("CYCLEDOCK_TEST_BAD_PROVIDER_KEY", secret)

	tests := []struct {
		name    string
		handler http.HandlerFunc
		timeout time.Duration
		code    string
	}{
		{
			name: "auth",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "upstream echoed "+secret, http.StatusUnauthorized)
			},
			timeout: time.Second,
			code:    "authentication_failed",
		},
		{
			name: "malformed",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{not-json"))
			},
			timeout: time.Second,
			code:    "malformed_response",
		},
		{
			name: "timeout",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(120 * time.Millisecond)
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"late"}}]}`))
			},
			timeout: 20 * time.Millisecond,
			code:    "timeout",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			result := New().Provider(context.Background(), ProviderInput{
				BaseURL: server.URL, Model: "test-model",
				APIKeyEnv: "CYCLEDOCK_TEST_BAD_PROVIDER_KEY", Timeout: test.timeout,
			})
			if result.OK || result.Code != test.code {
				t.Fatalf("result=%+v, want code=%s", result, test.code)
			}
			if strings.Contains(result.Message, secret) {
				t.Fatalf("secret leaked in result=%+v", result)
			}
		})
	}

	missing := New().Provider(context.Background(), ProviderInput{
		BaseURL: "http://127.0.0.1:9", Model: "test-model",
		APIKeyEnv: "CYCLEDOCK_TEST_MISSING_KEY", Timeout: time.Second,
	})
	if missing.OK || missing.Code != "secret_env_missing" {
		t.Fatalf("missing secret result=%+v", missing)
	}
}

func TestMCPTestInitializesAndListsWithoutCallingTools(t *testing.T) {
	const secret = "mcp-test-secret"
	t.Setenv("CYCLEDOCK_TEST_MCP_TOKEN", secret)
	var calls atomic.Int32
	var bearerSeen atomic.Bool

	server := mcp.NewServer(&mcp.Implementation{Name: "agentcheck-test", Version: "1.0.0"}, nil)
	type input struct {
		Value string `json:"value"`
	}
	type output struct {
		Value string `json:"value"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "dangerous_write", Description: "must not run during a connection test"},
		func(context.Context, *mcp.CallToolRequest, input) (*mcp.CallToolResult, output, error) {
			calls.Add(1)
			return nil, output{Value: "called"}, nil
		},
	)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+secret {
			bearerSeen.Store(true)
		} else {
			http.Error(w, "unauthorized "+secret, http.StatusUnauthorized)
			return
		}
		streamable.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	result := New().MCP(context.Background(), MCPInput{
		Name: "safe-test", URL: httpServer.URL,
		BearerTokenEnv: "CYCLEDOCK_TEST_MCP_TOKEN", Timeout: 2 * time.Second,
	})
	if !result.OK || result.Code != "ok" || result.ToolCount != 1 {
		t.Fatalf("result=%+v", result)
	}
	if !bearerSeen.Load() {
		t.Fatal("MCP bearer token was not sent")
	}
	if calls.Load() != 0 {
		t.Fatalf("connection test executed %d MCP tool calls", calls.Load())
	}
	if strings.Contains(result.Message, secret) {
		t.Fatal("MCP secret leaked in diagnostic result")
	}
}

func TestProviderRejectsInvalidEndpointAndMissingModel(t *testing.T) {
	checker := New()
	invalidEndpoint := checker.Provider(context.Background(), ProviderInput{
		BaseURL: "http://example.com/v1", Model: "test-model", Timeout: time.Second,
	})
	if invalidEndpoint.OK || invalidEndpoint.Code != "invalid_endpoint" {
		t.Fatalf("invalid endpoint result=%+v", invalidEndpoint)
	}
	missingModel := checker.Provider(context.Background(), ProviderInput{
		BaseURL: "http://127.0.0.1:9", Timeout: time.Second,
	})
	if missingModel.OK || missingModel.Code != "invalid_model" {
		t.Fatalf("missing model result=%+v", missingModel)
	}
}

func TestMCPDiagnosticsClassifyListToolsFailure(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "list-failure", Version: "1.0.0"}, nil)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if bytes.Contains(raw, []byte(`"method":"tools/list"`)) {
			http.Error(w, "list failed", http.StatusInternalServerError)
			return
		}
		streamable.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	result := New().MCP(context.Background(), MCPInput{
		Name: "list-failure", URL: httpServer.URL, Timeout: time.Second,
	})
	if result.OK || result.Code != "list_tools_failed" {
		t.Fatalf("result=%+v", result)
	}
}

func TestMCPDiagnosticsDoNotEchoSecret(t *testing.T) {
	const secret = "never-return-this-mcp-secret"
	t.Setenv("CYCLEDOCK_TEST_BAD_MCP_TOKEN", secret)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized "+secret, http.StatusUnauthorized)
	}))
	defer server.Close()

	result := New().MCP(context.Background(), MCPInput{
		Name: "auth-test", URL: server.URL,
		BearerTokenEnv: "CYCLEDOCK_TEST_BAD_MCP_TOKEN", Timeout: time.Second,
	})
	if result.OK || result.Code != "authentication_failed" {
		t.Fatalf("result=%+v", result)
	}
	if strings.Contains(result.Message, secret) {
		t.Fatalf("MCP secret leaked in result=%+v", result)
	}
}
