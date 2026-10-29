package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

const httpIntegrationPassword = "local-admin-password"

func TestHumanWebHTTPFlowAndTenantIsolation(t *testing.T) {
	handler, pool, dataStore := newHTTPIntegration(t)

	wrong := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": "wrong",
	}, nil, nil)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong login status: got %d body=%s", wrong.Code, wrong.Body.String())
	}

	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status: got %d body=%s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected session cookie flags: %+v", cookie)
	}
	var identity identityResponse
	decodeRecorder(t, login, &identity)
	workspaceID := uuid.MustParse(identity.Workspace.ID)
	humanSourceID := uuid.MustParse(identity.Source.ID)

	var digest []byte
	if err := pool.QueryRow(context.Background(), "SELECT token_digest FROM web_sessions").Scan(&digest); err != nil {
		t.Fatalf("load session digest: %v", err)
	}
	if len(digest) != 32 || bytes.Equal(digest, []byte(cookie.Value)) {
		t.Fatalf("session token was not stored as SHA-256 digest")
	}

	projectRecorder := doJSON(t, handler, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Phase 2", "description": "Human Web", "instructions": "Keep durable context.",
	}, cookie, nil)
	if projectRecorder.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", projectRecorder.Code, projectRecorder.Body.String())
	}
	var project projectResponse
	decodeRecorder(t, projectRecorder, &project)

	cardRecorder := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+project.ID+"/discussions", map[string]any{}, cookie, nil)
	if cardRecorder.Code != http.StatusCreated {
		t.Fatalf("create discussion: %d %s", cardRecorder.Code, cardRecorder.Body.String())
	}
	var card cardResponse
	decodeRecorder(t, cardRecorder, &card)
	if card.Kind != string(domain.CardKindDiscussion) || card.Title != "" ||
		card.DiscussionState != string(domain.DiscussionStateActive) {
		t.Fatalf("unexpected new discussion: %+v", card)
	}

	headers := map[string]string{"Idempotency-Key": "human-comment-1"}
	for i := 0; i < 2; i++ {
		comment := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID+"/comments", map[string]any{
			"content": "First direction", "dispatch_mode": "enqueue",
		}, cookie, headers)
		if comment.Code != http.StatusCreated {
			t.Fatalf("comment attempt %d: %d %s", i+1, comment.Code, comment.Body.String())
		}
	}

	detailRecorder := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID, nil, cookie, nil)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("get card: %d %s", detailRecorder.Code, detailRecorder.Body.String())
	}
	var detail cardDetailResponse
	decodeRecorder(t, detailRecorder, &detail)
	if len(detail.Timeline) != 1 || len(detail.PendingTurns) != 1 || detail.CurrentAttempt != nil {
		t.Fatalf("idempotent pending direction mismatch: timeline=%d pending=%d attempt=%+v", len(detail.Timeline), len(detail.PendingTurns), detail.CurrentAttempt)
	}
	if err := dataStore.CancelPendingTurn(
		context.Background(),
		workspaceID,
		uuid.MustParse(card.ID),
		uuid.MustParse(detail.PendingTurns[0].ID),
		humanSourceID,
		"test fixture cleanup",
	); err != nil {
		t.Fatalf("cancel earlier pending turn: %v", err)
	}

	todoRecorder := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+project.ID+"/todos", map[string]any{
		"title": "Ship Human Web", "description": "Complete the board.",
	}, cookie, nil)
	if todoRecorder.Code != http.StatusCreated {
		t.Fatalf("create todo: %d %s", todoRecorder.Code, todoRecorder.Body.String())
	}
	var todo cardResponse
	decodeRecorder(t, todoRecorder, &todo)
	if todo.Kind != string(domain.CardKindTodo) ||
		todo.TodoState != string(domain.TodoStateTodo) ||
		todo.Title != "Ship Human Web" ||
		todo.SourceDiscussionID != "" {
		t.Fatalf("unexpected manually created todo: %+v", todo)
	}
	for _, state := range []domain.TodoState{
		domain.TodoStateTodo,
		domain.TodoStateWorking,
		domain.TodoStateReview,
		domain.TodoStateDone,
	} {
		move := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+todo.ID+"/move", map[string]any{
			"state": string(state),
		}, cookie, nil)
		if move.Code != http.StatusOK {
			t.Fatalf("move to %s: %d %s", state, move.Code, move.Body.String())
		}
	}

	runCardRecorder := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+project.ID+"/discussions", map[string]any{}, cookie, nil)
	var runCard cardResponse
	decodeRecorder(t, runCardRecorder, &runCard)
	runCardID := uuid.MustParse(runCard.ID)
	agent, err := dataStore.CreateSource(context.Background(), workspaceID, domain.SourceKindCycleAgent, "Agent")
	if err != nil {
		t.Fatalf("create agent source: %v", err)
	}
	initialHTTP := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+runCard.ID+"/comments", map[string]any{
		"content": "Original direction", "dispatch_mode": "enqueue",
	}, cookie, map[string]string{"Idempotency-Key": "attempt-initial"})
	if initialHTTP.Code != http.StatusCreated {
		t.Fatalf("create initial turn: %d %s", initialHTTP.Code, initialHTTP.Body.String())
	}
	initialClaim, err := dataStore.ClaimNextTurnAttempt(
		context.Background(), workspaceID, agent.ID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatalf("claim initial attempt: %v", err)
	}

	interrupt := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+runCard.ID+"/comments", map[string]any{
		"content": "Turn now", "dispatch_mode": "interrupt",
	}, cookie, map[string]string{"Idempotency-Key": "attempt-interrupt"})
	if interrupt.Code != http.StatusCreated {
		t.Fatalf("interrupt: %d %s", interrupt.Code, interrupt.Body.String())
	}
	oldAttempt, err := dataStore.GetAttempt(context.Background(), workspaceID, initialClaim.Attempt.ID)
	if err != nil || oldAttempt.Status != domain.AttemptStatusSuperseded {
		t.Fatalf("interrupt did not supersede current attempt: attempt=%+v err=%v", oldAttempt, err)
	}
	current, err := dataStore.ClaimNextTurnAttempt(
		context.Background(), workspaceID, agent.ID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatalf("claim interrupted turn: %v", err)
	}
	if current.Attempt.ID == initialClaim.Attempt.ID || current.Attempt.RunnerSourceID != agent.ID {
		t.Fatalf("interrupt claim=%+v initial=%+v", current, initialClaim)
	}
	choiceID, _, err := dataStore.PublishReply(context.Background(), store.PublishReplyParams{
		WorkspaceID: workspaceID, CardID: runCardID, AttemptID: current.Attempt.ID,
		RunnerSourceID: agent.ID, IdempotencyKey: "http-choice",
		Message: store.ReplyMessage{Type: "choice", EndTurn: true, Widget: &store.ChoiceWidget{
			Question: "Which target?", Options: []store.ChoiceOption{{Value: "A", Label: "Target A"}, {Value: "B", Label: "Target B"}},
		}},
	})
	if err != nil {
		t.Fatalf("publish choice: %v", err)
	}
	for _, invalid := range []struct {
		body   map[string]any
		status int
	}{
		{map[string]any{"content": "B"}, http.StatusBadRequest},
		{map[string]any{"content": "B", "attempt_id": initialClaim.Attempt.ID, "message_id": choiceID}, http.StatusConflict},
		{map[string]any{"content": "invalid", "attempt_id": current.Attempt.ID, "message_id": choiceID}, http.StatusConflict},
		{map[string]any{"content": "B", "attempt_id": current.Attempt.ID, "message_id": uuid.New()}, http.StatusConflict},
	} {
		recorder := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+runCard.ID+"/reply", invalid.body,
			cookie, map[string]string{"Idempotency-Key": uuid.NewString()})
		if recorder.Code != invalid.status {
			t.Fatalf("invalid reply status=%d want=%d body=%s", recorder.Code, invalid.status, recorder.Body.String())
		}
	}
	reply := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+runCard.ID+"/reply", map[string]any{
		"content": "B", "attempt_id": current.Attempt.ID, "message_id": choiceID,
	}, cookie, map[string]string{"Idempotency-Key": "attempt-reply"})
	if reply.Code != http.StatusCreated {
		t.Fatalf("reply and continue: %d %s", reply.Code, reply.Body.String())
	}

	var replyResult commentResultResponse
	decodeRecorder(t, reply, &replyResult)
	if replyResult.EventID == "" || replyResult.TurnID == "" {
		t.Fatalf("reply response does not expose session IDs: %+v body=%s", replyResult, reply.Body.String())
	}
	waitingAttempt, err := dataStore.GetAttempt(context.Background(), workspaceID, current.Attempt.ID)
	if err != nil || waitingAttempt.Status != domain.AttemptStatusSuperseded {
		t.Fatalf("reply did not supersede waiting attempt: attempt=%+v err=%v", waitingAttempt, err)
	}
	continued, err := dataStore.ClaimNextTurnAttempt(
		context.Background(), workspaceID, agent.ID, time.Now().UTC(), time.Minute,
	)
	if err != nil || continued.Turn.ID.String() != replyResult.TurnID || continued.Attempt.RunnerSourceID != agent.ID {
		t.Fatalf("reply did not continue as next turn: claim=%+v err=%v", continued, err)
	}

	ongoing, err := dataStore.CreateOngoingCard(
		context.Background(), workspaceID, uuid.MustParse(project.ID), humanSourceID,
		"Daily health", "Check the service.", uuid.NullUUID{},
	)
	if err != nil {
		t.Fatal(err)
	}
	schedule := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+ongoing.ID.String()+"/schedules", map[string]any{
		"frequency": "daily", "hour": 9, "minute": 30, "timezone": "Asia/Shanghai",
	}, cookie, nil)
	if schedule.Code != http.StatusCreated {
		t.Fatalf("create schedule: %d %s", schedule.Code, schedule.Body.String())
	}
	var scheduleData scheduleResponse
	decodeRecorder(t, schedule, &scheduleData)
	if scheduleData.NextTriggerAt.IsZero() || scheduleData.Timezone != "Asia/Shanghai" {
		t.Fatalf("unexpected schedule: %+v", scheduleData)
	}
	if _, created, err := dataStore.ProcessDueScheduleTurn(
		context.Background(),
		workspaceID,
		uuid.MustParse(scheduleData.ID),
		scheduleData.NextTriggerAt.Add(time.Second),
	); err != nil || !created {
		t.Fatalf("create scheduled turn history: created=%v err=%v", created, err)
	}
	projectSchedules := doJSON(
		t, handler, http.MethodGet,
		"/api/v1/projects/"+project.ID+"/schedules",
		nil, cookie, nil,
	)
	if projectSchedules.Code != http.StatusOK {
		t.Fatalf("list project schedules: %d %s", projectSchedules.Code, projectSchedules.Body.String())
	}
	var projectScheduleItems []projectScheduleResponse
	decodeRecorder(t, projectSchedules, &projectScheduleItems)
	if len(projectScheduleItems) != 1 ||
		projectScheduleItems[0].CardID != ongoing.ID.String() ||
		projectScheduleItems[0].CardTitle != "Daily health" ||
		projectScheduleItems[0].LastTriggeredAt == nil {
		t.Fatalf("unexpected project schedule view: %+v", projectScheduleItems)
	}

	activity := doJSON(
		t, handler, http.MethodGet,
		"/api/v1/projects/"+project.ID+"/activity?limit=20",
		nil, cookie, nil,
	)
	if activity.Code != http.StatusOK {
		t.Fatalf("list project activity: %d %s", activity.Code, activity.Body.String())
	}
	var activityItems []projectActivityResponse
	decodeRecorder(t, activity, &activityItems)
	var sawScheduledQueue bool
	for _, item := range activityItems {
		if item.CardID == ongoing.ID.String() &&
			item.ActivityType == "turn" &&
			item.Status == "queued" {
			sawScheduledQueue = true
			break
		}
	}
	if !sawScheduledQueue {
		t.Fatalf("scheduled turn missing from project activity: %+v", activityItems)
	}
	badActivityLimit := doJSON(
		t, handler, http.MethodGet,
		"/api/v1/projects/"+project.ID+"/activity?limit=101",
		nil, cookie, nil,
	)
	if badActivityLimit.Code != http.StatusBadRequest {
		t.Fatalf("invalid activity limit accepted: %d %s", badActivityLimit.Code, badActivityLimit.Body.String())
	}
	deleteWithHistory := doJSON(
		t, handler, http.MethodDelete,
		"/api/v1/cards/"+ongoing.ID.String()+"/schedules/"+scheduleData.ID,
		nil, cookie, nil,
	)
	if deleteWithHistory.Code != http.StatusConflict {
		t.Fatalf("schedule with trigger history was deletable: %d %s", deleteWithHistory.Code, deleteWithHistory.Body.String())
	}

	otherWorkspace, err := dataStore.CreateWorkspace(context.Background(), "Other Workspace")
	if err != nil {
		t.Fatal(err)
	}
	otherHuman, err := dataStore.CreateSource(context.Background(), otherWorkspace.ID, domain.SourceKindHuman, "Other Human")
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := dataStore.CreateProject(context.Background(), otherWorkspace.ID, "Private", "", "")
	if err != nil {
		t.Fatal(err)
	}
	crossProjectCreate := doJSON(
		t, handler, http.MethodPost,
		"/api/v1/projects/"+otherProject.ID.String()+"/discussions",
		map[string]any{},
		cookie, nil,
	)
	if crossProjectCreate.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace project accepted discussion creation: %d %s", crossProjectCreate.Code, crossProjectCreate.Body.String())
	}
	otherCard, err := dataStore.CreateTodoCard(context.Background(), otherWorkspace.ID, otherProject.ID, otherHuman.ID, "Other private card", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	crossTenant := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+otherCard.ID.String(), nil, cookie, nil)
	if crossTenant.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace card read: got %d body=%s", crossTenant.Code, crossTenant.Body.String())
	}

	logout := doJSON(t, handler, http.MethodPost, "/api/v1/auth/logout", map[string]any{}, cookie, nil)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", logout.Code, logout.Body.String())
	}
	afterLogout := doJSON(t, handler, http.MethodGet, "/api/v1/me", nil, cookie, nil)
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("session after logout: got %d body=%s", afterLogout.Code, afterLogout.Body.String())
	}
}

func newHTTPIntegration(t *testing.T) (http.Handler, *pgxpool.Pool, *store.Store) {
	t.Helper()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	adminPool, err := pgxpool.New(context.Background(), baseURL)
	if err != nil {
		t.Fatalf("connect integration database: %v", err)
	}
	t.Cleanup(adminPool.Close)

	schema := "http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(context.Background(), "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create integration schema: %v", err)
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
		t.Fatalf("open migration database: %v", err)
	}
	t.Cleanup(func() { _ = migrationDB.Close() })
	_, filename, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(migrationDB, migrations); err != nil {
		t.Fatalf("migrate integration schema: %v", err)
	}

	pool, err := pgxpool.New(context.Background(), scopedURL)
	if err != nil {
		t.Fatalf("connect integration schema: %v", err)
	}
	t.Cleanup(pool.Close)
	authService, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	dataStore := store.New(pool)
	if err := dataStore.ConfigureMCPEncryption(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))); err != nil {
		t.Fatalf("configure MCP encryption: %v", err)
	}
	return NewRouter(pool, dataStore, authService, Config{}), pool, dataStore
}

func doJSON(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body any,
	cookie *http.Cookie,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeRecorder(t *testing.T, response *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func responseCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response did not contain cookie %s", name)
	return nil
}
