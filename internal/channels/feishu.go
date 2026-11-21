package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	defaultFeishuBaseURL = "https://open.feishu.cn"
	defaultLarkBaseURL   = "https://open.larksuite.com"
	feishuMaxRunes       = 3500
)

// FeishuAdapter 支持飞书/Lark 官方长连接（app_id + app_secret，免公网回调）
// 以及飞书自定义群机器人 Webhook（含可选加签 secret）。
type FeishuAdapter struct {
	channel    store.RemoteChannel
	secrets    store.RemoteChannelSecrets
	baseURL    string
	wsURL      string
	httpClient *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
	lastChatID  string
	lastThread  string
}

func NewFeishuAdapter(ch store.RemoteChannel, secrets store.RemoteChannelSecrets, client *http.Client) *FeishuAdapter {
	baseURL := strings.TrimRight(strings.TrimSpace(ch.Config.BaseURL), "/")
	if baseURL == "" {
		if strings.EqualFold(ch.Config.Domain, "lark") {
			baseURL = defaultLarkBaseURL
		} else {
			baseURL = defaultFeishuBaseURL
		}
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	lastChat := strings.TrimSpace(ch.Config.TargetChatID)
	if lastChat == "" {
		lastChat = strings.TrimSpace(ch.RuntimeState.LastChatID)
	}
	return &FeishuAdapter{
		channel:    ch,
		secrets:    secrets,
		baseURL:    baseURL,
		wsURL:      strings.TrimSpace(ch.Config.WSURL),
		httpClient: client,
		lastChatID: lastChat,
		lastThread: strings.TrimSpace(ch.RuntimeState.LastThreadID),
	}
}

func (a *FeishuAdapter) Platform() domain.ChannelPlatform {
	return domain.ChannelPlatformFeishu
}

func (a *FeishuAdapter) Run(ctx context.Context, rt AdapterRuntime) error {
	if a.channel.Transport == domain.ChannelTransportWebhook {
		if strings.TrimSpace(a.secrets.WebhookURL) == "" {
			err := errors.New("feishu webhook_url is not configured")
			if rt.OnStatus != nil {
				rt.OnStatus(ctx, domain.ChannelRuntimeError, err.Error(), false)
			}
			return err
		}
		if rt.OnStatus != nil {
			rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
		}
		<-ctx.Done()
		return ctx.Err()
	}

	if strings.TrimSpace(a.channel.Config.AppID) == "" || strings.TrimSpace(a.secrets.AppSecret) == "" {
		err := errors.New("feishu app_id and app_secret are required")
		if rt.OnStatus != nil {
			rt.OnStatus(ctx, domain.ChannelRuntimeError, err.Error(), false)
		}
		return err
	}

	wsEndpoint := a.wsURL
	if wsEndpoint == "" {
		var err error
		wsEndpoint, err = a.fetchWSEndpoint(ctx)
		if err != nil {
			return err
		}
	}
	conn, err := dialWS(ctx, wsEndpoint)
	if err != nil {
		return fmt.Errorf("dial feishu websocket: %w", err)
	}
	defer conn.Close()

	if rt.OnStatus != nil {
		rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
	}

	for {
		_, data, err := conn.Read()
		if err != nil {
			return err
		}
		a.handleWSFrame(ctx, conn, data, rt)
	}
}

func (a *FeishuAdapter) fetchWSEndpoint(ctx context.Context) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"AppID":     strings.TrimSpace(a.channel.Config.AppID),
		"AppSecret": strings.TrimSpace(a.secrets.AppSecret),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/callback/ws/endpoint", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("locale", "zh")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request feishu ws endpoint: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("feishu ws endpoint HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			URL string `json:"URL"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if parsed.Code != 0 || strings.TrimSpace(parsed.Data.URL) == "" {
		return "", fmt.Errorf("feishu ws endpoint failed (code=%d): %s", parsed.Code, parsed.Msg)
	}
	return strings.TrimSpace(parsed.Data.URL), nil
}

func (a *FeishuAdapter) handleWSFrame(ctx context.Context, conn *wsConn, data []byte, rt AdapterRuntime) {
	// 同时兼容 JSON 事件封装与长连接 ACK 协议，便于测试和与网关兼容。
	var envelope struct {
		Type   string          `json:"type"`
		ID     string          `json:"id"`
		Header json.RawMessage `json:"header"`
		Event  json.RawMessage `json:"event"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return
	}
	if envelope.Type == "ping" {
		_ = conn.WriteJSON(map[string]any{"type": "pong", "id": envelope.ID})
		if rt.OnStatus != nil {
			rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
		}
		return
	}
	if envelope.ID != "" {
		_ = conn.WriteJSON(map[string]any{"code": 200, "id": envelope.ID})
	}
	eventBytes := envelope.Event
	if len(eventBytes) == 0 && len(envelope.Data) > 0 {
		var nested struct {
			Event json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal(envelope.Data, &nested); err == nil && len(nested.Event) > 0 {
			eventBytes = nested.Event
		}
	}
	if len(eventBytes) == 0 {
		return
	}
	var event struct {
		Sender struct {
			SenderID struct {
				OpenID string `json:"open_id"`
				UserID string `json:"user_id"`
			} `json:"sender_id"`
			SenderType string `json:"sender_type"`
		} `json:"sender"`
		Message struct {
			MessageID string `json:"message_id"`
			RootID    string `json:"root_id"`
			ThreadID  string `json:"thread_id"`
			ChatID    string `json:"chat_id"`
			ChatType  string `json:"chat_type"`
			MsgType   string `json:"message_type"`
			Content   string `json:"content"`
			Mentions  []struct {
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"mentions"`
		} `json:"message"`
	}
	if err := json.Unmarshal(eventBytes, &event); err != nil {
		return
	}
	if strings.EqualFold(event.Sender.SenderType, "app") || strings.TrimSpace(event.Message.MessageID) == "" {
		return
	}
	text := extractFeishuText(event.Message.Content)
	for _, m := range event.Message.Mentions {
		if strings.TrimSpace(m.Key) != "" {
			text = strings.ReplaceAll(text, m.Key, "")
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	chatType := "direct"
	if strings.EqualFold(event.Message.ChatType, "group") {
		chatType = "group"
	}
	mentioned := chatType == "direct" || len(event.Message.Mentions) > 0
	senderID := strings.TrimSpace(event.Sender.SenderID.OpenID)
	if senderID == "" {
		senderID = strings.TrimSpace(event.Sender.SenderID.UserID)
	}
	chatID := strings.TrimSpace(event.Message.ChatID)
	threadID := strings.TrimSpace(event.Message.ThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(event.Message.RootID)
	}

	a.mu.Lock()
	a.lastChatID = chatID
	a.lastThread = threadID
	a.mu.Unlock()
	if rt.SaveState != nil {
		_ = rt.SaveState(ctx, func(st *store.RemoteChannelRuntimeState) {
			st.LastChatID = chatID
			st.LastChatType = chatType
			if threadID != "" {
				st.LastThreadID = threadID
			}
		})
	}
	if rt.OnInbound != nil {
		_ = rt.OnInbound(ctx, InboundMessage{
			ExternalMessageID: strings.TrimSpace(event.Message.MessageID),
			SenderID:          senderID,
			ChatID:            chatID,
			ChatType:          chatType,
			ThreadID:          threadID,
			Content:           text,
			Mentioned:         mentioned,
			Timestamp:         time.Now().UTC(),
		})
	}
}

func extractFeishuText(rawContent string) string {
	var textPayload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(rawContent), &textPayload); err == nil && strings.TrimSpace(textPayload.Text) != "" {
		return strings.TrimSpace(textPayload.Text)
	}
	return strings.TrimSpace(rawContent)
}

func (a *FeishuAdapter) tenantAccessToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	if a.token != "" && time.Now().Before(a.tokenExpiry) {
		tok := a.token
		a.mu.Unlock()
		return tok, nil
	}
	a.mu.Unlock()

	body, _ := json.Marshal(map[string]string{
		"app_id":     strings.TrimSpace(a.channel.Config.AppID),
		"app_secret": strings.TrimSpace(a.secrets.AppSecret),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var parsed struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if parsed.Code != 0 || strings.TrimSpace(parsed.TenantAccessToken) == "" {
		return "", fmt.Errorf("feishu tenant_access_token failed (code=%d): %s", parsed.Code, parsed.Msg)
	}
	ttl := time.Duration(parsed.Expire-120) * time.Second
	if ttl < time.Minute {
		ttl = 5 * time.Minute
	}
	a.mu.Lock()
	a.token = parsed.TenantAccessToken
	a.tokenExpiry = time.Now().Add(ttl)
	a.mu.Unlock()
	return parsed.TenantAccessToken, nil
}

func (a *FeishuAdapter) Send(ctx context.Context, msg OutboundMessage) error {
	fullText := FormatOutboundWithLink(msg.Text, msg.CardURL)
	chunks := SplitMessage(fullText, feishuMaxRunes)
	if a.channel.Transport == domain.ChannelTransportWebhook {
		webhookURL := strings.TrimSpace(a.secrets.WebhookURL)
		if webhookURL == "" {
			return errors.New("feishu webhook_url is not configured")
		}
		for _, chunk := range chunks {
			payload := map[string]any{
				"msg_type": "text",
				"content":  map[string]string{"text": chunk},
			}
			if sec := strings.TrimSpace(a.secrets.WebhookSecret); sec != "" {
				ts, sign := signFeishuWebhook(sec, time.Now())
				payload["timestamp"] = ts
				payload["sign"] = sign
			}
			if err := postJSONExpectOK(ctx, a.httpClient, webhookURL, payload, "code"); err != nil {
				return fmt.Errorf("feishu webhook send: %w", err)
			}
		}
		return nil
	}

	token, err := a.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	chatID := strings.TrimSpace(msg.ChatID)
	a.mu.Lock()
	if chatID == "" {
		chatID = a.lastChatID
	}
	a.mu.Unlock()
	if chatID == "" {
		return errors.New("feishu target chat_id is empty")
	}
	receiveIDType := "chat_id"
	if strings.HasPrefix(chatID, "ou_") {
		receiveIDType = "open_id"
	}
	for _, chunk := range chunks {
		contentBytes, _ := json.Marshal(map[string]string{"text": chunk})
		bodyBytes, _ := json.Marshal(map[string]any{
			"receive_id": chatID,
			"msg_type":   "text",
			"content":    string(contentBytes),
			"uuid":       uuid.NewString(),
		})
		endpoint := a.baseURL + "/open-apis/im/v1/messages?receive_id_type=" + receiveIDType
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return err
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("feishu send HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		var parsed struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Code != 0 {
			return fmt.Errorf("feishu send failed (code=%d): %s", parsed.Code, parsed.Msg)
		}
	}
	return nil
}

func (a *FeishuAdapter) Test(ctx context.Context, testText string) error {
	if a.channel.Transport == domain.ChannelTransportWebhook {
		return a.Send(ctx, OutboundMessage{Text: testText})
	}
	if _, err := a.tenantAccessToken(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	target := a.lastChatID
	a.mu.Unlock()
	if target != "" {
		return a.Send(ctx, OutboundMessage{ChatID: target, Text: testText})
	}
	return nil
}
