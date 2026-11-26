package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/sqldb"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func TestModelCallToResponseIncludesResponseModel(t *testing.T) {
	callID := uuid.New()
	routeID := uuid.New()
	now := time.Now().UTC()

	got := modelCallToResponse(db.AgentModelCall{
		ID:            callID,
		CallIndex:     1,
		Purpose:       "agent",
		ModelRouteID:  uuid.NullUUID{UUID: routeID, Valid: true},
		ModelID:       "auto-router",
		ResponseModel: "gemini-2.5-pro-preview-05-06",
		Provider:      "openai_compatible",
		StartedAt:     sqldb.Timestamptz{Time: now, Valid: true},
		FinishedAt:    sqldb.Timestamptz{Time: now, Valid: true},
		DurationMs:    320,
		Status:        "success",
		UsageSource:   "actual",
	})

	if got.ResponseModel != "gemini-2.5-pro-preview-05-06" {
		t.Fatalf("ResponseModel=%q, want gemini-2.5-pro-preview-05-06", got.ResponseModel)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["response_model"] != "gemini-2.5-pro-preview-05-06" {
		t.Fatalf("json response_model=%v, want gemini-2.5-pro-preview-05-06", payload["response_model"])
	}

	emptyGot := modelCallToResponse(db.AgentModelCall{
		ID:       uuid.New(),
		ModelID:  "gpt-5",
		Provider: "openai_compatible",
		Status:   "failed",
	})
	emptyRaw, err := json.Marshal(emptyGot)
	if err != nil {
		t.Fatal(err)
	}
	var emptyPayload map[string]any
	if err := json.Unmarshal(emptyRaw, &emptyPayload); err != nil {
		t.Fatal(err)
	}
	if _, exists := emptyPayload["response_model"]; exists {
		t.Fatalf("expected empty response_model to be omitted, got %v", emptyPayload["response_model"])
	}
}

func TestAttemptTraceResponseModelIntegration(t *testing.T) {
	handler, _, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	cookie := responseCookie(t, login, sessionCookieName)
	var me identityResponse
	decodeRecorder(t, login, &me)

	ctx := context.Background()
	workspaceID := uuid.MustParse(me.Workspace.ID)
	sourceID := uuid.MustParse(me.Source.ID)

	project, err := dataStore.CreateProject(ctx, workspaceID, "Trace Response Model Test", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspaceID, store.AgentSettingsInput{
		Routes: []store.ModelRouteInput{{
			ProviderType:    "openai_compatible",
			ProviderBaseURL: "https://api.openai.com/v1",
			Model:           "gemini-pro-alias",
			Enabled:         true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspaceID, card.ID, sourceID, "检查 trace 回答模型展示", "trace-turn-1", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now().UTC().Add(-2 * time.Second)
	if _, err := dataStore.RecordModelCall(ctx, store.RecordModelCallParams{
		WorkspaceID:   workspaceID,
		CardID:        card.ID,
		TurnID:        claim.Turn.ID,
		AttemptID:     claim.Attempt.ID,
		CallIndex:     1,
		Purpose:       "agent",
		ModelID:       "gemini-pro-alias",
		ResponseModel: "gemini-2.5-pro-preview-05-06",
		Provider:      "openai_compatible",
		StartedAt:     started,
		FinishedAt:    started.Add(500 * time.Millisecond),
		DurationMS:    500,
		Status:        "success",
		InputTokens:   1200,
		OutputTokens:  300,
		UsageSource:   "actual",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.RecordModelCall(ctx, store.RecordModelCallParams{
		WorkspaceID: workspaceID,
		CardID:      card.ID,
		TurnID:      claim.Turn.ID,
		AttemptID:   claim.Attempt.ID,
		CallIndex:   2,
		Purpose:     "agent",
		ModelID:     "fallback-model",
		Provider:    "openai_compatible",
		StartedAt:   started.Add(time.Second),
		FinishedAt:  started.Add(1500 * time.Millisecond),
		DurationMS:  500,
		Status:      "success",
		UsageSource: "estimated",
	}); err != nil {
		t.Fatal(err)
	}

	res := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID.String()+"/attempts/"+claim.Attempt.ID.String()+"/trace", nil, cookie, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("get trace status=%d body=%s", res.Code, res.Body.String())
	}
	var overview traceOverviewResponse
	decodeRecorder(t, res, &overview)
	if len(overview.ModelCalls) != 2 {
		t.Fatalf("model_calls count=%d, want 2", len(overview.ModelCalls))
	}
	if overview.ModelCalls[0].ModelID != "gemini-pro-alias" || overview.ModelCalls[0].ResponseModel != "gemini-2.5-pro-preview-05-06" {
		t.Fatalf("call[0]=%+v, want model_id=gemini-pro-alias response_model=gemini-2.5-pro-preview-05-06", overview.ModelCalls[0])
	}
	if overview.ModelCalls[1].ModelID != "fallback-model" || overview.ModelCalls[1].ResponseModel != "" {
		t.Fatalf("call[1]=%+v, want model_id=fallback-model empty response_model", overview.ModelCalls[1])
	}
}
