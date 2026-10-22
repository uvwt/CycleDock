package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/uvwt/CycleDock/internal/modelclient"
)

func TestFitRunContextKeepsStablePrefixAndLatestToolRound(t *testing.T) {
	messages := []modelclient.Message{
		{Role: "system", Content: "STABLE_SYSTEM"},
		{Role: "user", Content: "STABLE_CARD_CONTEXT"},
	}
	for round := 0; round < 4; round++ {
		callID := "call-" + string(rune('a'+round))
		messages = append(messages,
			modelclient.Message{Role: "assistant", ToolCalls: []modelclient.ToolCall{{
				ID: callID, Type: "function",
				Function: modelclient.ToolFunction{Name: "lookup", Arguments: `{"round":` + string(rune('0'+round)) + `}`},
			}}},
			modelclient.Message{Role: "tool", ToolCallID: callID, Name: "lookup", Content: strings.Repeat("historical tool result ", 900)},
		)
	}

	fitted, err := fitRunContext(messages, nil, 2500)
	if err != nil {
		t.Fatal(err)
	}
	if fitted[0].Content != "STABLE_SYSTEM" || fitted[1].Content != "STABLE_CARD_CONTEXT" {
		t.Fatalf("stable prefix changed: %#v", fitted[:2])
	}
	if len(fitted) < 5 || !strings.HasPrefix(fitted[2].Content, runCompactionHeader) {
		t.Fatalf("older rounds were not compacted: %#v", fitted)
	}
	lastAssistant := fitted[len(fitted)-2]
	lastTool := fitted[len(fitted)-1]
	if len(lastAssistant.ToolCalls) != 1 || lastAssistant.ToolCalls[0].ID != "call-d" {
		t.Fatalf("latest assistant tool call was not preserved: %#v", lastAssistant)
	}
	if lastTool.ToolCallID != "call-d" || lastTool.Role != "tool" {
		t.Fatalf("latest tool pairing was not preserved: %#v", lastTool)
	}
	if estimateRequestTokens(fitted, nil) > 2500 {
		t.Fatalf("fitted context exceeds budget: %d", estimateRequestTokens(fitted, nil))
	}
}

func TestFitRunContextRecompactsPreviousSummary(t *testing.T) {
	messages := []modelclient.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "card"},
		{Role: "assistant", Content: runCompactionHeader + "\n- tool old args={}\n  result old: ok"},
		{Role: "assistant", ToolCalls: []modelclient.ToolCall{{
			ID: "call-1", Type: "function",
			Function: modelclient.ToolFunction{Name: "first", Arguments: "{}"},
		}}},
		{Role: "tool", ToolCallID: "call-1", Name: "first", Content: strings.Repeat("x", 6000)},
		{Role: "assistant", ToolCalls: []modelclient.ToolCall{{
			ID: "call-2", Type: "function",
			Function: modelclient.ToolFunction{Name: "second", Arguments: "{}"},
		}}},
		{Role: "tool", ToolCallID: "call-2", Name: "second", Content: strings.Repeat("y", 6000)},
	}
	fitted, err := fitRunContext(messages, nil, 1800)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(fitted[2].Content, runCompactionHeader) != 1 {
		t.Fatalf("summary header duplicated: %q", fitted[2].Content)
	}
	if !strings.Contains(fitted[2].Content, "tool old") || !strings.Contains(fitted[2].Content, "tool first") {
		t.Fatalf("previous summary or newly compacted round was lost: %q", fitted[2].Content)
	}
}

func TestFitRunContextRejectsOversizedFixedContext(t *testing.T) {
	_, err := fitRunContext([]modelclient.Message{
		{Role: "system", Content: strings.Repeat("system ", 400)},
		{Role: "user", Content: strings.Repeat("card context ", 1200)},
	}, nil, 500)
	if !errors.Is(err, errRunContextBudgetExceeded) {
		t.Fatalf("expected context budget error, got %v", err)
	}
}
