package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/webpush"
)

type recordedPush struct {
	endpoint string
	payload  []byte
}

type recordingSender struct {
	mu        sync.Mutex
	publicKey string
	sent      []recordedPush
	gone      map[string]bool
	fail      map[string]error
}

func (s *recordingSender) PublicKey() string { return s.publicKey }

func (s *recordingSender) Send(_ context.Context, endpoint, _, _, _ string, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone[endpoint] {
		return webpush.ErrGone
	}
	if err := s.fail[endpoint]; err != nil {
		return err
	}
	s.sent = append(s.sent, recordedPush{endpoint: endpoint, payload: append([]byte(nil), payload...)})
	return nil
}

func (s *recordingSender) snapshot() []recordedPush {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedPush, len(s.sent))
	copy(out, s.sent)
	return out
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
	if _, err := dataStore.SavePushSubscription(ctx, workspaceID, userID, store.PushSubscription{
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

func TestBuildPushPayloadAndPartialDeliveryIsolation(t *testing.T) {
	_, pool, dataStore := newHTTPIntegration(t)
	authService, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	goodEndpoint := "https://web.push.apple.com/Q123"
	badEndpoint := "https://fcm.googleapis.com/fcm/send/broken"
	sender := &recordingSender{
		publicKey: "test",
		fail:      map[string]error{badEndpoint: errors.New("push endpoint fcm.googleapis.com returned 500")},
	}
	handler := NewRouter(pool, dataStore, authService, Config{Push: sender})
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	var me identityResponse
	decodeRecorder(t, login, &me)
	wsID := uuid.MustParse(me.Workspace.ID)
	userID := uuid.MustParse(me.HumanAccountID)
	humanID := uuid.MustParse(me.Source.ID)

	runner, err := dataStore.CreateSource(ctx, wsID, domain.SourceKindCycleAgent, "Runner")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, wsID, "Push Project", "")
	if err != nil {
		t.Fatal(err)
	}
	ongoing, err := dataStore.CreateOngoingCard(ctx, wsID, project.ID, humanID, strings.Repeat("标", 120), "", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := dataStore.SavePushSubscription(ctx, wsID, userID, store.PushSubscription{
		Endpoint: badEndpoint, P256dh: "k1", AuthSecret: "a1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SavePushSubscription(ctx, wsID, userID, store.PushSubscription{
		Endpoint: goodEndpoint, P256dh: "k2", AuthSecret: "a2",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.IdlePushSubscription(ctx, wsID, userID, badEndpoint); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.IdlePushSubscription(ctx, wsID, userID, goodEndpoint); err != nil {
		t.Fatal(err)
	}

	api := &API{pool: pool, store: dataStore, push: sender}
	// 建立基线
	if err := api.deliverWorkspacePushes(ctx, wsID); err != nil {
		t.Fatal(err)
	}

	longBody := strings.Repeat("长消息内容测试", 100)
	if _, err := dataStore.CreateHumanTurn(ctx, wsID, ongoing.ID, humanID, "触发", uuid.NewString(), domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, wsID, runner.ID, time.Now(), time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim turn: %v", err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID: wsID, CardID: ongoing.ID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: runner.ID, Message: store.ReplyMessage{Type: "text", Content: longBody, EndTurn: true},
		IdempotencyKey: "push-long-ongoing", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}

	// 坏端点失败会返回 error 供日志记录，但好端点已经并发投递成功且推进了自身快照。
	if err := api.deliverWorkspacePushes(ctx, wsID); err == nil {
		t.Fatal("expected error from failed endpoint")
	}
	got := sender.snapshot()
	if len(got) != 1 || got[0].endpoint != goodEndpoint {
		t.Fatalf("unexpected deliveries: %+v", got)
	}
	var decoded pushPayload
	if err := json.Unmarshal(got[0].payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(decoded.Title) != maxPushTitleRunes || !strings.HasSuffix(decoded.Title, "…") {
		t.Fatalf("title not truncated: %q", decoded.Title)
	}
	if utf8.RuneCountInString(decoded.Body) == 0 || utf8.RuneCountInString(decoded.Body) > maxPushBodyRunes || !strings.HasSuffix(decoded.Body, "…") {
		t.Fatalf("body not truncated within limit: len=%d body=%q", utf8.RuneCountInString(decoded.Body), decoded.Body)
	}
	// 同时直接验证 buildPushPayload 对超过 maxPushBodyRunes 的正文会按字符数截断到上限
	rawOversized, err := buildPushPayload(wsID, cardUpdate{
		ID:             ongoing.ID.String(),
		ProjectID:      project.ID.String(),
		Kind:           "ongoing",
		Title:          strings.Repeat("标", 120),
		MessagePreview: strings.Repeat("长消息内容测试", 100),
		Revision:       "oversized-rev",
	})
	if err != nil {
		t.Fatal(err)
	}
	var oversizedDecoded pushPayload
	if err := json.Unmarshal(rawOversized, &oversizedDecoded); err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(oversizedDecoded.Body) != maxPushBodyRunes || !strings.HasSuffix(oversizedDecoded.Body, "…") {
		t.Fatalf("oversized body not truncated to %d: len=%d", maxPushBodyRunes, utf8.RuneCountInString(oversizedDecoded.Body))
	}
	expectedTag := "card:" + wsID.String() + ":" + ongoing.ID.String()
	if decoded.Tag != expectedTag {
		t.Fatalf("tag = %q, want %q", decoded.Tag, expectedTag)
	}
	expectedURL := "/plans/" + project.ID.String() + "/" + ongoing.ID.String()
	if decoded.URL != expectedURL {
		t.Fatalf("ongoing push url = %q, want %q", decoded.URL, expectedURL)
	}

	// 修复坏端点后重试：只补推坏端点，不重复推好端点。
	sender.mu.Lock()
	delete(sender.fail, badEndpoint)
	sender.mu.Unlock()
	if err := api.deliverWorkspacePushes(ctx, wsID); err != nil {
		t.Fatalf("retry after recovery failed: %v", err)
	}
	got = sender.snapshot()
	if len(got) != 2 || got[1].endpoint != badEndpoint {
		t.Fatalf("expected retry only for failed endpoint, got %+v", got)
	}
}

func TestMultiDeviceAckIsolationAndToolApprovalBypassesMute(t *testing.T) {
	_, pool, dataStore := newHTTPIntegration(t)
	authService, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	live := realtime.NewHub()
	dataStore.SetWorkspaceChangedNotifier(live.NotifyWorkspace)

	sender := &recordingSender{publicKey: "test-vapid"}
	handler := NewRouter(pool, dataStore, authService, Config{Push: sender, Live: live})
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

	project, err := dataStore.CreateProject(ctx, workspaceID, "MultiDevice", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(ctx, workspaceID, project.ID, sourceID, "审批与多端测试", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Runner")
	if err != nil {
		t.Fatal(err)
	}

	desktopEndpoint := "https://push.example.test/desktop"
	phoneEndpoint := "https://push.example.test/phone"
	for _, ep := range []string{desktopEndpoint, phoneEndpoint} {
		rec := doJSON(t, handler, http.MethodPut, "/api/v1/notifications/push/subscription", map[string]any{
			"endpoint": ep,
			"keys":     map[string]string{"p256dh": "key", "auth": "auth"},
		}, cookie, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("save sub %s: %d %s", ep, rec.Code, rec.Body.String())
		}
	}
	// 手机锁屏进入 idle，桌面保持前台活跃
	if err := dataStore.IdlePushSubscription(ctx, workspaceID, userID, phoneEndpoint); err != nil {
		t.Fatal(err)
	}

	// 新回复落库
	if _, err := dataStore.CreateHumanTurn(ctx, workspaceID, card.ID, sourceID, "开始", uuid.NewString(), domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, runner.ID, time.Now(), time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID: workspaceID, CardID: card.ID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: runner.ID, Message: store.ReplyMessage{Type: "text", Content: "桌面看到了但手机没看", EndTurn: true},
		IdempotencyKey: "multi-dev-reply-1", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}

	api := &API{pool: pool, store: dataStore, push: sender, live: live}
	updates, err := api.cardUpdates(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var currentRev string
	for _, u := range updates {
		if u.ID == card.ID.String() {
			currentRev = u.Revision
		}
	}
	// 桌面端上报 /seen（携带桌面 endpoint）
	ackRec := doJSON(t, handler, http.MethodPost, "/api/v1/notifications/push/seen", map[string]any{
		"endpoint": desktopEndpoint,
		"cards":    []map[string]string{{"id": card.ID.String(), "revision": currentRev}},
	}, cookie, nil)
	if ackRec.Code != http.StatusNoContent {
		t.Fatalf("desktop ack failed: %d %s", ackRec.Code, ackRec.Body.String())
	}

	// 桌面 ack 不能吞掉锁屏手机的推送！
	if err := api.deliverWorkspacePushes(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	sent := sender.snapshot()
	if len(sent) != 1 || sent[0].endpoint != phoneEndpoint || !strings.Contains(string(sent[0].payload), "桌面看到了但手机没看") {
		t.Fatalf("expected phone push after desktop ack, got %+v", sent)
	}

	// 将卡片静音后触发工具待人工审批：审批通知必须穿透静音送达手机，且审批处理后不重复补推旧消息。
	if _, err := dataStore.SetCardNotificationsMuted(ctx, workspaceID, card.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(ctx, workspaceID, card.ID, sourceID, "执行高危操作", uuid.NewString(), domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim2, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, runner.ID, time.Now(), time.Minute)
	if err != nil || claim2 == nil {
		t.Fatalf("claim2: %v", err)
	}
	approval, err := dataStore.CreateToolApproval(ctx, store.CreateToolApprovalParams{
		WorkspaceID: workspaceID,
		CardID:      card.ID,
		TurnID:      &claim2.Turn.ID,
		AttemptID:   &claim2.Attempt.ID,
		ToolCallID:  "call-1",
		ToolName:    "exec_command",
		Arguments:   map[string]any{"cmd": "rm -rf /tmp/test"},
		RiskLevel:   "high",
		Reason:      "可能修改系统文件",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.PublishToolApprovalEvent(ctx, store.PublishToolApprovalEventParams{
		WorkspaceID:    workspaceID,
		CardID:         card.ID,
		AttemptID:      claim2.Attempt.ID,
		RunnerSourceID: runner.ID,
		ApprovalID:     approval.ID,
		Content:        "敏感操作待确认：exec_command",
	}); err != nil {
		t.Fatal(err)
	}

	if err := api.deliverWorkspacePushes(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	sent = sender.snapshot()
	if len(sent) != 2 || sent[1].endpoint != phoneEndpoint || !strings.Contains(string(sent[1].payload), "敏感操作待确认：exec_command") {
		t.Fatalf("expected tool approval push to bypass mute, got %+v", sent)
	}

	// 审批被处理后，m.sequence 会回落到上一条历史消息，绝不能把旧消息当成新消息再推一次。
	if _, err := dataStore.ResolveToolApproval(ctx, store.ResolveToolApprovalParams{
		WorkspaceID:        workspaceID,
		CardID:             card.ID,
		ApprovalID:         approval.ID,
		Status:             string(domain.ToolApprovalStatusApproved),
		ResolvedBySourceID: sourceID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverWorkspacePushes(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if len(sender.snapshot()) != 2 {
		t.Fatalf("resolving tool approval must not re-push older message, got %+v", sender.snapshot())
	}
}

func TestDeliverPushesWakesImmediatelyOnWorkspaceChange(t *testing.T) {
	_, pool, dataStore := newHTTPIntegration(t)
	authService, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	live := realtime.NewHub()
	dataStore.SetWorkspaceChangedNotifier(live.NotifyWorkspace)

	sender := &recordingSender{publicKey: "test-wake"}
	handler := NewRouter(pool, dataStore, authService, Config{Push: sender, Live: live})
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	var me identityResponse
	decodeRecorder(t, login, &me)
	wsID := uuid.MustParse(me.Workspace.ID)
	userID := uuid.MustParse(me.HumanAccountID)
	humanID := uuid.MustParse(me.Source.ID)

	runner, err := dataStore.CreateSource(ctx, wsID, domain.SourceKindCycleAgent, "Runner")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, wsID, "Wake Project", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, wsID, project.ID, humanID)
	if err != nil {
		t.Fatal(err)
	}

	endpoint := "https://push.example.test/wake-fast"
	if _, err := dataStore.SavePushSubscription(ctx, wsID, userID, store.PushSubscription{
		Endpoint: endpoint, P256dh: "k", AuthSecret: "a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.IdlePushSubscription(ctx, wsID, userID, endpoint); err != nil {
		t.Fatal(err)
	}

	// 建立初始基线
	initAPI := &API{pool: pool, store: dataStore, push: sender, live: live}
	if err := initAPI.deliverWorkspacePushes(ctx, wsID); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		DeliverPushes(ctx, pool, dataStore, sender, live, nil)
	}()
	// 等待 goroutine 完成订阅
	time.Sleep(50 * time.Millisecond)

	if _, err := dataStore.CreateHumanTurn(ctx, wsID, card.ID, humanID, "快推", uuid.NewString(), domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, wsID, runner.ID, time.Now(), time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	start := time.Now()
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID: wsID, CardID: card.ID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: runner.ID, Message: store.ReplyMessage{Type: "text", Content: "秒级即时推送", EndTurn: true},
		IdempotencyKey: "wake-reply-1", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sender.snapshot()) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	elapsed := time.Since(start)
	cancel()
	<-done

	sent := sender.snapshot()
	if len(sent) != 1 || !strings.Contains(string(sent[0].payload), "秒级即时推送") {
		t.Fatalf("expected immediate push delivery within 2s, got %+v (elapsed=%v)", sent, elapsed)
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("expected event-driven push much faster than fallback ticker, elapsed=%v", elapsed)
	}
}

type supervisorPushTestEnv struct {
	ctx         context.Context
	api         *API
	store       *store.Store
	sender      *recordingSender
	workspaceID uuid.UUID
	projectID   uuid.UUID
	humanID     uuid.UUID
	sourceID    uuid.UUID
	runnerID    uuid.UUID
}

func newSupervisorPushTestEnv(t *testing.T) *supervisorPushTestEnv {
	t.Helper()
	handler, pool, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	var me identityResponse
	decodeRecorder(t, login, &me)

	ctx := context.Background()
	workspaceID := uuid.MustParse(me.Workspace.ID)
	humanID := uuid.MustParse(me.HumanAccountID)
	sourceID := uuid.MustParse(me.Source.ID)

	project, err := dataStore.CreateProject(ctx, workspaceID, "Supervisor Push Dedupe", "")
	if err != nil {
		t.Fatal(err)
	}
	runner, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Cycle Agent")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "https://push.example.test/supervisor-dedupe"
	if err := dataStore.SavePushSubscription(ctx, workspaceID, humanID, store.PushSubscription{
		Endpoint: endpoint, P256dh: "key", AuthSecret: "auth",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.IdlePushSubscription(ctx, workspaceID, humanID, endpoint); err != nil {
		t.Fatal(err)
	}

	sender := &recordingSender{publicKey: "test"}
	api := &API{pool: pool, store: dataStore, push: sender}
	return &supervisorPushTestEnv{
		ctx:         ctx,
		api:         api,
		store:       dataStore,
		sender:      sender,
		workspaceID: workspaceID,
		projectID:   project.ID,
		humanID:     humanID,
		sourceID:    sourceID,
		runnerID:    runner.ID,
	}
}

func (e *supervisorPushTestEnv) baseline(t *testing.T) {
	t.Helper()
	if err := e.api.deliverWorkspacePushes(e.ctx, e.workspaceID); err != nil {
		t.Fatal(err)
	}
	e.sender.sent = nil
}

func (e *supervisorPushTestEnv) deliverAndDecode(t *testing.T) []pushPayload {
	t.Helper()
	before := len(e.sender.sent)
	if err := e.api.deliverWorkspacePushes(e.ctx, e.workspaceID); err != nil {
		t.Fatal(err)
	}
	out := make([]pushPayload, 0, len(e.sender.sent)-before)
	for _, rec := range e.sender.sent[before:] {
		var payload pushPayload
		if err := json.Unmarshal(rec.payload, &payload); err != nil {
			t.Fatalf("unmarshal push payload: %v", err)
		}
		out = append(out, payload)
	}
	return out
}

func (e *supervisorPushTestEnv) findUpdate(t *testing.T, cardID uuid.UUID) cardUpdate {
	t.Helper()
	updates, err := e.api.cardUpdates(e.ctx, e.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range updates {
		if u.ID == cardID.String() {
			return u
		}
	}
	t.Fatalf("card %s not found in cardUpdates", cardID)
	return cardUpdate{}
}

func (e *supervisorPushTestEnv) claimForCard(t *testing.T, cardID uuid.UUID, now time.Time) *store.AttemptClaim {
	t.Helper()
	claim, err := e.store.ClaimNextTurnAttempt(e.ctx, e.workspaceID, e.runnerID, now, time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim next attempt for %s: claim=%v err=%v", cardID, claim, err)
	}
	if claim.Attempt.CardID != cardID {
		t.Fatalf("claimed card %s, want %s", claim.Attempt.CardID, cardID)
	}
	return claim
}

func TestPushDedupe_ReviewCallbackSilencesTodoAndNotifiesSupervisorOnce(t *testing.T) {
	env := newSupervisorPushTestEnv(t)
	mainChat, _, err := env.store.EnsureMainDiscussion(env.ctx, env.workspaceID, env.projectID, env.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	todo, err := env.store.CreateTodoCard(env.ctx, env.workspaceID, env.projectID, env.sourceID, "实现推送去重", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.MoveTodo(env.ctx, env.workspaceID, todo.ID, env.runnerID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	env.baseline(t)

	// 1. 子任务执行并先 ConfirmCard 进入 Review，再在同一运行 Attempt 内发出总结气泡。
	if _, err := env.store.CreateHumanTurn(env.ctx, env.workspaceID, todo.ID, env.sourceID, "开始开发", "todo-review-1", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	todoClaim := env.claimForCard(t, todo.ID, now)
	confirmed, err := env.store.ConfirmCard(env.ctx, env.workspaceID, todo.ID, env.runnerID, "测试全部通过", "confirm-review-1")
	if err != nil || !confirmed.MovedToReview {
		t.Fatalf("confirm=%+v err=%v", confirmed, err)
	}
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: todoClaim.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "已完成改动并推进到 Review。", EndTurn: true},
		IdempotencyKey: "todo-review-reply-1", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}

	// 子任务自身必须被标记静默（与站内桌面通知同口径），且 Web Push 不发送但快照推进。
	todoUpdate := env.findUpdate(t, todo.ID)
	if !todoUpdate.NotificationSuppressed {
		t.Fatalf("expected Todo review update to have NotificationSuppressed=true, got %+v", todoUpdate)
	}
	if sent := env.deliverAndDecode(t); len(sent) != 0 {
		t.Fatalf("todo entering review with active supervisor must not push, got %+v", sent)
	}
	snapshots, err := env.store.ListPushCardSnapshots(env.ctx, env.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var todoSnap store.PushCardSnapshot
	for _, s := range snapshots {
		if s.CardID == todo.ID {
			todoSnap = s
		}
	}
	if todoSnap.EventSequence != todoUpdate.EventSequence {
		t.Fatalf("todo push snapshot not advanced: got seq %q want %q", todoSnap.EventSequence, todoUpdate.EventSequence)
	}

	// 2. 主聊天领取 review 监督回调 Turn 并向用户汇报 -> 严格发送 1 次 Web Push。
	mainClaim := env.claimForCard(t, mainChat.ID, now.Add(time.Second))
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: mainChat.ID, AttemptID: mainClaim.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "子任务已完成并进入 Review，请验收。", EndTurn: true},
		IdempotencyKey: "main-review-summary-1", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	mainUpdate := env.findUpdate(t, mainChat.ID)
	if mainUpdate.NotificationSuppressed {
		t.Fatalf("expected main discussion review summary not suppressed, got %+v", mainUpdate)
	}
	sent := env.deliverAndDecode(t)
	if len(sent) != 1 || sent[0].CardID != mainChat.ID.String() {
		t.Fatalf("expected exactly 1 push from main discussion, got %+v", sent)
	}

	// 3. 边界保留：进入 Review 后用户在任务卡内追加提问，子任务新回复仍照常推送。
	if _, err := env.store.SubmitSessionComment(env.ctx, store.SubmitSessionCommentParams{
		WorkspaceID: env.workspaceID, CardID: todo.ID, AuthorSourceID: env.sourceID,
		Content: "顺便确认一下覆盖率？", IdempotencyKey: "todo-post-review-comment",
		DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	followUpClaim := env.claimForCard(t, todo.ID, now.Add(2*time.Second))
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: followUpClaim.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "所有新增路径均已覆盖。", EndTurn: true},
		IdempotencyKey: "todo-post-review-reply", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	if u := env.findUpdate(t, todo.ID); u.NotificationSuppressed {
		t.Fatalf("post-review follow-up reply must not be suppressed: %+v", u)
	}
	sent = env.deliverAndDecode(t)
	if len(sent) != 1 || sent[0].CardID != todo.ID.String() {
		t.Fatalf("expected 1 push for post-review follow-up reply, got %+v", sent)
	}

	// 4. 边界保留：重新移回 Working（第 2 轮确认）后中途提问照常推，第 2 次确认进 Review 再次静默收口。
	if err := env.store.MoveTodo(env.ctx, env.workspaceID, todo.ID, env.sourceID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.CreateHumanTurn(env.ctx, env.workspaceID, todo.ID, env.sourceID, "按新要求再改一轮", "todo-round2-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	round2Claim := env.claimForCard(t, todo.ID, now.Add(3*time.Second))
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: round2Claim.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "第 2 轮已验证通过。", EndTurn: true},
		IdempotencyKey: "todo-round2-reply", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	// 先发完答复再 ConfirmCard 进入 Review（验证两种调用顺序都能正确静默）。
	confirmed2, err := env.store.ConfirmCard(env.ctx, env.workspaceID, todo.ID, env.runnerID, "round 2 verified", "confirm-review-2")
	if err != nil || !confirmed2.MovedToReview {
		t.Fatalf("confirm round 2=%+v err=%v", confirmed2, err)
	}
	if u := env.findUpdate(t, todo.ID); !u.NotificationSuppressed {
		t.Fatalf("round 2 review update should be suppressed: %+v", u)
	}
	if sent := env.deliverAndDecode(t); len(sent) != 0 {
		t.Fatalf("round 2 review todo must not push, got %+v", sent)
	}
	mainClaim2 := env.claimForCard(t, mainChat.ID, now.Add(4*time.Second))
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: mainChat.ID, AttemptID: mainClaim2.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "第 2 轮已完成并再次进入 Review。", EndTurn: true},
		IdempotencyKey: "main-review-summary-2", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	sent = env.deliverAndDecode(t)
	if len(sent) != 1 || sent[0].CardID != mainChat.ID.String() {
		t.Fatalf("expected 1 push from main discussion for round 2 summary, got %+v", sent)
	}
}

func TestPushDedupe_ReviewFallbackWhenSupervisorMutedOrMissing(t *testing.T) {
	t.Run("监督讨论已静音时子任务照常推送不漏推", func(t *testing.T) {
		env := newSupervisorPushTestEnv(t)
		mainChat, _, err := env.store.EnsureMainDiscussion(env.ctx, env.workspaceID, env.projectID, env.sourceID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.store.SetCardNotificationsMuted(env.ctx, env.workspaceID, mainChat.ID, true); err != nil {
			t.Fatal(err)
		}
		todo, err := env.store.CreateTodoCard(env.ctx, env.workspaceID, env.projectID, env.sourceID, "静音主聊天兜底", "", uuid.NullUUID{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := env.store.MoveTodo(env.ctx, env.workspaceID, todo.ID, env.runnerID, domain.TodoStateWorking); err != nil {
			t.Fatal(err)
		}
		env.baseline(t)

		if _, err := env.store.CreateHumanTurn(env.ctx, env.workspaceID, todo.ID, env.sourceID, "执行", "muted-sup-turn", domain.DispatchModeEnqueue); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		claim := env.claimForCard(t, todo.ID, now)
		if _, err := env.store.ConfirmCard(env.ctx, env.workspaceID, todo.ID, env.runnerID, "ok", "muted-sup-confirm"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
			WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: claim.Attempt.ID,
			RunnerSourceID: env.runnerID,
			Message:        store.ReplyMessage{Type: "text", Content: "已完成，请直接查看任务卡。", EndTurn: true},
			IdempotencyKey: "muted-sup-reply", FinishOnEnd: true,
		}); err != nil {
			t.Fatal(err)
		}
		if u := env.findUpdate(t, todo.ID); u.NotificationSuppressed {
			t.Fatalf("muted supervisor must not suppress todo: %+v", u)
		}
		sent := env.deliverAndDecode(t)
		if len(sent) != 1 || sent[0].CardID != todo.ID.String() {
			t.Fatalf("expected 1 fallback push from todo, got %+v", sent)
		}

		// 静音主聊天即使随后回复总结也不应推送，整体严格仅推 1 次。
		mainClaim := env.claimForCard(t, mainChat.ID, now.Add(time.Second))
		if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
			WorkspaceID: env.workspaceID, CardID: mainChat.ID, AttemptID: mainClaim.Attempt.ID,
			RunnerSourceID: env.runnerID,
			Message:        store.ReplyMessage{Type: "text", Content: "主聊天已静音总结。", EndTurn: true},
			IdempotencyKey: "muted-sup-main-reply", FinishOnEnd: true,
		}); err != nil {
			t.Fatal(err)
		}
		if sent := env.deliverAndDecode(t); len(sent) != 0 {
			t.Fatalf("muted main discussion must not push, got %+v", sent)
		}
	})

	t.Run("无活跃监督讨论时子任务照常推送不漏推", func(t *testing.T) {
		env := newSupervisorPushTestEnv(t)
		// 不创建 mainDiscussion，也不绑定 sourceDiscussion。
		todo, err := env.store.CreateTodoCard(env.ctx, env.workspaceID, env.projectID, env.sourceID, "无主聊天兜底", "", uuid.NullUUID{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := env.store.MoveTodo(env.ctx, env.workspaceID, todo.ID, env.runnerID, domain.TodoStateWorking); err != nil {
			t.Fatal(err)
		}
		env.baseline(t)

		if _, err := env.store.CreateHumanTurn(env.ctx, env.workspaceID, todo.ID, env.sourceID, "执行", "no-sup-turn", domain.DispatchModeEnqueue); err != nil {
			t.Fatal(err)
		}
		claim := env.claimForCard(t, todo.ID, time.Now().UTC())
		if _, err := env.store.ConfirmCard(env.ctx, env.workspaceID, todo.ID, env.runnerID, "ok", "no-sup-confirm"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
			WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: claim.Attempt.ID,
			RunnerSourceID: env.runnerID,
			Message:        store.ReplyMessage{Type: "text", Content: "已完成并进入 Review。", EndTurn: true},
			IdempotencyKey: "no-sup-reply", FinishOnEnd: true,
		}); err != nil {
			t.Fatal(err)
		}
		if u := env.findUpdate(t, todo.ID); u.NotificationSuppressed {
			t.Fatalf("todo without supervisor must not be suppressed: %+v", u)
		}
		sent := env.deliverAndDecode(t)
		if len(sent) != 1 || sent[0].CardID != todo.ID.String() {
			t.Fatalf("expected 1 fallback push from todo without supervisor, got %+v", sent)
		}
	})

	t.Run("来源讨论活跃但已静音时不回退主聊天且子任务照常推送", func(t *testing.T) {
		env := newSupervisorPushTestEnv(t)
		if _, _, err := env.store.EnsureMainDiscussion(env.ctx, env.workspaceID, env.projectID, env.sourceID); err != nil {
			t.Fatal(err)
		}
		srcDisc, err := env.store.CreateDiscussion(env.ctx, env.workspaceID, env.projectID, env.sourceID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.store.SetCardNotificationsMuted(env.ctx, env.workspaceID, srcDisc.ID, true); err != nil {
			t.Fatal(err)
		}
		todo, err := env.store.CreateTodoCard(env.ctx, env.workspaceID, env.projectID, env.sourceID, "静音来源讨论兜底", "", uuid.NullUUID{UUID: srcDisc.ID, Valid: true}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := env.store.MoveTodo(env.ctx, env.workspaceID, todo.ID, env.runnerID, domain.TodoStateWorking); err != nil {
			t.Fatal(err)
		}
		env.baseline(t)

		if _, err := env.store.CreateHumanTurn(env.ctx, env.workspaceID, todo.ID, env.sourceID, "执行", "muted-src-turn", domain.DispatchModeEnqueue); err != nil {
			t.Fatal(err)
		}
		claim := env.claimForCard(t, todo.ID, time.Now().UTC())
		if _, err := env.store.ConfirmCard(env.ctx, env.workspaceID, todo.ID, env.runnerID, "ok", "muted-src-confirm"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
			WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: claim.Attempt.ID,
			RunnerSourceID: env.runnerID,
			Message:        store.ReplyMessage{Type: "text", Content: "来源讨论静音时由我通知你。", EndTurn: true},
			IdempotencyKey: "muted-src-reply", FinishOnEnd: true,
		}); err != nil {
			t.Fatal(err)
		}
		if u := env.findUpdate(t, todo.ID); u.NotificationSuppressed {
			t.Fatalf("muted active source discussion must not suppress todo: %+v", u)
		}
		sent := env.deliverAndDecode(t)
		if len(sent) != 1 || sent[0].CardID != todo.ID.String() {
			t.Fatalf("expected 1 push from todo when source discussion is muted, got %+v", sent)
		}
	})
}

func TestPushDedupe_TodoMidTaskHumanInterventionAlwaysPushes(t *testing.T) {
	env := newSupervisorPushTestEnv(t)
	if _, _, err := env.store.EnsureMainDiscussion(env.ctx, env.workspaceID, env.projectID, env.sourceID); err != nil {
		t.Fatal(err)
	}
	todo, err := env.store.CreateTodoCard(env.ctx, env.workspaceID, env.projectID, env.sourceID, "中途需要人工介入", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.MoveTodo(env.ctx, env.workspaceID, todo.ID, env.runnerID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	env.baseline(t)

	// 1. 中途发 choice 选择题 -> 必须照常推送。
	if _, err := env.store.CreateHumanTurn(env.ctx, env.workspaceID, todo.ID, env.sourceID, "调研方案", "mid-choice-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claim1 := env.claimForCard(t, todo.ID, now)
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: claim1.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message: store.ReplyMessage{
			Type:    "choice",
			EndTurn: true,
			Widget: &store.ChoiceWidget{
				Question: "请拍板使用哪种策略？",
				Options: []store.ChoiceOption{
					{Value: "a", Label: "方案 A"},
					{Value: "b", Label: "方案 B"},
				},
			},
		},
		IdempotencyKey: "mid-choice-reply", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	if u := env.findUpdate(t, todo.ID); u.NotificationSuppressed {
		t.Fatalf("choice reply must not be suppressed: %+v", u)
	}
	sent := env.deliverAndDecode(t)
	if len(sent) != 1 || sent[0].CardID != todo.ID.String() {
		t.Fatalf("expected 1 push for choice reply, got %+v", sent)
	}

	// 2. 中途发普通提问（未进 Review）-> 必须照常推送。
	if _, err := env.store.CreateHumanTurn(env.ctx, env.workspaceID, todo.ID, env.sourceID, "选方案 A", "mid-question-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim2 := env.claimForCard(t, todo.ID, now.Add(time.Second))
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: claim2.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "请提供测试环境账号后我继续。", EndTurn: true},
		IdempotencyKey: "mid-question-reply", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	if u := env.findUpdate(t, todo.ID); u.NotificationSuppressed {
		t.Fatalf("mid-task question must not be suppressed: %+v", u)
	}
	sent = env.deliverAndDecode(t)
	if len(sent) != 1 || sent[0].CardID != todo.ID.String() {
		t.Fatalf("expected 1 push for mid-task question, got %+v", sent)
	}

	// 3. 若存在待审批工具（pending tool_approval），即使卡片进入终态也不得抑制。
	if _, err := env.store.CreateHumanTurn(env.ctx, env.workspaceID, todo.ID, env.sourceID, "继续执行", "mid-approval-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim3 := env.claimForCard(t, todo.ID, now.Add(2*time.Second))
	if _, err := env.store.CreateToolApproval(env.ctx, store.CreateToolApprovalParams{
		WorkspaceID: env.workspaceID, CardID: todo.ID,
		TurnID: &claim3.Turn.ID, AttemptID: &claim3.Attempt.ID,
		ToolCallID: "call-1", ToolName: "exec_command",
		RiskLevel: "high", Reason: "需要执行变更命令",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: claim3.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "等待你审批敏感命令。", EndTurn: true},
		IdempotencyKey: "mid-approval-reply", FinishOnEnd: false,
	}); err != nil {
		t.Fatal(err)
	}
	if u := env.findUpdate(t, todo.ID); u.NotificationSuppressed {
		t.Fatalf("pending tool approval must never be suppressed: %+v", u)
	}
	sent = env.deliverAndDecode(t)
	if len(sent) != 1 || sent[0].CardID != todo.ID.String() {
		t.Fatalf("expected 1 push while pending tool approval exists, got %+v", sent)
	}
}

func TestPushDedupe_FailureCallbacksSilentOnAutoRetryAndNotifyOnceOnCircuitOpen(t *testing.T) {
	env := newSupervisorPushTestEnv(t)
	mainChat, _, err := env.store.EnsureMainDiscussion(env.ctx, env.workspaceID, env.projectID, env.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	todo, err := env.store.CreateTodoCard(env.ctx, env.workspaceID, env.projectID, env.sourceID, "自动自愈与熔断测试", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.MoveTodo(env.ctx, env.workspaceID, todo.ID, env.runnerID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	env.baseline(t)

	// 1. Attempt 1（非熔断失败，即使 error_summary 文本里含“熔断”二字）：
	// 子任务发进度气泡后失败 -> 子任务静默；主聊天自动自愈回复（无 choice）-> 主聊天也静默。
	if _, err := env.store.CreateHumanTurn(env.ctx, env.workspaceID, todo.ID, env.sourceID, "执行部署检查", "fail-flow-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	attempt1 := env.claimForCard(t, todo.ID, now)
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: attempt1.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "正在尝试连接上游服务…", EndTurn: false},
		IdempotencyKey: "fail-a1-progress",
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.store.FinishAttempt(env.ctx, env.workspaceID, todo.ID, attempt1.Attempt.ID, domain.AttemptStatusFailed, "上游接口触发熔断，请换节点", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if u := env.findUpdate(t, todo.ID); !u.NotificationSuppressed {
		t.Fatalf("failed child attempt backed by active supervisor must be suppressed: %+v", u)
	}
	if sent := env.deliverAndDecode(t); len(sent) != 0 {
		t.Fatalf("non-circuit failed child card must not push, got %+v", sent)
	}

	mainRetryClaim := env.claimForCard(t, mainChat.ID, now.Add(2*time.Second))
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: mainChat.ID, AttemptID: mainRetryClaim.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "检测到上游异常，已自动调整参数让子任务重试。", EndTurn: true},
		IdempotencyKey: "main-auto-retry-reply", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	if u := env.findUpdate(t, mainChat.ID); !u.NotificationSuppressed {
		t.Fatalf("main discussion non-circuit auto-retry reply must be suppressed: %+v", u)
	}
	if sent := env.deliverAndDecode(t); len(sent) != 0 {
		t.Fatalf("main discussion non-circuit auto-retry must not push, got %+v", sent)
	}

	// 2. Attempt 2（不同错误，仍为非熔断），但主聊天主动向用户发 choice -> 必须照常推送。
	retry2, err := env.store.RetryAttempt(env.ctx, env.workspaceID, todo.ID, attempt1.Attempt.ID, env.runnerID, domain.AttemptReasonRetry)
	if err != nil {
		t.Fatal(err)
	}
	attempt2 := env.claimForCard(t, todo.ID, now.Add(3*time.Second))
	if attempt2.Attempt.ID != retry2.Attempt.ID {
		t.Fatalf("claimed %s, want %s", attempt2.Attempt.ID, retry2.Attempt.ID)
	}
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: attempt2.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "切换节点重试中…", EndTurn: false},
		IdempotencyKey: "fail-a2-progress",
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.store.FinishAttempt(env.ctx, env.workspaceID, todo.ID, attempt2.Attempt.ID, domain.AttemptStatusFailed, "port refused", now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if sent := env.deliverAndDecode(t); len(sent) != 0 {
		t.Fatalf("attempt 2 failed child card must not push, got %+v", sent)
	}
	mainChoiceClaim := env.claimForCard(t, mainChat.ID, now.Add(5*time.Second))
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: mainChat.ID, AttemptID: mainChoiceClaim.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message: store.ReplyMessage{
			Type:    "choice",
			EndTurn: true,
			Widget: &store.ChoiceWidget{
				Question: "是否改用备用端口重试？",
				Options: []store.ChoiceOption{
					{Value: "yes", Label: "改用备用端口"},
					{Value: "no", Label: "暂停"},
				},
			},
		},
		IdempotencyKey: "main-retry-choice", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	if u := env.findUpdate(t, mainChat.ID); u.NotificationSuppressed {
		t.Fatalf("main discussion choice during failure callback must not be suppressed: %+v", u)
	}
	sent := env.deliverAndDecode(t)
	if len(sent) != 1 || sent[0].CardID != mainChat.ID.String() {
		t.Fatalf("expected 1 push when main discussion emits choice, got %+v", sent)
	}

	// 3. Attempt 3 与 Attempt 2 连续相同错误（port refused）触发熔断（circuit_open=true）：
	// 子任务静默不重复推，主聊天熔断汇报正常推送 1 次。
	retry3, err := env.store.RetryAttempt(env.ctx, env.workspaceID, todo.ID, attempt2.Attempt.ID, env.runnerID, domain.AttemptReasonRetry)
	if err != nil {
		t.Fatal(err)
	}
	attempt3 := env.claimForCard(t, todo.ID, now.Add(6*time.Second))
	if attempt3.Attempt.ID != retry3.Attempt.ID {
		t.Fatalf("claimed %s, want %s", attempt3.Attempt.ID, retry3.Attempt.ID)
	}
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: todo.ID, AttemptID: attempt3.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "再次尝试连接端口…", EndTurn: false},
		IdempotencyKey: "fail-a3-progress",
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.store.FinishAttempt(env.ctx, env.workspaceID, todo.ID, attempt3.Attempt.ID, domain.AttemptStatusFailed, "port refused", now.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	if u := env.findUpdate(t, todo.ID); !u.NotificationSuppressed {
		t.Fatalf("circuit-open failed child card must stay suppressed so supervisor pushes once: %+v", u)
	}
	if sent := env.deliverAndDecode(t); len(sent) != 0 {
		t.Fatalf("circuit-open failed child card must not push, got %+v", sent)
	}

	mainCircuitClaim := env.claimForCard(t, mainChat.ID, now.Add(8*time.Second))
	if _, _, err := env.store.PublishReply(env.ctx, store.PublishReplyParams{
		WorkspaceID: env.workspaceID, CardID: mainChat.ID, AttemptID: mainCircuitClaim.Attempt.ID,
		RunnerSourceID: env.runnerID,
		Message:        store.ReplyMessage{Type: "text", Content: "子任务连续两次报 port refused 已熔断，需要你介入排查。", EndTurn: true},
		IdempotencyKey: "main-circuit-summary", FinishOnEnd: true,
	}); err != nil {
		t.Fatal(err)
	}
	if u := env.findUpdate(t, mainChat.ID); u.NotificationSuppressed {
		t.Fatalf("circuit-open supervisor summary must not be suppressed: %+v", u)
	}
	sent = env.deliverAndDecode(t)
	if len(sent) != 1 || sent[0].CardID != mainChat.ID.String() {
		t.Fatalf("expected exactly 1 push from main discussion for circuit-open summary, got %+v", sent)
	}
}

