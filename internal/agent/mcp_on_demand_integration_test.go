package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/session"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestMCPOnDemand_DiscoveryAndInvocation_AndSessionEvents(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	var mcpCalled atomic.Int32
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "github-mcp", Version: "1.0.0"}, nil)
	type issueInput struct {
		Title string `json:"title"`
		Body  string `json:"body,omitempty"`
	}
	type issueOutput struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	mcp.AddTool(
		mcpServer,
		&mcp.Tool{Name: "create_issue", Description: "Create a GitHub issue in the repository."},
		func(_ context.Context, _ *mcp.CallToolRequest, in issueInput) (*mcp.CallToolResult, issueOutput, error) {
			mcpCalled.Add(1)
			return nil, issueOutput{Number: 42, URL: "https://github.com/org/repo/issues/42"}, nil
		},
	)
	mcpHTTP := httptest.NewServer(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil),
	)
	defer mcpHTTP.Close()

	var modelCalls int
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		modelCalls++
		rawMessages, _ := json.Marshal(body.Messages)
		w.Header().Set("Content-Type", "application/json")

		switch modelCalls {
		case 1:
			// 验证普通外部 MCP 工具不再被逐个直接暴露，暴露的是两个按需入口
			if requestIncludesTool(body, "mcp_github_ext_create_issue") {
				t.Fatalf("external MCP tool should not be exposed directly: %+v", body.Tools)
			}
			if !requestIncludesTool(body, "search_mcp_tools") || !requestIncludesTool(body, "call_mcp_tool") {
				t.Fatalf("expected search_mcp_tools and call_mcp_tool tools in request: %+v", body.Tools)
			}
			// 步骤 1：按任务描述搜索工具
			writeModelResponse(t, w, "搜索 GitHub 工具", []modelclient.ToolCall{{
				ID:   "search-1",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "search_mcp_tools",
					Arguments: `{"query":"create github issue"}`,
				},
			}})
		case 2:
			// 步骤 2：验证搜索结果包含稳定 ID 及 Schema，发起调用
			if !strings.Contains(string(rawMessages), "github_ext:create_issue") {
				t.Fatalf("expected tool_id github_ext:create_issue in search result: %s", rawMessages)
			}
			writeModelResponse(t, w, "调用创建 Issue 工具", []modelclient.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "call_mcp_tool",
					Arguments: `{"tool_id":"github_ext:create_issue","arguments":{"title":"Found bug","body":"details"}}`,
				},
			}})
		case 3:
			// 步骤 3：验证工具执行结果返回，结束当前 Turn
			if !strings.Contains(string(rawMessages), "https://github.com/org/repo/issues/42") {
				t.Fatalf("call response missing expected issue URL: %s", rawMessages)
			}
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "reply-final",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"text","content":"成功创建 Issue #42。","end_turn":true,"idempotency_key":"msg-1"}`,
				},
			}})
		default:
			t.Fatalf("unexpected model call %d", modelCalls)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "MCP On Demand WS")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Project", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(
		ctx, workspace.ID, project.ID, human.ID, "Create Issue", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(
		ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "ondemand-model"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name: "github_ext", URL: mcpHTTP.URL, Enabled: true, TimeoutMS: 5000,
	}); err != nil {
		t.Fatal(err)
	}

	sessionService := agentsession.NewPostgresService(pool)
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    sessionService,
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})

	turn, err := dataStore.CreateHumanTurn(
		ctx, workspace.ID, card.ID, human.ID, "帮我建一个 GitHub issue", "turn-1", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(
		ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Turn.ID != turn.Turn.ID {
		t.Fatalf("claimed turn mismatch: %v vs %v", claim.Turn.ID, turn.Turn.ID)
	}

	if err := runner.RunAttempt(ctx, *claim); err != nil {
		t.Fatal(err)
	}

	if mcpCalled.Load() != 1 {
		t.Fatalf("MCP tool expected 1 call, got %d", mcpCalled.Load())
	}

	// 验证 Session Event 链路完整保留了实际连接、工具名称、参数及结果证据
	appName, userID, sessionID := agentsession.Identity(workspace.ID, card.ID)
	gotSession, err := sessionService.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}

	var foundCall, foundResponse bool
	for event := range gotSession.Session.Events().All() {
		if event == nil || event.Content == nil {
			continue
		}
		for _, part := range event.Content.Parts {
			if part.FunctionCall != nil && part.FunctionCall.Name == "call_mcp_tool" {
				foundCall = true
				if part.FunctionCall.Args["tool_id"] != "github_ext:create_issue" {
					t.Fatalf("session event tool_id mismatch: %v", part.FunctionCall.Args["tool_id"])
				}
			}
			if part.FunctionResponse != nil && part.FunctionResponse.Name == "call_mcp_tool" {
				foundResponse = true
				resp := part.FunctionResponse.Response
				if resp["tool_id"] != "github_ext:create_issue" ||
					resp["connection_name"] != "github_ext" ||
					resp["tool_name"] != "create_issue" {
					t.Fatalf("session event response identity mismatch: %+v", resp)
				}
			}
		}
	}
	if !foundCall || !foundResponse {
		t.Fatalf("session events missing call_mcp_tool call (%v) or response (%v)", foundCall, foundResponse)
	}
}

func TestMCPOnDemand_SameNameToolIsolation(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	var prodCalls, stagingCalls atomic.Int32

	// 创建两个独立的 MCP 服务，工具名均叫 run_query
	prodServer := mcp.NewServer(&mcp.Implementation{Name: "prod-db", Version: "1.0.0"}, nil)
	type queryInput struct {
		SQL string `json:"sql"`
	}
	type queryOutput struct {
		Target string `json:"target"`
	}
	mcp.AddTool(prodServer, &mcp.Tool{Name: "run_query", Description: "Execute SQL query on prod"},
		func(_ context.Context, _ *mcp.CallToolRequest, in queryInput) (*mcp.CallToolResult, queryOutput, error) {
			prodCalls.Add(1)
			return nil, queryOutput{Target: "prod:" + in.SQL}, nil
		},
	)
	prodHTTP := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return prodServer }, nil))
	defer prodHTTP.Close()

	stagingServer := mcp.NewServer(&mcp.Implementation{Name: "staging-db", Version: "1.0.0"}, nil)
	mcp.AddTool(stagingServer, &mcp.Tool{Name: "run_query", Description: "Execute SQL query on staging"},
		func(_ context.Context, _ *mcp.CallToolRequest, in queryInput) (*mcp.CallToolResult, queryOutput, error) {
			stagingCalls.Add(1)
			return nil, queryOutput{Target: "staging:" + in.SQL}, nil
		},
	)
	stagingHTTP := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return stagingServer }, nil))
	defer stagingHTTP.Close()

	var modelCalls int
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		modelCalls++
		rawMessages, _ := json.Marshal(body.Messages)
		w.Header().Set("Content-Type", "application/json")

		switch modelCalls {
		case 1:
			// 步骤 1：搜索 run_query
			writeModelResponse(t, w, "搜索查询工具", []modelclient.ToolCall{{
				ID:   "search-1",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "search_mcp_tools",
					Arguments: `{"query":"run_query"}`,
				},
			}})
		case 2:
			// 验证搜索返回了两个不同 ID：db_prod:run_query 和 db_staging:run_query
			raw := string(rawMessages)
			if !strings.Contains(raw, "db_prod:run_query") || !strings.Contains(raw, "db_staging:run_query") {
				t.Fatalf("expected both distinct tool_ids in search result: %s", raw)
			}
			// 步骤 2：分别调用 db_prod 和 db_staging
			writeModelResponse(t, w, "分别调用两端", []modelclient.ToolCall{
				{
					ID:   "call-prod",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "call_mcp_tool",
						Arguments: `{"tool_id":"db_prod:run_query","arguments":{"sql":"SELECT 1"}}`,
					},
				},
				{
					ID:   "call-staging",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "call_mcp_tool",
						Arguments: `{"tool_id":"db_staging:run_query","arguments":{"sql":"SELECT 2"}}`,
					},
				},
			})
		case 3:
			raw := string(rawMessages)
			if !strings.Contains(raw, "prod:SELECT 1") || !strings.Contains(raw, "staging:SELECT 2") {
				t.Fatalf("responses missing target results: %s", raw)
			}
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "reply-end",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"text","content":"查询完毕。","end_turn":true,"idempotency_key":"msg-db"}`,
				},
			}})
		default:
			t.Fatalf("unexpected step %d", modelCalls)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Isolation WS")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "DB Project", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(
		ctx, workspace.ID, project.ID, human.ID, "Query DB", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(
		ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "db-model"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name: "db_prod", URL: prodHTTP.URL, Enabled: true, TimeoutMS: 5000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name: "db_staging", URL: stagingHTTP.URL, Enabled: true, TimeoutMS: 5000,
	}); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})

	turn, err := dataStore.CreateHumanTurn(ctx, workspace.ID, card.ID, human.ID, "执行跨库查询", "t-db", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Turn.ID != turn.Turn.ID {
		t.Fatalf("turn mismatch")
	}
	if err := runner.RunAttempt(ctx, *claim); err != nil {
		t.Fatal(err)
	}

	if prodCalls.Load() != 1 || stagingCalls.Load() != 1 {
		t.Fatalf("expected 1 call each, prod=%d staging=%d", prodCalls.Load(), stagingCalls.Load())
	}
}

func TestMCPOnDemand_CrossWorkspaceIsolation(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	// MCP 服务在 Workspace 1 中配置，但在 Workspace 2 中不存在
	serverA := mcp.NewServer(&mcp.Implementation{Name: "server-a", Version: "1.0.0"}, nil)
	mcp.AddTool(serverA, &mcp.Tool{Name: "secret_op", Description: "Secret operation for ws1"},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, map[string]string, error) {
			return nil, map[string]string{"secret": "leaked!"}, nil
		},
	)
	httpA := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return serverA }, nil))
	defer httpA.Close()

	var modelCalls int
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		modelCalls++
		rawMessages, _ := json.Marshal(body.Messages)
		w.Header().Set("Content-Type", "application/json")

		switch modelCalls {
		case 1:
			// 步骤 1：在 Workspace 2 搜索工具，验证搜不到 server_a
			writeModelResponse(t, w, "搜索全部工具", []modelclient.ToolCall{{
				ID:   "search-1",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "search_mcp_tools",
					Arguments: `{"query":""}`,
				},
			}})
		case 2:
			raw := string(rawMessages)
			if strings.Contains(raw, "server_a:secret_op") {
				t.Fatalf("cross-workspace tool leaked in search: %s", raw)
			}
			// 步骤 2：试图直接调用 Workspace 1 的工具，验证被拦截
			writeModelResponse(t, w, "伪造调用 Workspace 1 的工具", []modelclient.ToolCall{{
				ID:   "call-forged",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "call_mcp_tool",
					Arguments: `{"tool_id":"server_a:secret_op","arguments":{}}`,
				},
			}})
		case 3:
			raw := string(rawMessages)
			if !strings.Contains(raw, "未启用或不存在") {
				t.Fatalf("expected connection not enabled error for forged tool call: %s", raw)
			}
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "reply-end",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"text","content":"无法跨工作区调用。","end_turn":true,"idempotency_key":"msg-iso"}`,
				},
			}})
		default:
			t.Fatalf("unexpected step %d", modelCalls)
		}
	}))
	defer modelHTTP.Close()

	ws1, err := dataStore.CreateWorkspace(ctx, "Workspace 1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, ws1.ID, store.MCPConnectionInput{
		Name: "server_a", URL: httpA.URL, Enabled: true, TimeoutMS: 5000,
	}); err != nil {
		t.Fatal(err)
	}

	ws2, err := dataStore.CreateWorkspace(ctx, "Workspace 2")
	if err != nil {
		t.Fatal(err)
	}
	human2, err := dataStore.CreateSource(ctx, ws2.ID, domain.SourceKindHuman, "Human2")
	if err != nil {
		t.Fatal(err)
	}
	project2, err := dataStore.CreateProject(ctx, ws2.ID, "Proj2", "")
	if err != nil {
		t.Fatal(err)
	}
	card2, err := dataStore.CreateTodoCard(ctx, ws2.ID, project2.ID, human2.ID, "Card2", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings2, err := dataStore.SaveAgentSettings(ctx, ws2.ID, testRunnerSettings(modelHTTP.URL, "m2"))
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})

	_, err = dataStore.CreateHumanTurn(ctx, ws2.ID, card2.ID, human2.ID, "测试隔离", "t-iso", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, ws2.ID, settings2.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.RunAttempt(ctx, *claim); err != nil {
		t.Fatal(err)
	}
}

func TestMCPOnDemand_DisabledConnection(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "disabled-test", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "ping"},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, map[string]string, error) {
			return nil, map[string]string{"pong": "true"}, nil
		},
	)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	var modelCalls int
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		modelCalls++
		rawMessages, _ := json.Marshal(body.Messages)
		w.Header().Set("Content-Type", "application/json")

		switch modelCalls {
		case 1:
			// 搜索指定连接
			writeModelResponse(t, w, "搜索停用连接", []modelclient.ToolCall{{
				ID:   "search-1",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "search_mcp_tools",
					Arguments: `{"connection_name":"my_disabled"}`,
				},
			}})
		case 2:
			raw := string(rawMessages)
			if !strings.Contains(raw, "不存在或未启用") {
				t.Fatalf("expected disabled message in search: %s", raw)
			}
			// 尝试调用停用连接
			writeModelResponse(t, w, "调用停用工具", []modelclient.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "call_mcp_tool",
					Arguments: `{"tool_id":"my_disabled:ping","arguments":{}}`,
				},
			}})
		case 3:
			raw := string(rawMessages)
			if !strings.Contains(raw, "未启用或不存在") {
				t.Fatalf("expected disabled error in call: %s", raw)
			}
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "reply-end",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"text","content":"连接已被停用。","end_turn":true,"idempotency_key":"msg-dis"}`,
				},
			}})
		default:
			t.Fatalf("unexpected step %d", modelCalls)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Disabled WS")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Disabled Proj", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Disabled Card", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "m-dis"))
	if err != nil {
		t.Fatal(err)
	}
	// 创建连接但设置 Enabled = false
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name: "my_disabled", URL: httpServer.URL, Enabled: false, TimeoutMS: 5000,
	}); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})

	_, err = dataStore.CreateHumanTurn(ctx, workspace.ID, card.ID, human.ID, "测试停用连接", "t-dis", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.RunAttempt(ctx, *claim); err != nil {
		t.Fatal(err)
	}
}

func TestMCPOnDemand_ParameterValidation(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	var remoteCallCount atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "param-val", Version: "1.0.0"}, nil)
	type calcInput struct {
		X  int    `json:"x"`
		Op string `json:"op"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "calc",
		Description: "Perform operation on integer x",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"x":  map[string]any{"type": "integer"},
				"op": map[string]any{"type": "string", "enum": []string{"add", "sub"}},
			},
			"required": []string{"x", "op"},
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in calcInput) (*mcp.CallToolResult, map[string]any, error) {
		remoteCallCount.Add(1)
		return nil, map[string]any{"result": in.X + 10}, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	var modelCalls int
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		modelCalls++
		rawMessages, _ := json.Marshal(body.Messages)
		w.Header().Set("Content-Type", "application/json")

		switch modelCalls {
		case 1:
			// 步骤 1：传入错误参数（缺少必填字段 op，且 x 类型错误）
			writeModelResponse(t, w, "传入错误参数", []modelclient.ToolCall{{
				ID:   "call-invalid",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "call_mcp_tool",
					Arguments: `{"tool_id":"calc_conn:calc","arguments":{"x":"not-an-int"}}`,
				},
			}})
		case 2:
			// 验证返回了参数校验失败的详细可操作错误与 input_schema，且远程服务未被调用
			raw := string(rawMessages)
			if !strings.Contains(raw, "参数校验失败") || !strings.Contains(raw, "input_schema") {
				t.Fatalf("expected validation error and schema in tool response: %s", raw)
			}
			if remoteCallCount.Load() != 0 {
				t.Fatalf("remote tool must NOT be called when validation fails, got %d calls", remoteCallCount.Load())
			}
			// 步骤 2：修正参数后再次调用
			writeModelResponse(t, w, "修正参数后调用", []modelclient.ToolCall{{
				ID:   "call-valid",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "call_mcp_tool",
					Arguments: `{"tool_id":"calc_conn:calc","arguments":{"x":5,"op":"add"}}`,
				},
			}})
		case 3:
			raw := string(rawMessages)
			if !strings.Contains(raw, "15") {
				t.Fatalf("expected valid result in tool response: %s", raw)
			}
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "reply-end",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"text","content":"计算完成。","end_turn":true,"idempotency_key":"msg-calc"}`,
				},
			}})
		default:
			t.Fatalf("unexpected step %d", modelCalls)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Param Val WS")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Param Proj", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Param Card", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "m-param"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name: "calc_conn", URL: httpServer.URL, Enabled: true, TimeoutMS: 5000,
	}); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})

	_, err = dataStore.CreateHumanTurn(ctx, workspace.ID, card.ID, human.ID, "测试参数校验", "t-param", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.RunAttempt(ctx, *claim); err != nil {
		t.Fatal(err)
	}

	if remoteCallCount.Load() != 1 {
		t.Fatalf("expected exactly 1 successful remote call, got %d", remoteCallCount.Load())
	}
}

func TestMCPOnDemand_CrossTurnStableToolIDReuse(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	var echoCalls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "echo-server", Version: "1.0.0"}, nil)
	type echoIn struct {
		Text string `json:"text"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo text"},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, map[string]string, error) {
			echoCalls.Add(1)
			return nil, map[string]string{"reply": "echoed:" + in.Text}, nil
		},
	)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	var modelMu sync.Mutex
	totalModelCalls := 0

	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		modelMu.Lock()
		totalModelCalls++
		step := totalModelCalls
		modelMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch step {
		// Turn 1
		case 1:
			writeModelResponse(t, w, "搜索 echo", []modelclient.ToolCall{{
				ID:   "t1-search",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "search_mcp_tools",
					Arguments: `{"query":"echo"}`,
				},
			}})
		case 2:
			writeModelResponse(t, w, "调用 echo", []modelclient.ToolCall{{
				ID:   "t1-call",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "call_mcp_tool",
					Arguments: `{"tool_id":"echo_svc:echo","arguments":{"text":"hello"}}`,
				},
			}})
		case 3:
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "t1-reply",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"text","content":"第一轮完成","end_turn":true,"idempotency_key":"msg-t1"}`,
				},
			}})

		// Turn 2: 同一 Card Session 下，直接复用已有 Schema 调用 call_mcp_tool，不重复搜索
		case 4:
			writeModelResponse(t, w, "直接调用无需重新搜索", []modelclient.ToolCall{{
				ID:   "t2-direct-call",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "call_mcp_tool",
					Arguments: `{"tool_id":"echo_svc:echo","arguments":{"text":"world"}}`,
				},
			}})
		case 5:
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "t2-reply",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"text","content":"第二轮完成","end_turn":true,"idempotency_key":"msg-t2"}`,
				},
			}})
		default:
			t.Fatalf("unexpected step %d", step)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Cross Turn WS")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Cross Turn Proj", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Cross Turn Card", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "m-turn"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name: "echo_svc", URL: httpServer.URL, Enabled: true, TimeoutMS: 5000,
	}); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})

	// Run Turn 1
	turn1, err := dataStore.CreateHumanTurn(ctx, workspace.ID, card.ID, human.ID, "第一轮请求", "t1", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	claim1, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim1.Turn.ID != turn1.Turn.ID {
		t.Fatal("turn1 mismatch")
	}
	if err := runner.RunAttempt(ctx, *claim1); err != nil {
		t.Fatal(err)
	}

	// Run Turn 2
	turn2, err := dataStore.CreateHumanTurn(ctx, workspace.ID, card.ID, human.ID, "第二轮请求", "t2", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	claim2, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim2.Turn.ID != turn2.Turn.ID {
		t.Fatal("turn2 mismatch")
	}
	if err := runner.RunAttempt(ctx, *claim2); err != nil {
		t.Fatal(err)
	}

	if echoCalls.Load() != 2 {
		t.Fatalf("expected 2 echo calls, got %d", echoCalls.Load())
	}
}
