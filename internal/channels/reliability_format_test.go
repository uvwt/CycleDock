package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestMaskRemoteIdentifier(t *testing.T) {
	if got := MaskRemoteIdentifier(""); got != "" {
		t.Fatalf("empty identifier should return empty, got %q", got)
	}
	if got := MaskRemoteIdentifier("abc"); got != "***" {
		t.Fatalf("short identifier should be masked as ***, got %q", got)
	}
	if got := MaskRemoteIdentifier("o9cq80_user_12345@im.wechat"); got != "o9c***hat" {
		t.Fatalf("long identifier mask mismatch, got %q", got)
	}
}

func TestFormatOutboundForPlatform_AllSixPlatforms(t *testing.T) {
	sample := "# 今日巡检\n**状态**：`正常`\n- [详情](https://example.com/a)\n- [工作卡](cycledock://card/11111111-1111-1111-1111-111111111111)\n```go\nfmt.Println(\"ok\")\n```"

	// 微信 / QQ 纯文本清洗：去掉 Markdown 语法符号，保留可读内容与纯 URL。
	wx := FormatOutboundForPlatform(domain.ChannelPlatformWeixin, sample, "")
	if strings.Contains(wx, "**") || strings.Contains(wx, "```") || strings.Contains(wx, "cycledock://") {
		t.Fatalf("weixin plain text still contains raw markdown/internal link: %q", wx)
	}
	if !strings.Contains(wx, "「工作卡」") || !strings.Contains(wx, "https://example.com/a") {
		t.Fatalf("weixin plain text lost key info: %q", wx)
	}

	qq := FormatOutboundForPlatform(domain.ChannelPlatformQQ, sample, "")
	if qq != wx {
		t.Fatalf("qq plain text should match weixin plain text; qq=%q wx=%q", qq, wx)
	}

	// Telegram HTML：转义 HTML 特殊字符，转换加粗/行内代码/代码块/链接。
	tg := FormatOutboundForPlatform(domain.ChannelPlatformTelegram, "**高危** `<script>` [文档](https://example.com/doc)", "")
	tgHTML := MarkdownToTelegramHTML(tg)
	if !strings.Contains(tgHTML, "<b>高危</b>") ||
		!strings.Contains(tgHTML, "<code>&lt;script&gt;</code>") ||
		!strings.Contains(tgHTML, `<a href="https://example.com/doc">文档</a>`) {
		t.Fatalf("telegram html conversion unexpected: %q", tgHTML)
	}

	// 飞书 / 钉钉 / 企微保留轻量 Markdown，清洗 cycledock:// 链接。
	for _, plat := range []domain.ChannelPlatform{
		domain.ChannelPlatformFeishu,
		domain.ChannelPlatformDingTalk,
		domain.ChannelPlatformWeCom,
	} {
		out := FormatOutboundForPlatform(plat, sample, "https://cd.example.com/cards/p/c")
		if strings.Contains(out, "cycledock://") {
			t.Fatalf("platform %s leaked cycledock:// link: %q", plat, out)
		}
		if !strings.Contains(out, "https://cd.example.com/cards/p/c") {
			t.Fatalf("platform %s missing card URL when provided: %q", plat, out)
		}
	}
}

func TestWeixinAdapter_PrepareFailedRefreshesContextTokenAndRetries(t *testing.T) {
	var sendCalls atomic.Int32
	var getConfigCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ilink/bot/getconfig":
			getConfigCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ret":           0,
				"context_token": "refreshed-ctx-token-002",
			})
		case "/ilink/bot/sendmessage":
			n := sendCalls.Add(1)
			var req struct {
				Msg struct {
					ContextToken string `json:"context_token"`
				} `json:"msg"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if n == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ret":    -2,
					"errmsg": "prepare failed",
				})
				return
			}
			if req.Msg.ContextToken != "refreshed-ctx-token-002" {
				t.Errorf("expected retry with refreshed context_token, got %q", req.Msg.ContextToken)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ch := store.RemoteChannel{
		ID:       uuid.New(),
		Platform: domain.ChannelPlatformWeixin,
		Config:   store.RemoteChannelConfig{BaseURL: srv.URL, TargetChatID: "wx-user-1"},
		RuntimeState: store.RemoteChannelRuntimeState{
			LastChatID:          "wx-user-1",
			WeixinContextTokens: map[string]string{"wx-user-1": "stale-ctx-token-001"},
		},
	}
	adapter := NewWeixinAdapter(ch, store.RemoteChannelSecrets{BotToken: "wx-bot-tok"}, srv.Client())
	// 预先将缓存时间设为刚刚获取，以验证首次 sendmessage 遇到 ret=-2 时仍会强制调用 getconfig 刷新。
	adapter.mu.Lock()
	adapter.contextTokenAt["wx-user-1"] = time.Now()
	adapter.mu.Unlock()

	if err := adapter.Send(context.Background(), OutboundMessage{Text: "**已修复** 微信推送"}); err != nil {
		t.Fatalf("weixin Send expected retry success after ret=-2 refresh, got: %v", err)
	}
	if sendCalls.Load() != 2 || getConfigCalls.Load() != 1 {
		t.Fatalf("expected 2 send calls and 1 forced getconfig, got send=%d getconfig=%d", sendCalls.Load(), getConfigCalls.Load())
	}
}

func TestTelegramAdapter_HTMLParseErrorFallsBackToPlainText(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		n := calls.Add(1)
		if n == 1 {
			if body["parse_mode"] != "HTML" {
				t.Errorf("first attempt should use HTML parse_mode, got %v", body["parse_mode"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":          false,
				"description": "Bad Request: can't parse entities: Unsupported start tag",
			})
			return
		}
		if _, hasParseMode := body["parse_mode"]; hasParseMode {
			t.Errorf("fallback attempt must omit parse_mode, got %v", body["parse_mode"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	ch := store.RemoteChannel{
		ID:       uuid.New(),
		Platform: domain.ChannelPlatformTelegram,
		Config:   store.RemoteChannelConfig{BaseURL: srv.URL, TargetChatID: "123456"},
	}
	adapter, err := NewTelegramAdapter(ch, store.RemoteChannelSecrets{BotToken: "tg-tok"}, srv.Client())
	if err != nil {
		t.Fatalf("new telegram adapter: %v", err)
	}
	if err := adapter.Send(context.Background(), OutboundMessage{Text: "**加粗** 内容"}); err != nil {
		t.Fatalf("telegram Send fallback failed: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 telegram sendMessage calls (HTML + plain fallback), got %d", calls.Load())
	}
}

func TestFeishuAdapter_InteractiveCardFallsBackToText(t *testing.T) {
	var msgTypes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"tenant_access_token": "t-feishu-tok",
				"expire":              7200,
			})
		case strings.HasSuffix(r.URL.Path, "/im/v1/messages"):
			var body struct {
				MsgType string `json:"msg_type"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			msgTypes = append(msgTypes, body.MsgType)
			if body.MsgType == "interactive" {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 230001, "msg": "invalid card"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ch := store.RemoteChannel{
		ID:        uuid.New(),
		Platform:  domain.ChannelPlatformFeishu,
		Transport: domain.ChannelTransportWebSocket,
		Config:    store.RemoteChannelConfig{AppID: "cli_1", BaseURL: srv.URL, TargetChatID: "oc_group_1"},
	}
	adapter := NewFeishuAdapter(ch, store.RemoteChannelSecrets{AppSecret: "sec_1"}, srv.Client())
	if err := adapter.Send(context.Background(), OutboundMessage{Text: "**飞书卡片** 测试"}); err != nil {
		t.Fatalf("feishu Send fallback failed: %v", err)
	}
	if len(msgTypes) != 2 || msgTypes[0] != "interactive" || msgTypes[1] != "text" {
		t.Fatalf("expected [interactive, text], got %v", msgTypes)
	}
}

func TestAdapters_ReturnErrTargetNotBoundWhenChatEmpty(t *testing.T) {
	ctx := context.Background()
	wx := NewWeixinAdapter(store.RemoteChannel{Platform: domain.ChannelPlatformWeixin}, store.RemoteChannelSecrets{BotToken: "tok"}, nil)
	if err := wx.Send(ctx, OutboundMessage{Text: "hi"}); !errors.Is(err, ErrTargetNotBound) {
		t.Fatalf("weixin empty target should wrap ErrTargetNotBound, got %v", err)
	}
	tg, err := NewTelegramAdapter(store.RemoteChannel{Platform: domain.ChannelPlatformTelegram}, store.RemoteChannelSecrets{BotToken: "tok"}, nil)
	if err != nil {
		t.Fatalf("new telegram adapter: %v", err)
	}
	if err := tg.Send(ctx, OutboundMessage{Text: "hi"}); !errors.Is(err, ErrTargetNotBound) {
		t.Fatalf("telegram empty target should wrap ErrTargetNotBound, got %v", err)
	}
	dt := NewDingTalkAdapter(store.RemoteChannel{Platform: domain.ChannelPlatformDingTalk, Transport: domain.ChannelTransportStream}, store.RemoteChannelSecrets{ClientSecret: "sec"}, nil)
	if err := dt.Send(ctx, OutboundMessage{Text: "hi"}); !errors.Is(err, ErrTargetNotBound) {
		t.Fatalf("dingtalk empty target should wrap ErrTargetNotBound, got %v", err)
	}
}

func TestDispatcher_BackoffAndResetOnNewInbound(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	d := NewDispatcher(nil, nil, nil, "")
	d.SetNowFunc(func() time.Time { return now })

	t1 := now.Add(-1 * time.Minute)
	ch := store.RemoteChannel{
		ID:            uuid.New(),
		LastInboundAt: &t1,
	}

	wait1 := d.recordChannelFailure(ch, errors.New("network timeout"))
	if wait1 != 10*time.Second {
		t.Fatalf("first backoff should be 10s, got %v", wait1)
	}
	if !d.shouldSkipByBackoff(ch) {
		t.Fatal("channel should be skipped during backoff window")
	}

	// 时间前进 11 秒后解除退避。
	now = now.Add(11 * time.Second)
	if d.shouldSkipByBackoff(ch) {
		t.Fatal("channel should resume after backoff window elapsed")
	}

	// 第二次失败退避升至 30 秒，但收到新的入站消息后立即解除退避。
	wait2 := d.recordChannelFailure(ch, errors.New("network timeout"))
	if wait2 != 30*time.Second {
		t.Fatalf("second backoff should be 30s, got %v", wait2)
	}
	t2 := now.Add(1 * time.Second)
	ch.LastInboundAt = &t2
	if d.shouldSkipByBackoff(ch) {
		t.Fatal("fresh inbound message should immediately reset channel backoff")
	}
}

func TestDispatcher_WeixinThrottleBackoffAndPrioritizeOwnConversation(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	d := NewDispatcher(nil, nil, nil, "")
	d.SetNowFunc(func() time.Time { return now })

	t1 := now.Add(-1 * time.Minute)
	ch := store.RemoteChannel{
		ID:            uuid.New(),
		Platform:      domain.ChannelPlatformWeixin,
		LastInboundAt: &t1,
	}

	// 命中微信 ret=-2 限流时，应直接进入 15m -> 30m -> 60m 长冷却，防止 5 分钟一次探测续期惩罚。
	throttleErr := fmt.Errorf("%w: weixin sendmessage failed (ret=-2 errcode=0): prepare failed", ErrWeixinSendThrottled)
	if wait := d.recordChannelFailure(ch, throttleErr); wait != 15*time.Minute {
		t.Fatalf("first weixin throttle backoff = %v, want 15m", wait)
	}
	if wait := d.recordChannelFailure(ch, throttleErr); wait != 30*time.Minute {
		t.Fatalf("second weixin throttle backoff = %v, want 30m", wait)
	}
	if wait := d.recordChannelFailure(ch, throttleErr); wait != 60*time.Minute {
		t.Fatalf("third weixin throttle backoff = %v, want 60m", wait)
	}
	if wait := d.recordChannelFailure(ch, throttleErr); wait != 60*time.Minute {
		t.Fatalf("fourth weixin throttle backoff should cap at 60m, got %v", wait)
	}

	// 收到用户新入站消息后仍可立即解除退避，保证交互回复及时性。
	t2 := now.Add(5 * time.Second)
	ch.LastInboundAt = &t2
	if d.shouldSkipByBackoff(ch) {
		t.Fatal("fresh inbound message should reset weixin throttle backoff")
	}

	// 验证专属对话卡永远排在项目主聊天与历史子任务之前。
	ownID := uuid.New()
	mainID := uuid.New()
	todoID := uuid.New()
	ch.ConversationCardID = uuid.NullUUID{UUID: ownID, Valid: true}
	ordered := prioritizeMainChatCards(ch, []cardSummary{
		{ID: mainID, IsMainChat: true},
		{ID: todoID, Kind: domain.CardKindTodo},
		{ID: ownID, Kind: domain.CardKindDiscussion},
	})
	if len(ordered) != 3 || ordered[0].ID != ownID || ordered[1].ID != mainID || ordered[2].ID != todoID {
		t.Fatalf("unexpected prioritized card order: %+v", ordered)
	}
}

func TestWeixinAdapter_ProactiveQuotaProtectsRepliesAndWrapsThrottleError(t *testing.T) {
	var sendCalls atomic.Int32
	var returnThrottle atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ilink/bot/getconfig":
			// 服务端不返回新 context_token，模拟生产常见情况。
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
		case "/ilink/bot/sendmessage":
			sendCalls.Add(1)
			if returnThrottle.Load() {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ret":     -2,
					"errcode": 0,
					"errmsg":  "prepare failed",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
		}
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	ch := store.RemoteChannel{
		ID:       uuid.New(),
		Platform: domain.ChannelPlatformWeixin,
		Config:   store.RemoteChannelConfig{BaseURL: srv.URL, TargetChatID: "wx-u1@im.wechat"},
		RuntimeState: store.RemoteChannelRuntimeState{
			WeixinContextTokens: map[string]string{"wx-u1@im.wechat": "ctx-1"},
		},
	}
	adapter := NewWeixinAdapter(ch, store.RemoteChannelSecrets{BotToken: "wx-tok"}, srv.Client())
	adapter.chunkDelay = 0
	adapter.nowFn = func() time.Time { return now }
	adapter.mu.Lock()
	adapter.contextTokenAt["wx-u1@im.wechat"] = now
	adapter.mu.Unlock()

	// 1. 主动推送在 24h 安全配额（4 条）内全部放行。
	for i := 0; i < defaultWeixinProactivePushLimit; i++ {
		if err := adapter.Send(context.Background(), OutboundMessage{Text: "主动通知", Proactive: true}); err != nil {
			t.Fatalf("proactive send #%d unexpected error: %v", i+1, err)
		}
	}
	// 2. 第 5 条主动推送命中本地安全配额，快速跳过且不调用网关 sendmessage。
	callsBefore := sendCalls.Load()
	err := adapter.Send(context.Background(), OutboundMessage{Text: "超配额主动通知", Proactive: true})
	if !errors.Is(err, ErrWeixinProactiveQuotaExceeded) {
		t.Fatalf("expected ErrWeixinProactiveQuotaExceeded, got %v", err)
	}
	if sendCalls.Load() != callsBefore {
		t.Fatalf("quota-blocked proactive send should not call gateway sendmessage")
	}
	// 3. 即使主动推送配额已满，用户实时对话回复（Proactive=false）仍不受限制正常发送。
	if err := adapter.Send(context.Background(), OutboundMessage{Text: "你好，实时回复", Proactive: false}); err != nil {
		t.Fatalf("interactive reply must bypass proactive quota, got: %v", err)
	}
	// 4. 24h 窗口滑过后，主动推送配额自动恢复。
	now = now.Add(24*time.Hour + time.Minute)
	if err := adapter.Send(context.Background(), OutboundMessage{Text: "次日主动通知", Proactive: true}); err != nil {
		t.Fatalf("proactive quota should recover after 24h window, got: %v", err)
	}
	// 5. 网关返回 ret=-2 且 getconfig 无新 token 时，应包装 ErrWeixinSendThrottled 且只调用一次 sendmessage。
	returnThrottle.Store(true)
	callsBefore = sendCalls.Load()
	err = adapter.Send(context.Background(), OutboundMessage{Text: "触发限流", Proactive: false})
	if !errors.Is(err, ErrWeixinSendThrottled) {
		t.Fatalf("expected ErrWeixinSendThrottled on ret=-2, got %v", err)
	}
	if sendCalls.Load() != callsBefore+1 {
		t.Fatalf("ret=-2 without refreshed token must fail fast with 1 sendmessage call, got %d", sendCalls.Load()-callsBefore)
	}
	if !strings.Contains(err.Error(), "has_context_token=true") {
		t.Fatalf("expected diagnostic summary in error, got %v", err)
	}
}

func TestWeixinAdapter_RunCallsNotifyStartAndStop(t *testing.T) {
	var startCalls, stopCalls, pollCalls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ilink/bot/msg/notifystart":
			startCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
		case "/ilink/bot/msg/notifystop":
			stopCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
		case "/ilink/bot/getupdates":
			pollCalls.Add(1)
			cancel()
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0, "msgs": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	adapter := NewWeixinAdapter(
		store.RemoteChannel{
			ID:       uuid.New(),
			Platform: domain.ChannelPlatformWeixin,
			Config:   store.RemoteChannelConfig{BaseURL: srv.URL},
		},
		store.RemoteChannelSecrets{BotToken: "wx-tok"},
		srv.Client(),
	)
	_ = adapter.Run(ctx, AdapterRuntime{})
	if startCalls.Load() != 1 || stopCalls.Load() != 1 || pollCalls.Load() != 1 {
		t.Fatalf("expected notifystart=1 notifystop=1 getupdates=1, got start=%d stop=%d poll=%d",
			startCalls.Load(), stopCalls.Load(), pollCalls.Load())
	}
}
