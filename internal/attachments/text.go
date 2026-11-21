package attachments

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	KindImage = "image"
	KindText  = "text"

	EncodingIdentity = "identity"
	EncodingGzip     = "gzip"

	DefaultMaxTextUploadBytes int64 = 2 << 20 // 单份文本/日志/代码附件原始大小上限 2 MiB
	TextMimeType                    = "text/plain; charset=utf-8"
	maxFileNameRunes                = 180
)

var (
	ErrEmptyFile           = errors.New("attachment payload is empty")
	ErrTextTooLarge        = errors.New("text attachment exceeds maximum upload size")
	ErrUnsupportedTextFile = errors.New("unsupported text file format or binary content")
)

var allowedTextExtensions = map[string]struct{}{
	".txt":        {},
	".log":        {},
	".md":         {},
	".markdown":   {},
	".json":       {},
	".jsonl":      {},
	".ndjson":     {},
	".yaml":       {},
	".yml":        {},
	".toml":       {},
	".ini":        {},
	".cfg":        {},
	".conf":       {},
	".csv":        {},
	".tsv":        {},
	".xml":        {},
	".sql":        {},
	".sh":         {},
	".bash":       {},
	".zsh":        {},
	".ps1":        {},
	".bat":        {},
	".cmd":        {},
	".go":         {},
	".mod":        {},
	".sum":        {},
	".ts":         {},
	".tsx":        {},
	".js":         {},
	".jsx":        {},
	".mjs":        {},
	".cjs":        {},
	".py":         {},
	".rs":         {},
	".java":       {},
	".kt":         {},
	".kts":        {},
	".c":          {},
	".cpp":        {},
	".cc":         {},
	".cxx":        {},
	".h":          {},
	".hpp":        {},
	".cs":         {},
	".rb":         {},
	".php":        {},
	".swift":      {},
	".scala":      {},
	".lua":        {},
	".r":          {},
	".css":        {},
	".scss":       {},
	".less":       {},
	".html":       {},
	".htm":        {},
	".diff":       {},
	".patch":      {},
	".dockerfile": {},
	".properties": {},
	".graphql":    {},
	".gql":        {},
	".proto":      {},
	".tf":         {},
	".tfvars":     {},
	".mk":         {},
	".cmake":      {},
	".gradle":     {},
	".vue":        {},
	".svelte":     {},
	".astro":      {},
}

var allowedSpecialTextFileNames = map[string]struct{}{
	".env.example":   {},
	"dockerfile":     {},
	"makefile":       {},
	"readme":         {},
	"license":        {},
	"changelog":      {},
	".gitignore":     {},
	".gitattributes": {},
	".editorconfig":  {},
	".dockerignore":  {},
}

type ProcessedTextFile struct {
	CompressedData  []byte
	MimeType        string
	FileName        string
	Ext             string
	SizeBytes       int64
	StoredSizeBytes int64
	LineCount       int
	SHA256          string
	StorageEncoding string
}

// SanitizeFileName 剥离路径段与控制字符，生成安全的展示与下载文件名。
func SanitizeFileName(raw string) string {
	replaced := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if replaced == "" {
		return ""
	}
	base := path.Base(replaced)
	if base == "." || base == ".." || base == "/" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(base))
	for _, r := range base {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if r == '/' || r == '\\' || r == '"' {
			continue
		}
		b.WriteRune(r)
	}
	clean := strings.TrimSpace(b.String())
	if clean == "" || clean == "." || clean == ".." {
		return ""
	}
	runes := []rune(clean)
	if len(runes) <= maxFileNameRunes {
		return clean
	}
	ext := path.Ext(clean)
	extRunes := []rune(ext)
	if len(extRunes) > 0 && len(extRunes) < maxFileNameRunes-4 {
		stem := runes[:maxFileNameRunes-len(extRunes)]
		return string(stem) + ext
	}
	return string(runes[:maxFileNameRunes])
}

// IsAllowedTextFileName 严格依据白名单校验文本/日志/代码文件名，明确排除 PDF、压缩包与二进制文件。
func IsAllowedTextFileName(fileName string) bool {
	clean := SanitizeFileName(fileName)
	if clean == "" {
		return false
	}
	lower := strings.ToLower(clean)
	if _, ok := allowedSpecialTextFileNames[lower]; ok {
		return true
	}
	if strings.HasSuffix(lower, ".env.example") {
		return true
	}
	ext := path.Ext(lower)
	if ext == "" {
		return false
	}
	_, ok := allowedTextExtensions[ext]
	return ok
}

// ValidateTextContent 校验字节流为合法 UTF-8 纯文本，拒绝空字节、PDF/归档/可执行文件魔数与二进制内容。
func ValidateTextContent(raw []byte) error {
	if len(raw) == 0 {
		return ErrEmptyFile
	}
	if bytes.IndexByte(raw, 0x00) >= 0 {
		return fmt.Errorf("%w: binary null bytes are not allowed", ErrUnsupportedTextFile)
	}
	if !utf8.Valid(raw) {
		return fmt.Errorf("%w: file must be valid UTF-8 text", ErrUnsupportedTextFile)
	}

	head := bytes.TrimLeft(raw[:min(len(raw), 64)], " \t\r\n")
	if bytes.HasPrefix(head, []byte("%PDF-")) {
		return fmt.Errorf("%w: PDF files are not supported", ErrUnsupportedTextFile)
	}
	if bytes.HasPrefix(raw, []byte{'P', 'K', 0x03, 0x04}) ||
		bytes.HasPrefix(raw, []byte{'P', 'K', 0x05, 0x06}) ||
		bytes.HasPrefix(raw, []byte{'P', 'K', 0x07, 0x08}) ||
		bytes.HasPrefix(raw, []byte{0x1f, 0x8b}) ||
		bytes.HasPrefix(raw, []byte{0x7f, 'E', 'L', 'F'}) {
		return fmt.Errorf("%w: archive or binary files are not supported", ErrUnsupportedTextFile)
	}

	// 检查是否存在明显异常的非打印控制字符，防止伪造后缀的二进制内容绕过。
	disallowedCtrl := 0
	for _, b := range raw {
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' && b != '\f' && b != '\v' && b != 0x1b {
			disallowedCtrl++
		}
	}
	if disallowedCtrl > 0 {
		if len(raw) < 256 || disallowedCtrl*100 > len(raw) {
			return fmt.Errorf("%w: content appears to be binary", ErrUnsupportedTextFile)
		}
	}
	return nil
}

// CountLines 统计 UTF-8 文本行数（末尾换行不额外多算空行）。
func CountLines(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	n := bytes.Count(raw, []byte{'\n'})
	if raw[len(raw)-1] != '\n' {
		n++
	}
	if n == 0 {
		return 1
	}
	return n
}

// ProcessTextFile 校验白名单、体积上限与合法 UTF-8 内容，并使用标准库 gzip 透明压缩以节省磁盘。
func ProcessTextFile(fileName string, raw []byte, maxBytes int64) (ProcessedTextFile, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxTextUploadBytes
	}
	cleanName := SanitizeFileName(fileName)
	if !IsAllowedTextFileName(cleanName) {
		return ProcessedTextFile{}, fmt.Errorf(
			"%w: only plain text, log, and source code files are allowed",
			ErrUnsupportedTextFile,
		)
	}
	if len(raw) == 0 {
		return ProcessedTextFile{}, ErrEmptyFile
	}
	if int64(len(raw)) > maxBytes {
		return ProcessedTextFile{}, fmt.Errorf("%w: max %d bytes", ErrTextTooLarge, maxBytes)
	}
	if err := ValidateTextContent(raw); err != nil {
		return ProcessedTextFile{}, err
	}

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	if err != nil {
		return ProcessedTextFile{}, fmt.Errorf("init gzip writer: %w", err)
	}
	if _, err := gz.Write(raw); err != nil {
		_ = gz.Close()
		return ProcessedTextFile{}, fmt.Errorf("gzip compress text attachment: %w", err)
	}
	if err := gz.Close(); err != nil {
		return ProcessedTextFile{}, fmt.Errorf("close gzip writer: %w", err)
	}

	sum := sha256.Sum256(raw)
	return ProcessedTextFile{
		CompressedData:  buf.Bytes(),
		MimeType:        TextMimeType,
		FileName:        cleanName,
		Ext:             ".txt.gz",
		SizeBytes:       int64(len(raw)),
		StoredSizeBytes: int64(buf.Len()),
		LineCount:       CountLines(raw),
		SHA256:          hex.EncodeToString(sum[:]),
		StorageEncoding: EncodingGzip,
	}, nil
}

// DecompressGzip 以有界上限安全解压 gzip 文本附件，防止解压炸弹。
func DecompressGzip(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxTextUploadBytes
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	data, err := io.ReadAll(io.LimitReader(gz, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("decompress gzip stream: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: decompressed payload exceeds limit", ErrTextTooLarge)
	}
	return data, nil
}
