package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	defaultDingTalkBaseURL = "https://api.dingtalk.com"
	dingTalkMaxRunes       = 3500
)

// DingTalkAdapter 支持钉钉官方 Stream 长连接模式（client_id + client_secret，免公网回调）
// 以及钉钉自定义群机器人 Webhook 模式（含 HMAC-SHA256 加签 secret）。
type DingTalkAdapter struct {
	channel    store.RemoteChannel
	secrets    store.RemoteChannelSecrets
	baseURL    string
	wsURL      string
	httpClient *http.Client

	mu                 sync.Mutex
	token              string
	tokenExpiry        time.Time
	lastChatID         string
	lastSessionWebhook string
	saveState          StatePersister
}

func NewDingTalkAdapter(ch store.RemoteChannel, secrets store.RemoteChannelSecrets, client *http.Client) *DingTalkAdapter {
	baseURL := strings.TrimRight(strings.TrimSpace(ch.Config.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultDingTalkBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	lastChat := strings.TrimSpace(ch.Config.TargetChatID)
	if lastChat == "" {
		lastChat = strings.TrimSpace(ch.RuntimeState.LastChatID)
	}
	return &DingTalkAdapter{
		channel:            ch,
		secrets:            secrets,
		baseURL:            baseURL,
		wsURL:              strings.TrimSpace(ch.Config.WSURL),
		httpClient:         client,
		lastChatID:         lastChat,
		lastSessionWebhook: strings.TrimSpace(ch.RuntimeState.LastSessionWebhook),
	}
}

func (a *DingTalkAdapter) Platform() domain.ChannelPlatform {
	return domain.ChannelPlatformDingTalk
}

func (a *DingTalkAdapter) Run(ctx context.Context, rt AdapterRuntime) error {
	if a.channel.Transport == domain.ChannelTransportWebhook {
		if strings.TrimSpace(a.secrets.WebhookURL) == "" {
			err := errors.New("dingtalk webhook_url is not configured")
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

	if strings.TrimSpace(a.channel.Config.ClientID) == "" || strings.TrimSpace(a.secrets.ClientSecret) == "" {
		err := errors.New("dingtalk client_id and client_secret are required for stream mode")
		if rt.OnStatus != nil {
			rt.OnStatus(ctx, domain.ChannelRuntimeError, err.Error(), false)
		}
		return err
	}

	endpoint := a.wsURL
	if endpoint == "" {
		var err error
		endpoint, err = a.openStreamConnection(ctx)
		if err != nil {
			return err
		}
	}
	conn, err := dialWS(ctx, endpoint)
	if err != nil {
		return fmt.Errorf("dial dingtalk stream websocket: %w", err)
	}
	defer conn.Close()

	a.mu.Lock()
	a.saveState = rt.SaveState
	a.mu.Unlock()
	rtLogger(rt).Info("dingtalk stream websocket connected")
	if rt.OnStatus != nil {
		rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
	}

	for {
		_, data, err := conn.Read()
		if err != nil {
			return err
		}
		if shouldDisconnect := a.handleStreamFrame(ctx, conn, data, rt); shouldDisconnect {
			return errors.New("dingtalk stream server requested disconnect")
		}
	}
}

func (a *DingTalkAdapter) openStreamConnection(ctx context.Context) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"clientId":     strings.TrimSpace(a.channel.Config.ClientID),
		"clientSecret": strings.TrimSpace(a.secrets.ClientSecret),
		"subscriptions": []map[string]string{
			{"type": "CALLBACK", "topic": "/v1.0/im/bot/messages/get"},
		},
		"ua": "cycledock-go-stream/1.0",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1.0/gateway/connections/open", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("open dingtalk stream connection: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("dingtalk stream open HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed struct {
		Endpoint string `json:"endpoint"`
		Ticket   string `json:"ticket"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Endpoint) == "" || strings.TrimSpace(parsed.Ticket) == "" {
		return "", fmt.Errorf("dingtalk stream open returned empty endpoint/ticket: %s", parsed.Message)
	}
	u, err := url.Parse(strings.TrimSpace(parsed.Endpoint))
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("ticket", strings.TrimSpace(parsed.Ticket))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (a *DingTalkAdapter) handleStreamFrame(ctx context.Context, conn *wsConn, raw []byte, rt AdapterRuntime) bool {
	log := rtLogger(rt)
	var frame struct {
		SpecVersion string `json:"specVersion"`
		Type        string `json:"type"`
		Headers     struct {
			Topic     string     `json:"topic"`
			MessageID flexString `json:"messageId"`
		} `json:"headers"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		log.Debug("dingtalk stream frame decode failed", "err", err)
		return false
	}
	headerMsgID := strings.TrimSpace(frame.Headers.MessageID.String())
	if strings.EqualFold(frame.Type, "SYSTEM") {
		if strings.EqualFold(frame.Headers.Topic, "ping") {
			_ = conn.WriteJSON(map[string]any{
				"code":    200,
				"headers": map[string]string{"contentType": "application/json", "messageId": headerMsgID},
				"message": "OK",
				"data":    frame.Data,
			})
			if rt.OnStatus != nil {
				rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
			}
			return false
		}
		if strings.EqualFold(frame.Headers.Topic, "disconnect") {
			log.Warn("dingtalk stream server sent disconnect frame")
			return true
		}
		return false
	}
	if headerMsgID != "" {
		_ = conn.WriteJSON(map[string]any{
			"code":    200,
			"headers": map[string]string{"contentType": "application/json", "messageId": headerMsgID},
			"message": "OK",
			"data":    `{"response":null}`,
		})
	}
	if strings.TrimSpace(frame.Data) == "" {
		return false
	}
	var msg struct {
		MsgID            flexString `json:"msgId"`
		MsgType          string     `json:"msgtype"`
		ConversationID   flexString `json:"conversationId"`
		ConversationType flexString `json:"conversationType"` // "1"=单聊, "2"=群聊（兼容数字 1/2）
		SenderStaffID    flexString `json:"senderStaffId"`
		SenderID         flexString `json:"senderId"`
		SessionWebhook   string     `json:"sessionWebhook"`
		IsInAtList       bool       `json:"isInAtList"`
		Text             struct {
			Content string `json:"content"`
		} `json:"text"`
		Content struct {
			RichText []struct {
				Text string `json:"text"`
			} `json:"richText"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(frame.Data), &msg); err != nil {
		log.Warn("dingtalk stream callback data decode failed", "topic", frame.Headers.Topic, "err", err)
		return false
	}
	text := strings.TrimSpace(msg.Text.Content)
	if text == "" && len(msg.Content.RichText) > 0 {
		parts := make([]string, 0, len(msg.Content.RichText))
		for _, item := range msg.Content.RichText {
			if t := strings.TrimSpace(item.Text); t != "" {
				parts = append(parts, t)
			}
		}
		text = strings.TrimSpace(strings.Join(parts, " "))
	}
	if text == "" {
		log.Debug("dingtalk stream callback ignored empty text", "msgtype", msg.MsgType)
		return false
	}
	senderID := strings.TrimSpace(msg.SenderStaffID.String())
	if senderID == "" {
		senderID = strings.TrimSpace(msg.SenderID.String())
	}
	// 单聊场景优先保存 senderID 作为 chatID，便于 sessionWebhook 过期后通过 /v1.0/robot/oToMessages/batchSend 主动补发；
	// 群聊场景保存 conversationId。
	chatType := "direct"
	if strings.TrimSpace(msg.ConversationType.String()) == "2" {
		chatType = "group"
	}
	chatID := senderID
	if chatType == "group" || chatID == "" {
		if convID := strings.TrimSpace(msg.ConversationID.String()); convID != "" {
			chatID = convID
		}
	}
	mentioned := chatType == "direct" || msg.IsInAtList
	msgID := strings.TrimSpace(msg.MsgID.String())
	if msgID == "" {
		msgID = headerMsgID
	}
	if msgID == "" {
		msgID = uuid.NewString()
	}

	a.mu.Lock()
	a.lastChatID = chatID
	if strings.TrimSpace(msg.SessionWebhook) != "" {
		a.lastSessionWebhook = strings.TrimSpace(msg.SessionWebhook)
	}
	a.mu.Unlock()
	if rt.SaveState != nil {
		_ = rt.SaveState(ctx, func(st *store.RemoteChannelRuntimeState) {
			st.LastChatID = chatID
			st.LastChatType = chatType
			if strings.TrimSpace(msg.SessionWebhook) != "" {
				st.LastSessionWebhook = strings.TrimSpace(msg.SessionWebhook)
			}
		})
	}
	if rt.OnInbound != nil {
		_ = rt.OnInbound(ctx, InboundMessage{
			ExternalMessageID: msgID,
			SenderID:          senderID,
			ChatID:            chatID,
			ChatType:          chatType,
			Content:           text,
			Mentioned:         mentioned,
			SessionWebhook:    strings.TrimSpace(msg.SessionWebhook),
			Timestamp:         time.Now().UTC(),
		})
	}
	return false
}

func (a *DingTalkAdapter) accessToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	if a.token != "" && time.Now().Before(a.tokenExpiry) {
		tok := a.token
		a.mu.Unlock()
		return tok, nil
	}
	a.mu.Unlock()

	body, _ := json.Marshal(map[string]string{
		"appKey":    strings.TrimSpace(a.channel.Config.ClientID),
		"appSecret": strings.TrimSpace(a.secrets.ClientSecret),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1.0/oauth2/accessToken", bytes.NewReader(body))
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
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("dingtalk accessToken HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed struct {
		AccessToken string    `json:"accessToken"`
		ExpireIn    flexInt64 `json:"expireIn"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return "", errors.New("dingtalk accessToken response is empty")
	}
	ttl := time.Duration(parsed.ExpireIn.Int64()-120) * time.Second
	if ttl < time.Minute {
		ttl = 5 * time.Minute
	}
	a.mu.Lock()
	a.token = parsed.AccessToken
	a.tokenExpiry = time.Now().Add(ttl)
	a.mu.Unlock()
	return parsed.AccessToken, nil
}

func (a *DingTalkAdapter) Send(ctx context.Context, msg OutboundMessage) error {
	fullText := FormatOutboundForPlatform(domain.ChannelPlatformDingTalk, msg.Text, msg.CardURL)
	chunks := SplitMessage(fullText, dingTalkMaxRunes)
	if a.channel.Transport == domain.ChannelTransportWebhook {
		webhookURL := strings.TrimSpace(a.secrets.WebhookURL)
		if webhookURL == "" {
			return errors.New("dingtalk webhook_url is not configured")
		}
		signedURL, err := signDingTalkWebhookURL(webhookURL, a.secrets.WebhookSecret, time.Now())
		if err != nil {
			return err
		}
		for _, chunk := range chunks {
			if err := postJSONExpectOK(ctx, a.httpClient, signedURL, map[string]any{
				"msgtype": "markdown",
				"markdown": map[string]string{
					"title": "CycleDock 通知",
					"text":  chunk,
				},
			}, "errcode"); err != nil {
				return fmt.Errorf("dingtalk webhook send: %w", err)
			}
		}
		return nil
	}

	// Stream 模式下优先复用入站回调附带的 sessionWebhook；若 sessionWebhook 已过期或发送失败，
	// 并且具备有效的单聊 chatID 与应用凭据，则自动清空过期 sessionWebhook 并回退到 OpenAPI 发送。
	a.mu.Lock()
	sessionWebhook := strings.TrimSpace(msg.SessionWebhook)
	if sessionWebhook == "" {
		sessionWebhook = a.lastSessionWebhook
	}
	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" {
		chatID = a.lastChatID
	}
	chatType := strings.TrimSpace(msg.ChatType)
	saveState := a.saveState
	a.mu.Unlock()

	if sessionWebhook != "" {
		var webhookErr error
		for _, chunk := range chunks {
			if err := postJSONExpectOK(ctx, a.httpClient, sessionWebhook, map[string]any{
				"msgtype": "markdown",
				"markdown": map[string]string{
					"title": "CycleDock 回复",
					"text":  chunk,
				},
			}, "errcode"); err != nil {
				webhookErr = fmt.Errorf("dingtalk sessionWebhook send: %w", err)
				break
			}
		}
		if webhookErr == nil {
			return nil
		}
		// 若不是群聊且有单聊目标与客户端凭据，自动清理失效的 sessionWebhook（含持久化态）并回退 OpenAPI 发送。
		if chatID == "" || strings.EqualFold(chatType, "group") ||
			strings.TrimSpace(a.channel.Config.ClientID) == "" ||
			strings.TrimSpace(a.secrets.ClientSecret) == "" {
			return webhookErr
		}
		a.mu.Lock()
		if a.lastSessionWebhook == sessionWebhook {
			a.lastSessionWebhook = ""
		}
		a.mu.Unlock()
		if saveState != nil {
			_ = saveState(ctx, func(st *store.RemoteChannelRuntimeState) {
				if st.LastSessionWebhook == sessionWebhook {
					st.LastSessionWebhook = ""
				}
			})
		}
	}

	if chatID == "" {
		return fmt.Errorf("%w: dingtalk target chat_id or sessionWebhook is empty", ErrTargetNotBound)
	}
	token, err := a.accessToken(ctx)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		mdParamBytes, _ := json.Marshal(map[string]string{
			"title": "CycleDock 回复",
			"text":  chunk,
		})
		if err := a.batchSendOnce(ctx, token, chatID, "sampleMarkdown", string(mdParamBytes)); err != nil {
			// sampleMarkdown 若因特殊语法被拒收，自动降级 sampleText 保证消息不丢。
			plain := MarkdownToPlainText(chunk)
			if plain == "" {
				plain = chunk
			}
			textParamBytes, _ := json.Marshal(map[string]string{"content": plain})
			if fallbackErr := a.batchSendOnce(ctx, token, chatID, "sampleText", string(textParamBytes)); fallbackErr != nil {
				return fmt.Errorf("%w (sampleText fallback failed: %v)", err, fallbackErr)
			}
		}
	}
	return nil
}

func (a *DingTalkAdapter) batchSendOnce(ctx context.Context, token, chatID, msgKey, msgParam string) error {
	bodyBytes, _ := json.Marshal(map[string]any{
		"robotCode": strings.TrimSpace(a.channel.Config.ClientID),
		"userIds":   []string{chatID},
		"msgKey":    msgKey,
		"msgParam":  msgParam,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1.0/robot/oToMessages/batchSend", bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("dingtalk batchSend HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (a *DingTalkAdapter) Test(ctx context.Context, testText string) error {
	if a.channel.Transport == domain.ChannelTransportWebhook {
		return a.Send(ctx, OutboundMessage{Text: testText})
	}
	if _, err := a.accessToken(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	hasTarget := a.lastSessionWebhook != "" || a.lastChatID != ""
	a.mu.Unlock()
	if hasTarget {
		return a.Send(ctx, OutboundMessage{Text: testText})
	}
	return nil
}
