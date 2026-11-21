package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestADKRouteModelRecordsResponseModelInTraceIntegration(t *testing.T) {
	dataStore, _ := newAgentIntegrationStore(t)
	ctx := context.Background()

	workspace, err := dataStore.CreateWorkspace(ctx, "Trace Response Model Workspace")
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
	card, err := dataStore.CreateDiscussion(ctx, workspace.ID, project.ID, human.ID)
	if err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gemini-2.5-pro-preview-05-06","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":120,"completion_tokens":30}}`))
	}))
	defer upstream.Close()

	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(upstream.URL, "gemini-alias"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, card.ID, human.ID, "验证 response_model 落库", "turn-trace-1", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	routeID := uuid.New()
	llm, err := newADKRouteModel(modelclient.New(), nil, dataStore, workspace.ID, []store.ModelRoute{{
		ID:              routeID,
		Enabled:         true,
		ProviderType:    "openai_compatible",
		ProviderBaseUrl: upstream.URL,
		Model:           "gemini-alias",
	}})
	if err != nil {
		t.Fatal(err)
	}
	llm.withTraceContext(card.ID, claim.Turn.ID, claim.Attempt.ID, "agent")

	for _, genErr := range llm.GenerateContent(ctx, &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hello", genai.RoleUser)},
	}, false) {
		if genErr != nil {
			t.Fatal(genErr)
		}
	}

	calls, err := dataStore.ListAttemptModelCalls(ctx, workspace.ID, card.ID, claim.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("recorded model calls=%d, want 1", len(calls))
	}
	if calls[0].ModelID != "gemini-alias" {
		t.Fatalf("ModelID=%q, want gemini-alias", calls[0].ModelID)
	}
	if calls[0].ResponseModel != "gemini-2.5-pro-preview-05-06" {
		t.Fatalf("ResponseModel=%q, want gemini-2.5-pro-preview-05-06", calls[0].ResponseModel)
	}
}
