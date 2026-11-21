package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestFlexTypes_DecodeNumberStringAndNull(t *testing.T) {
	t.Run("flexString", func(t *testing.T) {
		raw := `{
			"big_num": 8291047281920384712,
			"huge_uint": 18446744073709551615,
			"str": "wx-msg-1",
			"null_val": null,
			"bool_val": true
		}`
		var got struct {
			BigNum   flexString `json:"big_num"`
			HugeUint flexString `json:"huge_uint"`
			Str      flexString `json:"str"`
			NullVal  flexString `json:"null_val"`
			Omitted  flexString `json:"omitted"`
			BoolVal  flexString `json:"bool_val"`
		}
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("unmarshal flexString failed: %v", err)
		}
		if got.BigNum.String() != "8291047281920384712" {
			t.Fatalf("big_num lost precision: got %q", got.BigNum)
		}
		if got.HugeUint.String() != "18446744073709551615" {
			t.Fatalf("huge_uint lost precision: got %q", got.HugeUint)
		}
		if got.Str.String() != "wx-msg-1" || got.NullVal.String() != "" || got.Omitted.String() != "" || got.BoolVal.String() != "true" {
			t.Fatalf("unexpected flexString result: %+v", got)
		}

		var bad struct {
			Val flexString `json:"val"`
		}
		if err := json.Unmarshal([]byte(`{"val":{"nested":1}}`), &bad); err == nil {
			t.Fatal("expected error when decoding JSON object into flexString")
		}
	})

	t.Run("flexInt64", func(t *testing.T) {
		raw := `{
			"num": -14,
			"max64": 9223372036854775807,
			"str_num": "1730000000123",
			"str_neg": " -14 ",
			"str_float": "35000.0",
			"empty_str": "",
			"null_val": null
		}`
		var got struct {
			Num      flexInt64 `json:"num"`
			Max64    flexInt64 `json:"max64"`
			StrNum   flexInt64 `json:"str_num"`
			StrNeg   flexInt64 `json:"str_neg"`
			StrFloat flexInt64 `json:"str_float"`
			EmptyStr flexInt64 `json:"empty_str"`
			NullVal  flexInt64 `json:"null_val"`
			Omitted  flexInt64 `json:"omitted"`
		}
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("unmarshal flexInt64 failed: %v", err)
		}
		if got.Num != -14 || got.Max64 != 9223372036854775807 || got.StrNum != 1730000000123 ||
			got.StrNeg != -14 || got.StrFloat != 35000 || got.EmptyStr != 0 || got.NullVal != 0 || got.Omitted != 0 {
			t.Fatalf("unexpected flexInt64 result: %+v", got)
		}

		var bad struct {
			Val flexInt64 `json:"val"`
		}
		if err := json.Unmarshal([]byte(`{"val":"not-a-number"}`), &bad); err == nil {
			t.Fatal("expected error when decoding non-numeric string into flexInt64")
		}
	})
}

func TestWeixin_GetUpdates_DecodeNumericStringNullMessageIDAndItems(t *testing.T) {
	// 直接使用原始 JSON 文本模拟微信 iLink 生产返回：
	// message_id 为 64 位 JSON 数字、字符串、null、0、省略，同时混入引用消息与语音转文字。
	rawResp := `{
		"ret": "0",
		"errcode": null,
		"errmsg": "",
		"get_updates_buf": "cursor-prod-99",
		"longpolling_timeout_ms": 45000,
		"msgs": [
			{
				"message_id": 8291047281920384712,
				"seq": "101",
				"from_user_id": "wx-u1@im.wechat",
				"message_type": 1,
				"context_token": "ctx-u1",
				"create_time_ms": 1730000000123,
				"item_list": [
					{"type": 1, "text_item": {"text": "查看生产状态"}}
				]
			},
			{
				"message_id": "wx-str-id-2",
				"seq": 102,
				"from_user_id": "wx-u2@im.wechat",
				"message_type": "1",
				"context_token": "ctx-u2",
				"create_time_ms": "1730000000456",
				"item_list": [
					{
						"type": "1",
						"text_item": {"text": "我来处理"},
						"ref_msg": {
							"title": "告警机器人",
							"message_item": {
								"type": 1,
								"text_item": {"text": "微信通道断开"}
							}
						}
					}
				]
			},
			{
				"message_id": null,
				"seq": 202,
				"from_user_id": "wx-u3@im.wechat",
				"message_type": 1,
				"context_token": "ctx-u3",
				"create_time_ms": 1730000001000,
				"item_list": [
					{"type": 3, "voice_item": {"text": "帮我拉一下最新日志"}}
				]
			},
			{
				"from_user_id": "wx-u4@im.wechat",
				"message_type": 1,
				"create_time_ms": 1730000002000,
				"item_list": [
					{"type": 1, "text_item": {"text": "无 message_id 兜底"}}
				]
			},
			{
				"message_id": 0,
				"seq": 303,
				"from_user_id": "wx-u5@im.wechat",
				"message_type": 1,
				"item_list": [
					{"type": 1, "text_item": {"text": "零值 message_id 回退 seq"}}
				]
			},
			{
				"message_id": 999999999999,
				"from_user_id": "wx-bot",
				"message_type": 2,
				"item_list": [
					{"type": 1, "text_item": {"text": "机器人回显应忽略"}}
				]
			}
		]
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/getupdates" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rawResp))
	}))
	defer srv.Close()

	adapter := NewWeixinAdapter(
		store.RemoteChannel{
			ID:       uuid.New(),
			Platform: domain.ChannelPlatformWeixin,
			Config:   store.RemoteChannelConfig{BaseURL: srv.URL},
		},
		store.RemoteChannelSecrets{BotToken: "wx-token"},
		srv.Client(),
	)

	var inbound []InboundMessage
	var savedState store.RemoteChannelRuntimeState
	err := adapter.pollOnce(context.Background(), AdapterRuntime{
		OnInbound: func(_ context.Context, msg InboundMessage) error {
			inbound = append(inbound, msg)
			return nil
		},
		OnStatus: noopStatus,
		SaveState: func(_ context.Context, mutate func(*store.RemoteChannelRuntimeState)) error {
			mutate(&savedState)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("pollOnce failed: %v", err)
	}
	if len(inbound) != 5 {
		t.Fatalf("expected 5 inbound messages (bot echo skipped), got %d: %+v", len(inbound), inbound)
	}

	// 1. 64 位数字 message_id 完整保真
	if inbound[0].ExternalMessageID != "8291047281920384712" || inbound[0].Content != "查看生产状态" {
		t.Fatalf("msg[0] unexpected: %+v", inbound[0])
	}
	if inbound[0].Timestamp.UnixMilli() != 1730000000123 {
		t.Fatalf("msg[0] timestamp mismatch: %v", inbound[0].Timestamp)
	}

	// 2. 字符串 message_id + 引用消息 ref_msg
	wantRefContent := "[引用: 告警机器人 | 微信通道断开]\n我来处理"
	if inbound[1].ExternalMessageID != "wx-str-id-2" || inbound[1].Content != wantRefContent {
		t.Fatalf("msg[1] unexpected: id=%q content=%q", inbound[1].ExternalMessageID, inbound[1].Content)
	}

	// 3. null message_id 回退 seq + 语音转文字 type=3
	if inbound[2].ExternalMessageID != "202" || inbound[2].Content != "[语音] 帮我拉一下最新日志" {
		t.Fatalf("msg[2] unexpected: id=%q content=%q", inbound[2].ExternalMessageID, inbound[2].Content)
	}

	// 4. 省略 message_id 与 seq 时回退 from_user:create_time_ms
	if inbound[3].ExternalMessageID != "wx-u4@im.wechat:1730000002000" {
		t.Fatalf("msg[3] unexpected id: %q", inbound[3].ExternalMessageID)
	}

	// 5. 零值 message_id=0 时回退 seq 防止去重键碰撞
	if inbound[4].ExternalMessageID != "303" {
		t.Fatalf("msg[4] unexpected id: %q", inbound[4].ExternalMessageID)
	}

	// 验证 longpolling_timeout_ms 与游标持久化
	adapter.mu.Lock()
	gotPollTimeout := adapter.longPollTimeout
	gotTokenAt := adapter.contextTokenAt["wx-u1@im.wechat"]
	adapter.mu.Unlock()
	if gotPollTimeout != 45*time.Second {
		t.Fatalf("expected longPollTimeout=45s, got %v", gotPollTimeout)
	}
	if gotTokenAt.IsZero() {
		t.Fatal("expected contextTokenAt recorded for wx-u1@im.wechat")
	}
	if savedState.WeixinUpdatesBuf != "cursor-prod-99" || savedState.WeixinContextTokens["wx-u1@im.wechat"] != "ctx-u1" {
		t.Fatalf("unexpected persisted state: %+v", savedState)
	}
}

func TestWeixin_LongPollingTimeout_ClampAndClientTimeoutSafety(t *testing.T) {
	var hit atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hit.Add(1)
		switch n {
		case 1:
			// 模拟服务端空闲长轮询耗时超过外部 client 的 40ms Timeout，验证不会误判断开。
			time.Sleep(100 * time.Millisecond)
			_, _ = w.Write([]byte(`{"ret":0,"longpolling_timeout_ms":"2000","msgs":[]}`))
		case 2:
			_, _ = w.Write([]byte(`{"ret":0,"longpolling_timeout_ms":999999,"msgs":[]}`))
		default:
			_, _ = w.Write([]byte(`{"ret":0,"longpolling_timeout_ms":55000,"msgs":[]}`))
		}
	}))
	defer srv.Close()

	shortClient := srv.Client()
	shortClient.Timeout = 40 * time.Millisecond

	adapter := NewWeixinAdapter(
		store.RemoteChannel{
			ID:       uuid.New(),
			Platform: domain.ChannelPlatformWeixin,
			Config:   store.RemoteChannelConfig{BaseURL: srv.URL},
		},
		store.RemoteChannelSecrets{BotToken: "wx-token"},
		shortClient,
	)

	// 第 1 次：长轮询耗时 100ms，不能被 shortClient.Timeout(40ms) 误杀，且返回 2000ms 应夹紧到 5s。
	if err := adapter.pollOnce(context.Background(), AdapterRuntime{OnStatus: noopStatus}); err != nil {
		t.Fatalf("pollOnce #1 should not fail due to short client timeout: %v", err)
	}
	if shortClient.Timeout != 40*time.Millisecond {
		t.Fatalf("caller http.Client.Timeout was unexpectedly mutated: %v", shortClient.Timeout)
	}
	if adapter.longPollTimeout != minWeixinLongPollTimeout {
		t.Fatalf("expected min clamp %v, got %v", minWeixinLongPollTimeout, adapter.longPollTimeout)
	}

	// 第 2 次：超大 999999ms 应夹紧到 maxWeixinLongPollTimeout (120s)。
	if err := adapter.pollOnce(context.Background(), AdapterRuntime{OnStatus: noopStatus}); err != nil {
		t.Fatalf("pollOnce #2 failed: %v", err)
	}
	if adapter.longPollTimeout != maxWeixinLongPollTimeout {
		t.Fatalf("expected max clamp %v, got %v", maxWeixinLongPollTimeout, adapter.longPollTimeout)
	}

	// 第 3 次：正常 55000ms 应精确应用为 55s。
	if err := adapter.pollOnce(context.Background(), AdapterRuntime{OnStatus: noopStatus}); err != nil {
		t.Fatalf("pollOnce #3 failed: %v", err)
	}
	if adapter.longPollTimeout != 55*time.Second {
		t.Fatalf("expected 55s, got %v", adapter.longPollTimeout)
	}
}

func TestWeixin_ContextTokenRefreshViaGetConfig(t *testing.T) {
	t.Run("fresh_token_skips_getconfig", func(t *testing.T) {
		var getConfigHits atomic.Int32
		var sentToken string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/ilink/bot/getconfig":
				getConfigHits.Add(1)
				_, _ = w.Write([]byte(`{"ret":0,"context_token":"should-not-use"}`))
			case "/ilink/bot/sendmessage":
				var body struct {
					Msg struct {
						ContextToken string `json:"context_token"`
					} `json:"msg"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				sentToken = body.Msg.ContextToken
				_, _ = w.Write([]byte(`{"ret":0}`))
			}
		}))
		defer srv.Close()

		adapter := NewWeixinAdapter(
			store.RemoteChannel{ID: uuid.New(), Platform: domain.ChannelPlatformWeixin, Config: store.RemoteChannelConfig{BaseURL: srv.URL}},
			store.RemoteChannelSecrets{BotToken: "wx-token"},
			srv.Client(),
		)
		adapter.contextTokens["wx-u1"] = "ctx-fresh"
		adapter.contextTokenAt["wx-u1"] = time.Now()

		if err := adapter.Send(context.Background(), OutboundMessage{ChatID: "wx-u1", Text: "hello"}); err != nil {
			t.Fatalf("Send failed: %v", err)
		}
		if getConfigHits.Load() != 0 {
			t.Fatalf("expected 0 getconfig calls for fresh token, got %d", getConfigHits.Load())
		}
		if sentToken != "ctx-fresh" {
			t.Fatalf("expected ctx-fresh, got %q", sentToken)
		}
	})

	t.Run("stale_token_refreshes_and_persists", func(t *testing.T) {
		var (
			mu             sync.Mutex
			getConfigHits  int
			getConfigReq   map[string]any
			sentTokens     []string
			persistedToken string
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/ilink/bot/getconfig":
				var req map[string]any
				_ = json.NewDecoder(r.Body).Decode(&req)
				mu.Lock()
				getConfigHits++
				getConfigReq = req
				mu.Unlock()
				_, _ = w.Write([]byte(`{"ret":0,"errcode":0,"context_token":"ctx-refreshed-2"}`))
			case "/ilink/bot/sendmessage":
				var body struct {
					Msg struct {
						ContextToken string `json:"context_token"`
					} `json:"msg"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				mu.Lock()
				sentTokens = append(sentTokens, body.Msg.ContextToken)
				mu.Unlock()
				_, _ = w.Write([]byte(`{"ret":"0","errcode":null}`))
			}
		}))
		defer srv.Close()

		adapter := NewWeixinAdapter(
			store.RemoteChannel{ID: uuid.New(), Platform: domain.ChannelPlatformWeixin, Config: store.RemoteChannelConfig{BaseURL: srv.URL}},
			store.RemoteChannelSecrets{BotToken: "wx-token"},
			srv.Client(),
		)
		adapter.contextTokens["wx-u1"] = "ctx-stale-1"
		adapter.contextTokenAt["wx-u1"] = time.Now().Add(-90 * time.Second)
		adapter.saveState = func(_ context.Context, mutate func(*store.RemoteChannelRuntimeState)) error {
			var st store.RemoteChannelRuntimeState
			mutate(&st)
			mu.Lock()
			persistedToken = st.WeixinContextTokens["wx-u1"]
			mu.Unlock()
			return nil
		}

		// 第一次发送：超过 60s，应调用 getconfig 刷新并用新 token 发送。
		if err := adapter.Send(context.Background(), OutboundMessage{ChatID: "wx-u1", ContextToken: "ctx-stale-1", Text: "first"}); err != nil {
			t.Fatalf("first Send failed: %v", err)
		}
		// 紧接着第二次发送：刚刷新过，应直接复用新 token 不重复调用 getconfig。
		if err := adapter.Send(context.Background(), OutboundMessage{ChatID: "wx-u1", ContextToken: "ctx-stale-1", Text: "second"}); err != nil {
			t.Fatalf("second Send failed: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()
		if getConfigHits != 1 {
			t.Fatalf("expected exactly 1 getconfig call, got %d", getConfigHits)
		}
		if getConfigReq["ilink_user_id"] != "wx-u1" || getConfigReq["context_token"] != "ctx-stale-1" || getConfigReq["base_info"] == nil {
			t.Fatalf("unexpected getconfig payload: %+v", getConfigReq)
		}
		if len(sentTokens) != 2 || sentTokens[0] != "ctx-refreshed-2" || sentTokens[1] != "ctx-refreshed-2" {
			t.Fatalf("expected both sends to use refreshed token, got %v", sentTokens)
		}
		if persistedToken != "ctx-refreshed-2" {
			t.Fatalf("expected refreshed token persisted, got %q", persistedToken)
		}
	})

	t.Run("getconfig_failure_falls_back_to_old_token", func(t *testing.T) {
		var getConfigHits atomic.Int32
		var sentToken string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/ilink/bot/getconfig":
				getConfigHits.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`upstream error`))
			case "/ilink/bot/sendmessage":
				var body struct {
					Msg struct {
						ContextToken string `json:"context_token"`
					} `json:"msg"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				sentToken = body.Msg.ContextToken
				_, _ = w.Write([]byte(`{"ret":0}`))
			}
		}))
		defer srv.Close()

		adapter := NewWeixinAdapter(
			store.RemoteChannel{ID: uuid.New(), Platform: domain.ChannelPlatformWeixin, Config: store.RemoteChannelConfig{BaseURL: srv.URL}},
			store.RemoteChannelSecrets{BotToken: "wx-token"},
			srv.Client(),
		)
		adapter.contextTokens["wx-u1"] = "ctx-old"
		adapter.contextTokenAt["wx-u1"] = time.Now().Add(-2 * time.Minute)

		if err := adapter.Send(context.Background(), OutboundMessage{ChatID: "wx-u1", Text: "fallback test"}); err != nil {
			t.Fatalf("Send should fall back to old token instead of failing: %v", err)
		}
		if getConfigHits.Load() != 1 {
			t.Fatalf("expected 1 getconfig attempt, got %d", getConfigHits.Load())
		}
		if sentToken != "ctx-old" {
			t.Fatalf("expected fallback token ctx-old, got %q", sentToken)
		}
	})
}

func TestOtherChannels_FlexJSONCompatibility(t *testing.T) {
	t.Run("wecom_numeric_ids_and_chattype", func(t *testing.T) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			_, _, _ = conn.ReadMessage() // subscribe frame
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{
				"cmd":"aibot_msg_callback",
				"errcode":"0",
				"headers":{"req_id":99001},
				"body":{
					"msgid":7788990011223344,
					"chatid":556677,
					"chattype":2,
					"from":{"userid":123456},
					"text":{"content":"企微数字ID测试"}
				}
			}`))
			time.Sleep(150 * time.Millisecond)
		}))
		defer wsSrv.Close()

		adapter := NewWeComAdapter(
			store.RemoteChannel{
				ID:        uuid.New(),
				Platform:  domain.ChannelPlatformWeCom,
				Transport: domain.ChannelTransportWebSocket,
				Config:    store.RemoteChannelConfig{WSURL: "ws" + strings.TrimPrefix(wsSrv.URL, "http"), BotID: "b1"},
			},
			store.RemoteChannelSecrets{Secret: "sec"},
			nil,
		)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		got := make(chan InboundMessage, 1)
		go func() {
			_ = adapter.Run(ctx, AdapterRuntime{
				OnInbound: func(_ context.Context, msg InboundMessage) error {
					got <- msg
					return nil
				},
				OnStatus:  noopStatus,
				SaveState: noopSaveState,
			})
		}()
		select {
		case msg := <-got:
			if msg.ExternalMessageID != "7788990011223344" || msg.ChatID != "556677" || msg.ChatType != "group" || msg.SenderID != "123456" {
				t.Fatalf("unexpected wecom inbound: %+v", msg)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for wecom inbound")
		}
	})

	t.Run("dingtalk_numeric_conversation_type_and_ids", func(t *testing.T) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			inner := `{"msgId":880011223344,"conversationId":998877,"conversationType":2,"senderStaffId":445566,"isInAtList":true,"text":{"content":"钉钉数字类型测试"}}`
			frame := map[string]any{
				"specVersion": "1.0",
				"type":        "CALLBACK",
				"headers":     map[string]any{"topic": "/v1.0/im/bot/messages/get", "messageId": 667788},
				"data":        inner,
			}
			_ = conn.WriteJSON(frame)
			_, _, _ = conn.ReadMessage() // ACK
			time.Sleep(100 * time.Millisecond)
		}))
		defer wsSrv.Close()

		adapter := NewDingTalkAdapter(
			store.RemoteChannel{
				ID:        uuid.New(),
				Platform:  domain.ChannelPlatformDingTalk,
				Transport: domain.ChannelTransportStream,
				Config:    store.RemoteChannelConfig{WSURL: "ws" + strings.TrimPrefix(wsSrv.URL, "http"), ClientID: "cid"},
			},
			store.RemoteChannelSecrets{ClientSecret: "csec"},
			nil,
		)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		got := make(chan InboundMessage, 1)
		go func() {
			_ = adapter.Run(ctx, AdapterRuntime{
				OnInbound: func(_ context.Context, msg InboundMessage) error {
					got <- msg
					return nil
				},
				OnStatus:  noopStatus,
				SaveState: noopSaveState,
			})
		}()
		select {
		case msg := <-got:
			if msg.ExternalMessageID != "880011223344" || msg.ChatID != "998877" || msg.ChatType != "group" || msg.SenderID != "445566" {
				t.Fatalf("unexpected dingtalk inbound: %+v", msg)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for dingtalk inbound")
		}
	})

	t.Run("telegram_string_numeric_fields", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/getUpdates") {
				_, _ = w.Write([]byte(`{
					"ok": true,
					"result": [{
						"update_id": "1001",
						"message": {
							"message_id": "2002",
							"message_thread_id": "33",
							"date": "1730000000",
							"text": "telegram 兼容测试",
							"from": {"id": "4004", "is_bot": false, "username": "alice"},
							"chat": {"id": "-100998877", "type": "supergroup"}
						}
					}]
				}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		adapter, err := NewTelegramAdapter(
			store.RemoteChannel{
				ID:       uuid.New(),
				Platform: domain.ChannelPlatformTelegram,
				Config:   store.RemoteChannelConfig{BaseURL: srv.URL},
			},
			store.RemoteChannelSecrets{BotToken: "tg-tok"},
			srv.Client(),
		)
		if err != nil {
			t.Fatalf("NewTelegramAdapter failed: %v", err)
		}
		var got []InboundMessage
		if err := adapter.pollOnce(context.Background(), AdapterRuntime{
			OnInbound: func(_ context.Context, msg InboundMessage) error {
				got = append(got, msg)
				return nil
			},
			OnStatus:  noopStatus,
			SaveState: noopSaveState,
		}, "mybot"); err != nil {
			t.Fatalf("telegram pollOnce failed: %v", err)
		}
		if len(got) != 1 || got[0].ExternalMessageID != "-100998877:2002" || got[0].SenderID != "4004" || got[0].ThreadID != "33" {
			t.Fatalf("unexpected telegram inbound: %+v", got)
		}
	})
}
