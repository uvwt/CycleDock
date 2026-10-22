package agent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func testRunnerSettings(modelURL, model string) store.AgentSettingsInput {
	return store.AgentSettingsInput{
		Routes: []store.ModelRouteInput{{
			Enabled: true, ProviderType: "openai_compatible",
			ProviderBaseURL: modelURL, Model: model,
		}},
	}
}

func TestRunnerGeneratesFirstDiscussionMetadata(t *testing.T) {
	dataStore, _ := newAgentIntegrationStore(t)
	ctx := context.Background()

	var mu sync.Mutex
	var requests []modelRequest
	metadataStarted := make(chan struct{}, 1)
	releaseMetadata := make(chan struct{})
	releasedMetadata := false
	defer func() {
		if !releasedMetadata {
			close(releaseMetadata)
		}
	}()
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request modelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if requestIncludesTool(request, "cycledock_set_discussion_metadata") {
			select {
			case metadataStarted <- struct{}{}:
			default:
			}
			<-releaseMetadata
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "discussion-metadata-1", Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "cycledock_set_discussion_metadata",
					Arguments: `{"title":"Plan release rollout","description":"Clarify release scope, rollout steps, and monitoring expectations."}`,
				},
			}})
			return
		}
		writeModelResponse(t, w, "I have summarized the discussion and will keep clarifying the rollout before generating work.", nil)
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Discussion metadata E2E")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Release", "", "")
	if err != nil {
		t.Fatal(err)
	}
	discussion, err := dataStore.CreateDiscussion(ctx, workspace.ID, project.ID, human.ID)
	if err != nil {
		t.Fatal(err)
	}
	if discussion.Title != "" {
		t.Fatalf("new discussion title=%q, want empty", discussion.Title)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "fake-model"))
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := dataStore.SubmitComment(ctx, store.SubmitCommentParams{
		WorkspaceID: workspace.ID, CardID: discussion.ID, AuthorSourceID: human.ID,
		Content:        "Help me plan a release rollout and decide what work should come out of it.",
		IdempotencyKey: "discussion-metadata-trigger", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.TriggerID != trigger.TriggerID || claim.SourceID != settings.SourceID {
		t.Fatalf("claim=%+v trigger=%+v settings=%+v", claim, trigger, settings)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		HeartbeatInterval: 50 * time.Millisecond, LeaseDuration: time.Minute,
	})
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx, *claim)
	}()

	select {
	case <-metadataStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("metadata request did not start")
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("main Run waited for blocked discussion metadata")
	}

	card, err := dataStore.GetCard(ctx, workspace.ID, discussion.ID)
	if err != nil {
		t.Fatal(err)
	}
	if card.Title != "" {
		t.Fatalf("metadata completed before release: title=%q", card.Title)
	}

	close(releaseMetadata)
	releasedMetadata = true
	runner.waitBackground()

	card, err = dataStore.GetCard(ctx, workspace.ID, discussion.ID)
	if err != nil {
		t.Fatal(err)
	}
	if card.Title != "Plan release rollout" ||
		card.Description != "Clarify release scope, rollout steps, and monitoring expectations." {
		t.Fatalf("discussion metadata=%q / %q", card.Title, card.Description)
	}
	if card.Kind != string(domain.CardKindDiscussion) ||
		!card.DiscussionState.Valid ||
		card.DiscussionState.String != string(domain.DiscussionStateActive) {
		t.Fatalf("discussion lifecycle changed during first run: %+v", card)
	}

	mu.Lock()
	captured := append([]modelRequest(nil), requests...)
	mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("model request count=%d, want 2", len(captured))
	}
	var metadataRequests, mainRequests int
	for _, request := range captured {
		if !containsMessage(request.Messages, "Help me plan a release rollout") {
			t.Fatalf("model request missing discussion context: %+v", request.Messages)
		}
		if requestIncludesTool(request, "cycledock_set_discussion_metadata") {
			metadataRequests++
		} else {
			mainRequests++
		}
	}
	if metadataRequests != 1 || mainRequests != 1 {
		t.Fatalf("metadata requests=%d main requests=%d, want 1/1", metadataRequests, mainRequests)
	}
}

func TestRunnerUsesEphemeralMCPResultAndPersistsOnlyDurableKnowledge(t *testing.T) {
	dataStore, _ := newAgentIntegrationStore(t)
	ctx := context.Background()

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "agent-test", Version: "1.0.0"}, nil)
	type echoInput struct {
		Value string `json:"value"`
	}
	type echoOutput struct {
		Echo string `json:"echo"`
	}
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "echo", Description: "return raw external evidence"},
		func(context.Context, *mcp.CallToolRequest, echoInput) (*mcp.CallToolResult, echoOutput, error) {
			return nil, echoOutput{Echo: "RAW_EPHEMERAL_123"}, nil
		},
	)
	mcpHTTP := httptest.NewServer(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil),
	)
	defer mcpHTTP.Close()

	var mu sync.Mutex
	var requests []modelRequest
	var callCount int
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request modelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, request)
		callCount++
		current := callCount
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch current {
		case 1:
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "mcp-call-1", Type: "function",
				Function: modelclient.ToolFunction{
					Name: "mcp_demo_echo", Arguments: `{"value":"inspect"}`,
				},
			}})
		case 2:
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "finding-call-1", Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "cycledock_add_finding",
					Arguments: `{"content":"Durable distilled finding","idempotency_key":"runner-finding-1"}`,
				},
			}})
		default:
			writeModelResponse(t, w, "Durable final result from built-in runner.", nil)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Agent E2E")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Runtime Project", "latest project", "Verify external evidence before persisting findings.")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Investigate", "latest card description", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "fake-model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name: "demo", URL: mcpHTTP.URL, Enabled: true, TimeoutMS: 5000,
	}); err != nil {
		t.Fatal(err)
	}
	trigger, err := dataStore.SubmitComment(ctx, store.SubmitCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "Use MCP evidence and produce a durable finding.", IdempotencyKey: "runner-trigger-1",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.TriggerID != trigger.TriggerID || claim.SourceID != settings.SourceID {
		t.Fatalf("claim=%+v trigger=%+v settings=%+v", claim, trigger, settings)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		HeartbeatInterval: 50 * time.Millisecond, LeaseDuration: time.Minute,
	})
	if err := runner.Run(ctx, *claim); err != nil {
		t.Fatalf("run: %v", err)
	}

	run, err := dataStore.GetRun(ctx, workspace.ID, claim.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != string(domain.RunStatusCompleted) ||
		!strings.Contains(run.ResultSummary, "Durable final result") {
		t.Fatalf("run=%+v", run)
	}
	events, err := dataStore.ListRecentCardTimeline(ctx, workspace.ID, card.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sawFinding, sawResult bool
	for _, event := range events {
		if event.EventType == string(domain.EventTypeFinding) && event.Content == "Durable distilled finding" {
			sawFinding = true
		}
		if event.EventType == string(domain.EventTypeResult) && strings.Contains(event.Content, "Durable final result") {
			sawResult = true
		}
		if strings.Contains(event.Content, "RAW_EPHEMERAL_123") {
			t.Fatalf("raw MCP result leaked into durable Timeline: %+v", event)
		}
	}
	if !sawFinding || !sawResult {
		t.Fatalf("durable events missing finding=%v result=%v events=%+v", sawFinding, sawResult, events)
	}

	mu.Lock()
	captured := append([]modelRequest(nil), requests...)
	mu.Unlock()
	if len(captured) < 3 {
		t.Fatalf("model request count=%d", len(captured))
	}
	if !containsMessage(captured[0].Messages, "Use MCP evidence") ||
		!containsMessage(captured[0].Messages, "Verify external evidence") {
		t.Fatalf("initial model context missing durable project/trigger state: %+v", captured[0].Messages)
	}
	if !containsMessage(captured[1].Messages, "RAW_EPHEMERAL_123") {
		t.Fatal("current Run model loop did not receive MCP tool result")
	}

	secondTrigger, err := dataStore.SubmitComment(ctx, store.SubmitCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "Start a clean second execution.", IdempotencyKey: "runner-trigger-2",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondClaim, err := dataStore.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if secondClaim.TriggerID != secondTrigger.TriggerID {
		t.Fatalf("second claim=%+v", secondClaim)
	}
	secondContext, err := dataStore.LoadAgentRunContext(
		ctx, workspace.ID, secondClaim.RunID, settings.SourceID, 100,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondMessages := BuildContext(secondContext, settings.WorkspaceAgentSetting)
	if !containsModelMessage(secondMessages, "Durable distilled finding") ||
		!containsModelMessage(secondMessages, "Durable final result") {
		t.Fatalf("second Run lost durable knowledge: %+v", secondMessages)
	}
	if containsModelMessage(secondMessages, "RAW_EPHEMERAL_123") ||
		containsModelMessage(secondMessages, "mcp-call-1") {
		t.Fatalf("second Run inherited raw tool trace: %+v", secondMessages)
	}

}

func TestRunnerFailsOverOnceAndKeepsSuccessfulRouteForLaterToolRounds(t *testing.T) {
	dataStore, _ := newAgentIntegrationStore(t)
	ctx := context.Background()

	var primaryCalls int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer primary.Close()

	var secondaryCalls int
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalls++
		w.Header().Set("Content-Type", "application/json")
		if secondaryCalls == 1 {
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "fallback-finding", Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "cycledock_add_finding",
					Arguments: `{"content":"fallback route is healthy","idempotency_key":"fallback-finding"}`,
				},
			}})
			return
		}
		writeModelResponse(t, w, "fallback completed the run", nil)
	}))
	defer secondary.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Failover "+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Project", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Failover", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, store.AgentSettingsInput{
		Routes: []store.ModelRouteInput{
			{Enabled: true, ProviderType: "openai_compatible", ProviderBaseURL: primary.URL, Model: "primary"},
			{Enabled: true, ProviderType: "openai_compatible", ProviderBaseURL: secondary.URL, Model: "secondary"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SubmitComment(ctx, store.SubmitCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "exercise model failover", IdempotencyKey: "fallback-trigger",
		DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.SourceID != settings.SourceID {
		t.Fatalf("claim source=%s settings source=%s", claim.SourceID, settings.SourceID)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{LeaseDuration: time.Minute})
	if err := runner.Run(ctx, *claim); err != nil {
		t.Fatalf("run with fallback: %v", err)
	}
	if primaryCalls != 1 {
		t.Fatalf("primary route calls=%d, want 1; successful fallback must stay sticky", primaryCalls)
	}
	if secondaryCalls != 2 {
		t.Fatalf("secondary route calls=%d, want 2 across tool rounds", secondaryCalls)
	}
	run, err := dataStore.GetRun(ctx, workspace.ID, claim.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != string(domain.RunStatusCompleted) || run.ResultSummary != "fallback completed the run" {
		t.Fatalf("run after fallback=%+v", run)
	}
}

func TestRetryableModelRouteErrorClassification(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "rate limit", err: &modelclient.APIError{StatusCode: 429}, want: true},
		{name: "provider unavailable", err: &modelclient.APIError{StatusCode: 503}, want: true},
		{name: "bad request", err: &modelclient.APIError{StatusCode: 400}, want: false},
		{name: "model auth unavailable", err: modelauth.ErrUnavailable, want: true},
		{name: "reauth", err: modelauth.ErrReauthRequired, want: true},
		{name: "malformed response", err: modelclient.ErrMalformedResponse, want: true},
		{name: "cancelled", err: context.Canceled, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isRetryableModelRouteError(ctx, test.err); got != test.want {
				t.Fatalf("retryable=%v, want %v for %v", got, test.want, test.err)
			}
		})
	}
}

func TestRunnerWaitForHumanStopsWithoutResult(t *testing.T) {
	dataStore, _ := newAgentIntegrationStore(t)
	ctx := context.Background()

	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "", []modelclient.ToolCall{{
			ID: "wait-1", Type: "function",
			Function: modelclient.ToolFunction{
				Name:      "cycledock_wait_for_human",
				Arguments: `{"stage":"approval","summary":"Choose the deployment target."}`,
			},
		}})
	}))
	defer modelHTTP.Close()

	workspace, _ := dataStore.CreateWorkspace(ctx, "Waiting Workspace")
	human, _ := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	project, _ := dataStore.CreateProject(ctx, workspace.ID, "Project", "", "")
	card, _ := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Card", "", uuid.NullUUID{}, 1)
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "fake-model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SubmitComment(ctx, store.SubmitCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "ask if blocked", IdempotencyKey: "wait-trigger",
		DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.SourceID != settings.SourceID {
		t.Fatal("wrong built-in source")
	}
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{LeaseDuration: time.Minute})
	if err := runner.Run(ctx, *claim); err != nil {
		t.Fatalf("run: %v", err)
	}
	run, err := dataStore.GetRun(ctx, workspace.ID, claim.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != string(domain.RunStatusWaitingHuman) ||
		run.CurrentStage != "approval" ||
		!strings.Contains(run.ProgressSummary, "deployment target") ||
		run.ResultSummary != "" {
		t.Fatalf("run=%+v", run)
	}
}

func TestTwoDispatchersExecuteOneTriggerOnce(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	var modelCalls int
	var mu sync.Mutex
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		modelCalls++
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "one dispatcher result", nil)
	}))
	defer modelHTTP.Close()

	workspace, _ := dataStore.CreateWorkspace(ctx, "Dispatcher Workspace")
	human, _ := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	project, _ := dataStore.CreateProject(ctx, workspace.ID, "Project", "", "")
	card, _ := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Card", "", uuid.NullUUID{}, 1)
	if _, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "fake-model")); err != nil {
		t.Fatal(err)
	}
	trigger, err := dataStore.SubmitComment(ctx, store.SubmitCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "execute exactly once", IdempotencyKey: "dispatcher-trigger",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	dispatcherConfig := DispatcherConfig{
		PollInterval: 10 * time.Millisecond, MaintenanceEvery: time.Second,
		MaxConcurrent: 1, LeaseDuration: time.Minute,
	}
	dispatchers := []*Dispatcher{
		NewDispatcher(dataStore, modelclient.New(), nil, dispatcherConfig),
		NewDispatcher(dataStore, modelclient.New(), nil, dispatcherConfig),
	}
	var wg sync.WaitGroup
	for _, dispatcher := range dispatchers {
		wg.Add(1)
		go func(d *Dispatcher) {
			defer wg.Done()
			if err := d.Run(runCtx); err != nil {
				t.Errorf("dispatcher: %v", err)
			}
		}(dispatcher)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		var status string
		err := pool.QueryRow(ctx,
			"SELECT status FROM agent_runs WHERE workspace_id=$1 AND trigger_id=$2",
			workspace.ID, trigger.TriggerID,
		).Scan(&status)
		if err == nil && status == string(domain.RunStatusCompleted) {
			break
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			cancel()
			wg.Wait()
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			cancel()
			wg.Wait()
			t.Fatalf("trigger did not complete, last status=%q err=%v", status, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	wg.Wait()

	mu.Lock()
	calls := modelCalls
	mu.Unlock()
	if calls != 1 {
		t.Fatalf("two dispatchers executed one trigger %d times", calls)
	}
}

func TestRunnerLateModelResponseAfterInterruptCannotPersistResult(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	started := make(chan struct{})
	release := make(chan struct{})
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "STALE_LATE_MODEL_RESULT", nil)
	}))
	defer modelHTTP.Close()

	workspace, _ := dataStore.CreateWorkspace(ctx, "Interrupt Workspace")
	human, _ := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	project, _ := dataStore.CreateProject(ctx, workspace.ID, "Project", "", "")
	card, _ := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Card", "", uuid.NullUUID{}, 1)
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "fake-model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SubmitComment(ctx, store.SubmitCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "old direction", IdempotencyKey: "late-old",
		DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		HeartbeatInterval: time.Second, LeaseDuration: time.Minute,
	})
	result := make(chan error, 1)
	go func() { result <- runner.Run(ctx, *claim) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("model request did not start")
	}

	interrupt, err := dataStore.SubmitComment(ctx, store.SubmitCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "new direction", IdempotencyKey: "late-new",
		DispatchMode: domain.DispatchModeInterrupt,
	})
	if err != nil || interrupt.RunID == nil {
		t.Fatalf("interrupt=%+v err=%v", interrupt, err)
	}
	close(release)

	err = <-result
	if !errors.Is(err, domain.ErrRunNotCurrent) {
		t.Fatalf("late runner error=%v", err)
	}
	if settings.SourceID != claim.SourceID {
		t.Fatal("wrong runner source")
	}
	var staleResultCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM events WHERE workspace_id=$1 AND card_id=$2 AND event_type='result' AND content='STALE_LATE_MODEL_RESULT'",
		workspace.ID, card.ID,
	).Scan(&staleResultCount); err != nil {
		t.Fatal(err)
	}
	if staleResultCount != 0 {
		t.Fatalf("stale model result persisted %d times", staleResultCount)
	}
}

func TestDispatcherPersistsExplicitRuntimeFailureClasses(t *testing.T) {
	dataStore, _ := newAgentIntegrationStore(t)
	ctx := context.Background()

	t.Run("provider timeout", func(t *testing.T) {
		modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(1200 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			writeModelResponse(t, w, "late response", nil)
		}))
		defer modelHTTP.Close()
		status, summary := runFailureScenario(t, dataStore, ctx, modelHTTP.URL, time.Second, "")
		if status != string(domain.RunStatusFailed) || summary != "Built-in Agent operation timed out." {
			t.Fatalf("status=%q summary=%q", status, summary)
		}
	})

	t.Run("malformed provider response", func(t *testing.T) {
		modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{not-json"))
		}))
		defer modelHTTP.Close()
		status, summary := runFailureScenario(t, dataStore, ctx, modelHTTP.URL, 0, "")
		if status != string(domain.RunStatusFailed) || summary != "Model provider returned an invalid response." {
			t.Fatalf("status=%q summary=%q", status, summary)
		}
	})

	t.Run("runaway tool loop guard", func(t *testing.T) {
		modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "loop", Type: "function",
				Function: modelclient.ToolFunction{Name: "unknown_loop_tool", Arguments: "{}"},
			}})
		}))
		defer modelHTTP.Close()
		status, summary := runFailureScenario(t, dataStore, ctx, modelHTTP.URL, 0, "")
		if status != string(domain.RunStatusFailed) ||
			summary != "Built-in Agent stopped an unusually long tool loop before completion." {
			t.Fatalf("status=%q summary=%q", status, summary)
		}
	})

	t.Run("MCP initialize failure", func(t *testing.T) {
		modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			writeModelResponse(t, w, "should not execute", nil)
		}))
		defer modelHTTP.Close()
		mcpHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "MCP unavailable", http.StatusBadGateway)
		}))
		defer mcpHTTP.Close()

		status, summary := runFailureScenario(t, dataStore, ctx, modelHTTP.URL, 0, mcpHTTP.URL)
		if status != string(domain.RunStatusFailed) || summary != "Built-in Agent MCP connection or tool call failed." {
			t.Fatalf("status=%q summary=%q", status, summary)
		}
	})

	t.Run("MCP tool transport failure", func(t *testing.T) {
		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "failure-test", Version: "1.0.0"}, nil)
		type input struct {
			Value string `json:"value"`
		}
		type output struct {
			Value string `json:"value"`
		}
		mcp.AddTool(mcpServer, &mcp.Tool{Name: "boom"},
			func(context.Context, *mcp.CallToolRequest, input) (*mcp.CallToolResult, output, error) {
				return nil, output{Value: "unexpected"}, nil
			},
		)
		streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
		mcpHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read failed", http.StatusInternalServerError)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if bytes.Contains(raw, []byte(`"method":"tools/call"`)) {
				http.Error(w, "tool transport failed", http.StatusBadGateway)
				return
			}
			streamable.ServeHTTP(w, r)
		}))
		defer mcpHTTP.Close()

		modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "external-failure", Type: "function",
				Function: modelclient.ToolFunction{Name: "mcp_failtool_boom", Arguments: `{"value":"x"}`},
			}})
		}))
		defer modelHTTP.Close()

		status, summary := runFailureScenario(t, dataStore, ctx, modelHTTP.URL, 0, mcpHTTP.URL)
		if status != string(domain.RunStatusFailed) || summary != "Built-in Agent MCP connection or tool call failed." {
			t.Fatalf("status=%q summary=%q", status, summary)
		}
	})
}

func runFailureScenario(
	t *testing.T,
	dataStore *store.Store,
	ctx context.Context,
	modelURL string,
	modelTimeout time.Duration,
	mcpURL string,
) (string, string) {
	t.Helper()
	workspace, err := dataStore.CreateWorkspace(ctx, "Failure "+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Project", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Failure card", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelURL, "failure-model"))
	if err != nil {
		t.Fatal(err)
	}
	if mcpURL != "" {
		if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
			Name: "failtool", URL: mcpURL, Enabled: true, TimeoutMS: 2000,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dataStore.SubmitComment(ctx, store.SubmitCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "exercise failure recovery", IdempotencyKey: "failure-" + uuid.NewString(),
		DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim.SourceID != settings.SourceID {
		t.Fatalf("claim source=%s settings source=%s", claim.SourceID, settings.SourceID)
	}

	dispatcher := NewDispatcher(dataStore, modelclient.New(), nil, DispatcherConfig{
		MaxConcurrent: 1, LeaseDuration: time.Minute,
	})
	runCtx := ctx
	cancel := func() {}
	if modelTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, modelTimeout)
	}
	defer cancel()
	dispatcher.runClaim(runCtx, *claim)
	run, err := dataStore.GetRun(ctx, workspace.ID, claim.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return run.Status, run.ErrorSummary
}

type modelRequest struct {
	Model    string                `json:"model"`
	Messages []modelclient.Message `json:"messages"`
	Tools    []any                 `json:"tools"`
}

func requestIncludesTool(request modelRequest, name string) bool {
	raw, err := json.Marshal(request.Tools)
	if err != nil {
		return false
	}
	return bytes.Contains(raw, []byte(`"name":"`+name+`"`))
}

func writeModelResponse(t *testing.T, w http.ResponseWriter, content string, toolCalls []modelclient.ToolCall) {
	t.Helper()
	response := map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"content":    content,
				"tool_calls": toolCalls,
			},
		}},
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Errorf("encode model response: %v", err)
	}
}

func containsMessage(messages []modelclient.Message, needle string) bool {
	return containsModelMessage(messages, needle)
}

func containsModelMessage(messages []modelclient.Message, needle string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

func newAgentIntegrationStore(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	adminPool, err := pgxpool.New(context.Background(), baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)

	schema := "agent_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(context.Background(), "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
	})

	query := parsedURL.Query()
	query.Set("search_path", schema)
	parsedURL.RawQuery = query.Encode()
	scopedURL := parsedURL.String()

	migrationDB, err := sql.Open("pgx", scopedURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrationDB.Close() })
	_, filename, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(migrationDB, migrations); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), scopedURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return store.New(pool), pool
}
