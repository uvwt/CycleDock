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
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/CycleDock/internal/attachments"
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
	rtLogger(rt).Info("telegram bot authenticated", "bot_username", botUsername)
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
				Photo []struct {
					FileID   string    `json:"file_id"`
					FileSize flexInt64 `json:"file_size"`
					Width    flexInt64 `json:"width"`
					Height   flexInt64 `json:"height"`
				} `json:"photo"`
				Document *struct {
					FileID   string    `json:"file_id"`
					FileName string    `json:"file_name"`
					MimeType string    `json:"mime_type"`
					FileSize flexInt64 `json:"file_size"`
				} `json:"document"`
				Voice *struct {
					FileID   string    `json:"file_id"`
					Duration flexInt64 `json:"duration"`
					MimeType string    `json:"mime_type"`
					FileSize flexInt64 `json:"file_size"`
				} `json:"voice"`
				Audio *struct {
					FileID   string    `json:"file_id"`
					Duration flexInt64 `json:"duration"`
					FileName string    `json:"file_name"`
					Title    string    `json:"title"`
					MimeType string    `json:"mime_type"`
					FileSize flexInt64 `json:"file_size"`
				} `json:"audio"`
				Video *struct {
					FileID   string    `json:"file_id"`
					Duration flexInt64 `json:"duration"`
					FileName string    `json:"file_name"`
					MimeType string    `json:"mime_type"`
					FileSize flexInt64 `json:"file_size"`
				} `json:"video"`
				Sticker *struct {
					FileID     string `json:"file_id"`
					Emoji      string `json:"emoji"`
					IsAnimated bool   `json:"is_animated"`
					IsVideo    bool   `json:"is_video"`
				} `json:"sticker"`
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
	}
	if maxOffset > offset {
		a.mu.Lock()
		a.offset = maxOffset
		a.mu.Unlock()
		stateChanged = true
	}
	// 先持久化最新 offset 与会话定位，避免单条 OnInbound 报错或断开重连后重复拉取旧消息，
	// 并确保 Dispatcher 立刻能从数据库 RuntimeState 读到首条私聊的 LastChatID。
	for _, upd := range resp.Result {
		m := upd.Message
		if m == nil || (m.From != nil && m.From.IsBot) {
			continue
		}
		chatIDStr := strconv.FormatInt(m.Chat.ID.Int64(), 10)
		threadID := m.MessageThreadID.Int64()
		if boundChat := strings.TrimSpace(a.channel.Config.TargetChatID); boundChat != "" && boundChat != chatIDStr {
			continue
		}
		if a.channel.Config.MessageThreadID > 0 && threadID != a.channel.Config.MessageThreadID {
			continue
		}
		a.mu.Lock()
		if a.lastChatID != chatIDStr {
			a.lastChatID = chatIDStr
			stateChanged = true
		}
		if threadID > 0 && a.lastThread != threadID {
			a.lastThread = threadID
			stateChanged = true
		}
		a.mu.Unlock()
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
		stateChanged = false
	}

	for _, upd := range resp.Result {
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
		atts := make([]InboundAttachment, 0, 2)
		if len(m.Photo) > 0 {
			best := m.Photo[len(m.Photo)-1]
			bestScore := best.Width.Int64() * best.Height.Int64()
			for _, p := range m.Photo {
				score := p.Width.Int64() * p.Height.Int64()
				if score > bestScore || (score == bestScore && p.FileSize.Int64() > best.FileSize.Int64()) {
					best = p
					bestScore = score
				}
			}
			att := InboundAttachment{
				Kind:      InboundAttachmentImage,
				FileName:  "photo.jpg",
				MimeType:  "image/jpeg",
				SizeBytes: best.FileSize.Int64(),
			}
			if data, filePath, dlErr := a.downloadTelegramFile(ctx, best.FileID, attachments.DefaultMaxUploadBytes); dlErr == nil {
				att.RawData = data
				if base := strings.TrimSpace(path.Base(filePath)); base != "" && base != "." && base != "/" {
					att.FileName = base
				}
			} else {
				att.Note = "Telegram 图片下载失败：" + dlErr.Error()
			}
			atts = append(atts, att)
		}
		if m.Document != nil && strings.TrimSpace(m.Document.FileID) != "" {
			fileName := strings.TrimSpace(m.Document.FileName)
			mimeType := strings.TrimSpace(m.Document.MimeType)
			kind := classifyAttachmentKind(mimeType, fileName)
			att := InboundAttachment{
				Kind:      kind,
				FileName:  fileName,
				MimeType:  mimeType,
				SizeBytes: m.Document.FileSize.Int64(),
			}
			if (kind == InboundAttachmentImage || (kind == InboundAttachmentFile && isCandidateUploadableFile(fileName, mimeType))) &&
				m.Document.FileSize.Int64() <= attachments.DefaultMaxUploadBytes {
				if data, filePath, dlErr := a.downloadTelegramFile(ctx, m.Document.FileID, attachments.DefaultMaxUploadBytes); dlErr == nil {
					att.RawData = data
					if att.FileName == "" {
						if base := strings.TrimSpace(path.Base(filePath)); base != "" && base != "." && base != "/" {
							att.FileName = base
						}
					}
				} else {
					att.Note = "Telegram 文件下载失败：" + dlErr.Error()
				}
			}
			atts = append(atts, att)
		}
		if m.Voice != nil {
			note := ""
			if dur := m.Voice.Duration.Int64(); dur > 0 {
				note = fmt.Sprintf("时长 %d 秒", dur)
			}
			atts = append(atts, InboundAttachment{
				Kind:      InboundAttachmentAudio,
				FileName:  "voice.ogg",
				MimeType:  strings.TrimSpace(m.Voice.MimeType),
				SizeBytes: m.Voice.FileSize.Int64(),
				Note:      note,
			})
		}
		if m.Audio != nil {
			fileName := strings.TrimSpace(m.Audio.FileName)
			if fileName == "" {
				fileName = strings.TrimSpace(m.Audio.Title)
			}
			note := ""
			if dur := m.Audio.Duration.Int64(); dur > 0 {
				note = fmt.Sprintf("时长 %d 秒", dur)
			}
			atts = append(atts, InboundAttachment{
				Kind:      InboundAttachmentAudio,
				FileName:  fileName,
				MimeType:  strings.TrimSpace(m.Audio.MimeType),
				SizeBytes: m.Audio.FileSize.Int64(),
				Note:      note,
			})
		}
		if m.Video != nil {
			note := ""
			if dur := m.Video.Duration.Int64(); dur > 0 {
				note = fmt.Sprintf("时长 %d 秒", dur)
			}
			atts = append(atts, InboundAttachment{
				Kind:      InboundAttachmentVideo,
				FileName:  strings.TrimSpace(m.Video.FileName),
				MimeType:  strings.TrimSpace(m.Video.MimeType),
				SizeBytes: m.Video.FileSize.Int64(),
				Note:      note,
			})
		}
		if m.Sticker != nil {
			note := "贴纸表情"
			if emoji := strings.TrimSpace(m.Sticker.Emoji); emoji != "" {
				note = "贴纸 " + emoji
			}
			atts = append(atts, InboundAttachment{
				Kind: InboundAttachmentOther,
				Note: note,
			})
		}
		if text == "" && len(atts) == 0 {
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
				Attachments:       atts,
				Mentioned:         mentioned,
				Timestamp:         msgTime,
			})
		}
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
		return fmt.Errorf("%w: telegram target chat_id is empty", ErrTargetNotBound)
	}
	fullText := FormatOutboundForPlatform(domain.ChannelPlatformTelegram, msg.Text, msg.CardURL)
	for _, chunk := range SplitMessage(fullText, telegramMaxRunes) {
		htmlChunk := MarkdownToTelegramHTML(chunk)
		if err := a.sendMessageOnce(ctx, chatID, threadID, htmlChunk, "HTML"); err != nil {
			if isTelegramParseError(err) {
				plain := MarkdownToPlainText(chunk)
				if plain == "" {
					plain = chunk
				}
				if fallbackErr := a.sendMessageOnce(ctx, chatID, threadID, plain, ""); fallbackErr != nil {
					return fmt.Errorf("%v (fallback plain send failed: %w)", err, fallbackErr)
				}
				continue
			}
			return err
		}
	}
	return nil
}

func (a *TelegramAdapter) sendMessageOnce(ctx context.Context, chatID string, threadID int64, text, parseMode string) error {
	body := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	}
	if parseMode != "" {
		body["parse_mode"] = parseMode
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
	return nil
}

func isTelegramParseError(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "can't parse entities") ||
		strings.Contains(low, "parse entities") ||
		strings.Contains(low, "unsupported start tag") ||
		strings.Contains(low, "unclosed start tag") ||
		strings.Contains(low, "can't find end tag")
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

// downloadTelegramFile 通过 Telegram Bot API getFile 拉取文件字节，复用通道自带代理并在报错中严格隐藏 BotToken。
func (a *TelegramAdapter) downloadTelegramFile(ctx context.Context, fileID string, maxBytes int64) ([]byte, string, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return nil, "", errors.New("file_id 为空")
	}
	raw, err := a.apiCall(ctx, "getFile", map[string]any{"file_id": fileID})
	if err != nil {
		return nil, "", errors.New("getFile 请求失败")
	}
	var resp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			FilePath string    `json:"file_path"`
			FileSize flexInt64 `json:"file_size"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, "", errors.New("getFile 响应解析失败")
	}
	if !resp.OK || strings.TrimSpace(resp.Result.FilePath) == "" {
		if strings.TrimSpace(resp.Description) != "" {
			return nil, "", errors.New(strings.TrimSpace(resp.Description))
		}
		return nil, "", errors.New("getFile 未返回 file_path")
	}
	if maxBytes > 0 && resp.Result.FileSize.Int64() > maxBytes {
		return nil, resp.Result.FilePath, fmt.Errorf("文件过大（超出 %d MiB 上限）", maxBytes>>20)
	}
	filePath := strings.TrimLeft(strings.TrimSpace(resp.Result.FilePath), "/")
	fileURL := fmt.Sprintf("%s/file/bot%s/%s", a.baseURL, strings.TrimSpace(a.secrets.BotToken), filePath)
	data, _, err := downloadInboundAttachment(ctx, a.httpClient, fileURL, nil, maxBytes)
	if err != nil {
		// 严禁把包含 BotToken 的 URL 透传到日志或占位提示中。
		token := strings.TrimSpace(a.secrets.BotToken)
		msg := err.Error()
		if token != "" {
			msg = strings.ReplaceAll(msg, token, "***")
		}
		return nil, filePath, errors.New(msg)
	}
	return data, filePath, nil
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
