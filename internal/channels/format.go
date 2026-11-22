package channels

import (
	"errors"
	"html"
	"regexp"
	"strings"

	"github.com/uvwt/CycleDock/internal/domain"
)

// ErrTargetNotBound 表示通道尚未绑定任何可发送的目标会话（如未配置 target_chat_id 且尚未收到过入站消息）。
// Dispatcher 捕获该错误后会优雅跳过并静默推进初始水位，避免未绑定通道每 2 秒狂刷 WARN 日志。
var ErrTargetNotBound = errors.New("remote channel target not bound")

var (
	reMDHeading      = regexp.MustCompile(`(?m)^[ \t]{0,3}#{1,6}[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	reMDBlockquote   = regexp.MustCompile(`(?m)^[ \t]*>[ \t]?`)
	reMDTableSep     = regexp.MustCompile(`(?m)^[ \t]*\|?(?:[ \t]*:?-{3,}:?[ \t]*\|)+[ \t]*:?-{3,}:?[ \t]*\|?[ \t]*$`)
	reMDLink         = regexp.MustCompile(`\[([^\]\r\n]+)\]\((https?://[^\s)]+)\)`)
	reMDBoldStar     = regexp.MustCompile(`\*\*([^\*\r\n]+?)\*\*`)
	reMDBoldUnder    = regexp.MustCompile(`__([^_\r\n]+?)__`)
	reMDStrike       = regexp.MustCompile(`~~([^~\r\n]+?)~~`)
	reMDItalicStar   = regexp.MustCompile(`(^|[^\*\w])\*([^\*\r\n]+?)\*([^\*\w]|$)`)
	reMDInlineCode   = regexp.MustCompile("`([^`\r\n]+?)`")
	reMDUnorderedLst = regexp.MustCompile(`(?m)^([ \t]*)[-*+][ \t]+`)
	reMDHorizontal   = regexp.MustCompile(`(?m)^[ \t]*([-*_]){3,}[ \t]*$`)
	reMultiBlankLine = regexp.MustCompile(`\n{3,}`)
)

// FormatOutboundForPlatform 按目标 IM 平台特性对出站正文做统一格式化与兜底清洗：
// - 先清理 CycleDock 站内链接 [标题](cycledock://card/<id>) 避免裸露内部协议；
// - 微信/QQ：强制将 Markdown 清洗为干净自然纯文本，防止满屏星号/井号/代码块围栏；
// - 飞书/钉钉/企业微信/Telegram：保留语义结构并清理异常换行与站内协议链接（Telegram 发送时再转安全 HTML）。
func FormatOutboundForPlatform(platform domain.ChannelPlatform, text, cardURL string) string {
	clean := strings.TrimSpace(SanitizeExternalCardLinks(text))
	switch platform {
	case domain.ChannelPlatformWeixin, domain.ChannelPlatformQQ:
		clean = MarkdownToPlainText(clean)
	default:
		clean = CleanMarkdownForIM(clean)
	}
	return appendCardURLIfNeeded(clean, cardURL)
}

func appendCardURLIfNeeded(text, cardURL string) string {
	text = strings.TrimSpace(text)
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

// CleanMarkdownForIM 为支持 Markdown 的 IM 平台（飞书/钉钉/企微/Telegram 源文本）做轻量兼容性清洗：
// 统一换行符、移除零宽控制字符、清理站内链接标记并收敛过多连续空行。
func CleanMarkdownForIM(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(SanitizeExternalCardLinks(text))
	if text == "" {
		return ""
	}
	text = reMultiBlankLine.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// MarkdownToPlainText 将常见 Markdown 语法转换为适合微信、QQ 等纯文本客户端阅读的干净排版。
func MarkdownToPlainText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(SanitizeExternalCardLinks(text))
	if text == "" {
		return ""
	}

	// 逐行剥离代码块围栏（```lang）与表格分隔线，同时规整表格行。
	lines := strings.Split(text, "\n")
	outLines := make([]string, 0, len(lines))
	inCodeBlock := false
	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			outLines = append(outLines, rawLine)
			continue
		}
		if reMDTableSep.MatchString(rawLine) {
			continue
		}
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") && strings.Count(trimmed, "|") >= 2 {
			inner := strings.Trim(trimmed, "|")
			cells := strings.Split(inner, "|")
			cleanedCells := make([]string, 0, len(cells))
			for _, cell := range cells {
				c := strings.TrimSpace(cell)
				if c != "" {
					cleanedCells = append(cleanedCells, c)
				}
			}
			outLines = append(outLines, strings.Join(cleanedCells, " | "))
			continue
		}
		outLines = append(outLines, rawLine)
	}
	text = strings.Join(outLines, "\n")

	text = reMDHorizontal.ReplaceAllString(text, "")
	text = reMDHeading.ReplaceAllString(text, "$1")
	text = reMDBlockquote.ReplaceAllString(text, "")
	text = reMDUnorderedLst.ReplaceAllString(text, "${1}• ")
	text = reMDLink.ReplaceAllStringFunc(text, func(m string) string {
		sub := reMDLink.FindStringSubmatch(m)
		if len(sub) != 3 {
			return m
		}
		label := strings.TrimSpace(sub[1])
		href := strings.TrimSpace(sub[2])
		if label == "" || label == href {
			return href
		}
		return label + " (" + href + ")"
	})
	text = reMDInlineCode.ReplaceAllString(text, "$1")
	text = reMDBoldStar.ReplaceAllString(text, "$1")
	text = reMDBoldUnder.ReplaceAllString(text, "$1")
	text = reMDStrike.ReplaceAllString(text, "$1")
	text = reMDItalicStar.ReplaceAllString(text, "${1}${2}${3}")
	text = reMultiBlankLine.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// MarkdownToTelegramHTML 将常见 Markdown 轻量转换为 Telegram 安全 HTML（parse_mode=HTML）。
// 所有文本内容均经过严格的 HTML 转义，避免尖括号、& 符号触发 Telegram 解析错误。
func MarkdownToTelegramHTML(text string) string {
	text = CleanMarkdownForIM(text)
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	var out []string
	var codeBuf []string
	inCode := false

	flushCode := func() {
		escaped := html.EscapeString(strings.Join(codeBuf, "\n"))
		out = append(out, "<pre><code>"+escaped+"</code></pre>")
		codeBuf = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				flushCode()
				inCode = false
			} else {
				inCode = true
				codeBuf = nil
			}
			continue
		}
		if inCode {
			codeBuf = append(codeBuf, line)
			continue
		}
		if reMDTableSep.MatchString(line) {
			continue
		}
		if m := reMDHeading.FindStringSubmatch(line); len(m) == 2 {
			out = append(out, "<b>"+formatTelegramInlineHTML(strings.TrimSpace(m[1]))+"</b>")
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			inner := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			out = append(out, "<blockquote>"+formatTelegramInlineHTML(inner)+"</blockquote>")
			continue
		}
		if reMDHorizontal.MatchString(line) {
			out = append(out, "────────")
			continue
		}
		if reMDUnorderedLst.MatchString(line) {
			line = reMDUnorderedLst.ReplaceAllString(line, "${1}• ")
		}
		out = append(out, formatTelegramInlineHTML(line))
	}
	if inCode && len(codeBuf) > 0 {
		flushCode()
	}
	res := strings.Join(out, "\n")
	res = reMultiBlankLine.ReplaceAllString(res, "\n\n")
	return strings.TrimSpace(res)
}

// formatTelegramInlineHTML 安全格式化单行内的行内代码、链接、粗体、斜体与删除线。
func formatTelegramInlineHTML(line string) string {
	// 先把行内代码抽取为占位符，避免代码内部出现 * 或 _ 被误转换。
	var codePlaceholders []string
	withPlaceholders := reMDInlineCode.ReplaceAllStringFunc(line, func(m string) string {
		sub := reMDInlineCode.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		idx := len(codePlaceholders)
		codePlaceholders = append(codePlaceholders, "<code>"+html.EscapeString(sub[1])+"</code>")
		return "\x00CODE" + string(rune('A'+idx%26)) + "_" + strings.Repeat("X", idx/26) + "\x00"
	})

	// 再把链接抽取为占位符，避免 href 中的下划线/符号被二次改写。
	var linkPlaceholders []string
	withPlaceholders = reMDLink.ReplaceAllStringFunc(withPlaceholders, func(m string) string {
		sub := reMDLink.FindStringSubmatch(m)
		if len(sub) != 3 {
			return m
		}
		label := html.EscapeString(strings.TrimSpace(sub[1]))
		href := html.EscapeString(strings.TrimSpace(sub[2]))
		idx := len(linkPlaceholders)
		linkPlaceholders = append(linkPlaceholders, `<a href="`+href+`">`+label+`</a>`)
		return "\x00LINK" + string(rune('A'+idx%26)) + "_" + strings.Repeat("Y", idx/26) + "\x00"
	})

	escaped := html.EscapeString(withPlaceholders)
	escaped = reMDBoldStar.ReplaceAllString(escaped, "<b>$1</b>")
	escaped = reMDBoldUnder.ReplaceAllString(escaped, "<b>$1</b>")
	escaped = reMDStrike.ReplaceAllString(escaped, "<s>$1</s>")
	escaped = reMDItalicStar.ReplaceAllString(escaped, "${1}<i>${2}</i>${3}")

	for idx, htmlTag := range linkPlaceholders {
		token := "\x00LINK" + string(rune('A'+idx%26)) + "_" + strings.Repeat("Y", idx/26) + "\x00"
		escaped = strings.Replace(escaped, token, htmlTag, 1)
	}
	for idx, htmlTag := range codePlaceholders {
		token := "\x00CODE" + string(rune('A'+idx%26)) + "_" + strings.Repeat("X", idx/26) + "\x00"
		escaped = strings.Replace(escaped, token, htmlTag, 1)
	}
	return escaped
}

// MaskRemoteIdentifier 对外部平台用户 ID / 会话 ID 做日志脱敏展示，严禁打印完整敏感标识。
func MaskRemoteIdentifier(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= 6 {
		return "***"
	}
	return string(runes[:3]) + "***" + string(runes[len(runes)-3:])
}
