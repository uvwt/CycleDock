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
