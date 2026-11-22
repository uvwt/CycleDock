package channels

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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

	"github.com/uvwt/CycleDock/internal/attachments"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	defaultWeComWSURL = "wss://openws.work.weixin.qq.com"
	wecomMaxRunes     = 2000
)

// WeComAdapter 支持企业微信 AI Bot 官方 WebSocket 长连接（免公网回调），
// 以及专门用于定时任务推送的企微群机器人 Webhook 模式。
type WeComAdapter struct {
	channel    store.RemoteChannel
	secrets    store.RemoteChannelSecrets
	wsURL      string
	httpClient *http.Client

	mu         sync.Mutex
	conn       *wsConn
	lastChatID string
	lastChatTy string
}

func NewWeComAdapter(ch store.RemoteChannel, secrets store.RemoteChannelSecrets, client *http.Client) *WeComAdapter {
	wsURL := strings.TrimSpace(ch.Config.WSURL)
	if wsURL == "" {
		wsURL = defaultWeComWSURL
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
	return &WeComAdapter{
		channel:    ch,
		secrets:    secrets,
		wsURL:      wsURL,
		httpClient: client,
		lastChatID: lastChat,
		lastChatTy: lastChatTy,
	}
}

func (a *WeComAdapter) Platform() domain.ChannelPlatform {
	return domain.ChannelPlatformWeCom
}

func (a *WeComAdapter) Run(ctx context.Context, rt AdapterRuntime) error {
	if a.channel.Transport == domain.ChannelTransportWebhook {
		if strings.TrimSpace(a.secrets.WebhookURL) == "" {
			err := errors.New("wecom webhook_url is not configured")
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

	if strings.TrimSpace(a.channel.Config.BotID) == "" || strings.TrimSpace(a.secrets.Secret) == "" {
		err := errors.New("wecom bot_id and secret are required for websocket mode")
		if rt.OnStatus != nil {
			rt.OnStatus(ctx, domain.ChannelRuntimeError, err.Error(), false)
		}
		return err
	}

	conn, err := dialWS(ctx, a.wsURL)
	if err != nil {
		return fmt.Errorf("dial wecom websocket: %w", err)
	}
	defer func() {
		a.mu.Lock()
		if a.conn == conn {
			a.conn = nil
		}
		a.mu.Unlock()
		_ = conn.Close()
	}()

	// 按企微 AI Bot WebSocket 协议发送订阅鉴权帧（aibot_subscribe）。
	subFrame := map[string]any{
		"cmd":     "aibot_subscribe",
		"headers": map[string]string{"req_id": "sub-" + uuid.NewString()[:8]},
		"body": map[string]string{
			"bot_id": strings.TrimSpace(a.channel.Config.BotID),
			"secret": strings.TrimSpace(a.secrets.Secret),
		},
	}
	if err := conn.WriteJSON(subFrame); err != nil {
		return fmt.Errorf("send wecom subscribe frame: %w", err)
	}

	a.mu.Lock()
	a.conn = conn
	a.mu.Unlock()

	if rt.OnStatus != nil {
		rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
	}

	hbDone := make(chan struct{})
	defer close(hbDone)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-hbDone:
				return
			case <-ticker.C:
				_ = conn.WriteJSON(map[string]any{
					"cmd":     "ping",
					"headers": map[string]string{"req_id": "ping-" + uuid.NewString()[:8]},
				})
				if rt.OnStatus != nil {
					rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
				}
			}
		}
	}()

	for {
		_, data, err := conn.Read()
		if err != nil {
			return err
		}
		var frame struct {
			Cmd     string     `json:"cmd"`
			ErrCode flexInt64  `json:"errcode"`
			ErrMsg  flexString `json:"errmsg"`
			Headers struct {
				ReqID flexString `json:"req_id"`
			} `json:"headers"`
			Body struct {
				MsgID    flexString `json:"msgid"`
				ChatID   flexString `json:"chatid"`
				ChatType flexString `json:"chattype"`
				MsgType  flexString `json:"msgtype"`
				From     struct {
					UserID flexString `json:"userid"`
				} `json:"from"`
				Text struct {
					Content string `json:"content"`
				} `json:"text"`
				Image struct {
					URL    string `json:"url"`
					AESKey string `json:"aeskey"`
				} `json:"image"`
				File struct {
					URL      string `json:"url"`
					AESKey   string `json:"aeskey"`
					FileName string `json:"filename"`
					Name     string `json:"name"`
				} `json:"file"`
				Voice struct {
					Content string `json:"content"`
				} `json:"voice"`
				Video struct {
					URL      string `json:"url"`
					FileName string `json:"filename"`
				} `json:"video"`
				Mixed struct {
					MsgItem []struct {
						MsgType string `json:"msgtype"`
						Text    struct {
							Content string `json:"content"`
						} `json:"text"`
						Image struct {
							URL    string `json:"url"`
							AESKey string `json:"aeskey"`
						} `json:"image"`
						File struct {
							URL      string `json:"url"`
							AESKey   string `json:"aeskey"`
							FileName string `json:"filename"`
							Name     string `json:"name"`
						} `json:"file"`
					} `json:"msg_item"`
				} `json:"mixed"`
				Content string `json:"content"`
			} `json:"body"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			continue
		}
		if frame.ErrCode != 0 && strings.Contains(strings.ToLower(frame.Cmd), "subscribe") {
			return fmt.Errorf("wecom subscribe failed (%d): %s", frame.ErrCode.Int64(), frame.ErrMsg.String())
		}
		if frame.ErrCode != 0 && strings.Contains(strings.ToLower(frame.Cmd), "send") {
			if rt.OnStatus != nil {
				rt.OnStatus(ctx, domain.ChannelRuntimeConnected, fmt.Sprintf("企微消息发送返回错误(%d): %s", frame.ErrCode.Int64(), frame.ErrMsg.String()), true)
			}
			continue
		}
		if frame.Cmd == "pong" || frame.Cmd == "aibot_subscribe_ack" || frame.Cmd == "aibot_subscribe" {
			if rt.OnStatus != nil {
				rt.OnStatus(ctx, domain.ChannelRuntimeConnected, "", true)
			}
			continue
		}
		if frame.Cmd != "aibot_msg_callback" && frame.Cmd != "message.text" && strings.TrimSpace(frame.Body.MsgID.String()) == "" {
			continue
		}
		text := strings.TrimSpace(frame.Body.Text.Content)
		if text == "" {
			text = strings.TrimSpace(frame.Body.Content)
		}
		atts := make([]InboundAttachment, 0, 2)
		msgType := strings.ToLower(strings.TrimSpace(frame.Body.MsgType.String()))
		switch msgType {
		case "image":
			atts = append(atts, a.fetchWeComAttachment(ctx, InboundAttachmentImage, frame.Body.Image.URL, frame.Body.Image.AESKey, "image.png"))
		case "file":
			fileName := strings.TrimSpace(frame.Body.File.FileName)
			if fileName == "" {
				fileName = strings.TrimSpace(frame.Body.File.Name)
			}
			kind := classifyAttachmentKind("", fileName)
			if kind == InboundAttachmentImage || (kind == InboundAttachmentFile && isCandidateUploadableFile(fileName, "")) {
				atts = append(atts, a.fetchWeComAttachment(ctx, kind, frame.Body.File.URL, frame.Body.File.AESKey, fileName))
			} else {
				atts = append(atts, InboundAttachment{
					Kind:     kind,
					FileName: fileName,
				})
			}
		case "voice":
			atts = append(atts, InboundAttachment{
				Kind:       InboundAttachmentAudio,
				Transcript: strings.TrimSpace(frame.Body.Voice.Content),
			})
		case "video":
			atts = append(atts, InboundAttachment{
				Kind:     InboundAttachmentVideo,
				FileName: strings.TrimSpace(frame.Body.Video.FileName),
			})
		case "mixed":
			textParts := make([]string, 0, len(frame.Body.Mixed.MsgItem))
			for _, item := range frame.Body.Mixed.MsgItem {
				switch strings.ToLower(strings.TrimSpace(item.MsgType)) {
				case "text":
					if t := strings.TrimSpace(item.Text.Content); t != "" {
						textParts = append(textParts, t)
					}
				case "image":
					atts = append(atts, a.fetchWeComAttachment(ctx, InboundAttachmentImage, item.Image.URL, item.Image.AESKey, "image.png"))
				case "file":
					fileName := strings.TrimSpace(item.File.FileName)
					if fileName == "" {
						fileName = strings.TrimSpace(item.File.Name)
					}
					kind := classifyAttachmentKind("", fileName)
					if kind == InboundAttachmentImage || (kind == InboundAttachmentFile && isCandidateUploadableFile(fileName, "")) {
						atts = append(atts, a.fetchWeComAttachment(ctx, kind, item.File.URL, item.File.AESKey, fileName))
					} else {
						atts = append(atts, InboundAttachment{
							Kind:     kind,
							FileName: fileName,
						})
					}
				}
			}
			if text == "" {
				text = strings.TrimSpace(strings.Join(textParts, "\n"))
			}
		default:
			// 兼容未标明 msgtype 但携带了 image/voice/file 字段的回调帧。
			if strings.TrimSpace(frame.Body.Image.URL) != "" {
				atts = append(atts, a.fetchWeComAttachment(ctx, InboundAttachmentImage, frame.Body.Image.URL, frame.Body.Image.AESKey, "image.png"))
			}
			if strings.TrimSpace(frame.Body.Voice.Content) != "" {
				atts = append(atts, InboundAttachment{
					Kind:       InboundAttachmentAudio,
					Transcript: strings.TrimSpace(frame.Body.Voice.Content),
				})
			}
			if text == "" && len(atts) == 0 && msgType != "" && msgType != "text" {
				atts = append(atts, InboundAttachment{
					Kind: InboundAttachmentOther,
					Note: fmt.Sprintf("企业微信 %s 消息", msgType),
				})
			}
		}
		if text == "" && len(atts) == 0 {
			continue
		}
		senderID := strings.TrimSpace(frame.Body.From.UserID.String())
		chatType := "direct"
		chatID := senderID
		chatTypeRaw := strings.TrimSpace(frame.Body.ChatType.String())
		bodyChatID := strings.TrimSpace(frame.Body.ChatID.String())
		if strings.EqualFold(chatTypeRaw, "group") || chatTypeRaw == "2" || bodyChatID != "" {
			if strings.EqualFold(chatTypeRaw, "group") || chatTypeRaw == "2" {
				chatType = "group"
			}
			if bodyChatID != "" {
				chatID = bodyChatID
			}
		}
		msgID := strings.TrimSpace(frame.Body.MsgID.String())
		if msgID == "" {
			msgID = strings.TrimSpace(frame.Headers.ReqID.String())
		}
		if msgID == "" {
			msgID = uuid.NewString()
		}

		a.mu.Lock()
		a.lastChatID = chatID
		a.lastChatTy = chatType
		a.mu.Unlock()
		if rt.SaveState != nil {
			_ = rt.SaveState(ctx, func(st *store.RemoteChannelRuntimeState) {
				st.LastChatID = chatID
				st.LastChatType = chatType
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
				Mentioned:         true, // 企微 AI Bot 群聊仅在 @ 机器人时回调
				Timestamp:         time.Now().UTC(),
			})
		}
	}
}

// fetchWeComAttachment 下载企业微信 AI Bot 媒体文件；若携带 aeskey 则执行 AES-256-CBC 解密，失败时保留明确占位说明。
func (a *WeComAdapter) fetchWeComAttachment(
	ctx context.Context,
	kind InboundAttachmentKind,
	rawURL, aesKey, fileName string,
) InboundAttachment {
	att := InboundAttachment{
		Kind:     kind,
		FileName: strings.TrimSpace(fileName),
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		att.Note = "企业微信消息未提供下载链接"
		return att
	}
	aesKey = strings.TrimSpace(aesKey)
	if aesKey == "" {
		att.DownloadURL = rawURL
		return att
	}
	raw, respMime, err := downloadInboundAttachment(ctx, a.httpClient, rawURL, nil, attachments.DefaultMaxUploadBytes)
	if err != nil {
		att.Note = "企业微信媒体下载失败：" + err.Error()
		return att
	}
	if _, _, isImg := attachments.DetectImageFormat(raw); isImg {
		att.RawData = raw
		att.MimeType = respMime
		return att
	}
	plain, err := decryptWeComAES256CBC(raw, aesKey)
	if err != nil {
		att.Note = "企业微信加密媒体解密失败：" + err.Error()
		return att
	}
	att.RawData = plain
	att.MimeType = respMime
	return att
}

func (a *WeComAdapter) Send(ctx context.Context, msg OutboundMessage) error {
	fullText := FormatOutboundForPlatform(domain.ChannelPlatformWeCom, msg.Text, msg.CardURL)
	chunks := SplitMessage(fullText, wecomMaxRunes)
	if a.channel.Transport == domain.ChannelTransportWebhook {
		webhookURL := strings.TrimSpace(a.secrets.WebhookURL)
		if webhookURL == "" {
			return errors.New("wecom webhook_url is not configured")
		}
		for _, chunk := range chunks {
			if err := postJSONExpectOK(ctx, a.httpClient, webhookURL, map[string]any{
				"msgtype":  "markdown",
				"markdown": map[string]string{"content": chunk},
			}, "errcode"); err != nil {
				return fmt.Errorf("wecom webhook send: %w", err)
			}
		}
		return nil
	}

	a.mu.Lock()
	conn := a.conn
	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" {
		chatID = a.lastChatID
	}
	chatType := strings.TrimSpace(msg.ChatType)
	if chatType == "" {
		chatType = a.lastChatTy
	}
	a.mu.Unlock()

	if conn == nil {
		return errors.New("wecom websocket is not connected")
	}
	if chatID == "" {
		return fmt.Errorf("%w: wecom target chat_id is empty", ErrTargetNotBound)
	}
	chatTypeNum := 1
	if strings.EqualFold(chatType, "group") {
		chatTypeNum = 2
	}
	for _, chunk := range chunks {
		frame := map[string]any{
			"cmd":     "aibot_send_msg",
			"headers": map[string]string{"req_id": "send-" + uuid.NewString()[:8]},
			"body": map[string]any{
				"chatid":    chatID,
				"chat_type": chatTypeNum,
				"msgtype":   "markdown",
				"markdown":  map[string]string{"content": chunk},
			},
		}
		if err := conn.WriteJSON(frame); err != nil {
			return fmt.Errorf("wecom websocket send: %w", err)
		}
	}
	return nil
}

func (a *WeComAdapter) Test(ctx context.Context, testText string) error {
	if a.channel.Transport == domain.ChannelTransportWebhook {
		return a.Send(ctx, OutboundMessage{Text: testText})
	}
	a.mu.Lock()
	connected := a.conn != nil
	target := a.lastChatID
	a.mu.Unlock()
	if connected && target != "" {
		return a.Send(ctx, OutboundMessage{ChatID: target, Text: testText})
	}
	if strings.TrimSpace(a.channel.Config.BotID) == "" || strings.TrimSpace(a.secrets.Secret) == "" {
		return errors.New("wecom bot_id and secret are required")
	}
	// 轻量校验：尝试短连接并发送订阅帧验证端点可达。
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := dialWS(dialCtx, a.wsURL)
	if err != nil {
		return fmt.Errorf("wecom websocket connectivity test failed: %w", err)
	}
	defer conn.Close()
	return conn.WriteJSON(map[string]any{
		"cmd":     "aibot_subscribe",
		"headers": map[string]string{"req_id": "test-" + uuid.NewString()[:8]},
		"body": map[string]string{
			"bot_id": strings.TrimSpace(a.channel.Config.BotID),
			"secret": strings.TrimSpace(a.secrets.Secret),
		},
	})
}

func postJSONExpectOK(ctx context.Context, client *http.Client, endpoint string, body any, codeField string) error {
	rawBody, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respRaw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respRaw)))
	}
	if codeField != "" && len(respRaw) > 0 {
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal(respRaw, &parsed); err == nil {
			if rawCode, ok := parsed[codeField]; ok {
				var code flexInt64
				if err := json.Unmarshal(rawCode, &code); err == nil && code != 0 {
					return fmt.Errorf("remote error %s=%d: %s", codeField, code.Int64(), strings.TrimSpace(string(respRaw)))
				}
			}
		}
	}
	return nil
}

// signWebhookHMAC 生成钉钉/飞书群机器人 Webhook 加签摘要。
func signDingTalkWebhookURL(rawURL, secret string, now time.Time) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return rawURL, nil
	}
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	stringToSign := ts + "\n" + secret
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stringToSign))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := parsed.Query()
	q.Set("timestamp", ts)
	q.Set("sign", sig)
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func signFeishuWebhook(secret string, now time.Time) (string, string) {
	ts := strconv.FormatInt(now.Unix(), 10)
	stringToSign := ts + "\n" + secret
	mac := hmac.New(sha256.New, []byte(stringToSign))
	return ts, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
