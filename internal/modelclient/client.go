package modelclient

import (
	"bytes"
	"context"
	"encoding/base64"
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
	BaseURL     string
	Model       string
	APIKey      string
	Timeout     time.Duration
	Temperature float64
	// ReasoningEffort 可选的推理强度提示（例如 "low"），仅对支持该字段的 Responses API 序列化为 reasoning.effort。
	// 留空时不发送该字段；Chat Completions 默认不透传，避免不兼容该扩展字段的通用 openai_compatible 上游报错。
	ReasoningEffort string
	Transport       Transport
	Headers         map[string]string
	OutputSchema    *OutputSchema
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
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens"`
	PromptTokensDetails  struct {
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

type ContentPartType string

const (
	ContentPartText  ContentPartType = "text"
	ContentPartImage ContentPartType = "image"
)

// ContentPart 描述多模态消息中的单个有序内容片段（文本或图像）。
// 图像可提供现成的 DataURL，也可提供 MIMEType + Base64Data 或原始 Data 字节，
// 由传输层按 /chat/completions 或 /responses 协议序列化为标准 data URL。
type ContentPart struct {
	Type       ContentPartType
	Text       string
	MIMEType   string
	Base64Data string
	Data       []byte
	DataURL    string
}

func NewTextPart(text string) ContentPart {
	return ContentPart{
		Type: ContentPartText,
		Text: text,
	}
}

func NewImagePartFromBytes(mimeType string, data []byte) ContentPart {
	return ContentPart{
		Type:     ContentPartImage,
		MIMEType: normalizeImageMIMEType(mimeType),
		Data:     bytes.Clone(data),
	}
}

func NewImagePartFromBase64(mimeType, base64Data string) ContentPart {
	return ContentPart{
		Type:       ContentPartImage,
		MIMEType:   normalizeImageMIMEType(mimeType),
		Base64Data: cleanBase64String(base64Data),
	}
}

func NewImagePartFromDataURL(dataURL string) ContentPart {
	mimeType, b64, ok := parseDataURL(dataURL)
	if ok {
		return ContentPart{
			Type:       ContentPartImage,
			MIMEType:   mimeType,
			Base64Data: b64,
			DataURL:    strings.TrimSpace(dataURL),
		}
	}
	return ContentPart{
		Type:    ContentPartImage,
		DataURL: strings.TrimSpace(dataURL),
	}
}

// ImageDataURL 生成用于 OpenAI Chat Completions / Responses 传输的 data:<mime>;base64,<data> 字符串。
func (p ContentPart) ImageDataURL() (string, error) {
	if trimmed := strings.TrimSpace(p.DataURL); trimmed != "" {
		return trimmed, nil
	}
	mimeType := normalizeImageMIMEType(p.MIMEType)
	if len(p.Data) > 0 {
		return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(p.Data), nil
	}
	if b64 := cleanBase64String(p.Base64Data); b64 != "" {
		return "data:" + mimeType + ";base64," + b64, nil
	}
	return "", errors.New("image content part has no image payload")
}

func normalizeImageMIMEType(mimeType string) string {
	trimmed := strings.TrimSpace(mimeType)
	if semi := strings.IndexByte(trimmed, ';'); semi >= 0 {
		trimmed = strings.TrimSpace(trimmed[:semi])
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "image/") && len(lower) > len("image/") {
		return lower
	}
	return "image/png"
}

func cleanBase64String(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if !strings.ContainsAny(trimmed, " \t\r\n") {
		return trimmed
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for i := 0; i < len(trimmed); i++ {
		switch trimmed[i] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			b.WriteByte(trimmed[i])
		}
	}
	return b.String()
}

func parseDataURL(raw string) (mimeType, base64Data string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		return "", "", false
	}
	headerAndPayload := trimmed[len("data:"):]
	header, payload, found := strings.Cut(headerAndPayload, ",")
	if !found {
		return "", "", false
	}
	lowerHeader := strings.ToLower(strings.TrimSpace(header))
	if !strings.HasSuffix(lowerHeader, ";base64") {
		return "", "", false
	}
	mediaType := strings.TrimSpace(header[:len(header)-len(";base64")])
	return normalizeImageMIMEType(mediaType), cleanBase64String(payload), true
}

type Message struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	Parts      []ContentPart `json:"-"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
}

// HasImageParts 报告消息是否包含多模态图像块。
// 无图像块时，所有序列化路径均退化为原有纯字符串 content 以确保向后兼容。
func (m Message) HasImageParts() bool {
	for _, part := range m.Parts {
		if part.Type == ContentPartImage {
			return true
		}
	}
	return false
}

// PlainText 返回消息合并后的纯文本内容：优先拼接 Parts 中的文本片段，未设置 Parts 时返回 Content。
func (m Message) PlainText() string {
	if len(m.Parts) == 0 {
		return m.Content
	}
	var b strings.Builder
	for _, part := range m.Parts {
		if part.Type == ContentPartText && part.Text != "" {
			b.WriteString(part.Text)
		}
	}
	if b.Len() == 0 {
		return m.Content
	}
	return b.String()
}

func (m Message) normalizedParts() []ContentPart {
	if len(m.Parts) > 0 {
		return m.Parts
	}
	if m.Content != "" {
		return []ContentPart{{Type: ContentPartText, Text: m.Content}}
	}
	return nil
}

type chatWireContentPart struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	ImageURL *chatWireImageURL `json:"image_url,omitempty"`
}

type chatWireImageURL struct {
	URL string `json:"url"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	// 无图像片段时严格保持原有纯字符串序列化，完全兼容不支持数组 content 的纯文本端点。
	if !m.HasImageParts() {
		wire := struct {
			Role       string     `json:"role"`
			Content    string     `json:"content,omitempty"`
			ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
			ToolCallID string     `json:"tool_call_id,omitempty"`
			Name       string     `json:"name,omitempty"`
		}{
			Role:       m.Role,
			Content:    m.PlainText(),
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		return json.Marshal(wire)
	}

	parts := m.normalizedParts()
	wireParts := make([]chatWireContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case ContentPartText:
			if part.Text == "" {
				continue
			}
			wireParts = append(wireParts, chatWireContentPart{
				Type: "text",
				Text: part.Text,
			})
		case ContentPartImage:
			dataURL, err := part.ImageDataURL()
			if err != nil {
				return nil, err
			}
			wireParts = append(wireParts, chatWireContentPart{
				Type:     "image_url",
				ImageURL: &chatWireImageURL{URL: dataURL},
			})
		default:
			return nil, fmt.Errorf("unsupported message content part type %q", part.Type)
		}
	}
	wire := struct {
		Role       string                `json:"role"`
		Content    []chatWireContentPart `json:"content,omitempty"`
		ToolCalls  []ToolCall            `json:"tool_calls,omitempty"`
		ToolCallID string                `json:"tool_call_id,omitempty"`
		Name       string                `json:"name,omitempty"`
	}{
		Role:       m.Role,
		Content:    wireParts,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
		Name:       m.Name,
	}
	return json.Marshal(wire)
}

func (m *Message) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCalls  []ToolCall      `json:"tool_calls"`
		ToolCallID string          `json:"tool_call_id"`
		Name       string          `json:"name"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := Message{
		Role:       raw.Role,
		ToolCalls:  raw.ToolCalls,
		ToolCallID: raw.ToolCallID,
		Name:       raw.Name,
	}
	trimmed := bytes.TrimSpace(raw.Content)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*m = out
		return nil
	}
	if trimmed[0] == '"' {
		if err := json.Unmarshal(trimmed, &out.Content); err != nil {
			return err
		}
		*m = out
		return nil
	}
	if trimmed[0] == '[' {
		var wireParts []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			ImageURL json.RawMessage `json:"image_url"`
		}
		if err := json.Unmarshal(trimmed, &wireParts); err != nil {
			return err
		}
		var textBuilder strings.Builder
		for _, wp := range wireParts {
			switch wp.Type {
			case "text", "input_text", "output_text":
				out.Parts = append(out.Parts, NewTextPart(wp.Text))
				textBuilder.WriteString(wp.Text)
			case "image_url", "input_image":
				urlStr, err := decodeWireImageURL(wp.ImageURL)
				if err != nil {
					return err
				}
				out.Parts = append(out.Parts, NewImagePartFromDataURL(urlStr))
			default:
				return fmt.Errorf("unsupported wire content part type %q", wp.Type)
			}
		}
		out.Content = textBuilder.String()
		*m = out
		return nil
	}
	return fmt.Errorf("unsupported message content JSON: %s", string(trimmed))
}

func decodeWireImageURL(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", errors.New("empty image_url")
	}
	if trimmed[0] == '"' {
		var urlStr string
		if err := json.Unmarshal(trimmed, &urlStr); err != nil {
			return "", err
		}
		return urlStr, nil
	}
	var obj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return "", err
	}
	return obj.URL, nil
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
	Content       string
	ToolCalls     []ToolCall
	Usage         *TokenUsage
	ResponseModel string
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
	noReasoning   map[string]bool
	catalogMu     sync.Mutex
	catalogCache  map[string]catalogCacheEntry
}

func New() *Client {
	return &Client{
		httpClient:    &http.Client{},
		noStreamUsage: make(map[string]bool),
		noReasoning:   make(map[string]bool),
	}
}

func NewWithHTTPClient(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{}
	}
	return &Client{
		httpClient:    client,
		noStreamUsage: make(map[string]bool),
		noReasoning:   make(map[string]bool),
	}
}

func reasoningCacheKey(baseURL, model string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

func (c *Client) reasoningUnsupported(baseURL, model string) bool {
	if c == nil {
		return false
	}
	key := reasoningCacheKey(baseURL, model)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.noReasoning[key]
}

func (c *Client) markReasoningUnsupported(baseURL, model string) {
	if c == nil {
		return
	}
	key := reasoningCacheKey(baseURL, model)
	c.mu.Lock()
	if c.noReasoning == nil {
		c.noReasoning = make(map[string]bool)
	}
	c.noReasoning[key] = true
	c.mu.Unlock()
}

func isUnsupportedReasoningError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusUnprocessableEntity {
		return false
	}
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "reasoning")
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
			Model   string `json:"model"`
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
			Content:       strings.TrimSpace(decoded.Choices[0].Message.Content),
			ToolCalls:     decoded.Choices[0].Message.ToolCalls,
			Usage:         decoded.Usage.toTokenUsage(),
			ResponseModel: strings.TrimSpace(decoded.Model),
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
