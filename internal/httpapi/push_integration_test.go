package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/webpush"
)

type recordedPush struct {
	endpoint string
	payload  []byte
}

type recordingSender struct {
	publicKey string
	sent      []recordedPush
	gone      map[string]bool
	fail      map[string]error
}

func (s *recordingSender) PublicKey() string { return s.publicKey }

func (s *recordingSender) Send(_ context.Context, endpoint, _, _, _ string, payload []byte) error {
	if s.gone[endpoint] {
		return webpush.ErrGone
	}
	if err := s.fail[endpoint]; err != nil {
		return err
	}
	s.sent = append(s.sent, recordedPush{endpoint: endpoint, payload: append([]byte(nil), payload...)})
	return nil
}

func TestPushWaitsUntilThePageCloses(t *testing.T) {
	_, pool, dataStore := newHTTPIntegration(t)
	authService, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewRouter(pool, dataStore, authService, Config{})
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var me identityResponse
	decodeRecorder(t, login, &me)
	ctx := context.Background()
	workspaceID := uuid.MustParse(me.Workspace.ID)
	userID := uuid.MustParse(me.HumanAccountID)
	sourceID := uuid.MustParse(me.Source.ID)
	project, err := dataStore.CreateProject(ctx, workspaceID, "Push", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "https://push.example.test/subscription"
	if err := dataStore.SavePushSubscription(ctx, workspaceID, userID, store.PushSubscription{
		Endpoint: endpoint, P256dh: "key", AuthSecret: "auth",
	}); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{publicKey: "test"}
	api := &API{pool: pool, store: dataStore, push: sender}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("baseline push = %d", len(sender.sent))
	}
	if _, err := dataStore.UpdateCardText(ctx, workspaceID, card.ID, "只是改标题", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetCardPinned(ctx, workspaceID, card.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.IdlePushSubscription(ctx, workspaceID, userID, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("pin or title was pushed: %+v", sender.sent)
	}
	runner, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Runner")
	if err != nil {
		t.Fatal(err)
	}

	// 1. 验证用户消息（comment）不会触发 push
	if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspaceID, CardID: card.ID, AuthorSourceID: sourceID,
		Content: "用户发送的消息", IdempotencyKey: "push-comment-user", DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("user comment was pushed: %+v", sender.sent)
	}

	// 2. 模型回复消息：中间进度气泡（end_turn=false）在页面 Idle 时也不应触发推送；
	// 最终答复（end_turn=true）在页面还开着时（Touch 活跃状态）不应推送，关掉后补推。
	if _, err := dataStore.CreateHumanTurn(ctx, workspaceID, card.ID, sourceID, "问个问题", uuid.NewString(), domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, runner.ID, time.Now(), time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim turn: %v", err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID: workspaceID, CardID: card.ID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: runner.ID, Message: store.ReplyMessage{Type: "text", Content: "正在排查中…", EndTurn: false},
		IdempotencyKey: "push-progress-ignored",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.IdlePushSubscription(ctx, workspaceID, userID, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("intermediate progress message was pushed: %+v", sender.sent)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID: workspaceID, CardID: card.ID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: runner.ID, Message: store.ReplyMessage{Type: "text", Content: "页面还开着", EndTurn: true},
		IdempotencyKey: "push-open", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.TouchPushSubscription(ctx, workspaceID, userID, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("open page was pushed: %d", len(sender.sent))
	}
	// 页面还开着时不能把新消息记成已读。关掉之后（Idle），不用再发一条，也应把没确认的消息推出去。
	if err := dataStore.IdlePushSubscription(ctx, workspaceID, userID, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || !strings.Contains(string(sender.sent[0].payload), "页面还开着") {
		t.Fatalf("unacked push = %+v", sender.sent)
	}

	if _, err := dataStore.TouchPushSubscription(ctx, workspaceID, userID, endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspaceID, card.ID, sourceID, "再问一个", uuid.NewString(), domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim2, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, runner.ID, time.Now(), time.Minute)
	if err != nil || claim2 == nil {
		t.Fatalf("claim turn 2: %v", err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID: workspaceID, CardID: card.ID, AttemptID: claim2.Attempt.ID,
		RunnerSourceID: runner.ID, Message: store.ReplyMessage{Type: "text", Content: "页面确认看过", EndTurn: true},
		IdempotencyKey: "push-acked", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	updates, err := api.cardUpdates(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var seenRevision string
	for _, update := range updates {
		if update.ID == card.ID.String() {
			seenRevision = update.Revision
		}
	}
	if seenRevision == "" {
		t.Fatal("missing card revision")
	}
	acked := doJSON(t, handler, http.MethodPost, "/api/v1/notifications/push/seen", map[string]any{
		"cards": []map[string]string{{"id": card.ID.String(), "revision": seenRevision}},
	}, cookie, nil)
	if acked.Code != http.StatusNoContent {
		t.Fatalf("ack: %d %s", acked.Code, acked.Body.String())
	}
	if err := dataStore.IdlePushSubscription(ctx, workspaceID, userID, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("acked revision was pushed again: %+v", sender.sent)
	}

	sender.gone = map[string]bool{endpoint: true}
	if _, err := dataStore.CreateHumanTurn(ctx, workspaceID, card.ID, sourceID, "又问一个", uuid.NewString(), domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim3, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, runner.ID, time.Now(), time.Minute)
	if err != nil || claim3 == nil {
		t.Fatalf("claim turn 3: %v", err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID: workspaceID, CardID: card.ID, AttemptID: claim3.Attempt.ID,
		RunnerSourceID: runner.ID, Message: store.ReplyMessage{Type: "text", Content: "订阅失效", EndTurn: true},
		IdempotencyKey: "push-gone", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	idle, err := dataStore.ListIdlePushSubscriptions(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(idle) != 0 {
		t.Fatalf("gone subscription remained: %+v", idle)
	}
}

func TestPushSubscriptionRequiresConfiguration(t *testing.T) {
	_, pool, dataStore := newHTTPIntegration(t)
	authService, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewRouter(pool, dataStore, authService, Config{})
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	cookie := responseCookie(t, login, sessionCookieName)
	unconfigured := doJSON(t, handler, http.MethodGet, "/api/v1/notifications/push", nil, cookie, nil)
	if unconfigured.Code != http.StatusOK || !strings.Contains(unconfigured.Body.String(), `"public_key":""`) {
		t.Fatalf("unconfigured key: %d %s", unconfigured.Code, unconfigured.Body.String())
	}
	rejected := doJSON(t, handler, http.MethodPut, "/api/v1/notifications/push/subscription", map[string]any{
		"endpoint": "https://push.example.test/one",
		"keys":     map[string]string{"p256dh": "key", "auth": "auth"},
	}, cookie, nil)
	if rejected.Code != http.StatusServiceUnavailable {
		t.Fatalf("save without keys: %d %s", rejected.Code, rejected.Body.String())
	}

	configured := NewRouter(pool, dataStore, authService, Config{Push: &recordingSender{publicKey: "public-test-key"}})
	key := doJSON(t, configured, http.MethodGet, "/api/v1/notifications/push", nil, cookie, nil)
	if key.Code != http.StatusOK || !strings.Contains(key.Body.String(), "public-test-key") {
		t.Fatalf("configured key: %d %s", key.Code, key.Body.String())
	}
	saved := doJSON(t, configured, http.MethodPut, "/api/v1/notifications/push/subscription", map[string]any{
		"endpoint": "https://push.example.test/one",
		"keys":     map[string]string{"p256dh": "key", "auth": "auth"},
	}, cookie, nil)
	if saved.Code != http.StatusNoContent {
		t.Fatalf("save subscription: %d %s", saved.Code, saved.Body.String())
	}
	insecure := doJSON(t, configured, http.MethodPut, "/api/v1/notifications/push/subscription", map[string]any{
		"endpoint": "http://push.example.test/one",
		"keys":     map[string]string{"p256dh": "key", "auth": "auth"},
	}, cookie, nil)
	if insecure.Code != http.StatusBadRequest {
		t.Fatalf("http endpoint: %d %s", insecure.Code, insecure.Body.String())
	}
}

func TestSendCardPushTruncatesLongPreviewAndIsolatesPartialFailures(t *testing.T) {
	goodEndpoint := "https://web.push.apple.com/Q123"
	badEndpoint := "https://fcm.googleapis.com/fcm/send/broken"
	sender := &recordingSender{
		publicKey: "test",
		fail:      map[string]error{badEndpoint: errors.New("push endpoint fcm.googleapis.com returned 500")},
	}
	api := &API{push: sender}
	longBody := strings.Repeat("长消息内容测试", 100)
	update := cardUpdate{
		ID:             uuid.NewString(),
		ProjectID:      uuid.NewString(),
		Kind:           "todo",
		Title:          strings.Repeat("标", 120),
		Revision:       "rev-1",
		EventSequence:  "2",
		MessagePreview: longBody,
	}
	subs := []store.PushSubscription{
		{Endpoint: badEndpoint, P256dh: "k1", AuthSecret: "a1"},
		{Endpoint: goodEndpoint, P256dh: "k2", AuthSecret: "a2"},
	}
	workspaceID := uuid.New()
	if err := api.sendCardPush(context.Background(), workspaceID, update, subs); err != nil {
		t.Fatalf("partial success should advance snapshot instead of retrying forever: %v", err)
	}
	if len(sender.sent) != 1 || sender.sent[0].endpoint != goodEndpoint {
		t.Fatalf("unexpected deliveries: %+v", sender.sent)
	}
	var decoded pushPayload
	if err := json.Unmarshal(sender.sent[0].payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(decoded.Title) != maxPushTitleRunes || !strings.HasSuffix(decoded.Title, "…") {
		t.Fatalf("title not truncated: %q", decoded.Title)
	}
	if utf8.RuneCountInString(decoded.Body) != maxPushBodyRunes || !strings.HasSuffix(decoded.Body, "…") {
		t.Fatalf("body not truncated: len=%d", utf8.RuneCountInString(decoded.Body))
	}
	expectedTag := "card:" + workspaceID.String() + ":" + update.ID
	if decoded.Tag != expectedTag {
		t.Fatalf("tag = %q, want %q", decoded.Tag, expectedTag)
	}

	// 全部有效订阅都投递失败时仍返回错误，以便下一轮重试。
	if err := api.sendCardPush(context.Background(), uuid.New(), update, subs[:1]); err == nil {
		t.Fatal("expected error when every subscription delivery failed")
	}
}
