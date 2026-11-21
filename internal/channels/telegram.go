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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	defaultTelegramBaseURL = "https://api.telegram.org"
	telegramMaxRunes       = 4000
)

// TelegramAdapter 实现 Telegram Bot API 长轮询（getUpdates）与消息发送，
// 原生支持群 Topics（message_thread_id）绑定和可选代理。
type TelegramAdapter struct {
	channel    store.RemoteChannel
	secrets    store.RemoteChannelSecrets
	baseURL    string
	httpClient *http.Client

	mu         sync.Mutex
	offset     int64
	lastChatID string
	lastThread int64
}

func NewTelegramAdapter(ch store.RemoteChannel, secrets store.RemoteChannelSecrets, client *http.Client) (*TelegramAdapter, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(ch.Config.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultTelegramBaseURL
	}
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if proxyRaw := strings.TrimSpace(ch.Config.ProxyURL); proxyRaw != "" {
			parsed, err := url.Parse(proxyRaw)
			if err != nil {
				return nil, fmt.Errorf("invalid telegram proxy_url: %w", err)
			}
			transport.Proxy = http.ProxyURL(parsed)
		}
		client = &http.Client{
			Timeout:   45 * time.Second,
			Transport: transport,
		}
	}
	lastChat := strings.TrimSpace(ch.Config.TargetChatID)
	if lastChat == "" {
		lastChat = strings.TrimSpace(ch.RuntimeState.LastChatID)
	}
	lastThread := ch.Config.MessageThreadID
	if lastThread == 0 && strings.TrimSpace(ch.RuntimeState.LastThreadID) != "" {
		if parsed, err := strconv.ParseInt(strings.TrimSpace(ch.RuntimeState.LastThreadID), 10, 64); err == nil {
			lastThread = parsed
		}
	}
	return &TelegramAdapter{
		channel:    ch,
		secrets:    secrets,
		baseURL:    baseURL,
		httpClient: client,
		offset:     ch.RuntimeState.TelegramOffset,
		lastChatID: lastChat,
		lastThread: lastThread,
	}, nil
}

func (a *TelegramAdapter) Platform() domain.ChannelPlatform {
	return domain.ChannelPlatformTelegram
}

func (a *TelegramAdapter) Run(ctx context.Context, rt AdapterRuntime) error {
	if strings.TrimSpace(a.secrets.BotToken) == "" {
		err := errors.New("telegram bot_token is not configured")
		if rt.OnStatus != nil {
			rt.OnStatus(ctx, domain.ChannelRuntimeError, err.Error(), false)
		}
		return err
	}
	botUsername, err := a.fetchBotUsername(ctx)
	if err != nil {
		return err
	}
	if rt.OnStatus != nil {
		rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := a.pollOnce(ctx, rt, botUsername); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
}

func (a *TelegramAdapter) fetchBotUsername(ctx context.Context) (string, error) {
	raw, err := a.apiCall(ctx, "getMe", nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("telegram getMe failed: %s", resp.Description)
	}
	return strings.ToLower(strings.TrimSpace(resp.Result.Username)), nil
}

func (a *TelegramAdapter) pollOnce(ctx context.Context, rt AdapterRuntime, botUsername string) error {
	a.mu.Lock()
	offset := a.offset
	a.mu.Unlock()

	params := map[string]any{
		"timeout":         25,
		"allowed_updates": []string{"message"},
	}
	if offset > 0 {
		params["offset"] = offset
	}
	raw, err := a.apiCall(ctx, "getUpdates", params)
	if err != nil {
		return err
	}
	var resp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      []struct {
			UpdateID flexInt64 `json:"update_id"`
			Message  *struct {
				MessageID       flexInt64 `json:"message_id"`
				MessageThreadID flexInt64 `json:"message_thread_id"`
				Date            flexInt64 `json:"date"`
				Text            string    `json:"text"`
				Caption         string    `json:"caption"`
				From            *struct {
					ID       flexInt64 `json:"id"`
					IsBot    bool      `json:"is_bot"`
					Username string    `json:"username"`
				} `json:"from"`
				Chat struct {
					ID   flexInt64 `json:"id"`
					Type string    `json:"type"`
				} `json:"chat"`
				Entities []struct {
					Type   string    `json:"type"`
					Offset flexInt64 `json:"offset"`
					Length flexInt64 `json:"length"`
				} `json:"entities"`
			} `json:"message"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("telegram getUpdates failed: %s", resp.Description)
	}
	if rt.OnStatus != nil {
		rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
	}

	var maxOffset int64
	stateChanged := false
	for _, upd := range resp.Result {
		updateID := upd.UpdateID.Int64()
		if updateID+1 > maxOffset {
			maxOffset = updateID + 1
		}
		m := upd.Message
		if m == nil || (m.From != nil && m.From.IsBot) {
			continue
		}
		chatIDStr := strconv.FormatInt(m.Chat.ID.Int64(), 10)
		threadID := m.MessageThreadID.Int64()
		// 若通道绑定了特定 chat_id 或 Topic（message_thread_id），严格过滤避免跨 Topic 串台。
		if boundChat := strings.TrimSpace(a.channel.Config.TargetChatID); boundChat != "" && boundChat != chatIDStr {
			continue
		}
		if a.channel.Config.MessageThreadID > 0 && threadID != a.channel.Config.MessageThreadID {
			continue
		}
		text := strings.TrimSpace(m.Text)
		if text == "" {
			text = strings.TrimSpace(m.Caption)
		}
		if text == "" {
			continue
		}
		chatType := "direct"
		if m.Chat.Type == "group" || m.Chat.Type == "supergroup" {
			chatType = "group"
		}
		mentioned := chatType == "direct"
		if !mentioned && botUsername != "" {
			lower := strings.ToLower(text)
			if strings.Contains(lower, "@"+botUsername) {
				mentioned = true
				text = strings.TrimSpace(stripCaseInsensitive(text, "@"+botUsername))
			}
		}
		senderID := chatIDStr
		if m.From != nil && m.From.ID.Int64() != 0 {
			senderID = strconv.FormatInt(m.From.ID.Int64(), 10)
		}

		a.mu.Lock()
		a.lastChatID = chatIDStr
		if threadID > 0 {
			a.lastThread = threadID
		}
		a.mu.Unlock()
		stateChanged = true

		msgTime := time.Now().UTC()
		if m.Date.Int64() > 0 {
			msgTime = time.Unix(m.Date.Int64(), 0).UTC()
		}
		if rt.OnInbound != nil {
			_ = rt.OnInbound(ctx, InboundMessage{
				ExternalMessageID: fmt.Sprintf("%s:%d", chatIDStr, m.MessageID.Int64()),
				SenderID:          senderID,
				ChatID:            chatIDStr,
				ChatType:          chatType,
				ThreadID:          formatNonZeroInt64(threadID),
				Content:           text,
				Mentioned:         mentioned,
				Timestamp:         msgTime,
			})
		}
	}

	if maxOffset > offset {
		a.mu.Lock()
		a.offset = maxOffset
		a.mu.Unlock()
		stateChanged = true
	}
	if stateChanged && rt.SaveState != nil {
		a.mu.Lock()
		nextOffset := a.offset
		nextChat := a.lastChatID
		nextThread := formatNonZeroInt64(a.lastThread)
		a.mu.Unlock()
		_ = rt.SaveState(ctx, func(st *store.RemoteChannelRuntimeState) {
			st.TelegramOffset = nextOffset
			if nextChat != "" {
				st.LastChatID = nextChat
			}
			if nextThread != "" {
				st.LastThreadID = nextThread
			}
		})
	}
	return nil
}

func (a *TelegramAdapter) Send(ctx context.Context, msg OutboundMessage) error {
	if strings.TrimSpace(a.secrets.BotToken) == "" {
		return errors.New("telegram bot_token is not configured")
	}
	chatID := strings.TrimSpace(msg.ChatID)
	a.mu.Lock()
	if chatID == "" {
		chatID = a.lastChatID
	}
	threadID := a.channel.Config.MessageThreadID
	if threadID == 0 {
		threadID = a.lastThread
	}
	a.mu.Unlock()
	if parsed, err := strconv.ParseInt(strings.TrimSpace(msg.ThreadID), 10, 64); err == nil && parsed > 0 {
		threadID = parsed
	}
	if chatID == "" {
		return errors.New("telegram target chat_id is empty")
	}
	fullText := FormatOutboundWithLink(msg.Text, msg.CardURL)
	for _, chunk := range SplitMessage(fullText, telegramMaxRunes) {
		body := map[string]any{
			"chat_id":                  chatID,
			"text":                     chunk,
			"disable_web_page_preview": true,
		}
		if threadID > 0 {
			body["message_thread_id"] = threadID
		}
		raw, err := a.apiCall(ctx, "sendMessage", body)
		if err != nil {
			return err
		}
		var resp struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(raw, &resp); err == nil && !resp.OK {
			return fmt.Errorf("telegram sendMessage failed: %s", resp.Description)
		}
	}
	return nil
}

func (a *TelegramAdapter) Test(ctx context.Context, testText string) error {
	if _, err := a.fetchBotUsername(ctx); err != nil {
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

func (a *TelegramAdapter) apiCall(ctx context.Context, method string, body map[string]any) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/bot%s/%s", a.baseURL, strings.TrimSpace(a.secrets.BotToken), method)
	var reqBody io.Reader
	httpMethod := http.MethodGet
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(encoded)
		httpMethod = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, httpMethod, endpoint, reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
		return nil, fmt.Errorf("telegram %s HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func formatNonZeroInt64(v int64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}

func stripCaseInsensitive(text, needle string) string {
	lower := strings.ToLower(text)
	target := strings.ToLower(needle)
	idx := strings.Index(lower, target)
	if idx < 0 {
		return text
	}
	return text[:idx] + text[idx+len(needle):]
}
