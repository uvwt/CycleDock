package modelclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxResponseBytes int64 = 4 << 20

var (
	ErrEmptyResponse     = errors.New("model returned no choices")
	ErrMalformedResponse = errors.New("malformed model response")
	ErrResponseTooLarge  = errors.New("model response too large")
)

type Transport string

const (
	TransportChatCompletions Transport = "chat_completions"
	TransportResponses       Transport = "responses"
)

type Config struct {
	BaseURL      string
	Model        string
	APIKey       string
	Timeout      time.Duration
	Temperature  float64
	Transport    Transport
	Headers      map[string]string
	OutputSchema *OutputSchema
	// OnTextDelta forwards text to the ephemeral browser stream; never store raw tool traces.
	OnTextDelta func(string)
	// OnToolCall 接收当前调用的累计参数快照；回调只用于临时预览，不提前执行工具。
	OnToolCall func(ToolCall)
}

// OutputSchema 是模型输出的 JSON Schema 契约，与工具参数 Schema 独立。
type OutputSchema struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
	Strict bool           `json:"strict"`
}

type chatResponseFormat struct {
	Type       string        `json:"type"`
	JSONSchema *OutputSchema `json:"json_schema"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// TokenUsage 记录单次模型调用的 Token 统计。
// 当上游服务商或中转未返回有效统计时，Completion.Usage 保持为 nil，由调用方区分实际值与估算值。
type TokenUsage struct {
	InputTokens           int
	OutputTokens          int
	CachedInputTokens     int
	ReasoningOutputTokens int
}

type wireTokenUsage struct {
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	InputTokens             int `json:"input_tokens"`
	OutputTokens            int `json:"output_tokens"`
	CacheReadInputTokens    int `json:"cache_read_input_tokens"`
	PromptTokensDetails     struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func (u *wireTokenUsage) toTokenUsage() *TokenUsage {
	if u == nil {
		return nil
	}
	input := max(u.PromptTokens, u.InputTokens, 0)
	output := max(u.CompletionTokens, u.OutputTokens, 0)
	cached := max(u.PromptTokensDetails.CachedTokens, u.InputTokensDetails.CachedTokens, u.CacheReadInputTokens, 0)
	reasoning := max(u.CompletionTokensDetails.ReasoningTokens, u.OutputTokensDetails.ReasoningTokens, 0)
	if input == 0 && output == 0 && cached == 0 && reasoning == 0 {
		return nil
	}
	return &TokenUsage{
		InputTokens:           input,
		OutputTokens:          output,
		CachedInputTokens:     cached,
		ReasoningOutputTokens: reasoning,
	}
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	Index    int          `json:"-"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function ToolDefinitionFunc `json:"function"`
}

type ToolDefinitionFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type Completion struct {
	Content   string
	ToolCalls []ToolCall
	Usage     *TokenUsage
}

type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("model API returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("model API returned HTTP %d: %s", e.StatusCode, e.Body)
}

type Client struct {
	httpClient    *http.Client
	mu            sync.Mutex
	noStreamUsage map[string]bool
}

func New() *Client {
	return &Client{
		httpClient:    &http.Client{},
		noStreamUsage: make(map[string]bool),
	}
}

func NewWithHTTPClient(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{}
	}
	return &Client{
		httpClient:    client,
		noStreamUsage: make(map[string]bool),
	}
}

func (c *Client) streamUsageUnsupported(baseURL string) bool {
	if c == nil {
		return false
	}
	key := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.noStreamUsage[key]
}

func (c *Client) markStreamUsageUnsupported(baseURL string) {
	if c == nil {
		return
	}
	key := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	c.mu.Lock()
	if c.noStreamUsage == nil {
		c.noStreamUsage = make(map[string]bool)
	}
	c.noStreamUsage[key] = true
	c.mu.Unlock()
}

func isUnsupportedStreamOptionsError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusUnprocessableEntity {
		return false
	}
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "stream_options") || strings.Contains(lower, "include_usage")
}

func (c *Client) Complete(
	ctx context.Context,
	config Config,
	messages []Message,
	tools []ToolDefinition,
) (Completion, error) {
	if strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.Model) == "" {
		return Completion{}, errors.New("model base URL and model are required")
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if config.Transport == TransportResponses {
		return c.completeResponses(ctx, config, messages, tools, timeout)
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stream := config.OnTextDelta != nil || config.OnToolCall != nil
	includeStreamUsage := stream && !c.streamUsageUnsupported(config.BaseURL)

	for {
		body := struct {
			Model          string              `json:"model"`
			Messages       []Message           `json:"messages"`
			Tools          []ToolDefinition    `json:"tools,omitempty"`
			Temperature    float64             `json:"temperature,omitempty"`
			Stream         bool                `json:"stream"`
			StreamOptions  *chatStreamOptions  `json:"stream_options,omitempty"`
			ResponseFormat *chatResponseFormat `json:"response_format,omitempty"`
		}{
			Model: strings.TrimSpace(config.Model), Messages: messages,
			Tools: tools, Temperature: config.Temperature, Stream: stream,
		}
		if includeStreamUsage {
			body.StreamOptions = &chatStreamOptions{IncludeUsage: true}
		}
		if config.OutputSchema != nil {
			body.ResponseFormat = &chatResponseFormat{Type: "json_schema", JSONSchema: config.OutputSchema}
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return Completion{}, fmt.Errorf("encode model request: %w", err)
		}

		endpoint := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/") + "/chat/completions"
		request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(raw))
		if err != nil {
			return Completion{}, fmt.Errorf("create model request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		if stream {
			request.Header.Set("Accept", "text/event-stream")
		} else {
			request.Header.Set("Accept", "application/json")
		}
		if config.APIKey != "" {
			request.Header.Set("Authorization", "Bearer "+config.APIKey)
		}

		response, err := c.httpClient.Do(request)
		if err != nil {
			return Completion{}, fmt.Errorf("model request: %w", err)
		}

		if stream &&
			response.StatusCode >= 200 && response.StatusCode < 300 &&
			strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
			completion, streamErr := readChatStream(response.Body, config.OnTextDelta, config.OnToolCall)
			response.Body.Close()
			return completion, streamErr
		}
		responseBody, err := readBounded(response.Body, response.ContentLength, maxResponseBytes)
		response.Body.Close()
		if err != nil {
			return Completion{}, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			// 部分 OpenAI 兼容中转不支持 stream_options.include_usage，遇到明确字段报错时降级重发并记住该端点。
			if includeStreamUsage && isUnsupportedStreamOptionsError(response.StatusCode, responseBody) {
				c.markStreamUsageUnsupported(config.BaseURL)
				includeStreamUsage = false
				continue
			}
			return Completion{}, &APIError{
				StatusCode: response.StatusCode,
				Body:       summarizeBody(responseBody),
			}
		}

		var decoded struct {
			Choices []struct {
				Message struct {
					Content   string     `json:"content"`
					ToolCalls []ToolCall `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
			Usage *wireTokenUsage `json:"usage"`
		}
		if err := json.Unmarshal(responseBody, &decoded); err != nil {
			return Completion{}, fmt.Errorf("%w: %v", ErrMalformedResponse, err)
		}
		if len(decoded.Choices) == 0 {
			return Completion{}, ErrEmptyResponse
		}
		return Completion{
			Content:   strings.TrimSpace(decoded.Choices[0].Message.Content),
			ToolCalls: decoded.Choices[0].Message.ToolCalls,
			Usage:     decoded.Usage.toTokenUsage(),
		}, nil
	}
}

func readBounded(reader io.Reader, contentLength, limit int64) ([]byte, error) {
	if contentLength > limit {
		return nil, ErrResponseTooLarge
	}
	limited := io.LimitReader(reader, limit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read model response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

func summarizeBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 500 {
		text = string(runes[:500]) + "..."
	}
	return text
}
