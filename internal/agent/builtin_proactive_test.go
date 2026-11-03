package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestBuildADKInstructions_内置定时主动触达注入只读约束与单气泡规则(t *testing.T) {
	card := store.AgentCardContext{
		WorkspaceName:      "ws",
		ProjectName:        "proj",
		CardTitle:          "主聊天",
		CardKind:           domain.CardKindDiscussion,
		IsMainDiscussion:   true,
		IsBuiltInProactive: true,
	}
	instructions := buildADKInstructions(card, "", `{"nodes":[]}`)
	for _, want := range []string{
		"当前执行模式：主聊天内置定时主动消息",
		"主动调用不具破坏性",
		"1 条气泡",
		"end_turn=true",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions missing %q", want)
		}
	}
}

func TestBuiltInProactiveRunAttempt_调用设备SkillMCP且只发送一条气泡到主聊天(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	var mcpCalled int
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "device-skill-test", Version: "1.0.0"}, nil)
	type digestInput struct {
		Topic string `json:"topic"`
	}
	type digestOutput struct {
		Summary string `json:"summary"`
	}
	mcp.AddTool(
		mcpServer,
		&mcp.Tool{Name: "read_digest", Description: "Read non-destructive device skill digest for topic."},
		func(_ context.Context, _ *mcp.CallToolRequest, in digestInput) (*mcp.CallToolResult, digestOutput, error) {
			mcpCalled++
			return nil, digestOutput{
				Summary: "Rust 1.85 发布了 async closure 稳定版，本机 cargo 工具链当前为 1.84.0",
			}, nil
		},
	)
	mcpHTTP := httptest.NewServer(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil),
	)
	defer mcpHTTP.Close()

	var modelMu sync.Mutex
	modelCalls := 0
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		for _, forbidden := range []string{
			"cycledock_generate_work",
			"cycledock_send_card_message",
			"cycledock_create_schedule",
			"cycledock_update_schedule",
			"cycledock_delete_schedule",
			"cycledock_confirm_card",
		} {
			if requestIncludesTool(body, forbidden) {
				t.Errorf("built-in proactive turn must not expose mutating tool %s", forbidden)
			}
		}
		if !requestIncludesTool(body, "reply_message") || !requestIncludesTool(body, "call_mcp_tool") {
			t.Errorf("expected reply_message and call_mcp_tool tools, got %+v", body.Tools)
		}

		modelMu.Lock()
		modelCalls++
		step := modelCalls
		modelMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch step {
		case 1:
			writeModelResponse(t, w, "", []modelclient.ToolCall{
				{
					ID:   "progress-bubble-blocked",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "reply_message",
						Arguments: `{"type":"text","content":"正在为你查询...","end_turn":false,"idempotency_key":"proactive-progress"}`,
					},
				},
				{
					ID:   "skill-mcp-1",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "call_mcp_tool",
						Arguments: `{"tool_id":"device:read_digest","arguments":{"topic":"Rust async closure"}}`,
					},
				},
			})
		case 2:
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "final-single-bubble",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"text","content":"看到你最近在关注 Rust 异步闭包：Rust 1.85 已稳定支持 async closure，你本机当前 cargo 为 1.84.0，需要时可以升级体验。","end_turn":true,"idempotency_key":"proactive-final"}`,
				},
			}})
		default:
			t.Errorf("unexpected model call %d", step)
			writeModelResponse(t, w, "unexpected", nil)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Proactive Agent")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Rust Project", "")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "proactive-model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name: "device", URL: mcpHTTP.URL, Enabled: true, TimeoutMS: 5000,
	}); err != nil {
		t.Fatal(err)
	}

	todoCard, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Rust 工具链检查", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, todoCard.ID, human.ID,
		"最近在研究 Rust async closure 和本机工具链版本", "seed-interest-done", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	seedClaim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC), time.Minute)
	if err != nil || seedClaim == nil {
		t.Fatalf("seed claim=%+v err=%v", seedClaim, err)
	}
	if err := dataStore.CompleteAttempt(ctx, workspace.ID, todoCard.ID, seedClaim.Attempt.ID, settings.SourceID, time.Date(2026, 11, 1, 1, 1, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE projects SET created_at = '2026-11-01T00:00:00Z' WHERE id = $1`, project.ID); err != nil {
		t.Fatal(err)
	}

	loc, _ := time.LoadLocation("Asia/Shanghai")
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	triggerAt, _, _, err := store.BuiltInProactiveTriggerWindow(project.ID, day, store.BuiltInProactiveSlots()[0])
	if err != nil {
		t.Fatal(err)
	}
	created, err := dataStore.ProcessBuiltInProactiveTurns(ctx, workspace.ID, triggerAt.Add(time.Minute))
	if err != nil || len(created) != 1 {
		t.Fatalf("ProcessBuiltInProactiveTurns created=%v err=%v", created, err)
	}
	mainCard, err := dataStore.MainDiscussion(ctx, workspace.ID, project.ID)
	if err != nil {
		t.Fatal(err)
	}

	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, triggerAt.Add(2*time.Minute), time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}

	hub := realtime.NewHub()
	sub, _ := hub.Subscribe(mainCard.ID)
	defer sub.Close()

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		Live:              hub,
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})
	if err := runner.RunAttempt(ctx, *claim); err != nil {
		t.Fatalf("RunAttempt: %v", err)
	}
	if mcpCalled != 1 {
		t.Fatalf("expected non-destructive MCP tool called once, got %d", mcpCalled)
	}

	for len(sub.Events) > 0 {
		ev := <-sub.Events
		if ev.Type == "attempt.started" || ev.Type == "attempt.finished" || ev.Type == "reply.preview" {
			t.Fatalf("built-in proactive turn must not emit intermediate SSE event %q", ev.Type)
		}
	}

	timeline, err := dataStore.ListCardTimeline(ctx, workspace.ID, mainCard.ID)
	if err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, ev := range timeline {
		if ev.EventType == "message" {
			messages = append(messages, ev.Content)
		}
	}
	if len(messages) != 1 {
		t.Fatalf("expected strictly 1 message bubble in Main Chat, got %d: %+v", len(messages), messages)
	}
	if !strings.Contains(messages[0], "Rust 1.85") {
		t.Fatalf("unexpected final bubble content: %s", messages[0])
	}
}
