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

	"github.com/uvwt/CycleDock/internal/modelclient"
)

const (
	// 单片字符上限显著低于 LLMSummarizer.MaxTranscriptChars（120000），
	// 为转义换行、前缀标签及滚动 seed 摘要留出安全余量。
	defaultChunkedSummaryMaxChars        = 80000
	defaultChunkedSummaryMaxEvents       = 120
	defaultChunkedSummaryToolContentCaps = 4000
	defaultLLMSummarizerMaxTranscript    = 120000

	// 当所有摘要模型路由全部失败时，若待压缩历史已达到防膨胀水位或触发上下文容量超限，
	// 启用确定性结构化纯文本兜底摘要，阻止 ADK 放行未压缩超大 Prompt 滚雪球爆窗。
	defaultEmergencyFallbackMinChars     = 16000
	defaultEmergencyFallbackMaxChars     = 16000
	defaultEmergencyFallbackLineMaxRunes = 320
	defaultEmergencyFallbackMaxItems     = 30
)

// ChunkedSummarizerConfig 配置分块滚动摘要器。
type ChunkedSummarizerConfig struct {
	Summarizer                compaction.Summarizer
	MaxChunkChars             int
	MaxChunkEvents            int
	MaxToolContentChars       int
	EnableEmergencyFallback   bool
	EmergencyFallbackMinChars int
	EmergencyFallbackMaxChars int
}

// ChunkedSummarizer 包装底层 compaction.Summarizer（生产中为 LLMSummarizer）。
// 当待压缩事件窗口过大或事件条数过多时，按工具调用/响应配对边界分片滚动摘要：
// 将前一片摘要包装为 seed 事件后与下一片事件合并再摘要，避免一次性渲染突破 MaxTranscriptChars 导致压缩死锁。
type ChunkedSummarizer struct {
	inner                     compaction.Summarizer
	maxChunkChars             int
	maxChunkEvents            int
	maxToolContentChars       int
	enableEmergencyFallback   bool
	emergencyFallbackMinChars int
	emergencyFallbackMaxChars int
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
	fallbackMinChars := cfg.EmergencyFallbackMinChars
	if fallbackMinChars <= 0 {
		fallbackMinChars = defaultEmergencyFallbackMinChars
	}
	fallbackMaxChars := cfg.EmergencyFallbackMaxChars
	if fallbackMaxChars <= 0 {
		fallbackMaxChars = defaultEmergencyFallbackMaxChars
	}
	return &ChunkedSummarizer{
		inner:                     cfg.Summarizer,
		maxChunkChars:             maxChunkChars,
		maxChunkEvents:            maxChunkEvents,
		maxToolContentChars:       maxToolContentChars,
		enableEmergencyFallback:   cfg.EnableEmergencyFallback,
		emergencyFallbackMinChars: fallbackMinChars,
		emergencyFallbackMaxChars: fallbackMaxChars,
	}, nil
}

func newADKCompactionSummarizer(summaryLLM model.LLM) (compaction.Summarizer, error) {
	// 注意：此处不设置 LLMSummarizerConfig.Timeout。
	// ADK LLMSummarizer 会用 context.WithTimeout 包装传给 summaryLLM 的外层 ctx；
	// 若外层 Timeout 与单条路由请求超时（DefaultModelRequestTimeout=120s）等长，
	// 首条路由一旦超时就会令外层 ctx.Err() != nil，导致 adkRouteModel 无法继续 failover 到备用路由。
	// 单次 HTTP 请求超时由 adkRouteModel.routeConfig 按路由独立控制，外层生命周期遵循 Attempt runCtx。
	baseSummarizer, err := compaction.NewLLMSummarizer(compaction.LLMSummarizerConfig{
		Model:               summaryLLM,
		PromptTemplate:      compactionPromptTemplate,
		MaxToolContentChars: defaultChunkedSummaryToolContentCaps,
		MaxTranscriptChars:  defaultLLMSummarizerMaxTranscript,
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK context summarizer: %w", err)
	}
	summarizer, err := NewChunkedSummarizer(ChunkedSummarizerConfig{
		Summarizer:                baseSummarizer,
		MaxChunkChars:             defaultChunkedSummaryMaxChars,
		MaxChunkEvents:            defaultChunkedSummaryMaxEvents,
		MaxToolContentChars:       defaultChunkedSummaryToolContentCaps,
		EnableEmergencyFallback:   true,
		EmergencyFallbackMinChars: defaultEmergencyFallbackMinChars,
		EmergencyFallbackMaxChars: defaultEmergencyFallbackMaxChars,
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
		res, err := s.inner.SummarizeEvents(ctx, events)
		if (err != nil || res.Content == nil) && ctx.Err() == nil && s.shouldUseEmergencyFallback(totalChars, err) {
			if fallback := s.buildEmergencyFallbackSummary(nil, events); fallback != nil {
				return compaction.SummarizeResult{
					Content: fallback,
					Usage:   res.Usage,
				}, nil
			}
		}
		return res, err
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
		if err != nil || res.Content == nil {
			if ctx.Err() == nil && s.shouldUseEmergencyFallback(totalChars, err) {
				if fallback := s.buildEmergencyFallbackSummary(seed, events[cursor:]); fallback != nil {
					return compaction.SummarizeResult{
						Content: fallback,
						Usage:   totalUsage,
					}, nil
				}
			}
			if err != nil {
				return compaction.SummarizeResult{Usage: totalUsage}, err
			}
			// 若任意分片主动拒绝摘要（Content == nil），必须整体拒绝而不能丢弃前序分片。
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

func (s *ChunkedSummarizer) shouldUseEmergencyFallback(totalChars int, err error) bool {
	if s == nil || !s.enableEmergencyFallback {
		return false
	}
	if err != nil && (errors.Is(err, ErrRouteContextCapacityExceeded) ||
		errors.Is(err, ErrRouteContextCapacityUnknown) ||
		modelclient.IsContextTooLargeError(err)) {
		return true
	}
	return totalChars >= s.emergencyFallbackMinChars
}

// buildEmergencyFallbackSummary 在所有摘要模型路由均不可用或单分片异常时，
// 构建确定性结构化纯文本（prose）兜底摘要。
// ADK TailRetention 传入的 events 已经严格按工具调用/响应配对边界裁剪且保留了近期活跃尾部；
// 此处只输出 RoleModel 纯文本 Part（不含任何 FunctionCall/FunctionResponse），
// 既不破坏工具配对完整性，也能防止未压缩大历史持续滚雪球突破主模型上下文窗口。
func (s *ChunkedSummarizer) buildEmergencyFallbackSummary(
	seed *session.Event,
	remaining []*session.Event,
) *genai.Content {
	maxChars := s.emergencyFallbackMaxChars
	if maxChars <= 0 {
		maxChars = defaultEmergencyFallbackMaxChars
	}

	var priorSummary string
	rawEvents := remaining
	if seed != nil {
		priorSummary = extractCompactionSeedText(seed)
	} else if len(rawEvents) > 0 && rawEvents[0] != nil && rawEvents[0].Actions.Compaction != nil {
		priorSummary = extractCompactionSeedText(rawEvents[0])
		rawEvents = rawEvents[1:]
	}

	var (
		userSnippets  []string
		modelSnippets []string
		toolSnippets  []string
		artifactRefs  []string
		seenArtifacts = make(map[string]struct{})
		hasNonASCII   bool
	)

	checkLang := func(text string) {
		if hasNonASCII {
			return
		}
		for _, r := range text {
			if r >= utf8.RuneSelf {
				hasNonASCII = true
				return
			}
		}
	}
	checkLang(priorSummary)

	for _, ev := range rawEvents {
		if ev == nil || ev.LLMResponse.Content == nil {
			continue
		}
		role := ev.LLMResponse.Content.Role
		if role == "" {
			role = ev.Author
		}
		for _, p := range ev.LLMResponse.Content.Parts {
			if p == nil || p.Thought {
				continue
			}
			if txt := strings.TrimSpace(p.Text); txt != "" {
				checkLang(txt)
				compact := compactSingleLine(txt, defaultEmergencyFallbackLineMaxRunes)
				if role == genai.RoleUser || ev.Author == "user" {
					userSnippets = append(userSnippets, compact)
				} else {
					modelSnippets = append(modelSnippets, compact)
				}
			}
			if fc := p.FunctionCall; fc != nil {
				argsPreview := compactMapPreview(fc.Args, 160)
				toolSnippets = append(toolSnippets, fmt.Sprintf("call `%s`(%s)", fc.Name, argsPreview))
			}
			if fr := p.FunctionResponse; fr != nil {
				for _, ref := range extractArtifactRefsFromMap(fr.Response) {
					if _, exists := seenArtifacts[ref]; !exists {
						seenArtifacts[ref] = struct{}{}
						artifactRefs = append(artifactRefs, ref)
					}
				}
				respPreview := compactMapPreview(fr.Response, 200)
				toolSnippets = append(toolSnippets, fmt.Sprintf("result `%s`: %s", fr.Name, respPreview))
			}
		}
	}

	lang := "English"
	if hasNonASCII {
		lang = "中文"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Conversation Language: %s\n\n", lang)
	b.WriteString("## Objective\n")
	if len(userSnippets) > 0 {
		fmt.Fprintf(&b, "- %s\n", userSnippets[0])
		if len(userSnippets) > 1 {
			fmt.Fprintf(&b, "- Latest user instruction: %s\n", userSnippets[len(userSnippets)-1])
		}
	} else {
		b.WriteString("- Continue the active Card Session task from retained recent turns and prior context.\n")
	}

	if priorSummary != "" {
		priorCap := max(maxChars/2, 2000)
		b.WriteString("\n## Prior Compacted Checkpoint\n")
		b.WriteString(truncateRunesWithEllipsis(priorSummary, priorCap))
		b.WriteString("\n")
	}

	b.WriteString("\n## Work State\n### Completed\n")
	if len(toolSnippets) == 0 && len(modelSnippets) == 0 {
		b.WriteString("- Deterministic emergency compaction condensed earlier raw events while all summarization model routes were unavailable.\n")
	} else {
		for _, item := range tailStrings(toolSnippets, defaultEmergencyFallbackMaxItems) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		for _, item := range tailStrings(modelSnippets, 8) {
			fmt.Fprintf(&b, "- Agent progress: %s\n", item)
		}
	}

	b.WriteString("\n### Active\n- Resuming from the uncompacted recent event tail.\n")
	b.WriteString("\n### Blocked\n- Summarizer model routes were unavailable during compaction; deterministic fallback summary applied to prevent context window overflow.\n")
	b.WriteString("\n## Next Move\n- Continue from the retained recent turns; use `cycledock_search_session_history` or `cycledock_read_artifact` if exact older raw details are needed.\n")

	if len(artifactRefs) > 0 {
		b.WriteString("\n## Important Context\n")
		for _, ref := range tailStrings(artifactRefs, 20) {
			fmt.Fprintf(&b, "- artifact_ref: `%s`\n", ref)
		}
	}

	out := strings.TrimSpace(truncateRunesWithEllipsis(b.String(), maxChars))
	if out == "" {
		return nil
	}
	return genai.NewContentFromText(out, genai.RoleModel)
}

func extractCompactionSeedText(ev *session.Event) string {
	if ev == nil {
		return ""
	}
	if ev.Actions.Compaction != nil && ev.Actions.Compaction.CompactedContent != nil {
		if txt := textFromADKContent(ev.Actions.Compaction.CompactedContent); txt != "" {
			return txt
		}
	}
	return textFromADKContent(ev.LLMResponse.Content)
}

func extractArtifactRefsFromMap(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	var refs []string
	if raw, ok := m["artifact_ref"]; ok {
		if ref := strings.TrimSpace(fmt.Sprint(raw)); ref != "" && ref != "<nil>" {
			refs = append(refs, ref)
		}
	}
	for _, v := range m {
		if nested, ok := v.(map[string]any); ok {
			refs = append(refs, extractArtifactRefsFromMap(nested)...)
		}
	}
	return refs
}

func compactMapPreview(m map[string]any, maxRunes int) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		valPreview := compactSingleLine(fmt.Sprint(m[k]), max(maxRunes/2, 48))
		fmt.Fprintf(&b, "%s: %s", k, valPreview)
		if utf8.RuneCountInString(b.String()) >= maxRunes {
			break
		}
	}
	b.WriteByte('}')
	return truncateRunesWithEllipsis(b.String(), maxRunes)
}

func compactSingleLine(text string, maxRunes int) string {
	fields := strings.Fields(text)
	joined := strings.Join(fields, " ")
	return truncateRunesWithEllipsis(joined, maxRunes)
}

func truncateRunesWithEllipsis(text string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	if maxRunes <= 3 {
		var b strings.Builder
		count := 0
		for _, r := range text {
			if count >= maxRunes {
				break
			}
			b.WriteRune(r)
			count++
		}
		return b.String()
	}
	var b strings.Builder
	limit := maxRunes - 3
	count := 0
	for _, r := range text {
		if count >= limit {
			break
		}
		b.WriteRune(r)
		count++
	}
	b.WriteString("...")
	return b.String()
}

func tailStrings(items []string, limit int) []string {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[len(items)-limit:]
}
