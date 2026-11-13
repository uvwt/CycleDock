package agent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	adkrunner "google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/compaction"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentconfig"
	"github.com/uvwt/CycleDock/internal/modelclient"
)

type recordingChunkSummarizerLLM struct {
	mu      sync.Mutex
	prompts []string
}

func (m *recordingChunkSummarizerLLM) Name() string {
	return "recording-chunk-summarizer-llm"
}

func (m *recordingChunkSummarizerLLM) GenerateContent(
	_ context.Context,
	req *model.LLMRequest,
	_ bool,
) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		var b strings.Builder
		for _, c := range req.Contents {
			if c == nil {
				continue
			}
			for _, p := range c.Parts {
				if p != nil && p.Text != "" {
					b.WriteString(p.Text)
				}
			}
		}
		prompt := b.String()

		m.mu.Lock()
		chunkIdx := len(m.prompts) + 1
		m.prompts = append(m.prompts, prompt)
		m.mu.Unlock()

		// 模拟摘要器将最早的持久事实和本分片最新工具进展滚动传递下去。
		summary := fmt.Sprintf(
			"Conversation Language: 中文\n\n## Objective\n修复生产卡片 001329a7-4f8d-468a-8432-a64d1d48b5ba 的压缩超限。\n\n## Work State\n### Completed\n- 已完成分片 %d 摘要。\n\n## Important Context\n- card_id: 001329a7-4f8d-468a-8432-a64d1d48b5ba\n- chunk_count: %d",
			chunkIdx, chunkIdx,
		)
		yield(&model.LLMResponse{
			Content: genai.NewContentFromText(summary, genai.RoleModel),
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:        1000,
				CandidatesTokenCount:    200,
				CachedContentTokenCount: 50,
				ThoughtsTokenCount:      20,
				TotalTokenCount:         1220,
			},
			FinishReason: genai.FinishReasonStop,
			TurnComplete: true,
		}, nil)
	}
}

func buildPairedBacklogEvents(totalPairs int) []*session.Event {
	base := time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)
	events := make([]*session.Event, 0, totalPairs*3+1)
	events = append(events, &session.Event{
		ID:           "ev-init",
		InvocationID: "inv-init",
		Author:       "user",
		Timestamp:    base,
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromText(
				"请排查生产卡片 001329a7-4f8d-468a-8432-a64d1d48b5ba 的 token 超限问题并完成修复。",
				genai.RoleUser,
			),
		},
	})

	for i := 1; i <= totalPairs; i++ {
		callID1 := fmt.Sprintf("call-%04d-a", i)
		callID2 := fmt.Sprintf("call-%04d-b", i)
		ts := base.Add(time.Duration(i*3) * time.Second)

		events = append(events, &session.Event{
			ID:           fmt.Sprintf("ev-call-%04d", i),
			InvocationID: fmt.Sprintf("inv-%04d", i),
			Author:       "cycle_agent",
			Timestamp:    ts,
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleModel,
					Parts: []*genai.Part{
						func() *genai.Part {
							p := genai.NewPartFromFunctionCall("cycledock_search_session_history", map[string]any{
								"path":   fmt.Sprintf("internal/agent/file_%04d.go", i),
								"detail": strings.Repeat("arg-", 40),
							})
							p.FunctionCall.ID = callID1
							return p
						}(),
						func() *genai.Part {
							p := genai.NewPartFromFunctionCall("workspace_read_source_file", map[string]any{
								"target": fmt.Sprintf("pkg/service/handler_%04d.go", i),
								"detail": strings.Repeat("opt-", 40),
							})
							p.FunctionCall.ID = callID2
							return p
						}(),
					},
				},
			},
		})

		events = append(events, &session.Event{
			ID:           fmt.Sprintf("ev-resp-%04d", i),
			InvocationID: fmt.Sprintf("inv-%04d", i),
			Author:       "cycle_agent",
			Timestamp:    ts.Add(time.Second),
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleUser,
					Parts: []*genai.Part{
						func() *genai.Part {
							p := genai.NewPartFromFunctionResponse("cycledock_search_session_history", map[string]any{
								"call_tag": callID1,
								"content":  fmt.Sprintf("result-%04d:%s", i, strings.Repeat("x", 220)),
							})
							p.FunctionResponse.ID = callID1
							return p
						}(),
						func() *genai.Part {
							p := genai.NewPartFromFunctionResponse("workspace_read_source_file", map[string]any{
								"call_tag": callID2,
								"content":  fmt.Sprintf("source-%04d:%s", i, strings.Repeat("y", 220)),
							})
							p.FunctionResponse.ID = callID2
							return p
						}(),
					},
				},
			},
		})

		events = append(events, &session.Event{
			ID:           fmt.Sprintf("ev-model-%04d", i),
			InvocationID: fmt.Sprintf("inv-%04d", i),
			Author:       "cycle_agent",
			Timestamp:    ts.Add(2 * time.Second),
			LLMResponse: model.LLMResponse{
				Content: genai.NewContentFromText(
					fmt.Sprintf("已核查第 %d 步输出 %s。", i, strings.Repeat("note-", 20)),
					genai.RoleModel,
				),
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
					PromptTokenCount:     900000,
					CandidatesTokenCount: 100,
					TotalTokenCount:      900100,
				},
			},
		})
	}
	return events
}

func TestChunkedSummarizer_820PlusEventsWithPairedToolsHealsAndAggregatesUsage(t *testing.T) {
	// 275 组 * 3 + 1 = 826 条事件，渲染后超过 120,000 字符，直接复现生产 810~824 条积压死锁窗口。
	events := buildPairedBacklogEvents(275)
	if len(events) < 820 {
		t.Fatalf("len(events)=%d, want >= 820", len(events))
	}

	rawLLM := &recordingChunkSummarizerLLM{}
	rawSummarizer, err := compaction.NewLLMSummarizer(compaction.LLMSummarizerConfig{
		Model:               rawLLM,
		PromptTemplate:      compactionPromptTemplate,
		MaxToolContentChars: 4000,
		MaxTranscriptChars:  120000,
		Timeout:             agentconfig.DefaultModelRequestTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 先确认裸 LLMSummarizer 在该积压窗口上必然报错（复现生产故障）。
	if _, rawErr := rawSummarizer.SummarizeEvents(context.Background(), events); rawErr == nil {
		t.Fatal("expected raw LLMSummarizer to fail on >120000 char backlog window")
	}

	chunkLLM := &recordingChunkSummarizerLLM{}
	chunked, err := newADKCompactionSummarizer(chunkLLM)
	if err != nil {
		t.Fatal(err)
	}

	res, err := chunked.SummarizeEvents(context.Background(), events)
	if err != nil {
		t.Fatalf("ChunkedSummarizer failed on 826-event backlog: %v", err)
	}
	if res.Content == nil {
		t.Fatal("expected non-nil summary content")
	}

	chunkCount := len(chunkLLM.prompts)
	if chunkCount < 2 {
		t.Fatalf("chunkCount=%d, want >= 2 chunks for 826 events", chunkCount)
	}

	// 验证每个分片：
	// 1. 第 2 片及之后都携带前一片摘要作为 seed；
	// 2. 任意分片都不会把一对 FunctionCall 与 FunctionResponse 拆散到两个分片。
	for idx, prompt := range chunkLLM.prompts {
		if idx > 0 {
			wantSeedMarker := fmt.Sprintf("已完成分片 %d 摘要", idx)
			if !strings.Contains(prompt, wantSeedMarker) {
				t.Fatalf("chunk %d missing previous seed summary marker %q", idx+1, wantSeedMarker)
			}
		}
		for i := 1; i <= 275; i++ {
			callMarker := fmt.Sprintf("internal/agent/file_%04d.go", i)
			respMarker := fmt.Sprintf("call_tag: call-%04d-a", i)
			hasCall := strings.Contains(prompt, callMarker)
			hasResp := strings.Contains(prompt, respMarker)
			if hasCall != hasResp {
				t.Fatalf("chunk %d split tool call/response pair %04d (hasCall=%v hasResp=%v)", idx+1, i, hasCall, hasResp)
			}
			callMarker2 := fmt.Sprintf("pkg/service/handler_%04d.go", i)
			respMarker2 := fmt.Sprintf("call_tag: call-%04d-b", i)
			hasCall2 := strings.Contains(prompt, callMarker2)
			hasResp2 := strings.Contains(prompt, respMarker2)
			if hasCall2 != hasResp2 {
				t.Fatalf("chunk %d split tool call/response pair %04d-b (hasCall=%v hasResp=%v)", idx+1, i, hasCall2, hasResp2)
			}
		}
	}

	// 验证返回单份最终摘要与累加 Usage。
	finalText := textFromADKContent(res.Content)
	if !strings.Contains(finalText, fmt.Sprintf("chunk_count: %d", chunkCount)) {
		t.Fatalf("final summary did not come from last chunk (%d): %s", chunkCount, finalText)
	}
	if res.Usage == nil ||
		int(res.Usage.PromptTokenCount) != chunkCount*1000 ||
		int(res.Usage.CandidatesTokenCount) != chunkCount*200 ||
		int(res.Usage.CachedContentTokenCount) != chunkCount*50 ||
		int(res.Usage.ThoughtsTokenCount) != chunkCount*20 ||
		int(res.Usage.TotalTokenCount) != chunkCount*1220 {
		t.Fatalf("aggregated usage=%+v, want %d * (1000/200/50/20/1220)", res.Usage, chunkCount)
	}
}

type stubChunkInnerSummarizer struct {
	calls int
	fn    func(call int, events []*session.Event) (compaction.SummarizeResult, error)
}

func (s *stubChunkInnerSummarizer) SummarizeEvents(_ context.Context, events []*session.Event) (compaction.SummarizeResult, error) {
	s.calls++
	return s.fn(s.calls, events)
}

func TestChunkedSummarizer_SmallWindowAndFailurePropagation(t *testing.T) {
	now := time.Now().UTC()
	smallEvents := []*session.Event{
		{
			ID:        "s1",
			Author:    "user",
			Timestamp: now,
			LLMResponse: model.LLMResponse{
				Content: genai.NewContentFromText("hello", genai.RoleUser),
			},
		},
		{
			ID:        "s2",
			Author:    "model",
			Timestamp: now.Add(time.Second),
			LLMResponse: model.LLMResponse{
				Content: genai.NewContentFromText("world", genai.RoleModel),
			},
		},
	}

	inner := &stubChunkInnerSummarizer{
		fn: func(_ int, events []*session.Event) (compaction.SummarizeResult, error) {
			if len(events) != 2 {
				t.Fatalf("expected 2 events directly, got %d", len(events))
			}
			return compaction.SummarizeResult{
				Content: genai.NewContentFromText("single summary", genai.RoleModel),
				Usage:   &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 10, TotalTokenCount: 15},
			}, nil
		},
	}
	cs, err := NewChunkedSummarizer(ChunkedSummarizerConfig{Summarizer: inner})
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.SummarizeEvents(context.Background(), smallEvents)
	if err != nil || inner.calls != 1 || textFromADKContent(res.Content) != "single summary" {
		t.Fatalf("small window got res=%+v err=%v calls=%d", res, err, inner.calls)
	}

	// 验证分片途中失败或拒绝时，保留已消耗的分片 Usage 且不返回伪造摘要。
	manyEvents := make([]*session.Event, 5)
	for i := range manyEvents {
		manyEvents[i] = &session.Event{
			ID:        fmt.Sprintf("m%d", i),
			Author:    "user",
			Timestamp: now.Add(time.Duration(i) * time.Second),
			LLMResponse: model.LLMResponse{
				Content: genai.NewContentFromText("msg", genai.RoleUser),
			},
		}
	}
	boom := errors.New("chunk 2 failed")
	failInner := &stubChunkInnerSummarizer{
		fn: func(call int, _ []*session.Event) (compaction.SummarizeResult, error) {
			if call == 1 {
				return compaction.SummarizeResult{
					Content: genai.NewContentFromText("chunk-1-ok", genai.RoleModel),
					Usage:   &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 100, TotalTokenCount: 120},
				}, nil
			}
			return compaction.SummarizeResult{
				Usage: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 50, TotalTokenCount: 50},
			}, boom
		},
	}
	failCS, err := NewChunkedSummarizer(ChunkedSummarizerConfig{
		Summarizer:     failInner,
		MaxChunkEvents: 2,
		MaxChunkChars:  10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	failRes, err := failCS.SummarizeEvents(context.Background(), manyEvents)
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v, want %v", err, boom)
	}
	if failRes.Content != nil || failRes.Usage == nil || failRes.Usage.PromptTokenCount != 150 {
		t.Fatalf("expected partial usage 150 and nil content on failure, got %+v", failRes)
	}

	declineInner := &stubChunkInnerSummarizer{
		fn: func(call int, _ []*session.Event) (compaction.SummarizeResult, error) {
			if call == 1 {
				return compaction.SummarizeResult{
					Content: genai.NewContentFromText("chunk-1-ok", genai.RoleModel),
					Usage:   &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 80, TotalTokenCount: 90},
				}, nil
			}
			return compaction.SummarizeResult{
				Usage: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 40, TotalTokenCount: 40},
			}, nil
		},
	}
	declineCS, err := NewChunkedSummarizer(ChunkedSummarizerConfig{
		Summarizer:     declineInner,
		MaxChunkEvents: 2,
		MaxChunkChars:  10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	declineRes, err := declineCS.SummarizeEvents(context.Background(), manyEvents)
	if err != nil || declineRes.Content != nil || declineRes.Usage == nil || declineRes.Usage.PromptTokenCount != 120 {
		t.Fatalf("expected decline with partial usage 120, got res=%+v err=%v", declineRes, err)
	}
}

type captureAgentLLM struct {
	mu           sync.Mutex
	seenContents [][]*genai.Content
}

func (m *captureAgentLLM) Name() string {
	return "capture-agent-llm"
}

func (m *captureAgentLLM) GenerateContent(
	_ context.Context,
	req *model.LLMRequest,
	_ bool,
) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.mu.Lock()
		m.seenContents = append(m.seenContents, req.Contents)
		m.mu.Unlock()

		yield(&model.LLMResponse{
			Content: genai.NewContentFromText("积压历史已成功分块压缩，本轮正常恢复响应。", genai.RoleModel),
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     5000,
				CandidatesTokenCount: 100,
				TotalTokenCount:      5100,
			},
			FinishReason: genai.FinishReasonStop,
			TurnComplete: true,
		}, nil)
	}
}

func TestChunkedSummarizer_ADKRunnerHeals820BacklogEvents(t *testing.T) {
	ctx := context.Background()
	sessionSvc := session.InMemoryService()
	appName, userID, sessionID := "cycledock", "ws-1", "card-001329a7"

	created, err := sessionSvc.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := created.Session

	// 向会话预先灌入 826 条含成对工具调用的积压历史事件（>135K transcript chars，末条 PromptTokenCount=900K）。
	backlog := buildPairedBacklogEvents(275)
	for _, ev := range backlog {
		if err := sessionSvc.AppendEvent(ctx, sess, ev); err != nil {
			t.Fatalf("append backlog event %s: %v", ev.ID, err)
		}
	}

	limits1M := modelclient.ModelLimits{
		ContextWindowTokens: 1048576,
		MaxInputTokens:      1048576,
		MaxOutputTokens:     65536,
		Source:              modelclient.LimitsSourceRegistry,
	}
	budget := ComputeRouteCompactionBudget(limits1M, DefaultCompactionBudgetPolicy())
	if budget.CompactAtTokens != 256000 {
		t.Fatalf("CompactAtTokens=%d, want 256000", budget.CompactAtTokens)
	}

	summaryLLM := &recordingChunkSummarizerLLM{}
	summarizer, err := newADKCompactionSummarizer(summaryLLM)
	if err != nil {
		t.Fatal(err)
	}

	agentLLM := &captureAgentLLM{}
	rootAgent, err := llmagent.New(llmagent.Config{
		Name:            "cycle_agent",
		Description:     "Backlog healing integration agent",
		Model:           agentLLM,
		IncludeContents: llmagent.IncludeContentsDefault,
	})
	if err != nil {
		t.Fatal(err)
	}

	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        appName,
		Agent:          rootAgent,
		SessionService: sessionSvc,
		Compaction: &compaction.Config{
			TokenThreshold:     budget.CompactAtTokens,
			EventRetentionSize: defaultCompactionEventRetention,
			Summarizer:         summarizer,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	turnMsg := genai.NewContentFromText("继续执行本轮任务。", genai.RoleUser)
	for _, runErr := range runner.Run(ctx, userID, sessionID, turnMsg, agent.RunConfig{}) {
		if runErr != nil {
			t.Fatalf("ADK runner failed on 826-event backlog session: %v", runErr)
		}
	}

	if len(summaryLLM.prompts) < 2 {
		t.Fatalf("expected ChunkedSummarizer to run multiple chunks during ADK TailRetention, got %d", len(summaryLLM.prompts))
	}
	if len(agentLLM.seenContents) != 1 {
		t.Fatalf("agentLLM calls=%d, want 1", len(agentLLM.seenContents))
	}
	// 验证发送给主模型的 Contents 已由 826 条缩减为 1 条压缩摘要 + 约 20 条保留尾部 + 当前用户消息。
	if gotContents := len(agentLLM.seenContents[0]); gotContents > 30 {
		t.Fatalf("prompt Contents len=%d, want <= 30 after chunked tail-retention compaction", gotContents)
	}

	// 验证会话里确实写入了 Compaction 事件。
	reloaded, err := sessionSvc.Get(ctx, &session.GetRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	compactionEvents := 0
	for i := 0; i < reloaded.Session.Events().Len(); i++ {
		if reloaded.Session.Events().At(i).Actions.Compaction != nil {
			compactionEvents++
		}
	}
	if compactionEvents != 1 {
		t.Fatalf("compactionEvents=%d, want 1 persisted EventCompaction", compactionEvents)
	}
}
