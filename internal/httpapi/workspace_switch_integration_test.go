package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestHumanWorkspaceSwitchUsesMembershipScope(t *testing.T) {
	handler, pool, dataStore := newHTTPIntegration(t)

	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var original identityResponse
	decodeRecorder(t, login, &original)

	list := doJSON(t, handler, http.MethodGet, "/api/v1/workspaces", nil, cookie, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list workspaces: %d %s", list.Code, list.Body.String())
	}
	var initial []workspaceResponse
	decodeRecorder(t, list, &initial)
	if len(initial) != 1 || initial[0].ID != original.Workspace.ID {
		t.Fatalf("initial workspaces=%+v identity=%+v", initial, original)
	}

	secondWorkspace, err := dataStore.CreateWorkspace(context.Background(), "Second Workspace")
	if err != nil {
		t.Fatal(err)
	}
	secondSource, err := dataStore.CreateSource(
		context.Background(),
		secondWorkspace.ID,
		domain.SourceKindHuman,
		"Second Workspace Human",
	)
	if err != nil {
		t.Fatal(err)
	}
	humanAccountID := uuid.MustParse(original.HumanAccountID)
	if _, err := pool.Exec(
		context.Background(),
		"INSERT INTO workspace_memberships (workspace_id, human_account_id, source_id, role, is_personal) VALUES ($1, $2, $3, 'member', false)",
		secondWorkspace.ID,
		humanAccountID,
		secondSource.ID,
	); err != nil {
		t.Fatalf("insert second workspace membership: %v", err)
	}

	list = doJSON(t, handler, http.MethodGet, "/api/v1/workspaces", nil, cookie, nil)
	var memberships []workspaceResponse
	decodeRecorder(t, list, &memberships)
	if len(memberships) != 2 {
		t.Fatalf("workspaces after membership=%+v", memberships)
	}

	switchRecorder := doJSON(
		t,
		handler,
		http.MethodPost,
		"/api/v1/workspaces/"+secondWorkspace.ID.String()+"/switch",
		map[string]any{},
		cookie,
		nil,
	)
	if switchRecorder.Code != http.StatusOK {
		t.Fatalf("switch workspace: %d %s", switchRecorder.Code, switchRecorder.Body.String())
	}
	var switched identityResponse
	decodeRecorder(t, switchRecorder, &switched)
	if switched.Workspace.ID != secondWorkspace.ID.String() ||
		switched.Source.ID != secondSource.ID.String() {
		t.Fatalf("switched identity=%+v", switched)
	}

	me := doJSON(t, handler, http.MethodGet, "/api/v1/me", nil, cookie, nil)
	var current identityResponse
	decodeRecorder(t, me, &current)
	if current.Workspace.ID != secondWorkspace.ID.String() ||
		current.Source.ID != secondSource.ID.String() {
		t.Fatalf("session did not persist switched scope: %+v", current)
	}

	unrelatedWorkspace, err := dataStore.CreateWorkspace(context.Background(), "No Membership")
	if err != nil {
		t.Fatal(err)
	}
	denied := doJSON(
		t,
		handler,
		http.MethodPost,
		"/api/v1/workspaces/"+unrelatedWorkspace.ID.String()+"/switch",
		map[string]any{},
		cookie,
		nil,
	)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unrelated workspace switch: %d %s", denied.Code, denied.Body.String())
	}

	me = doJSON(t, handler, http.MethodGet, "/api/v1/me", nil, cookie, nil)
	decodeRecorder(t, me, &current)
	if current.Workspace.ID != secondWorkspace.ID.String() {
		t.Fatalf("denied switch changed session scope: %+v", current)
	}
}
