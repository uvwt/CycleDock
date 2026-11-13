package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/compaction"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentconfig"
)

const (
	// 单片字符上限显著低于 LLMSummarizer.MaxTranscriptChars（120000），
	// 为转义换行、前缀标签及滚动 seed 摘要留出安全余量。
	defaultChunkedSummaryMaxChars        = 80000
	defaultChunkedSummaryMaxEvents       = 120
	defaultChunkedSummaryToolContentCaps = 4000
	defaultLLMSummarizerMaxTranscript    = 120000
)

// ChunkedSummarizerConfig 配置分块滚动摘要器。
type ChunkedSummarizerConfig struct {
	Summarizer          compaction.Summarizer
	MaxChunkChars       int
	MaxChunkEvents      int
	MaxToolContentChars int
}

// ChunkedSummarizer 包装底层 compaction.Summarizer（生产中为 LLMSummarizer）。
// 当待压缩事件窗口过大或事件条数过多时，按工具调用/响应配对边界分片滚动摘要：
// 将前一片摘要包装为 seed 事件后与下一片事件合并再摘要，避免一次性渲染突破 MaxTranscriptChars 导致压缩死锁。
type ChunkedSummarizer struct {
	inner               compaction.Summarizer
	maxChunkChars       int
	maxChunkEvents      int
	maxToolContentChars int
}

var _ compaction.Summarizer = (*ChunkedSummarizer)(nil)

func NewChunkedSummarizer(cfg ChunkedSummarizerConfig) (*ChunkedSummarizer, error) {
	if cfg.Summarizer == nil {
		return nil, errors.New("chunked summarizer requires inner compaction.Summarizer")
	}
	maxChunkChars := cfg.MaxChunkChars
	if maxChunkChars <= 0 {
		maxChunkChars = defaultChunkedSummaryMaxChars
	}
	maxChunkEvents := cfg.MaxChunkEvents
	if maxChunkEvents <= 0 {
		maxChunkEvents = defaultChunkedSummaryMaxEvents
	}
	maxToolContentChars := cfg.MaxToolContentChars
	if maxToolContentChars == 0 {
		maxToolContentChars = defaultChunkedSummaryToolContentCaps
	}
	return &ChunkedSummarizer{
		inner:               cfg.Summarizer,
		maxChunkChars:       maxChunkChars,
		maxChunkEvents:      maxChunkEvents,
		maxToolContentChars: maxToolContentChars,
	}, nil
}

func newADKCompactionSummarizer(summaryLLM model.LLM) (compaction.Summarizer, error) {
	baseSummarizer, err := compaction.NewLLMSummarizer(compaction.LLMSummarizerConfig{
		Model:               summaryLLM,
		PromptTemplate:      compactionPromptTemplate,
		MaxToolContentChars: defaultChunkedSummaryToolContentCaps,
		MaxTranscriptChars:  defaultLLMSummarizerMaxTranscript,
		Timeout:             agentconfig.DefaultModelRequestTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK context summarizer: %w", err)
	}
	summarizer, err := NewChunkedSummarizer(ChunkedSummarizerConfig{
		Summarizer:          baseSummarizer,
		MaxChunkChars:       defaultChunkedSummaryMaxChars,
		MaxChunkEvents:      defaultChunkedSummaryMaxEvents,
		MaxToolContentChars: defaultChunkedSummaryToolContentCaps,
	})
	if err != nil {
		return nil, fmt.Errorf("create chunked ADK context summarizer: %w", err)
	}
	return summarizer, nil
}

// GetGoogleLLMVariant 透传底层 Summarizer 的后端标识，保持 ADK telemetry 行为一致。
func (s *ChunkedSummarizer) GetGoogleLLMVariant() genai.Backend {
	if provider, ok := s.inner.(interface{ GetGoogleLLMVariant() genai.Backend }); ok {
		return provider.GetGoogleLLMVariant()
	}
	return genai.BackendUnspecified
}

func (s *ChunkedSummarizer) SummarizeEvents(ctx context.Context, events []*session.Event) (compaction.SummarizeResult, error) {
	if len(events) == 0 {
		return compaction.SummarizeResult{}, nil
	}
	if err := ctx.Err(); err != nil {
		return compaction.SummarizeResult{}, err
	}

	eventChars := make([]int, len(events))
	totalChars := 0
	for i, ev := range events {
		c := estimateEventTranscriptChars(ev, s.maxToolContentChars)
		eventChars[i] = c
		if i > 0 && c > 0 {
			c++ // strings.Join 的行间换行
		}
		totalChars += c
	}

	if len(events) <= s.maxChunkEvents && totalChars <= s.maxChunkChars {
		return s.inner.SummarizeEvents(ctx, events)
	}

	var (
		cursor     int
		chunkIndex int
		seed       *session.Event
		totalUsage *genai.GenerateContentResponseUsageMetadata
		lastResult compaction.SummarizeResult
	)

	for cursor < len(events) {
		if err := ctx.Err(); err != nil {
			return compaction.SummarizeResult{Usage: totalUsage}, err
		}

		end := s.nextChunkEnd(events, eventChars, cursor, seed)
		chunkEvents := events[cursor:end]
		if seed != nil {
			combined := make([]*session.Event, 0, 1+len(chunkEvents))
			combined = append(combined, seed)
			combined = append(combined, chunkEvents...)
			chunkEvents = combined
		}

		res, err := s.inner.SummarizeEvents(ctx, chunkEvents)
		totalUsage = mergeUsageMetadata(totalUsage, res.Usage)
		if err != nil {
			return compaction.SummarizeResult{Usage: totalUsage}, err
		}
		// 若任意分片主动拒绝摘要（Content == nil），必须整体拒绝而不能丢弃前序分片。
		if res.Content == nil {
			return compaction.SummarizeResult{Usage: totalUsage}, nil
		}

		lastResult = res
		cursor = end
		chunkIndex++
		if cursor < len(events) {
			seed = buildChunkSeedEvent(chunkIndex, chunkEvents, res.Content)
		}
	}

	return compaction.SummarizeResult{
		Content: lastResult.Content,
		Usage:   totalUsage,
	}, nil
}

// nextChunkEnd 从 cursor 开始向右扫描，优先在预算内找到最晚的工具调用配对闭合边界；
// 若首个闭合组本身已超预算，则推进到该闭合边界为止，确保不会把工具调用与结果拆散且始终单调前进。
func (s *ChunkedSummarizer) nextChunkEnd(events []*session.Event, eventChars []int, cursor int, seed *session.Event) int {
	seedCount := 0
	chars := 0
	if seed != nil {
		seedCount = 1
		chars = estimateEventTranscriptChars(seed, s.maxToolContentChars)
	}

	openIDs := make(map[string]struct{})
	openNoIDByName := make(map[string]int)
	lastSafeEnd := 0

	for i := cursor; i < len(events); i++ {
		ev := events[i]
		if ev != nil {
			if ev.LLMResponse.Content != nil {
				// 先处理 FunctionResponse 再处理 FunctionCall，与 ADK longestSelfContainedPrefix 一致。
				for _, part := range ev.LLMResponse.Content.Parts {
					if part == nil || part.FunctionResponse == nil {
						continue
					}
					if part.FunctionResponse.ID != "" {
						delete(openIDs, part.FunctionResponse.ID)
					} else if part.FunctionResponse.Name != "" && openNoIDByName[part.FunctionResponse.Name] > 0 {
						openNoIDByName[part.FunctionResponse.Name]--
						if openNoIDByName[part.FunctionResponse.Name] == 0 {
							delete(openNoIDByName, part.FunctionResponse.Name)
						}
					}
				}
				for _, part := range ev.LLMResponse.Content.Parts {
					if part == nil || part.FunctionCall == nil {
						continue
					}
					if part.FunctionCall.ID != "" {
						openIDs[part.FunctionCall.ID] = struct{}{}
					} else {
						openNoIDByName[part.FunctionCall.Name]++
					}
				}
			}
			for id := range ev.Actions.RequestedToolConfirmations {
				openIDs[id] = struct{}{}
			}
		}

		count := seedCount + (i - cursor + 1)
		if chars > 0 && eventChars[i] > 0 {
			chars++
		}
		chars += eventChars[i]

		withinBudget := count <= s.maxChunkEvents && chars <= s.maxChunkChars
		if !withinBudget && lastSafeEnd > cursor {
			break
		}

		if len(openIDs) == 0 && len(openNoIDByName) == 0 {
			lastSafeEnd = i + 1
			if !withinBudget {
				break
			}
		}
	}

	if lastSafeEnd > cursor {
		return lastSafeEnd
	}
	return len(events)
}

func buildChunkSeedEvent(chunkIndex int, covered []*session.Event, summary *genai.Content) *session.Event {
	seedContent := sanitizeSeedContent(summary)
	startTS, endTS := eventTimeBounds(covered)
	var branch, isolationScope string
	for _, ev := range covered {
		if ev != nil {
			branch = ev.Branch
			isolationScope = ev.IsolationScope
			break
		}
	}
	return &session.Event{
		ID:             fmt.Sprintf("chunk-seed-%d", chunkIndex),
		Author:         "model",
		Timestamp:      startTS,
		Branch:         branch,
		IsolationScope: isolationScope,
		LLMResponse:    model.LLMResponse{Content: seedContent},
		Actions: session.EventActions{
			Compaction: &session.EventCompaction{
				StartTimestamp:   startTS,
				EndTimestamp:     endTS,
				CompactedContent: seedContent,
			},
		},
	}
}

func sanitizeSeedContent(summary *genai.Content) *genai.Content {
	if summary == nil {
		return nil
	}
	kept := make([]*genai.Part, 0, len(summary.Parts))
	for _, p := range summary.Parts {
		if p != nil && !p.Thought && strings.TrimSpace(p.Text) != "" {
			copied := *p
			kept = append(kept, &copied)
		}
	}
	if len(kept) == 0 {
		return summary
	}
	return &genai.Content{
		Role:  genai.RoleModel,
		Parts: kept,
	}
}

func eventTimeBounds(events []*session.Event) (time.Time, time.Time) {
	var minTS, maxTS time.Time
	for _, ev := range events {
		if ev == nil {
			continue
		}
		candidates := []time.Time{ev.Timestamp}
		if ev.Actions.Compaction != nil {
			candidates = append(candidates, ev.Actions.Compaction.StartTimestamp, ev.Actions.Compaction.EndTimestamp)
		}
		for _, ts := range candidates {
			if ts.IsZero() {
				continue
			}
			if minTS.IsZero() || ts.Before(minTS) {
				minTS = ts
			}
			if maxTS.IsZero() || ts.After(maxTS) {
				maxTS = ts
			}
		}
	}
	return minTS, maxTS
}

func mergeUsageMetadata(
	acc *genai.GenerateContentResponseUsageMetadata,
	delta *genai.GenerateContentResponseUsageMetadata,
) *genai.GenerateContentResponseUsageMetadata {
	if delta == nil {
		return acc
	}
	if acc == nil {
		copied := *delta
		return &copied
	}
	acc.PromptTokenCount += delta.PromptTokenCount
	acc.CandidatesTokenCount += delta.CandidatesTokenCount
	acc.CachedContentTokenCount += delta.CachedContentTokenCount
	acc.ThoughtsTokenCount += delta.ThoughtsTokenCount
	acc.TotalTokenCount += delta.TotalTokenCount
	return acc
}

// estimateEventTranscriptChars 估算单个事件经 LLMSummarizer.formatEvents 渲染后的最大字符数。
func estimateEventTranscriptChars(ev *session.Event, cap int) int {
	if ev == nil || ev.LLMResponse.Content == nil || len(ev.LLMResponse.Content.Parts) == 0 {
		return 0
	}
	authorLen := estimateEscapedRunes(ev.Author, -1)
	isCompaction := ev.Actions.Compaction != nil
	total := 0
	lines := 0

	addLine := func(lineChars int) {
		if lines > 0 {
			total++
		}
		total += lineChars
		lines++
	}

	for _, p := range ev.LLMResponse.Content.Parts {
		if p == nil {
			continue
		}
		switch {
		case p.Thought && p.Text != "":
			if !isCompaction {
				addLine(authorLen + len(" (thought): ") + estimateEscapedRunes(p.Text, cap))
			}
		case p.Text != "":
			addLine(authorLen + len(": ") + estimateEscapedRunes(p.Text, cap))
		}
		if p.FunctionCall != nil {
			nameLen := estimateEscapedRunes(p.FunctionCall.Name, -1)
			argsLen := estimateEscapedMapRunes(p.FunctionCall.Args, cap)
			addLine(authorLen + len(" called tool: ") + nameLen + 2 + argsLen)
		}
		if p.FunctionResponse != nil {
			nameLen := estimateEscapedRunes(p.FunctionResponse.Name, -1)
			respLen := estimateEscapedMapRunes(p.FunctionResponse.Response, cap)
			addLine(len("Tool response from ") + nameLen + len(": ") + respLen)
		}
		if hasPlaceholderPart(p) {
			addLine(authorLen + len(": []") + 48)
		}
	}
	return total
}

func hasPlaceholderPart(p *genai.Part) bool {
	return p.InlineData != nil || p.FileData != nil || p.ExecutableCode != nil || p.CodeExecutionResult != nil
}

func estimateEscapedMapRunes(v map[string]any, cap int) int {
	if len(v) == 0 {
		return 0
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var b strings.Builder
	b.WriteByte('{')
	// 只需构建到 cap 字符上界之外的少量余量即可，避免超大工具载荷反复拼接整份字符串。
	limit := -1
	if cap >= 0 {
		limit = cap + 64
	}
	truncatedEarly := false
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s: %v", k, v[k])
		if limit >= 0 && utf8.RuneCountInString(b.String()) > limit {
			truncatedEarly = true
			break
		}
	}
	if !truncatedEarly {
		b.WriteByte('}')
	}
	rendered := b.String()
	chars := estimateEscapedRunes(rendered, cap)
	if truncatedEarly && cap >= 0 && chars < cap+32 {
		chars = cap + 32
	}
	return chars
}

func estimateEscapedRunes(text string, cap int) int {
	if text == "" {
		return 0
	}
	count := 0
	extra := 0
	truncated := false
	for _, r := range text {
		if cap >= 0 && count >= cap {
			truncated = true
			break
		}
		count++
		if isLineBreakerRune(r) {
			extra++
		}
	}
	if truncated {
		return count + extra + 32
	}
	return count + extra
}

func isLineBreakerRune(r rune) bool {
	switch r {
	case '\r', '\n', '\v', '\f', '\u0085', '\u2028', '\u2029':
		return true
	default:
		return false
	}
}
