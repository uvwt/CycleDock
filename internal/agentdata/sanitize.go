package agentdata

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const Redacted = "[REDACTED]"

var sensitiveKeys = map[string]struct{}{
	"password":           {},
	"passwd":             {},
	"secret":             {},
	"clientsecret":       {},
	"apikey":             {},
	"apitoken":           {},
	"token":              {},
	"accesstoken":        {},
	"refreshtoken":       {},
	"sessiontoken":       {},
	"authorization":      {},
	"proxyauthorization": {},
	"cookie":             {},
	"setcookie":          {},
	"privatekey":         {},
	"credential":         {},
	"credentials":        {},
}

// NormalizeText 将文本规范化为 PostgreSQL text / jsonb 可安全持久化的有效 UTF-8 字符串。
//
// PostgreSQL 的 text 与 jsonb 类型底层均不允许 U+0000（NUL 字符，写库会报 SQLSTATE 22021 或 22P05），
// 也不接受非法 UTF-8 字节序列。这里在持久化边界统一将非法 UTF-8 字节与 NUL 字符替换为 U+FFFD，
// 同时完整保留中文、emoji、多语言字符、换行与制表符等正常 Unicode 内容。
func NormalizeText(value string) (string, bool) {
	hasNUL := strings.IndexByte(value, 0) >= 0
	validUTF8 := utf8.ValidString(value)
	if !hasNUL && validUTF8 {
		return value, false
	}

	normalized := value
	if !validUTF8 {
		normalized = strings.ToValidUTF8(normalized, "\uFFFD")
	}
	if hasNUL {
		normalized = strings.ReplaceAll(normalized, "\x00", "\uFFFD")
	}
	return normalized, true
}

// Sanitize returns a deep copy of JSON-like data with structurally identified
// credentials removed and text values normalized for PostgreSQL persistence.
// It deliberately does not try to regex arbitrary strings: doing so would
// corrupt ordinary tool evidence and still miss many real secrets.
func Sanitize(value any) any {
	sanitized, _ := SanitizeWithReport(value)
	return sanitized
}

// SanitizeWithReport 在执行结构化凭据脱敏的同时，规范化所有键名与字符串值中的非法 UTF-8 及 NUL 字符，
// 并返回本次处理是否发生过异常 Unicode 字符替换（不含敏感字段脱敏），供调用方按需记录脱敏摘要日志。
func SanitizeWithReport(value any) (any, bool) {
	switch current := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(current))
		normalizedAny := false
		for key, item := range current {
			cleanKey, keyChanged := NormalizeText(key)
			if keyChanged {
				normalizedAny = true
			}
			if isSensitiveKey(cleanKey) {
				out[cleanKey] = Redacted
				continue
			}
			cleanItem, itemChanged := SanitizeWithReport(item)
			if itemChanged {
				normalizedAny = true
			}
			out[cleanKey] = cleanItem
		}
		return out, normalizedAny
	case []any:
		out := make([]any, len(current))
		normalizedAny := false
		for i, item := range current {
			cleanItem, itemChanged := SanitizeWithReport(item)
			if itemChanged {
				normalizedAny = true
			}
			out[i] = cleanItem
		}
		return out, normalizedAny
	case []string:
		out := make([]string, len(current))
		normalizedAny := false
		for i, item := range current {
			cleanItem, itemChanged := NormalizeText(item)
			if itemChanged {
				normalizedAny = true
			}
			out[i] = cleanItem
		}
		return out, normalizedAny
	case string:
		clean, changed := NormalizeText(current)
		trimmed := strings.TrimSpace(clean)
		if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
			return clean, changed
		}
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return clean, changed
		}
		// 嵌套 JSON 字符串（如 MCP TextContent 承载的序列化结果）解码后可能重新还原出 \u0000，
		// 递归执行脱敏与 Unicode 规范化后再重编码，确保持久化与后续二次解析均安全。
		sanitizedDecoded, nestedChanged := SanitizeWithReport(decoded)
		safe, err := json.Marshal(sanitizedDecoded)
		if err != nil {
			return clean, changed
		}
		safeJSON, jsonChanged := NormalizeJSONB(safe)
		return string(safeJSON), changed || nestedChanged || jsonChanged
	default:
		return value, false
	}
}

// MarshalJSONB 将任意值序列化为可直接写入 PostgreSQL jsonb 列的 UTF-8 JSON 字节切片。
func MarshalJSONB(value any) ([]byte, bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false, err
	}
	normalized, changed := NormalizeJSONB(raw)
	return normalized, changed, nil
}

// NormalizeJSONB 清理已序列化 JSON 字节切片中会导致 PostgreSQL jsonb 解析报错的 \u0000 转义与原始 NUL 字节。
//
// Go 的 json.Marshal 会将非法 UTF-8 与孤立 UTF-16 代理项转为 \ufffd，但会把 U+0000 编码为合法 JSON 转义 \u0000。
// PostgreSQL jsonb 在解析任意键或字符串值中的未转义 \u0000 时都会拒绝并返回 SQLSTATE 22P05。
// 这里仅替换真正表示 NUL 字符的 \u0000 转义（前导反斜杠数量为奇数）为 \ufffd，不破坏字面量 "\\u0000"。
func NormalizeJSONB(raw []byte) ([]byte, bool) {
	if len(raw) == 0 {
		return raw, false
	}

	changed := false
	data := raw
	if !utf8.Valid(data) {
		data = []byte(strings.ToValidUTF8(string(data), "\uFFFD"))
		changed = true
	}

	hasRawNUL := false
	for _, b := range data {
		if b == 0 {
			hasRawNUL = true
			break
		}
	}
	if !hasRawNUL && !containsJSONNullEscape(data) {
		return data, changed
	}

	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] == 0 {
			out = append(out, "\uFFFD"...)
			changed = true
			i++
			continue
		}
		if i+6 <= len(data) && isJSONNullEscape(data[i:i+6]) && hasEvenPrecedingBackslashes(data, i) {
			out = append(out, `\ufffd`...)
			changed = true
			i += 6
			continue
		}
		out = append(out, data[i])
		i++
	}
	return out, changed
}

func containsJSONNullEscape(data []byte) bool {
	for i := 0; i+6 <= len(data); i++ {
		if isJSONNullEscape(data[i : i+6]) {
			return true
		}
	}
	return false
}

func isJSONNullEscape(chunk []byte) bool {
	return len(chunk) == 6 &&
		chunk[0] == '\\' &&
		(chunk[1] == 'u' || chunk[1] == 'U') &&
		chunk[2] == '0' &&
		chunk[3] == '0' &&
		chunk[4] == '0' &&
		chunk[5] == '0'
}

// hasEvenPrecedingBackslashes 判断位置 pos 的 '\' 前面是否有偶数个连续的 '\'。
// 若为偶数个，说明当前 '\' 不是被转义的字面反斜杠，而是实际生效的 JSON 转义起始符。
func hasEvenPrecedingBackslashes(data []byte, pos int) bool {
	count := 0
	for i := pos - 1; i >= 0 && data[i] == '\\'; i-- {
		count++
	}
	return count%2 == 0
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	normalized = replacer.Replace(normalized)
	_, ok := sensitiveKeys[normalized]
	return ok
}
