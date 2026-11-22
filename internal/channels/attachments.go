package channels

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/attachments"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	defaultAttachmentDownloadTimeout = 30 * time.Second
	maxAttachmentDownloadRedirects   = 5
)

var defaultAttachmentHTTPClient = &http.Client{
	Timeout: defaultAttachmentDownloadTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxAttachmentDownloadRedirects {
			return errors.New("too many redirects")
		}
		if err := validateHTTPDownloadURL(req.URL); err != nil {
			return err
		}
		// 跨主机重定向时剥离敏感认证头，避免凭据泄漏到第三方域名。
		if len(via) > 0 && !strings.EqualFold(via[0].URL.Host, req.URL.Host) {
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
		}
		return nil
	},
}

func validateHTTPDownloadURL(u *url.URL) error {
	if u == nil {
		return errors.New("download url is empty")
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported url scheme %q", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return errors.New("download url host is empty")
	}
	return nil
}

// normalizeRemoteDownloadURL 规范化外部 IM 返回的媒体下载链接（如 QQ 的 // 开头的无协议链接）并严格校验 http/https。
func normalizeRemoteDownloadURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("download url is empty")
	}
	if strings.HasPrefix(trimmed, "//") {
		trimmed = "https:" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid download url: %w", err)
	}
	if err := validateHTTPDownloadURL(parsed); err != nil {
		return "", err
	}
	return parsed.String(), nil
}

// downloadInboundAttachment 安全下载远端附件字节：
// 严格校验 http/https 协议、绑定超时、预检查 Content-Length 并使用 LimitReader 防止 OOM。
func downloadInboundAttachment(
	ctx context.Context,
	client *http.Client,
	rawURL string,
	headers map[string]string,
	maxBytes int64,
) ([]byte, string, error) {
	targetURL, err := normalizeRemoteDownloadURL(rawURL)
	if err != nil {
		return nil, "", err
	}
	if maxBytes <= 0 {
		maxBytes = attachments.DefaultMaxUploadBytes
	}
	if client == nil {
		client = defaultAttachmentHTTPClient
	}
	dlCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		dlCtx, cancel = context.WithTimeout(ctx, defaultAttachmentDownloadTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, "", err
	}
	for k, v := range headers {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return nil, "", fmt.Errorf("文件过大（超出 %d MiB 上限）", maxBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("文件过大（超出 %d MiB 上限）", maxBytes>>20)
	}
	if len(data) == 0 {
		return nil, "", errors.New("附件内容为空")
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	return data, contentType, nil
}

// classifyAttachmentKind 根据 MIME 类型和文件名后缀归一化外部附件类别。
func classifyAttachmentKind(mimeType, fileName string) InboundAttachmentKind {
	lowerMime := strings.ToLower(strings.TrimSpace(mimeType))
	if idx := strings.Index(lowerMime, ";"); idx >= 0 {
		lowerMime = strings.TrimSpace(lowerMime[:idx])
	}
	switch {
	case strings.HasPrefix(lowerMime, "image/"):
		return InboundAttachmentImage
	case lowerMime == "voice" || strings.HasPrefix(lowerMime, "audio/"):
		return InboundAttachmentAudio
	case strings.HasPrefix(lowerMime, "video/"):
		return InboundAttachmentVideo
	}
	ext := strings.ToLower(path.Ext(strings.TrimSpace(fileName)))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return InboundAttachmentImage
	case ".silk", ".amr", ".mp3", ".wav", ".ogg", ".oga", ".opus", ".m4a", ".aac", ".flac":
		return InboundAttachmentAudio
	case ".mp4", ".mov", ".avi", ".mkv", ".webm", ".m4v":
		return InboundAttachmentVideo
	default:
		return InboundAttachmentFile
	}
}

// isCandidateUploadableFile 判断未下载前某个文件是否值得尝试拉取并交给附件服务处理（图片或受支持的纯文本/代码/日志）。
func isCandidateUploadableFile(fileName, mimeType string) bool {
	if classifyAttachmentKind(mimeType, fileName) == InboundAttachmentImage {
		return true
	}
	if attachments.IsAllowedTextFileName(fileName) {
		return true
	}
	lowerMime := strings.ToLower(strings.TrimSpace(mimeType))
	if idx := strings.Index(lowerMime, ";"); idx >= 0 {
		lowerMime = strings.TrimSpace(lowerMime[:idx])
	}
	switch {
	case strings.HasPrefix(lowerMime, "text/"):
		return true
	case lowerMime == "application/json",
		lowerMime == "application/xml",
		lowerMime == "application/yaml",
		lowerMime == "application/x-yaml",
		lowerMime == "application/toml",
		lowerMime == "application/javascript":
		return true
	default:
		return false
	}
}

func inferInboundFileName(att InboundAttachment, rawData []byte, respMime string) string {
	name := strings.TrimSpace(att.FileName)
	if name == "" && strings.TrimSpace(att.DownloadURL) != "" {
		if normalized, err := normalizeRemoteDownloadURL(att.DownloadURL); err == nil {
			if parsed, err := url.Parse(normalized); err == nil {
				base := strings.TrimSpace(path.Base(parsed.Path))
				if base != "" && base != "/" && base != "." {
					name = base
				}
			}
		}
	}
	if _, ext, isImg := attachments.DetectImageFormat(rawData); isImg {
		if name == "" {
			return "image" + ext
		}
		if path.Ext(name) == "" {
			return name + ext
		}
		return name
	}
	effectiveMime := strings.ToLower(strings.TrimSpace(att.MimeType))
	if effectiveMime == "" {
		effectiveMime = strings.ToLower(strings.TrimSpace(respMime))
	}
	if idx := strings.Index(effectiveMime, ";"); idx >= 0 {
		effectiveMime = strings.TrimSpace(effectiveMime[:idx])
	}
	// 纯文本 MIME 无文件名或无后缀时补齐常见安全后缀，便于附件服务按文本规则入库。
	if name == "" || path.Ext(name) == "" {
		var ext string
		switch effectiveMime {
		case "text/plain":
			ext = ".txt"
		case "text/markdown":
			ext = ".md"
		case "application/json":
			ext = ".json"
		case "text/csv":
			ext = ".csv"
		case "text/yaml", "application/yaml", "application/x-yaml":
			ext = ".yaml"
		case "text/xml", "application/xml":
			ext = ".xml"
		case "text/html":
			ext = ".html"
		default:
			if exts, _ := mime.ExtensionsByType(effectiveMime); len(exts) > 0 && attachments.IsAllowedTextFileName("file"+exts[0]) {
				ext = exts[0]
			}
		}
		if ext != "" {
			if name == "" {
				return "attachment" + ext
			}
			return name + ext
		}
	}
	return name
}

func formatAttachmentLabel(prefix, fileName string) string {
	clean := strings.TrimSpace(fileName)
	if clean == "" {
		return prefix
	}
	return fmt.Sprintf("%s（%s）", prefix, clean)
}

func formatUnsupportedAttachmentNote(att InboundAttachment) string {
	transcript := strings.TrimSpace(att.Transcript)
	note := strings.TrimSpace(att.Note)
	name := strings.TrimSpace(att.FileName)

	switch att.Kind {
	case InboundAttachmentAudio:
		if transcript != "" {
			return "[语音转写] " + transcript
		}
		label := formatAttachmentLabel("收到语音消息", name)
		if note != "" {
			return fmt.Sprintf("[%s：%s，当前暂不支持原生语音解析]", label, note)
		}
		return fmt.Sprintf("[%s：当前暂不支持原生语音解析]", label)
	case InboundAttachmentVideo:
		label := formatAttachmentLabel("收到视频消息", name)
		if note != "" {
			return fmt.Sprintf("[%s：%s，当前暂不支持原生视频解析]", label, note)
		}
		return fmt.Sprintf("[%s：当前暂不支持原生视频解析]", label)
	case InboundAttachmentImage:
		label := formatAttachmentLabel("收到图片", name)
		if note != "" {
			return fmt.Sprintf("[%s：%s]", label, note)
		}
		return fmt.Sprintf("[%s：暂无法获取图片内容]", label)
	case InboundAttachmentOther:
		if note != "" {
			label := formatAttachmentLabel("收到媒体消息", name)
			return fmt.Sprintf("[%s：%s]", label, note)
		}
		return fmt.Sprintf("[%s：当前暂不支持原生解析]", formatAttachmentLabel("收到媒体消息", name))
	default:
		label := formatAttachmentLabel("收到文件", name)
		if note != "" {
			return fmt.Sprintf("[%s：%s]", label, note)
		}
		return fmt.Sprintf("[%s：当前仅支持图片及纯文本/代码/日志类附件，暂不支持原生解析该格式]", label)
	}
}

// prepareInboundAttachments 在入站建 Turn 前处理外部 IM 附带的图片/文件/语音：
// - 支持的图片与纯文本/代码/日志附件下载（若有 DownloadURL）并通过 attachments.Service 校验落盘，返回 AttachmentIDs；
// - 语音、视频、暂不支持的二进制文件或下载失败项生成清晰的中文说明拼接到消息正文中，绝不静默丢弃；
// - 单条消息严格遵守 attachments.MaxAttachmentsPerTurn 上限。
func (m *Manager) prepareInboundAttachments(
	ctx context.Context,
	ch store.RemoteChannel,
	msg InboundMessage,
	chLogger *slog.Logger,
) (string, []uuid.UUID) {
	baseText := strings.TrimSpace(msg.Content)
	if len(msg.Attachments) == 0 {
		return baseText, nil
	}

	m.mu.Lock()
	attSvc := m.attachments
	m.mu.Unlock()

	var humanSourceID uuid.UUID
	if attSvc != nil && m.store != nil {
		if resolved, err := m.store.ResolveChannelHumanSource(ctx, ch.WorkspaceID, ch.CreatedBySourceID); err == nil {
			humanSourceID = resolved
		} else {
			chLogger.Warn("resolve human source for inbound attachment upload failed", "err", err)
		}
	}

	maxUploadBytes := attachments.DefaultMaxUploadBytes
	if attSvc != nil && attSvc.MaxUploadBytes() > 0 {
		maxUploadBytes = attSvc.MaxUploadBytes()
	}

	attachmentIDs := make([]uuid.UUID, 0, min(len(msg.Attachments), attachments.MaxAttachmentsPerTurn))
	notes := make([]string, 0)
	overflowCount := 0

	for _, att := range msg.Attachments {
		kind := att.Kind
		if kind == "" {
			kind = classifyAttachmentKind(att.MimeType, att.FileName)
			att.Kind = kind
		}

		// 语音、视频及无原始数据/下载地址的纯说明项直接转为可读占位或转写文本，不发起无效下载。
		if kind == InboundAttachmentAudio || kind == InboundAttachmentVideo || kind == InboundAttachmentOther {
			notes = append(notes, formatUnsupportedAttachmentNote(att))
			continue
		}
		hasPayload := len(att.RawData) > 0 || strings.TrimSpace(att.DownloadURL) != ""
		if !hasPayload {
			notes = append(notes, formatUnsupportedAttachmentNote(att))
			continue
		}

		// 对于普通文件，在下载前先按文件名和 MIME 过滤，避免盲目拉取大型 PDF/ZIP 等不支持的二进制文件。
		if kind == InboundAttachmentFile && len(att.RawData) == 0 && !isCandidateUploadableFile(att.FileName, att.MimeType) {
			notes = append(notes, formatUnsupportedAttachmentNote(att))
			continue
		}

		if len(attachmentIDs) >= attachments.MaxAttachmentsPerTurn {
			overflowCount++
			continue
		}

		if attSvc == nil || humanSourceID == uuid.Nil {
			// 未挂载附件服务时保留清晰占位，避免消息在测试或降级环境下被静默吞掉。
			prefix := "收到附件"
			if kind == InboundAttachmentImage {
				prefix = "收到图片"
			}
			notes = append(notes, fmt.Sprintf("[%s：当前未启用附件存储，暂无法读取原文件]", formatAttachmentLabel(prefix, att.FileName)))
			continue
		}

		if att.SizeBytes > maxUploadBytes {
			notes = append(notes, fmt.Sprintf("[%s：文件大小超出 %d MiB 上限，未入库]", formatAttachmentLabel("收到附件", att.FileName), maxUploadBytes>>20))
			continue
		}

		rawData := att.RawData
		respMime := ""
		if len(rawData) == 0 {
			var dlErr error
			rawData, respMime, dlErr = downloadInboundAttachment(ctx, nil, att.DownloadURL, att.DownloadHeaders, maxUploadBytes)
			if dlErr != nil {
				chLogger.Warn("download remote channel inbound attachment failed",
					"kind", kind,
					"file_name", att.FileName,
					"err", dlErr,
				)
				prefix := "收到附件"
				if kind == InboundAttachmentImage {
					prefix = "收到图片"
				}
				notes = append(notes, fmt.Sprintf("[%s，下载失败：%s]", formatAttachmentLabel(prefix, att.FileName), dlErr.Error()))
				continue
			}
		} else if int64(len(rawData)) > maxUploadBytes {
			notes = append(notes, fmt.Sprintf("[%s：文件大小超出 %d MiB 上限，未入库]", formatAttachmentLabel("收到附件", att.FileName), maxUploadBytes>>20))
			continue
		}

		fileName := inferInboundFileName(att, rawData, respMime)
		_, _, isImg := attachments.DetectImageFormat(rawData)
		if !isImg && !attachments.IsAllowedTextFileName(fileName) {
			att.FileName = fileName
			notes = append(notes, formatUnsupportedAttachmentNote(att))
			continue
		}

		uploaded, uploadErr := attSvc.Upload(ctx, attachments.UploadParams{
			WorkspaceID:        ch.WorkspaceID,
			UploadedBySourceID: humanSourceID,
			FileName:           fileName,
			RawData:            rawData,
		})
		if uploadErr != nil {
			chLogger.Warn("upload remote channel inbound attachment failed",
				"kind", kind,
				"file_name", fileName,
				"err", uploadErr,
			)
			prefix := "收到附件"
			if kind == InboundAttachmentImage {
				prefix = "收到图片"
			}
			notes = append(notes, fmt.Sprintf("[%s，未能入库：%s]", formatAttachmentLabel(prefix, fileName), uploadErr.Error()))
			continue
		}
		attachmentIDs = append(attachmentIDs, uploaded.ID)
	}

	if overflowCount > 0 {
		notes = append(notes, fmt.Sprintf("[另有 %d 个附件超出单条消息最多 %d 个附件的上限，未入库]", overflowCount, attachments.MaxAttachmentsPerTurn))
	}

	if len(notes) == 0 {
		return baseText, attachmentIDs
	}
	noteBlock := strings.Join(notes, "\n")
	if baseText == "" {
		return noteBlock, attachmentIDs
	}
	return baseText + "\n\n" + noteBlock, attachmentIDs
}

// decryptWeixinAES128ECB 解密微信 iLink CDN 加密媒体（AES-128-ECB + PKCS#7）。
// 密钥兼容 16 字节原文、32 位十六进制字符串、Base64(16 字节) 或 Base64(32 位十六进制字符串)。
func decryptWeixinAES128ECB(ciphertext []byte, rawKey string) ([]byte, error) {
	key, err := parseWeixinAESKey(rawKey)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("invalid ciphertext block length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ciphertext))
	for offset := 0; offset < len(ciphertext); offset += aes.BlockSize {
		block.Decrypt(plain[offset:offset+aes.BlockSize], ciphertext[offset:offset+aes.BlockSize])
	}
	return pkcs7Unpad(plain, aes.BlockSize)
}

func parseWeixinAESKey(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("empty aes key")
	}
	if len(trimmed) == 16 {
		return []byte(trimmed), nil
	}
	if len(trimmed) == 32 {
		if decoded, err := hex.DecodeString(trimmed); err == nil && len(decoded) == 16 {
			return decoded, nil
		}
	}
	if decoded, err := decodeBase64Flexible(trimmed); err == nil {
		if len(decoded) == 16 {
			return decoded, nil
		}
		if len(decoded) == 32 {
			if hexDecoded, hexErr := hex.DecodeString(string(decoded)); hexErr == nil && len(hexDecoded) == 16 {
				return hexDecoded, nil
			}
		}
	}
	return nil, errors.New("unsupported weixin aes key format")
}

// decryptWeComAES256CBC 解密企业微信 AI Bot 媒体文件（AES-256-CBC，IV 为密钥前 16 字节，PKCS#7 填充）。
func decryptWeComAES256CBC(ciphertext []byte, rawKey string) ([]byte, error) {
	key, err := decodeBase64Flexible(strings.TrimSpace(rawKey))
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid wecom aeskey")
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("invalid wecom ciphertext block length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plain, ciphertext)
	// 企业微信 PKCS#7 填充块大小可能为 32 或 16，优先按 32 校验再回退 16。
	if unpadded, err := pkcs7Unpad(plain, 32); err == nil {
		return unpadded, nil
	}
	return pkcs7Unpad(plain, aes.BlockSize)
}

func decodeBase64Flexible(raw string) ([]byte, error) {
	if raw == "" {
		return nil, errors.New("empty base64")
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(raw); err == nil {
		return b, nil
	}
	return base64.RawURLEncoding.DecodeString(raw)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || blockSize <= 0 {
		return nil, errors.New("invalid pkcs7 input")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, errors.New("invalid pkcs7 padding")
	}
	if !bytes.Equal(data[len(data)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return nil, errors.New("corrupt pkcs7 padding")
	}
	return data[:len(data)-pad], nil
}
