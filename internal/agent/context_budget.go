package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/uvwt/CycleDock/internal/modelclient"
)

const (
	defaultRunInputBudgetTokens      = 96 * 1024
	conservativeRunInputBudgetTokens = 24 * 1024
	runSummaryMaxTokens              = 8 * 1024
	latestToolResultMaxTokens        = 1024
	maxRunToolRounds                 = 64
)

var errRunContextBudgetExceeded = errors.New("run context budget exceeded")

const runCompactionHeader = "Earlier tool execution summary (deterministic; raw outputs are intentionally compacted):"

// fitRunContext 只压缩当前 Run 内已经完成的旧工具轮次。
// BuildContext 生成的 system + Card context 始终保持原样，既不破坏 durable context，
// 也让同一 Run 的模型请求尽可能保持稳定前缀，便于上游 prompt cache 复用。
func fitRunContext(
	messages []modelclient.Message,
	tools []modelclient.ToolDefinition,
	inputBudget int,
) ([]modelclient.Message, error) {
	if inputBudget <= 0 {
		return messages, nil
	}
	if estimateRequestTokens(messages, tools) <= inputBudget {
		return messages, nil
	}
	if len(messages) <= 2 {
		return nil, fmt.Errorf("%w: fixed Card context and tool schemas exceed the input budget", errRunContextBudgetExceeded)
	}

	lastRoundStart := latestToolRoundStart(messages)
	if lastRoundStart < 2 {
		return nil, fmt.Errorf("%w: fixed Card context and tool schemas exceed the input budget", errRunContextBudgetExceeded)
	}

	fixed := cloneMessages(messages[:2])
	latest := cloneMessages(messages[lastRoundStart:])
	latest = compactLatestToolResults(latest)
	fixedAndLatest := append(cloneMessages(fixed), latest...)
	remaining := inputBudget - estimateRequestTokens(fixedAndLatest, tools)
	if remaining <= 0 {
		return nil, fmt.Errorf("%w: fixed Card context, tool schemas, and latest tool round exceed the input budget", errRunContextBudgetExceeded)
	}
	if remaining > runSummaryMaxTokens {
		remaining = runSummaryMaxTokens
	}

	summary := summarizeRunExecution(messages[2:lastRoundStart], remaining)
	out := cloneMessages(fixed)
	if summary != "" {
		out = append(out, modelclient.Message{Role: "assistant", Content: summary})
	}
	out = append(out, latest...)
	if estimateRequestTokens(out, tools) > inputBudget {
		return nil, fmt.Errorf("%w: compacted run still exceeds the input budget", errRunContextBudgetExceeded)
	}
	return out, nil
}

func latestToolRoundStart(messages []modelclient.Message) int {
	for i := len(messages) - 1; i >= 2; i-- {
		if messages[i].Role == "assistant" && len(messages[i].ToolCalls) > 0 {
			return i
		}
	}
	return -1
}

func compactLatestToolResults(messages []modelclient.Message) []modelclient.Message {
	out := cloneMessages(messages)
	for i := range out {
		if out[i].Role != "tool" || estimateTokens(out[i].Content) <= latestToolResultMaxTokens {
			continue
		}
		preview := truncateToTokens(out[i].Content, latestToolResultMaxTokens-80)
		payload, _ := json.Marshal(map[string]any{
			"compacted": true,
			"message":   "Tool result was compacted to keep the active Run within its model context budget.",
			"preview":   preview,
		})
		out[i].Content = string(payload)
	}
	return out
}

func summarizeRunExecution(messages []modelclient.Message, maxTokens int) string {
	if len(messages) == 0 || maxTokens <= estimateTokens(runCompactionHeader) {
		return ""
	}
	lines := make([]string, 0, len(messages)*2)
	for _, message := range messages {
		if message.Role == "assistant" && strings.HasPrefix(message.Content, runCompactionHeader) {
			for _, line := range strings.Split(strings.TrimSpace(strings.TrimPrefix(message.Content, runCompactionHeader)), "\n") {
				if strings.TrimSpace(line) != "" {
					lines = append(lines, line)
				}
			}
			continue
		}
		if message.Role == "assistant" {
			if note := strings.TrimSpace(message.Content); note != "" {
				lines = append(lines, "- assistant: "+compactContextValue(note, 320))
			}
			for _, call := range message.ToolCalls {
				line := fmt.Sprintf(
					"- tool %s args=%s",
					call.Function.Name,
					compactContextValue(call.Function.Arguments, 360),
				)
				lines = append(lines, line)
			}
			continue
		}
		if message.Role == "tool" {
			lines = append(lines, fmt.Sprintf(
				"  result %s: %s",
				message.Name,
				compactContextValue(message.Content, 520),
			))
		}
	}
	if len(lines) == 0 {
		return ""
	}

	headerTokens := estimateTokens(runCompactionHeader) + 2
	used := headerTokens
	kept := make([]string, 0, len(lines))
	for i := len(lines) - 1; i >= 0; i-- {
		lineTokens := estimateTokens(lines[i]) + 1
		if used+lineTokens > maxTokens {
			continue
		}
		kept = append(kept, lines[i])
		used += lineTokens
	}
	if len(kept) == 0 {
		return ""
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return runCompactionHeader + "\n" + strings.Join(kept, "\n")
}

func compactContextValue(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return "(empty)"
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "…"
}

func estimateRequestTokens(messages []modelclient.Message, tools []modelclient.ToolDefinition) int {
	total := 0
	for _, message := range messages {
		raw, err := json.Marshal(message)
		if err == nil {
			total += estimateTokens(string(raw)) + 4
		}
	}
	if raw, err := json.Marshal(tools); err == nil {
		total += estimateTokens(string(raw))
	}
	return total
}

func estimateTokens(value string) int {
	han, other := 0, 0
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			han++
		} else if !unicode.IsSpace(r) {
			other++
		}
	}
	return han + (other+3)/4
}

func truncateToTokens(value string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	var builder strings.Builder
	usedOther := 0
	tokens := 0
	for _, r := range value {
		nextTokens := tokens
		if unicode.Is(unicode.Han, r) {
			nextTokens++
		} else if !unicode.IsSpace(r) {
			usedOther++
			nextTokens = tokens + (usedOther+3)/4 - (usedOther+2)/4
		}
		if nextTokens > maxTokens {
			break
		}
		builder.WriteRune(r)
		tokens = nextTokens
	}
	if builder.Len() == len(value) {
		return value
	}
	return strings.TrimSpace(builder.String()) + "…"
}

func cloneMessages(messages []modelclient.Message) []modelclient.Message {
	out := append([]modelclient.Message(nil), messages...)
	for i := range out {
		out[i].ToolCalls = append([]modelclient.ToolCall(nil), messages[i].ToolCalls...)
	}
	return out
}
