package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/CycleDock/internal/agentsession"
)

func TestFormatFullSessionRoundsTruncationAndSanitization(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	t.Run("small rounds remain untruncated with image placeholder preserved", func(t *testing.T) {
		rounds := []agentsession.SessionRound{
			{
				EventID:      "evt-1",
				InvocationID: "inv-1",
				OccurredAt:   now,
				Author:       "cycle_agent",
				Role:         "model",
				Text:         "检查完成",
				Thinking:     "简要思路",
				ToolCalls: []map[string]any{
					{"id": "call-1", "name": "view_image", "args": map[string]any{"path": "shot.png"}},
				},
				ToolResults: []map[string]any{
					{
						"id":   "call-1",
						"name": "view_image",
						"response": map[string]any{
							"ok":            true,
							"inline_images": []any{map[string]any{"data": "RAW_BASE64_SHOULD_NOT_LEAK"}},
						},
					},
				},
			},
		}

		views, truncated := formatFullSessionRounds(rounds, defaultFullSessionLimits)
		if truncated {
			t.Fatalf("expected truncated=false, got true")
		}
		if len(views) != 1 {
			t.Fatalf("expected 1 round, got %d", len(views))
		}
		respMap, ok := views[0].ToolResults[0]["response"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected tool result response: %#v", views[0].ToolResults[0]["response"])
		}
		if respMap["inline_images"] != imagePayloadOmittedPlaceholder {
			t.Fatalf("inline_images=%v, want %q", respMap["inline_images"], imagePayloadOmittedPlaceholder)
		}
	})

	t.Run("oversized fields trigger per-field truncation and preserve utf8", func(t *testing.T) {
		longCN := strings.Repeat("中文日志片段", 200)
		rounds := []agentsession.SessionRound{
			{
				EventID:      "evt-2",
				InvocationID: "inv-2",
				OccurredAt:   now,
				Author:       "cycle_agent",
				Role:         "model",
				Text:         longCN,
				Thinking:     longCN,
				ToolCalls: []map[string]any{
					{"id": "call-2", "name": "exec_command", "args": map[string]any{"cmd": longCN}},
				},
				ToolResults: []map[string]any{
					{
						"id":   "call-2",
						"name": "exec_command",
						"response": map[string]any{
							"stdout":        longCN,
							"exit_code":     0,
							"inline_images": imagePayloadOmittedPlaceholder,
						},
					},
				},
			},
		}

		limits := fullSessionLimits{
			maxTextRunes:       120,
			maxThinkingRunes:   100,
			maxToolCallRunes:   200,
			maxToolResultRunes: 260,
			maxTotalRunes:      4000,
		}
		views, truncated := formatFullSessionRounds(rounds, limits)
		if !truncated {
			t.Fatal("expected truncated=true for oversized fields")
		}
		if len(views) != 1 {
			t.Fatalf("expected 1 round, got %d", len(views))
		}
		if !utf8.ValidString(views[0].Text) || !strings.HasSuffix(views[0].Text, truncatedSuffix) {
			t.Fatalf("text not properly truncated: %q", views[0].Text)
		}
		if !utf8.ValidString(views[0].Thinking) || !strings.HasSuffix(views[0].Thinking, truncatedSuffix) {
			t.Fatalf("thinking not properly truncated: %q", views[0].Thinking)
		}
		respJSON, err := json.Marshal(views[0].ToolResults[0]["response"])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(respJSON), imagePayloadOmittedPlaceholder) {
			t.Fatalf("truncated tool_results lost inline_images placeholder: %s", respJSON)
		}
		if !strings.Contains(string(respJSON), truncatedSuffix) {
			t.Fatalf("truncated tool_results missing truncation suffix: %s", respJSON)
		}
	})

	t.Run("total response budget keeps newest rounds in chronological order", func(t *testing.T) {
		rounds := make([]agentsession.SessionRound, 0, 5)
		for i := 1; i <= 5; i++ {
			rounds = append(rounds, agentsession.SessionRound{
				EventID:    "evt-" + string(rune('0'+i)),
				OccurredAt: now.Add(time.Duration(i) * time.Second),
				Author:     "cycle_agent",
				Role:       "model",
				Text:       strings.Repeat("x", 180),
			})
		}

		limits := fullSessionLimits{
			maxTextRunes:       300,
			maxThinkingRunes:   300,
			maxToolCallRunes:   300,
			maxToolResultRunes: 300,
			maxTotalRunes:      650,
		}
		views, truncated := formatFullSessionRounds(rounds, limits)
		if !truncated {
			t.Fatal("expected truncated=true when exceeding total budget")
		}
		if len(views) < 1 || len(views) >= len(rounds) {
			t.Fatalf("expected partial tail rounds, got %d of %d", len(views), len(rounds))
		}
		if views[len(views)-1].EventID != "evt-5" {
			t.Fatalf("expected newest round evt-5 to be preserved, got %s", views[len(views)-1].EventID)
		}
		if len(views) > 1 && views[0].EventID >= views[len(views)-1].EventID {
			t.Fatalf("rounds should remain in chronological order: %+v", views)
		}
	})
}

func TestSessionToolsDiscoveredAndInstructionsDocumentGuidance(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "cycledock", Version: "test"}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})
	registerTools(server, nil, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	initResult := session.InitializeResult()
	if initResult == nil {
		t.Fatal("missing InitializeResult")
	}
	for _, want := range []string{"get_card", "get_card_session(mode=simple)", "mode=full", "search_card_session"} {
		if !strings.Contains(initResult.Instructions, want) {
			t.Fatalf("server Instructions missing %q: %s", want, initResult.Instructions)
		}
	}

	discovered, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"get_card_session":    false,
		"search_card_session": false,
	}
	for _, tool := range discovered.Tools {
		if _, ok := required[tool.Name]; ok {
			required[tool.Name] = true
			if tool.InputSchema == nil {
				t.Fatalf("tool %s missing InputSchema", tool.Name)
			}
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("missing tool %s in MCP ListTools", name)
		}
	}
}
