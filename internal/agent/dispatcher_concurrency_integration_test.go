package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/session"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
)

func TestDispatcherRunsMoreThanFourAcrossWorkspacesWithinConfiguredLimits(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()
	started := make(chan struct{}, 12)
	release := make(chan struct{})
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			writeModelResponse(t, w, "done", nil)
		case <-r.Context().Done():
		}
	}))
	defer modelHTTP.Close()
	// 即使断言失败也先释放模型请求，避免测试服务器关闭时等待未结束连接。
	defer close(release)
	for i := 0; i < 3; i++ {
		workspace, err := dataStore.CreateWorkspace(ctx, fmt.Sprintf("Tenant %d", i))
		if err != nil {
			t.Fatal(err)
		}
		human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
		if err != nil {
			t.Fatal(err)
		}
		project, err := dataStore.CreateProject(ctx, workspace.ID, "Project", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "model")); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 3; j++ {
			card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Work", "", uuid.NullUUID{}, 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, card.ID, human.ID,
				"Run", fmt.Sprintf("input-%d", j), domain.DispatchModeEnqueue); err != nil {
				t.Fatal(err)
			}
		}
	}
	dispatcher := NewDispatcher(dataStore, modelclient.New(), nil, DispatcherConfig{
		MaxConcurrent: 6, MaxConcurrentPerWorkspace: 2,
		PollInterval: 10 * time.Millisecond, MaintenanceEvery: time.Hour,
		SessionService: session.InMemoryService(),
	})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(runCtx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("dispatcher stopped: %v", err)
		}
	}()
	deadline := time.After(5 * time.Second)
	for i := 0; i < 6; i++ {
		select {
		case <-started:
		case <-deadline:
			t.Fatalf("only %d model calls started; want 6 concurrent calls", i)
		}
	}
	var running, tenants, largestTenant int
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(sum(n),0), count(*), COALESCE(max(n),0)
		FROM (SELECT count(*) n FROM turn_attempts WHERE status='running' GROUP BY workspace_id) t
	`).Scan(&running, &tenants, &largestTenant); err != nil {
		t.Fatal(err)
	}
	if running != 6 || tenants != 3 || largestTenant != 2 {
		t.Fatalf("running=%d tenants=%d largestTenant=%d", running, tenants, largestTenant)
	}
	select {
	case <-started:
		t.Fatal("dispatcher exceeded configured capacity")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDispatcherDefaultWorkspaceLimitAllowsTenConcurrentAttempts(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()
	started := make(chan struct{}, 16)
	release := make(chan struct{})
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			writeModelResponse(t, w, "done", nil)
		case <-r.Context().Done():
		}
	}))
	defer modelHTTP.Close()
	defer close(release)

	workspace, err := dataStore.CreateWorkspace(ctx, "Single Workspace")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Project", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "model")); err != nil {
		t.Fatal(err)
	}
	// 创建 11 张独立卡片并同时入队，验证默认配置下单工作区最多并发执行 10 个 Attempt，第 11 个保持等待。
	for i := 0; i < DefaultMaxConcurrentPerWorkspace+1; i++ {
		card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, fmt.Sprintf("Work %d", i), "", uuid.NullUUID{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, card.ID, human.ID,
			"Run", fmt.Sprintf("input-%d", i), domain.DispatchModeEnqueue); err != nil {
			t.Fatal(err)
		}
	}

	dispatcher := NewDispatcher(dataStore, modelclient.New(), nil, DispatcherConfig{
		PollInterval:     10 * time.Millisecond,
		MaintenanceEvery: time.Hour,
		SessionService:   session.InMemoryService(),
	})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(runCtx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("dispatcher stopped: %v", err)
		}
	}()

	deadline := time.After(5 * time.Second)
	for i := 0; i < 10; i++ {
		select {
		case <-started:
		case <-deadline:
			t.Fatalf("only %d model calls started; want 10 concurrent calls in single workspace", i)
		}
	}

	var running int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM turn_attempts WHERE workspace_id=$1 AND status='running'
	`, workspace.ID).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if running != 10 {
		t.Fatalf("running=%d, want 10", running)
	}

	select {
	case <-started:
		t.Fatal("dispatcher exceeded default single-workspace capacity of 10")
	case <-time.After(50 * time.Millisecond):
	}
}
