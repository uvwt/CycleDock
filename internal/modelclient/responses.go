package modelclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type responsesTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type responsesItem struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   any    `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type responseOutputItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type responsesEvent struct {
	Type     string             `json:"type"`
	Delta    string             `json:"delta"`
	Item     responseOutputItem `json:"item"`
	Response struct {
		Output []responseOutputItem `json:"output"`
	} `json:"response"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (c *Client) completeResponses(
	ctx context.Context,
	config Config,
	messages []Message,
	tools []ToolDefinition,
	timeout time.Duration,
) (Completion, error) {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	instructions, input := buildResponsesInput(messages)
	responseTools := make([]responsesTool, 0, len(tools))
	for _, tool := range tools {
		responseTools = append(responseTools, responsesTool{
			Type: "function", Name: tool.Function.Name,
			Description: tool.Function.Description, Parameters: tool.Function.Parameters,
		})
	}
	body := struct {
		Model        string          `json:"model"`
		Instructions string          `json:"instructions,omitempty"`
		Input        []responsesItem `json:"input"`
		Tools        []responsesTool `json:"tools,omitempty"`
		Store        bool            `json:"store"`
		Stream       bool            `json:"stream"`
	}{
		Model: strings.TrimSpace(config.Model), Instructions: instructions,
		Input: input, Tools: responseTools, Store: false, Stream: true,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Completion{}, fmt.Errorf("encode Responses request: %w", err)
	}

	endpoint := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/") + "/responses"
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return Completion{}, fmt.Errorf("create Responses request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	for name, value := range config.Headers {
		if strings.TrimSpace(name) != "" && strings.TrimSpace(value) != "" {
			request.Header.Set(name, value)
		}
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return Completion{}, fmt.Errorf("Responses request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBody, readErr := readBounded(response.Body, response.ContentLength, maxResponseBytes)
		if readErr != nil {
			return Completion{}, readErr
		}
		return Completion{}, &APIError{
			StatusCode: response.StatusCode,
			Body:       summarizeBody(responseBody),
		}
	}

	completion, err := readResponsesStream(response)
	if err != nil {
		return Completion{}, err
	}
	if strings.TrimSpace(completion.Content) == "" && len(completion.ToolCalls) == 0 {
		return Completion{}, ErrEmptyResponse
	}
	return completion, nil
}

func buildResponsesInput(messages []Message) (string, []responsesItem) {
	var instructions []string
	input := make([]responsesItem, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "system":
			if content := strings.TrimSpace(message.Content); content != "" {
				instructions = append(instructions, content)
			}
		case "tool":
			input = append(input, responsesItem{
				Type: "function_call_output", CallID: message.ToolCallID, Output: message.Content,
			})
		default:
			if content := strings.TrimSpace(message.Content); content != "" {
				input = append(input, responsesItem{
					Type: "message", Role: message.Role, Content: content,
				})
			}
			for _, call := range message.ToolCalls {
				input = append(input, responsesItem{
					Type: "function_call", CallID: call.ID,
					Name: call.Function.Name, Arguments: call.Function.Arguments,
				})
			}
		}
	}
	return strings.Join(instructions, "\n\n"), input
}

func readResponsesStream(response *http.Response) (Completion, error) {
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), int(maxResponseBytes))
	var total int64
	var text strings.Builder
	toolCalls := make([]ToolCall, 0)
	seenCalls := make(map[string]struct{})
	var completedOutput []responseOutputItem

	for scanner.Scan() {
		line := scanner.Text()
		total += int64(len(line)) + 1
		if total > maxResponseBytes {
			return Completion{}, ErrResponseTooLarge
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event responsesEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return Completion{}, fmt.Errorf("%w: invalid Responses SSE event", ErrMalformedResponse)
		}
		switch event.Type {
		case "response.output_text.delta":
			text.WriteString(event.Delta)
		case "response.output_item.done":
			appendResponseToolCall(&toolCalls, seenCalls, event.Item)
		case "response.completed":
			completedOutput = event.Response.Output
		case "response.failed":
			code := strings.TrimSpace(event.Error.Code)
			if code == "" {
				code = "response_failed"
			}
			return Completion{}, fmt.Errorf("Responses stream failed: %s", code)
		}
	}
	if err := scanner.Err(); err != nil {
		return Completion{}, fmt.Errorf("read Responses stream: %w", err)
	}

	for _, item := range completedOutput {
		appendResponseToolCall(&toolCalls, seenCalls, item)
	}
	if text.Len() == 0 {
		for _, item := range completedOutput {
			if item.Type != "message" {
				continue
			}
			for _, content := range item.Content {
				if content.Type == "output_text" {
					text.WriteString(content.Text)
				}
			}
		}
	}
	return Completion{Content: strings.TrimSpace(text.String()), ToolCalls: toolCalls}, nil
}

func appendResponseToolCall(calls *[]ToolCall, seen map[string]struct{}, item responseOutputItem) {
	if item.Type != "function_call" || strings.TrimSpace(item.Name) == "" {
		return
	}
	callID := strings.TrimSpace(item.CallID)
	if callID == "" {
		callID = strings.TrimSpace(item.ID)
	}
	if callID == "" {
		return
	}
	if _, exists := seen[callID]; exists {
		return
	}
	seen[callID] = struct{}{}
	*calls = append(*calls, ToolCall{
		ID: callID, Type: "function",
		Function: ToolFunction{Name: item.Name, Arguments: item.Arguments},
	})
}
