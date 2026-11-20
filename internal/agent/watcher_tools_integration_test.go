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

func TestBuiltInWatcherTools_WatchEventAndScheduleWakeup(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	workspace, err := dataStore.CreateWorkspace(ctx, "Watcher Tools Workspace")
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
	todoCard, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "CI Watch Todo", "Watch PR CI", uuid.NullUUID{}, 1)
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
		if !requestIncludesTool(body, "cycledock_watch_event") || !requestIncludesTool(body, "cycledock_schedule_wakeup") {
			t.Errorf("expected watcher tools in built-in tool list")
		}

		w.Header().Set("Content-Type", "application/json")
		switch step.Add(1) {
		case 1:
			watchArgs, _ := json.Marshal(map[string]any{
				"match_key":       "github:uvwt/CycleDock:pr:88:ci_completed",
				"note":            "PR #88 CI 完成后继续验证",
				"timeout_minutes": 45,
				"idempotency_key": "agent-watch-pr-88",
			})
			wakeupArgs, _ := json.Marshal(map[string]any{
				"after_minutes":   15,
				"note":            "15 分钟后复查部署状态",
				"idempotency_key": "agent-wakeup-15m",
			})
			writeModelResponse(t, w, "", []modelclient.ToolCall{
				{
					ID:   "watch-1",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "cycledock_watch_event",
						Arguments: string(watchArgs),
					},
				},
				{
					ID:   "wakeup-1",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "cycledock_schedule_wakeup",
						Arguments: string(wakeupArgs),
					},
				},
			})
		case 2:
			writeModelResponse(t, w, "", []modelclient.ToolCall{
				{
					ID:   "list-1",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "cycledock_list_watchers",
						Arguments: `{"armed_only":true}`,
					},
				},
			})
		case 3:
			rawMessages, _ := json.Marshal(body.Messages)
			if !strings.Contains(string(rawMessages), "github:uvwt/cycledock:pr:88:ci_completed") {
				t.Errorf("expected normalized match_key in list_watchers output: %s", string(rawMessages))
			}
			writeModelResponse(t, w, "", []modelclient.ToolCall{
				{
					ID:   "reply-end",
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      "reply_message",
						Arguments: `{"type":"text","content":"已挂起等待 PR #88 CI 完成，并登记 15 分钟复查提醒。","end_turn":true,"idempotency_key":"watcher-reply-done"}`,
					},
				},
			})
		default:
			t.Errorf("unexpected model step %d", step.Load())
			writeModelResponse(t, w, "unexpected", nil)
		}
	}))
	defer modelHTTP.Close()

	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "watcher-test-model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, todoCard.ID, human.ID, "请挂起等待 PR #88 CI", "human-watch-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}

	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim attempt: %v", err)
	}
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService: agentsession.NewPostgresService(pool),
		Live:           realtime.NewHub(),
	})
	if err := runner.RunAttempt(ctx, *claim); err != nil {
		t.Fatalf("run attempt: %v", err)
	}

	watchers, err := dataStore.ListCardWatchers(ctx, workspace.ID, todoCard.ID, true, 10)
	if err != nil {
		t.Fatalf("list watchers: %v", err)
	}
	if len(watchers) != 2 {
		t.Fatalf("expected 2 armed watchers, got %d", len(watchers))
	}
}
