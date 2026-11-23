package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/channels"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

// TestRemoteChannels_PrimaryPushRoute_NoMultiChannelSpam 端到端验证远程通道「唯一主推路由」：
// 1. 多通道同时启用时，项目主聊天消息与安全审批挂起仅投递给当前唯一主推通道（不广播轰炸）；
// 2. last_active 模式自动跟随最近活跃入站通道，且切换后不向新主推补发历史积压通知；
// 3. 显式 channel 模式只推指定主力通道，目标被禁用/删除后安全降级为静默（绝不退化为全量广播）；
// 4. none 模式仅网页端接收，所有远程通道保持静默；
// 5. 各通道专属讨论卡回复始终独立回推给对应通道自身，不受主推路由影响；
// 6. scheduled_push 定时推送通道完全独立，不受项目主推路由影响。
func TestRemoteChannels_PrimaryPushRoute_NoMultiChannelSpam(t *testing.T) {
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
	humanSourceID := uuid.MustParse(identity.Source.ID)

	projRec := doJSON(t, handler, http.MethodPost, "/api/v1/projects", map[string]any{
		"name":        "Primary Push Route Project",
		"description": "single primary push integration test",
	}, cookie, nil)
	if projRec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", projRec.Code, projRec.Body.String())
	}
	var project projectResponse
	decodeRecorder(t, projRec, &project)
	projectID := uuid.MustParse(project.ID)
	if project.PrimaryPushMode != string(domain.PrimaryPushModeLastActive) {
		t.Fatalf("default project primary_push_mode = %q, want last_active", project.PrimaryPushMode)
	}

	agentSrc, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Cycle Agent")
	if err != nil {
		t.Fatalf("create agent source: %v", err)
	}
	ongoingCard, err := dataStore.CreateOngoingCard(ctx, workspaceID, projectID, agentSrc.ID, "巡检定时卡", "scheduled push independent test", uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create ongoing card: %v", err)
	}
	ongoingCardID := ongoingCard.ID

	// 创建两个双向对话通道（chA: 飞书，chB: Telegram）+ 一个定时推送通道（schedCh）
	createCh := func(name string, platform string, role string, transport string, targetCardID *uuid.UUID, cfg map[string]any, secrets map[string]any) uuid.UUID {
		body := map[string]any{
			"name":                name,
			"platform":            platform,
			"role":                role,
			"transport":           transport,
			"enabled":             true,
			"project_id":          projectID.String(),
			"mirror_web_messages": true,
			"group_policy":        "all",
			"config":              cfg,
			"secrets":             secrets,
		}
		if targetCardID != nil {
			body["target_card_id"] = targetCardID.String()
		}
		rec := doJSON(t, handler, http.MethodPost, "/api/v1/channels", body, cookie, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create channel %s failed: %d %s", name, rec.Code, rec.Body.String())
		}
		var resp struct {
			Channel remoteChannelDTO `json:"channel"`
		}
		decodeRecorder(t, rec, &resp)
		return resp.Channel.ID
	}

	chAID := createCh("飞书主力", "feishu", "main_chat", "websocket", nil,
		map[string]any{"app_id": "cli_a", "target_chat_id": "oc_feishu_a"},
		map[string]any{"app_secret": "sec-a"})
	chBID := createCh("Telegram 备用", "telegram", "main_chat", "long_poll", nil,
		map[string]any{"target_chat_id": "tg_chat_b"},
		map[string]any{"bot_token": "tok-b"})
	schedID := createCh("企微定时巡检", "wecom", "scheduled_push", "webhook", &ongoingCardID,
		map[string]any{},
		map[string]any{"webhook_url": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=sched-1"})

	stubA := newStubChannelAdapter(domain.ChannelPlatformFeishu)
	stubB := newStubChannelAdapter(domain.ChannelPlatformTelegram)
	stubSched := newStubChannelAdapter(domain.ChannelPlatformWeCom)

	mgr := channels.NewManager(dataStore, nil, "https://cycledock.example.com")
	mgr.SetAdapterFactory(func(ch store.RemoteChannel, _ store.RemoteChannelSecrets) (channels.Adapter, error) {
		switch ch.ID {
		case chAID:
			return stubA, nil
		case chBID:
			return stubB, nil
		case schedID:
			return stubSched, nil
		default:
			return newStubChannelAdapter(ch.Platform), nil
		}
	})
	mgrCtx, cancelMgr := context.WithCancel(ctx)
	defer cancelMgr()
	if err := mgr.Start(mgrCtx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer mgr.Stop()

	dispatcher := channels.NewDispatcher(dataStore, mgr, nil, "https://cycledock.example.com")
	mainDisc, _, err := dataStore.EnsureMainDiscussion(ctx, workspaceID, projectID, humanSourceID)
	if err != nil {
		t.Fatalf("ensure main discussion: %v", err)
	}
	mainCardID := mainDisc.ID

	// 1. 初始状态为 last_active，且 A/B 均无入站记录 -> 有效主推为空 -> 主聊天推送不发远程（也不轰炸）
	getRoute := func() projectPrimaryPushRouteDTO {
		rec := doJSON(t, handler, http.MethodGet, "/api/v1/projects/"+projectID.String()+"/primary-push-route", nil, cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("get primary push route: %d %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Route projectPrimaryPushRouteDTO `json:"route"`
		}
		decodeRecorder(t, rec, &out)
		return out.Route
	}
	if r := getRoute(); r.Mode != domain.PrimaryPushModeLastActive || r.EffectiveChannelID != nil {
		t.Fatalf("expected empty effective primary before any inbound, got %+v", r)
	}

	publishMainReply := func(key, text string) {
		commentRec := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+mainCardID.String()+"/comments", map[string]any{
			"content": "触发-" + key,
		}, cookie, map[string]string{"Idempotency-Key": "comment-" + key})
		if commentRec.Code != http.StatusCreated {
			t.Fatalf("create main comment %s: %d %s", key, commentRec.Code, commentRec.Body.String())
		}
		claim, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, agentSrc.ID, time.Now().UTC(), 5*time.Minute)
		if err != nil {
			t.Fatalf("claim attempt %s: %v", key, err)
		}
		if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
			WorkspaceID:    workspaceID,
			CardID:         mainCardID,
			AttemptID:      claim.Attempt.ID,
			RunnerSourceID: agentSrc.ID,
			IdempotencyKey: "reply-" + key,
			FinishOnEnd:    true,
			Message: store.ReplyMessage{
				Type:    "text",
				Content: text,
				EndTurn: true,
			},
		}); err != nil {
			t.Fatalf("publish main reply %s: %v", key, err)
		}
		if err := dispatcher.DeliverOnce(ctx); err != nil {
			t.Fatalf("deliver once %s: %v", key, err)
		}
	}

	publishMainReply("before-inbound", "未有活跃通道时的历史主聊天通知")
	if len(stubA.SentMessages()) != 0 || len(stubB.SentMessages()) != 0 {
		t.Fatalf("expected 0 outbound pushes before any inbound in last_active mode, got A=%d B=%d",
			len(stubA.SentMessages()), len(stubB.SentMessages()))
	}

	// 2. 通道 A 发入站消息 -> A 成为 last_active 唯一主推；历史通知绝不补发灌爆；A 的专属回复仅回给 A
	stubA.inbound <- channels.InboundMessage{
		ExternalMessageID: "a-in-1",
		SenderID:          "u-a",
		ChatID:            "oc_feishu_a",
		ChatType:          "dm",
		Content:           "我在飞书提问",
	}
	var convCardA uuid.UUID
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		chA, _ := dataStore.GetRemoteChannel(ctx, workspaceID, chAID)
		if chA.ConversationCardID.Valid && chA.LastInboundAt != nil {
			convCardA = chA.ConversationCardID.UUID
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if convCardA == uuid.Nil {
		t.Fatalf("channel A inbound did not bind conversation card")
	}

	if r := getRoute(); r.EffectiveChannelID == nil || *r.EffectiveChannelID != chAID {
		t.Fatalf("expected channel A as effective primary after inbound, got %+v", r)
	}

	// 校验 A 专属回复只回 A，且历史未发的主聊天消息未被灌爆补发
	claimA, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, agentSrc.ID, time.Now().UTC(), 5*time.Minute)
	if err != nil {
		t.Fatalf("claim convA attempt: %v", err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    workspaceID,
		CardID:         convCardA,
		AttemptID:      claimA.Attempt.ID,
		RunnerSourceID: agentSrc.ID,
		IdempotencyKey: "convA-reply-1",
		FinishOnEnd:    true,
		Message:        store.ReplyMessage{Type: "text", Content: "飞书专属回复", EndTurn: true},
	}); err != nil {
		t.Fatalf("publish convA reply: %v", err)
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after convA reply: %v", err)
	}
	sentA := stubA.SentMessages()
	if len(sentA) != 1 || !strings.Contains(sentA[0].Text, "飞书专属回复") {
		t.Fatalf("expected only convA reply (no historical spam), got A=%+v", sentA)
	}
	if len(stubB.SentMessages()) != 0 {
		t.Fatalf("expected B silent on convA reply, got B=%+v", stubB.SentMessages())
	}

	// 3. 项目主聊天主动消息 -> 仅推给当前唯一主推 A，B 静默且游标同步推进
	publishMainReply("main-to-a", "早安播报：今日计划已就绪")
	if gotA, gotB := stubA.SentMessages(), stubB.SentMessages(); len(gotA) != 2 || !strings.Contains(gotA[1].Text, "早安播报") || len(gotB) != 0 {
		t.Fatalf("expected main push only to A, got A=%+v B=%+v", gotA, gotB)
	}

	// 4. 安全审批挂起 -> 仅推给当前唯一主推 A，B 静默不轰炸
	approval, err := dataStore.CreateToolApproval(ctx, store.CreateToolApprovalParams{
		WorkspaceID: workspaceID,
		CardID:      mainCardID,
		ToolCallID:  "call-approval-1",
		ToolName:    "exec_command",
		Arguments:   map[string]any{"cmd": "rm -rf /tmp/demo"},
		RiskLevel:   "high",
		Reason:      "高危命令需要确认",
	})
	if err != nil {
		t.Fatalf("create tool approval: %v", err)
	}
	if approval.ID == uuid.Nil {
		t.Fatalf("expected non-nil approval id")
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after approval 1: %v", err)
	}
	if gotA, gotB := stubA.SentMessages(), stubB.SentMessages(); len(gotA) != 3 || !strings.Contains(gotA[2].Text, "安全审批") || len(gotB) != 0 {
		t.Fatalf("expected tool approval only to primary A, got A=%+v B=%+v", gotA, gotB)
	}

	// 5. 通道 B 发入站消息 -> last_active 自动切换跟随 B；B 收到自己的专属回复，但绝不补发之前给 A 的主聊天与审批！
	time.Sleep(50 * time.Millisecond)
	stubB.inbound <- channels.InboundMessage{
		ExternalMessageID: "b-in-1",
		SenderID:          "u-b",
		ChatID:            "tg_chat_b",
		ChatType:          "dm",
		Content:           "我现在改用 Telegram 聊天",
	}
	var convCardB uuid.UUID
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		chB, _ := dataStore.GetRemoteChannel(ctx, workspaceID, chBID)
		if chB.ConversationCardID.Valid && chB.LastInboundAt != nil {
			convCardB = chB.ConversationCardID.UUID
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if convCardB == uuid.Nil {
		t.Fatalf("channel B inbound did not bind conversation card")
	}
	if r := getRoute(); r.EffectiveChannelID == nil || *r.EffectiveChannelID != chBID {
		t.Fatalf("expected channel B to take over in last_active mode, got %+v", r)
	}

	claimB, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, agentSrc.ID, time.Now().UTC(), 5*time.Minute)
	if err != nil {
		t.Fatalf("claim convB attempt: %v", err)
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    workspaceID,
		CardID:         convCardB,
		AttemptID:      claimB.Attempt.ID,
		RunnerSourceID: agentSrc.ID,
		IdempotencyKey: "convB-reply-1",
		FinishOnEnd:    true,
		Message:        store.ReplyMessage{Type: "text", Content: "Telegram 专属回复", EndTurn: true},
	}); err != nil {
		t.Fatalf("publish convB reply: %v", err)
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver after convB reply: %v", err)
	}
	if gotB := stubB.SentMessages(); len(gotB) != 1 || !strings.Contains(gotB[0].Text, "Telegram 专属回复") {
		t.Fatalf("expected B to receive only its own reply without historical flood, got B=%+v", gotB)
	}

	// 新主聊天消息仅推 B，不推 A
	publishMainReply("main-to-b", "晚间播报：只应发给 Telegram")
	if gotA, gotB := stubA.SentMessages(), stubB.SentMessages(); len(gotA) != 3 || len(gotB) != 2 || !strings.Contains(gotB[1].Text, "晚间播报") {
		t.Fatalf("expected main push only to new active B, got A=%d B=%+v", len(gotA), gotB)
	}

	// 6. 显式切换为指定主力通道 A（PUT /primary-push-route）-> 新主聊天仅推 A，不补发之前给 B 的晚间播报
	putA := doJSON(t, handler, http.MethodPut, "/api/v1/projects/"+projectID.String()+"/primary-push-route", map[string]any{
		"mode":       "channel",
		"channel_id": chAID.String(),
	}, cookie, nil)
	if putA.Code != http.StatusOK {
		t.Fatalf("put primary route channel A: %d %s", putA.Code, putA.Body.String())
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver right after switch to A: %v", err)
	}
	if gotA := stubA.SentMessages(); len(gotA) != 3 {
		t.Fatalf("switching primary to A must NOT replay old messages, got A=%+v", gotA)
	}

	publishMainReply("pinned-to-a", "指定主力测试：应仅推给飞书主力")
	if gotA, gotB := stubA.SentMessages(), stubB.SentMessages(); len(gotA) != 4 || !strings.Contains(gotA[3].Text, "指定主力测试") || len(gotB) != 2 {
		t.Fatalf("expected pinned primary push only to A, got A=%+v B=%d", gotA, len(gotB))
	}

	// 校验 GET /channels 返回 primary_push_routes 且与当前设置一致
	listChRec := doJSON(t, handler, http.MethodGet, "/api/v1/channels", nil, cookie, nil)
	if listChRec.Code != http.StatusOK {
		t.Fatalf("list channels: %d %s", listChRec.Code, listChRec.Body.String())
	}
	var listChOut struct {
		Channels          []remoteChannelDTO           `json:"channels"`
		PrimaryPushRoutes []projectPrimaryPushRouteDTO `json:"primary_push_routes"`
	}
	decodeRecorder(t, listChRec, &listChOut)
	var foundRoute *projectPrimaryPushRouteDTO
	for i := range listChOut.PrimaryPushRoutes {
		if listChOut.PrimaryPushRoutes[i].ProjectID == projectID {
			foundRoute = &listChOut.PrimaryPushRoutes[i]
			break
		}
	}
	if foundRoute == nil || foundRoute.Mode != domain.PrimaryPushModeChannel || foundRoute.EffectiveChannelID == nil || *foundRoute.EffectiveChannelID != chAID {
		t.Fatalf("GET /channels primary_push_routes mismatch: %+v", listChOut.PrimaryPushRoutes)
	}

	// 7. 禁用指定主力通道 A -> 必须安全降级为静默，绝不退化为向 B 广播
	disableA := doJSON(t, handler, http.MethodPost, "/api/v1/channels/"+chAID.String()+"/enabled", map[string]any{
		"enabled": false,
	}, cookie, nil)
	if disableA.Code != http.StatusOK {
		t.Fatalf("disable channel A: %d %s", disableA.Code, disableA.Body.String())
	}
	if r := getRoute(); r.Mode != domain.PrimaryPushModeChannel || r.EffectiveChannelID != nil {
		t.Fatalf("disabled pinned primary should keep channel mode with nil effective id, got %+v", r)
	}
	publishMainReply("while-pinned-disabled", "主推通道停用时的静默消息")
	if len(stubA.SentMessages()) != 4 || len(stubB.SentMessages()) != 2 {
		t.Fatalf("disabled pinned primary must NOT fall back to broadcast, got A=%d B=%d",
			len(stubA.SentMessages()), len(stubB.SentMessages()))
	}

	// 8. 切换为 none（仅网页端 / 免打扰）：主聊天与安全审批均不推任何远程通道；但定时推送通道 scheduled_push 照常工作，通道 B 专属讨论卡也照常回推
	putNone := doJSON(t, handler, http.MethodPut, "/api/v1/projects/"+projectID.String()+"/primary-push-route", map[string]any{
		"mode": "none",
	}, cookie, nil)
	if putNone.Code != http.StatusOK {
		t.Fatalf("put primary route none: %d %s", putNone.Code, putNone.Body.String())
	}
	publishMainReply("while-none", "仅网页端模式下的消息")
	if _, err := dataStore.CreateToolApproval(ctx, store.CreateToolApprovalParams{
		WorkspaceID: workspaceID,
		CardID:      mainCardID,
		ToolCallID:  "call-approval-none",
		ToolName:    "exec_command",
		Arguments:   map[string]any{"cmd": "echo hi"},
		RiskLevel:   "high",
		Reason:      "none 模式审批测试",
	}); err != nil {
		t.Fatalf("create tool approval in none mode: %v", err)
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver in none mode: %v", err)
	}
	if len(stubA.SentMessages()) != 4 || len(stubB.SentMessages()) != 2 {
		t.Fatalf("none mode must silence both main push and tool approval, got A=%d B=%d",
			len(stubA.SentMessages()), len(stubB.SentMessages()))
	}

	// 验证 scheduled_push 定时推送在 none 模式下依然正常推送
	if _, err := pool.Exec(ctx, `
		INSERT INTO events (id, workspace_id, card_id, event_type, actor_source_id, content, checkin_status)
		VALUES ($1, $2, $3, 'checkin', $4, '巡检告警：磁盘占用 91%', 'warning')
	`, uuid.New(), workspaceID, ongoingCardID, agentSrc.ID); err != nil {
		t.Fatalf("insert ongoing checkin: %v", err)
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver scheduled push in none mode: %v", err)
	}
	if gotSched := stubSched.SentMessages(); len(gotSched) != 1 || !strings.Contains(gotSched[0].Text, "磁盘占用 91%") {
		t.Fatalf("scheduled_push must remain unaffected by primary push mode=none, got %+v", gotSched)
	}

	// 验证在 none 模式下，通道 B 的专属讨论卡仍独立正常回推给 B
	stubB.inbound <- channels.InboundMessage{
		ExternalMessageID: "b-in-none-2",
		SenderID:          "u-b",
		ChatID:            "tg_chat_b",
		ChatType:          "dm",
		Content:           "none 模式下我在 Telegram 直接问一句",
	}
	var claimB2 *store.AttemptClaim
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := dataStore.ClaimNextTurnAttempt(ctx, workspaceID, agentSrc.ID, time.Now().UTC(), 5*time.Minute)
		if err == nil && c.Attempt.CardID == convCardB {
			claimB2 = c
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if claimB2 == nil {
		t.Fatalf("expected queued attempt on convCardB in none mode")
	}
	if _, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    workspaceID,
		CardID:         convCardB,
		AttemptID:      claimB2.Attempt.ID,
		RunnerSourceID: agentSrc.ID,
		IdempotencyKey: "convB-reply-none-2",
		FinishOnEnd:    true,
		Message:        store.ReplyMessage{Type: "text", Content: "none 模式下专属回复照常送达", EndTurn: true},
	}); err != nil {
		t.Fatalf("publish convB reply in none mode: %v", err)
	}
	if err := dispatcher.DeliverOnce(ctx); err != nil {
		t.Fatalf("deliver convB reply in none mode: %v", err)
	}
	if gotB := stubB.SentMessages(); len(gotB) != 3 || !strings.Contains(gotB[2].Text, "none 模式下专属回复照常送达") {
		t.Fatalf("own conversation card must still reply in none mode, got B=%+v", gotB)
	}

	// 9. 删除被设为指定主推的通道（ON DELETE SET NULL + 安全静默）
	putA2 := doJSON(t, handler, http.MethodPut, "/api/v1/projects/"+projectID.String()+"/primary-push-route", map[string]any{
		"mode":       "channel",
		"channel_id": chAID.String(),
	}, cookie, nil)
	if putA2.Code != http.StatusOK {
		t.Fatalf("re-pin channel A before delete: %d %s", putA2.Code, putA2.Body.String())
	}
	delA := doJSON(t, handler, http.MethodDelete, "/api/v1/channels/"+chAID.String(), nil, cookie, nil)
	if delA.Code != http.StatusOK {
		t.Fatalf("delete pinned channel A: %d %s", delA.Code, delA.Body.String())
	}
	if r := getRoute(); r.Mode != domain.PrimaryPushModeChannel || r.ChannelID != nil || r.EffectiveChannelID != nil {
		t.Fatalf("after deleting pinned channel A, expected safe silent channel mode with nil ids, got %+v", r)
	}
	// 同时校验直接数据库层外键没有破坏项目完整性
	var dbMode string
	var dbChID *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT primary_push_mode, primary_push_channel_id FROM projects WHERE id = $1`, projectID).Scan(&dbMode, &dbChID); err != nil {
		t.Fatalf("query project row after channel delete: %v", err)
	}
	if dbMode != "channel" || dbChID != nil {
		t.Fatalf("expected DB primary_push_mode='channel', channel_id=NULL after delete, got mode=%q ch=%v", dbMode, dbChID)
	}
}
