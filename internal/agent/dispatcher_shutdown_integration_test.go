package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentartifact"
	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestDispatcherGracefulShutdownResumesAcrossConsecutiveRestarts(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	var mu sync.Mutex
	callCount := 0
	startedCh := make(chan int, 4)
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		callCount++
		current := callCount
		mu.Unlock()
		startedCh <- current

		// 前两次模拟服务发版重启：请求挂起直到客户端因优雅停机取消。
		if current <= 2 {
			<-r.Context().Done()
			return
		}
		// 第 3 次（重启后新进程）顺利完成任务。
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "已在新进程顺利完成。", nil)
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Graceful Shutdown Resume")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Deploy", "")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	mainChat, _, err := dataStore.EnsureMainDiscussion(ctx, workspace.ID, project.ID, human.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "test-model")); err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "跨重启续跑", "", uuid.NullUUID{UUID: mainChat.ID, Valid: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := dataStore.CreateHumanTurn(
		ctx, workspace.ID, card.ID, human.ID,
		"执行长任务", "graceful-dispatcher-turn", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}

	newDispatcher := func() *Dispatcher {
		return NewDispatcher(dataStore, modelclient.New(), nil, DispatcherConfig{
			MaxConcurrent:             2,
			MaxConcurrentPerWorkspace: 2,
			PollInterval:              20 * time.Millisecond,
			MaintenanceEvery:          time.Hour,
			LeaseDuration:             5 * time.Minute,
			SessionService:            agentsession.NewPostgresService(pool),
			ArtifactService:           agentartifact.NewPostgresService(pool),
		})
	}

	// 连续模拟两次优雅停机（SIGTERM/SIGINT），每次都在模型请求进行中取消 Dispatcher。
	for restart := 1; restart <= 2; restart++ {
		runCtx, cancel := context.WithCancel(ctx)
		d := newDispatcher()
		done := make(chan error, 1)
		go func() {
			done <- d.Run(runCtx)
		}()

		select {
		case got := <-startedCh:
			if got != restart {
				cancel()
				t.Fatalf("restart #%d saw model call %d, want %d", restart, got, restart)
			}
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatalf("restart #%d timed out waiting for model call", restart)
		}

		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("restart #%d dispatcher run err=%v", restart, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("restart #%d timed out waiting for dispatcher graceful stop", restart)
		}

		// 验证：无需等待 5 分钟租约超时，数据库里已经立即存在下一条 queued recovery attempt。
		var status string
		var attemptNumber int
		var reason string
		if err := pool.QueryRow(ctx, `
			SELECT status, attempt_number, reason
			FROM turn_attempts
			WHERE workspace_id=$1 AND turn_id=$2
			ORDER BY attempt_number DESC
			LIMIT 1
		`, workspace.ID, turn.Turn.ID).Scan(&status, &attemptNumber, &reason); err != nil {
			t.Fatal(err)
		}
		if status != string(domain.AttemptStatusQueued) || attemptNumber != restart+1 || reason != string(domain.AttemptReasonRecovery) {
			t.Fatalf("after restart #%d latest attempt=(status=%s num=%d reason=%s), want queued/%d/recovery",
				restart, status, attemptNumber, reason, restart+1)
		}

		// 期间严禁向主聊天发送失败回调。
		var callbackCount int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM card_turns
			WHERE card_id=$1 AND idempotency_key LIKE 'supervisor-callback:%'
		`, mainChat.ID).Scan(&callbackCount); err != nil {
			t.Fatal(err)
		}
		if callbackCount != 0 {
			t.Fatalf("after restart #%d supervisor callbacks=%d, want 0", restart, callbackCount)
		}
	}

	// 第 3 次启动新进程（Dispatcher）：立即接续排队中的 recovery attempt 并顺利完成。
	runCtx, cancel := context.WithCancel(ctx)
	d := newDispatcher()
	done := make(chan error, 1)
	go func() {
		done <- d.Run(runCtx)
	}()

	select {
	case got := <-startedCh:
		if got != 3 {
			cancel()
			t.Fatalf("final run saw model call %d, want 3", got)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatalf("final run timed out waiting for model call")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		var status string
		var attemptNumber int
		if err := pool.QueryRow(ctx, `
			SELECT status, attempt_number
			FROM turn_attempts
			WHERE workspace_id=$1 AND turn_id=$2
			ORDER BY attempt_number DESC
			LIMIT 1
		`, workspace.ID, turn.Turn.ID).Scan(&status, &attemptNumber); err != nil {
			cancel()
			t.Fatal(err)
		}
		if status == string(domain.AttemptStatusCompleted) && attemptNumber == 3 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("timed out waiting for resumed attempt completion; latest=(%s, #%d)", status, attemptNumber)
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	<-done

	// 全程 0 次失败回调，且前两次 attempt 均为 superseded + interrupted by server shutdown。
	var callbackCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM card_turns
		WHERE card_id=$1 AND idempotency_key LIKE 'supervisor-callback:%'
	`, mainChat.ID).Scan(&callbackCount); err != nil {
		t.Fatal(err)
	}
	if callbackCount != 0 {
		t.Fatalf("supervisor callbacks=%d, want 0", callbackCount)
	}
	var shutdownCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM turn_attempts
		WHERE workspace_id=$1 AND turn_id=$2
		  AND status='superseded'
		  AND error_summary=$3
	`, workspace.ID, turn.Turn.ID, store.AttemptErrorServerShutdown).Scan(&shutdownCount); err != nil {
		t.Fatal(err)
	}
	if shutdownCount != 2 {
		t.Fatalf("graceful shutdown superseded attempts=%d, want 2", shutdownCount)
	}
}
