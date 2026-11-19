package httpapi

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/domain"
)

func TestAuthenticatedCardSSEDeliversDurableEvents(t *testing.T) {
	handler, _, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login %d: %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var me identityResponse
	decodeRecorder(t, login, &me)
	workspaceID := uuid.MustParse(me.Workspace.ID)
	sourceID := uuid.MustParse(me.Source.ID)

	project, err := dataStore.CreateProject(context.Background(), workspaceID, "SSE Test", "")
	if err != nil {
		t.Fatal(err)
	}
	discussion, err := dataStore.CreateDiscussion(context.Background(), workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/cards/"+discussion.ID.String()+"/stream", nil)
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
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("unexpected Content-Type %q", contentType)
	}
	reader := bufio.NewReader(response.Body)
	// The initial comment must arrive immediately even for an idle Card.
	firstLine, err := reader.ReadString('\n')
	if err != nil || firstLine != ": connected\n" {
		t.Fatalf("SSE handshake = %q (%v)", firstLine, err)
	}

	comment := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+discussion.ID.String()+"/comments", map[string]string{
		"content": "PostgreSQL SSE integration event", "dispatch_mode": "enqueue",
	}, cookie, map[string]string{"Idempotency-Key": uuid.NewString()})
	if comment.Code != http.StatusCreated {
		t.Fatalf("comment = %d %s", comment.Code, comment.Body.String())
	}
	var found bool
	for !found {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				t.Fatal("SSE stream closed before durable event")
			}
			t.Fatalf("reading stream: %v", err)
		}
		if strings.TrimSpace(line) == "event: event.created" {
			found = true
		}
	}
	// Every Event is still recoverable from the original REST endpoint.
	card := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+discussion.ID.String(), nil, cookie, nil)
	if card.Code != http.StatusOK || !strings.Contains(card.Body.String(), "PostgreSQL SSE integration event") {
		t.Fatalf("durable Timeline did not reconcile: %d %s", card.Code, card.Body.String())
	}

	other, err := dataStore.CreateWorkspace(context.Background(), "Other SSE Workspace")
	if err != nil {
		t.Fatal(err)
	}
	otherHuman, err := dataStore.CreateSource(context.Background(), other.ID, domain.SourceKindHuman, "Other Human")
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := dataStore.CreateProject(context.Background(), other.ID, "Private SSE", "")
	if err != nil {
		t.Fatal(err)
	}
	privateCard, err := dataStore.CreateDiscussion(context.Background(), other.ID, otherProject.ID, otherHuman.ID)
	if err != nil {
		t.Fatal(err)
	}
	denied := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+privateCard.ID.String()+"/stream", nil, cookie, nil)
	if denied.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace SSE unexpectedly accessible: %d %s", denied.Code, denied.Body.String())
	}
	unauthed := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+discussion.ID.String()+"/stream", nil, nil, nil)
	if unauthed.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated SSE = %d", unauthed.Code)
	}
}

func TestCardSSEConnectDeliversCompletedAttemptWithoutWaitingTicker(t *testing.T) {
	handler, _, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login %d: %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var me identityResponse
	decodeRecorder(t, login, &me)
	ctx := context.Background()
	workspaceID := uuid.MustParse(me.Workspace.ID)
	sourceID := uuid.MustParse(me.Source.ID)

	project, err := dataStore.CreateProject(ctx, workspaceID, "SSE Reconcile", "")
	if err != nil {
		t.Fatal(err)
	}
	discussion, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspaceID, discussion.ID, sourceID, "测试切回即时对账", uuid.NewString(), domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	runner, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Runner")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, runner.ID, time.Now(), time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim = %+v, err = %v", claim, err)
	}
	if err := dataStore.FinishAttempt(ctx, workspaceID, discussion.ID, claim.Attempt.ID, domain.AttemptStatusCompleted, "", time.Now()); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/cards/"+discussion.ID.String()+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	start := time.Now()
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading initial stream: %v", err)
		}
		if strings.TrimSpace(line) == "event: attempt.finished" {
			// 建连首帧应立即推送终态，不能白等 1.5s ticker。
			if elapsed := time.Since(start); elapsed >= 1200*time.Millisecond {
				t.Fatalf("attempt.finished took too long on connect: %v", elapsed)
			}
			break
		}
	}
}
