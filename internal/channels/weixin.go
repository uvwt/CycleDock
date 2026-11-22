package channels

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	defaultWeixinBaseURL   = "https://ilinkai.weixin.qq.com"
	weixinChannelVersion   = "2.4.10"
	weixinAppID            = "bot"
	weixinClientVersionInt = (2 << 16) | (4 << 8) | 10
	weixinItemText         = 1
	weixinItemImage        = 2
	weixinItemVoice        = 3
	weixinItemFile         = 4
	weixinItemVideo        = 5
	weixinMsgTypeBot       = 2
	weixinMsgStateFinish   = 2
	weixinMaxRunes         = 1800

	// 微信 iLink 长轮询与接口超时约定：
	// 服务端默认长轮询约 35s，并可能通过 longpolling_timeout_ms 动态调整，
	// 客户端 HTTP 超时需始终大于服务端长轮询时长，避免空闲长轮询被误判断开重连。
	defaultWeixinLongPollTimeout = 35 * time.Second
	minWeixinLongPollTimeout     = 5 * time.Second
	maxWeixinLongPollTimeout     = 120 * time.Second
	weixinLongPollTimeoutBuffer  = 15 * time.Second
	defaultWeixinAPITimeout      = 15 * time.Second
	defaultWeixinConfigTimeout   = 10 * time.Second

	// iLink context_token 在服务端空闲约 90~160s 后可能过期，
	// 出站发送前若缓存已超过 60s，先静默调用 getconfig 刷新，失败则回退旧 token。
	weixinContextTokenRefreshAge = 60 * time.Second
	// 多片消息连续调用 ilink/bot/sendmessage 时保留短间隔，降低服务端 ret=-2 prepare failed 概率。
	defaultWeixinChunkDelay = 200 * time.Millisecond
	// 微信 iLink 对“主动推送”存在账号级 24h 突发阈值（实测约 5~6 条独立消息即触发 ret=-2 惩罚），
	// 而交互式回复（用户发消息后立即回复）不受该阈值限制。
	// 本地为主动推送预留安全配额（每 24h 最多 4 条），避免定时/主聊天镜像把正常微信聊天通道打进限流惩罚。
	defaultWeixinProactivePushLimit  = 4
	defaultWeixinProactivePushWindow = 24 * time.Hour
)

// ErrWeixinSendThrottled 表示微信 iLink 网关返回 ret=-2 (prepare failed) 或触发本地主动推送限流保护。
// 实测 ret=-2 涵盖账号级主动推送限流惩罚与上下文失效，且惩罚期内任何持续探测都会拉长冷却窗口，
// 因此调用方必须立即中止本轮同通道其余发送并进入长冷却，严禁短间隔连打重试。
var ErrWeixinSendThrottled = errors.New("weixin sendmessage throttled")

// ErrWeixinProactiveQuotaExceeded 表示主动推送已达本地安全配额（仅作用于 Proactive=true 的消息，不影响对话回复）。
var ErrWeixinProactiveQuotaExceeded = errors.New("weixin proactive push quota exceeded")

// WeixinLoginSession 表示一个进行中的微信扫码登录会话。
type WeixinLoginSession struct {
	SessionID     string    `json:"session_id"`
	WorkspaceID   uuid.UUID `json:"-"`
	ChannelID     uuid.UUID `json:"channel_id,omitempty"`
	QRCodeID      string    `json:"qrcode_id"`
	QRCodeContent string    `json:"qrcode_content"`
	Status        string    `json:"status"` // wait / scanned / confirmed / expired / error
	BaseURL       string    `json:"base_url"`
	PollBaseURL   string    `json:"-"`
	BotID         string    `json:"bot_id,omitempty"`
	WeixinUserID  string    `json:"weixin_user_id,omitempty"`
	BotToken      string    `json:"-"`
	Message       string    `json:"message,omitempty"`
	ExpiresAt     time.Time `json:"expires_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// WeixinLoginStore 在后端维护短期微信扫码登录会话，支持并发轮询并在确认后自动回填加密凭据。
type WeixinLoginStore struct {
	mu         sync.Mutex
	sessions   map[string]*WeixinLoginSession
	httpClient *http.Client
	defaultURL string
}

func NewWeixinLoginStore(client *http.Client, defaultBaseURL string) *WeixinLoginStore {
	if client == nil {
		client = &http.Client{Timeout: 65 * time.Second}
	}
	if strings.TrimSpace(defaultBaseURL) == "" {
		defaultBaseURL = defaultWeixinBaseURL
	}
	return &WeixinLoginStore{
		sessions:   make(map[string]*WeixinLoginSession),
		httpClient: client,
		defaultURL: strings.TrimRight(strings.TrimSpace(defaultBaseURL), "/"),
	}
}

func weixinHeaders(token string) http.Header {
	var raw [4]byte
	_, _ = rand.Read(raw[:])
	uin := base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(binary.BigEndian.Uint32(raw[:])), 10)))

	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("X-WECHAT-UIN", uin)
	h.Set("AuthorizationType", "ilink_bot_token")
	h.Set("iLink-App-Id", weixinAppID)
	h.Set("iLink-App-ClientVersion", strconv.Itoa(weixinClientVersionInt))
	if strings.TrimSpace(token) != "" {
		h.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	return h
}

func weixinBaseInfo() map[string]string {
	return map[string]string{
		"channel_version": weixinChannelVersion,
		"bot_agent":       "cycledock/1.0 (go)",
	}
}

func (s *WeixinLoginStore) Start(ctx context.Context, workspaceID, channelID uuid.UUID, baseURL string) (*WeixinLoginSession, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = s.defaultURL
	}
	reqBody, _ := json.Marshal(map[string]any{"local_token_list": []string{}})
	endpoint := base + "/ilink/bot/get_bot_qrcode?bot_type=3"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header = weixinHeaders("")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request weixin qrcode: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("weixin qrcode HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Ret              flexInt64  `json:"ret"`
		ErrCode          flexInt64  `json:"errcode"`
		ErrMsg           flexString `json:"errmsg"`
		QRCode           string     `json:"qrcode"`
		QRCodeImgContent string     `json:"qrcode_img_content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode weixin qrcode response: %w", err)
	}
	if payload.Ret != 0 || payload.ErrCode != 0 {
		return nil, fmt.Errorf("weixin get_bot_qrcode failed (ret=%d errcode=%d): %s", payload.Ret.Int64(), payload.ErrCode.Int64(), payload.ErrMsg.String())
	}
	if strings.TrimSpace(payload.QRCode) == "" {
		return nil, errors.New("weixin get_bot_qrcode returned empty qrcode")
	}
	content := strings.TrimSpace(payload.QRCodeImgContent)
	if content == "" {
		content = strings.TrimSpace(payload.QRCode)
	}
	now := time.Now().UTC()
	sess := &WeixinLoginSession{
		SessionID:     uuid.NewString(),
		WorkspaceID:   workspaceID,
		ChannelID:     channelID,
		QRCodeID:      strings.TrimSpace(payload.QRCode),
		QRCodeContent: content,
		Status:        "wait",
		BaseURL:       base,
		PollBaseURL:   base,
		Message:       "请使用微信扫描二维码并确认登录",
		ExpiresAt:     now.Add(5 * time.Minute),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.mu.Lock()
	s.pruneLocked(now)
	s.sessions[sess.SessionID] = sess
	copySess := *sess
	s.mu.Unlock()
	return &copySess, nil
}

func (s *WeixinLoginStore) Poll(ctx context.Context, workspaceID uuid.UUID, sessionID, verifyCode string) (*WeixinLoginSession, error) {
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	if !ok || sess.WorkspaceID != workspaceID {
		s.mu.Unlock()
		return nil, domain.ErrNotFound
	}
	if sess.Status == "confirmed" || sess.Status == "expired" {
		copySess := *sess
		s.mu.Unlock()
		return &copySess, nil
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		sess.Status = "expired"
		sess.Message = "二维码已过期，请重新生成"
		sess.UpdatedAt = time.Now().UTC()
		copySess := *sess
		s.mu.Unlock()
		return &copySess, nil
	}
	pollBase := sess.PollBaseURL
	qrcodeID := sess.QRCodeID
	s.mu.Unlock()

	params := url.Values{}
	params.Set("qrcode", qrcodeID)
	if strings.TrimSpace(verifyCode) != "" {
		params.Set("verify_code", strings.TrimSpace(verifyCode))
	}
	endpoint := strings.TrimRight(pollBase, "/") + "/ilink/bot/get_qrcode_status?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header = weixinHeaders("")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll weixin qrcode status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("weixin qrcode status HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Status       string     `json:"status"`
		BotToken     string     `json:"bot_token"`
		ILinkBotID   flexString `json:"ilink_bot_id"`
		ILinkUserID  flexString `json:"ilink_user_id"`
		BaseURL      string     `json:"baseurl"`
		RedirectHost string     `json:"redirect_host"`
		ErrMsg       flexString `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode weixin qrcode status: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok = s.sessions[sessionID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	sess.UpdatedAt = time.Now().UTC()
	switch strings.ToLower(strings.TrimSpace(payload.Status)) {
	case "confirmed":
		if strings.TrimSpace(payload.BotToken) == "" {
			sess.Status = "error"
			sess.Message = "扫码已确认但未返回 bot_token"
		} else {
			sess.Status = "confirmed"
			sess.BotToken = strings.TrimSpace(payload.BotToken)
			sess.BotID = strings.TrimSpace(payload.ILinkBotID.String())
			sess.WeixinUserID = strings.TrimSpace(payload.ILinkUserID.String())
			if strings.TrimSpace(payload.BaseURL) != "" {
				sess.BaseURL = strings.TrimRight(strings.TrimSpace(payload.BaseURL), "/")
			}
			sess.Message = "微信登录已确认，凭据已加密保存"
		}
	case "scaned", "scanned":
		sess.Status = "scanned"
		sess.Message = "已扫码，请在微信客户端点击确认登录"
	case "scaned_but_redirect":
		sess.Status = "scanned"
		if host := strings.TrimSpace(payload.RedirectHost); host != "" {
			if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
				sess.PollBaseURL = strings.TrimRight(host, "/")
			} else {
				sess.PollBaseURL = "https://" + strings.TrimRight(host, "/")
			}
		}
		sess.Message = "已扫码，正在完成区域路由确认"
	case "need_verifycode":
		sess.Status = "need_verifycode"
		sess.Message = "微信要求输入验证码确认"
	case "expired", "verify_code_blocked":
		sess.Status = "expired"
		sess.Message = "二维码已失效，请重新生成"
	default:
		sess.Status = "wait"
		sess.Message = "等待扫码确认"
	}
	copySess := *sess
	return &copySess, nil
}

func (s *WeixinLoginStore) pruneLocked(now time.Time) {
	for id, sess := range s.sessions {
		if now.Sub(sess.UpdatedAt) > 15*time.Minute {
			delete(s.sessions, id)
		}
	}
}

// WeixinAdapter 实现个人微信 iLink HTTP 长轮询协议（免公网回调）。
type WeixinAdapter struct {
	channel    store.RemoteChannel
	secrets    store.RemoteChannelSecrets
	baseURL    string
	httpClient *http.Client
	chunkDelay time.Duration

	mu                    sync.Mutex
	updatesBuf            string
	longPollTimeout       time.Duration
	contextTokens         map[string]string
	contextTokenAt        map[string]time.Time
	lastChatID            string
	saveState             StatePersister
	lastProactivePushAt   time.Time
	proactivePushSent     []time.Time
	proactivePushLimit    int
	proactivePushWindow   time.Duration
	nowFn                 func() time.Time
}

func NewWeixinAdapter(ch store.RemoteChannel, secrets store.RemoteChannelSecrets, client *http.Client) *WeixinAdapter {
	baseURL := strings.TrimRight(strings.TrimSpace(ch.Config.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultWeixinBaseURL
	}
	if client == nil {
		// 不在 client 上写死固定超时，由单次请求的 context.WithTimeout 精确控制
		// （getupdates 长轮询超时会随服务端 longpolling_timeout_ms 动态调整）。
		client = &http.Client{}
	}
	ctxTokens := make(map[string]string)
	for k, v := range ch.RuntimeState.WeixinContextTokens {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			ctxTokens[k] = v
		}
	}
	lastChat := strings.TrimSpace(ch.Config.TargetChatID)
	if lastChat == "" {
		lastChat = strings.TrimSpace(ch.Config.WeixinUserID)
	}
	if lastChat == "" {
		lastChat = strings.TrimSpace(ch.RuntimeState.LastChatID)
	}
	return &WeixinAdapter{
		channel:             ch,
		secrets:             secrets,
		baseURL:             baseURL,
		httpClient:          client,
		chunkDelay:          defaultWeixinChunkDelay,
		updatesBuf:          strings.TrimSpace(ch.RuntimeState.WeixinUpdatesBuf),
		longPollTimeout:     defaultWeixinLongPollTimeout,
		contextTokens:       ctxTokens,
		contextTokenAt:      make(map[string]time.Time),
		lastChatID:          lastChat,
		proactivePushLimit:  defaultWeixinProactivePushLimit,
		proactivePushWindow: defaultWeixinProactivePushWindow,
		nowFn:               time.Now,
	}
}

func (a *WeixinAdapter) now() time.Time {
	a.mu.Lock()
	fn := a.nowFn
	a.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return time.Now()
}

// reserveProactiveQuota 检查主动推送（Proactive=true）是否仍在 24h 安全配额内。
// 交互式回复（Proactive=false）不经过此配额限制；返回 nil 表示允许发送，并在真正发送成功后由 recordProactiveSent 记账。
func (a *WeixinAdapter) checkProactiveQuota() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proactivePushLimit <= 0 || a.proactivePushWindow <= 0 {
		return nil
	}
	now := time.Now()
	if a.nowFn != nil {
		now = a.nowFn()
	}
	cutoff := now.Add(-a.proactivePushWindow)
	kept := a.proactivePushSent[:0]
	for _, ts := range a.proactivePushSent {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	a.proactivePushSent = kept
	if len(a.proactivePushSent) >= a.proactivePushLimit {
		return fmt.Errorf("%w (%d/%d in %s; skipping proactive push to protect WeChat reply availability)",
			ErrWeixinProactiveQuotaExceeded, len(a.proactivePushSent), a.proactivePushLimit, a.proactivePushWindow)
	}
	return nil
}

func (a *WeixinAdapter) recordProactiveSent() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if a.nowFn != nil {
		now = a.nowFn()
	}
	a.lastProactivePushAt = now
	a.proactivePushSent = append(a.proactivePushSent, now)
}

func (a *WeixinAdapter) Platform() domain.ChannelPlatform {
	return domain.ChannelPlatformWeixin
}

func (a *WeixinAdapter) Run(ctx context.Context, rt AdapterRuntime) error {
	if strings.TrimSpace(a.secrets.BotToken) == "" {
		err := errors.New("weixin bot_token is not configured; complete QR login first")
		if rt.OnStatus != nil {
			rt.OnStatus(ctx, domain.ChannelRuntimeError, err.Error(), false)
		}
		return err
	}
	if rt.OnStatus != nil {
		rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
	}
	a.mu.Lock()
	a.saveState = rt.SaveState
	a.mu.Unlock()
	// 对齐官方 openclaw-weixin 生命周期：长轮询启停时非致命上报 notifystart / notifystop。
	a.notifyLifecycle(ctx, "ilink/bot/msg/notifystart", rtLogger(rt))
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		a.notifyLifecycle(stopCtx, "ilink/bot/msg/notifystop", rtLogger(rt))
	}()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := a.pollOnce(ctx, rt); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
}

func (a *WeixinAdapter) notifyLifecycle(ctx context.Context, endpoint string, logger *slog.Logger) {
	if strings.TrimSpace(a.secrets.BotToken) == "" {
		return
	}
	if _, err := a.apiPostWithTimeout(ctx, endpoint, map[string]any{
		"base_info": weixinBaseInfo(),
	}, defaultWeixinConfigTimeout); err != nil && ctx.Err() == nil && logger != nil {
		logger.Debug("weixin lifecycle notify skipped", "endpoint", endpoint, "err", err)
	}
}

func (a *WeixinAdapter) pollOnce(ctx context.Context, rt AdapterRuntime) error {
	a.mu.Lock()
	buf := a.updatesBuf
	pollTimeout := a.longPollTimeout
	a.mu.Unlock()
	if pollTimeout <= 0 {
		pollTimeout = defaultWeixinLongPollTimeout
	}

	body := map[string]any{
		"get_updates_buf": buf,
		"base_info":       weixinBaseInfo(),
	}
	raw, err := a.apiPostWithTimeout(ctx, "ilink/bot/getupdates", body, pollTimeout+weixinLongPollTimeoutBuffer)
	if err != nil {
		return err
	}
	var resp struct {
		Ret                  flexInt64          `json:"ret"`
		ErrCode              flexInt64          `json:"errcode"`
		ErrMsg               flexString         `json:"errmsg"`
		GetUpdatesBuf        string             `json:"get_updates_buf"`
		LongPollingTimeoutMs flexInt64          `json:"longpolling_timeout_ms"`
		Msgs                 []weixinInboundMsg `json:"msgs"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode weixin getupdates: %w", err)
	}
	if resp.Ret != 0 || resp.ErrCode != 0 {
		if resp.Ret == -14 || resp.ErrCode == -14 {
			return errors.New("weixin login expired (-14), please scan QR code again")
		}
		return fmt.Errorf("weixin getupdates failed (ret=%d errcode=%d): %s", resp.Ret.Int64(), resp.ErrCode.Int64(), resp.ErrMsg.String())
	}
	if rt.OnStatus != nil {
		rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
	}

	if nextTimeout := clampWeixinLongPollTimeout(resp.LongPollingTimeoutMs.Int64()); nextTimeout > 0 {
		a.mu.Lock()
		a.longPollTimeout = nextTimeout
		a.mu.Unlock()
	}

	stateChanged := false
	if resp.GetUpdatesBuf != "" && resp.GetUpdatesBuf != buf {
		a.mu.Lock()
		a.updatesBuf = resp.GetUpdatesBuf
		a.mu.Unlock()
		stateChanged = true
	}
	// 先把新的 get_updates_buf 与本批收到的 context_token 及时持久化，
	// 避免单条 OnInbound 处理失败或连接中断后 updatesBuf 回退，导致微信服务端无限重推旧消息。
	for _, item := range resp.Msgs {
		if item.MessageType.Int() == weixinMsgTypeBot {
			continue
		}
		fromUser := strings.TrimSpace(item.FromUserID.String())
		if fromUser == "" {
			continue
		}
		ctxTok := strings.TrimSpace(item.ContextToken)
		a.mu.Lock()
		if ctxTok != "" && a.contextTokens[fromUser] != ctxTok {
			a.contextTokens[fromUser] = ctxTok
			a.contextTokenAt[fromUser] = time.Now()
			stateChanged = true
		} else if ctxTok != "" {
			a.contextTokenAt[fromUser] = time.Now()
		}
		if a.lastChatID != fromUser {
			a.lastChatID = fromUser
			stateChanged = true
		}
		a.mu.Unlock()
	}
	if stateChanged && rt.SaveState != nil {
		a.persistState(ctx, rt.SaveState)
		stateChanged = false
	}

	for idx, item := range resp.Msgs {
		if item.MessageType.Int() == weixinMsgTypeBot {
			continue
		}
		fromUser := strings.TrimSpace(item.FromUserID.String())
		if fromUser == "" {
			continue
		}
		text := extractWeixinText(item.ItemList)
		if text == "" {
			continue
		}
		createTimeMs := item.CreateTimeMs.Int64()
		msgID := resolveWeixinExternalMessageID(item, fromUser, createTimeMs, text, idx)
		ctxTok := strings.TrimSpace(item.ContextToken)

		chatType := "direct"
		if strings.HasSuffix(fromUser, "@chatroom") {
			chatType = "group"
		}
		msgTime := time.Now().UTC()
		if createTimeMs > 0 {
			msgTime = time.UnixMilli(createTimeMs)
		}
		if rt.OnInbound != nil {
			_ = rt.OnInbound(ctx, InboundMessage{
				ExternalMessageID: msgID,
				SenderID:          fromUser,
				ChatID:            fromUser,
				ChatType:          chatType,
				Content:           text,
				Mentioned:         chatType == "direct",
				ContextToken:      ctxTok,
				Timestamp:         msgTime,
			})
		}
	}

	if stateChanged && rt.SaveState != nil {
		a.persistState(ctx, rt.SaveState)
	}
	return nil
}

// resolveWeixinExternalMessageID 优先使用 iLink 返回的各类消息 ID/序号字段；
// 当服务端全部缺失时使用确定性 SHA-256 指纹（基于发送者、时间戳/批内序号与正文），
// 杜绝旧版 uuid.NewString() 导致去重表失效的问题。
func resolveWeixinExternalMessageID(item weixinInboundMsg, fromUser string, createTimeMs int64, text string, batchIdx int) string {
	candidates := []string{
		strings.TrimSpace(item.MessageID.String()),
		strings.TrimSpace(item.MsgID.String()),
		strings.TrimSpace(item.NewMsgID.String()),
		strings.TrimSpace(item.ClientID.String()),
	}
	for _, id := range candidates {
		if id != "" && id != "0" {
			return id
		}
	}
	if seq := item.Seq.Int64(); seq > 0 {
		return strconv.FormatInt(seq, 10)
	}
	if createTimeMs > 0 {
		return fmt.Sprintf("%s:%d", fromUser, createTimeMs)
	}
	h := sha256.New()
	_, _ = io.WriteString(h, fromUser)
	_, _ = io.WriteString(h, "|")
	_, _ = io.WriteString(h, strconv.FormatInt(createTimeMs, 10))
	_, _ = io.WriteString(h, "|")
	_, _ = io.WriteString(h, strconv.Itoa(batchIdx))
	_, _ = io.WriteString(h, "|")
	_, _ = io.WriteString(h, text)
	return "fp:" + hex.EncodeToString(h.Sum(nil)[:16])
}

func (a *WeixinAdapter) persistState(ctx context.Context, save StatePersister) {
	if save == nil {
		return
	}
	a.mu.Lock()
	nextBuf := a.updatesBuf
	nextChat := a.lastChatID
	tokensCopy := make(map[string]string, len(a.contextTokens))
	for k, v := range a.contextTokens {
		tokensCopy[k] = v
	}
	a.mu.Unlock()
	_ = save(ctx, func(st *store.RemoteChannelRuntimeState) {
		st.WeixinUpdatesBuf = nextBuf
		st.WeixinContextTokens = tokensCopy
		if nextChat != "" {
			st.LastChatID = nextChat
		}
	})
}

func clampWeixinLongPollTimeout(ms int64) time.Duration {
	if ms <= 0 {
		return 0
	}
	d := time.Duration(ms) * time.Millisecond
	if d < minWeixinLongPollTimeout {
		return minWeixinLongPollTimeout
	}
	if d > maxWeixinLongPollTimeout {
		return maxWeixinLongPollTimeout
	}
	return d
}

type weixinInboundMsg struct {
	MessageID    flexString      `json:"message_id"`
	MsgID        flexString      `json:"msg_id"`
	NewMsgID     flexString      `json:"new_msg_id"`
	ClientID     flexString      `json:"client_id"`
	Seq          flexInt64       `json:"seq"`
	FromUserID   flexString      `json:"from_user_id"`
	MessageType  flexInt64       `json:"message_type"`
	ContextToken string          `json:"context_token"`
	CreateTimeMs flexInt64       `json:"create_time_ms"`
	ItemList     []weixinMsgItem `json:"item_list"`
}

type weixinMsgItem struct {
	Type      flexInt64       `json:"type"`
	TextItem  weixinTextItem  `json:"text_item"`
	VoiceItem weixinVoiceItem `json:"voice_item"`
	RefMsg    *weixinRefMsg   `json:"ref_msg"`
}

type weixinTextItem struct {
	Text string `json:"text"`
}

type weixinVoiceItem struct {
	Text string `json:"text"`
}

type weixinRefMsg struct {
	Title       string          `json:"title"`
	MessageItem *weixinRefInner `json:"message_item"`
}

type weixinRefInner struct {
	Type      flexInt64       `json:"type"`
	TextItem  weixinTextItem  `json:"text_item"`
	VoiceItem weixinVoiceItem `json:"voice_item"`
}

// extractWeixinText 从微信 iLink item_list 提取可读文本：
// 支持普通文本、带引用上下文（ref_msg）的回复文本，以及微信服务端附带的语音转文字（type=3 voice_item.text）。
func extractWeixinText(items []weixinMsgItem) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		switch item.Type.Int() {
		case weixinItemText:
			text := strings.TrimSpace(item.TextItem.Text)
			refSummary := formatWeixinRefSummary(item.RefMsg)
			switch {
			case refSummary != "" && text != "":
				parts = append(parts, fmt.Sprintf("[引用: %s]\n%s", refSummary, text))
			case text != "":
				parts = append(parts, text)
			case refSummary != "":
				parts = append(parts, fmt.Sprintf("[引用: %s]", refSummary))
			}
		case weixinItemVoice:
			if vt := strings.TrimSpace(item.VoiceItem.Text); vt != "" {
				parts = append(parts, "[语音] "+vt)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func formatWeixinRefSummary(ref *weixinRefMsg) string {
	if ref == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	title := strings.TrimSpace(ref.Title)
	if title != "" {
		parts = append(parts, title)
	}
	if ref.MessageItem != nil {
		refText := strings.TrimSpace(ref.MessageItem.TextItem.Text)
		if refText == "" && ref.MessageItem.Type.Int() == weixinItemVoice {
			refText = strings.TrimSpace(ref.MessageItem.VoiceItem.Text)
		}
		if refText != "" && refText != title {
			parts = append(parts, refText)
		}
	}
	return strings.Join(parts, " | ")
}

func (a *WeixinAdapter) Send(ctx context.Context, msg OutboundMessage) error {
	if strings.TrimSpace(a.secrets.BotToken) == "" {
		return errors.New("weixin bot_token is not configured")
	}
	toUser := strings.TrimSpace(msg.ChatID)
	a.mu.Lock()
	if toUser == "" {
		toUser = a.lastChatID
	}
	ctxToken := strings.TrimSpace(msg.ContextToken)
	if toUser != "" {
		if cached := strings.TrimSpace(a.contextTokens[toUser]); cached != "" {
			if ctxToken == "" || !a.contextTokenAt[toUser].IsZero() {
				ctxToken = cached
			}
		}
	}
	a.mu.Unlock()

	if toUser == "" {
		return fmt.Errorf("%w: weixin target user_id is empty; send one message from WeChat or configure target_chat_id", ErrTargetNotBound)
	}
	if msg.Proactive {
		if err := a.checkProactiveQuota(); err != nil {
			return err
		}
	}
	if ctxToken != "" {
		ctxToken = a.refreshContextToken(ctx, toUser, ctxToken, false)
	}
	fullText := FormatOutboundForPlatform(domain.ChannelPlatformWeixin, msg.Text, msg.CardURL)
	chunks := SplitMessage(fullText, weixinMaxRunes)
	if len(chunks) == 0 {
		return nil
	}
	for idx, chunk := range chunks {
		if idx > 0 && a.chunkDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(a.chunkDelay):
			}
		}
		retCode, errCode, errMsg, sendErr := a.sendSingleChunk(ctx, toUser, ctxToken, chunk)
		if sendErr == nil {
			continue
		}
		// 微信 iLink 返回 ret=-2 (prepare failed) 实测可能是账号级主动推送限流惩罚或上下文失效，
		// 且在限流惩罚窗口内盲目重复请求 sendmessage 会不断拉长冷却时长。
		// 仅在强制调用 getconfig 明确拿到「不同于旧值的新 context_token」时重试一次；否则立即返回
		// ErrWeixinSendThrottled 让上层停止连打并进入冷却退避。
		if isWeixinPrepareFailed(retCode, errCode, errMsg) {
			if ctxToken != "" {
				refreshed := a.refreshContextToken(ctx, toUser, ctxToken, true)
				if refreshed != "" && refreshed != ctxToken {
					ctxToken = refreshed
					if _, _, _, retryErr := a.sendSingleChunk(ctx, toUser, ctxToken, chunk); retryErr == nil {
						continue
					}
				}
			}
			// 标记缓存时间失效，同时附带脱敏诊断摘要（是否存在 context_token 与文本长度），
			// 并包装 ErrWeixinSendThrottled 通知 Dispatcher 立即熔断本轮同通道其余发送。
			a.mu.Lock()
			delete(a.contextTokenAt, toUser)
			a.mu.Unlock()
			return fmt.Errorf("%w: %v (has_context_token=%t text_runes=%d proactive=%t; ilink may be rate-limiting proactive sends or context_token expired)",
				ErrWeixinSendThrottled, sendErr, ctxToken != "", len([]rune(chunk)), msg.Proactive)
		}
		return sendErr
	}
	// 仅保留最近一次检查与记录逻辑，确保在 Send 成功后准确记账；
	// 在长轮询收到任何新的用户入站消息后，重置主动推送的短期连续计数无关，保持 24h 滑动窗口即可。
	// 若单测需要自定义时钟或限额，可直接配置 nowFn / proactivePushLimit / proactivePushWindow。
	if msg.Proactive {
		a.recordProactiveSent()
	}
	return nil
}

func isWeixinPrepareFailed(retCode, errCode int64, errMsg string) bool {
	if retCode == -2 || errCode == -2 {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(errMsg)), "prepare failed")
}

func (a *WeixinAdapter) sendSingleChunk(ctx context.Context, toUser, ctxToken, chunk string) (int64, int64, string, error) {
	weixinMsg := map[string]any{
		"from_user_id":  "",
		"to_user_id":    toUser,
		"client_id":     "cycledock-" + uuid.NewString()[:12],
		"message_type":  weixinMsgTypeBot,
		"message_state": weixinMsgStateFinish,
		"item_list": []map[string]any{
			{
				"type":      weixinItemText,
				"text_item": map[string]string{"text": chunk},
			},
		},
	}
	if ctxToken != "" {
		weixinMsg["context_token"] = ctxToken
	}
	raw, err := a.apiPostWithTimeout(ctx, "ilink/bot/sendmessage", map[string]any{
		"msg":       weixinMsg,
		"base_info": weixinBaseInfo(),
	}, defaultWeixinAPITimeout)
	if err != nil {
		return 0, 0, "", err
	}
	var resp struct {
		Ret     flexInt64  `json:"ret"`
		ErrCode flexInt64  `json:"errcode"`
		ErrMsg  flexString `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &resp); err == nil && (resp.Ret != 0 || resp.ErrCode != 0) {
		retV := resp.Ret.Int64()
		errV := resp.ErrCode.Int64()
		msgV := resp.ErrMsg.String()
		return retV, errV, msgV, fmt.Errorf("weixin sendmessage failed (ret=%d errcode=%d): %s", retV, errV, msgV)
	}
	return 0, 0, "", nil
}

// refreshContextToken 在缓存的 context_token 超过 60 秒、缺少获取时间记录或 force=true 时，
// 静默调用 ilink/bot/getconfig 刷新；任何失败都回退沿用旧 token，不阻断出站消息发送。
func (a *WeixinAdapter) refreshContextToken(ctx context.Context, userID, ctxToken string, force bool) string {
	ctxToken = strings.TrimSpace(ctxToken)
	if userID == "" || ctxToken == "" {
		return ctxToken
	}
	now := time.Now()
	a.mu.Lock()
	cachedTok := strings.TrimSpace(a.contextTokens[userID])
	cachedAt := a.contextTokenAt[userID]
	saveState := a.saveState
	a.mu.Unlock()

	if !force && cachedTok == ctxToken && !cachedAt.IsZero() && now.Sub(cachedAt) < weixinContextTokenRefreshAge {
		return ctxToken
	}

	raw, err := a.apiPostWithTimeout(ctx, "ilink/bot/getconfig", map[string]any{
		"ilink_user_id": userID,
		"context_token": ctxToken,
		"base_info":     weixinBaseInfo(),
	}, defaultWeixinConfigTimeout)
	if err != nil {
		return ctxToken
	}
	var resp struct {
		Ret          flexInt64  `json:"ret"`
		ErrCode      flexInt64  `json:"errcode"`
		ContextToken flexString `json:"context_token"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ctxToken
	}
	if resp.Ret != 0 || resp.ErrCode != 0 {
		return ctxToken
	}
	refreshed := strings.TrimSpace(resp.ContextToken.String())
	if refreshed == "" {
		return ctxToken
	}
	a.mu.Lock()
	a.contextTokens[userID] = refreshed
	a.contextTokenAt[userID] = time.Now()
	a.mu.Unlock()
	if refreshed != ctxToken && saveState != nil {
		a.persistState(ctx, saveState)
	}
	return refreshed
}

func (a *WeixinAdapter) Test(ctx context.Context, testText string) error {
	if strings.TrimSpace(a.secrets.BotToken) == "" {
		return errors.New("weixin bot_token is not configured; complete QR login first")
	}
	a.mu.Lock()
	target := a.lastChatID
	ctxToken := a.contextTokens[target]
	pollTimeout := a.longPollTimeout
	a.mu.Unlock()
	if target != "" {
		return a.Send(ctx, OutboundMessage{
			ChatID:       target,
			ContextToken: ctxToken,
			Text:         testText,
		})
	}
	if pollTimeout <= 0 {
		pollTimeout = defaultWeixinLongPollTimeout
	}
	// 尚未收到过私聊会话时，探测一次 getupdates 校验 bot_token 有效性。
	raw, err := a.apiPostWithTimeout(ctx, "ilink/bot/getupdates", map[string]any{
		"get_updates_buf": a.updatesBuf,
		"base_info":       weixinBaseInfo(),
	}, pollTimeout+weixinLongPollTimeoutBuffer)
	if err != nil {
		return err
	}
	var resp struct {
		Ret     flexInt64  `json:"ret"`
		ErrCode flexInt64  `json:"errcode"`
		ErrMsg  flexString `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.Ret != 0 || resp.ErrCode != 0 {
		return fmt.Errorf("weixin token check failed (ret=%d errcode=%d): %s", resp.Ret.Int64(), resp.ErrCode.Int64(), resp.ErrMsg.String())
	}
	return nil
}

func (a *WeixinAdapter) apiPostWithTimeout(ctx context.Context, endpoint string, body map[string]any, timeout time.Duration) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	reqCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, a.baseURL+"/"+strings.TrimLeft(endpoint, "/"), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header = weixinHeaders(a.secrets.BotToken)
	client := a.httpClient
	if timeout > 0 && client.Timeout > 0 && client.Timeout < timeout {
		// 仅放开本次长轮询请求的客户端超时上限，不修改调用方共享的 http.Client。
		cloned := *client
		cloned.Timeout = timeout
		client = &cloned
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("weixin %s HTTP %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}
