package modelclient

import (
	"errors"
	"strings"
)

var contextTooLargeMarkers = []string{
	"maximum context length",
	"context length exceeded",
	"context_length_exceeded",
	"context window",
	"input exceeds",
	"input is too long",
	"too many input tokens",
	"prompt is too long",
}

// IsContextTooLargeError 只识别模型上下文过大的错误。
// 429 的 token rate limit 不属于上下文溢出，不能通过压缩历史来解决。
func IsContextTooLargeError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 429 {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range contextTooLargeMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
