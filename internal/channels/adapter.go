package channels

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

// InboundAttachmentKind 描述外部 IM 入站附件的归一化媒体类别。
type InboundAttachmentKind string

const (
	InboundAttachmentImage InboundAttachmentKind = "image"
	InboundAttachmentFile  InboundAttachmentKind = "file"
	InboundAttachmentAudio InboundAttachmentKind = "audio"
	InboundAttachmentVideo InboundAttachmentKind = "video"
	InboundAttachmentOther InboundAttachmentKind = "other"
)

// InboundAttachment 表示外部 IM 消息附带的一份媒体或文件。
// 适配器既可直接填充已拉取或解密后的 RawData，也可提供 DownloadURL（及可选请求头）由公共层统一受限下载；
// 对于当前附件服务不支持原生解析的媒体（如语音/视频）或无法拉取的加密媒体，可填写 Transcript 或 Note 防止消息静默丢失。
type InboundAttachment struct {
	Kind            InboundAttachmentKind
	FileName        string
	MimeType        string
	SizeBytes       int64
	RawData         []byte
	DownloadURL     string
	DownloadHeaders map[string]string
	Transcript      string
	Note            string
}

// InboundMessage 表示从外部 IM 平台接收并已做协议归一化的一条消息。
type InboundMessage struct {
	ExternalMessageID string
	SenderID          string
	ChatID            string
	ChatType          string // "direct" 或 "group"
	ThreadID          string
	Content           string
	Attachments       []InboundAttachment
	Mentioned         bool
	SessionWebhook    string
	ContextToken      string
	Timestamp         time.Time
}

// OutboundMessage 表示发往外部 IM 平台的一条终态或定时推送消息。
type OutboundMessage struct {
	ChatID         string
	ChatType       string
	ThreadID       string
	Text           string
	SessionWebhook string
	ContextToken   string
	CardURL        string
}

// InboundHandler 处理单条外部入站消息。
type InboundHandler func(ctx context.Context, msg InboundMessage) error

// StatusReporter 用于适配器上报实时运行状态、心跳和最近错误。
type StatusReporter func(ctx context.Context, status domain.ChannelRuntimeStatus, lastErr string, heartbeat bool)

// StatePersister 用于适配器持久化增量游标与会话定位（如 get_updates_buf / telegram_offset）。
type StatePersister func(ctx context.Context, mutate func(*store.RemoteChannelRuntimeState)) error

// AdapterRuntime 是传给各个平台适配器的受控运行时回调。
type AdapterRuntime struct {
	OnInbound InboundHandler
	OnStatus  StatusReporter
	SaveState StatePersister
	Logger    *slog.Logger
}

func rtLogger(rt AdapterRuntime) *slog.Logger {
	if rt.Logger != nil {
		return rt.Logger
	}
	return slog.Default()
}

// Adapter 是 6 大 IM 平台的统一协议抽象。
// 长连接/长轮询通道通过 Run 阻塞运行并在 ctx 取消时优雅退出；
// 轻量化 Webhook 通道的 Run 直接标记 ready 并在 ctx 结束时返回。
type Adapter interface {
	Platform() domain.ChannelPlatform
	Run(ctx context.Context, rt AdapterRuntime) error
	Send(ctx context.Context, msg OutboundMessage) error
	Test(ctx context.Context, testText string) error
}

// PolicyAllows 检查一条入站消息是否满足通道的白名单与群聊策略。
func PolicyAllows(ch store.RemoteChannel, msg InboundMessage) bool {
	if ch.Role != domain.ChannelRoleMainChat {
		return false
	}
	if strings.TrimSpace(msg.Content) == "" && len(msg.Attachments) == 0 {
		return false
	}
	isGroup := strings.EqualFold(strings.TrimSpace(msg.ChatType), "group")
	allowedByList := matchesAllowFrom(ch.AllowFrom, msg.SenderID, msg.ChatID)

	if !isGroup {
		if len(ch.AllowFrom) == 0 {
			return true
		}
		return allowedByList
	}

	switch ch.GroupPolicy {
	case domain.ChannelGroupPolicyAll:
		if len(ch.AllowFrom) == 0 {
			return true
		}
		return allowedByList
	case domain.ChannelGroupPolicyAllowlist:
		return len(ch.AllowFrom) > 0 && allowedByList
	default: // mention_only
		if !msg.Mentioned {
			return false
		}
		if len(ch.AllowFrom) == 0 {
			return true
		}
		return allowedByList
	}
}

func matchesAllowFrom(allowFrom []string, senderID, chatID string) bool {
	if len(allowFrom) == 0 {
		return false
	}
	sender := strings.TrimSpace(senderID)
	chat := strings.TrimSpace(chatID)
	for _, raw := range allowFrom {
		rule := strings.TrimSpace(raw)
		if rule == "" {
			continue
		}
		if rule == "*" {
			return true
		}
		if sender != "" && strings.EqualFold(rule, sender) {
			return true
		}
		if chat != "" && strings.EqualFold(rule, chat) {
			return true
		}
	}
	return false
}

// SplitMessage 按平台最大字符长度安全切分长文本，尽量在段落、换行或标点处断句。
func SplitMessage(text string, maxRunes int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return []string{text}
	}
	runes := []rune(text)
	chunks := make([]string, 0, len(runes)/maxRunes+1)
	for len(runes) > 0 {
		if len(runes) <= maxRunes {
			chunks = append(chunks, strings.TrimSpace(string(runes)))
			break
		}
		cut := findSplitPoint(runes[:maxRunes])
		piece := strings.TrimSpace(string(runes[:cut]))
		if piece != "" {
			chunks = append(chunks, piece)
		}
		runes = []rune(strings.TrimLeft(string(runes[cut:]), " \t\r\n"))
	}
	return chunks
}

func findSplitPoint(window []rune) int {
	n := len(window)
	minCut := n / 2
	// 优先在空行段落边界切分
	for i := n - 1; i >= minCut; i-- {
		if i > 0 && window[i] == '\n' && window[i-1] == '\n' {
			return i
		}
	}
	// 其次在单换行切分
	for i := n - 1; i >= minCut; i-- {
		if window[i] == '\n' {
			return i
		}
	}
	// 最后在常见中英文句末标点切分
	for i := n - 1; i >= minCut; i-- {
		switch window[i] {
		case '。', '！', '？', '；', '.', '!', '?', ';', ' ':
			return i + 1
		}
	}
	return n
}

// FormatOutboundWithLink 自动清洗正文中的站内 cycledock:// 卡片引用，仅在存在直达链接时追加链接；
// 日常回复传入空 CardURL 即输出干净文本。
func FormatOutboundWithLink(text, cardURL string) string {
	text = strings.TrimSpace(SanitizeExternalCardLinks(text))
	cardURL = strings.TrimSpace(cardURL)
	if cardURL == "" {
		return text
	}
	if strings.Contains(text, cardURL) {
		return text
	}
	if text == "" {
		return "查看卡片：" + cardURL
	}
	return text + "\n\n查看卡片：" + cardURL
}

// ChannelCardURL 构造 CycleDock 卡片直达链接（对齐前端 /cards/:projectID/:cardID 路由）。
func ChannelCardURL(baseURL string, projectID, cardID uuid.UUID) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" || cardID == uuid.Nil {
		return ""
	}
	if projectID != uuid.Nil {
		return base + "/cards/" + projectID.String() + "/" + cardID.String()
	}
	return base + "/cards/" + cardID.String()
}

// SanitizeExternalCardLinks 将 AI 正文中的 CycleDock 站内卡片引用 [标题](cycledock://card/<uuid>)
// 清洗为适合外部 IM 阅读的形式，避免裸露内部 cycledock:// 协议链接。
func SanitizeExternalCardLinks(text string) string {
	if !strings.Contains(text, "cycledock://card/") {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	rest := text
	for {
		openBracket := strings.IndexByte(rest, '[')
		if openBracket < 0 {
			out.WriteString(rest)
			break
		}
		out.WriteString(rest[:openBracket])
		afterOpen := rest[openBracket+1:]
		mid := strings.Index(afterOpen, "](cycledock://card/")
		if mid < 0 {
			out.WriteByte('[')
			rest = afterOpen
			continue
		}
		title := strings.TrimSpace(afterOpen[:mid])
		afterPrefix := afterOpen[mid+len("](cycledock://card/"):]
		closeParen := strings.IndexByte(afterPrefix, ')')
		if closeParen < 0 || strings.ContainsAny(title, "\r\n") {
			out.WriteByte('[')
			rest = afterOpen
			continue
		}
		if title != "" {
			out.WriteString("「")
			out.WriteString(title)
			out.WriteString("」")
		}
		rest = afterPrefix[closeParen+1:]
	}
	return strings.TrimSpace(out.String())
}
