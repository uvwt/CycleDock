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

func TestHumanWorkControlHTTPFlowAndWorkspaceIsolation(t *testing.T) {
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
		"name": "Work control", "description": "", "instructions": "",
	}, cookie, nil)
	var project projectResponse
	decodeRecorder(t, projectRecorder, &project)

	cardRecorder := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+project.ID+"/discussions", map[string]any{}, cookie, nil)
	if cardRecorder.Code != http.StatusCreated {
		t.Fatalf("create discussion: %d %s", cardRecorder.Code, cardRecorder.Body.String())
	}
	var card cardResponse
	decodeRecorder(t, cardRecorder, &card)

	comment := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID+"/comments", map[string]any{
		"content": "queued work", "dispatch_mode": "enqueue",
	}, cookie, map[string]string{"Idempotency-Key": "http-work-control-original"})
	if comment.Code != http.StatusCreated {
		t.Fatalf("comment: %d %s", comment.Code, comment.Body.String())
	}
	var original commentResultResponse
	decodeRecorder(t, comment, &original)
	if original.TriggerID == "" || original.RunID != "" {
		t.Fatalf("unexpected pending result: %+v", original)
	}

	// UI 必须能用稳定 ID 把原始 Comment、Trigger 与排队状态关联起来。
	detailRecorder := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	var detail cardDetailResponse
	decodeRecorder(t, detailRecorder, &detail)
	if len(detail.Timeline) != 1 || len(detail.Queue) != 1 {
		t.Fatalf("pending linkage timeline=%d queue=%d", len(detail.Timeline), len(detail.Queue))
	}
	if detail.Timeline[0].ID != original.EventID || detail.Timeline[0].TriggerID != original.TriggerID {
		t.Fatalf("comment linkage=%+v original=%+v", detail.Timeline[0], original)
	}
	if detail.Queue[0].TriggerID != original.TriggerID ||
		detail.Queue[0].CauseEventID != original.EventID ||
		detail.Queue[0].SourceEventID != original.EventID {
		t.Fatalf("pending trigger linkage=%+v original=%+v", detail.Queue[0], original)
	}

	cancelPending := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID+"/triggers/"+original.TriggerID+"/cancel",
		map[string]any{"reason": "changed priority"}, cookie, nil,
	)
	if cancelPending.Code != http.StatusNoContent {
		t.Fatalf("cancel pending: %d %s", cancelPending.Code, cancelPending.Body.String())
	}

	detailRecorder = doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	decodeRecorder(t, detailRecorder, &detail)
	if len(detail.Queue) != 0 || len(detail.WorkHistory) != 1 {
		t.Fatalf("after pending cancel queue=%d history=%d", len(detail.Queue), len(detail.WorkHistory))
	}
	if detail.WorkHistory[0].Status != string(domain.RunStatusCancelled) || !detail.WorkHistory[0].CanRetry {
		t.Fatalf("cancelled pending history=%+v", detail.WorkHistory[0])
	}
	if detail.WorkHistory[0].SourceEventID != original.EventID || detail.WorkHistory[0].CauseEventID != original.EventID {
		t.Fatalf("cancelled work linkage=%+v original=%+v", detail.WorkHistory[0], original)
	}

	retryHeaders := map[string]string{"Idempotency-Key": "http-retry-cancelled"}
	retry := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID+"/triggers/"+original.TriggerID+"/retry",
		map[string]any{}, cookie, retryHeaders,
	)
	if retry.Code != http.StatusCreated {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
	var retried retryResultResponse
	decodeRecorder(t, retry, &retried)
	retryReplay := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID+"/triggers/"+original.TriggerID+"/retry",
		map[string]any{}, cookie, retryHeaders,
	)
	var replay retryResultResponse
	decodeRecorder(t, retryReplay, &replay)
	if retryReplay.Code != http.StatusCreated || !replay.Reused || replay.TriggerID != retried.TriggerID {
		t.Fatalf("retry replay status=%d first=%+v replay=%+v", retryReplay.Code, retried, replay)
	}

	detailRecorder = doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	decodeRecorder(t, detailRecorder, &detail)
	if len(detail.Queue) != 1 || detail.Queue[0].TriggerID != retried.TriggerID ||
		detail.Queue[0].RetryOfTriggerID != original.TriggerID ||
		detail.Queue[0].SourceEventID != original.EventID || detail.Queue[0].CauseEventID != "" {
		t.Fatalf("retry queue linkage=%+v original=%+v retried=%+v", detail.Queue, original, retried)
	}

	agent, err := dataStore.CreateSource(context.Background(), workspaceID, domain.SourceKindCycleAgent, "HTTP Agent")
	if err != nil {
		t.Fatal(err)
	}
	run, err := dataStore.ClaimNextTrigger(
		context.Background(), workspaceID, uuid.MustParse(card.ID), agent.ID, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.StartRun(context.Background(), workspaceID, run.ID, agent.ID, time.Minute); err != nil {
		t.Fatal(err)
	}
	detailRecorder = doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	decodeRecorder(t, detailRecorder, &detail)
	if detail.CurrentRun == nil || detail.CurrentRun.TriggerID != retried.TriggerID ||
		detail.CurrentRun.RetryOfTriggerID != original.TriggerID ||
		detail.CurrentRun.SourceEventID != original.EventID {
		t.Fatalf("current retry linkage=%+v original=%+v retried=%+v", detail.CurrentRun, original, retried)
	}
	cancelRun := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID+"/runs/"+run.ID.String()+"/cancel",
		map[string]any{"reason": "human stop"}, cookie, nil,
	)
	if cancelRun.Code != http.StatusNoContent {
		t.Fatalf("cancel run: %d %s", cancelRun.Code, cancelRun.Body.String())
	}

	detailRecorder = doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	decodeRecorder(t, detailRecorder, &detail)
	if detail.CurrentRun != nil || len(detail.WorkHistory) < 2 {
		t.Fatalf("cancelled run still current or missing history: current=%+v history=%+v", detail.CurrentRun, detail.WorkHistory)
	}
	if detail.WorkHistory[0].TriggerID != retried.TriggerID ||
		detail.WorkHistory[0].Status != string(domain.RunStatusCancelled) ||
		!detail.WorkHistory[0].CanRetry {
		t.Fatalf("cancelled run history=%+v", detail.WorkHistory[0])
	}

	otherWorkspace, err := dataStore.CreateWorkspace(context.Background(), "Other work-control workspace")
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
	otherCard, err := dataStore.CreateTodoCard(context.Background(), otherWorkspace.ID, otherProject.ID, otherHuman.ID, "Private", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	otherWork, err := dataStore.SubmitComment(context.Background(), store.SubmitCommentParams{
		WorkspaceID: otherWorkspace.ID, CardID: otherCard.ID, AuthorSourceID: otherHuman.ID,
		RunnerSourceID: &otherAgent.ID,
		Content:        "private work", IdempotencyKey: "other-work",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil || otherWork.RunID == nil {
		t.Fatalf("other work=%+v err=%v", otherWork, err)
	}

	crossCancel := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+otherCard.ID.String()+"/runs/"+otherWork.RunID.String()+"/cancel",
		map[string]any{"reason": "must not work"}, cookie, nil,
	)
	if crossCancel.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace run cancel: %d %s", crossCancel.Code, crossCancel.Body.String())
	}
	crossRetry := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/cards/"+otherCard.ID.String()+"/triggers/"+otherWork.TriggerID.String()+"/retry",
		map[string]any{}, cookie, map[string]string{"Idempotency-Key": "cross-retry"},
	)
	if crossRetry.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace retry: %d %s", crossRetry.Code, crossRetry.Body.String())
	}

	otherCurrent, err := dataStore.CurrentRun(context.Background(), otherWorkspace.ID, otherCard.ID)
	if err != nil || otherCurrent == nil || otherCurrent.ID != *otherWork.RunID {
		t.Fatalf("cross-workspace request changed private work: run=%+v err=%v", otherCurrent, err)
	}
	_ = humanSourceID
}
