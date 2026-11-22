package channels

import (
	"context"
	"encoding/json"
	"errors"
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

	wait1 := d.recordChannelFailure(ch)
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
	wait2 := d.recordChannelFailure(ch)
	if wait2 != 30*time.Second {
		t.Fatalf("second backoff should be 30s, got %v", wait2)
	}
	t2 := now.Add(1 * time.Second)
	ch.LastInboundAt = &t2
	if d.shouldSkipByBackoff(ch) {
		t.Fatal("fresh inbound message should immediately reset channel backoff")
	}
}
