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
	BaseURL     string
	Model       string
	APIKey      string
	Timeout     time.Duration
	Temperature float64
	Transport   Transport
	Headers     map[string]string
	// OnTextDelta forwards text to the ephemeral browser stream; never store raw tool traces.
	OnTextDelta func(string)
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
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
	httpClient *http.Client
}

func New() *Client {
	return &Client{httpClient: &http.Client{}}
}

func NewWithHTTPClient(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{}
	}
	return &Client{httpClient: client}
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

	body := struct {
		Model       string           `json:"model"`
		Messages    []Message        `json:"messages"`
		Tools       []ToolDefinition `json:"tools,omitempty"`
		Temperature float64          `json:"temperature,omitempty"`
		Stream      bool             `json:"stream"`
	}{
		Model: strings.TrimSpace(config.Model), Messages: messages,
		Tools: tools, Temperature: config.Temperature, Stream: config.OnTextDelta != nil,
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
	if config.OnTextDelta != nil {
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
	defer response.Body.Close()

	if config.OnTextDelta != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		return readChatStream(response.Body, config.OnTextDelta)
	}
	responseBody, err := readBounded(response.Body, response.ContentLength, maxResponseBytes)
	if err != nil {
		return Completion{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
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
	}, nil
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
