package agent

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"

	"github.com/uvwt/CycleDock/internal/modelclient"
)

const (
	maxRepeatedToolRounds = 8
)

var (
	errRunawayToolLoop = errors.New("tool calls and results repeated without progress")
)

// repeatedToolLoop 只比较尾部完整的工具轮次，不把调用 ID、过程解说或历史轮数当成进展。
// 返回结果变化的状态轮询仍可继续；不完整的调用/响应对交给 ADK 处理，不据此中止。
func repeatedToolLoop(messages []modelclient.Message) bool {
	var previous [sha256.Size]byte
	end := len(messages)
	for round := 0; round < maxRepeatedToolRounds; round++ {
		// 若工具批次后面紧跟着系统为携带工具图片而注入的合成 user 多模态消息，
		// 跳过该合成消息再回溯对齐上一轮的 tool 响应批次，避免误伤死循环熔断检测。
		if end > 0 && isSyntheticToolImageUserMessage(messages[end-1]) {
			end--
		}
		start := end
		for start > 0 && messages[start-1].Role == "tool" {
			start--
		}
		if start == 0 || start == end {
			return false
		}
		request := messages[start-1]
		if request.Role != "assistant" || len(request.ToolCalls) != end-start {
			return false
		}
		exchange := make([]struct {
			Name      string
			Arguments string
			Result    string
		}, len(request.ToolCalls))
		for i, call := range request.ToolCalls {
			response := messages[start+i]
			if response.ToolCallID != call.ID || response.Name != call.Function.Name {
				return false
			}
			exchange[i].Name = call.Function.Name
			exchange[i].Arguments = call.Function.Arguments
			exchange[i].Result = response.Content
		}
		// 参数和响应已由 ADK 适配器经 json.Marshal 规范化；摘要只用于内存比较，不记录工具内容。
		raw, _ := json.Marshal(exchange)
		current := sha256.Sum256(raw)
		if round > 0 && current != previous {
			return false
		}
		previous = current
		end = start - 1
	}
	return true
}

func isSyntheticToolImageUserMessage(msg modelclient.Message) bool {
	if msg.Role != "user" || !msg.HasImageParts() {
		return false
	}
	for _, part := range msg.Parts {
		if part.Type == modelclient.ContentPartText && strings.HasPrefix(part.Text, "Image #") && strings.Contains(part.Text, "returned by tool") {
			return true
		}
	}
	return false
}
