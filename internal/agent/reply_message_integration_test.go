package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestADKReplyMessageMultipleBubblesAndChoiceWait(t *testing.T) {
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
			}})
		case 2:
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "reply-2", Type: "function",
				Function: modelclient.ToolFunction{Name: "reply_message",
					Arguments: `{"type":"text","content":"Everything looks normal","end_turn":true,"idempotency_key":"reply-2"}`},
			}})
		case 3:
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
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Messages", "", "")
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
		if i == 1 {
			expected = domain.AttemptStatusWaitingHuman
		}
		if attempt.Status != expected {
			t.Fatalf("attempt %d status=%s want %s", i, attempt.Status, expected)
		}
	}
	if modelCalls != 3 {
		t.Fatalf("model calls=%d want 3", modelCalls)
	}
	timeline, err := dataStore.ListCardTimeline(ctx, workspace.ID, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	var bubbles, results int
	for _, e := range timeline {
		if e.EventType == "message" {
			bubbles++
		}
		if e.EventType == "result" {
			results++
		}
	}
	if bubbles != 3 || results != 0 {
		t.Fatalf("message bubbles=%d result events=%d", bubbles, results)
	}
	// waiting_human 仍是 Attempt 状态，但用户回覆会创建新的 Turn，而非调用旧等待工具。
	reply, err := dataStore.ReplySessionAndContinue(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "yes", IdempotencyKey: "choice-answer",
	})
	if err != nil || reply.TurnID == uuid.Nil {
		t.Fatalf("reply and continue=%+v err=%v", reply, err)
	}
}
