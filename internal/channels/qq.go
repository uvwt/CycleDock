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
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	defaultQQAPIBaseURL    = "https://api.sgroup.qq.com"
	defaultQQAuthBaseURL   = "https://bots.qq.com"
	qqIntentsGroupAndC2C   = 1 << 25
	qqMaxRunes             = 2000
	qqPassiveMsgMaxAge     = 4 * time.Minute // QQ 被动回复 msg_id 有效期 5 分钟，留 1 分钟安全余量
)

// QQAdapter 实现 QQ 开放平台官方 Bot API v2 + WebSocket Gateway（app_id + secret，C2C 单聊与群聊）。
type QQAdapter struct {
	channel    store.RemoteChannel
	secrets    store.RemoteChannelSecrets
	apiBaseURL string
	authURL    string
	wsURL      string
	httpClient *http.Client
	nowFn      func() time.Time

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
	lastChatID  string
	lastChatTy  string
	lastMsgID   string
	lastMsgAt   time.Time
	msgSeq      int
	saveState   StatePersister
}

// qqLastSeedSeq 保证同进程内短时间重建 QQAdapter 时 msg_seq 种子仍严格递增，避免同秒重建与旧 msg_id+msg_seq 碰撞。
var qqLastSeedSeq atomic.Int64

func nextQQSeedSeq() int {
	base := time.Now().Unix()%900000 + 1000
	for {
		prev := qqLastSeedSeq.Load()
		next := base
		if next <= prev {
			next = prev + 100
		}
		if qqLastSeedSeq.CompareAndSwap(prev, next) {
			return int(next)
		}
	}
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
	var lastMsgAt time.Time
	if ch.RuntimeState.LastMessageAt > 0 {
		lastMsgAt = time.Unix(ch.RuntimeState.LastMessageAt, 0).UTC()
	}
	// 使用严格递增的高位种子初始化 msgSeq，防止进程重启或同秒重建实例后与旧 msg_id+msg_seq 碰撞被 QQ 拒收。
	seedSeq := nextQQSeedSeq()
	return &QQAdapter{
		channel:    ch,
		secrets:    secrets,
		apiBaseURL: apiBase,
		authURL:    authURL,
		wsURL:      strings.TrimSpace(ch.Config.WSURL),
		httpClient: client,
		nowFn:      time.Now,
		lastChatID: lastChat,
		lastChatTy: lastChatTy,
		lastMsgID:  strings.TrimSpace(ch.RuntimeState.LastMessageID),
		lastMsgAt:  lastMsgAt,
		msgSeq:     seedSeq,
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
		AccessToken string    `json:"access_token"`
		ExpiresIn   flexInt64 `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return "", errors.New("qq getAppAccessToken returned empty access_token")
	}
	expireSec := parsed.ExpiresIn.Int64()
	if expireSec <= 0 {
		expireSec = 7200
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

	a.mu.Lock()
	a.saveState = rt.SaveState
	a.mu.Unlock()

	var lastSeq atomic.Int64
	hbStop := make(chan struct{})
	defer close(hbStop)
	hbStarted := false
	log := rtLogger(rt)

	for {
		_, data, err := conn.Read()
		if err != nil {
			return err
		}
		var payload struct {
			Op flexInt64       `json:"op"`
			S  *flexInt64      `json:"s"`
			T  string          `json:"t"`
			D  json.RawMessage `json:"d"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			continue
		}
		if payload.S != nil {
			lastSeq.Store(payload.S.Int64())
		}
		switch payload.Op.Int() {
		case 10: // Hello -> 发送 Identify 并启动心跳
			var hello struct {
				HeartbeatInterval flexInt64 `json:"heartbeat_interval"`
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
				interval := time.Duration(hello.HeartbeatInterval.Int64()) * time.Millisecond
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
							_ = conn.WriteJSON(map[string]any{"op": 1, "d": lastSeq.Load()})
						}
					}
				}()
			}
		case 11: // Heartbeat ACK
			if rt.OnStatus != nil {
				rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
			}
		case 7: // Reconnect
			log.Warn("qq gateway requested reconnect (op=7)")
			return errors.New("qq gateway requested reconnect")
		case 9: // Invalid Session
			log.Warn("qq gateway rejected session (op=9, check app_id/secret/intents)")
			return errors.New("qq gateway invalid session (op=9)")
		case 0: // Dispatch
			if payload.T == "READY" {
				log.Info("qq gateway READY received")
				if rt.OnStatus != nil {
					rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
				}
				continue
			}
			if payload.T == "C2C_MESSAGE_CREATE" || payload.T == "GROUP_AT_MESSAGE_CREATE" {
				a.handleDispatchMessage(ctx, payload.T, payload.D, rt)
			} else if payload.T != "" {
				log.Debug("qq gateway ignored dispatch event", "event_type", payload.T)
			}
		}
	}
}

func (a *QQAdapter) handleDispatchMessage(ctx context.Context, eventType string, raw json.RawMessage, rt AdapterRuntime) {
	var msg struct {
		ID          flexString `json:"id"`
		Content     string     `json:"content"`
		GroupOpenID flexString `json:"group_openid"`
		Author      struct {
			UserOpenID   flexString `json:"user_openid"`
			MemberOpenID flexString `json:"member_openid"`
			ID           flexString `json:"id"`
		} `json:"author"`
		Attachments []struct {
			ContentType string     `json:"content_type"`
			FileName    string     `json:"filename"`
			URL         string     `json:"url"`
			Size        flexInt64  `json:"size"`
			Width       flexInt64  `json:"width"`
			Height      flexInt64  `json:"height"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	text := strings.TrimSpace(msg.Content)
	atts := make([]InboundAttachment, 0, len(msg.Attachments))
	for _, rawAtt := range msg.Attachments {
		fileName := strings.TrimSpace(rawAtt.FileName)
		mimeType := strings.TrimSpace(rawAtt.ContentType)
		kind := classifyAttachmentKind(mimeType, fileName)
		att := InboundAttachment{
			Kind:      kind,
			FileName:  fileName,
			MimeType:  mimeType,
			SizeBytes: rawAtt.Size.Int64(),
		}
		// 仅图片与文件类型保留下载地址交由公共层受限拉取；语音/视频不盲目下载，直接由公共层生成明确占位说明。
		if kind == InboundAttachmentImage || kind == InboundAttachmentFile {
			if dlURL := strings.TrimSpace(rawAtt.URL); dlURL != "" {
				att.DownloadURL = dlURL
			}
		}
		atts = append(atts, att)
	}
	if text == "" && len(atts) == 0 {
		return
	}
	groupOpenID := strings.TrimSpace(msg.GroupOpenID.String())
	isGroup := eventType == "GROUP_AT_MESSAGE_CREATE" || groupOpenID != ""
	senderID := strings.TrimSpace(msg.Author.UserOpenID.String())
	if senderID == "" {
		senderID = strings.TrimSpace(msg.Author.MemberOpenID.String())
	}
	if senderID == "" {
		senderID = strings.TrimSpace(msg.Author.ID.String())
	}
	chatID := senderID
	chatType := "direct"
	if isGroup {
		chatType = "group"
		chatID = groupOpenID
	}
	msgID := strings.TrimSpace(msg.ID.String())
	if msgID == "" {
		msgID = uuid.NewString()
	}

	now := a.now()
	a.mu.Lock()
	a.lastChatID = chatID
	a.lastChatTy = chatType
	a.lastMsgID = msgID
	a.lastMsgAt = now
	a.mu.Unlock()
	if rt.SaveState != nil {
		_ = rt.SaveState(ctx, func(st *store.RemoteChannelRuntimeState) {
			st.LastChatID = chatID
			st.LastChatType = chatType
			st.LastMessageID = msgID
			st.LastMessageAt = now.Unix()
		})
	}
	if rt.OnInbound != nil {
		_ = rt.OnInbound(ctx, InboundMessage{
			ExternalMessageID: msgID,
			SenderID:          senderID,
			ChatID:            chatID,
			ChatType:          chatType,
			Content:           text,
			Attachments:       atts,
			Mentioned:         true, // QQ 群聊仅投递 GROUP_AT_MESSAGE_CREATE
			Timestamp:         now,
		})
	}
}

func (a *QQAdapter) now() time.Time {
	if a.nowFn != nil {
		return a.nowFn().UTC()
	}
	return time.Now().UTC()
}

func (a *QQAdapter) Send(ctx context.Context, msg OutboundMessage) error {
	token, err := a.accessToken(ctx)
	if err != nil {
		return err
	}
	now := a.now()
	a.mu.Lock()
	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" {
		chatID = a.lastChatID
	}
	chatType := strings.TrimSpace(msg.ChatType)
	if chatType == "" {
		chatType = a.lastChatTy
	}
	replyMsgID := ""
	if a.lastMsgID != "" && !a.lastMsgAt.IsZero() && now.Sub(a.lastMsgAt) <= qqPassiveMsgMaxAge {
		replyMsgID = a.lastMsgID
	}
	a.mu.Unlock()

	if chatID == "" {
		return fmt.Errorf("%w: qq target openid is empty", ErrTargetNotBound)
	}
	endpoint := a.apiBaseURL + "/v2/users/" + chatID + "/messages"
	if strings.EqualFold(chatType, "group") {
		endpoint = a.apiBaseURL + "/v2/groups/" + chatID + "/messages"
	}
	fullText := FormatOutboundForPlatform(domain.ChannelPlatformQQ, msg.Text, msg.CardURL)
	for _, chunk := range SplitMessage(fullText, qqMaxRunes) {
		if err := a.sendChunk(ctx, token, endpoint, chunk, replyMsgID); err != nil {
			if replyMsgID == "" {
				return err
			}
			// 被动回复 msg_id 可能已过期或达到单 msg_id 回复上限（如 5 条），清空过期 msg_id 后自动降级为主动消息重试一次。
			expiredMsgID := replyMsgID
			a.mu.Lock()
			if a.lastMsgID == expiredMsgID {
				a.lastMsgID = ""
				a.lastMsgAt = time.Time{}
			}
			saveState := a.saveState
			a.mu.Unlock()
			if saveState != nil {
				_ = saveState(ctx, func(st *store.RemoteChannelRuntimeState) {
					if st.LastMessageID == expiredMsgID {
						st.LastMessageID = ""
						st.LastMessageAt = 0
					}
				})
			}
			replyMsgID = ""
			if fallbackErr := a.sendChunk(ctx, token, endpoint, chunk, ""); fallbackErr != nil {
				return fmt.Errorf("%v (fallback proactive send failed: %w)", err, fallbackErr)
			}
		}
	}
	return nil
}

func (a *QQAdapter) sendChunk(ctx context.Context, token, endpoint, chunk, replyMsgID string) error {
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
