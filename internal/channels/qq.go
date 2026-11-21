package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	defaultQQAPIBaseURL  = "https://api.sgroup.qq.com"
	defaultQQAuthBaseURL = "https://bots.qq.com"
	qqIntentsGroupAndC2C = 1 << 25
	qqMaxRunes           = 2000
)

// QQAdapter 实现 QQ 开放平台官方 Bot API v2 + WebSocket Gateway（app_id + secret，C2C 单聊与群聊）。
type QQAdapter struct {
	channel    store.RemoteChannel
	secrets    store.RemoteChannelSecrets
	apiBaseURL string
	authURL    string
	wsURL      string
	httpClient *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
	lastChatID  string
	lastChatTy  string
	lastMsgID   string
	msgSeq      int
}

func NewQQAdapter(ch store.RemoteChannel, secrets store.RemoteChannelSecrets, client *http.Client) *QQAdapter {
	apiBase := strings.TrimRight(strings.TrimSpace(ch.Config.BaseURL), "/")
	authURL := defaultQQAuthBaseURL + "/app/getAppAccessToken"
	if apiBase == "" {
		apiBase = defaultQQAPIBaseURL
	} else {
		// 测试或自定义网关时，auth 端点也复用自定义 BaseURL。
		authURL = apiBase + "/app/getAppAccessToken"
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	lastChat := strings.TrimSpace(ch.Config.TargetChatID)
	if lastChat == "" {
		lastChat = strings.TrimSpace(ch.RuntimeState.LastChatID)
	}
	lastChatTy := strings.TrimSpace(ch.Config.TargetChatType)
	if lastChatTy == "" {
		lastChatTy = strings.TrimSpace(ch.RuntimeState.LastChatType)
	}
	return &QQAdapter{
		channel:    ch,
		secrets:    secrets,
		apiBaseURL: apiBase,
		authURL:    authURL,
		wsURL:      strings.TrimSpace(ch.Config.WSURL),
		httpClient: client,
		lastChatID: lastChat,
		lastChatTy: lastChatTy,
		lastMsgID:  strings.TrimSpace(ch.RuntimeState.LastMessageID),
	}
}

func (a *QQAdapter) Platform() domain.ChannelPlatform {
	return domain.ChannelPlatformQQ
}

func (a *QQAdapter) accessToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	if a.token != "" && time.Now().Before(a.tokenExpiry) {
		tok := a.token
		a.mu.Unlock()
		return tok, nil
	}
	a.mu.Unlock()

	if strings.TrimSpace(a.channel.Config.AppID) == "" || strings.TrimSpace(a.secrets.Secret) == "" {
		return "", errors.New("qq app_id and secret are required")
	}
	body, _ := json.Marshal(map[string]string{
		"appId":        strings.TrimSpace(a.channel.Config.AppID),
		"clientSecret": strings.TrimSpace(a.secrets.Secret),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.authURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request qq access token: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("qq getAppAccessToken HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   any    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return "", errors.New("qq getAppAccessToken returned empty access_token")
	}
	expireSec := 7200
	switch v := parsed.ExpiresIn.(type) {
	case float64:
		expireSec = int(v)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			expireSec = n
		}
	}
	ttl := time.Duration(expireSec-120) * time.Second
	if ttl < time.Minute {
		ttl = 5 * time.Minute
	}
	a.mu.Lock()
	a.token = parsed.AccessToken
	a.tokenExpiry = time.Now().Add(ttl)
	a.mu.Unlock()
	return parsed.AccessToken, nil
}

func (a *QQAdapter) fetchGatewayURL(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.apiBaseURL+"/gateway", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "QQBot "+token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request qq gateway: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("qq gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.URL) == "" {
		return "", errors.New("qq gateway returned empty url")
	}
	return strings.TrimSpace(parsed.URL), nil
}

func (a *QQAdapter) Run(ctx context.Context, rt AdapterRuntime) error {
	token, err := a.accessToken(ctx)
	if err != nil {
		if rt.OnStatus != nil {
			rt.OnStatus(ctx, domain.ChannelRuntimeError, err.Error(), false)
		}
		return err
	}
	wsEndpoint := a.wsURL
	if wsEndpoint == "" {
		wsEndpoint, err = a.fetchGatewayURL(ctx, token)
		if err != nil {
			return err
		}
	}
	conn, err := dialWS(ctx, wsEndpoint)
	if err != nil {
		return fmt.Errorf("dial qq gateway websocket: %w", err)
	}
	defer conn.Close()

	var lastSeq int64
	hbStop := make(chan struct{})
	defer close(hbStop)
	hbStarted := false

	for {
		_, data, err := conn.Read()
		if err != nil {
			return err
		}
		var payload struct {
			Op int             `json:"op"`
			S  *int64          `json:"s"`
			T  string          `json:"t"`
			D  json.RawMessage `json:"d"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			continue
		}
		if payload.S != nil {
			lastSeq = *payload.S
		}
		switch payload.Op {
		case 10: // Hello -> 发送 Identify 并启动心跳
			var hello struct {
				HeartbeatInterval int64 `json:"heartbeat_interval"`
			}
			_ = json.Unmarshal(payload.D, &hello)
			if err := conn.WriteJSON(map[string]any{
				"op": 2,
				"d": map[string]any{
					"token":   "QQBot " + token,
					"intents": qqIntentsGroupAndC2C,
					"shard":   []int{0, 1},
				},
			}); err != nil {
				return fmt.Errorf("qq identify failed: %w", err)
			}
			if !hbStarted {
				hbStarted = true
				interval := time.Duration(hello.HeartbeatInterval) * time.Millisecond
				if interval <= 0 {
					interval = 30 * time.Second
				}
				go func() {
					ticker := time.NewTicker(interval)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-hbStop:
							return
						case <-ticker.C:
							_ = conn.WriteJSON(map[string]any{"op": 1, "d": lastSeq})
						}
					}
				}()
			}
		case 11: // Heartbeat ACK
			if rt.OnStatus != nil {
				rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
			}
		case 0: // Dispatch
			if payload.T == "READY" {
				if rt.OnStatus != nil {
					rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
				}
				continue
			}
			if payload.T == "C2C_MESSAGE_CREATE" || payload.T == "GROUP_AT_MESSAGE_CREATE" {
				a.handleDispatchMessage(ctx, payload.T, payload.D, rt)
			}
		}
	}
}

func (a *QQAdapter) handleDispatchMessage(ctx context.Context, eventType string, raw json.RawMessage, rt AdapterRuntime) {
	var msg struct {
		ID          string `json:"id"`
		Content     string `json:"content"`
		GroupOpenID string `json:"group_openid"`
		Author      struct {
			UserOpenID   string `json:"user_openid"`
			MemberOpenID string `json:"member_openid"`
			ID           string `json:"id"`
		} `json:"author"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		return
	}
	isGroup := eventType == "GROUP_AT_MESSAGE_CREATE" || strings.TrimSpace(msg.GroupOpenID) != ""
	senderID := strings.TrimSpace(msg.Author.UserOpenID)
	if senderID == "" {
		senderID = strings.TrimSpace(msg.Author.MemberOpenID)
	}
	if senderID == "" {
		senderID = strings.TrimSpace(msg.Author.ID)
	}
	chatID := senderID
	chatType := "direct"
	if isGroup {
		chatType = "group"
		chatID = strings.TrimSpace(msg.GroupOpenID)
	}
	msgID := strings.TrimSpace(msg.ID)
	if msgID == "" {
		msgID = uuid.NewString()
	}

	a.mu.Lock()
	a.lastChatID = chatID
	a.lastChatTy = chatType
	a.lastMsgID = msgID
	a.mu.Unlock()
	if rt.SaveState != nil {
		_ = rt.SaveState(ctx, func(st *store.RemoteChannelRuntimeState) {
			st.LastChatID = chatID
			st.LastChatType = chatType
			st.LastMessageID = msgID
		})
	}
	if rt.OnInbound != nil {
		_ = rt.OnInbound(ctx, InboundMessage{
			ExternalMessageID: msgID,
			SenderID:          senderID,
			ChatID:            chatID,
			ChatType:          chatType,
			Content:           text,
			Mentioned:         true, // QQ 群聊仅投递 GROUP_AT_MESSAGE_CREATE
			Timestamp:         time.Now().UTC(),
		})
	}
}

func (a *QQAdapter) Send(ctx context.Context, msg OutboundMessage) error {
	token, err := a.accessToken(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" {
		chatID = a.lastChatID
	}
	chatType := strings.TrimSpace(msg.ChatType)
	if chatType == "" {
		chatType = a.lastChatTy
	}
	replyMsgID := a.lastMsgID
	a.mu.Unlock()

	if chatID == "" {
		return errors.New("qq target openid is empty")
	}
	endpoint := a.apiBaseURL + "/v2/users/" + chatID + "/messages"
	if strings.EqualFold(chatType, "group") {
		endpoint = a.apiBaseURL + "/v2/groups/" + chatID + "/messages"
	}
	fullText := FormatOutboundWithLink(msg.Text, msg.CardURL)
	for _, chunk := range SplitMessage(fullText, qqMaxRunes) {
		a.mu.Lock()
		a.msgSeq++
		seq := a.msgSeq
		a.mu.Unlock()
		payload := map[string]any{
			"content":  chunk,
			"msg_type": 0,
			"msg_seq":  seq,
		}
		if replyMsgID != "" {
			payload["msg_id"] = replyMsgID
		}
		bodyBytes, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "QQBot "+token)
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return err
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("qq send HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
	}
	return nil
}

func (a *QQAdapter) Test(ctx context.Context, testText string) error {
	if _, err := a.accessToken(ctx); err != nil {
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
