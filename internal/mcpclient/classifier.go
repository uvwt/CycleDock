package mcpclient

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrorCategory 对 MCP 调用错误进行分类，以决定是否重连、淘汰坏会话或退避重试。
type ErrorCategory int

const (
	// CategoryUnknown 表示空错误或未分类状态。
	CategoryUnknown ErrorCategory = iota
	// CategoryCallerCanceled 表示调用方父级 Context 已取消或到期，禁止触发重连或重试。
	CategoryCallerCanceled
	// CategoryTimeout 表示单次工具执行超时（含网关 504），直接交回模型，不重试也不主动拆毁健康连接。
	CategoryTimeout
	// CategoryConnectionClosed 表示底层连接已关闭或服务端 Session 已失效（请求尚未执行），可自动重连后重试一次。
	CategoryConnectionClosed
	// CategoryConnectionDropped 表示请求发出后在读取响应途中连接中断或流损坏；需淘汰坏会话供后续调用恢复，但本次禁止自动重发以防重复副作用。
	CategoryConnectionDropped
	// CategoryTransient 表示服务端限流或暂不可用（429/502/503），可短暂退避后重试。
	CategoryTransient
	// CategoryTerminal 表示协议错误、参数错误或响应超限等确定性错误，立即返回。
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
	case CategoryConnectionDropped:
		return "ConnectionDropped"
	case CategoryTransient:
		return "Transient"
	case CategoryTerminal:
		return "Terminal"
	default:
		return "Unknown"
	}
}

// ClassifyError 根据调用方 Context 与错误对象判断 MCP 错误类别。
func ClassifyError(callerCtx context.Context, err error) ErrorCategory {
	return classifyCallError(callerCtx, nil, err)
}

// classifyCallError 结合父级 callerCtx 与单次工具超时派生出的 callCtx 进行分类。
// 引入 callCtx 是为了区分“本次调用自身超时”与“go-sdk streamableClientConn 缓存的上一轮读流超时死连接错误”。
func classifyCallError(callerCtx, callCtx context.Context, err error) ErrorCategory {
	if err == nil {
		return CategoryUnknown
	}

	// 1. 调用方自身已取消或到期时，绝对禁止触发重连或自动重试。
	if callerCtx != nil && callerCtx.Err() != nil {
		return CategoryCallerCanceled
	}

	// 2. 服务端正常返回的 JSON-RPC 2.0 错误（如参数校验失败、方法不存在）属于业务/协议错误，
	// 必须在任何字符串匹配前短路，避免错误文案中恰好包含状态码或 timeout 词组被误判。
	var wireErr *jsonrpc.Error
	if errors.As(err, &wireErr) {
		return CategoryTerminal
	}

	// 3. 单次工具调用自身的 Context 超时或显式超时错误。
	if callCtx != nil && errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return CategoryTimeout
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CategoryTimeout
	}
	if errors.Is(err, context.Canceled) {
		return CategoryCallerCanceled
	}

	// 4. MCP SDK 标准连接关闭与 Session 过期（HTTP 404）哨兵，请求均未进入服务端工具执行逻辑。
	if errors.Is(err, mcp.ErrConnectionClosed) || errors.Is(err, mcp.ErrSessionMissing) {
		return CategoryConnectionClosed
	}

	// 5. 标准库 IO 与网络层错误分类。
	// io.ErrUnexpectedEOF 代表响应流读取到一半中断，服务端大概率已执行请求，归为 Dropped 防止重复副作用。
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return CategoryConnectionDropped
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return CategoryConnectionClosed
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return CategoryTimeout
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

	// 6. 针对 go-sdk 内部未用 %w 暴露底层类型的传输层包装错误做受控模式匹配。
	msg := strings.ToLower(err.Error())

	// 本地 boundedTransport 拦截超大响应体：go-sdk 的 handleJSON 会将连接置为永久失败，
	// 但错误本质是该工具响应超限，禁止重试，由调用方淘汰坏连接即可。
	if strings.Contains(msg, "mcp response exceeds ") {
		return CategoryTerminal
	}

	// go-sdk streamableClientConn 在读取响应体或 SSE 流失败后会通过 c.fail() 永久置死连接：
	// - 若上一轮读响应体超时将连接置死，下一轮新调用（callCtx 未超时）会在 Write() 入口立即收到缓存的
	//   "failed to read body: context deadline exceeded"，此时请求完全未发出，应触发重连恢复。
	// - 若当前调用自身在读取响应体/解码响应/SSE 流重连时中断，说明请求已送达服务端，归为 Dropped 仅淘汰坏连接。
	if strings.Contains(msg, "failed to read body:") {
		if callCtx != nil && callCtx.Err() == nil && strings.Contains(msg, "deadline exceeded") {
			return CategoryConnectionClosed
		}
		return CategoryConnectionDropped
	}
	if strings.Contains(msg, "failed to decode response:") ||
		(strings.Contains(msg, "exceeded ") && strings.Contains(msg, "retries without progress")) ||
		strings.Contains(msg, "failed to reconnect (session id:") {
		return CategoryConnectionDropped
	}

	// 连接已关闭或握手拒绝，可安全重连后重发一次。
	if strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "connection is closed") ||
		strings.Contains(msg, "closed connection") ||
		strings.Contains(msg, "client is closing") ||
		strings.Contains(msg, "server is closing") ||
		strings.Contains(msg, "standalone sse request failed") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "transport is closed") ||
		strings.Contains(msg, "session not found") ||
		strings.Contains(msg, "session closed") {
		return CategoryConnectionClosed
	}

	// 网关 504 与传输层超时：上游可能仍在执行，归为 Timeout 不做自动重试。
	if hasHTTPStatus(msg, "504", "gateway timeout") ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "tls handshake timeout") ||
		strings.Contains(msg, "request timeout") ||
		strings.Contains(msg, "connection timeout") ||
		strings.Contains(msg, "awaiting headers") ||
		strings.Contains(msg, "timed out") {
		return CategoryTimeout
	}

	// 429 / 502 / 503 属于服务端限流或网关尚未转发成功的瞬态错误，可短退避重试。
	if hasHTTPStatus(msg, "429", "too many requests") ||
		hasHTTPStatus(msg, "502", "bad gateway") ||
		hasHTTPStatus(msg, "503", "service unavailable") {
		return CategoryTransient
	}

	return CategoryTerminal
}

func hasHTTPStatus(lowerMsg, code, phrase string) bool {
	if phrase != "" && strings.Contains(lowerMsg, phrase) {
		return true
	}
	return strings.Contains(lowerMsg, "status "+code) ||
		strings.Contains(lowerMsg, "status code "+code) ||
		strings.Contains(lowerMsg, "http "+code)
}
