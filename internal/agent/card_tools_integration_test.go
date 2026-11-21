package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/realtime"
)

func TestBuiltInUpdateCardTool_UpdatesTodoAndOngoingAndPublishesRealtime(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	workspace, err := dataStore.CreateWorkspace(ctx, "Card Update Workspace")
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
	todoCard, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "初始 Todo", "初始描述", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ongoingCard, err := dataStore.CreateOngoingCard(ctx, workspace.ID, project.ID, human.ID, "初始 Ongoing", "初始监控说明", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	// 先确保 todo 和 ongoing 在同一次集成运行前都处于各自合法初始态
	_ = ongoingCard

	hub := realtime.NewHub()
	todoSub, _ := hub.Subscribe(todoCard.ID)
	defer todoSub.Close()
	ongoingSub, _ := hub.Subscribe(ongoingCard.ID)
	defer ongoingSub.Close()

	var step atomic.Int32
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !requestIncludesTool(body, "cycledock_update_card") {
			t.Errorf("expected cycledock_update_card in built-in tool list")
		}

		w.Header().Set("Content-Type", "application/json")
		switch step.Add(1) {
		case 1:
			todoArgs, _ := json.Marshal(map[string]any{
				"title":                  "已更新的 Todo 标题",
				"description":            "更新后的 Todo 验收标准",
				"pinned":                 true,
				"required_confirmations": 2,
			})
			ongoingArgs, _ := json.Marshal(map[string]any{
				"card_id":       ongoingCard.ID.String(),
				"title":         "已更新的 Ongoing 监控",
				"description":   "暂停巡检等待窗口",
				"ongoing_state": "paused",
				"pinned":        true,
			})
			writeModelResponse(t, w, "", []modelclient.ToolCall{
				{
					ID:   "upd-self",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "cycledock_update_card",
						Arguments: string(todoArgs),
					},
				},
				{
					ID:   "upd-ongoing",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "cycledock_update_card",
						Arguments: string(ongoingArgs),
					},
				},
			})
		case 2:
			rawMessages, _ := json.Marshal(body.Messages)
			if !strings.Contains(string(rawMessages), `\"status_changed\":true`) &&
				!strings.Contains(string(rawMessages), `"status_changed":true`) {
				t.Errorf("expected tool response to report status_changed=true, got %s", string(rawMessages))
			}
			writeModelResponse(t, w, "", []modelclient.ToolCall{
				{
					ID:   "reply-done",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "reply_message",
						Arguments: `{"type":"text","content":"两张卡片的信息都已更新完成。","end_turn":true,"idempotency_key":"upd-card-reply"}`,
					},
				},
			})
		default:
			t.Errorf("unexpected model step %d", step.Load())
			writeModelResponse(t, w, "unexpected", nil)
		}
	}))
	defer modelHTTP.Close()

	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "card-update-model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, todoCard.ID, human.ID, "请更新当前 Todo 和长期监控卡的信息", "turn-1", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		Live:              hub,
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})
	if err := runner.RunAttempt(ctx, *claim); err != nil {
		t.Fatalf("RunAttempt failed: %v", err)
	}

	updatedTodo, err := dataStore.GetCard(ctx, workspace.ID, todoCard.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedTodo.Title != "已更新的 Todo 标题" ||
		updatedTodo.Description != "更新后的 Todo 验收标准" ||
		!updatedTodo.PinnedAt.Valid ||
		updatedTodo.RequiredConfirmations != 2 {
		t.Fatalf("unexpected updated todo card: %+v", updatedTodo)
	}

	updatedOngoing, err := dataStore.GetCard(ctx, workspace.ID, ongoingCard.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedOngoing.Title != "已更新的 Ongoing 监控" ||
		updatedOngoing.Description != "暂停巡检等待窗口" ||
		!updatedOngoing.PinnedAt.Valid ||
		updatedOngoing.OngoingState.String != string(domain.OngoingStatePaused) {
		t.Fatalf("unexpected updated ongoing card: %+v", updatedOngoing)
	}

	assertSawCardUpdated := func(name string, events <-chan realtime.Event) {
		t.Helper()
		timeout := time.After(500 * time.Millisecond)
		for {
			select {
			case ev := <-events:
				if ev.Type == "card.updated" {
					return
				}
			case <-timeout:
				t.Fatalf("expected card.updated realtime event for %s", name)
			}
		}
	}
	assertSawCardUpdated("todoCard", todoSub.Events)
	assertSawCardUpdated("ongoingCard", ongoingSub.Events)
}

func TestBuiltInGenerateWorkTool_AutoStartFalseSkipsInitialTurn(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	workspace, err := dataStore.CreateWorkspace(ctx, "Generate Work AutoStart Workspace")
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
	discussion, err := dataStore.CreateDiscussion(ctx, workspace.ID, project.ID, human.ID)
	if err != nil {
		t.Fatal(err)
	}

	var step atomic.Int32
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch step.Add(1) {
		case 1:
			silentArgs, _ := json.Marshal(map[string]any{
				"kind":            "todo",
				"title":           "先留档的待办",
				"description":     "等用户手动开始执行",
				"auto_start":      false,
				"idempotency_key": "adk-gen-silent-1",
			})
			autoArgs, _ := json.Marshal(map[string]any{
				"kind":            "todo",
				"title":           "立即执行的待办",
				"description":     "默认立即启动",
				"idempotency_key": "adk-gen-auto-1",
			})
			writeModelResponse(t, w, "", []modelclient.ToolCall{
				{
					ID:   "gen-silent",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "cycledock_generate_work",
						Arguments: string(silentArgs),
					},
				},
				{
					ID:   "gen-auto",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "cycledock_generate_work",
						Arguments: string(autoArgs),
					},
				},
			})
		case 2:
			replyArgs, _ := json.Marshal(map[string]any{
				"type":            "text",
				"content":         "已为你建好两张待办卡。",
				"end_turn":        true,
				"idempotency_key": "adk-gen-reply-1",
			})
			writeModelResponse(t, w, "", []modelclient.ToolCall{
				{
					ID:   "reply-final",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "reply_message",
						Arguments: string(replyArgs),
					},
				},
			})
		default:
			t.Errorf("unexpected model step %d", step.Load())
			http.Error(w, "unexpected step", http.StatusInternalServerError)
		}
	}))
	defer modelHTTP.Close()

	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "gen-work-model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, discussion.ID, human.ID, "请建两张卡，一张先留档不启动，一张马上执行", "turn-gen-1", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})
	if err := runner.RunAttempt(ctx, *claim); err != nil {
		t.Fatalf("RunAttempt failed: %v", err)
	}

	var silentID, autoID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM cards WHERE workspace_id=$1 AND title='先留档的待办'`, workspace.ID).Scan(&silentID); err != nil {
		t.Fatalf("find silent card: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM cards WHERE workspace_id=$1 AND title='立即执行的待办'`, workspace.ID).Scan(&autoID); err != nil {
		t.Fatalf("find auto card: %v", err)
	}

	var silentTurns, autoTurns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM card_turns WHERE card_id=$1`, silentID).Scan(&silentTurns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM card_turns WHERE card_id=$1`, autoID).Scan(&autoTurns); err != nil {
		t.Fatal(err)
	}
	if silentTurns != 0 {
		t.Fatalf("auto_start=false card should have 0 turns, got %d", silentTurns)
	}
	if autoTurns != 1 {
		t.Fatalf("default auto_start card should have 1 initial turn, got %d", autoTurns)
	}
}
