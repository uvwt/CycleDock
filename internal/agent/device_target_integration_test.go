package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestADKDeviceTarget_选择切换和不指定均进入对应Turn上下文(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()
	var mu sync.Mutex
	var inputs []string
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var latest string
		for _, message := range body.Messages {
			if message.Role == "user" {
				raw, _ := json.Marshal(message.Content)
				latest = string(raw)
			}
		}
		mu.Lock()
		inputs = append(inputs, latest)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "Acknowledged.", nil)
	}))
	defer modelHTTP.Close()
	workspace, err := dataStore.CreateWorkspace(ctx, "Device targets")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Devices", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Inspect", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "fake-model"))
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{SessionService: agentsession.NewPostgresService(pool), HeartbeatInterval: 50 * time.Millisecond, LeaseDuration: time.Minute})
	for i, target := range []*store.DeviceTarget{{NodeID: "node-a", Name: "RN"}, {NodeID: "node-b", Name: "Mac"}, nil} {
		if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{WorkspaceID: workspace.ID, CardID: card.ID, AuthorSourceID: human.ID, Content: "Inspect the service", IdempotencyKey: uuid.NewString(), DispatchMode: domain.DispatchModeEnqueue, DeviceTarget: target}); err != nil {
			t.Fatal(err)
		}
		claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := runner.RunAttempt(ctx, *claim); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		latest := inputs[len(inputs)-1]
		mu.Unlock()
		if target != nil && (!strings.Contains(latest, target.NodeID) || !strings.Contains(latest, target.Name)) {
			t.Fatalf("第%d轮未带目标: %s", i, latest)
		}
		if target == nil && (!strings.Contains(latest, "未指定操作设备") || strings.Contains(latest, "node-b")) {
			t.Fatalf("取消指定后不得自动沿用上轮目标: %s", latest)
		}
	}
}
