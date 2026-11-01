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
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/session"

	"github.com/uvwt/CycleDock/internal/agentartifact"
	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestADKRunnerReusesPriorTurnToolResultWithoutRepeatingToolCall(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	var toolMu sync.Mutex
	toolCalls := 0
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "session-test", Version: "1.0.0"}, nil)
	type inventoryInput struct{}
	type inventoryOutput struct {
		Containers []map[string]string `json:"containers"`
	}
	mcp.AddTool(
		mcpServer,
		&mcp.Tool{Name: "inventory", Description: "Read the current container inventory."},
		func(context.Context, *mcp.CallToolRequest, inventoryInput) (*mcp.CallToolResult, inventoryOutput, error) {
			toolMu.Lock()
			toolCalls++
			toolMu.Unlock()
			return nil, inventoryOutput{
				Containers: []map[string]string{
					{"name": "api", "image": "api:v4"},
					{"name": "nginx", "image": "nginx:1.25"},
				},
			}, nil
		},
	)
	mcpHTTP := httptest.NewServer(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil),
	)
	defer mcpHTTP.Close()

	var modelMu sync.Mutex
	modelCalls := 0
	secondTurnSawOldToolResult := false
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		modelMu.Lock()
		modelCalls++
		current := modelCalls
		modelMu.Unlock()

		rawMessages, _ := json.Marshal(body.Messages)
		hasInventoryEvidence := strings.Contains(string(rawMessages), "nginx:1.25")
		w.Header().Set("Content-Type", "application/json")
		switch current {
		case 1:
			if !requestIncludesTool(body, "mcp_lab_inventory") {
				t.Errorf("first turn did not expose inventory tool")
			}
			writeModelResponse(t, w, "I will inspect the inventory.", []modelclient.ToolCall{{
				ID:   "inventory-call-1",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "mcp_lab_inventory",
					Arguments: "{}",
				},
			}})
		case 2:
			if !hasInventoryEvidence {
				t.Errorf("tool response missing from first turn follow-up request: %s", rawMessages)
			}
			writeModelResponse(t, w, "The nginx container uses nginx:1.25.", nil)
		case 3:
			if !hasInventoryEvidence {
				t.Errorf("second turn did not receive prior tool response: %s", rawMessages)
			} else {
				secondTurnSawOldToolResult = true
			}
			writeModelResponse(t, w, "The image we saw earlier was nginx:1.25.", nil)
		default:
			t.Errorf("unexpected extra model request %d", current)
			writeModelResponse(t, w, "unexpected", nil)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "ADK Session Continuity")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Runtime", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(
		ctx,
		workspace.ID,
		project.ID,
		human.ID,
		"Inspect containers",
		"",
		uuid.NullUUID{},
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(
		ctx,
		workspace.ID,
		testRunnerSettings(modelHTTP.URL, "session-model"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name: "lab", URL: mcpHTTP.URL, Enabled: true, TimeoutMS: 5000,
	}); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})

	firstTurn, err := dataStore.CreateHumanTurn(
		ctx,
		workspace.ID,
		card.ID,
		human.ID,
		"Inspect the container inventory and tell me which image nginx uses.",
		"session-turn-1",
		domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstClaim, err := dataStore.ClaimNextTurnAttempt(
		ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstClaim.Turn.ID != firstTurn.Turn.ID {
		t.Fatalf("claimed turn=%s want=%s", firstClaim.Turn.ID, firstTurn.Turn.ID)
	}
	if err := runner.RunAttempt(ctx, *firstClaim); err != nil {
		t.Fatal(err)
	}

	timeline, err := dataStore.ListCardTimeline(ctx, workspace.ID, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, event := range timeline {
		if event.AttemptID.Valid && event.AttemptID.UUID == firstClaim.Attempt.ID &&
			event.EventType == "message" {
			messages = append(messages, event.Content)
		}
	}
	if len(messages) != 1 || messages[0] != "The nginx container uses nginx:1.25." {
		t.Fatalf("agent timeline messages=%v", messages)
	}

	secondTurn, err := dataStore.CreateHumanTurn(
		ctx,
		workspace.ID,
		card.ID,
		human.ID,
		"What image did that nginx container use in the result you already checked?",
		"session-turn-2",
		domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondClaim, err := dataStore.ClaimNextTurnAttempt(
		ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if secondClaim.Turn.ID != secondTurn.Turn.ID {
		t.Fatalf("claimed second turn=%s want=%s", secondClaim.Turn.ID, secondTurn.Turn.ID)
	}
	if err := runner.RunAttempt(ctx, *secondClaim); err != nil {
		t.Fatal(err)
	}

	toolMu.Lock()
	gotToolCalls := toolCalls
	toolMu.Unlock()
	if gotToolCalls != 1 {
		t.Fatalf("inventory tool calls=%d want exactly 1 across both turns", gotToolCalls)
	}
	if !secondTurnSawOldToolResult {
		t.Fatal("second turn did not see the prior turn tool result")
	}

	appName, userID, sessionID := agentsession.Identity(workspace.ID, card.ID)
	gotSession, err := runner.sessionService.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.Session.Events().Len() < 6 {
		t.Fatalf("persisted session events=%d, want multi-turn user/model/tool history", gotSession.Session.Events().Len())
	}
}

func TestADKRunnerExternalizesLargeToolResultAndReadsArtifactWithoutRepeatingMCP(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	const oldTarget = "EXACT_OLD_TARGET_9472"
	const secret = "SUPER_SECRET_PHASE_D"
	var toolMu sync.Mutex
	toolCalls := 0
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "artifact-test", Version: "1.0.0"}, nil)
	type largeInput struct{}
	type largeOutput struct {
		AccessToken string `json:"access_token"`
		Filler      string `json:"filler"`
		Target      string `json:"target"`
	}
	mcp.AddTool(
		mcpServer,
		&mcp.Tool{Name: "large_inventory", Description: "Return a large inventory snapshot."},
		func(context.Context, *mcp.CallToolRequest, largeInput) (*mcp.CallToolResult, largeOutput, error) {
			toolMu.Lock()
			toolCalls++
			toolMu.Unlock()
			return nil, largeOutput{
				AccessToken: secret,
				Filler:      strings.Repeat("inventory-padding-", 1200),
				Target:      oldTarget,
			}, nil
		},
	)
	mcpHTTP := httptest.NewServer(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil),
	)
	defer mcpHTTP.Close()

	var modelMu sync.Mutex
	regularCalls := 0
	var artifactRef string
	var artifactVersion int64
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(body.Tools) == 0 {
			writeModelResponse(t, w, "Artifact test summary.", nil)
			return
		}

		modelMu.Lock()
		regularCalls++
		current := regularCalls
		modelMu.Unlock()
		rawMessages, _ := json.Marshal(body.Messages)
		all := string(rawMessages)
		if strings.Contains(all, secret) {
			t.Errorf("model request leaked structured credential")
		}
		w.Header().Set("Content-Type", "application/json")

		switch current {
		case 1:
			if !requestIncludesTool(body, "mcp_lab_large_inventory") {
				t.Errorf("large MCP tool missing")
			}
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "large-call-1", Type: "function",
				Function: modelclient.ToolFunction{Name: "mcp_lab_large_inventory", Arguments: "{}"},
			}})
		case 2:
			artifactRef, artifactVersion = extractArtifactReference(body.Messages)
			if artifactRef == "" || artifactVersion == 0 {
				t.Errorf("large result was not replaced by artifact reference: %s", all)
			}
			if strings.Contains(all, oldTarget) {
				t.Errorf("tail target unexpectedly leaked into preview")
			}
			if !strings.Contains(all, "[REDACTED]") {
				t.Errorf("sanitized preview did not contain redaction marker")
			}
			writeModelResponse(t, w, "Snapshot reference stored.", nil)
		case 3:
			if artifactRef == "" {
				t.Fatal("artifact reference was not captured")
			}
			if strings.Contains(all, oldTarget) {
				t.Errorf("second turn already had exact tail target without artifact read")
			}
			if !requestIncludesTool(body, "cycledock_read_artifact") {
				t.Errorf("artifact reader tool missing")
			}
			args, _ := json.Marshal(map[string]any{
				"file_name": artifactRef,
				"version":   artifactVersion,
				"offset":    12000,
				"max_chars": 20000,
			})
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "artifact-read-1", Type: "function",
				Function: modelclient.ToolFunction{
					Name: "cycledock_read_artifact", Arguments: string(args),
				},
			}})
		case 4:
			if !strings.Contains(all, oldTarget) {
				t.Errorf("artifact read did not recover exact tail value: %s", all)
			}
			if strings.Contains(all, secret) {
				t.Errorf("artifact read re-exposed credential")
			}
			writeModelResponse(t, w, "The exact old target was "+oldTarget+".", nil)
		default:
			t.Errorf("unexpected regular model call %d", current)
			writeModelResponse(t, w, "unexpected", nil)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Artifact continuity")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Runtime", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(
		ctx, workspace.ID, project.ID, human.ID, "Large snapshot", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(
		ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "artifact-model"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveMCPConnection(ctx, workspace.ID, store.MCPConnectionInput{
		Name: "lab", URL: mcpHTTP.URL, Enabled: true, TimeoutMS: 5000,
	}); err != nil {
		t.Fatal(err)
	}

	sessionService := agentsession.NewPostgresService(pool)
	artifactService := agentartifact.NewPostgresService(pool)
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService: sessionService, ArtifactService: artifactService,
		HeartbeatInterval: 50 * time.Millisecond, LeaseDuration: time.Minute,
	})

	runTurn := func(content, key string) {
		t.Helper()
		created, err := dataStore.CreateHumanTurn(
			ctx, workspace.ID, card.ID, human.ID, content, key, domain.DispatchModeEnqueue,
		)
		if err != nil {
			t.Fatalf("create human turn %q: %v", key, err)
		}
		claim, err := dataStore.ClaimNextTurnAttempt(
			ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute,
		)
		if err != nil {
			t.Fatalf("claim turn %q: %v", key, err)
		}
		if claim.Turn.ID != created.Turn.ID {
			t.Fatalf("claimed turn=%s want=%s", claim.Turn.ID, created.Turn.ID)
		}
		if err := runner.RunAttempt(ctx, *claim); err != nil {
			t.Fatalf("run attempt for %q: %v", key, err)
		}
	}

	runTurn("Capture the large inventory snapshot.", "artifact-turn-1")
	if artifactRef == "" || artifactVersion == 0 {
		t.Fatalf("artifact reference missing after first turn: ref=%q version=%d", artifactRef, artifactVersion)
	}
	appName, userID, sessionID := agentsession.Identity(workspace.ID, card.ID)
	if _, err := artifactService.Load(ctx, &artifact.LoadRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
		FileName: artifactRef, Version: artifactVersion,
	}); err != nil {
		t.Fatalf("artifact missing immediately after first turn: %v", err)
	}
	runTurn("What exact target was in that old snapshot? Use stored evidence, not the live system.", "artifact-turn-2")

	toolMu.Lock()
	gotCalls := toolCalls
	toolMu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("MCP tool calls=%d, want exactly 1 across both turns", gotCalls)
	}

	loaded, err := artifactService.Load(ctx, &artifact.LoadRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
		FileName: artifactRef, Version: artifactVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Part == nil || loaded.Part.InlineData == nil {
		t.Fatalf("artifact payload=%+v", loaded.Part)
	}
	artifactText := string(loaded.Part.InlineData.Data)
	if !strings.Contains(artifactText, oldTarget) {
		t.Fatal("artifact did not preserve exact old target")
	}
	if strings.Contains(artifactText, secret) || !strings.Contains(artifactText, "[REDACTED]") {
		t.Fatalf("artifact secret handling failed: %s", artifactText)
	}
}

func extractArtifactReference(messages []modelclient.Message) (string, int64) {
	for _, message := range messages {
		if message.Role != "tool" || message.Content == "" {
			continue
		}
		var value map[string]any
		if json.Unmarshal([]byte(message.Content), &value) != nil {
			continue
		}
		ref, _ := value["artifact_ref"].(string)
		version, _ := value["artifact_version"].(float64)
		if ref != "" && version > 0 {
			return ref, int64(version)
		}
	}
	return "", 0
}

func TestADKTailCompactionKeepsPromptBoundedAndHistoryRecoversExactOldValue(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()
	const oldValue = "eu-west-7-exact-marker"

	var modelMu sync.Mutex
	regularCalls := 0
	summaryCalls := 0
	firstPromptBytes := 0
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		rawMessages, _ := json.Marshal(body.Messages)
		all := string(rawMessages)
		w.Header().Set("Content-Type", "application/json")

		if len(body.Tools) == 0 {
			modelMu.Lock()
			summaryCalls++
			modelMu.Unlock()
			// Deliberately omit the exact value. History retrieval, rather than
			// trusting lossy rolling summaries, must recover it later.
			writeModelResponse(t, w, "Earlier context contained a deployment target; exact value omitted.", nil)
			return
		}

		modelMu.Lock()
		regularCalls++
		current := regularCalls
		modelMu.Unlock()
		switch current {
		case 1:
			if !strings.Contains(all, oldValue) {
				t.Errorf("first turn missing its own exact value")
			}
			firstPromptBytes = len(all)
			writeModelResponse(t, w, "Recorded the deployment context.", nil)
		case 2:
			// EventRetentionSize=2 intentionally keeps the first Turn raw at this
			// point: only two older candidate events exist yet.
			if !strings.Contains(all, oldValue) {
				t.Errorf("retained tail unexpectedly lost first-turn evidence")
			}
			writeModelResponse(t, w, "Continued with the retained tail.", nil)
		case 3:
			if strings.Contains(all, oldValue) {
				t.Errorf("third turn unexpectedly received compacted-away exact value")
			}
			if !strings.Contains(all, "exact value omitted") {
				t.Errorf("compaction summary missing from third-turn prompt: %s", all)
			}
			if firstPromptBytes > 0 && len(all) >= firstPromptBytes {
				t.Errorf("compacted prompt bytes=%d, first prompt bytes=%d", len(all), firstPromptBytes)
			}
			if !requestIncludesTool(body, "cycledock_search_session_history") {
				t.Errorf("history search tool missing")
			}
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "history-search-1", Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "cycledock_search_session_history",
					Arguments: `{"query":"DEPLOYMENT_TARGET","limit":4}`,
				},
			}})
		case 4:
			if !strings.Contains(all, oldValue) {
				t.Errorf("history search did not recover exact old value: %s", all)
			}
			writeModelResponse(t, w, "The old deployment target was "+oldValue+".", nil)
		default:
			t.Errorf("unexpected regular model call %d", current)
			writeModelResponse(t, w, "unexpected", nil)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Compaction history")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Runtime", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(
		ctx, workspace.ID, project.ID, human.ID, "Long session", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(
		ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "compaction-model"),
	)
	if err != nil {
		t.Fatal(err)
	}

	sessionService := agentsession.NewPostgresService(pool)
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    sessionService,
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})
	runner.compactionTokenThreshold = 100
	runner.compactionEventRetention = 2

	runTurn := func(content, key string) {
		t.Helper()
		created, err := dataStore.CreateHumanTurn(
			ctx, workspace.ID, card.ID, human.ID, content, key, domain.DispatchModeEnqueue,
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
		if claim.Turn.ID != created.Turn.ID {
			t.Fatalf("claimed turn=%s want=%s", claim.Turn.ID, created.Turn.ID)
		}
		if err := runner.RunAttempt(ctx, *claim); err != nil {
			t.Fatal(err)
		}
	}

	runTurn(
		strings.Repeat("context filler ", 300)+" DEPLOYMENT_TARGET="+oldValue,
		"compaction-turn-1",
	)
	runTurn("Continue; no need to repeat the old deployment target.", "compaction-turn-2")
	runTurn("What exact DEPLOYMENT_TARGET did I give in the first turn? Recover historical evidence instead of guessing.", "compaction-turn-3")

	modelMu.Lock()
	gotSummaryCalls := summaryCalls
	modelMu.Unlock()
	if gotSummaryCalls == 0 {
		t.Fatal("tail-retention compaction never invoked the summarizer")
	}

	appName, userID, sessionID := agentsession.Identity(workspace.ID, card.ID)
	var compactionEvents int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_session_events
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3
		  AND event::text LIKE '%"compaction"%'
	`, appName, userID, sessionID).Scan(&compactionEvents); err != nil {
		t.Fatal(err)
	}
	if compactionEvents == 0 {
		t.Fatal("no persisted compaction event found")
	}
	var rawOldEvents int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_session_events
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3
		  AND strpos(event::text, $4) > 0
	`, appName, userID, sessionID, oldValue).Scan(&rawOldEvents); err != nil {
		t.Fatal(err)
	}
	if rawOldEvents == 0 {
		t.Fatal("compaction deleted the original raw event")
	}
}

func TestADKTailCompactionKeepsRecentToolCallResponsePair(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()
	const oldValue = "pairing-old-marker"

	var modelMu sync.Mutex
	regularCalls := 0
	summaryCalls := 0
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if len(body.Tools) == 0 {
			modelMu.Lock()
			summaryCalls++
			modelMu.Unlock()
			writeModelResponse(t, w, "Earlier deployment context was summarized.", nil)
			return
		}

		modelMu.Lock()
		regularCalls++
		current := regularCalls
		modelMu.Unlock()
		switch current {
		case 1:
			writeModelResponse(t, w, "Recorded.", nil)
		case 2:
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID: "pair-call-1", Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"text","content":"PAIR_SUMMARY","end_turn":false,"idempotency_key":"pair-summary"}`,
				},
			}})
		case 3:
			rawMessages, _ := json.Marshal(body.Messages)
			all := string(rawMessages)
			if strings.Contains(all, oldValue) {
				t.Errorf("old raw event survived compaction: %s", all)
			}
			if !strings.Contains(all, "Earlier deployment context was summarized.") {
				t.Errorf("compaction summary missing: %s", all)
			}
			callIndex, responseIndex := -1, -1
			for index, message := range body.Messages {
				for _, call := range message.ToolCalls {
					if call.ID == "pair-call-1" && call.Function.Name == "reply_message" {
						callIndex = index
					}
				}
				if message.Role == "tool" && message.ToolCallID == "pair-call-1" {
					responseIndex = index
				}
			}
			if callIndex < 0 || responseIndex < 0 || responseIndex <= callIndex {
				t.Errorf("tool pair was not preserved in order: call=%d response=%d messages=%s", callIndex, responseIndex, all)
			}
			writeModelResponse(t, w, "Progress recorded after compacting older context.", nil)
		default:
			t.Errorf("unexpected regular model call %d", current)
			writeModelResponse(t, w, "unexpected", nil)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Compaction pairing")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Runtime", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(
		ctx, workspace.ID, project.ID, human.ID, "Pairing", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(
		ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "pairing-model"),
	)
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})
	runner.compactionTokenThreshold = 100
	runner.compactionEventRetention = 2

	runTurn := func(content, key string) {
		t.Helper()
		created, err := dataStore.CreateHumanTurn(
			ctx, workspace.ID, card.ID, human.ID, content, key, domain.DispatchModeEnqueue,
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
		if claim.Turn.ID != created.Turn.ID {
			t.Fatalf("claimed turn=%s want=%s", claim.Turn.ID, created.Turn.ID)
		}
		if err := runner.RunAttempt(ctx, *claim); err != nil {
			t.Fatal(err)
		}
	}

	runTurn(strings.Repeat("old context ", 300)+" "+oldValue, "pairing-turn-1")
	runTurn("Record progress with the CycleDock tool.", "pairing-turn-2")

	modelMu.Lock()
	gotSummaryCalls := summaryCalls
	modelMu.Unlock()
	if gotSummaryCalls == 0 {
		t.Fatal("tool follow-up did not trigger compaction")
	}
}

func TestADKRunnerWaitForHumanPersistsToolResponseAndContinuesSameSession(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	var modelMu sync.Mutex
	modelCalls := 0
	secondTurnSawWaitResponse := false
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		modelMu.Lock()
		modelCalls++
		current := modelCalls
		modelMu.Unlock()
		rawMessages, _ := json.Marshal(body.Messages)
		w.Header().Set("Content-Type", "application/json")
		switch current {
		case 1:
			if !requestIncludesTool(body, "reply_message") {
				t.Errorf("reply_message tool missing from first turn")
			}
			writeModelResponse(t, w, "", []modelclient.ToolCall{{
				ID:   "wait-call-1",
				Type: "function",
				Function: modelclient.ToolFunction{
					Name:      "reply_message",
					Arguments: `{"type":"choice","widget":{"question":"Which target should I use?","options":[{"value":"A","label":"Target A"},{"value":"B","label":"Target B"}]},"end_turn":true,"idempotency_key":"wait-target"}`,
				},
			}})
		case 2:
			if !strings.Contains(string(rawMessages), "Which target should I use?") ||
				!strings.Contains(string(rawMessages), "Use target B") {
				t.Errorf("second turn missing wait FunctionResponse or human reply: %s", rawMessages)
			} else {
				secondTurnSawWaitResponse = true
			}
			writeModelResponse(t, w, "Continuing with target B.", nil)
		default:
			t.Errorf("unexpected extra model request %d", current)
			writeModelResponse(t, w, "unexpected", nil)
		}
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "ADK Wait Continuity")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Runtime", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(
		ctx, workspace.ID, project.ID, human.ID,
		"Choose a target", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(
		ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "wait-model"),
	)
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})

	firstTurn, err := dataStore.CreateHumanTurn(
		ctx, workspace.ID, card.ID, human.ID,
		"Prepare the change, but ask me which target to use.",
		"wait-turn-1", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstClaim, err := dataStore.ClaimNextTurnAttempt(
		ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstClaim.Turn.ID != firstTurn.Turn.ID {
		t.Fatalf("claimed turn=%s want=%s", firstClaim.Turn.ID, firstTurn.Turn.ID)
	}
	if err := runner.RunAttempt(ctx, *firstClaim); err != nil {
		t.Fatal(err)
	}
	firstAttempt, err := dataStore.GetAttempt(ctx, workspace.ID, firstClaim.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstAttempt.Status != domain.AttemptStatusWaitingHuman {
		t.Fatalf("first attempt status=%s want waiting_human", firstAttempt.Status)
	}
	timeline, err := dataStore.ListCardTimeline(ctx, workspace.ID, card.ID)
	if err != nil {
		t.Fatal(err)
	}
	var prompts int
	for _, event := range timeline {
		if event.EventType == "message" && event.AttemptID.Valid && event.AttemptID.UUID == firstClaim.Attempt.ID {
			var message store.ReplyMessage
			if err := json.Unmarshal(event.Message, &message); err != nil {
				t.Fatal(err)
			}
			if message.Widget != nil && message.Widget.Question == "Which target should I use?" {
				prompts++
			}
		}
	}
	if prompts != 1 {
		t.Fatalf("persisted questions=%d want 1", prompts)
	}
	modelMu.Lock()
	callsAfterWait := modelCalls
	modelMu.Unlock()
	if callsAfterWait != 1 {
		t.Fatalf("model calls after wait=%d want 1", callsAfterWait)
	}

	reply, err := dataStore.ReplySessionAndContinue(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "Use target B", IdempotencyKey: "wait-reply",
		ReplyToAttemptID: firstClaim.Attempt.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstAttempt, err = dataStore.GetAttempt(ctx, workspace.ID, firstClaim.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstAttempt.Status != domain.AttemptStatusSuperseded {
		t.Fatalf("waiting attempt after reply=%s want superseded", firstAttempt.Status)
	}

	secondClaim, err := dataStore.ClaimNextTurnAttempt(
		ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if secondClaim.Turn.ID != reply.TurnID {
		t.Fatalf("claimed reply turn=%s want=%s", secondClaim.Turn.ID, reply.TurnID)
	}
	if err := runner.RunAttempt(ctx, *secondClaim); err != nil {
		t.Fatal(err)
	}
	secondAttempt, err := dataStore.GetAttempt(ctx, workspace.ID, secondClaim.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondAttempt.Status != domain.AttemptStatusCompleted {
		t.Fatalf("second attempt status=%s want completed", secondAttempt.Status)
	}
	if !secondTurnSawWaitResponse {
		t.Fatal("second Turn did not receive persisted reply_message FunctionResponse")
	}
}
