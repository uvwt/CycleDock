package channels

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

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestChannelCardURLAndSanitizeOutbound(t *testing.T) {
	projectID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	cardID := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	gotURL := ChannelCardURL("https://dock.example.com/", projectID, cardID)
	wantURL := "https://dock.example.com/cards/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222"
	if gotURL != wantURL {
		t.Fatalf("ChannelCardURL() = %q, want %q", gotURL, wantURL)
	}

	raw := "已为你派生子任务\n\n[修复微信刷屏](cycledock://card/22222222-2222-2222-2222-222222222222)\n\n稍后回报进度。"
	// 日常回复不带 CardURL：应去除 cycledock://card/ 协议，且不拼接查看卡片链接。
	gotDaily := FormatOutboundWithLink(raw, "")
	if strings.Contains(gotDaily, "cycledock://") {
		t.Fatalf("daily outbound should not leak cycledock:// scheme: %q", gotDaily)
	}
	if strings.Contains(gotDaily, "查看卡片") {
		t.Fatalf("daily outbound without cardURL should not append card link: %q", gotDaily)
	}
	if !strings.Contains(gotDaily, "「修复微信刷屏」") {
		t.Fatalf("expected readable card title in outbound: %q", gotDaily)
	}

	// 高价值提醒（审批/待验收/异常）传入 cardURL 时保留直达链接。
	gotAlert := FormatOutboundWithLink("【安全审批挂起】需要确认", gotURL)
	if !strings.Contains(gotAlert, "查看卡片："+wantURL) {
		t.Fatalf("alert outbound should keep direct card link: %q", gotAlert)
	}
}

func TestChannelConfigFingerprintIgnoresRuntimeUpdates(t *testing.T) {
	ch := store.RemoteChannel{
		ID:                uuid.New(),
		WorkspaceID:       uuid.New(),
		Name:              "微信",
		Platform:          domain.ChannelPlatformWeixin,
		Role:              domain.ChannelRoleMainChat,
		Transport:         domain.ChannelTransportLongPoll,
		Enabled:           true,
		ProjectID:         uuid.New(),
		PushFilter:        domain.ChannelPushFilterAll,
		MirrorWebMessages: false,
		GroupPolicy:       domain.ChannelGroupPolicyMentionOnly,
		AllowFrom:         []string{"u1"},
		Config:            store.RemoteChannelConfig{BaseURL: "https://ilinkai.weixin.qq.com", BotID: "bot-1"},
		SecretCiphertext:  []byte("cipher-v1"),
		UpdatedAt:         time.Now().UTC(),
		RuntimeState:      store.RemoteChannelRuntimeState{WeixinUpdatesBuf: "buf-1"},
	}
	fp1 := channelConfigFingerprint(ch)

	// 模拟运行态变化（WeixinUpdatesBuf / LastError / UpdatedAt / ConversationCardID），配置指纹不得改变，避免误重启长轮询。
	ch.UpdatedAt = ch.UpdatedAt.Add(time.Minute)
	ch.LastError = "temporary timeout"
	ch.RuntimeState.WeixinUpdatesBuf = "buf-2"
	ch.ConversationCardID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
	fp2 := channelConfigFingerprint(ch)
	if fp1 != fp2 {
		t.Fatalf("runtime state update should not change config fingerprint: %q vs %q", fp1, fp2)
	}

	// 真正修改配置或密钥后，指纹必须变化以触发重启。
	ch.SecretCiphertext = []byte("cipher-v2")
	fp3 := channelConfigFingerprint(ch)
	if fp1 == fp3 {
		t.Fatalf("secret update should change config fingerprint")
	}
}

func TestWeixinPollOnceDeterministicDedupAndEagerStatePersist(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		// 返回一条缺失 message_id / seq / create_time_ms 的微信入站消息 + 新 updatesBuf + context_token。
		_, _ = w.Write([]byte(`{
			"ret": 0,
			"get_updates_buf": "buf-next-99",
			"msgs": [
				{
					"from_user_id": "wx_user_1",
					"context_token": "ctx_tok_1",
					"message_type": 1,
					"item_list": [{"type": 1, "text_item": {"text": "你好微信"}}]
				}
			]
		}`))
	}))
	defer srv.Close()

	ch := store.RemoteChannel{
		ID:       uuid.New(),
		Platform: domain.ChannelPlatformWeixin,
		Role:     domain.ChannelRoleMainChat,
		Config:   store.RemoteChannelConfig{BaseURL: srv.URL},
	}
	adapter := NewWeixinAdapter(ch, store.RemoteChannelSecrets{BotToken: "tok"}, srv.Client())

	var (
		savedState   store.RemoteChannelRuntimeState
		saveOrder    []string
		inboundIDs   []string
	)
	rt := AdapterRuntime{
		SaveState: func(_ context.Context, mutate func(st *store.RemoteChannelRuntimeState)) error {
			saveOrder = append(saveOrder, "save")
			mutate(&savedState)
			return nil
		},
		OnInbound: func(_ context.Context, msg InboundMessage) error {
			saveOrder = append(saveOrder, "inbound")
			inboundIDs = append(inboundIDs, msg.ExternalMessageID)
			return context.DeadlineExceeded // 模拟入站失败
		},
	}

	if err := adapter.pollOnce(context.Background(), rt); err != nil {
		t.Fatalf("pollOnce #1 failed: %v", err)
	}
	if savedState.WeixinUpdatesBuf != "buf-next-99" {
		t.Fatalf("expected WeixinUpdatesBuf persisted eagerly, got %q", savedState.WeixinUpdatesBuf)
	}
	if savedState.WeixinContextTokens["wx_user_1"] != "ctx_tok_1" {
		t.Fatalf("expected WeixinContextTokens[wx_user_1] persisted eagerly, got %+v", savedState.WeixinContextTokens)
	}
	if len(saveOrder) < 2 || saveOrder[0] != "save" || saveOrder[1] != "inbound" {
		t.Fatalf("expected SaveState before OnInbound so buf does not roll back on error, order=%v", saveOrder)
	}

	// 再次拉到同一条缺失 ID 的消息，确定性指纹必须与第一次一致，保障数据库去重生效。
	if err := adapter.pollOnce(context.Background(), rt); err != nil {
		t.Fatalf("pollOnce #2 failed: %v", err)
	}
	if len(inboundIDs) != 2 || inboundIDs[0] == "" || inboundIDs[0] != inboundIDs[1] {
		t.Fatalf("expected deterministic external message ID across polls, got %v", inboundIDs)
	}
	if !strings.HasPrefix(inboundIDs[0], "fp:") {
		t.Fatalf("expected fingerprint prefix fp:, got %q", inboundIDs[0])
	}
}

func TestQQSendExpiredPassiveMsgFallbackAndSeqMonotonic(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []map[string]any
		failOnce = true
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/app/getAppAccessToken") {
			_, _ = w.Write([]byte(`{"access_token":"qq_tok","expires_in":7200}`))
			return
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		requests = append(requests, payload)
		shouldFail := failOnce && payload["msg_id"] != nil
		if shouldFail {
			failOnce = false
		}
		mu.Unlock()
		if shouldFail {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":40034101,"message":"msg_id expired"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"sent_1"}`))
	}))
	defer srv.Close()

	baseNow := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
	now := baseNow

	ch := store.RemoteChannel{
		ID:       uuid.New(),
		Platform: domain.ChannelPlatformQQ,
		Role:     domain.ChannelRoleMainChat,
		Config: store.RemoteChannelConfig{
			BaseURL:      srv.URL,
			AppID:        "10001",
			TargetChatID: "openid_1",
		},
		RuntimeState: store.RemoteChannelRuntimeState{
			LastChatID:    "openid_1",
			LastMessageID: "passive_msg_1",
			LastMessageAt: baseNow.Unix(),
		},
	}
	adapter := NewQQAdapter(ch, store.RemoteChannelSecrets{Secret: "sec"}, srv.Client())
	adapter.nowFn = func() time.Time { return now }

	// 场景 1：在 4 分钟窗口内，先带 msg_id 发送；服务端报过期错误时，自动清空 msg_id 降级为主动消息重试成功。
	if err := adapter.Send(context.Background(), OutboundMessage{Text: "hello qq"}); err != nil {
		t.Fatalf("QQ Send fallback failed: %v", err)
	}
	mu.Lock()
	if len(requests) != 2 {
		mu.Unlock()
		t.Fatalf("expected 2 requests (passive + proactive fallback), got %d", len(requests))
	}
	if requests[0]["msg_id"] != "passive_msg_1" {
		mu.Unlock()
		t.Fatalf("first request should carry passive msg_id, got %v", requests[0]["msg_id"])
	}
	if _, hasMsgID := requests[1]["msg_id"]; hasMsgID {
		mu.Unlock()
		t.Fatalf("fallback request should omit expired msg_id, got %v", requests[1]["msg_id"])
	}
	seq1 := requests[0]["msg_seq"].(float64)
	seq2 := requests[1]["msg_seq"].(float64)
	mu.Unlock()
	if seq2 <= seq1 {
		t.Fatalf("msg_seq must strictly increase: seq1=%v seq2=%v", seq1, seq2)
	}

	// 场景 2：超过 4 分钟窗口的旧 msg_id 直接跳过被动回复，直接走主动消息，不浪费一次报错。
	adapter.mu.Lock()
	adapter.lastMsgID = "old_passive_msg"
	adapter.lastMsgAt = baseNow
	adapter.mu.Unlock()
	now = baseNow.Add(5 * time.Minute)
	if err := adapter.Send(context.Background(), OutboundMessage{Text: "second msg"}); err != nil {
		t.Fatalf("QQ Send after passive expiry failed: %v", err)
	}
	mu.Lock()
	lastReq := requests[len(requests)-1]
	mu.Unlock()
	if _, hasMsgID := lastReq["msg_id"]; hasMsgID {
		t.Fatalf("expired msg_id (>4m) should not be attached: %v", lastReq)
	}
}
