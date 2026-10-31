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
	"google.golang.org/adk/v2/session"

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

func TestCommentWakesDispatcherWithoutWaitingForPoll(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "woke without the ticker", nil)
	}))
	defer modelHTTP.Close()

	workspace, _ := dataStore.CreateWorkspace(ctx, "Wake Workspace")
	human, _ := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	project, _ := dataStore.CreateProject(ctx, workspace.ID, "Project", "", "")
	card, _ := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Card", "", uuid.NullUUID{}, 1)
	if _, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "fake-model")); err != nil {
		t.Fatal(err)
	}

	// Polling and maintenance are deliberately far beyond the assertion window;
	// a fast completion therefore proves SubmitSessionComment triggered Wake.
	runCtx, cancel := context.WithCancel(ctx)
	dispatcher := NewDispatcher(dataStore, modelclient.New(), nil, DispatcherConfig{
		PollInterval: time.Hour, MaintenanceEvery: time.Hour,
		MaxConcurrent: 1, LeaseDuration: time.Minute,
		SessionService: session.InMemoryService(),
	})
	dataStore.SetWorkReadyNotifier(dispatcher.Wake)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := dispatcher.Run(runCtx); err != nil {
			t.Errorf("dispatcher: %v", err)
		}
	}()

	turn, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "start now", IdempotencyKey: "wake-turn",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		cancel()
		wg.Wait()
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var status string
	var scanErr error
	for {
		scanErr = pool.QueryRow(ctx,
			"SELECT status FROM turn_attempts WHERE workspace_id=$1 AND turn_id=$2",
			workspace.ID, turn.TurnID,
		).Scan(&status)
		if scanErr == nil && status == string(domain.AttemptStatusCompleted) {
			break
		}
		if scanErr != nil && !errors.Is(scanErr, pgx.ErrNoRows) {
			cancel()
			wg.Wait()
			t.Fatal(scanErr)
		}
		if time.Now().After(deadline) {
			cancel()
			wg.Wait()
			t.Fatalf("comment was not claimed without the poll, status=%q err=%v", status, scanErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	wg.Wait()
}

func TestTwoDispatchersExecuteOneTurnOnce(t *testing.T) {
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
	turn, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "execute exactly once", IdempotencyKey: "dispatcher-turn",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	dispatcherConfig := DispatcherConfig{
		PollInterval: 10 * time.Millisecond, MaintenanceEvery: time.Second,
		MaxConcurrent: 1, LeaseDuration: time.Minute,
		SessionService: session.InMemoryService(),
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
			"SELECT status FROM turn_attempts WHERE workspace_id=$1 AND turn_id=$2",
			workspace.ID, turn.TurnID,
		).Scan(&status)
		if err == nil && status == string(domain.AttemptStatusCompleted) {
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
			t.Fatalf("turn did not complete, last status=%q err=%v", status, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	wg.Wait()

	mu.Lock()
	calls := modelCalls
	mu.Unlock()
	if calls != 1 {
		t.Fatalf("two dispatchers executed one turn %d times", calls)
	}
}

func TestRunnerLateModelResponseAfterInterruptCannotPersistResult(t *testing.T) {

	dataStore, _ := newAgentIntegrationStore(t)
	ctx := context.Background()

	t.Run("provider timeout", func(t *testing.T) {
		modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(1200 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			writeModelResponse(t, w, "late response", nil)
		}))
		defer modelHTTP.Close()
		status, summary, _, _ := runFailureScenario(t, dataStore, ctx, modelHTTP.URL, time.Second, "")
		if status != string(domain.AttemptStatusFailed) || summary != "Built-in Agent operation timed out." {
			t.Fatalf("status=%q summary=%q", status, summary)
		}
	})

	t.Run("malformed provider response", func(t *testing.T) {
		modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{not-json"))
		}))
		defer modelHTTP.Close()
		status, summary, _, _ := runFailureScenario(t, dataStore, ctx, modelHTTP.URL, 0, "")
		if status != string(domain.AttemptStatusFailed) || summary != "Model provider returned an invalid response." {
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
		status, summary, _, _ := runFailureScenario(t, dataStore, ctx, modelHTTP.URL, 0, "")
		if status != string(domain.AttemptStatusFailed) ||
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

		status, summary, _, _ := runFailureScenario(t, dataStore, ctx, modelHTTP.URL, 0, mcpHTTP.URL)
		if status != string(domain.AttemptStatusFailed) || summary != "Built-in Agent MCP connection or tool call failed." {
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

		var modelCalls int
		modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			modelCalls++
			if modelCalls == 1 {
				writeModelResponse(t, w, "", []modelclient.ToolCall{{
					ID: "external-failure", Type: "function",
					Function: modelclient.ToolFunction{Name: "mcp_failtool_boom", Arguments: `{"value":"x"}`},
				}})
				return
			}
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "explain-failure", Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"text","content":"这次没做成，工具连接中断了。","end_turn":true,"idempotency_key":"explain-mcp-failure"}`,
				},
			}})
		}))
		defer modelHTTP.Close()

		status, summary, workspaceID, cardID := runFailureScenario(t, dataStore, ctx, modelHTTP.URL, 0, mcpHTTP.URL)
		if status != string(domain.AttemptStatusCompleted) || summary != "" {
			t.Fatalf("status=%q summary=%q", status, summary)
		}
		events, err := dataStore.ListCardEvents(ctx, workspaceID, cardID)
		if err != nil {
			t.Fatal(err)
		}
		var explained bool
		for _, event := range events {
			if strings.Contains(event.Content, "这次没做成，工具连接中断了。") {
				explained = true
			}
		}
		if !explained {
			t.Fatal("transport failure was not explained in a reply")
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
) (string, string, uuid.UUID, uuid.UUID) {
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
	if _, err := dataStore.CreateHumanTurn(
		ctx, workspace.ID, card.ID, human.ID,
		"exercise failure recovery", "failure-"+uuid.NewString(), domain.DispatchModeEnqueue,
	); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(
		ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Attempt.RunnerSourceID != settings.SourceID {
		t.Fatalf("claim source=%s settings source=%s", claim.Attempt.RunnerSourceID, settings.SourceID)
	}

	dispatcher := NewDispatcher(dataStore, modelclient.New(), nil, DispatcherConfig{
		MaxConcurrent: 1, LeaseDuration: time.Minute,
		SessionService: session.InMemoryService(),
	})
	runCtx := ctx
	cancel := func() {}
	if modelTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, modelTimeout)
	}
	defer cancel()
	dispatcher.runAttempt(runCtx, *claim)
	attempt, err := dataStore.GetAttempt(ctx, workspace.ID, claim.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	return string(attempt.Status), attempt.ErrorSummary, workspace.ID, card.ID
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
