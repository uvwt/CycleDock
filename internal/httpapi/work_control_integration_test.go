package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestHumanTurnAttemptControlHTTPFlowAndWorkspaceIsolation(t *testing.T) {
	handler, _, dataStore := newHTTPIntegration(t)

	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var identity identityResponse
	decodeRecorder(t, login, &identity)
	workspaceID := uuid.MustParse(identity.Workspace.ID)
	humanSourceID := uuid.MustParse(identity.Source.ID)

	projectRecorder := doJSON(t, handler, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Session control", "description": "", "instructions": "",
	}, cookie, nil)
	var project projectResponse
	decodeRecorder(t, projectRecorder, &project)

	cardRecorder := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+project.ID+"/discussions", map[string]any{}, cookie, nil)
	if cardRecorder.Code != http.StatusCreated {
		t.Fatalf("create discussion: %d %s", cardRecorder.Code, cardRecorder.Body.String())
	}
	var card cardResponse
	decodeRecorder(t, cardRecorder, &card)

	settings, err := dataStore.SaveAgentSettings(
		context.Background(),
		workspaceID,
		store.AgentSettingsInput{
			Routes: []store.ModelRouteInput{{
				Enabled:         true,
				ProviderType:    "openai_compatible",
				ProviderBaseURL: "http://127.0.0.1:1/v1",
				Model:           "test-model",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	comment := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID+"/comments", map[string]any{
		"content": "queued work", "dispatch_mode": "enqueue",
	}, cookie, map[string]string{"Idempotency-Key": "http-turn-original"})
	if comment.Code != http.StatusCreated {
		t.Fatalf("comment: %d %s", comment.Code, comment.Body.String())
	}
	var original commentResultResponse
	decodeRecorder(t, comment, &original)
	if original.EventID == "" || original.TurnID == "" {
		t.Fatalf("unexpected turn result: %+v", original)
	}

	detailRecorder := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	var detail cardDetailResponse
	decodeRecorder(t, detailRecorder, &detail)
	if len(detail.Timeline) != 1 || len(detail.PendingTurns) != 1 || detail.CurrentAttempt != nil {
		t.Fatalf("pending linkage timeline=%d pending=%d current=%+v", len(detail.Timeline), len(detail.PendingTurns), detail.CurrentAttempt)
	}
	if detail.Timeline[0].ID != original.EventID {
		t.Fatalf("comment event=%+v original=%+v", detail.Timeline[0], original)
	}
	if detail.PendingTurns[0].ID != original.TurnID ||
		detail.PendingTurns[0].SourceEventID != original.EventID {
		t.Fatalf("pending turn linkage=%+v original=%+v", detail.PendingTurns[0], original)
	}

	cancelPending := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID+"/turns/"+original.TurnID+"/cancel",
		map[string]any{"reason": "changed priority"}, cookie, nil,
	)
	if cancelPending.Code != http.StatusNoContent {
		t.Fatalf("cancel pending turn: %d %s", cancelPending.Code, cancelPending.Body.String())
	}
	detailRecorder = doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	decodeRecorder(t, detailRecorder, &detail)
	if len(detail.PendingTurns) != 0 || detail.CurrentAttempt != nil {
		t.Fatalf("cancelled pending turn remained active: pending=%+v current=%+v", detail.PendingTurns, detail.CurrentAttempt)
	}

	second := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID+"/comments", map[string]any{
		"content": "run and then stop", "dispatch_mode": "enqueue",
	}, cookie, map[string]string{"Idempotency-Key": "http-attempt-control"})
	if second.Code != http.StatusCreated {
		t.Fatalf("second comment: %d %s", second.Code, second.Body.String())
	}
	var secondResult commentResultResponse
	decodeRecorder(t, second, &secondResult)

	claim, err := dataStore.ClaimNextTurnAttempt(
		context.Background(), workspaceID, settings.SourceID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Turn.ID.String() != secondResult.TurnID {
		t.Fatalf("claimed turn=%s want=%s", claim.Turn.ID, secondResult.TurnID)
	}

	detailRecorder = doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	decodeRecorder(t, detailRecorder, &detail)
	if detail.CurrentAttempt == nil ||
		detail.CurrentAttempt.ID != claim.Attempt.ID.String() ||
		detail.CurrentAttempt.TurnID != secondResult.TurnID ||
		detail.CurrentAttempt.SourceEventID != secondResult.EventID {
		t.Fatalf("current attempt linkage=%+v claim=%+v", detail.CurrentAttempt, claim)
	}
	progressID, _, err := dataStore.ReportAttemptProgress(
		context.Background(),
		store.AttemptProgressParams{
			WorkspaceID:    workspaceID,
			CardID:         uuid.MustParse(card.ID),
			AttemptID:      claim.Attempt.ID,
			RunnerSourceID: settings.SourceID,
			Stage:          "inspect",
			Summary:        "attempt-linked progress",
			EventContent:   "attempt-linked durable progress",
			IdempotencyKey: "http-attempt-linked-progress",
			LeaseDuration:  time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	detailRecorder = doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	decodeRecorder(t, detailRecorder, &detail)
	var sawLinkedProgress bool
	for _, event := range detail.Timeline {
		if event.ID == progressID.String() {
			if event.AttemptID != claim.Attempt.ID.String() {
				t.Fatalf("progress attempt_id=%q want=%q", event.AttemptID, claim.Attempt.ID)
			}
			sawLinkedProgress = true
		}
	}
	if !sawLinkedProgress {
		t.Fatalf("attempt-linked progress missing from timeline: %+v", detail.Timeline)
	}

	cancelAttempt := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID+"/attempts/"+claim.Attempt.ID.String()+"/cancel",
		map[string]any{"reason": "human stop"}, cookie, nil,
	)
	if cancelAttempt.Code != http.StatusNoContent {
		t.Fatalf("cancel attempt: %d %s", cancelAttempt.Code, cancelAttempt.Body.String())
	}

	detailRecorder = doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	decodeRecorder(t, detailRecorder, &detail)
	if detail.CurrentAttempt != nil || len(detail.AttemptHistory) == 0 {
		t.Fatalf("cancelled attempt view current=%+v history=%+v", detail.CurrentAttempt, detail.AttemptHistory)
	}
	cancelled := detail.AttemptHistory[0]
	if cancelled.ID != claim.Attempt.ID.String() ||
		cancelled.Status != string(domain.AttemptStatusCancelled) ||
		!cancelled.CanRetry ||
		cancelled.SourceEventID != secondResult.EventID {
		t.Fatalf("cancelled attempt history=%+v", cancelled)
	}

	retry := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID+"/attempts/"+claim.Attempt.ID.String()+"/retry",
		map[string]any{}, cookie, nil,
	)
	if retry.Code != http.StatusCreated {
		t.Fatalf("retry attempt: %d %s", retry.Code, retry.Body.String())
	}
	var retried retryResultResponse
	decodeRecorder(t, retry, &retried)
	if retried.AttemptID == "" {
		t.Fatalf("retry result=%+v", retried)
	}

	replay := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID+"/attempts/"+claim.Attempt.ID.String()+"/retry",
		map[string]any{}, cookie, nil,
	)
	var replayed retryResultResponse
	decodeRecorder(t, replay, &replayed)
	if replay.Code != http.StatusCreated || !replayed.Reused || replayed.AttemptID != retried.AttemptID {
		t.Fatalf("retry replay status=%d first=%+v replay=%+v", replay.Code, retried, replayed)
	}

	detailRecorder = doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	decodeRecorder(t, detailRecorder, &detail)
	if detail.CurrentAttempt == nil ||
		detail.CurrentAttempt.ID != retried.AttemptID ||
		detail.CurrentAttempt.RetryOfAttemptID != claim.Attempt.ID.String() ||
		detail.CurrentAttempt.AttemptNumber != 2 ||
		detail.CurrentAttempt.Reason != string(domain.AttemptReasonRetry) ||
		detail.CurrentAttempt.SourceEventID != secondResult.EventID {
		t.Fatalf("retry current attempt=%+v first=%+v", detail.CurrentAttempt, claim.Attempt)
	}

	otherWorkspace, err := dataStore.CreateWorkspace(context.Background(), "Other session workspace")
	if err != nil {
		t.Fatal(err)
	}
	otherHuman, err := dataStore.CreateSource(context.Background(), otherWorkspace.ID, domain.SourceKindHuman, "Other Human")
	if err != nil {
		t.Fatal(err)
	}
	otherAgent, err := dataStore.CreateSource(context.Background(), otherWorkspace.ID, domain.SourceKindCycleAgent, "Other Agent")
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := dataStore.CreateProject(context.Background(), otherWorkspace.ID, "Private", "", "")
	if err != nil {
		t.Fatal(err)
	}
	otherCard, err := dataStore.CreateTodoCard(
		context.Background(), otherWorkspace.ID, otherProject.ID, otherHuman.ID,
		"Private", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	otherTurn, err := dataStore.CreateHumanTurn(
		context.Background(), otherWorkspace.ID, otherCard.ID, otherHuman.ID,
		"private work", "other-turn", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	otherClaim, err := dataStore.ClaimNextTurnAttempt(
		context.Background(), otherWorkspace.ID, otherAgent.ID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if otherClaim.Turn.ID != otherTurn.Turn.ID {
		t.Fatalf("other claim=%+v turn=%+v", otherClaim, otherTurn)
	}

	crossCancel := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+otherCard.ID.String()+"/attempts/"+otherClaim.Attempt.ID.String()+"/cancel",
		map[string]any{"reason": "must not work"}, cookie, nil,
	)
	if crossCancel.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace attempt cancel: %d %s", crossCancel.Code, crossCancel.Body.String())
	}
	crossRetry := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+otherCard.ID.String()+"/attempts/"+otherClaim.Attempt.ID.String()+"/retry",
		map[string]any{}, cookie, nil,
	)
	if crossRetry.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace attempt retry: %d %s", crossRetry.Code, crossRetry.Body.String())
	}

	stillCurrent, err := dataStore.GetAttempt(context.Background(), otherWorkspace.ID, otherClaim.Attempt.ID)
	if err != nil || stillCurrent.Status != domain.AttemptStatusRunning {
		t.Fatalf("cross-workspace request changed private attempt: attempt=%+v err=%v", stillCurrent, err)
	}

	_ = humanSourceID
}
