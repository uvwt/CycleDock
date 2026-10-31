package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func TestADKRouteModelFallsBackAfterHTTP402(t *testing.T) {
	var primaryCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		http.Error(w, "subscription quota exhausted", http.StatusPaymentRequired)
	}))
	defer primary.Close()

	var backupCalls atomic.Int32
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backupCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "backup route answered", nil)
	}))
	defer backup.Close()

	llm, err := newADKRouteModel(modelclient.New(), nil, uuid.New(), []db.WorkspaceModelRoute{
		{Enabled: true, ProviderBaseUrl: primary.URL, Model: "primary"},
		{Enabled: true, ProviderBaseUrl: backup.URL, Model: "backup"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var answer string
	for response, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("你好", genai.RoleUser)},
	}, true) {
		if err != nil {
			t.Fatal(err)
		}
		if response.Partial {
			continue
		}
		if response.Content != nil {
			for _, part := range response.Content.Parts {
				answer += part.Text
			}
		}
	}

	if primaryCalls.Load() != 1 || backupCalls.Load() != 1 {
		t.Fatalf("route calls: primary=%d backup=%d, want 1/1", primaryCalls.Load(), backupCalls.Load())
	}
	if answer != "backup route answered" {
		t.Fatalf("answer=%q, want backup route answer", answer)
	}
}
