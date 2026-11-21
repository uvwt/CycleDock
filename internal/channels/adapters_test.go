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
	"github.com/gorilla/websocket"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func noopStatus(context.Context, domain.ChannelRuntimeStatus, string, bool) {}
func noopSaveState(context.Context, func(*store.RemoteChannelRuntimeState)) error {
	return nil
}

func TestAdapter_Weixin_QRLoginPollAndSend(t *testing.T) {
	var (
		mu       sync.Mutex
		sentText string
		sentCtx  string
		pollHits int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/ilink/bot/get_bot_qrcode"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"qrcode":             "qr-ticket-001",
				"qrcode_img_content": "https://ilink.test/qr/qr-ticket-001",
			})
		case strings.HasPrefix(r.URL.Path, "/ilink/bot/get_qrcode_status"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":        "confirmed",
				"bot_token":     "wx-bot-token-secret",
				"ilink_bot_id":  "wx-bot-001",
				"ilink_user_id": "wx-user-888",
				"baseurl":       "http://" + r.Host,
			})
		case r.URL.Path == "/ilink/bot/getupdates":
			mu.Lock()
			pollHits++
			hit := pollHits
			mu.Unlock()
			if hit == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ret":             0,
					"get_updates_buf": "cursor-1",
					"msgs": []map[string]any{
						{
							"message_id":    "wx-msg-1",
							"from_user_id":  "wx-user-888",
							"message_type":  1,
							"context_token": "ctx-tok-abc",
							"item_list": []map[string]any{
								{"type": 1, "text_item": map[string]any{"text": "帮我看下构建状态"}},
							},
						},
					},
				})
				return
			}
			time.Sleep(150 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0, "get_updates_buf": "cursor-1", "msgs": []any{}})
		case r.URL.Path == "/ilink/bot/sendmessage":
			var body struct {
				Msg struct {
					ToUserID     string `json:"to_user_id"`
					ContextToken string `json:"context_token"`
					ItemList     []struct {
						TextItem struct {
							Text string `json:"text"`
						} `json:"text_item"`
					} `json:"item_list"`
				} `json:"msg"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			sentCtx = body.Msg.ContextToken
			if len(body.Msg.ItemList) > 0 {
				sentText = body.Msg.ItemList[0].TextItem.Text
			}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// 1. 微信扫码登录会话与轮询确认
	wsID := uuid.New()
	chID := uuid.New()
	loginStore := NewWeixinLoginStore(srv.Client(), srv.URL)
	sess, err := loginStore.Start(context.Background(), wsID, chID, srv.URL)
	if err != nil {
		t.Fatalf("weixin qr start: %v", err)
	}
	if sess.QRCodeContent != "https://ilink.test/qr/qr-ticket-001" {
		t.Fatalf("unexpected qr content: %s", sess.QRCodeContent)
	}
	confirmed, err := loginStore.Poll(context.Background(), wsID, sess.SessionID, "")
	if err != nil {
		t.Fatalf("weixin qr poll: %v", err)
	}
	if confirmed.Status != "confirmed" || confirmed.BotToken != "wx-bot-token-secret" || confirmed.WeixinUserID != "wx-user-888" {
		t.Fatalf("unexpected confirmed session: %+v", confirmed)
	}

	// 2. 长轮询入站与携带 context_token 出站
	factory := DefaultAdapterFactory(srv.Client())
	ch := store.RemoteChannel{
		ID:        chID,
		Platform:  domain.ChannelPlatformWeixin,
		Role:      domain.ChannelRoleMainChat,
		Transport: domain.ChannelTransportLongPoll,
		Config:    store.RemoteChannelConfig{BaseURL: srv.URL, WeixinUserID: "wx-user-888"},
	}
	adapter, err := factory(ch, store.RemoteChannelSecrets{BotToken: "wx-bot-token-secret"})
	if err != nil {
		t.Fatalf("create weixin adapter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gotInbound := make(chan InboundMessage, 1)
	go func() {
		_ = adapter.Run(ctx, AdapterRuntime{
			OnInbound: func(_ context.Context, msg InboundMessage) error {
				gotInbound <- msg
				cancel()
				return nil
			},
			OnStatus:  noopStatus,
			SaveState: noopSaveState,
		})
	}()

	select {
	case in := <-gotInbound:
		if in.ExternalMessageID != "wx-msg-1" || in.SenderID != "wx-user-888" || in.Content != "帮我看下构建状态" {
			t.Fatalf("unexpected inbound: %+v", in)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for weixin inbound message")
	}

	if err := adapter.Send(context.Background(), OutboundMessage{Text: "构建已通过", CardURL: "https://cd.test/c/1"}); err != nil {
		t.Fatalf("weixin send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if sentCtx != "ctx-tok-abc" {
		t.Fatalf("expected context_token ctx-tok-abc, got %q", sentCtx)
	}
	if !strings.Contains(sentText, "构建已通过") || !strings.Contains(sentText, "https://cd.test/c/1") {
		t.Fatalf("unexpected sent text: %q", sentText)
	}
}

func TestAdapter_WeCom_WebSocketAndWebhook(t *testing.T) {
	// 1. 企微群 Webhook 推送
	var webhookGot string
	whSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MsgType  string `json:"msgtype"`
			Markdown struct {
				Content string `json:"content"`
			} `json:"markdown"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		webhookGot = body.Markdown.Content
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"})
	}))
	defer whSrv.Close()

	factory := DefaultAdapterFactory(whSrv.Client())
	whAdapter, err := factory(
		store.RemoteChannel{ID: uuid.New(), Platform: domain.ChannelPlatformWeCom, Role: domain.ChannelRoleScheduledPush, Transport: domain.ChannelTransportWebhook},
		store.RemoteChannelSecrets{WebhookURL: whSrv.URL},
	)
	if err != nil {
		t.Fatalf("create wecom webhook adapter: %v", err)
	}
	if err := whAdapter.Send(context.Background(), OutboundMessage{Text: "每日巡检正常", CardURL: "https://cd.test/card/1"}); err != nil {
		t.Fatalf("wecom webhook send: %v", err)
	}
	if !strings.Contains(webhookGot, "每日巡检正常") {
		t.Fatalf("unexpected webhook payload: %q", webhookGot)
	}

	// 2. 企微 AI Bot WebSocket 长连接双向收发
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	sentOut := make(chan string, 1)
	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, subRaw, err := conn.ReadMessage()
		if err != nil || !strings.Contains(string(subRaw), "aibot_subscribe") {
			return
		}
		_ = conn.WriteJSON(map[string]any{"cmd": "aibot_subscribe", "errcode": 0})
		_ = conn.WriteJSON(map[string]any{
			"cmd": "aibot_msg_callback",
			"body": map[string]any{
				"msgid":    "wecom-m1",
				"chattype": "single",
				"from":     map[string]any{"userid": "zhangsan"},
				"msgtype":  "text",
				"text":     map[string]any{"content": "你好企微"},
			},
		})
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if strings.Contains(string(raw), "aibot_send_msg") {
				sentOut <- string(raw)
				return
			}
		}
	}))
	defer wsSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(wsSrv.URL, "http")
	wsAdapter, err := factory(
		store.RemoteChannel{
			ID:        uuid.New(),
			Platform:  domain.ChannelPlatformWeCom,
			Role:      domain.ChannelRoleMainChat,
			Transport: domain.ChannelTransportWebSocket,
			Config:    store.RemoteChannelConfig{WSURL: wsURL, BotID: "bot-001", TargetChatID: "zhangsan"},
		},
		store.RemoteChannelSecrets{Secret: "wecom-secret"},
	)
	if err != nil {
		t.Fatalf("create wecom ws adapter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gotIn := make(chan InboundMessage, 1)
	go func() {
		_ = wsAdapter.Run(ctx, AdapterRuntime{
			OnInbound: func(_ context.Context, msg InboundMessage) error {
				gotIn <- msg
				return nil
			},
			OnStatus:  noopStatus,
			SaveState: noopSaveState,
		})
	}()

	select {
	case in := <-gotIn:
		if in.ExternalMessageID != "wecom-m1" || in.Content != "你好企微" {
			t.Fatalf("unexpected wecom inbound: %+v", in)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for wecom ws inbound")
	}

	if err := wsAdapter.Send(context.Background(), OutboundMessage{Text: "收到企微消息"}); err != nil {
		t.Fatalf("wecom ws send: %v", err)
	}
	select {
	case raw := <-sentOut:
		if !strings.Contains(raw, "收到企微消息") {
			t.Fatalf("unexpected ws outbound: %s", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for wecom ws outbound")
	}
}

func TestAdapter_Feishu_WebhookAndOpenAPISend(t *testing.T) {
	var gotWebhook, gotOpenAPI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/webhook":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["sign"] == "" {
				t.Errorf("expected feishu webhook sign")
			}
			b, _ := json.Marshal(body)
			gotWebhook = string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t-feishu-tok", "expire": 7200})
		case "/open-apis/im/v1/messages":
			if r.Header.Get("Authorization") != "Bearer t-feishu-tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			b, _ := json.Marshal(body)
			gotOpenAPI = string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	factory := DefaultAdapterFactory(srv.Client())
	// 1. 飞书加签群 Webhook
	whAdapter, err := factory(
		store.RemoteChannel{ID: uuid.New(), Platform: domain.ChannelPlatformFeishu, Role: domain.ChannelRoleScheduledPush, Transport: domain.ChannelTransportWebhook},
		store.RemoteChannelSecrets{WebhookURL: srv.URL + "/webhook", WebhookSecret: "fs-sign-secret"},
	)
	if err != nil {
		t.Fatalf("create feishu webhook adapter: %v", err)
	}
	if err := whAdapter.Send(context.Background(), OutboundMessage{Text: "飞书定时巡检告警"}); err != nil {
		t.Fatalf("feishu webhook send: %v", err)
	}
	if !strings.Contains(gotWebhook, "飞书定时巡检告警") {
		t.Fatalf("unexpected feishu webhook: %s", gotWebhook)
	}

	// 2. 飞书应用 OpenAPI 发消息
	apiAdapter, err := factory(
		store.RemoteChannel{
			ID:        uuid.New(),
			Platform:  domain.ChannelPlatformFeishu,
			Role:      domain.ChannelRoleMainChat,
			Transport: domain.ChannelTransportWebSocket,
			Config:    store.RemoteChannelConfig{BaseURL: srv.URL, AppID: "cli_123", TargetChatID: "oc_chat_1"},
		},
		store.RemoteChannelSecrets{AppSecret: "sec_123"},
	)
	if err != nil {
		t.Fatalf("create feishu api adapter: %v", err)
	}
	if err := apiAdapter.Send(context.Background(), OutboundMessage{Text: "飞书主聊天回复"}); err != nil {
		t.Fatalf("feishu openapi send: %v", err)
	}
	if !strings.Contains(gotOpenAPI, "飞书主聊天回复") || !strings.Contains(gotOpenAPI, "oc_chat_1") {
		t.Fatalf("unexpected feishu openapi send: %s", gotOpenAPI)
	}
}

func TestAdapter_DingTalk_StreamAndWebhook(t *testing.T) {
	// 1. 钉钉加签群 Webhook
	var whGot string
	whSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sign") == "" || r.URL.Query().Get("timestamp") == "" {
			t.Errorf("expected dingtalk webhook sign and timestamp")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		b, _ := json.Marshal(body)
		whGot = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"})
	}))
	defer whSrv.Close()

	factory := DefaultAdapterFactory(whSrv.Client())
	whAdapter, err := factory(
		store.RemoteChannel{ID: uuid.New(), Platform: domain.ChannelPlatformDingTalk, Role: domain.ChannelRoleScheduledPush, Transport: domain.ChannelTransportWebhook},
		store.RemoteChannelSecrets{WebhookURL: whSrv.URL, WebhookSecret: "SEC_test_ding"},
	)
	if err != nil {
		t.Fatalf("create dingtalk webhook adapter: %v", err)
	}
	if err := whAdapter.Send(context.Background(), OutboundMessage{Text: "钉钉定时推送正常"}); err != nil {
		t.Fatalf("dingtalk webhook send: %v", err)
	}
	if !strings.Contains(whGot, "钉钉定时推送正常") {
		t.Fatalf("unexpected dingtalk webhook body: %s", whGot)
	}

	// 2. 钉钉 Stream 长连接 + SessionWebhook 回复
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	replyGot := make(chan string, 1)
	var apiSrv *httptest.Server
	apiSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/gateway/connections/open":
			wsEndpoint := "ws" + strings.TrimPrefix(apiSrv.URL, "http") + "/stream"
			_ = json.NewEncoder(w).Encode(map[string]any{"endpoint": wsEndpoint, "ticket": "dt-ticket-1"})
		case "/stream":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			callbackPayload, _ := json.Marshal(map[string]any{
				"msgId":            "dt-msg-1",
				"senderStaffId":    "staff-001",
				"senderNick":       "李四",
				"conversationId":   "cid-1",
				"conversationType": "1",
				"sessionWebhook":   apiSrv.URL + "/session-reply",
				"text":             map[string]any{"content": "钉钉消息测试"},
			})
			_ = conn.WriteJSON(map[string]any{
				"type":    "CALLBACK",
				"headers": map[string]string{"messageId": "hdr-1", "topic": "/v1.0/im/bot/messages/get"},
				"data":    string(callbackPayload),
			})
			_, _, _ = conn.ReadMessage()
			time.Sleep(200 * time.Millisecond)
		case "/session-reply":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			b, _ := json.Marshal(body)
			replyGot <- string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiSrv.Close()

	streamAdapter, err := DefaultAdapterFactory(apiSrv.Client())(
		store.RemoteChannel{
			ID:        uuid.New(),
			Platform:  domain.ChannelPlatformDingTalk,
			Role:      domain.ChannelRoleMainChat,
			Transport: domain.ChannelTransportStream,
			Config:    store.RemoteChannelConfig{BaseURL: apiSrv.URL, ClientID: "ding-cli-1"},
		},
		store.RemoteChannelSecrets{ClientSecret: "ding-sec-1"},
	)
	if err != nil {
		t.Fatalf("create dingtalk stream adapter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gotIn := make(chan InboundMessage, 1)
	go func() {
		_ = streamAdapter.Run(ctx, AdapterRuntime{
			OnInbound: func(_ context.Context, msg InboundMessage) error {
				gotIn <- msg
				return nil
			},
			OnStatus:  noopStatus,
			SaveState: noopSaveState,
		})
	}()

	select {
	case in := <-gotIn:
		if in.ExternalMessageID != "dt-msg-1" || in.Content != "钉钉消息测试" {
			t.Fatalf("unexpected dingtalk inbound: %+v", in)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for dingtalk stream inbound")
	}

	if err := streamAdapter.Send(context.Background(), OutboundMessage{Text: "钉钉回复成功"}); err != nil {
		t.Fatalf("dingtalk send: %v", err)
	}
	select {
	case raw := <-replyGot:
		if !strings.Contains(raw, "钉钉回复成功") {
			t.Fatalf("unexpected dingtalk session reply: %s", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for dingtalk reply")
	}
}

func TestAdapter_QQ_GatewayAndReply(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	sentC2C := make(chan string, 1)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "qq-tok-1", "expires_in": "7200"})
		case "/gateway":
			_ = json.NewEncoder(w).Encode(map[string]any{"url": "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"})
		case "/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.WriteJSON(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 30000}})
			_, _, _ = conn.ReadMessage()
			_ = conn.WriteJSON(map[string]any{"op": 0, "s": 1, "t": "READY", "d": map[string]any{"session_id": "qq-s1"}})
			_ = conn.WriteJSON(map[string]any{
				"op": 0, "s": 2, "t": "C2C_MESSAGE_CREATE",
				"d": map[string]any{
					"id":      "qq-msg-1",
					"content": "QQ单聊测试",
					"author":  map[string]any{"user_openid": "openid-u1"},
				},
			})
			time.Sleep(200 * time.Millisecond)
		case "/v2/users/openid-u1/messages":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			b, _ := json.Marshal(body)
			sentC2C <- string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "reply-1"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	adapter, err := DefaultAdapterFactory(srv.Client())(
		store.RemoteChannel{
			ID:        uuid.New(),
			Platform:  domain.ChannelPlatformQQ,
			Role:      domain.ChannelRoleMainChat,
			Transport: domain.ChannelTransportWebSocket,
			Config:    store.RemoteChannelConfig{BaseURL: srv.URL, AppID: "qq-app-1"},
		},
		store.RemoteChannelSecrets{Secret: "qq-sec-1"},
	)
	if err != nil {
		t.Fatalf("create qq adapter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gotIn := make(chan InboundMessage, 1)
	go func() {
		_ = adapter.Run(ctx, AdapterRuntime{
			OnInbound: func(_ context.Context, msg InboundMessage) error {
				gotIn <- msg
				return nil
			},
			OnStatus:  noopStatus,
			SaveState: noopSaveState,
		})
	}()

	select {
	case in := <-gotIn:
		if in.ExternalMessageID != "qq-msg-1" || in.SenderID != "openid-u1" || in.Content != "QQ单聊测试" {
			t.Fatalf("unexpected qq inbound: %+v", in)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for qq inbound")
	}

	if err := adapter.Send(context.Background(), OutboundMessage{Text: "QQ回复正常"}); err != nil {
		t.Fatalf("qq send: %v", err)
	}
	select {
	case raw := <-sentC2C:
		if !strings.Contains(raw, "QQ回复正常") {
			t.Fatalf("unexpected qq outbound: %s", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for qq outbound")
	}
}

func TestAdapter_Telegram_LongPollAndTopicSend(t *testing.T) {
	var (
		mu         sync.Mutex
		pollCount  int
		sentThread int64
		sentChatID string
		sentText   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"id": 1, "username": "cycledock_bot"}})
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			mu.Lock()
			pollCount++
			c := pollCount
			mu.Unlock()
			if c == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok": true,
					"result": []map[string]any{
						{
							"update_id": 1001,
							"message": map[string]any{
								"message_id":        55,
								"message_thread_id": 42,
								"text":              "@cycledock_bot 检查线上状态",
								"from":              map[string]any{"id": 9001, "username": "alice"},
								"chat":              map[string]any{"id": -100123456, "type": "supergroup"},
							},
						},
					},
				})
				return
			}
			time.Sleep(150 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var body struct {
				ChatID          string `json:"chat_id"`
				MessageThreadID int64  `json:"message_thread_id"`
				Text            string `json:"text"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			sentChatID = body.ChatID
			sentThread = body.MessageThreadID
			sentText = body.Text
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	adapter, err := DefaultAdapterFactory(srv.Client())(
		store.RemoteChannel{
			ID:        uuid.New(),
			Platform:  domain.ChannelPlatformTelegram,
			Role:      domain.ChannelRoleMainChat,
			Transport: domain.ChannelTransportLongPoll,
			Config:    store.RemoteChannelConfig{BaseURL: srv.URL, TargetChatID: "-100123456", MessageThreadID: 42},
		},
		store.RemoteChannelSecrets{BotToken: "123:tg-secret"},
	)
	if err != nil {
		t.Fatalf("create telegram adapter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gotIn := make(chan InboundMessage, 1)
	go func() {
		_ = adapter.Run(ctx, AdapterRuntime{
			OnInbound: func(_ context.Context, msg InboundMessage) error {
				gotIn <- msg
				cancel()
				return nil
			},
			OnStatus:  noopStatus,
			SaveState: noopSaveState,
		})
	}()

	select {
	case in := <-gotIn:
		if in.ChatType != "group" || !in.Mentioned || in.Content != "检查线上状态" || in.ThreadID != "42" {
			t.Fatalf("unexpected telegram inbound: %+v", in)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for telegram inbound")
	}

	if err := adapter.Send(context.Background(), OutboundMessage{Text: "线上全部健康"}); err != nil {
		t.Fatalf("telegram send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if sentChatID != "-100123456" || sentThread != 42 || !strings.Contains(sentText, "线上全部健康") {
		t.Fatalf("unexpected telegram send: chat=%s thread=%d text=%q", sentChatID, sentThread, sentText)
	}
}

func TestPolicyAllows_AllowFromAndGroupPolicy(t *testing.T) {
	ch := store.RemoteChannel{
		Role:        domain.ChannelRoleMainChat,
		GroupPolicy: domain.ChannelGroupPolicyMentionOnly,
		AllowFrom:   []string{"user-a", "user-b"},
	}
	if !PolicyAllows(ch, InboundMessage{SenderID: "user-a", ChatType: "direct", Content: "hi"}) {
		t.Fatal("expected direct message from allowlisted user-a allowed")
	}
	if PolicyAllows(ch, InboundMessage{SenderID: "user-c", ChatType: "direct", Content: "hi"}) {
		t.Fatal("expected direct message from user-c denied")
	}
	if PolicyAllows(ch, InboundMessage{SenderID: "user-a", ChatType: "group", Mentioned: false, Content: "hi"}) {
		t.Fatal("mention_only group message without mention must be rejected")
	}
	if !PolicyAllows(ch, InboundMessage{SenderID: "user-a", ChatType: "group", Mentioned: true, Content: "hi"}) {
		t.Fatal("mention_only group message with mention should be accepted")
	}
	ch.GroupPolicy = domain.ChannelGroupPolicyAllowlist
	if !PolicyAllows(ch, InboundMessage{SenderID: "user-a", ChatType: "group", Mentioned: false, Content: "hi"}) {
		t.Fatal("allowlist group policy should allow allowlisted sender even without mention")
	}
	ch.Role = domain.ChannelRoleScheduledPush
	if PolicyAllows(ch, InboundMessage{SenderID: "user-a", ChatType: "direct", Content: "hi"}) {
		t.Fatal("scheduled_push channel must never accept inbound messages")
	}
}
