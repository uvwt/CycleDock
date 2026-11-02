package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	adkagent "google.golang.org/adk/v2/agent"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestBuiltInCardMessageQueuesAndRunsIsolatedTargetSession(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()
	workspace, err := dataStore.CreateWorkspace(ctx, "Cross-card messages")
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
	source, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Source", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	target, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Target", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		current := calls.Add(1)
		switch current {
		case 1, 2:
			if !requestIncludesTool(body, "cycledock_send_card_message") {
				t.Error("built-in model did not receive the cross-card message tool")
			}
			args, _ := json.Marshal(map[string]any{
				"card_id": target.ID.String(), "content": "handoff-only: inspect the target task", "idempotency_key": "cross-card-handoff",
			})
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: fmt.Sprintf("handoff-%d", current), Type: "function",
				Function: modelclient.ToolFunction{Name: "cycledock_send_card_message", Arguments: string(args)},
			}})
		case 3:
			writeModelResponse(t, w, "The target task has been queued.", nil)
		case 4:
			messages, _ := json.Marshal(body.Messages)
			if !strings.Contains(string(messages), "handoff-only") || strings.Contains(string(messages), "source-private-context") {
				t.Error("target session must receive the handoff without source session history")
			}
			writeModelResponse(t, w, "Target task received and executed.", nil)
		default:
			t.Errorf("unexpected model call %d", current)
			writeModelResponse(t, w, "unexpected", nil)
		}
	}))
	defer modelHTTP.Close()
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "cross-card-model"))
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond, LeaseDuration: time.Minute,
	})
	if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, source.ID, human.ID,
		"source-private-context: send the handoff to the target card", "source-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	sourceClaim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var wakes atomic.Int32
	dataStore.SetWorkReadyNotifier(func() { wakes.Add(1) })
	if err := runner.RunAttempt(ctx, *sourceClaim); err != nil {
		t.Fatal(err)
	}
	pending, err := dataStore.ListPendingTurns(ctx, workspace.ID, target.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("target pending turns=%+v err=%v", pending, err)
	}
	if wakes.Load() == 0 {
		t.Error("message did not wake the dispatcher")
	}
	var targetSessionEvents int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM agent_session_events WHERE user_id=$1 AND session_id=$2", workspace.ID.String(), target.ID.String()).Scan(&targetSessionEvents); err != nil || targetSessionEvents != 0 {
		t.Fatalf("queued turn leaked into target session before claim: events=%d err=%v", targetSessionEvents, err)
	}
	timeline, err := dataStore.ListCardTimeline(ctx, workspace.ID, target.ID)
	if err != nil || len(timeline) != 1 || timeline[0].ActorSourceID != settings.SourceID {
		t.Fatalf("handoff must be a single agent-authored event: %+v err=%v", timeline, err)
	}
	if !timeline[0].SourceCardID.Valid || timeline[0].SourceCardID.UUID != source.ID {
		t.Fatalf("source card must be recorded on delegated event: %+v", timeline[0])
	}
	if timeline[0].SourceCardTitle != source.Title {
		t.Fatalf("source card title mismatch: got %q, want %q", timeline[0].SourceCardTitle, source.Title)
	}
	targetClaim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if targetClaim == nil || targetClaim.Attempt.CardID != target.ID {
		t.Fatalf("wrong target claim: %+v", targetClaim)
	}
	if err := runner.RunAttempt(ctx, *targetClaim); err != nil {
		t.Fatal(err)
	}
	timeline, err = dataStore.ListCardTimeline(ctx, workspace.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range timeline {
		if event.Content == "Target task received and executed." {
			found = true
		}
	}
	if !found || calls.Load() != 4 {
		t.Fatalf("target did not execute handoff: found=%v model_calls=%d", found, calls.Load())
	}

	runtime := attemptToolRuntime{store: dataStore, context: store.AgentCardContext{
		WorkspaceID: workspace.ID, CardID: source.ID, RunnerSourceID: settings.SourceID,
	}}
	handler, err := runtime.internalHandler("cycledock_send_card_message")
	if err != nil {
		t.Fatal(err)
	}
	toolCtx := adkagent.NewStrictContextMock(ctx)
	foreignWorkspace, err := dataStore.CreateWorkspace(ctx, "Other tenant")
	if err != nil {
		t.Fatal(err)
	}
	foreignHuman, err := dataStore.CreateSource(ctx, foreignWorkspace.ID, domain.SourceKindHuman, "Other human")
	if err != nil {
		t.Fatal(err)
	}
	foreignProject, err := dataStore.CreateProject(ctx, foreignWorkspace.ID, "Other project", "")
	if err != nil {
		t.Fatal(err)
	}
	foreignCard, err := dataStore.CreateTodoCard(ctx, foreignWorkspace.ID, foreignProject.ID, foreignHuman.ID, "Other card", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := dataStore.CreateDiscussion(ctx, workspace.ID, project.ID, human.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.ArchiveDiscussion(ctx, workspace.ID, archived.ID, human.ID); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name, cardID, content, key string
		want                       error
	}{
		{name: "cross tenant", cardID: foreignCard.ID.String(), content: "private", key: "foreign", want: domain.ErrNotFound},
		{name: "idempotency mismatch", cardID: target.ID.String(), content: "changed", key: "cross-card-handoff", want: domain.ErrIdempotencyMismatch},
		{name: "missing target", cardID: uuid.NewString(), content: "message", key: "missing", want: domain.ErrNotFound},
		{name: "archived discussion", cardID: archived.ID.String(), content: "message", key: "archived", want: domain.ErrInvalidTransition},
		{name: "empty message", cardID: target.ID.String(), key: "empty", want: domain.ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := handler(&toolCtx, map[string]any{"card_id": test.cardID, "content": test.content, "idempotency_key": test.key})
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want %v", err, test.want)
			}
		})
	}
	for _, cardID := range []string{"invalid", source.ID.String()} {
		if _, err := handler(&toolCtx, map[string]any{"card_id": cardID, "content": "message", "idempotency_key": "invalid-target"}); err == nil {
			t.Fatalf("invalid target %q accepted", cardID)
		}
	}
	// 目标有 current Attempt 时，新消息只排队，不能替换或终止它。
	if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, target.ID, human.ID, "busy target", "busy-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	busyClaim, err := dataStore.ClaimCardTurnAttempt(ctx, workspace.ID, target.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler(&toolCtx, map[string]any{"card_id": target.ID.String(), "content": "follow-up while busy", "idempotency_key": "busy-follow-up"}); err != nil {
		t.Fatal(err)
	}
	current, err := dataStore.CurrentAttemptView(ctx, workspace.ID, target.ID)
	if err != nil || current == nil || current.ID != busyClaim.Attempt.ID || current.Status != domain.AttemptStatusRunning {
		t.Fatalf("message interrupted target attempt: %+v err=%v", current, err)
	}
	pending, err = dataStore.ListPendingTurns(ctx, workspace.ID, target.ID)
	if err != nil || len(pending) != 1 || pending[0].Content != "follow-up while busy" {
		t.Fatalf("busy target did not queue follow-up: %+v err=%v", pending, err)
	}

}
