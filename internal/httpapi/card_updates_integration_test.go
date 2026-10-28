package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/domain"
)

func TestCardUpdatesScopeAndDurableRevisions(t *testing.T) {
	handler, pool, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	cookie := responseCookie(t, login, sessionCookieName)
	var me identityResponse
	decodeRecorder(t, login, &me)
	ctx := context.Background()
	workspaceID := uuid.MustParse(me.Workspace.ID)
	sourceID := uuid.MustParse(me.Source.ID)
	project, err := dataStore.CreateProject(ctx, workspaceID, "Notifications", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	api := &API{pool: pool}
	revision := func() string {
		t.Helper()
		updates, err := api.cardUpdates(ctx, workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if len(updates) != 1 || updates[0].ID != card.ID.String() {
			t.Fatalf("workspace snapshot = %+v", updates)
		}
		return updates[0].Revision
	}
	before := revision()
	if before != revision() {
		t.Fatal("unchanged card changed revision")
	}
	empty, err := api.cardUpdates(ctx, uuid.New())
	if err != nil || len(empty) != 0 {
		t.Fatalf("other workspace snapshot = %+v, %v", empty, err)
	}

	edited := doJSON(t, handler, http.MethodPatch, "/api/v1/cards/"+card.ID.String(), map[string]string{"title": "Updated title", "description": "Updated description"}, cookie, nil)
	if edited.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", edited.Code, edited.Body.String())
	}
	afterEdit := revision()
	if afterEdit == before {
		t.Fatal("card metadata did not change revision")
	}
	turn, err := dataStore.CreateHumanTurn(ctx, workspaceID, card.ID, sourceID, "New message", uuid.NewString(), domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatal(err)
	}
	afterMessage := revision()
	if afterMessage == afterEdit {
		t.Fatal("message did not change revision")
	}
	runner, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Runner")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, runner.ID, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim == nil || claim.Turn.ID != turn.Turn.ID {
		t.Fatal("turn not claimed")
	}
	afterStart := revision()
	if afterStart == afterMessage {
		t.Fatal("attempt start did not change revision")
	}
	if err := dataStore.HeartbeatAttempt(ctx, workspaceID, card.ID, claim.Attempt.ID, time.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE turn_attempts SET current_stage='Generating', updated_at=now() WHERE id=$1`, claim.Attempt.ID); err != nil {
		t.Fatal(err)
	}
	if revision() != afterStart {
		t.Fatal("heartbeat/stage should not trigger a notification")
	}
	if err := dataStore.FinishAttempt(ctx, workspaceID, card.ID, claim.Attempt.ID, domain.AttemptStatusFailed, "Test failure", time.Now()); err != nil {
		t.Fatal(err)
	}
	if revision() == afterStart {
		t.Fatal("attempt failure did not change revision")
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspaceID, card.ID, sourceID, "请给出结果", uuid.NewString(), domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	second, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, runner.ID, time.Now(), time.Minute)
	if err != nil || second == nil {
		t.Fatalf("claim next turn: %v", err)
	}
	answer := strings.Repeat("检查完成", 60)
	if err := dataStore.CompleteAttempt(ctx, workspaceID, card.ID, second.Attempt.ID, runner.ID, answer, time.Now()); err != nil {
		t.Fatal(err)
	}
	updates, err := api.cardUpdates(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	update := updates[0]
	if update.MessagePreview != string([]rune(answer)[:180])+"…" {
		t.Fatalf("model reply preview = %q", update.MessagePreview)
	}
	if update.EventSequence == "0" || update.AttemptStatus != "completed" {
		t.Fatalf("model reply identity/status missing: %+v", update)
	}
}

func TestWorkspaceCardStreamDeliversUpdates(t *testing.T) {
	handler, _, dataStore := newHTTPIntegration(t)
	denied := doJSON(t, handler, http.MethodGet, "/api/v1/cards/updates/stream", nil, nil, nil)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated stream = %d", denied.Code)
	}
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	cookie := responseCookie(t, login, sessionCookieName)
	var me identityResponse
	decodeRecorder(t, login, &me)
	ctx := context.Background()
	workspaceID := uuid.MustParse(me.Workspace.ID)
	sourceID := uuid.MustParse(me.Source.ID)
	project, err := dataStore.CreateProject(ctx, workspaceID, "Notifications SSE", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := dataStore.CreateWorkspace(ctx, "Private")
	if err != nil {
		t.Fatal(err)
	}
	otherSource, err := dataStore.CreateSource(ctx, other.ID, domain.SourceKindHuman, "Other")
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := dataStore.CreateProject(ctx, other.ID, "Private", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateDiscussion(ctx, other.ID, otherProject.ID, otherSource.ID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/cards/updates/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	snapshot := func() []cardUpdate {
		t.Helper()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "data: ") {
				var updates []cardUpdate
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &updates); err != nil {
					t.Fatal(err)
				}
				if len(updates) != 1 || updates[0].ID != card.ID.String() {
					t.Fatalf("SSE scope: %+v", updates)
				}
				return updates
			}
		}
	}
	initial := snapshot()
	comment := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID.String()+"/comments", map[string]string{
		"content": "SSE update", "dispatch_mode": "enqueue",
	}, cookie, map[string]string{"Idempotency-Key": uuid.NewString()})
	if comment.Code != http.StatusCreated {
		t.Fatalf("submit message: %d %s", comment.Code, comment.Body.String())
	}
	changed := snapshot()
	if changed[0].MessagePreview != "SSE update" || changed[0].EventSequence == initial[0].EventSequence {
		t.Fatalf("SSE missing new message content: %+v", changed[0])
	}
	if changed[0].Revision == initial[0].Revision {
		t.Fatal("SSE did not deliver updated revision")
	}
	// 新连接立即拿到当前快照，客户端可和断线前版本比较。
	reconnect, err := client.Do(req.Clone(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer reconnect.Body.Close()
	reconnectReader := bufio.NewReader(reconnect.Body)
	for {
		line, err := reconnectReader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, changed[0].Revision) {
				t.Fatalf("reconnect snapshot = %s", line)
			}
			break
		}
	}
}
