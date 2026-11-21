package httpapi

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

	"github.com/uvwt/CycleDock/internal/channels"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

type stubChannelAdapter struct {
	platform domain.ChannelPlatform

	mu      sync.Mutex
	sent    []channels.OutboundMessage
	inbound chan channels.InboundMessage
}

func newStubChannelAdapter(platform domain.ChannelPlatform) *stubChannelAdapter {
	return &stubChannelAdapter{
		platform: platform,
		inbound:  make(chan channels.InboundMessage, 16),
	}
}

func (s *stubChannelAdapter) Platform() domain.ChannelPlatform { return s.platform }

func (s *stubChannelAdapter) Run(ctx context.Context, rt channels.AdapterRuntime) error {
	if rt.OnStatus != nil {
		rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-s.inbound:
			if rt.OnInbound != nil {
				_ = rt.OnInbound(ctx, msg)
			}
		}
	}
}

func (s *stubChannelAdapter) Send(_ context.Context, msg channels.OutboundMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, msg)
	return nil
}

func (s *stubChannelAdapter) Test(_ context.Context, testText string) error {
	return s.Send(context.Background(), channels.OutboundMessage{Text: testText})
}

func (s *stubChannelAdapter) SentMessages() []channels.OutboundMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]channels.OutboundMessage, len(s.sent))
	copy(out, s.sent)
	return out
}

func TestRemoteChannels_EndToEnd_EncryptionDedupMainChatAndScheduledPush(t *testing.T) {
	handler, pool, dataStore := newHTTPIntegration(t)
	ctx := context.Background()

	if err := dataStore.ConfigureChannelEncryption("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"); err != nil {
		t.Fatalf("configure channel encryption: %v", err)
	}

	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var identity identityResponse
	decodeRecorder(t, login, &identity)
	workspaceID := uuid.MustParse(identity.Workspace.ID)

	projRec := doJSON(t, handler, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Remote Channels Project", "description": "integration test",
	}, cookie, nil)
	if projRec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", projRec.Code, projRec.Body.String())
	}
	var project projectResponse
	decodeRecorder(t, projRec, &project)
	projectID := uuid.MustParse(project.ID)

	// 1. 创建主聊天通道（带敏感凭据）验证加密落库与返回脱敏
	createMain := doJSON(t, handler, http.MethodPost, "/api/v1/channels", map[string]any{
		"name":                "Telegram 主聊天",
		"platform":            "telegram",
		"role":                "main_chat",
		"transport":           "long_poll",
		"enabled":             true,
		"project_id":          project.ID,
		"mirror_web_messages": true,
		"group_policy":        "mention_only",
		"allow_from":          []string{"tg-user-1"},
		"config": map[string]any{
			"target_chat_id":    "-100999",
			"message_thread_id": 1,
		},
		"secrets": map[string]any{
			"bot_token": "999888777:SUPER_SECRET_BOT_TOKEN_VALUE",
		},
	}, cookie, nil)
	if createMain.Code != http.StatusCreated {
		t.Fatalf("create main channel: %d %s", createMain.Code, createMain.Body.String())
	}
	if strings.Contains(createMain.Body.String(), "SUPER_SECRET_BOT_TOKEN_VALUE") {
		t.Fatalf("plaintext secret leaked in create response: %s", createMain.Body.String())
	}
	var createdMainResp struct {
		Channel remoteChannelDTO `json:"channel"`
	}
	decodeRecorder(t, createMain, &createdMainResp)
	mainChID := createdMainResp.Channel.ID
	if !createdMainResp.Channel.HasSecrets || createdMainResp.Channel.SecretHints["bot_token"] == "" {
		t.Fatalf("expected secret hint on main channel: %+v", createdMainResp.Channel)
	}

	// 验证数据库中保存的是密文而非明文
	var rawCipher []byte
	if err := pool.QueryRow(ctx, `SELECT secret_ciphertext FROM remote_channels WHERE id = $1`, mainChID).Scan(&rawCipher); err != nil {
		t.Fatalf("read secret_ciphertext: %v", err)
	}
	if len(rawCipher) == 0 || strings.Contains(string(rawCipher), "SUPER_SECRET_BOT_TOKEN_VALUE") {
		t.Fatalf("expected encrypted secret ciphertext in DB")
	}

	// 验证定时任务 Webhook 通道创建，并拦截 main_chat 使用 webhook 的非法配置
	invalidMainWH := doJSON(t, handler, http.MethodPost, "/api/v1/channels", map[string]any{
		"name":       "非法主聊天 Webhook",
		"platform":   "wecom",
		"role":       "main_chat",
		"transport":  "webhook",
		"project_id": project.ID,
		"secrets":    map[string]any{"webhook_url": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc"},
	}, cookie, nil)
	if invalidMainWH.Code < 400 {
		t.Fatalf("expected main_chat+webhook rejected, got %d", invalidMainWH.Code)
	}

	// 创建 Ongoing 卡片及定时任务独立推送通道（warnings_only）
	agentSrc, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Cycle Agent")
	if err != nil {
		t.Fatalf("create agent source: %v", err)
	}
	ongoingCard, err := dataStore.CreateOngoingCard(ctx, workspaceID, projectID, agentSrc.ID, "核心 API 健康巡检", "每小时巡检", uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create ongoing: %v", err)
	}
	ongoingID := ongoingCard.ID

	createSched := doJSON(t, handler, http.MethodPost, "/api/v1/channels", map[string]any{
		"name":           "企微定时巡检群",
		"platform":       "wecom",
		"role":           "scheduled_push",
		"transport":      "webhook",
		"enabled":        true,
		"project_id":     project.ID,
		"target_card_id": ongoingID.String(),
		"push_filter":    "warnings_only",
		"secrets": map[string]any{
			"webhook_url": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=sched-secret-key",
		},
	}, cookie, nil)
	if createSched.Code != http.StatusCreated {
		t.Fatalf("create scheduled_push channel: %d %s", createSched.Code, createSched.Body.String())
	}
	var createdSchedResp struct {
		Channel remoteChannelDTO `json:"channel"`
	}
	decodeRecorder(t, createSched, &createdSchedResp)
	schedChID := createdSchedResp.Channel.ID

	// 2. 启动 Manager + Dispatcher 并注入可控 Stub Adapter
	mainStub := newStubChannelAdapter(domain.ChannelPlatformTelegram)
	schedStub := newStubChannelAdapter(domain.ChannelPlatformWeCom)

	mgr := channels.NewManager(dataStore, nil, "https://cycledock.example")
	mgr.SetAdapterFactory(func(ch store.RemoteChannel, _ store.RemoteChannelSecrets) (channels.Adapter, error) {
		switch ch.ID {
		case mainChID:
			return mainStub, nil
		case schedChID:
			return schedStub, nil
		default:
			return newStubChannelAdapter(ch.Platform), nil
		}
	})
	mgrCtx, mgrCancel := context.WithCancel(ctx)
	defer mgrCancel()
	if err := mgr.Start(mgrCtx); err != nil {
		t.Fatalf("manager start: %v", err)
	}
	defer mgr.Stop()

	dispatcher := channels.NewDispatcher(dataStore, mgr, nil, "https://cycledock.example")

	// 3. 入站测试：白名单拦截 + 合法入站自动创建主聊天 + 幂等去重
	// 非白名单用户应被忽略
	mainStub.inbound <- channels.InboundMessage{
		ExternalMessageID: "ext-denied-1",
		SenderID:          "intruder",
		ChatID:            "-100999",
		ChatType:          "direct",
		Content:           "未授权消息",
	}
	// 合法用户发送相同 external_message_id 两次，应只生成 1 条 Turn
	for range 2 {
		mainStub.inbound <- channels.InboundMessage{
			ExternalMessageID: "ext-msg-001",
			SenderID:          "tg-user-1",
			ChatID:            "-100999",
			ChatType:          "direct",
			Content:           "请汇总今天进展",
		}
	}

	// 等待入站写入主聊天并落库 comment
	var mainCardID uuid.UUID
	var commentCount int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		card, err := dataStore.MainDiscussion(ctx, workspaceID, projectID)
		if err == nil {
			mainCardID = card.ID
			_ = pool.QueryRow(ctx, `
				SELECT COUNT(*) FROM events
				WHERE workspace_id = $1 AND card_id = $2 AND event_type = 'comment'
			`, workspaceID, mainCardID).Scan(&commentCount)
			if commentCount > 0 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	// 多等一小会儿确保重复消息也处理完且不会产生第二条 comment
	time.Sleep(150 * time.Millisecond)
	if mainCardID == uuid.Nil {
		t.Fatal("expected main discussion auto-created by inbound message")
	}

	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM events
		WHERE workspace_id = $1 AND card_id = $2 AND event_type = 'comment'
	`, workspaceID, mainCardID).Scan(&commentCount); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if commentCount != 1 {
		t.Fatalf("expected exactly 1 deduplicated comment from allowlisted sender, got %d", commentCount)
	}

	// 4. 先跑一轮 Dispatcher：来自通道自身的入站消息严禁被镜像回传（防回环）
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver once after inbound: %v", err)
	}
	if got := mainStub.SentMessages(); len(got) != 0 {
		t.Fatalf("loopback guard violated: expected 0 outbound messages for own inbound comment, got %+v", got)
	}

	// 5. 模拟 Agent 认领并回复：先发中间进度气泡 (end_turn=false)，再发最终气泡 (end_turn=true)
	// 验证：中间进度气泡绝不推送，只推送最终气泡且附带直达链接。
	claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, agentSrc.ID, time.Now().UTC(), 5*time.Minute)
	if err != nil {
		t.Fatalf("claim main turn attempt: %v", err)
	}
	attemptID := claim.Attempt.ID

	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    workspaceID,
		CardID:         mainCardID,
		AttemptID:      attemptID,
		RunnerSourceID: agentSrc.ID,
		IdempotencyKey: "progress-bubble-1",
		Message: store.ReplyMessage{
			Type:    "text",
			Content: "中间进度：正在查询日志...",
			EndTurn: false,
		},
	}); err != nil {
		t.Fatalf("publish progress bubble: %v", err)
	}

	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after progress bubble: %v", err)
	}
	if got := mainStub.SentMessages(); len(got) != 0 {
		t.Fatalf("end_turn=false progress bubble must NEVER be pushed to IM, got %+v", got)
	}

	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    workspaceID,
		CardID:         mainCardID,
		AttemptID:      attemptID,
		RunnerSourceID: agentSrc.ID,
		IdempotencyKey: "final-bubble-1",
		FinishOnEnd:    true,
		Message: store.ReplyMessage{
			Type:    "text",
			Content: "今日全部服务运行正常。",
			EndTurn: true,
		},
	}); err != nil {
		t.Fatalf("publish final bubble: %v", err)
	}

	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after final bubble: %v", err)
	}
	sentMain := mainStub.SentMessages()
	if len(sentMain) != 1 || !strings.Contains(sentMain[0].Text, "今日全部服务运行正常") || !strings.Contains(sentMain[0].CardURL, mainCardID.String()) {
		t.Fatalf("expected 1 final bubble with card link, got %+v", sentMain)
	}

	// 6. 验证 mirror_web_messages=true 时：
	// - 不再把 Web 端人类评论原文包装成 [Web 主聊天] 回传到 IM；
	// - 但 Web 端发起对话后的 AI 最终回复会同步到 IM。
	webComment := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+mainCardID.String()+"/comments", map[string]any{
		"content": "这是我在 Web 端发的补充消息",
	}, cookie, map[string]string{"Idempotency-Key": "web-comment-mirror-1"})
	if webComment.Code != http.StatusCreated {
		t.Fatalf("web comment failed: %d %s", webComment.Code, webComment.Body.String())
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after web comment: %v", err)
	}
	sentMain = mainStub.SentMessages()
	if len(sentMain) != 1 {
		t.Fatalf("web human comment must NEVER be echoed back to IM even when mirror_web_messages=true, got %+v", sentMain)
	}
	webClaim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, agentSrc.ID, time.Now().UTC(), 5*time.Minute)
	if err != nil {
		t.Fatalf("claim web human turn attempt: %v", err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    workspaceID,
		CardID:         mainCardID,
		AttemptID:      webClaim.Attempt.ID,
		RunnerSourceID: agentSrc.ID,
		IdempotencyKey: "web-reply-mirror-on-1",
		FinishOnEnd:    true,
		Message: store.ReplyMessage{
			Type:    "text",
			Content: "已收到你在 Web 端的补充消息，正在继续跟进。",
			EndTurn: true,
		},
	}); err != nil {
		t.Fatalf("publish web reply (mirror on): %v", err)
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after web reply (mirror on): %v", err)
	}
	sentMain = mainStub.SentMessages()
	if len(sentMain) != 2 || !strings.Contains(sentMain[1].Text, "已收到你在 Web 端的补充消息") {
		t.Fatalf("expected mirrored web AI reply (without human echo), got %+v", sentMain)
	}

	// 6b. 关闭 mirror_web_messages=false 后验证：
	// - Web 端人类发起对话的 AI 最终回复不再推送；
	// - 后台 system（如主聊天子任务终态回调）与 schedule/主动消息回复仍默认始终推送；
	// - Todo 进入 Review 回调主聊天时，不额外发重复模板提醒，仅由主聊天系统回调汇总。
	if _, err := pool.Exec(ctx, `
		UPDATE remote_channels SET mirror_web_messages = false WHERE workspace_id = $1 AND id = $2
	`, workspaceID, mainChID); err != nil {
		t.Fatalf("disable mirror_web_messages: %v", err)
	}
	webComment2 := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+mainCardID.String()+"/comments", map[string]any{
		"content": "关闭同步后在 Web 端问的问题",
	}, cookie, map[string]string{"Idempotency-Key": "web-comment-mirror-off-1"})
	if webComment2.Code != http.StatusCreated {
		t.Fatalf("web comment 2 failed: %d %s", webComment2.Code, webComment2.Body.String())
	}
	webClaim2, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, agentSrc.ID, time.Now().UTC(), 5*time.Minute)
	if err != nil {
		t.Fatalf("claim web human turn attempt 2: %v", err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    workspaceID,
		CardID:         mainCardID,
		AttemptID:      webClaim2.Attempt.ID,
		RunnerSourceID: agentSrc.ID,
		IdempotencyKey: "web-reply-mirror-off-1",
		FinishOnEnd:    true,
		Message: store.ReplyMessage{
			Type:    "text",
			Content: "这条 Web 对话回复在关闭同步时不应推到 IM。",
			EndTurn: true,
		},
	}); err != nil {
		t.Fatalf("publish web reply (mirror off): %v", err)
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after web reply (mirror off): %v", err)
	}
	if got := mainStub.SentMessages(); len(got) != 2 {
		t.Fatalf("expected web human reply filtered when mirror_web_messages=false, got %+v", got)
	}

	// 主动消息/系统触发（source=system）的主聊天终态回复即使 mirror_web_messages=false 也必须推送。
	proactiveTurnID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO card_turns (id, workspace_id, card_id, source, content, created_by_source_id, dispatch_mode, idempotency_key, request_hash)
		VALUES ($1, $2, $3, 'system', '早安主动问候', $4, 'enqueue', 'proactive-main-greeting-1', 'hash-proactive-main-greeting-1')
	`, proactiveTurnID, workspaceID, mainCardID, agentSrc.ID); err != nil {
		t.Fatalf("insert proactive system turn on main chat: %v", err)
	}
	schedClaim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, agentSrc.ID, time.Now().UTC(), 5*time.Minute)
	if err != nil {
		t.Fatalf("claim proactive turn on main chat: %v", err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    workspaceID,
		CardID:         mainCardID,
		AttemptID:      schedClaim.Attempt.ID,
		RunnerSourceID: agentSrc.ID,
		IdempotencyKey: "main-proactive-reply-1",
		FinishOnEnd:    true,
		Message: store.ReplyMessage{
			Type:    "text",
			Content: "早安，昨晚所有巡检都已平稳通过。",
			EndTurn: true,
		},
	}); err != nil {
		t.Fatalf("publish proactive reply on main chat: %v", err)
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after proactive reply on main chat: %v", err)
	}
	sentMain = mainStub.SentMessages()
	if len(sentMain) != 3 || !strings.Contains(sentMain[2].Text, "早安，昨晚所有巡检都已平稳通过") {
		t.Fatalf("expected proactive reply delivered with mirror_web_messages=false, got %+v", sentMain)
	}

	// Todo 派生自主聊天进入 Review：触发主聊天 system 终态回调，不重复发纯模板【待验收】提醒；
	// 主聊天收到 system 回调后回复的总结在 mirror_web_messages=false 下仍默认推送。
	autoStartFalse := false
	genTodo, err := dataStore.GenerateWorkFromDiscussion(ctx, store.GenerateWorkParams{
		WorkspaceID:           workspaceID,
		DiscussionID:          mainCardID,
		SourceID:              agentSrc.ID,
		Kind:                  domain.CardKindTodo,
		Title:                 "修复微信长轮询解码",
		Description:           "测试主聊天回调去重",
		RequiredConfirmations: 1,
		AutoStart:             &autoStartFalse,
		IdempotencyKey:        "gen-todo-main-review-dedup",
	})
	if err != nil {
		t.Fatalf("generate todo from main chat: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE cards SET todo_state = 'working' WHERE workspace_id = $1 AND id = $2
	`, workspaceID, genTodo.Card.ID); err != nil {
		t.Fatalf("move todo to working: %v", err)
	}
	confirmRes, err := dataStore.ConfirmCard(ctx, workspaceID, genTodo.Card.ID, agentSrc.ID, "已通过单测", "confirm-main-review-dedup-1")
	if err != nil || !confirmRes.MovedToReview {
		t.Fatalf("confirm todo to review: res=%+v err=%v", confirmRes, err)
	}
	// 先跑一轮 Dispatcher：此时 Todo 已产生 working -> review 状态变更，因回调目标就是主聊天，绝不发重复模板提醒。
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after todo review: %v", err)
	}
	if got := mainStub.SentMessages(); len(got) != 3 {
		t.Fatalf("expected no duplicate template review alert when supervisor callback targets main chat, got %+v", got)
	}
	// 接着主聊天认领该 system 终态回调并回复总结：即便 mirror_web_messages=false，也必须推送给 IM。
	cbClaim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, agentSrc.ID, time.Now().UTC(), 5*time.Minute)
	if err != nil {
		t.Fatalf("claim supervisor review callback turn: %v", err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    workspaceID,
		CardID:         mainCardID,
		AttemptID:      cbClaim.Attempt.ID,
		RunnerSourceID: agentSrc.ID,
		IdempotencyKey: "main-system-callback-reply-1",
		FinishOnEnd:    true,
		Message: store.ReplyMessage{
			Type:    "text",
			Content: "子任务「修复微信长轮询解码」已完成验证进入待验收。",
			EndTurn: true,
		},
	}); err != nil {
		t.Fatalf("publish supervisor callback summary: %v", err)
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after supervisor callback summary: %v", err)
	}
	sentMain = mainStub.SentMessages()
	if len(sentMain) != 4 || !strings.Contains(sentMain[3].Text, "修复微信长轮询解码") {
		t.Fatalf("expected system callback summary delivered with mirror_web_messages=false, got %+v", sentMain)
	}

	// 而派生自非主聊天普通讨论的 Todo 进 Review，由于回调到普通讨论卡，主聊天通道仍应保留【待验收】模板提醒。
	otherDiscCard, err := dataStore.CreateDiscussion(ctx, workspaceID, projectID, agentSrc.ID)
	if err != nil {
		t.Fatalf("create other discussion: %v", err)
	}
	otherDiscID := otherDiscCard.ID
	otherTodo, err := dataStore.GenerateWorkFromDiscussion(ctx, store.GenerateWorkParams{
		WorkspaceID:           workspaceID,
		DiscussionID:          otherDiscID,
		SourceID:              agentSrc.ID,
		Kind:                  domain.CardKindTodo,
		Title:                 "专项子任务",
		Description:           "来自非主聊天讨论",
		RequiredConfirmations: 1,
		AutoStart:             &autoStartFalse,
		IdempotencyKey:        "gen-todo-other-disc-review",
	})
	if err != nil {
		t.Fatalf("generate todo from other discussion: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE cards SET todo_state = 'working' WHERE workspace_id = $1 AND id = $2
	`, workspaceID, otherTodo.Card.ID); err != nil {
		t.Fatalf("move other todo to working: %v", err)
	}
	if res, err := dataStore.ConfirmCard(ctx, workspaceID, otherTodo.Card.ID, agentSrc.ID, "已核查", "confirm-other-disc-review-1"); err != nil || !res.MovedToReview {
		t.Fatalf("confirm other todo to review: res=%+v err=%v", res, err)
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after other discussion todo review: %v", err)
	}
	sentMain = mainStub.SentMessages()
	if len(sentMain) != 5 || !strings.Contains(sentMain[4].Text, "【待验收】专项子任务") {
		t.Fatalf("expected template review reminder for non-main-chat todo, got %+v", sentMain)
	}

	// 7. 验证定时任务独立推送：healthy 巡检在 warnings_only 下被过滤，warning 巡检被推送，且绝不写入主聊天 Session！
	if _, err := pool.Exec(ctx, `
		INSERT INTO events (id, workspace_id, card_id, event_type, actor_source_id, content, checkin_status)
		VALUES ($1, $2, $3, 'checkin', $4, '全部节点延迟正常', 'healthy'),
		       ($5, $2, $3, 'checkin', $4, '数据库慢查询激增', 'warning')
	`, uuid.New(), workspaceID, ongoingID, agentSrc.ID, uuid.New()); err != nil {
		t.Fatalf("insert ongoing checkins: %v", err)
	}

	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver scheduled push: %v", err)
	}
	sentSched := schedStub.SentMessages()
	if len(sentSched) != 1 || !strings.Contains(sentSched[0].Text, "数据库慢查询激增") || strings.Contains(sentSched[0].Text, "全部节点延迟正常") {
		t.Fatalf("expected only warning checkin pushed to scheduled channel, got %+v", sentSched)
	}

	// 验证定时任务推送绝未污染主聊天事件表
	var mainEventsAfter int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM events WHERE workspace_id = $1 AND card_id = $2 AND content LIKE '%数据库慢查询激增%'
	`, workspaceID, mainCardID).Scan(&mainEventsAfter); err != nil {
		t.Fatalf("check main chat isolation: %v", err)
	}
	if mainEventsAfter != 0 {
		t.Fatalf("scheduled_push must NEVER write into main chat session, found %d leaked events", mainEventsAfter)
	}

	// 8. 验证微信扫码登录 HTTP 端点流程（向后兼容）
	wxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ilink/bot/get_bot_qrcode") {
			_ = json.NewEncoder(w).Encode(map[string]any{"qrcode": "qr-1", "qrcode_img_content": "https://wx.test/qr-1"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":        "confirmed",
			"bot_token":     "wx-qr-secret-tok",
			"ilink_bot_id":  "wx-bot-9",
			"ilink_user_id": "wx-u-9",
			"baseurl":       "http://" + r.Host,
		})
	}))
	defer wxSrv.Close()

	wxChRec := doJSON(t, handler, http.MethodPost, "/api/v1/channels", map[string]any{
		"name":       "个人微信",
		"platform":   "weixin",
		"role":       "main_chat",
		"transport":  "long_poll",
		"enabled":    false,
		"project_id": project.ID,
		"config":     map[string]any{"base_url": wxSrv.URL},
	}, cookie, nil)
	if wxChRec.Code != http.StatusCreated {
		t.Fatalf("create weixin channel: %d %s", wxChRec.Code, wxChRec.Body.String())
	}
	var wxChResp struct {
		Channel remoteChannelDTO `json:"channel"`
	}
	decodeRecorder(t, wxChRec, &wxChResp)

	startQR := doJSON(t, handler, http.MethodPost, "/api/v1/channels/weixin/login/start", map[string]any{
		"channel_id": wxChResp.Channel.ID.String(),
		"base_url":   wxSrv.URL,
	}, cookie, nil)
	if startQR.Code != http.StatusCreated {
		t.Fatalf("start weixin qr: %d %s", startQR.Code, startQR.Body.String())
	}
	var qrStartResp struct {
		Session channels.WeixinLoginSession `json:"session"`
	}
	decodeRecorder(t, startQR, &qrStartResp)
	if qrStartResp.Session.QRCodeContent != "https://wx.test/qr-1" {
		t.Fatalf("unexpected qr start session: %+v", qrStartResp.Session)
	}

	pollQR := doJSON(t, handler, http.MethodGet, "/api/v1/channels/weixin/login/"+qrStartResp.Session.SessionID, nil, cookie, nil)
	if pollQR.Code != http.StatusOK {
		t.Fatalf("poll weixin qr: %d %s", pollQR.Code, pollQR.Body.String())
	}
	if strings.Contains(pollQR.Body.String(), "wx-qr-secret-tok") {
		t.Fatalf("weixin bot_token leaked in poll response: %s", pollQR.Body.String())
	}
	var qrPollResp struct {
		Session channels.WeixinLoginSession `json:"session"`
		Channel remoteChannelDTO            `json:"channel"`
	}
	decodeRecorder(t, pollQR, &qrPollResp)
	if qrPollResp.Session.Status != "confirmed" || !qrPollResp.Channel.HasSecrets || qrPollResp.Channel.Config.WeixinUserID != "wx-u-9" {
		t.Fatalf("expected weixin secrets encrypted & saved, got %+v", qrPollResp)
	}

	// 9. 验证通用五平台扫码端点：不传 channel_id 直接一步扫码建卡并自动启用（以微信为例）
	uniStart := doJSON(t, handler, http.MethodPost, "/api/v1/remote-channels/qr/start", map[string]any{
		"platform":   "weixin",
		"project_id": project.ID,
		"base_url":   wxSrv.URL,
	}, cookie, nil)
	if uniStart.Code != http.StatusCreated {
		t.Fatalf("start unified qr: %d %s", uniStart.Code, uniStart.Body.String())
	}
	var uniStartResp struct {
		Session channels.QRConnectSession `json:"session"`
	}
	decodeRecorder(t, uniStart, &uniStartResp)
	if uniStartResp.Session.ChannelID != uuid.Nil || uniStartResp.Session.QRCodeContent != "https://wx.test/qr-1" {
		t.Fatalf("unexpected unified qr start session: %+v", uniStartResp.Session)
	}

	// 连续两次 Poll confirmed，验证只自动创建一条通道（幂等不重复建卡）且敏感凭据不泄露
	uniPoll1 := doJSON(t, handler, http.MethodGet, "/api/v1/remote-channels/qr/"+uniStartResp.Session.SessionID, nil, cookie, nil)
	if uniPoll1.Code != http.StatusOK {
		t.Fatalf("poll unified qr 1: %d %s", uniPoll1.Code, uniPoll1.Body.String())
	}
	if strings.Contains(uniPoll1.Body.String(), "wx-qr-secret-tok") {
		t.Fatalf("unified qr response leaked bot_token: %s", uniPoll1.Body.String())
	}
	var uniPollResp1 struct {
		Session channels.QRConnectSession `json:"session"`
		Channel remoteChannelDTO          `json:"channel"`
	}
	decodeRecorder(t, uniPoll1, &uniPollResp1)
	if uniPollResp1.Session.Status != "confirmed" || uniPollResp1.Channel.ID == uuid.Nil || !uniPollResp1.Channel.Enabled || !uniPollResp1.Channel.HasSecrets {
		t.Fatalf("expected auto-created enabled channel, got %+v", uniPollResp1)
	}

	uniPoll2 := doJSON(t, handler, http.MethodGet, "/api/v1/remote-channels/qr/"+uniStartResp.Session.SessionID, nil, cookie, nil)
	if uniPoll2.Code != http.StatusOK {
		t.Fatalf("poll unified qr 2: %d %s", uniPoll2.Code, uniPoll2.Body.String())
	}
	var uniPollResp2 struct {
		Session channels.QRConnectSession `json:"session"`
		Channel remoteChannelDTO          `json:"channel"`
	}
	decodeRecorder(t, uniPoll2, &uniPollResp2)
	if uniPollResp2.Channel.ID != uniPollResp1.Channel.ID {
		t.Fatalf("expected idempotent channel id %s on repeat poll, got %s", uniPollResp1.Channel.ID, uniPollResp2.Channel.ID)
	}

	// 10. 验证入站首条消息自动记忆 target_chat_id（即使通道创建时未填写 target_chat_id）
	autoChRec := doJSON(t, handler, http.MethodPost, "/api/v1/channels", map[string]any{
		"name":        "自动记忆测试",
		"platform":    "telegram",
		"role":        "main_chat",
		"transport":   "long_poll",
		"enabled":     true,
		"project_id":  project.ID,
		"push_filter": "all",
		"secrets":     map[string]any{"bot_token": "123:auto"},
	}, cookie, nil)
	if autoChRec.Code != http.StatusCreated {
		t.Fatalf("create auto memory channel: %d %s", autoChRec.Code, autoChRec.Body.String())
	}
	var autoChResp struct {
		Channel remoteChannelDTO `json:"channel"`
	}
	decodeRecorder(t, autoChRec, &autoChResp)
	autoChID := autoChResp.Channel.ID

	autoStub := newStubChannelAdapter(domain.ChannelPlatformTelegram)
	autoMgr := channels.NewManager(dataStore, nil, "https://cycledock.example")
	autoMgr.SetAdapterFactory(func(ch store.RemoteChannel, _ store.RemoteChannelSecrets) (channels.Adapter, error) {
		if ch.ID == autoChID {
			return autoStub, nil
		}
		return newStubChannelAdapter(ch.Platform), nil
	})
	autoCtx, autoCancel := context.WithCancel(ctx)
	defer autoCancel()
	if err := autoMgr.Start(autoCtx); err != nil {
		t.Fatalf("start auto manager: %v", err)
	}
	defer autoMgr.Stop()

	autoStub.inbound <- channels.InboundMessage{
		ExternalMessageID: "auto-mem-1",
		SenderID:          "tg-u-9",
		ChatID:            "-100777888",
		ChatType:          "group",
		ThreadID:          "42",
		Content:           "hello auto target",
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		gotCh, err := dataStore.GetRemoteChannel(ctx, workspaceID, autoChID)
		if err == nil && gotCh.Config.TargetChatID == "-100777888" && gotCh.Config.MessageThreadID == 42 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	gotCh, err := dataStore.GetRemoteChannel(ctx, workspaceID, autoChID)
	if err != nil || gotCh.Config.TargetChatID != "-100777888" || gotCh.Config.TargetChatType != "group" || gotCh.Config.MessageThreadID != 42 {
		t.Fatalf("expected target_chat_id auto persisted, got %+v err=%v", gotCh.Config, err)
	}
}
