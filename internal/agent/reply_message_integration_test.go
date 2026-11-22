package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/session"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestADKReplyBatchPreservesOrderAndStopsAfterEndTurn(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()
	modelCalls := 0
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !requestIncludesTool(body, "reply_message") || requestIncludesTool(body, "cycledock_add_finding") {
			t.Errorf("tool surface not unified: %+v", body.Tools)
		}
		modelCalls++
		w.Header().Set("Content-Type", "application/json")
		switch modelCalls {
		case 1:
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "reply-1", Type: "function",
				Function: modelclient.ToolFunction{Name: "reply_message",
					Arguments: `{"type":"text","content":"Checking resources","end_turn":false,"idempotency_key":"reply-1"}`},
			}, {
				ID: "reply-2", Type: "function",
				Function: modelclient.ToolFunction{Name: "reply_message",
					Arguments: `{"type":"text","content":"Everything looks normal","end_turn":true,"idempotency_key":"reply-2"}`},
			}, {
				ID: "after-end", Type: "function",
				Function: modelclient.ToolFunction{Name: "cycledock_confirm_card",
					Arguments: `{"content":"Must not reach review after end_turn","idempotency_key":"after-end"}`},
			}})
		case 2:
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "reply-choice", Type: "function",
				Function: modelclient.ToolFunction{Name: "reply_message",
					Arguments: `{"type":"choice","widget":{"question":"Continue?","options":[{"value":"yes","label":"Yes"},{"value":"no","label":"No"}]},"end_turn":true,"idempotency_key":"reply-choice"}`},
			}})
		default:
			t.Errorf("unexpected model call %d after end_turn", modelCalls)
			writeModelResponse(t, w, "unexpected", nil)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Message test")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Messages", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID,
		"Check server", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "reply-model"))
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond, LeaseDuration: time.Minute,
	})
	for i, input := range []string{"Check the server", "Should we continue?"} {
		_, err := dataStore.CreateHumanTurn(ctx, workspace.ID, card.ID, human.ID,
			input, "reply-turn-"+input, domain.DispatchModeEnqueue)
		if err != nil {
			t.Fatal(err)
		}
		claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
		if err != nil || claim == nil {
			t.Fatalf("claim %d err=%v", i, err)
		}
		if err := runner.RunAttempt(ctx, *claim); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		attempt, err := dataStore.GetAttempt(ctx, workspace.ID, claim.Attempt.ID)
		if err != nil {
			t.Fatal(err)
		}
		expected := domain.AttemptStatusCompleted
		if attempt.Status != expected {
			t.Fatalf("attempt %d status=%s want %s", i, attempt.Status, expected)
		}
	}
	if modelCalls != 2 {
		t.Fatalf("model calls=%d want 2", modelCalls)
	}
	timeline, err := dataStore.ListCardTimeline(ctx, workspace.ID, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	var bubbles, results int
	var waitingAttemptID uuid.UUID
	var choiceMessageID uuid.UUID
	for _, e := range timeline {
		if e.EventType == "confirm" {
			t.Fatal("tool after end_turn created a confirmation")
		}
		if e.EventType == "message" {
			bubbles++
			if bubbles == 1 && e.Content != "Checking resources" || bubbles == 2 && e.Content != "Everything looks normal" {
				t.Fatalf("reply order changed at bubble %d: %s", bubbles, e.Content)
			}
			if bubbles == 3 {
				waitingAttemptID = e.AttemptID.UUID
				choiceMessageID = e.ID
			}
		}
		if e.EventType == "result" {
			results++
		}
	}
	if bubbles != 3 || results != 0 {
		t.Fatalf("message bubbles=%d result events=%d", bubbles, results)
	}
	// 选择消息已完成本次 Attempt，回答创建新的 Turn，继续使用同一 Session。
	reply, err := dataStore.ReplySessionAndContinue(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "yes", IdempotencyKey: "choice-answer",
		ReplyToAttemptID: waitingAttemptID, ChoiceMessageID: choiceMessageID,
	})
	if err != nil || reply.TurnID == uuid.Nil {
		t.Fatalf("reply and continue=%+v err=%v", reply, err)
	}
}

func TestReplyPreviewStreamsBeforeToolCommit(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finish := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(finish) })
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || !body.Stream {
			t.Errorf("stream request=%+v err=%v", body, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		packet := func(args string, first bool) {
			call := map[string]any{"index": 0, "function": map[string]string{"arguments": args}}
			if first {
				call["id"] = "preview-call"
				call["function"].(map[string]string)["name"] = "reply_message"
			}
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{call}}}}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"PRIVATE MODEL TEXT"}}]}`)
		packet(`{"type":"text","content":"你好`, true)
		select {
		case <-finish:
		case <-request.Context().Done():
			return
		}
		packet(`，世界","end_turn":true,"idempotency_key":"preview-reply"}`, false)
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer modelHTTP.Close()
	// 测试中途失败也要先释放正在等待的模型服务，再关闭 HTTP server。
	defer release.Do(func() { close(finish) })
	workspace, err := dataStore.CreateWorkspace(ctx, "Preview test")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Preview", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Preview", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "preview-model"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = dataStore.CreateHumanTurn(ctx, workspace.ID, card.ID, human.ID, "Reply", "preview-turn", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	hub := realtime.NewHub()
	sub, _ := hub.Subscribe(card.ID)
	defer sub.Close()
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{SessionService: agentsession.NewPostgresService(pool), Live: hub, HeartbeatInterval: 50 * time.Millisecond, LeaseDuration: time.Minute})
	done := make(chan error, 1)
	go func() { done <- runner.RunAttempt(ctx, *claim) }()
	var previewID string
	for previewID == "" {
		select {
		case event := <-sub.Events:
			if event.Type == "reply.preview" {
				if event.Text != "你好" {
					t.Fatalf("preview=%+v", event)
				}
				previewID = event.ReplyID
			}
		case err := <-done:
			t.Fatalf("runner ended before preview: %v", err)
		case <-ctx.Done():
			t.Fatal("no preview before tool finished")
		}
	}
	timeline, err := dataStore.ListCardTimeline(ctx, workspace.ID, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	var previewEventID uuid.UUID
	for _, event := range timeline {
		if event.EventType == "message" {
			var payload struct {
				PreviewID string `json:"preview_id"`
				Preview   bool   `json:"preview"`
			}
			if err := json.Unmarshal(event.Message, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.PreviewID != previewID || !payload.Preview || event.Content != "你好" {
				t.Fatalf("persisted preview=%+v payload=%+v", event, payload)
			}
			previewEventID = event.ID
		}
	}
	if previewEventID == uuid.Nil {
		t.Fatal("visible preview missing from refreshed timeline")
	}
	release.Do(func() { close(finish) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var committed string
	for len(sub.Events) > 0 {
		event := <-sub.Events
		if strings.Contains(event.Text, "PRIVATE") {
			t.Fatal("raw model text leaked")
		}
		if event.Type == "reply.committed" {
			if event.ReplyID != previewID {
				t.Fatalf("commit lost preview identity: %+v", event)
			}
			committed = event.EventID
		}
	}
	timeline, err = dataStore.ListCardTimeline(ctx, workspace.ID, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for _, event := range timeline {
		if event.EventType == "message" {
			count++
			if event.ID.String() != committed || event.Content != "你好，世界" {
				t.Fatalf("committed=%s event=%+v", committed, event)
			}
		}
	}
	if count != 1 || committed != previewEventID.String() {
		t.Fatalf("messages=%d committed=%s", count, committed)
	}
	appName, userID, sessionID := agentsession.Identity(workspace.ID, card.ID)
	stored, err := runner.sessionService.Get(ctx, &session.GetRequest{AppName: appName, UserID: userID, SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	calls, responses := 0, 0
	for event := range stored.Session.Events().All() {
		if event.Partial {
			t.Fatal("ADK partial event was persisted")
		}
		if event.Content == nil {
			continue
		}
		for _, part := range event.Content.Parts {
			if part.FunctionCall != nil && part.FunctionCall.Name == "reply_message" {
				calls++
				if len(part.FunctionCall.PartialArgs) > 0 || part.FunctionCall.Args["content"] != "你好，世界" {
					t.Fatalf("incomplete persisted call=%+v", part.FunctionCall)
				}
			}
			if part.FunctionResponse != nil && part.FunctionResponse.Name == "reply_message" {
				responses++
			}
		}
	}
	if calls != 1 || responses != 1 {
		t.Fatalf("ADK calls=%d responses=%d", calls, responses)
	}
}

func TestFailedReplyMessageDiscardsPreviewWithoutDuplicateBubbles(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	modelCalls := 0
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		modelCalls++
		w.Header().Set("Content-Type", "text/event-stream")
		writeCall := func(index int, id, args string) {
			raw, _ := json.Marshal(map[string]any{
				"choices": []any{map[string]any{
					"delta": map[string]any{
						"tool_calls": []any{map[string]any{
							"index": index, "id": id,
							"function": map[string]string{"name": "reply_message", "arguments": args},
						}},
					},
				}},
			})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		switch modelCalls {
		case 1:
			// 第 1 次调用：流式输出正文后携带非法 widget 字段，触发校验失败。
			writeCall(0, "bad-reply", `{"type":"text","content":"你好！有什么我可以帮你的吗？","widget":{"question":"选一个","options":[{"value":"a","label":"A"},{"value":"b","label":"B"}]},"end_turn":true,"idempotency_key":"reply-greeting-1"}`)
		case 2:
			// 第 2 次调用：修正参数后成功发送，并在同批追加一条应被拒绝的第二条 reply_message。
			writeCall(0, "good-reply", `{"type":"text","content":"你好！有什么我可以帮你的吗？","end_turn":true,"idempotency_key":"reply-greeting-1"}`)
			writeCall(1, "extra-after-end", `{"type":"text","content":"不应残留的第二条","end_turn":false,"idempotency_key":"reply-greeting-2"}`)
		default:
			t.Errorf("unexpected model call %d", modelCalls)
		}
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Discard preview test")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Discard", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID,
		"Greeting", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "preview-model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, card.ID, human.ID,
		"你好", "discard-e2e-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}

	hub := realtime.NewHub()
	sub, _ := hub.Subscribe(card.ID)
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
	if modelCalls != 2 {
		t.Fatalf("modelCalls=%d want 2", modelCalls)
	}

	var discardedCount, committedCount int
	for len(sub.Events) > 0 {
		event := <-sub.Events
		switch event.Type {
		case "reply.discarded":
			discardedCount++
		case "reply.committed":
			committedCount++
		}
	}
	if discardedCount != 2 || committedCount != 1 {
		t.Fatalf("sse events discarded=%d committed=%d want 2/1", discardedCount, committedCount)
	}

	timeline, err := dataStore.ListCardTimeline(ctx, workspace.ID, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, event := range timeline {
		if event.EventType != "message" {
			continue
		}
		var payload struct {
			Preview bool `json:"preview"`
		}
		if err := json.Unmarshal(event.Message, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Preview {
			t.Fatalf("orphan preview remained in timeline: %+v", event)
		}
		messages = append(messages, event.Content)
	}
	if len(messages) != 1 || messages[0] != "你好！有什么我可以帮你的吗？" {
		t.Fatalf("timeline messages=%v want single committed reply", messages)
	}
}
