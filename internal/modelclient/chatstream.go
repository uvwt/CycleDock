package modelclient

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type chatStreamDelta struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

// Chat Completions tool call arguments arrive in fragments, indexed by call.
// We assemble them only inside the current Run; raw fragments are never durable.
func readChatStream(reader io.Reader, onDelta func(string)) (Completion, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), int(maxResponseBytes))
	var bytesRead int64
	var answer strings.Builder
	completed := false
	calls := make(map[int]*ToolCall)
	for scanner.Scan() {
		line := scanner.Text()
		bytesRead += int64(len(line) + 1)
		if bytesRead > maxResponseBytes {
			return Completion{}, ErrResponseTooLarge
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			completed = true
			break
		}
		var packet chatStreamDelta
		if err := json.Unmarshal([]byte(data), &packet); err != nil {
			return Completion{}, fmt.Errorf("%w: chat stream data: %v", ErrMalformedResponse, err)
		}
		for _, choice := range packet.Choices {
			if choice.Delta.Content != "" {
				answer.WriteString(choice.Delta.Content)
				if onDelta != nil {
					onDelta(choice.Delta.Content)
				}
			}
			for _, part := range choice.Delta.ToolCalls {
				call := calls[part.Index]
				if call == nil {
					call = &ToolCall{Type: "function"}
					calls[part.Index] = call
				}
				if part.ID != "" {
					call.ID = part.ID
				}
				if part.Type != "" {
					call.Type = part.Type
				}
				call.Function.Name += part.Function.Name
				call.Function.Arguments += part.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Completion{}, fmt.Errorf("read chat stream: %w", err)
	}
	// The transport can end early without a network error. Never persist a partial answer as a completed Result.
	if !completed {
		return Completion{}, fmt.Errorf("%w: chat stream ended before [DONE]", ErrMalformedResponse)
	}
	indices := make([]int, 0, len(calls))
	for i := range calls {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	result := Completion{Content: strings.TrimSpace(answer.String())}
	for _, i := range indices {
		call := calls[i]
		if call.ID == "" || call.Function.Name == "" {
			return Completion{}, ErrMalformedResponse
		}
		result.ToolCalls = append(result.ToolCalls, *call)
	}
	if result.Content == "" && len(result.ToolCalls) == 0 {
		return Completion{}, ErrEmptyResponse
	}
	return result, nil
}
