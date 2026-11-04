package mcpclient

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrorCategory classifies MCP errors to determine appropriate recovery or reporting action.
type ErrorCategory int

const (
	// CategoryUnknown represents an unclassified error.
	CategoryUnknown ErrorCategory = iota
	// CategoryCallerCanceled indicates the caller's context was canceled or exceeded deadline.
	// This error MUST NOT trigger reconnection or automatic retry.
	CategoryCallerCanceled
	// CategoryTimeout indicates the tool execution timed out at the transport or request level.
	// This is a recoverable error to report back to the model without killing the connection.
	CategoryTimeout
	// CategoryConnectionClosed indicates the underlying connection or transport is closed/broken,
	// requiring an automatic reconnection before subsequent calls can succeed.
	CategoryConnectionClosed
	// CategoryTransient indicates a temporary failure (e.g. rate limit 429, 502/503/504)
	// that may succeed on immediate or backoff retry.
	CategoryTransient
	// CategoryTerminal indicates an unrecoverable client or protocol error (e.g. invalid params, bad schema).
	CategoryTerminal
)

func (c ErrorCategory) String() string {
	switch c {
	case CategoryCallerCanceled:
		return "CallerCanceled"
	case CategoryTimeout:
		return "Timeout"
	case CategoryConnectionClosed:
		return "ConnectionClosed"
	case CategoryTransient:
		return "Transient"
	case CategoryTerminal:
		return "Terminal"
	default:
		return "Unknown"
	}
}

// ClassifyError inspects an error returned by an MCP operation, considering callerCtx.
func ClassifyError(callerCtx context.Context, err error) ErrorCategory {
	if err == nil {
		return CategoryUnknown
	}

	// 1. Check if the parent/caller context itself is canceled or timed out.
	// Caller cancellation must take absolute precedence and NEVER trigger reconnect.
	if callerCtx != nil && callerCtx.Err() != nil {
		return CategoryCallerCanceled
	}

	// 2. Check for explicit context errors (e.g. per-call timeout or deadline exceeded)
	if errors.Is(err, context.DeadlineExceeded) {
		return CategoryTimeout
	}
	if errors.Is(err, context.Canceled) {
		return CategoryCallerCanceled
	}

	// 3. Check for MCP SDK specific connection closed sentinel
	if errors.Is(err, mcp.ErrConnectionClosed) {
		return CategoryConnectionClosed
	}

	// 4. Check for standard library net and io errors indicating broken connection
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return CategoryConnectionClosed
	}

	// Check for network errors like connection refused, reset, broken pipe
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return CategoryTimeout
		}
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNRESET) ||
			errors.Is(opErr.Err, syscall.ECONNABORTED) ||
			errors.Is(opErr.Err, syscall.ECONNREFUSED) ||
			errors.Is(opErr.Err, syscall.EPIPE) {
			return CategoryConnectionClosed
		}
	}

	// 5. String-based classification for wrapped JSON-RPC 2.0 / MCP SDK transport errors
	msg := strings.ToLower(err.Error())

	// Reconnection needed patterns
	if strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "connection is closed") ||
		strings.Contains(msg, "closed connection") ||
		strings.Contains(msg, "client is closing") ||
		strings.Contains(msg, "server is closing") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "transport is closed") ||
		strings.Contains(msg, "session not found") ||
		strings.Contains(msg, "session closed") {
		return CategoryConnectionClosed
	}

	// Timeout patterns
	if strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out") {
		return CategoryTimeout
	}

	// Transient HTTP error patterns
	if strings.Contains(msg, "429") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "502") ||
		strings.Contains(msg, "bad gateway") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "service unavailable") ||
		strings.Contains(msg, "504") ||
		strings.Contains(msg, "gateway timeout") {
		return CategoryTransient
	}

	return CategoryTerminal
}

// IsConnectionClosed reports whether err indicates that the connection has closed or terminated.
func IsConnectionClosed(callerCtx context.Context, err error) bool {
	return ClassifyError(callerCtx, err) == CategoryConnectionClosed
}

// IsTimeout reports whether err indicates a timeout on tool execution.
func IsTimeout(callerCtx context.Context, err error) bool {
	return ClassifyError(callerCtx, err) == CategoryTimeout
}
