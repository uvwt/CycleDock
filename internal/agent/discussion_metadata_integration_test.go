package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestDiscussionMetadataGenerationRetriesWithoutAttempt(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()
	workspace, _ := dataStore.CreateWorkspace(ctx, "Metadata workspace")
	human, _ := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	project, _ := dataStore.CreateProject(ctx, workspace.ID, "Project", "", "")
	card, err := dataStore.CreateDiscussion(ctx, workspace.ID, project.ID, human.ID)
	if err != nil {
		t.Fatal(err)
	}
	replies := []string{
		"invalid JSON",
		`{"title":"Title","description":"Description","extra":"unexpected"}`,
		`{"title":"Title"}`,
		`{"title":"  ","description":"Description"}`,
		`{"title":"Title","description":"Description"} {}`,
		`{"title":"Release planning","description":"Plan the next release."}`,
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var request struct {
			Tools          []json.RawMessage `json:"tools"`
			ResponseFormat struct {
				Type       string `json:"type"`
				JSONSchema struct {
					Strict bool           `json:"strict"`
					Schema map[string]any `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Tools) != 0 {
			t.Error("metadata generation must not expose tools")
		}
		w.Header().Set("Content-Type", "application/json")
		wantSchema := map[string]any{
			"type":       "object",
			"properties": map[string]any{"title": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"}},
			"required":   []any{"title", "description"}, "additionalProperties": false,
		}
		if request.ResponseFormat.Type != "json_schema" || !request.ResponseFormat.JSONSchema.Strict || !reflect.DeepEqual(request.ResponseFormat.JSONSchema.Schema, wantSchema) {
			t.Errorf("metadata schema was not transmitted: %+v", request.ResponseFormat)
		}
		index := int(calls.Add(1)) - 1
		if index >= len(replies) {
			t.Error("unexpected extra generation request")
			index = len(replies) - 1
		}
		writeModelResponse(t, w, replies[index], nil)
	}))
	defer server.Close()
	if _, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(server.URL, "fake-model")); err != nil {
		t.Fatal(err)
	}
	hub := realtime.NewHub()
	subscriber, _ := hub.Subscribe(card.ID)
	defer subscriber.Close()
	dispatcher := NewDispatcher(dataStore, modelclient.New(), nil, DispatcherConfig{Live: hub})
	dispatcher.generateDiscussionMetadata(ctx)
	if calls.Load() != 0 {
		t.Fatal("empty discussion triggered metadata generation")
	}
	if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID,
		Content: "Plan the next release", IdempotencyKey: "first", DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	for range len(replies) - 1 {
		dispatcher.generateDiscussionMetadata(ctx)
		updated, err := dataStore.GetCard(ctx, workspace.ID, card.ID)
		if err != nil || updated.Title != "" {
			t.Fatalf("invalid structured output changed title: card=%+v err=%v", updated, err)
		}
	}
	// 不合规输出重试；成功后不再生成，整个过程无需 Claim 主 Turn。
	dispatcher.generateDiscussionMetadata(ctx)
	dispatcher.generateDiscussionMetadata(ctx)
	updated, err := dataStore.GetCard(ctx, workspace.ID, card.ID)
	if err != nil || updated.Title != "Release planning" || updated.Description != "Plan the next release." || calls.Load() != int32(len(replies)) {
		t.Fatalf("metadata retry failed: card=%+v calls=%d err=%v", updated, calls.Load(), err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM turn_attempts").Scan(&count); err != nil || count != 0 {
		t.Fatalf("metadata generation created Attempt: count=%d err=%v", count, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM agent_session_events").Scan(&count); err != nil || count != 0 {
		t.Fatalf("metadata generation polluted Session: count=%d err=%v", count, err)
	}
	select {
	case event := <-subscriber.Events:
		if event.Type != "card.updated" {
			t.Fatalf("unexpected live event: %+v", event)
		}
	default:
		t.Fatal("metadata update did not notify browser")
	}
}
