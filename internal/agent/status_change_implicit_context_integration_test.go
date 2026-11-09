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

func TestADKRunner_卡片状态变更在下次发消息时隐式注入模型上下文(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	var mu sync.Mutex
	var userInputs []string

	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var latestUserMessage string
		for _, msg := range body.Messages {
			if msg.Role == "user" {
				latestUserMessage = msg.Content
			}
		}

		mu.Lock()
		userInputs = append(userInputs, latestUserMessage)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "Task finished.", []modelclient.ToolCall{{
			ID:   "reply-call-" + uuid.NewString(),
			Type: "function",
			Function: modelclient.ToolFunction{
				Name:      "reply_message",
				Arguments: `{"type":"text","content":"Done.","end_turn":true,"idempotency_key":"reply-` + uuid.NewString() + `"}`,
			},
		}})
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Status Change Test Workspace")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human Operator")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Status Change Project", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Test Todo Card", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "fake-model"))
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    agentsession.NewPostgresService(pool),
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})

	// === Turn 1 ===
	// 用户发起第 1 轮留言
	turn1, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID:    workspace.ID,
		CardID:         card.ID,
		AuthorSourceID: human.ID,
		Content:        "Turn 1: Please begin the task",
		IdempotencyKey: uuid.NewString(),
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = turn1

	claim1, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.RunAttempt(ctx, *claim1); err != nil {
		t.Fatalf("first RunAttempt failed: %v", err)
	}

	mu.Lock()
	firstInput := userInputs[len(userInputs)-1]
	mu.Unlock()

	// 验证第 1 轮模型输入：无状态变更历史提示
	if strings.Contains(firstInput, "自上一轮执行以来的卡片状态变更：") {
		t.Fatalf("first turn should not have status change prompt: %s", firstInput)
	}
	if !strings.Contains(firstInput, "Turn 1: Please begin the task") {
		t.Fatalf("first turn user content missing: %s", firstInput)
	}

	// === 状态变更操作 ===
	// 在第 1 轮结束后，模拟外部用户更改卡片状态：
	// 先移到 review，再移回 working
	if err := dataStore.MoveTodo(ctx, workspace.ID, card.ID, human.ID, domain.TodoStateReview); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MoveTodo(ctx, workspace.ID, card.ID, human.ID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}

	// === Turn 2 ===
	// 用户发起第 2 轮留言
	turn2, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID:    workspace.ID,
		CardID:         card.ID,
		AuthorSourceID: human.ID,
		Content:        "Turn 2: Continue after status changes",
		IdempotencyKey: uuid.NewString(),
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = turn2

	claim2, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.RunAttempt(ctx, *claim2); err != nil {
		t.Fatalf("second RunAttempt failed: %v", err)
	}

	mu.Lock()
	secondInput := userInputs[len(userInputs)-1]
	mu.Unlock()

	// 验证第 2 轮模型输入：
	// 1. 保留用户原消息开头
	if !strings.HasPrefix(secondInput, "Turn 2: Continue after status changes") {
		t.Fatalf("second turn message should start with user content: %s", secondInput)
	}
	// 2. 隐式包含自上一轮执行以来的状态流转记录
	if !strings.Contains(secondInput, "自上一轮执行以来的卡片状态变更：") {
		t.Fatalf("second turn should contain status change prompt: %s", secondInput)
	}
	if !strings.Contains(secondInput, "working -> review") || !strings.Contains(secondInput, "review -> working") {
		t.Fatalf("second turn missing specific status transitions: %s", secondInput)
	}
	if !strings.Contains(secondInput, "操作人：Human Operator") {
		t.Fatalf("second turn missing actor name: %s", secondInput)
	}

	// 验证前端可见气泡（events 表）：
	// 确保没有为该状态提示在 events 表中多塞入任何用户可见 comment 气泡
	events, err := dataStore.ListCardEvents(ctx, workspace.ID, card.ID)
	if err != nil {
		t.Fatal(err)
	}

	var commentCount int
	for _, e := range events {
		if e.EventType == "comment" {
			commentCount++
			// 确认所有 comment 气泡的内容严格是用户的原始输入，没有任何静默注入文本
			if strings.Contains(e.Content, "自上一轮执行以来的卡片状态变更") {
				t.Fatalf("found implicit status change text leaked into visible comment event: %s", e.Content)
			}
		}
	}
	// 总共应该正好是 2 条 user comment 气泡（Turn 1 和 Turn 2）
	if commentCount != 2 {
		t.Fatalf("expected exactly 2 visible comment events, got %d", commentCount)
	}

	// === Turn 3 ===
	// 用户未再做任何状态变更，发起第 3 轮留言
	turn3, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID:    workspace.ID,
		CardID:         card.ID,
		AuthorSourceID: human.ID,
		Content:        "Turn 3: No new status changes",
		IdempotencyKey: uuid.NewString(),
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = turn3

	claim3, err := dataStore.ClaimNextTurnAttempt(ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.RunAttempt(ctx, *claim3); err != nil {
		t.Fatalf("third RunAttempt failed: %v", err)
	}

	mu.Lock()
	thirdInput := userInputs[len(userInputs)-1]
	mu.Unlock()

	// 验证第 3 轮模型输入：旧状态流转已被消费，不应再次注入
	if strings.Contains(thirdInput, "自上一轮执行以来的卡片状态变更：") {
		t.Fatalf("third turn should not contain already consumed status change prompt: %s", thirdInput)
	}
	if thirdInput != "Turn 3: No new status changes" {
		t.Fatalf("third turn should match raw user content exactly: %q", thirdInput)
	}
}
