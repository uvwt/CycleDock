package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	adkrunner "google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/mcpclient"
)

func TestNexusDockConversation_设备和Cloud工具全部通过MCP执行(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "nexusdock-test", Version: "1"}, nil)
	bootstrapCalls := 0
	mcp.AddTool(server, &mcp.Tool{Name: "agentdock_context"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
			bootstrapCalls++
			return nil, map[string]any{"nodes": []any{
				map[string]any{"node_id": "node_a", "name": "RN", "online": true},
			}}, nil
		})
	var mu sync.Mutex
	called := make(map[string]bool)
	for _, name := range []string{"exec_command", "recall_search", "workflow_template_manage"} {
		mcp.AddTool(server, &mcp.Tool{Name: name},
			func(_ context.Context, _ *mcp.CallToolRequest, input map[string]any) (*mcp.CallToolResult, map[string]any, error) {
				if name == "exec_command" && (input["node_id"] != "node_a" || input["cmd"] != "docker ps") {
					t.Errorf("设备路由和命令必须交给 MCP: %#v", input)
				}
				mu.Lock()
				called[name] = true
				mu.Unlock()
				return nil, map[string]any{"tool": name, "ok": true}, nil
			})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Errorf("对话不得访问 REST 目录或 Execution API: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer oauth-token" {
			t.Error("MCP 必须使用 Workspace 授权凭据")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	sessions, err := mcpclient.Connect(context.Background(), []mcpclient.Connection{{
		Name: firstPartyNexusDockServerName, URL: httpServer.URL + "/mcp", BearerToken: "oauth-token",
		Timeout: 5 * time.Second, NativeToolNames: true, HiddenRemoteTools: map[string]bool{"agentdock_context": true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Close()
	bootstrap, err := nexusDockExecutionTargets(context.Background(), sessions)
	if err != nil || bootstrapCalls != 1 || !strings.Contains(bootstrap, "node_a") || strings.Contains(bootstrap, "oauth-token") {
		t.Fatalf("MCP bootstrap 失败: calls=%d err=%v context=%s", bootstrapCalls, err, bootstrap)
	}
	tools, err := buildADKTools(attemptToolRuntime{mcpSessions: sessions, fatal: &attemptToolFatal{}})
	if err != nil {
		t.Fatal(err)
	}
	llm := &nexusDockConversationTestModel{}
	agent, err := llmagent.New(llmagent.Config{Name: "nexus_test", Model: llm, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := adkrunner.New(adkrunner.Config{AppName: "nexus_test", Agent: agent, SessionService: session.InMemoryService(), AutoCreateSession: true})
	if err != nil {
		t.Fatal(err)
	}
	responses := 0
	for event, err := range runner.Run(context.Background(), "workspace", "card", genai.NewContentFromText("inspect devices and shared knowledge", genai.RoleUser), adkagent.RunConfig{}) {
		if err != nil {
			t.Fatal(err)
		}
		if event.Content != nil {
			for _, part := range event.Content.Parts {
				if part.FunctionResponse != nil {
					responses++
				}
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(called) != 3 || responses != 3 || llm.calls != 2 {
		t.Fatalf("设备、Recall、Workflow 应都经 MCP 返回 ADK: called=%v responses=%d modelCalls=%d", called, responses, llm.calls)
	}
}

type nexusDockConversationTestModel struct{ calls int }

func (*nexusDockConversationTestModel) Name() string { return "nexus_test" }

func (m *nexusDockConversationTestModel) GenerateContent(_ context.Context, request *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.calls++
		if m.calls == 1 {
			declarations, err := json.Marshal(request.Config.Tools)
			if err != nil {
				yield(nil, err)
				return
			}
			for _, name := range []string{"exec_command", "recall_search", "workflow_template_manage"} {
				if !strings.Contains(string(declarations), `"name":"`+name+`"`) {
					yield(nil, fmt.Errorf("MCP 工具未向模型提供: %s", name))
					return
				}
			}
			if strings.Contains(string(declarations), `"name":"agentdock_context"`) {
				yield(nil, fmt.Errorf("已完成 bootstrap 的内部工具不应重复向模型提供"))
				return
			}
			yield(&model.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
				{FunctionCall: &genai.FunctionCall{ID: "device", Name: "exec_command", Args: map[string]any{"node_id": "node_a", "cmd": "docker ps"}}},
				{FunctionCall: &genai.FunctionCall{ID: "recall", Name: "recall_search", Args: map[string]any{"query": "deploy"}}},
				{FunctionCall: &genai.FunctionCall{ID: "workflow", Name: "workflow_template_manage", Args: map[string]any{"action": "list"}}},
			}}}, nil)
			return
		}
		yield(&model.LLMResponse{Content: genai.NewContentFromText("done", genai.RoleModel)}, nil)
	}
}
