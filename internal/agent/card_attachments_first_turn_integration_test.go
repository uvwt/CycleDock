package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/attachments"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestCardAttachmentsInjectedOnFirstTurnWhenGlobalTurnSequenceExceedsOne(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	workspace, err := dataStore.CreateWorkspace(ctx, "Card Attachments E2E")
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

	// 先创建另一张卡片并生成 3 条 Turn，把全局序列 card_turns_sequence_seq 推高到 > 1。
	otherCard, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "其他卡片", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := dataStore.CreateHumanTurn(
			ctx, workspace.ID, otherCard.ID, human.ID,
			fmt.Sprintf("other card turn %d", i),
			fmt.Sprintf("other-card-turn-%d", i),
			domain.DispatchModeEnqueue,
		); err != nil {
			t.Fatalf("create other card turn %d: %v", i, err)
		}
	}

	attSvc, err := attachments.New(pool, attachments.Config{
		Dir:                t.TempDir(),
		TTL:                24 * time.Hour,
		MaxTotalBytes:      16 << 20,
		MaxTextUploadBytes: 2 << 20,
	})
	if err != nil {
		t.Fatalf("attachments.New: %v", err)
	}

	const smallTextToken = "SMALL_CARD_ATTACHMENT_MARKER_98765"
	const largeTextFirstLine = "LARGE_LOG_HEADER_MARKER_12345"
	var largeBuilder strings.Builder
	largeBuilder.WriteString(largeTextFirstLine + "\n")
	for i := 2; i <= 260; i++ {
		fmt.Fprintf(&largeBuilder, "2026-11-21T10:00:%02dZ INFO worker line=%d payload=%s\n", i%60, i, strings.Repeat("x", 48))
	}

	uploadTriple := func(prefix string) (attachments.Attachment, attachments.Attachment, attachments.Attachment) {
		t.Helper()
		img, err := attSvc.Upload(ctx, attachments.UploadParams{
			WorkspaceID:        workspace.ID,
			UploadedBySourceID: human.ID,
			RawData:            makeAgentTestPNG(t, 40, 40),
		})
		if err != nil {
			t.Fatalf("upload %s image: %v", prefix, err)
		}
		smallTxt, err := attSvc.Upload(ctx, attachments.UploadParams{
			WorkspaceID:        workspace.ID,
			UploadedBySourceID: human.ID,
			FileName:           prefix + "-notes.txt",
			RawData:            []byte("说明文档\n" + smallTextToken + "\n"),
		})
		if err != nil {
			t.Fatalf("upload %s small text: %v", prefix, err)
		}
		largeTxt, err := attSvc.Upload(ctx, attachments.UploadParams{
			WorkspaceID:        workspace.ID,
			UploadedBySourceID: human.ID,
			FileName:           prefix + "-app.log",
			RawData:            []byte(largeBuilder.String()),
		})
		if err != nil {
			t.Fatalf("upload %s large text: %v", prefix, err)
		}
		return img, smallTxt, largeTxt
	}

	var mu sync.Mutex
	var capturedBodies []string
	failNext := true
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read model request body: %v", err)
			http.Error(w, "read body failed", http.StatusBadRequest)
			return
		}
		mu.Lock()
		capturedBodies = append(capturedBodies, string(raw))
		shouldFail := failNext
		if failNext {
			// 返回不可重试的 400 错误，确保首次 Attempt 在 ADK 写入首轮 user event 后直接失败，
			// 用于验证后续显式 RetryAttempt（AttemptNumber=2）的行为。
			failNext = false
		}
		mu.Unlock()

		if shouldFail {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"simulated fatal model failure for retry test"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "已完整查看图片、小文本与大文本摘要。", nil)
	}))
	defer modelHTTP.Close()

	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "vision-model"))
	if err != nil {
		t.Fatal(err)
	}

	sessionService := agentsession.NewPostgresService(pool)
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    sessionService,
		Attachments:       attSvc,
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})

	assertSingleHydration := func(t *testing.T, rawBody string, largeAttID uuid.UUID, label string) {
		t.Helper()
		if got := strings.Count(rawBody, "data:image/"); got != 1 {
			t.Fatalf("%s: expected exactly 1 hydrated image (data:image/), got %d; body=%s", label, got, rawBody)
		}
		if got := strings.Count(rawBody, smallTextToken); got != 1 {
			t.Fatalf("%s: expected small text content exactly once, got %d; body=%s", label, got, rawBody)
		}
		if got := strings.Count(rawBody, largeTextFirstLine); got != 1 {
			t.Fatalf("%s: expected large text preview exactly once, got %d; body=%s", label, got, rawBody)
		}
		if !strings.Contains(rawBody, largeAttID.String()) || !strings.Contains(rawBody, "cycledock_read_attachment") {
			t.Fatalf("%s: expected large text attachment_id %s and cycledock_read_attachment hint, body=%s", label, largeAttID, rawBody)
		}
	}

	// 1. autoStart=true：首轮 Attempt #1 失败 + RetryAttempt Attempt #2 成功 + 第 2 轮不重复注入。
	img1, small1, large1 := uploadTriple("auto")
	autoCard, _, err := dataStore.CreateTodoCardWithAttachmentsAndOptions(
		ctx, workspace.ID, project.ID, human.ID,
		"核对线上故障截图与日志", "请结合附带截图、说明和日志分析原因",
		uuid.NullUUID{}, 1, []uuid.UUID{img1.ID, small1.ID, large1.ID}, true,
	)
	if err != nil {
		t.Fatalf("create autoStart card: %v", err)
	}

	claim1, err := dataStore.ClaimCardTurnAttempt(ctx, workspace.ID, autoCard.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim autoStart first turn: %v", err)
	}
	loaded1, err := dataStore.LoadAgentCardContext(ctx, workspace.ID, claim1.Attempt.ID, settings.SourceID)
	if err != nil {
		t.Fatalf("LoadAgentCardContext: %v", err)
	}
	if loaded1.TurnSequence <= 1 || loaded1.CardTurnNumber != 1 {
		t.Fatalf("expected global TurnSequence > 1 and CardTurnNumber == 1, got seq=%d cardTurn=%d", loaded1.TurnSequence, loaded1.CardTurnNumber)
	}

	toAttemptClaim := func(res store.CardAttemptClaimResult) store.AttemptClaim {
		return store.AttemptClaim{Attempt: res.Attempt, Turn: res.Turn}
	}

	// Attempt #1 执行并在模型返回 400 时失败（此时 ADK Session 已持久化首轮 user event）。
	if err := runner.RunAttempt(ctx, toAttemptClaim(claim1)); err == nil {
		t.Fatal("expected attempt #1 to fail on simulated 400 error")
	}
	if err := dataStore.FinishAttempt(ctx, workspace.ID, autoCard.ID, claim1.Attempt.ID, domain.AttemptStatusFailed, "simulated 400", time.Now().UTC()); err != nil {
		t.Fatalf("finish attempt #1 as failed: %v", err)
	}

	mu.Lock()
	if len(capturedBodies) != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 model request after attempt #1, got %d", len(capturedBodies))
	}
	bodyAttempt1 := capturedBodies[0]
	mu.Unlock()
	assertSingleHydration(t, bodyAttempt1, large1.ID, "autoStart attempt #1")

	// 对首轮发起失败重试（AttemptNumber == 2）：模型仍应收到完整附件且每种附件仅水合 1 份（不丢失、不重复）。
	if _, err := dataStore.RetryAttempt(ctx, workspace.ID, autoCard.ID, claim1.Attempt.ID, settings.SourceID, domain.AttemptReasonRetry); err != nil {
		t.Fatalf("RetryAttempt: %v", err)
	}
	claim1Retry, err := dataStore.ClaimCardTurnAttempt(ctx, workspace.ID, autoCard.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim retry attempt: %v", err)
	}
	if claim1Retry.Attempt.AttemptNumber != 2 {
		t.Fatalf("expected AttemptNumber == 2, got %d", claim1Retry.Attempt.AttemptNumber)
	}
	if err := runner.RunAttempt(ctx, toAttemptClaim(claim1Retry)); err != nil {
		t.Fatalf("RunAttempt(retry): %v", err)
	}

	mu.Lock()
	if len(capturedBodies) != 2 {
		mu.Unlock()
		t.Fatalf("expected 2 model requests after attempt #2, got %d", len(capturedBodies))
	}
	bodyAttempt2 := capturedBodies[1]
	mu.Unlock()
	assertSingleHydration(t, bodyAttempt2, large1.ID, "autoStart attempt #2 retry")

	// 第 2 轮跟进消息：CardTurnNumber == 2，新写入 ADK Session 的 user event 不再重复携带 CardAttachments。
	if _, err := dataStore.CreateHumanTurn(ctx, workspace.ID, autoCard.ID, human.ID, "第二轮追问：还有补充吗？", "auto-card-turn-2", domain.DispatchModeEnqueue); err != nil {
		t.Fatalf("create second turn: %v", err)
	}
	claim2, err := dataStore.ClaimCardTurnAttempt(ctx, workspace.ID, autoCard.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim second turn: %v", err)
	}
	if err := runner.RunAttempt(ctx, toAttemptClaim(claim2)); err != nil {
		t.Fatalf("RunAttempt(turn 2): %v", err)
	}

	appName, userID, sessionID := agentsession.Identity(workspace.ID, autoCard.ID)
	rows, err := pool.Query(ctx, `
		SELECT event
		FROM agent_session_events
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3
		  AND event->>'author'='user'
		ORDER BY sequence ASC
	`, appName, userID, sessionID)
	if err != nil {
		t.Fatalf("query user session events: %v", err)
	}
	defer rows.Close()
	var userEvents []string
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan session event: %v", err)
		}
		userEvents = append(userEvents, string(raw))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate session events: %v", err)
	}
	if len(userEvents) != 3 {
		t.Fatalf("expected 3 persisted user events (attempt#1 + attempt#2 + turn#2), got %d", len(userEvents))
	}
	turn2Event := userEvents[len(userEvents)-1]
	if strings.Contains(turn2Event, userAttachmentURI(img1.ID)) ||
		strings.Contains(turn2Event, userAttachmentURI(small1.ID)) ||
		strings.Contains(turn2Event, userAttachmentURI(large1.ID)) {
		t.Fatalf("turn 2 user event must not re-inject CardAttachments URIs, got %s", turn2Event)
	}

	// 2. autoStart=false + StartCardExecution：首轮模型同样完整看到卡片附件。
	img2, small2, large2 := uploadTriple("manual-start")
	manualCard, _, err := dataStore.CreateTodoCardWithAttachmentsAndOptions(
		ctx, workspace.ID, project.ID, human.ID,
		"静默建卡后一键启动", "等待点击开始执行",
		uuid.NullUUID{}, 1, []uuid.UUID{img2.ID, small2.ID, large2.ID}, false,
	)
	if err != nil {
		t.Fatalf("create manual start card: %v", err)
	}
	if _, err := dataStore.StartCardExecution(ctx, store.StartCardExecutionParams{
		WorkspaceID:    workspace.ID,
		CardID:         manualCard.ID,
		AuthorSourceID: human.ID,
		IdempotencyKey: "start-manual-card-e2e",
	}); err != nil {
		t.Fatalf("StartCardExecution: %v", err)
	}
	manualClaim, err := dataStore.ClaimCardTurnAttempt(ctx, workspace.ID, manualCard.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim manual card: %v", err)
	}
	if err := runner.RunAttempt(ctx, toAttemptClaim(manualClaim)); err != nil {
		t.Fatalf("RunAttempt(manual start): %v", err)
	}
	mu.Lock()
	bodyManual := capturedBodies[len(capturedBodies)-1]
	mu.Unlock()
	assertSingleHydration(t, bodyManual, large2.ID, "autoStart=false + StartCardExecution")

	// 3. autoStart=false + 直接发首条消息（SubmitSessionComment）：首轮模型同样完整看到卡片附件。
	img3, small3, large3 := uploadTriple("direct-msg")
	directCard, _, err := dataStore.CreateTodoCardWithAttachmentsAndOptions(
		ctx, workspace.ID, project.ID, human.ID,
		"静默建卡后直接留言", "等待用户第一条消息",
		uuid.NullUUID{}, 1, []uuid.UUID{img3.ID, small3.ID, large3.ID}, false,
	)
	if err != nil {
		t.Fatalf("create direct message card: %v", err)
	}
	if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID:    workspace.ID,
		CardID:         directCard.ID,
		AuthorSourceID: human.ID,
		Content:        "请结合卡片附件直接开始处理",
		DispatchMode:   domain.DispatchModeEnqueue,
		IdempotencyKey: "direct-first-message-e2e",
	}); err != nil {
		t.Fatalf("SubmitSessionComment: %v", err)
	}
	directClaim, err := dataStore.ClaimCardTurnAttempt(ctx, workspace.ID, directCard.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim direct message card: %v", err)
	}
	if err := runner.RunAttempt(ctx, toAttemptClaim(directClaim)); err != nil {
		t.Fatalf("RunAttempt(direct first message): %v", err)
	}
	mu.Lock()
	bodyDirect := capturedBodies[len(capturedBodies)-1]
	mu.Unlock()
	assertSingleHydration(t, bodyDirect, large3.ID, "autoStart=false + SubmitSessionComment")
}
