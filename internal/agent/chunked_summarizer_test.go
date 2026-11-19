package agent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	adkrunner "google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/compaction"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentconfig"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
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

func TestCompactionSummarizerFailsOverOnPrimaryTimeoutAndSharesActiveRoute(t *testing.T) {
	ctx := context.Background()

	var primaryCalls atomic.Int32
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls.Add(1)
		// 模拟首条路由连接挂起直至单路由请求超时。
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
			http.Error(w, "slow upstream", http.StatusGatewayTimeout)
		}
	}))
	defer primarySrv.Close()

	var backupCalls atomic.Int32
	backupSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callNum := backupCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if callNum == 1 {
			writeModelResponse(t, w, "Conversation Language: 中文\n\n## Objective\n验证首路由超时后备用路由成功生成压缩摘要。\n\n## Next Move\n继续由备用路由回复。", nil)
			return
		}
		writeModelResponse(t, w, "备用路由在共享 activeRoute 后直接完成主模型回复。", nil)
	}))
	defer backupSrv.Close()

	routes := []store.ModelRoute{
		{
			ID:                  uuid.New(),
			Enabled:             true,
			ProviderBaseUrl:     primarySrv.URL,
			Model:               "gpt-5.4-primary-hung",
			ContextWindowTokens: pgtype.Int4{Int32: 400000, Valid: true},
			MaxInputTokens:      pgtype.Int4{Int32: 400000, Valid: true},
			MaxOutputTokens:     pgtype.Int4{Int32: 16000, Valid: true},
			LimitsSource:        modelclient.LimitsSourceUser,
		},
		{
			ID:                  uuid.New(),
			Enabled:             true,
			ProviderBaseUrl:     backupSrv.URL,
			Model:               "grok-4.7-backup",
			ContextWindowTokens: pgtype.Int4{Int32: 239000, Valid: true},
			MaxInputTokens:      pgtype.Int4{Int32: 239000, Valid: true},
			MaxOutputTokens:     pgtype.Int4{Int32: 16000, Valid: true},
			LimitsSource:        modelclient.LimitsSourceUser,
		},
	}

	wsID := uuid.New()
	mainLLM, err := newADKRouteModel(modelclient.New(), nil, nil, wsID, routes)
	if err != nil {
		t.Fatal(err)
	}
	summaryLLM, err := newADKRouteModel(modelclient.New(), nil, nil, wsID, routes)
	if err != nil {
		t.Fatal(err)
	}
	// 共享同一 Attempt 的活跃路由游标，并将单次路由请求超时缩短为 40ms 以快速触发真实 DeadlineExceeded。
	summaryLLM.shareActiveRouteFrom(mainLLM)
	mainLLM.requestTimeout = 40 * time.Millisecond
	summaryLLM.requestTimeout = 40 * time.Millisecond

	summarizer, err := newADKCompactionSummarizer(summaryLLM)
	if err != nil {
		t.Fatal(err)
	}

	events := []*session.Event{
		{
			ID:        "evt-u1",
			Timestamp: time.Now().Add(-2 * time.Minute),
			Author:    "user",
			LLMResponse: model.LLMResponse{
				Content: genai.NewContentFromText("请汇总线上压缩超时告警并切换路由。", genai.RoleUser),
			},
		},
		{
			ID:        "evt-m1",
			Timestamp: time.Now().Add(-time.Minute),
			Author:    "cycle_agent",
			LLMResponse: model.LLMResponse{
				Content: genai.NewContentFromText("已定位首路由 120s 超时阻断 failover。", genai.RoleModel),
			},
		},
	}

	res, err := summarizer.SummarizeEvents(ctx, events)
	if err != nil {
		t.Fatalf("SummarizeEvents failed instead of failing over on primary route timeout: %v", err)
	}
	if res.Content == nil {
		t.Fatalf("expected non-nil compacted summary content, got %+v", res)
	}
	if got := textFromADKContent(res.Content); !strings.Contains(got, "验证首路由超时后备用路由成功生成压缩摘要") {
		t.Fatalf("unexpected summary text: %q", got)
	}
	primaryAfterSummary := primaryCalls.Load()
	if primaryAfterSummary == 0 {
		t.Fatal("expected primary route to be attempted before timeout failover")
	}
	if backupCalls.Load() != 1 {
		t.Fatalf("backupCalls=%d after summary, want 1", backupCalls.Load())
	}
	if mainLLM.loadActiveRouteIndex() != 1 || summaryLLM.loadActiveRouteIndex() != 1 {
		t.Fatalf("shared activeRoute index mismatch: main=%d summary=%d, want both 1", mainLLM.loadActiveRouteIndex(), summaryLLM.loadActiveRouteIndex())
	}

	// 验证接下来主模型发起请求时直接命中已共享切换的备用路由，不再重新撞向超时的首路由。
	var mainReply string
	for resp, runErr := range mainLLM.GenerateContent(ctx, &model.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText("继续执行后续步骤", genai.RoleUser),
		},
	}, false) {
		if runErr != nil {
			t.Fatalf("mainLLM GenerateContent error: %v", runErr)
		}
		if resp != nil && !resp.Partial {
			mainReply = textFromADKContent(resp.Content)
		}
	}
	if primaryCalls.Load() != primaryAfterSummary {
		t.Fatalf("mainLLM re-hit timed-out primary route! primaryCalls before=%d after=%d", primaryAfterSummary, primaryCalls.Load())
	}
	if backupCalls.Load() != 2 {
		t.Fatalf("backupCalls=%d, want 2", backupCalls.Load())
	}
	if mainReply != "备用路由在共享 activeRoute 后直接完成主模型回复。" {
		t.Fatalf("mainReply=%q", mainReply)
	}
}

func TestEmergencyFallbackCompactsLargeBacklogWithoutBreakingToolCallPairing(t *testing.T) {
	ctx := context.Background()

	// 所有摘要路由均返回 HTTP 503，模拟极端全挂场景。
	deadSummarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "all summary upstreams unavailable", http.StatusServiceUnavailable)
	}))
	defer deadSummarySrv.Close()

	routes := []store.ModelRoute{
		{
			ID:                  uuid.New(),
			Enabled:             true,
			ProviderBaseUrl:     deadSummarySrv.URL,
			Model:               "dead-summary-primary",
			ContextWindowTokens: pgtype.Int4{Int32: 200000, Valid: true},
			MaxInputTokens:      pgtype.Int4{Int32: 200000, Valid: true},
			MaxOutputTokens:     pgtype.Int4{Int32: 16000, Valid: true},
			LimitsSource:        modelclient.LimitsSourceUser,
		},
		{
			ID:                  uuid.New(),
			Enabled:             true,
			ProviderBaseUrl:     deadSummarySrv.URL,
			Model:               "dead-summary-backup",
			ContextWindowTokens: pgtype.Int4{Int32: 200000, Valid: true},
			MaxInputTokens:      pgtype.Int4{Int32: 200000, Valid: true},
			MaxOutputTokens:     pgtype.Int4{Int32: 16000, Valid: true},
			LimitsSource:        modelclient.LimitsSourceUser,
		},
	}

	summaryLLM, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), routes)
	if err != nil {
		t.Fatal(err)
	}
	summarizer, err := newADKCompactionSummarizer(summaryLLM)
	if err != nil {
		t.Fatal(err)
	}

	sessionSvc := session.InMemoryService()
	const (
		appName   = "cycledock_builtin_agent"
		userID    = "workspace-emergency"
		sessionID = "card-emergency-fallback"
	)
	created, err := sessionSvc.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}

	baseTime := time.Now().Add(-3 * time.Hour)
	appendEvent := func(idx int, author string, content *genai.Content) {
		ev := &session.Event{
			ID:           fmt.Sprintf("evt-%03d", idx),
			InvocationID: fmt.Sprintf("inv-%d", idx),
			Timestamp:    baseTime.Add(time.Duration(idx) * time.Second),
			Author:       author,
			LLMResponse: model.LLMResponse{
				Content: content,
			},
		}
		if err := sessionSvc.AppendEvent(ctx, created.Session, ev); err != nil {
			t.Fatalf("append event %d: %v", idx, err)
		}
	}

	appendEvent(0, "user", genai.NewContentFromText(
		"任务目标：修复上下文压缩超时死锁，分支名 fix/compaction-failover-and-overflow，禁止单开 PR。",
		genai.RoleUser,
	))

	// 写入 40 轮大体积 FunctionCall + FunctionResponse（超过 40,000 字符），并在保留尾部附近包含成对工具调用，
	// 验证全路由不可用时：1）触发确定性兜底摘要防雪崩；2）发送给主模型的 Contents 体积被成功截断；3）FunctionCall/FunctionResponse 配对 100% 完整。
	seq := 1
	for round := 0; round < 40; round++ {
		callID := fmt.Sprintf("call-%03d", round)
		fc := genai.NewPartFromFunctionCall("exec_command", map[string]any{
			"cmd":     fmt.Sprintf("go test ./pkg/round_%03d", round),
			"workdir": "C:\\Project\\CycleDock-worktrees\\fix-compaction-failover-and-overflow",
		})
		fc.FunctionCall.ID = callID
		thought := genai.NewPartFromText("内部隐藏推理秘密标记 SECRET_THOUGHT_DO_NOT_PERSIST")
		thought.Thought = true
		appendEvent(seq, "cycle_agent", &genai.Content{
			Role:  genai.RoleModel,
			Parts: []*genai.Part{thought, fc},
		})
		seq++

		fr := genai.NewPartFromFunctionResponse("exec_command", map[string]any{
			"artifact_ref": fmt.Sprintf("tool-result-round-%03d.json", round),
			"output":       fmt.Sprintf("round=%03d %s", round, strings.Repeat("日志载荷", 350)),
		})
		fr.FunctionResponse.ID = callID
		appendEvent(seq, "cycle_agent", &genai.Content{
			Role:  genai.RoleUser,
			Parts: []*genai.Part{fr},
		})
		seq++
	}

	agentLLM := &captureAgentLLM{}
	rootAgent, err := llmagent.New(llmagent.Config{
		Name:        "cycle_agent",
		Description: "test agent",
		Model:       agentLLM,
	})
	if err != nil {
		t.Fatal(err)
	}

	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        appName,
		Agent:          rootAgent,
		SessionService: sessionSvc,
		Compaction: &compaction.Config{
			TokenThreshold:     4000,
			EventRetentionSize: 6,
			Summarizer:         summarizer,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	turnMsg := genai.NewContentFromText("汇报当前进度并继续验证。", genai.RoleUser)
	for _, runErr := range runner.Run(ctx, userID, sessionID, turnMsg, agent.RunConfig{}) {
		if runErr != nil {
			t.Fatalf("runner failed under dead summary routes: %v", runErr)
		}
	}

	if len(agentLLM.seenContents) != 1 {
		t.Fatalf("agentLLM calls=%d, want 1", len(agentLLM.seenContents))
	}
	seen := agentLLM.seenContents[0]
	// 原会话共 81 条历史事件 + 1 条新消息 = 82 条；兜底压缩后应缩减至 1 条兜底摘要 + 约 6 条保留事件 + 1 条新消息 <= 12 条。
	if len(seen) > 12 {
		t.Fatalf("expected emergency fallback to compact prompt, got %d contents (uncompacted avalanche!)", len(seen))
	}

	summaryText := textFromADKContent(seen[0])
	if !strings.Contains(summaryText, "fix/compaction-failover-and-overflow") {
		t.Fatalf("emergency fallback lost initial user objective: %q", summaryText)
	}
	if !strings.Contains(summaryText, "tool-result-round-") {
		t.Fatalf("emergency fallback should preserve externalized artifact_ref pointers: %q", summaryText)
	}
	if strings.Contains(summaryText, "SECRET_THOUGHT_DO_NOT_PERSIST") {
		t.Fatalf("emergency fallback must never persist Thought parts: %q", summaryText)
	}

	// 严格校验发送给主模型的全部 Contents 中每一对 FunctionCall 与 FunctionResponse 均完整成对、无孤立项。
	openCalls := make(map[string]string)
	for idx, content := range seen {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionCall != nil {
				openCalls[part.FunctionCall.ID] = part.FunctionCall.Name
			}
			if part.FunctionResponse != nil {
				wantName, exists := openCalls[part.FunctionResponse.ID]
				if !exists {
					t.Fatalf("content[%d] has orphan FunctionResponse id=%q name=%q without preceding FunctionCall", idx, part.FunctionResponse.ID, part.FunctionResponse.Name)
				}
				if wantName != part.FunctionResponse.Name {
					t.Fatalf("FunctionResponse id=%q name=%q, want %q", part.FunctionResponse.ID, part.FunctionResponse.Name, wantName)
				}
				delete(openCalls, part.FunctionResponse.ID)
			}
		}
	}
	if len(openCalls) != 0 {
		t.Fatalf("unmatched FunctionCalls left in compacted prompt: %+v", openCalls)
	}
}

func TestChunkedSummarizer_HandlesMultimodalImageAndToolArtifactEventsSafely(t *testing.T) {
	ctx := context.Background()
	summaryLLM := &recordingChunkSummarizerLLM{}
	summarizer, err := newADKCompactionSummarizer(summaryLLM)
	if err != nil {
		t.Fatalf("newADKCompactionSummarizer failed: %v", err)
	}

	base := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	largeImageBytes := make([]byte, 200*1024)
	copy(largeImageBytes, []byte{0x89, 0x50, 0x4e, 0x47})

	events := []*session.Event{
		{
			ID:           "ev-user-1",
			InvocationID: "inv-1",
			Author:       "user",
			Timestamp:    base,
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleUser,
					Parts: []*genai.Part{
						genai.NewPartFromText("请查看截图"),
						genai.NewPartFromBytes(largeImageBytes, "image/png"),
					},
				},
			},
		},
		{
			ID:           "ev-tool-call",
			InvocationID: "inv-1",
			Author:       "cycle_agent",
			Timestamp:    base.Add(time.Second),
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleModel,
					Parts: []*genai.Part{
						func() *genai.Part {
							p := genai.NewPartFromFunctionCall("view_image", map[string]any{"path": "shot.png"})
							p.FunctionCall.ID = "call-view-1"
							return p
						}(),
					},
				},
			},
		},
		{
			ID:           "ev-tool-resp",
			InvocationID: "inv-1",
			Author:       "user",
			Timestamp:    base.Add(2 * time.Second),
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleUser,
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								ID:   "call-view-1",
								Name: "view_image",
								Response: map[string]any{
									"status": "ok",
									imageArtifactsFieldKey: []map[string]any{{
										imageArtifactRefKey:     "tool-image-1.png",
										imageArtifactVersionKey: int64(1),
										imageArtifactMIMEKey:    "image/png",
										imageArtifactBytesKey:   len(largeImageBytes),
									}},
								},
							},
						},
					},
				},
			},
		},
	}

	res, err := summarizer.SummarizeEvents(ctx, events)
	if err != nil {
		t.Fatalf("SummarizeEvents failed on multimodal image events: %v", err)
	}
	if res.Content == nil || len(res.Content.Parts) == 0 {
		t.Fatalf("expected non-empty summary content, got %+v", res)
	}
	if len(summaryLLM.prompts) != 1 {
		t.Fatalf("summaryLLM calls=%d, want 1", len(summaryLLM.prompts))
	}
	prompt := summaryLLM.prompts[0]
	if !strings.Contains(prompt, "tool-image-1.png") {
		t.Fatalf("summary prompt should preserve image artifact_ref metadata: %s", prompt)
	}
	if len(prompt) > 20000 {
		t.Fatalf("summary prompt unexpectedly bloated by raw image bytes: len=%d", len(prompt))
	}
}
