package channels

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	weixinChannelVersion   = "2.4.6"
	weixinAppID            = "bot"
	weixinClientVersionInt = (2 << 16) | (4 << 8) | 6
	weixinItemText         = 1
	weixinMsgTypeBot       = 2
	weixinMsgStateFinish   = 2
	weixinMaxRunes         = 1800
)

// WeixinLoginSession 表示一个进行中的微信扫码登录会话。
type WeixinLoginSession struct {
	SessionID        string    `json:"session_id"`
	WorkspaceID      uuid.UUID `json:"-"`
	ChannelID        uuid.UUID `json:"channel_id,omitempty"`
	QRCodeID         string    `json:"qrcode_id"`
	QRCodeContent    string    `json:"qrcode_content"`
	Status           string    `json:"status"` // wait / scanned / confirmed / expired / error
	BaseURL          string    `json:"base_url"`
	PollBaseURL      string    `json:"-"`
	BotID            string    `json:"bot_id,omitempty"`
	WeixinUserID     string    `json:"weixin_user_id,omitempty"`
	BotToken         string    `json:"-"`
	Message          string    `json:"message,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
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
		Ret              int    `json:"ret"`
		ErrCode          int    `json:"errcode"`
		ErrMsg           string `json:"errmsg"`
		QRCode           string `json:"qrcode"`
		QRCodeImgContent string `json:"qrcode_img_content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode weixin qrcode response: %w", err)
	}
	if payload.Ret != 0 || payload.ErrCode != 0 {
		return nil, fmt.Errorf("weixin get_bot_qrcode failed (ret=%d errcode=%d): %s", payload.Ret, payload.ErrCode, payload.ErrMsg)
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
		Status       string `json:"status"`
		BotToken     string `json:"bot_token"`
		ILinkBotID   string `json:"ilink_bot_id"`
		ILinkUserID  string `json:"ilink_user_id"`
		BaseURL      string `json:"baseurl"`
		RedirectHost string `json:"redirect_host"`
		ErrMsg       string `json:"errmsg"`
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
			sess.BotID = strings.TrimSpace(payload.ILinkBotID)
			sess.WeixinUserID = strings.TrimSpace(payload.ILinkUserID)
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

	mu            sync.Mutex
	updatesBuf    string
	contextTokens map[string]string
	lastChatID    string
}

func NewWeixinAdapter(ch store.RemoteChannel, secrets store.RemoteChannelSecrets, client *http.Client) *WeixinAdapter {
	baseURL := strings.TrimRight(strings.TrimSpace(ch.Config.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultWeixinBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 50 * time.Second}
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
		channel:       ch,
		secrets:       secrets,
		baseURL:       baseURL,
		httpClient:    client,
		updatesBuf:    strings.TrimSpace(ch.RuntimeState.WeixinUpdatesBuf),
		contextTokens: ctxTokens,
		lastChatID:    lastChat,
	}
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

func (a *WeixinAdapter) pollOnce(ctx context.Context, rt AdapterRuntime) error {
	a.mu.Lock()
	buf := a.updatesBuf
	a.mu.Unlock()

	body := map[string]any{
		"get_updates_buf": buf,
		"base_info":       weixinBaseInfo(),
	}
	raw, err := a.apiPost(ctx, "ilink/bot/getupdates", body)
	if err != nil {
		return err
	}
	var resp struct {
		Ret           int               `json:"ret"`
		ErrCode       int               `json:"errcode"`
		ErrMsg        string            `json:"errmsg"`
		GetUpdatesBuf string            `json:"get_updates_buf"`
		Msgs          []weixinInboundMsg `json:"msgs"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode weixin getupdates: %w", err)
	}
	if resp.Ret != 0 || resp.ErrCode != 0 {
		if resp.Ret == -14 || resp.ErrCode == -14 {
			return errors.New("weixin login expired (-14), please scan QR code again")
		}
		return fmt.Errorf("weixin getupdates failed (ret=%d errcode=%d): %s", resp.Ret, resp.ErrCode, resp.ErrMsg)
	}
	if rt.OnStatus != nil {
		rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
	}

	stateChanged := false
	if resp.GetUpdatesBuf != "" && resp.GetUpdatesBuf != buf {
		a.mu.Lock()
		a.updatesBuf = resp.GetUpdatesBuf
		a.mu.Unlock()
		stateChanged = true
	}

	for _, item := range resp.Msgs {
		if item.MessageType == weixinMsgTypeBot {
			continue
		}
		fromUser := strings.TrimSpace(item.FromUserID)
		if fromUser == "" {
			continue
		}
		text := extractWeixinText(item.ItemList)
		if text == "" {
			continue
		}
		msgID := strings.TrimSpace(item.MessageID)
		if msgID == "" && item.Seq > 0 {
			msgID = strconv.FormatInt(item.Seq, 10)
		}
		if msgID == "" {
			msgID = fmt.Sprintf("%s:%d", fromUser, item.CreateTimeMs)
		}
		a.mu.Lock()
		if strings.TrimSpace(item.ContextToken) != "" {
			a.contextTokens[fromUser] = strings.TrimSpace(item.ContextToken)
			stateChanged = true
		}
		a.lastChatID = fromUser
		a.mu.Unlock()

		chatType := "direct"
		if strings.HasSuffix(fromUser, "@chatroom") {
			chatType = "group"
		}
		if rt.OnInbound != nil {
			_ = rt.OnInbound(ctx, InboundMessage{
				ExternalMessageID: msgID,
				SenderID:          fromUser,
				ChatID:            fromUser,
				ChatType:          chatType,
				Content:           text,
				Mentioned:         chatType == "direct",
				ContextToken:      strings.TrimSpace(item.ContextToken),
				Timestamp:         time.UnixMilli(item.CreateTimeMs),
			})
		}
	}

	if stateChanged && rt.SaveState != nil {
		a.mu.Lock()
		nextBuf := a.updatesBuf
		nextChat := a.lastChatID
		tokensCopy := make(map[string]string, len(a.contextTokens))
		for k, v := range a.contextTokens {
			tokensCopy[k] = v
		}
		a.mu.Unlock()
		_ = rt.SaveState(ctx, func(st *store.RemoteChannelRuntimeState) {
			st.WeixinUpdatesBuf = nextBuf
			st.WeixinContextTokens = tokensCopy
			if nextChat != "" {
				st.LastChatID = nextChat
			}
		})
	}
	return nil
}

type weixinInboundMsg struct {
	MessageID    string `json:"message_id"`
	Seq          int64  `json:"seq"`
	FromUserID   string `json:"from_user_id"`
	MessageType  int    `json:"message_type"`
	ContextToken string `json:"context_token"`
	CreateTimeMs int64  `json:"create_time_ms"`
	ItemList     []struct {
		Type     int `json:"type"`
		TextItem struct {
			Text string `json:"text"`
		} `json:"text_item"`
	} `json:"item_list"`
}

func extractWeixinText(items []struct {
	Type     int `json:"type"`
	TextItem struct {
		Text string `json:"text"`
	} `json:"text_item"`
}) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item.Type == weixinItemText {
			if t := strings.TrimSpace(item.TextItem.Text); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n")
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
	if ctxToken == "" && toUser != "" {
		ctxToken = a.contextTokens[toUser]
	}
	a.mu.Unlock()

	if toUser == "" {
		return errors.New("weixin target user_id is empty; send one message from WeChat or configure target_chat_id")
	}
	fullText := FormatOutboundWithLink(msg.Text, msg.CardURL)
	chunks := SplitMessage(fullText, weixinMaxRunes)
	for _, chunk := range chunks {
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
		raw, err := a.apiPost(ctx, "ilink/bot/sendmessage", map[string]any{
			"msg":       weixinMsg,
			"base_info": weixinBaseInfo(),
		})
		if err != nil {
			return err
		}
		var resp struct {
			Ret     int    `json:"ret"`
			ErrCode int    `json:"errcode"`
			ErrMsg  string `json:"errmsg"`
		}
		if err := json.Unmarshal(raw, &resp); err == nil && (resp.Ret != 0 || resp.ErrCode != 0) {
			return fmt.Errorf("weixin sendmessage failed (ret=%d errcode=%d): %s", resp.Ret, resp.ErrCode, resp.ErrMsg)
		}
	}
	return nil
}

func (a *WeixinAdapter) Test(ctx context.Context, testText string) error {
	if strings.TrimSpace(a.secrets.BotToken) == "" {
		return errors.New("weixin bot_token is not configured; complete QR login first")
	}
	a.mu.Lock()
	target := a.lastChatID
	ctxToken := a.contextTokens[target]
	a.mu.Unlock()
	if target != "" {
		return a.Send(ctx, OutboundMessage{
			ChatID:       target,
			ContextToken: ctxToken,
			Text:         testText,
		})
	}
	// 尚未收到过私聊会话时，探测一次 getupdates 校验 bot_token 有效性。
	raw, err := a.apiPost(ctx, "ilink/bot/getupdates", map[string]any{
		"get_updates_buf": a.updatesBuf,
		"base_info":       weixinBaseInfo(),
	})
	if err != nil {
		return err
	}
	var resp struct {
		Ret     int    `json:"ret"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.Ret != 0 || resp.ErrCode != 0 {
		return fmt.Errorf("weixin token check failed (ret=%d errcode=%d): %s", resp.Ret, resp.ErrCode, resp.ErrMsg)
	}
	return nil
}

func (a *WeixinAdapter) apiPost(ctx context.Context, endpoint string, body map[string]any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/"+strings.TrimLeft(endpoint, "/"), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header = weixinHeaders(a.secrets.BotToken)
	resp, err := a.httpClient.Do(req)
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
