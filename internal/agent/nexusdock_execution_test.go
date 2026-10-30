package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/mcpclient"
	"github.com/uvwt/CycleDock/internal/nexusdock"
)

func executionTestRouter(t *testing.T, server *httptest.Server, capability directoryCapability) *nexusDockExecutionRouter {
	t.Helper()
	rest, ok := newNexusDockRESTClient(nexusdock.RuntimeConnection{
		URL: server.URL + "/mcp", AccessToken: "oauth-secret",
	})
	if !ok {
		t.Fatal("loopback NexusDock REST client should be supported")
	}
	router := newNexusDockExecutionRouter(rest, []directoryCapability{capability})
	if router == nil {
		t.Fatal("online capability should create execution router")
	}
	return router
}

func TestNexusDockExecutionRouter_设备Tool通过ExecutionSSE并保留MCP结果形状(t *testing.T) {
	var createBody map[string]any
	var idempotency string
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer oauth-secret" {
			t.Fatalf("missing NexusDock bearer: %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/integrations/executions":
			idempotency = r.Header.Get("Idempotency-Key")
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"execution":{"id":"exec_00000000000000000000000000000001","status":"queued","version":1}}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events"):
			if r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Last-Event-ID") != "1" {
				t.Fatalf("unexpected SSE headers: %#v", r.Header)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "id: 2\nevent: execution\ndata: {\"status\":\"succeeded\",\"version\":2}\n\n")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/integrations/executions/exec_00000000000000000000000000000001":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"execution":{"id":"exec_00000000000000000000000000000001","status":"succeeded","version":2,"result":{"content":[{"type":"text","text":"done"}],"structuredContent":{"ok":true},"isError":false}}}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	router := executionTestRouter(t, server, directoryCapability{
		ID: "cap_probe", NodeID: "node_a", Name: "probe", Kind: "tool",
		Online: true, ContractHash: "sha256:probe",
	})
	raw, handled, err := router.Call(
		context.Background(),
		uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		"call_1",
		mcpclient.Tool{ServerName: firstPartyNexusDockServerName, RemoteName: "probe"},
		map[string]any{"node_id": "node_a", "value": "hello"},
	)
	if err != nil || !handled {
		t.Fatalf("execution call failed: handled=%v err=%v", handled, err)
	}
	if calls.Load() != 3 || !strings.HasPrefix(idempotency, "cd_exec_") || len(idempotency) != len("cd_exec_")+64 {
		t.Fatalf("unexpected execution requests/idempotency: calls=%d key=%q", calls.Load(), idempotency)
	}
	if createBody["capability_id"] != "cap_probe" || createBody["contract_hash"] != "sha256:probe" {
		t.Fatalf("unexpected create body: %#v", createBody)
	}
	input := createBody["input"].(map[string]any)
	if input["value"] != "hello" {
		t.Fatalf("tool input lost: %#v", input)
	}
	if _, exists := input["node_id"]; exists {
		t.Fatalf("routing node_id must not be sent to AgentDock tool input: %#v", input)
	}
	if !strings.Contains(raw, `"structuredContent":{"ok":true}`) || !strings.Contains(raw, `"done"`) {
		t.Fatalf("MCP-shaped result lost: %s", raw)
	}
}

func TestNexusDockExecutionRouter_NodeToolCall解包为真实Capability(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/integrations/executions":
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"execution":{"id":"exec_00000000000000000000000000000002","status":"succeeded","version":2,"result":{"ok":true}}}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	router := executionTestRouter(t, server, directoryCapability{
		ID: "cap_exec", NodeID: "node_a", Name: "exec_command", Kind: "tool",
		Online: true, ContractHash: "sha256:exec",
	})
	_, handled, err := router.Call(
		context.Background(), uuid.New(), "call_fallback",
		mcpclient.Tool{ServerName: firstPartyNexusDockServerName, RemoteName: "node_tool_call"},
		map[string]any{
			"node_id": "node_a", "name": "exec_command",
			"arguments": map[string]any{"cmd": "echo ok"},
		},
	)
	if err != nil || !handled {
		t.Fatalf("node_tool_call should use execution API: handled=%v err=%v", handled, err)
	}
	if body["capability_id"] != "cap_exec" || body["input"].(map[string]any)["cmd"] != "echo ok" {
		t.Fatalf("node_tool_call was not unwrapped: %#v", body)
	}
	if raw, handled, err := router.Call(
		context.Background(), uuid.New(), "call_plain_result",
		mcpclient.Tool{ServerName: firstPartyNexusDockServerName, RemoteName: "node_tool_call"},
		map[string]any{"node_id": "node_a", "name": "exec_command", "arguments": map[string]any{"cmd": "echo ok"}},
	); err != nil || !handled || !strings.Contains(raw, `"structuredContent":{"ok":true}`) || !strings.Contains(raw, `"isError":false`) {
		t.Fatalf("普通结构化结果应包装为 MCP Tool Result: handled=%v err=%v raw=%s", handled, err, raw)
	}
}

func TestNexusDockExecutionRouter_旧Cloud404才允许回退MCP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	defer server.Close()
	router := executionTestRouter(t, server, directoryCapability{
		ID: "cap_probe", NodeID: "node_a", Name: "probe", Kind: "tool",
		Online: true, ContractHash: "sha256:probe",
	})
	tool := mcpclient.Tool{ServerName: firstPartyNexusDockServerName, RemoteName: "probe"}
	args := map[string]any{"node_id": "node_a"}

	for i := 0; i < 2; i++ {
		_, handled, err := router.Call(context.Background(), uuid.New(), fmt.Sprintf("call_%d", i), tool, args)
		if err != nil || handled {
			t.Fatalf("old Cloud should fall back to MCP: handled=%v err=%v", handled, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("unsupported Execution API should be cached for this Attempt, calls=%d", calls.Load())
	}
}

func TestNexusDockExecutionRouter_服务端歧义失败绝不回退MCP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"code":"INTERNAL_ERROR","message":"temporary"}}`)
	}))
	defer server.Close()
	router := executionTestRouter(t, server, directoryCapability{
		ID: "cap_probe", NodeID: "node_a", Name: "probe", Kind: "tool",
		Online: true, ContractHash: "sha256:probe",
	})

	_, handled, err := router.Call(
		context.Background(), uuid.New(), "call_ambiguous",
		mcpclient.Tool{ServerName: firstPartyNexusDockServerName, RemoteName: "probe"},
		map[string]any{"node_id": "node_a"},
	)
	if err == nil || !handled {
		t.Fatalf("ambiguous failure must not fall back to MCP: handled=%v err=%v", handled, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("same idempotency request should retry REST once, calls=%d", calls.Load())
	}
}

func TestNexusDockExecutionRouter_契约变更发生在发送前时安全回退MCP实时校验(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":{"code":"CAPABILITY_SCHEMA_CHANGED","message":"refresh capability"}}`)
	}))
	defer server.Close()
	router := executionTestRouter(t, server, directoryCapability{
		ID: "cap_probe", NodeID: "node_a", Name: "probe", Kind: "tool",
		Online: true, ContractHash: "sha256:old",
	})

	_, handled, err := router.Call(
		context.Background(), uuid.New(), "call_schema_changed",
		mcpclient.Tool{ServerName: firstPartyNexusDockServerName, RemoteName: "probe"},
		map[string]any{"node_id": "node_a", "value": "old"},
	)
	if err != nil || handled {
		t.Fatalf("明确的发送前 Schema 变化应交还 MCP 实时校验: handled=%v err=%v", handled, err)
	}
}

func TestNexusDockExecutionRouter_明确未执行的业务拒绝作为ToolError返回模型(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":{"code":"NODE_OFFLINE","message":"node unavailable"}}`)
	}))
	defer server.Close()
	router := executionTestRouter(t, server, directoryCapability{
		ID: "cap_probe", NodeID: "node_a", Name: "probe", Kind: "tool",
		Online: true, ContractHash: "sha256:probe",
	})

	raw, handled, err := router.Call(
		context.Background(), uuid.New(), "call_offline",
		mcpclient.Tool{ServerName: firstPartyNexusDockServerName, RemoteName: "probe"},
		map[string]any{"node_id": "node_a"},
	)
	if err != nil || !handled || !strings.Contains(raw, `"isError":true`) ||
		!strings.Contains(raw, `"NODE_OFFLINE"`) || !strings.Contains(raw, `"retry_safe":true`) {
		t.Fatalf("明确未执行的业务拒绝应保持 MCP ToolError 语义: handled=%v err=%v raw=%s", handled, err, raw)
	}
}

func TestNexusDockExecutionRouter_调用取消后向Cloud取消同一Execution(t *testing.T) {
	cancelSeen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/integrations/executions":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"execution":{"id":"exec_00000000000000000000000000000003","status":"running","version":2}}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
			cancelSeen <- struct{}{}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"execution":{"id":"exec_00000000000000000000000000000003","status":"running","version":3}}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	router := executionTestRouter(t, server, directoryCapability{
		ID: "cap_probe", NodeID: "node_a", Name: "probe", Kind: "tool",
		Online: true, ContractHash: "sha256:probe",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, handled, err := router.Call(
		ctx, uuid.New(), "call_cancel",
		mcpclient.Tool{ServerName: firstPartyNexusDockServerName, RemoteName: "probe"},
		map[string]any{"node_id": "node_a"},
	)
	if err == nil || !handled {
		t.Fatalf("cancelled execution should return context error: handled=%v err=%v", handled, err)
	}
	select {
	case <-cancelSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("CycleDock cancellation did not call NexusDock execution cancel")
	}
}
