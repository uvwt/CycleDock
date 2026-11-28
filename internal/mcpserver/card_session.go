package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	defaultCardSessionLimit   = 50
	maxCardSessionLimit       = 200
	defaultSessionSearchLimit = 8
	maxSessionSearchLimit     = 12
	maxSessionSearchQueryLen  = 240

	imagePayloadOmittedPlaceholder = "[image payload omitted in history]"
	truncatedSuffix                = "... [truncated]"

	// full 模式单字段与整体响应的字符预算（按 Unicode rune 计），
	// 防止单次大工具输出或长思考文本撑爆第三方 MCP 客户端上下文窗口。
	maxFullTextRunes       = 4000
	maxFullThinkingRunes   = 4000
	maxFullToolCallRunes   = 2000
	maxFullToolResultRunes = 4000
	maxFullTotalRunes      = 60000
)

type fullSessionLimits struct {
	maxTextRunes       int
	maxThinkingRunes   int
	maxToolCallRunes   int
	maxToolResultRunes int
	maxTotalRunes      int
}

var defaultFullSessionLimits = fullSessionLimits{
	maxTextRunes:       maxFullTextRunes,
	maxThinkingRunes:   maxFullThinkingRunes,
	maxToolCallRunes:   maxFullToolCallRunes,
	maxToolResultRunes: maxFullToolResultRunes,
	maxTotalRunes:      maxFullTotalRunes,
}

type GetCardSessionInput struct {
	CardID string `json:"card_id" jsonschema:"CycleDock card UUID (supports discussion, todo, and ongoing cards)"`
	Mode   string `json:"mode,omitempty" jsonschema:"Session detail mode: simple (default, clean human-agent conversation bubbles) or full (raw execution rounds with thinking, tool_calls, and tool_results)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum bubbles or execution rounds to return, 1-200; default 50"`
}

type SessionBubbleView struct {
	Author    string   `json:"author"`
	Content   string   `json:"content,omitempty"`
	Images    []string `json:"images,omitempty"`
	Timestamp string   `json:"timestamp"`
}

type SessionRoundView struct {
	EventID      string           `json:"event_id"`
	InvocationID string           `json:"invocation_id,omitempty"`
	OccurredAt   string           `json:"occurred_at"`
	Author       string           `json:"author"`
	Role         string           `json:"role,omitempty"`
	Text         string           `json:"text,omitempty"`
	Thinking     string           `json:"thinking,omitempty"`
	ToolCalls    []map[string]any `json:"tool_calls,omitempty"`
	ToolResults  []map[string]any `json:"tool_results,omitempty"`
}

type GetCardSessionOutput struct {
	CardID    string              `json:"card_id"`
	CardTitle string              `json:"card_title"`
	CardKind  string              `json:"card_kind"`
	Mode      string              `json:"mode"`
	Count     int                 `json:"count"`
	Truncated bool                `json:"truncated"`
	Note      string              `json:"note,omitempty"`
	Bubbles   []SessionBubbleView `json:"bubbles,omitempty"`
	Rounds    []SessionRoundView  `json:"rounds,omitempty"`
}

type SearchCardSessionInput struct {
	CardID string `json:"card_id" jsonschema:"CycleDock card UUID (supports discussion, todo, and ongoing cards)"`
	Query  string `json:"query" jsonschema:"Keyword or phrase to search in persisted session execution events (max 240 characters)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum matching history events to return, 1-12; default 8"`
}

type SessionHistoryHitView struct {
	EventID      string `json:"event_id"`
	InvocationID string `json:"invocation_id,omitempty"`
	OccurredAt   string `json:"occurred_at"`
	Author       string `json:"author"`
	Content      string `json:"content"`
}

type SearchCardSessionOutput struct {
	CardID    string                  `json:"card_id"`
	CardTitle string                  `json:"card_title"`
	CardKind  string                  `json:"card_kind"`
	Query     string                  `json:"query"`
	Count     int                     `json:"count"`
	Note      string                  `json:"note,omitempty"`
	Hits      []SessionHistoryHitView `json:"hits"`
}

func getCardSessionHandler(
	dataStore *store.Store,
	sessionService *agentsession.Service,
) mcp.ToolHandlerFor[GetCardSessionInput, GetCardSessionOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input GetCardSessionInput) (*mcp.CallToolResult, GetCardSessionOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, GetCardSessionOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, GetCardSessionOutput{}, err
		}
		mode := strings.ToLower(strings.TrimSpace(input.Mode))
		if mode == "" {
			mode = "simple"
		}
		if mode != "simple" && mode != "full" {
			return nil, GetCardSessionOutput{}, errors.New("INVALID_REQUEST: mode must be simple or full")
		}
		limit := input.Limit
		if limit == 0 {
			limit = defaultCardSessionLimit
		} else if limit < 0 || limit > maxCardSessionLimit {
			return nil, GetCardSessionOutput{}, fmt.Errorf("INVALID_REQUEST: limit must be between 1 and %d", maxCardSessionLimit)
		}

		// 任何模式均先按认证 Workspace 校验卡片存在性与归属，保持严格多租户隔离。
		card, err := dataStore.GetCard(ctx, actor.WorkspaceID, cardID)
		if err != nil {
			return nil, GetCardSessionOutput{}, stableToolError(err)
		}

		if mode == "full" {
			if sessionService == nil {
				return nil, GetCardSessionOutput{}, errors.New("UNAVAILABLE: session service is not configured for full session inspection")
			}
			rounds, err := sessionService.InspectFullSession(ctx, actor.WorkspaceID, cardID, limit)
			if err != nil {
				return nil, GetCardSessionOutput{}, stableToolError(err)
			}
			formatted, truncated := formatFullSessionRounds(rounds, defaultFullSessionLimits)
			output := GetCardSessionOutput{
				CardID:    card.ID.String(),
				CardTitle: card.Title,
				CardKind:  card.Kind,
				Mode:      "full",
				Count:     len(formatted),
				Truncated: truncated,
				Rounds:    formatted,
			}
			if len(formatted) == 0 {
				output.Note = `No retained execution rounds found for this card (it may not have executed yet, or older process events were cleaned up past retention); use mode="simple" to inspect durable conversation bubbles.`
			} else if truncated {
				output.Note = "Output was truncated to stay within client context limits; narrow limit or use search_card_session for specific historical evidence."
			}
			return nil, output, nil
		}

		timeline, err := dataStore.ListCardTimeline(ctx, actor.WorkspaceID, cardID)
		if err != nil {
			return nil, GetCardSessionOutput{}, stableToolError(err)
		}
		bubbles := store.ExtractSessionBubbles(timeline, limit)
		views := make([]SessionBubbleView, 0, len(bubbles))
		for _, item := range bubbles {
			views = append(views, SessionBubbleView{
				Author:    item.Author,
				Content:   item.Content,
				Images:    item.Images,
				Timestamp: item.Timestamp.UTC().Format(time.RFC3339),
			})
		}
		return nil, GetCardSessionOutput{
			CardID:    card.ID.String(),
			CardTitle: card.Title,
			CardKind:  card.Kind,
			Mode:      "simple",
			Count:     len(views),
			Truncated: false,
			Bubbles:   views,
		}, nil
	}
}

func searchCardSessionHandler(
	dataStore *store.Store,
	sessionService *agentsession.Service,
) mcp.ToolHandlerFor[SearchCardSessionInput, SearchCardSessionOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input SearchCardSessionInput) (*mcp.CallToolResult, SearchCardSessionOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, SearchCardSessionOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, SearchCardSessionOutput{}, err
		}
		query := strings.TrimSpace(input.Query)
		if query == "" {
			return nil, SearchCardSessionOutput{}, errors.New("INVALID_REQUEST: query is required")
		}
		if utf8.RuneCountInString(query) > maxSessionSearchQueryLen {
			return nil, SearchCardSessionOutput{}, fmt.Errorf("INVALID_REQUEST: query exceeds %d characters", maxSessionSearchQueryLen)
		}
		limit := input.Limit
		if limit == 0 {
			limit = defaultSessionSearchLimit
		} else if limit < 0 || limit > maxSessionSearchLimit {
			return nil, SearchCardSessionOutput{}, fmt.Errorf("INVALID_REQUEST: limit must be between 1 and %d", maxSessionSearchLimit)
		}
		if sessionService == nil {
			return nil, SearchCardSessionOutput{}, errors.New("UNAVAILABLE: session service is not configured for session history search")
		}

		// 先按 Workspace 校验卡片归属，避免越权探测其他 Workspace 的会话历史。
		card, err := dataStore.GetCard(ctx, actor.WorkspaceID, cardID)
		if err != nil {
			return nil, SearchCardSessionOutput{}, stableToolError(err)
		}

		hits, err := sessionService.SearchHistory(ctx, actor.WorkspaceID, cardID, query, limit)
		if err != nil {
			return nil, SearchCardSessionOutput{}, stableToolError(err)
		}

		views := make([]SessionHistoryHitView, 0, len(hits))
		for _, hit := range hits {
			views = append(views, SessionHistoryHitView{
				EventID:      hit.EventID,
				InvocationID: hit.InvocationID,
				OccurredAt:   hit.OccurredAt.UTC().Format(time.RFC3339Nano),
				Author:       hit.Author,
				Content:      hit.Content,
			})
		}

		output := SearchCardSessionOutput{
			CardID:    card.ID.String(),
			CardTitle: card.Title,
			CardKind:  card.Kind,
			Query:     query,
			Count:     len(views),
			Hits:      views,
		}
		if len(views) == 0 {
			output.Note = "No matching session history events found; older detailed events beyond retention may have been cleaned up."
		}
		return nil, output, nil
	}
}

// formatFullSessionRounds 对底层执行轮次执行两级截断保护：
// 1. 先按字段预算截断单条 text / thinking / tool_calls / tool_results，并保留图片脱敏占位；
// 2. 再按整体响应字符预算从最新轮次向前保留，确保第三方 MCP 客户端优先拿到最近状态且不撑爆上下文。
func formatFullSessionRounds(rounds []agentsession.SessionRound, limits fullSessionLimits) ([]SessionRoundView, bool) {
	if len(rounds) == 0 {
		return nil, false
	}

	truncated := false
	candidates := make([]SessionRoundView, 0, len(rounds))
	for _, round := range rounds {
		text, textTrunc := truncateRunes(round.Text, limits.maxTextRunes)
		thinking, thinkTrunc := truncateRunes(round.Thinking, limits.maxThinkingRunes)
		toolCalls, callsTrunc := truncateToolEntries(round.ToolCalls, "args", limits.maxToolCallRunes)
		toolResults, resultsTrunc := truncateToolEntries(round.ToolResults, "response", limits.maxToolResultRunes)
		if textTrunc || thinkTrunc || callsTrunc || resultsTrunc {
			truncated = true
		}

		candidates = append(candidates, SessionRoundView{
			EventID:      round.EventID,
			InvocationID: round.InvocationID,
			OccurredAt:   round.OccurredAt.UTC().Format(time.RFC3339Nano),
			Author:       round.Author,
			Role:         round.Role,
			Text:         text,
			Thinking:     thinking,
			ToolCalls:    toolCalls,
			ToolResults:  toolResults,
		})
	}

	if limits.maxTotalRunes <= 0 {
		return candidates, truncated
	}

	keptReverse := make([]SessionRoundView, 0, len(candidates))
	usedRunes := 0
	for i := len(candidates) - 1; i >= 0; i-- {
		size := jsonRuneCount(candidates[i])
		if usedRunes+size <= limits.maxTotalRunes {
			keptReverse = append(keptReverse, candidates[i])
			usedRunes += size
			continue
		}
		truncated = true
		if len(keptReverse) == 0 {
			// 即使单条最新轮次本身包含过多工具输出，也强制压缩到预算内并保留这一条，避免返回空结果。
			keptReverse = append(keptReverse, compactSingleRound(candidates[i], limits.maxTotalRunes))
		}
		break
	}
	slices.Reverse(keptReverse)
	return keptReverse, truncated
}

func truncateToolEntries(entries []map[string]any, payloadKey string, maxEntryRunes int) ([]map[string]any, bool) {
	if len(entries) == 0 {
		return nil, false
	}
	truncated := false
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		cloned := make(map[string]any, len(entry))
		for k, v := range entry {
			if k == payloadKey {
				shrunk, entryTrunc := truncateStructuredValue(v, maxEntryRunes)
				cloned[k] = shrunk
				if entryTrunc {
					truncated = true
				}
				continue
			}
			if str, ok := v.(string); ok {
				shrunk, strTrunc := truncateRunes(str, maxEntryRunes)
				cloned[k] = shrunk
				if strTrunc {
					truncated = true
				}
				continue
			}
			cloned[k] = v
		}
		out = append(out, cloned)
	}
	return out, truncated
}

func truncateStructuredValue(val any, maxRunes int) (any, bool) {
	if val == nil || maxRunes <= 0 {
		return val, false
	}
	sanitized := sanitizeInlineImages(val)
	raw, err := json.Marshal(sanitized)
	if err == nil && utf8.RuneCount(raw) <= maxRunes {
		return sanitized, false
	}

	// 先保留原结构并截断深层长字符串字段，尽量不破坏常用字段的可读性。
	leafLimit := max(128, maxRunes/2)
	shrunk := truncateStringsInValue(sanitized, leafLimit)
	if shrunkRaw, err := json.Marshal(shrunk); err == nil && utf8.RuneCount(shrunkRaw) <= maxRunes {
		return shrunk, true
	}

	// 结构本身字段/元素过多时退回截断预览，同时保留 inline_images 脱敏占位标识。
	previewSource := string(raw)
	if previewSource == "" {
		previewSource = fmt.Sprint(sanitized)
	}
	preview, _ := truncateRunes(previewSource, maxRunes)
	fallback := map[string]any{
		"_truncated": true,
		"preview":    preview,
	}
	if m, ok := sanitized.(map[string]any); ok {
		if _, hasInline := m["inline_images"]; hasInline {
			fallback["inline_images"] = imagePayloadOmittedPlaceholder
		}
	}
	return fallback, true
}

func sanitizeInlineImages(val any) any {
	switch typed := val.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			if k == "inline_images" {
				// 无论底层是否已替换，统一确保图片二进制载荷以脱敏占位符呈现。
				out[k] = imagePayloadOmittedPlaceholder
				continue
			}
			out[k] = sanitizeInlineImages(v)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeInlineImages(item)
		}
		return out
	default:
		return val
	}
}

func truncateStringsInValue(val any, maxLeafRunes int) any {
	switch typed := val.(type) {
	case string:
		out, _ := truncateRunes(typed, maxLeafRunes)
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			if k == "inline_images" {
				out[k] = imagePayloadOmittedPlaceholder
				continue
			}
			out[k] = truncateStringsInValue(v, maxLeafRunes)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = truncateStringsInValue(item, maxLeafRunes)
		}
		return out
	default:
		return val
	}
}

func compactSingleRound(round SessionRoundView, maxRunes int) SessionRoundView {
	if jsonRuneCount(round) <= maxRunes {
		return round
	}
	// 先裁剪较早的工具结果与调用，尽量保留最新执行结果与文本结论。
	for len(round.ToolResults) > 1 && jsonRuneCount(round) > maxRunes {
		round.ToolResults = round.ToolResults[1:]
	}
	for len(round.ToolCalls) > 1 && jsonRuneCount(round) > maxRunes {
		round.ToolCalls = round.ToolCalls[1:]
	}
	if jsonRuneCount(round) > maxRunes {
		fieldBudget := max(128, maxRunes/4)
		round.Text, _ = truncateRunes(round.Text, fieldBudget)
		round.Thinking, _ = truncateRunes(round.Thinking, fieldBudget)
		round.ToolCalls, _ = truncateToolEntries(round.ToolCalls, "args", fieldBudget)
		round.ToolResults, _ = truncateToolEntries(round.ToolResults, "response", fieldBudget)
	}
	if jsonRuneCount(round) > maxRunes {
		round.ToolResults = nil
		round.ToolCalls = nil
		round.Thinking, _ = truncateRunes(round.Thinking, max(64, maxRunes/3))
		round.Text, _ = truncateRunes(round.Text, max(64, maxRunes/3))
	}
	return round
}

func truncateRunes(s string, maxRunes int) (string, bool) {
	if maxRunes <= 0 {
		return s, false
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s, false
	}
	return string(runes[:maxRunes]) + truncatedSuffix, true
}

func jsonRuneCount(v any) int {
	raw, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return utf8.RuneCount(raw)
}
