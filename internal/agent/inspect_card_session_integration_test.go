package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	adkagent "google.golang.org/adk/v2/agent"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestInspectCardSessionTool(t *testing.T) {
	// 验证 buildADKTools 包含 cycledock_inspect_card_session
	tools, err := buildADKTools(attemptToolRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	var inspectToolFound bool
	for _, tool := range tools {
		if tool.Name() == "cycledock_inspect_card_session" {
			inspectToolFound = true
			break
		}
	}
	if !inspectToolFound {
		t.Fatalf("expected cycledock_inspect_card_session in internal tools")
	}

	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	workspace, err := dataStore.CreateWorkspace(ctx, "Inspect Session Workspace")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Test Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Inspect Project", "", "")
	if err != nil {
		t.Fatal(err)
	}

	discussionCard, err := dataStore.CreateDiscussion(ctx, workspace.ID, project.ID, human.ID)
	if err != nil {
		t.Fatal(err)
	}

	todoCard, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Todo Card", "Testing inspect session", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Add timeline events to todoCard:
	// 1. Human message
	if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID:    workspace.ID,
		CardID:         todoCard.ID,
		AuthorSourceID: human.ID,
		Content:        "Please implement the feature.",
		IdempotencyKey: "msg-1",
		DispatchMode:   domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}

	// 2. Cycle reply message (choice widget)
	agentSource, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindCycleAgent, "Cycle Agent")
	if err != nil {
		t.Fatal(err)
	}
	attemptClaim, err := dataStore.ClaimCardTurnAttempt(ctx, workspace.ID, todoCard.ID, agentSource.ID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    workspace.ID,
		CardID:         todoCard.ID,
		AttemptID:      attemptClaim.Attempt.ID,
		RunnerSourceID: agentSource.ID,
		Message: store.ReplyMessage{
			Type:    "choice",
			Content: "Which mode do you prefer?",
			EndTurn: true,
			Widget: &store.ChoiceWidget{
				Question: "Select one:",
				Options: []store.ChoiceOption{
					{Value: "opt1", Label: "Option 1"},
					{Value: "opt2", Label: "Option 2"},
				},
			},
		},
		IdempotencyKey: "msg-2",
	})
	if err != nil {
		t.Fatal(err)
	}

	historySvc := agentsession.NewPostgresService(pool)
	runtime := attemptToolRuntime{
		store:          dataStore,
		historyService: historySvc,
		context: store.AgentCardContext{
			WorkspaceID: workspace.ID,
			CardID:      todoCard.ID,
		},
	}

	handler, err := runtime.internalHandler("cycledock_inspect_card_session")
	if err != nil {
		t.Fatal(err)
	}
	toolCtx := adkagent.NewStrictContextMock(ctx)

	t.Run("simple mode extracts visible bubbles correctly", func(t *testing.T) {
		resMap, err := handler(&toolCtx, map[string]any{
			"card_id": todoCard.ID.String(),
			"mode":    "simple",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resMap["card_id"] != todoCard.ID.String() {
			t.Errorf("expected card_id %s, got %v", todoCard.ID.String(), resMap["card_id"])
		}
		if resMap["mode"] != "simple" {
			t.Errorf("expected mode simple, got %v", resMap["mode"])
		}
		bubblesRaw, ok := resMap["bubbles"]
		if !ok {
			t.Fatalf("missing bubbles field: %+v", resMap)
		}
		// bubbles is a slice
		bubblesJSON, err := json.Marshal(bubblesRaw)
		if err != nil {
			t.Fatal(err)
		}
		var bubbles []struct {
			Author    string    `json:"author"`
			Content   string    `json:"content"`
			Timestamp time.Time `json:"timestamp"`
		}
		if err := json.Unmarshal(bubblesJSON, &bubbles); err != nil {
			t.Fatal(err)
		}
		if len(bubbles) != 2 {
			t.Fatalf("expected 2 bubbles, got %d: %+v", len(bubbles), bubbles)
		}
		if bubbles[0].Author != "Test Human" {
			t.Errorf("expected author 'Test Human', got %q", bubbles[0].Author)
		}
		if bubbles[0].Content != "Please implement the feature." {
			t.Errorf("expected content 'Please implement the feature.', got %q", bubbles[0].Content)
		}
		if bubbles[1].Author != "Cycle" {
			t.Errorf("expected author 'Cycle', got %q", bubbles[1].Author)
		}
		expectedChoiceContent := "Which mode do you prefer?\nSelect one:\n1. Option 1\n2. Option 2"
		if bubbles[1].Content != expectedChoiceContent {
			t.Errorf("expected choice content %q, got %q", expectedChoiceContent, bubbles[1].Content)
		}
	})

	t.Run("rejects discussion cards", func(t *testing.T) {
		_, err := handler(&toolCtx, map[string]any{
			"card_id": discussionCard.ID.String(),
			"mode":    "simple",
		})
		if err == nil {
			t.Fatal("expected error inspecting discussion card, got nil")
		}
	})

	t.Run("cross workspace card is not accessible", func(t *testing.T) {
		foreignWorkspace, err := dataStore.CreateWorkspace(ctx, "Foreign Workspace")
		if err != nil {
			t.Fatal(err)
		}
		foreignProject, err := dataStore.CreateProject(ctx, foreignWorkspace.ID, "Foreign Project", "", "")
		if err != nil {
			t.Fatal(err)
		}
		foreignHuman, err := dataStore.CreateSource(ctx, foreignWorkspace.ID, domain.SourceKindHuman, "Foreign Human")
		if err != nil {
			t.Fatal(err)
		}
		foreignCard, err := dataStore.CreateTodoCard(ctx, foreignWorkspace.ID, foreignProject.ID, foreignHuman.ID, "Foreign Card", "", uuid.NullUUID{}, 1)
		if err != nil {
			t.Fatal(err)
		}

		_, err = handler(&toolCtx, map[string]any{
			"card_id": foreignCard.ID.String(),
			"mode":    "simple",
		})
		if err == nil {
			t.Fatal("expected error inspecting foreign workspace card, got nil")
		}
	})

	t.Run("full mode calls history service", func(t *testing.T) {
		resMap, err := handler(&toolCtx, map[string]any{
			"card_id": todoCard.ID.String(),
			"mode":    "full",
		})
		if err != nil {
			t.Fatalf("unexpected error in full mode: %v", err)
		}
		if resMap["mode"] != "full" {
			t.Errorf("expected mode full, got %v", resMap["mode"])
		}
	})
}
