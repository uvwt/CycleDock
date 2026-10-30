package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/mcpclient"
)

const (
	nexusDockExecutionTimeout = 30 * time.Second
	nexusDockCancelTimeout    = 5 * time.Second
)

var errNexusDockEventStreamClosed = errors.New("NexusDock execution event stream closed before terminal status")
var errNexusDockSchemaChangedFallback = errors.New("NexusDock execution capability schema changed before dispatch")

type nexusDockExecutionKey struct {
	NodeID string
	Name   string
}

type nexusDockExecutionTarget struct {
	CapabilityID string
	ContractHash string
}

type nexusDockExecutionRouter struct {
	rest    *nexusDockRESTClient
	targets map[nexusDockExecutionKey]nexusDockExecutionTarget

	mu          sync.Mutex
	unsupported bool
}

type nexusDockExecutionError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type nexusDockExecution struct {
	ID       string                   `json:"id"`
	Status   string                   `json:"status"`
	Version  int64                    `json:"version"`
	Result   map[string]any           `json:"result,omitempty"`
	Error    *nexusDockExecutionError `json:"error,omitempty"`
	ToolName string                   `json:"tool_name,omitempty"`
}

type nexusDockExecutionResponse struct {
	Execution nexusDockExecution `json:"execution"`
}

type nexusDockAPIError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type nexusDockAPIStatusError struct {
	Status  int
	Code    string
	Message string
}

func (e *nexusDockAPIStatusError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = e.Code
	}
	return fmt.Sprintf("NexusDock Execution failed: %s (HTTP %d)", message, e.Status)
}

type nexusDockExecutionEvent struct {
	Status  string `json:"status"`
	Version int64  `json:"version"`
}

func newNexusDockExecutionRouter(rest *nexusDockRESTClient, capabilities []directoryCapability) *nexusDockExecutionRouter {
	if rest == nil {
		return nil
	}
	targets := make(map[nexusDockExecutionKey]nexusDockExecutionTarget)
	for _, capability := range capabilities {
		if capability.Kind != "tool" || !capability.Online || capability.Stale {
			continue
		}
		id := strings.TrimSpace(capability.ID)
		nodeID := strings.TrimSpace(capability.NodeID)
		name := strings.TrimSpace(capability.Name)
		hash := strings.TrimSpace(capability.ContractHash)
		if id == "" || nodeID == "" || name == "" || hash == "" {
			continue
		}
		targets[nexusDockExecutionKey{NodeID: nodeID, Name: name}] = nexusDockExecutionTarget{
			CapabilityID: id, ContractHash: hash,
		}
	}
	if len(targets) == 0 {
		return nil
	}
	return &nexusDockExecutionRouter{rest: rest, targets: targets}
}

// Call 只处理 NexusDock 第一方 MCP 中能精确映射到目录 Tool capability 的调用。
// handled=false 表示它不是设备 Tool（或旧 Cloud 尚无 Execution API），调用方继续走原 MCP。
func (r *nexusDockExecutionRouter) Call(
	ctx context.Context,
	attemptID uuid.UUID,
	functionCallID string,
	tool mcpclient.Tool,
	arguments map[string]any,
) (raw string, handled bool, err error) {
	if r == nil || tool.ServerName != firstPartyNexusDockServerName || r.isUnsupported() {
		return "", false, nil
	}
	nodeID, toolName, input, ok := nexusDockExecutionInput(tool.RemoteName, arguments)
	if !ok {
		return "", false, nil
	}
	target, ok := r.targets[nexusDockExecutionKey{NodeID: nodeID, Name: toolName}]
	if !ok {
		return "", false, nil
	}
	functionCallID = strings.TrimSpace(functionCallID)
	if functionCallID == "" {
		return "", true, errors.New("NexusDock Execution requires a stable ADK function call id")
	}
	idempotencyKey := nexusDockIdempotencyKey(attemptID, functionCallID, nodeID, toolName)
	body := map[string]any{
		"capability_id": target.CapabilityID,
		"contract_hash": target.ContractHash,
		"input":         input,
		"timeout_ms":    int(nexusDockExecutionTimeout / time.Millisecond),
	}

	callCtx, cancel := context.WithTimeout(ctx, nexusDockExecutionTimeout)
	defer cancel()
	execution, unsupported, err := r.create(callCtx, idempotencyKey, body)
	if unsupported {
		r.markUnsupported()
		return "", false, nil
	}
	if err != nil {
		if errors.Is(err, errNexusDockSchemaChangedFallback) {
			// Cloud 的这个 409 只会在创建记录和发送前返回，因此可以安全交还原 MCP 路径。
			// MCP 会针对当前连接再次做 Fleet/现场 Schema 校验，不会直接执行旧契约参数。
			return "", false, nil
		}
		var apiStatus *nexusDockAPIStatusError
		if errors.As(err, &apiStatus) && nexusDockSafeToolError(apiStatus) {
			encoded, encodeErr := nexusDockPreDispatchErrorModelResult(apiStatus)
			if encodeErr != nil {
				return "", true, encodeErr
			}
			return string(encoded), true, nil
		}
		if callCtx.Err() != nil {
			r.recoverAndCancel(idempotencyKey, body)
		}
		return "", true, err
	}
	if !nexusDockTerminalStatus(execution.Status) {
		if _, err := r.waitTerminal(callCtx, execution.ID, execution.Version); err != nil {
			if callCtx.Err() != nil {
				r.cancel(execution.ID)
			}
			return "", true, err
		}
		detail, err := r.get(callCtx, execution.ID)
		if err != nil {
			return "", true, err
		}
		execution = detail
	}
	encoded, err := nexusDockExecutionModelResult(execution)
	if err != nil {
		return "", true, err
	}
	return string(encoded), true, nil
}

func nexusDockExecutionInput(remoteName string, arguments map[string]any) (string, string, map[string]any, bool) {
	if remoteName == "node_tool_call" {
		nodeID, _ := arguments["node_id"].(string)
		name, _ := arguments["name"].(string)
		input, _ := arguments["arguments"].(map[string]any)
		nodeID, name = strings.TrimSpace(nodeID), strings.TrimSpace(name)
		if nodeID == "" || name == "" {
			return "", "", nil, false
		}
		if input == nil {
			input = map[string]any{}
		}
		return nodeID, name, input, true
	}
	nodeID, _ := arguments["node_id"].(string)
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return "", "", nil, false
	}
	input := make(map[string]any, len(arguments))
	for key, value := range arguments {
		if key != "node_id" {
			input[key] = value
		}
	}
	return nodeID, remoteName, input, true
}

func nexusDockIdempotencyKey(attemptID uuid.UUID, functionCallID, nodeID, toolName string) string {
	sum := sha256.Sum256([]byte(attemptID.String() + "\x00" + functionCallID + "\x00" + nodeID + "\x00" + toolName))
	return "cd_exec_" + hex.EncodeToString(sum[:])
}

func (r *nexusDockExecutionRouter) create(
	ctx context.Context,
	idempotencyKey string,
	body map[string]any,
) (nexusDockExecution, bool, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		status, _, raw, err := r.rest.request(ctx, http.MethodPost, "/v1/integrations/executions", body, map[string]string{
			"Idempotency-Key": idempotencyKey,
		}, 1<<20)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nexusDockExecution{}, false, ctx.Err()
			}
			if attempt == 0 {
				if !sleepContext(ctx, 150*time.Millisecond) {
					return nexusDockExecution{}, false, ctx.Err()
				}
				continue
			}
			return nexusDockExecution{}, false, fmt.Errorf("create NexusDock execution: %w", err)
		}
		var response nexusDockExecutionResponse
		if status == http.StatusAccepted || status == http.StatusOK {
			if err := json.Unmarshal(raw, &response); err != nil || response.Execution.ID == "" {
				return nexusDockExecution{}, false, errors.New("NexusDock Execution create returned an invalid response")
			}
			return response.Execution, false, nil
		}
		apiErr, decoded := decodeNexusDockAPIError(raw)
		if status == http.StatusNotFound && (!decoded || apiErr.Error.Code == "NOT_FOUND" || apiErr.Error.Code == "") {
			return nexusDockExecution{}, true, nil
		}
		if status == http.StatusConflict && decoded && apiErr.Error.Code == "CAPABILITY_SCHEMA_CHANGED" {
			return nexusDockExecution{}, false, errNexusDockSchemaChangedFallback
		}
		if status >= http.StatusInternalServerError && attempt == 0 {
			if !sleepContext(ctx, 150*time.Millisecond) {
				return nexusDockExecution{}, false, ctx.Err()
			}
			continue
		}
		return nexusDockExecution{}, false, nexusDockHTTPError("create", status, apiErr, decoded)
	}
	return nexusDockExecution{}, false, fmt.Errorf("create NexusDock execution: %w", lastErr)
}

func (r *nexusDockExecutionRouter) get(ctx context.Context, executionID string) (nexusDockExecution, error) {
	var response nexusDockExecutionResponse
	status, _, err := r.rest.json(ctx, http.MethodGet, "/v1/integrations/executions/"+executionID, nil, nil, &response)
	if err != nil {
		return nexusDockExecution{}, fmt.Errorf("read NexusDock execution: %w", err)
	}
	if status != http.StatusOK {
		return nexusDockExecution{}, fmt.Errorf("read NexusDock execution: HTTP %d", status)
	}
	if response.Execution.ID == "" {
		return nexusDockExecution{}, errors.New("NexusDock Execution detail omitted id")
	}
	return response.Execution, nil
}

func (r *nexusDockExecutionRouter) waitTerminal(ctx context.Context, executionID string, lastVersion int64) (nexusDockExecutionEvent, error) {
	for {
		event, err := r.watchOnce(ctx, executionID, lastVersion)
		if err == nil && nexusDockTerminalStatus(event.Status) {
			return event, nil
		}
		if err == nil {
			lastVersion = max(lastVersion, event.Version)
		} else if !errors.Is(err, errNexusDockEventStreamClosed) {
			return nexusDockExecutionEvent{}, err
		}
		if ctx.Err() != nil {
			return nexusDockExecutionEvent{}, ctx.Err()
		}
		if !sleepContext(ctx, 200*time.Millisecond) {
			return nexusDockExecutionEvent{}, ctx.Err()
		}
	}
}

func (r *nexusDockExecutionRouter) watchOnce(ctx context.Context, executionID string, lastVersion int64) (nexusDockExecutionEvent, error) {
	headers := map[string]string{}
	if lastVersion > 0 {
		headers["Last-Event-ID"] = strconv.FormatInt(lastVersion, 10)
	}
	response, err := r.rest.stream(ctx, "/v1/integrations/executions/"+executionID+"/events", headers)
	if err != nil {
		if ctx.Err() != nil {
			return nexusDockExecutionEvent{}, ctx.Err()
		}
		return nexusDockExecutionEvent{}, errNexusDockEventStreamClosed
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		apiErr, decoded := decodeNexusDockAPIError(raw)
		return nexusDockExecutionEvent{}, nexusDockHTTPError("watch", response.StatusCode, apiErr, decoded)
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 256<<10)
	var latest nexusDockExecutionEvent
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var event nexusDockExecutionEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return nexusDockExecutionEvent{}, fmt.Errorf("decode NexusDock execution event: %w", err)
		}
		if event.Version > latest.Version {
			latest = event
		}
		if nexusDockTerminalStatus(event.Status) {
			return event, nil
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return latest, errNexusDockEventStreamClosed
	}
	if ctx.Err() != nil {
		return latest, ctx.Err()
	}
	return latest, errNexusDockEventStreamClosed
}

func (r *nexusDockExecutionRouter) cancel(executionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), nexusDockCancelTimeout)
	defer cancel()
	_, _, _, _ = r.rest.request(ctx, http.MethodPost, "/v1/integrations/executions/"+executionID+"/cancel", nil, nil, 1<<20)
}

func (r *nexusDockExecutionRouter) recoverAndCancel(idempotencyKey string, body map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), nexusDockCancelTimeout)
	defer cancel()
	execution, unsupported, err := r.create(ctx, idempotencyKey, body)
	if err == nil && !unsupported && execution.ID != "" && !nexusDockTerminalStatus(execution.Status) {
		r.cancel(execution.ID)
	}
}

func (r *nexusDockExecutionRouter) isUnsupported() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.unsupported
}

func (r *nexusDockExecutionRouter) markUnsupported() {
	r.mu.Lock()
	r.unsupported = true
	r.mu.Unlock()
}

func nexusDockTerminalStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled", "unknown":
		return true
	default:
		return false
	}
}

func nexusDockExecutionModelResult(execution nexusDockExecution) ([]byte, error) {
	if execution.Status == "succeeded" {
		if execution.Result == nil {
			execution.Result = map[string]any{}
		}
		_, hasContent := execution.Result["content"]
		_, hasErrorFlag := execution.Result["isError"]
		if hasContent && hasErrorFlag {
			return json.Marshal(execution.Result)
		}
		// AgentDock Tool 可以直接返回普通结构化对象；MCP 会把这种结果包装成标准 Tool Result。
		// REST 路径保持同一形状，避免 Session 因传输方式不同而出现两种 FunctionResponse。
		text, _ := json.MarshalIndent(execution.Result, "", "  ")
		return json.Marshal(map[string]any{
			"content":           []any{map[string]any{"type": "text", "text": string(text)}},
			"structuredContent": execution.Result,
			"isError":           false,
		})
	}
	errorView := execution.Error
	if errorView == nil {
		errorView = &nexusDockExecutionError{Code: "EXECUTION_FAILED", Message: "NexusDock execution did not succeed"}
	}
	structured := map[string]any{
		"execution_id": execution.ID,
		"status":       execution.Status,
		"error":        errorView,
	}
	if execution.Status == "unknown" {
		structured["retry_safe"] = false
		structured["retry_guidance"] = "The tool may already have executed. Do not automatically retry this operation."
	}
	text, _ := json.Marshal(structured)
	return json.Marshal(map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(text)}},
		"structuredContent": structured,
		"isError":           true,
	})
}

func decodeNexusDockAPIError(raw []byte) (nexusDockAPIError, bool) {
	var response nexusDockAPIError
	if json.Unmarshal(raw, &response) != nil || strings.TrimSpace(response.Error.Code) == "" {
		return nexusDockAPIError{}, false
	}
	return response, true
}

func nexusDockHTTPError(operation string, status int, response nexusDockAPIError, decoded bool) error {
	if decoded {
		message := strings.TrimSpace(response.Error.Message)
		if message == "" {
			message = response.Error.Code
		}
		return &nexusDockAPIStatusError{Status: status, Code: response.Error.Code, Message: message}
	}
	return fmt.Errorf("NexusDock Execution %s failed: HTTP %d", operation, status)
}

// nexusDockSafeToolError 只包含 Cloud 明确在 Execution 创建/发送前返回的业务拒绝。
// 这些错误沿用 MCP 的 isError Tool Result 语义，让模型可以换设备、修参数或稍后重试；
// 鉴权错误、幂等冲突和不确定的 5xx 仍作为运行时错误结束 Attempt。
func nexusDockSafeToolError(err *nexusDockAPIStatusError) bool {
	switch err.Code {
	case "CAPABILITY_NOT_FOUND",
		"EXECUTION_ARGUMENTS_INVALID",
		"EXECUTION_LIMIT_REACHED",
		"EXECUTION_RATE_LIMITED",
		"NODE_OFFLINE":
		return true
	default:
		return false
	}
}

func nexusDockPreDispatchErrorModelResult(err *nexusDockAPIStatusError) ([]byte, error) {
	structured := map[string]any{
		"status": "failed",
		"error": map[string]any{
			"code": err.Code, "message": err.Message,
		},
		"retry_safe": true,
	}
	text, _ := json.Marshal(structured)
	return json.Marshal(map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(text)}},
		"structuredContent": structured,
		"isError":           true,
	})
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
