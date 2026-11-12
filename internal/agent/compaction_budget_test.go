package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
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
	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestComputeRouteCompactionBudget(t *testing.T) {
	policy := DefaultCompactionBudgetPolicy()

	t.Run("gemini-2.5-pro 大窗口模型动态计算", func(t *testing.T) {
		limits, ok := modelclient.LookupRegistryModelLimits("gemini-2.5-pro", "")
		if !ok {
			t.Fatal("expected gemini-2.5-pro in registry")
		}
		budget := ComputeRouteCompactionBudget(limits, policy)
		if budget.UnknownCapacity {
			t.Fatal("expected known capacity")
		}
		// 1,048,576 - 16,384 (default reserved output) = 1,032,192
		if budget.EffectiveInputLimit != 1032192 {
			t.Fatalf("EffectiveInputLimit=%d, want 1032192", budget.EffectiveInputLimit)
		}
		// 1,032,192 * 15% = 154,828
		if budget.SafetyReserve != 154828 {
			t.Fatalf("SafetyReserve=%d, want 154828", budget.SafetyReserve)
		}
		// 1,032,192 - 154,828 = 877,364
		if budget.CompactAtTokens != 877364 {
			t.Fatalf("CompactAtTokens=%d, want 877364", budget.CompactAtTokens)
		}
	})

	t.Run("gpt-4o 128K 窗口动态计算", func(t *testing.T) {
		limits, ok := modelclient.LookupRegistryModelLimits("gpt-4o", "")
		if !ok {
			t.Fatal("expected gpt-4o in registry")
		}
		budget := ComputeRouteCompactionBudget(limits, policy)
		// 128,000 - 16,384 = 111,616; 15% = 16,742; compactAt = 94,874
		if budget.EffectiveInputLimit != 111616 || budget.SafetyReserve != 16742 || budget.CompactAtTokens != 94874 {
			t.Fatalf("unexpected gpt-4o budget=%+v", budget)
		}
	})

	t.Run("用户手动覆盖与独立 MaxInputTokens", func(t *testing.T) {
		route := store.ModelRoute{
			ID:                  uuid.New(),
			Model:               "custom-proxy-model",
			ContextWindowTokens: pgtype.Int4{Int32: 64000, Valid: true},
			MaxInputTokens:      pgtype.Int4{Int32: 48000, Valid: true},
			MaxOutputTokens:     pgtype.Int4{Int32: 8192, Valid: true},
			LimitsSource:        modelclient.LimitsSourceUser,
		}
		limits := ResolveRouteModelLimits(route, nil)
		budget := ComputeRouteCompactionBudget(limits, policy)
		// min(48000, 64000-8192=55808) = 48000; safetyReserve = max(48000*15%=7200, 16000) = 16000; compactAt = 32000
		if budget.EffectiveInputLimit != 48000 || budget.SafetyReserve != 16000 || budget.CompactAtTokens != 32000 {
			t.Fatalf("unexpected user override budget=%+v", budget)
		}
	})

	t.Run("小窗口模型自动退回比例预留避免负阈值", func(t *testing.T) {
		limits := modelclient.ModelLimits{
			ContextWindowTokens: 16000,
			MaxOutputTokens:     4000,
			Source:              modelclient.LimitsSourceUser,
		}
		budget := ComputeRouteCompactionBudget(limits, policy)
		// 16000 - 4000 = 12000; 12000 - 16000 < 8000 -> fallback reserve 20% = 2400 -> compactAt = 9600
		if budget.EffectiveInputLimit != 12000 || budget.CompactAtTokens != 9600 {
			t.Fatalf("unexpected small window budget=%+v", budget)
		}
	})

	t.Run("未知容量模型使用独立保守策略且不伪造容量", func(t *testing.T) {
		route := store.ModelRoute{
			ID:           uuid.New(),
			Model:        "gemini-3.8-flash-high-unknown",
			LimitsSource: modelclient.LimitsSourceUnknown,
		}
		limits := ResolveRouteModelLimits(route, nil)
		budget := ComputeRouteCompactionBudget(limits, policy)
		if !budget.UnknownCapacity {
			t.Fatalf("expected UnknownCapacity=true, got %+v", budget)
		}
		if budget.EffectiveInputLimit != 0 || budget.Limits.ContextWindowTokens != 0 {
			t.Fatalf("unknown model must not fabricate capacity: %+v", budget)
		}
		if budget.CompactAtTokens != defaultCompactionTokenThreshold {
			t.Fatalf("CompactAtTokens=%d, want %d", budget.CompactAtTokens, defaultCompactionTokenThreshold)
		}
	})
}

func TestFailoverSkipsSmallAndUnknownBackupRoutesForOversizedPrompt(t *testing.T) {
	var primaryCalls, smallBackupCalls, unknownBackupCalls, largeBackupCalls atomic.Int32

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		http.Error(w, "service temporarily overloaded", http.StatusServiceUnavailable)
	}))
	defer primaryServer.Close()

	smallBackupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		smallBackupCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "should never hit small backup", nil)
	}))
	defer smallBackupServer.Close()

	unknownBackupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		unknownBackupCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "should never hit unknown backup on oversized prompt", nil)
	}))
	defer unknownBackupServer.Close()

	largeBackupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		largeBackupCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "large backup route handled 40k prompt safely", nil)
	}))
	defer largeBackupServer.Close()

	primaryID := uuid.New()
	smallID := uuid.New()
	unknownID := uuid.New()
	largeID := uuid.New()

	routes := []store.ModelRoute{
		{
			ID:              primaryID,
			Enabled:         true,
			ProviderBaseUrl: primaryServer.URL,
			Model:           "gpt-4o", // 128K registry window
		},
		{
			ID:                  smallID,
			Enabled:             true,
			ProviderBaseUrl:     smallBackupServer.URL,
			Model:               "small-backup-16k",
			ContextWindowTokens: pgtype.Int4{Int32: 16000, Valid: true},
			MaxInputTokens:      pgtype.Int4{Int32: 12000, Valid: true},
			LimitsSource:        modelclient.LimitsSourceUser,
		},
		{
			ID:              unknownID,
			Enabled:         true,
			ProviderBaseUrl: unknownBackupServer.URL,
			Model:           "custom-relay-alias-unknown",
			LimitsSource:    modelclient.LimitsSourceUnknown,
		},
		{
			ID:              largeID,
			Enabled:         true,
			ProviderBaseUrl: largeBackupServer.URL,
			Model:           "claude-sonnet-4-5", // 200K registry window
		},
	}

	llm, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), routes)
	if err != nil {
		t.Fatal(err)
	}

	// 构造约 40,000 Token（约 160,000 ASCII 字符）的大 Prompt。
	oversizedPrompt := strings.Repeat("A", 160000)

	var finalText string
	for resp, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText(oversizedPrompt, genai.RoleUser)},
	}, false) {
		if err != nil {
			t.Fatalf("expected failover to large backup route to succeed, got error: %v", err)
		}
		if !resp.Partial {
			finalText = textFromADKContent(resp.Content)
		}
	}

	if primaryCalls.Load() != 2 {
		t.Fatalf("primaryCalls=%d, want 2 (initial + 1 retry)", primaryCalls.Load())
	}
	if smallBackupCalls.Load() != 0 {
		t.Fatalf("smallBackupCalls=%d, want 0 (must skip 16K backup route for 40K prompt)", smallBackupCalls.Load())
	}
	if unknownBackupCalls.Load() != 0 {
		t.Fatalf("unknownBackupCalls=%d, want 0 (must skip unknown capacity backup route for 40K prompt)", unknownBackupCalls.Load())
	}
	if largeBackupCalls.Load() != 1 {
		t.Fatalf("largeBackupCalls=%d, want 1", largeBackupCalls.Load())
	}
	if finalText != "large backup route handled 40k prompt safely" {
		t.Fatalf("finalText=%q", finalText)
	}
	activeRoute, ok := llm.ActiveRoute()
	if !ok || activeRoute.ID != largeID {
		t.Fatalf("ActiveRoute=%+v, want large backup route %s", activeRoute, largeID)
	}

	// 验证当没有足够容量的大窗口备用路由时，返回可诊断错误且映射为明确的公开错误文案。
	insufficientLLM, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), routes[:3])
	if err != nil {
		t.Fatal(err)
	}
	var capacityErr error
	for _, err := range insufficientLLM.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText(oversizedPrompt, genai.RoleUser)},
	}, false) {
		if err != nil {
			capacityErr = err
			break
		}
	}
	if !errors.Is(capacityErr, ErrRouteContextCapacityUnknown) && !errors.Is(capacityErr, ErrRouteContextCapacityExceeded) {
		t.Fatalf("expected capacity error, got: %v", capacityErr)
	}
	if gotPublic := publicRunError(capacityErr); gotPublic != "Model context window is insufficient for the current session size." {
		t.Fatalf("publicRunError=%q", gotPublic)
	}
}

func TestContextTooLargeErrorFailsOverWithoutRetryingSameRoute(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int32

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		http.Error(w, `{"error":{"message":"This model's maximum context length is 32768 tokens. However, your messages resulted in 40120 tokens."}}`, http.StatusBadRequest)
	}))
	defer primaryServer.Close()

	backupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backupCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "backup route handled context overflow", nil)
	}))
	defer backupServer.Close()

	llm, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), []store.ModelRoute{
		{ID: uuid.New(), Enabled: true, ProviderBaseUrl: primaryServer.URL, Model: "custom-primary"},
		{ID: uuid.New(), Enabled: true, ProviderBaseUrl: backupServer.URL, Model: "gemini-2.5-pro"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var answer string
	for resp, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("test context overflow failover", genai.RoleUser)},
	}, false) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !resp.Partial {
			answer = textFromADKContent(resp.Content)
		}
	}
	if primaryCalls.Load() != 1 {
		t.Fatalf("primaryCalls=%d, want 1 (must not retry context overflow on the same route)", primaryCalls.Load())
	}
	if backupCalls.Load() != 1 || answer != "backup route handled context overflow" {
		t.Fatalf("backupCalls=%d answer=%q", backupCalls.Load(), answer)
	}
}

type rollingCheckpointSummarizerLLM struct {
	compactionCount int
	lastPrompt      string
	lastSummary     string
}

func (m *rollingCheckpointSummarizerLLM) Name() string {
	return "rolling-checkpoint-summarizer"
}

func (m *rollingCheckpointSummarizerLLM) GenerateContent(
	_ context.Context,
	req *model.LLMRequest,
	_ bool,
) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		var builder strings.Builder
		for _, content := range req.Contents {
			if content == nil {
				continue
			}
			for _, part := range content.Parts {
				if part != nil && part.Text != "" {
					builder.WriteString(part.Text)
				}
			}
		}
		m.compactionCount++
		m.lastPrompt = builder.String()
		m.lastSummary = fmt.Sprintf(`Conversation Language: 中文

## Objective
实现 CycleDock 动态上下文压缩预算与长任务恢复验证（验收目标：连续 100+ 次压缩不丢失核心状态）。

## Requirements
- 保留 Google ADK Go v2 与既有 Session-first 架构边界。
- 严格区分 Completed 与 Active / Blocked。

## Decisions
- 采用 inputLimit = min(MaxInputTokens, ContextWindowTokens - reservedOutput) 与 15%%/16K 安全预留。
- 备用路由切换前校验 EffectiveInputLimit，拒绝盲目向小窗口发送超大 Prompt。

## Work State
### Completed
- 已完成第 1 至 %d 轮增量分析与步骤验证（证据：internal/agent/compaction_budget.go）。

### Active
- 正在推进第 %d 轮后续验证。

### Blocked
- 无

## Next Move
执行第 %d 轮操作：继续推进 internal/agent/compaction_budget_test.go 中的回归步骤，不重复读取已完成分析的文件。

## Relevant Files
- internal/agent/compaction_budget.go: 动态上下文预算与路由切换容量校验。
- internal/agent/adk_runner.go: Runner 压缩阈值注入。

## Important Context
- 累计压缩次数：%d。`, m.compactionCount, m.compactionCount+1, m.compactionCount+1, m.compactionCount)

		yield(&model.LLMResponse{
			Content:      genai.NewContentFromText(m.lastSummary, genai.RoleModel),
			FinishReason: genai.FinishReasonStop,
			TurnComplete: true,
		}, nil)
	}
}

type stepEchoAgentLLM struct {
	round int
}

func (m *stepEchoAgentLLM) Name() string {
	return "step-echo-agent-llm"
}

func (m *stepEchoAgentLLM) GenerateContent(
	_ context.Context,
	_ *model.LLMRequest,
	_ bool,
) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.round++
		yield(&model.LLMResponse{
			Content: genai.NewContentFromText(
				fmt.Sprintf("已完成步骤 %d 的验证，准备进入步骤 %d。%s", m.round, m.round+1, strings.Repeat("y", 180)),
				genai.RoleModel,
			),
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     160,
				CandidatesTokenCount: 50,
				TotalTokenCount:      210,
			},
			FinishReason: genai.FinishReasonStop,
			TurnComplete: true,
		}, nil)
	}
}

func TestContinuous100PlusCompactionsPreserveObjectiveDecisionsNextMove(t *testing.T) {
	_, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	sessionSvc := agentsession.NewPostgresService(pool)
	appName, userID, sessionID := agentsession.Identity(uuid.New(), uuid.New())

	if _, err := sessionSvc.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}); err != nil {
		t.Fatal(err)
	}

	summarizerLLM := &rollingCheckpointSummarizerLLM{}
	summarizer, err := compaction.NewLLMSummarizer(compaction.LLMSummarizerConfig{
		Model:               summarizerLLM,
		PromptTemplate:      compactionPromptTemplate,
		MaxToolContentChars: 4000,
		MaxTranscriptChars:  120000,
		Timeout:             agentconfig.DefaultModelRequestTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}

	rootAgent, err := llmagent.New(llmagent.Config{
		Name:            "cycle_agent",
		Description:     "Continuity test agent",
		Model:           &stepEchoAgentLLM{},
		IncludeContents: llmagent.IncludeContentsDefault,
	})
	if err != nil {
		t.Fatal(err)
	}

	adkRunner, err := adkrunner.New(adkrunner.Config{
		AppName:        appName,
		Agent:          rootAgent,
		SessionService: sessionSvc,
		Compaction: &compaction.Config{
			TokenThreshold:     40,
			EventRetentionSize: 1,
			Summarizer:         summarizer,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	const totalRounds = 105
	for round := 1; round <= totalRounds; round++ {
		msg := genai.NewContentFromText(
			fmt.Sprintf("第 %d 轮指令：请继续执行步骤 %d 并记录详细进展 %s", round, round, strings.Repeat("x", 180)),
			genai.RoleUser,
		)
		for _, runErr := range adkRunner.Run(ctx, userID, sessionID, msg, agent.RunConfig{}) {
			if runErr != nil {
				t.Fatalf("round %d ADK run error: %v", round, runErr)
			}
		}
	}

	if summarizerLLM.compactionCount < 100 {
		t.Fatalf("compactionCount=%d, want >= 100", summarizerLLM.compactionCount)
	}

	// 验证第 100+ 次压缩时，最新摘要完整保留目标、决策和下一步动作。
	for _, section := range []string{
		"## Objective",
		"## Requirements",
		"## Decisions",
		"## Work State",
		"### Completed",
		"### Active",
		"### Blocked",
		"## Next Move",
		"## Relevant Files",
		"## Important Context",
		fmt.Sprintf("执行第 %d 轮操作", summarizerLLM.compactionCount+1),
	} {
		if !strings.Contains(summarizerLLM.lastSummary, section) {
			t.Fatalf("100+ 次压缩后最新摘要丢失关键内容 %q:\n%s", section, summarizerLLM.lastSummary)
		}
	}

	reloaded, err := sessionSvc.Get(ctx, &session.GetRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}

	var latestCompactionSummary string
	allEvents := reloaded.Session.Events()
	for i := 0; i < allEvents.Len(); i++ {
		ev := allEvents.At(i)
		if ev.Actions.Compaction != nil && ev.Actions.Compaction.CompactedContent != nil {
			latestCompactionSummary = textFromADKContent(ev.Actions.Compaction.CompactedContent)
		}
	}
	if latestCompactionSummary != summarizerLLM.lastSummary {
		t.Fatalf("persisted latest compaction summary mismatch:\ngot:  %s\nwant: %s", latestCompactionSummary, summarizerLLM.lastSummary)
	}
}

func TestRepeatedTechnicalFailuresPreserveEvidenceAndNeverMarkCompleted(t *testing.T) {
	llm := &captureSummarizerLLM{
		reply: `Conversation Language: 中文

## Objective
修复 PostgreSQL 迁移脚本 000025_model_route_limits.sql 的约束冲突并完成集成测试。

## Requirements
- 不能破坏既有 workspace_model_routes 数据。

## Decisions
- 保持 limits_source 枚举约束为 ('user', 'provider', 'registry', 'unknown')。

## Work State
### Completed
- 已定位失败测试为 internal/store/baseline_migration_integration_test.go:81。

### Active
- 正在排查连续两次 go test 失败原因。

### Blocked
- 第 1 次失败：ERROR: new row for relation "workspace_model_routes" violates check constraint "workspace_model_routes_limits_source_check" (SQLSTATE 23514)，完整日志见 artifact://workspace/card/fail-log-1 (version=1)。
- 第 2 次失败：baseline_migration_integration_test.go:81: migration version = 25, want 24，完整日志见 artifact://workspace/card/fail-log-2 (version=1)。

## Next Move
修改 db/query/agent.sql 中 CreateWorkspaceModelRoute 的 COALESCE 默认值并更新 baseline_migration_integration_test.go 的版本号断言，然后重新运行 go test。

## Relevant Files
- db/query/agent.sql: CreateWorkspaceModelRoute 插入语句。
- internal/store/baseline_migration_integration_test.go: 第 81 行迁移版本断言。

## Important Context
- 外置日志引用：artifact://workspace/card/fail-log-1、artifact://workspace/card/fail-log-2。`,
	}

	summarizer, err := compaction.NewLLMSummarizer(compaction.LLMSummarizerConfig{
		Model:               llm,
		PromptTemplate:      compactionPromptTemplate,
		MaxToolContentChars: 4000,
		MaxTranscriptChars:  120000,
		Timeout:             agentconfig.DefaultModelRequestTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	events := []*session.Event{
		{
			ID:        "fail-ev-1",
			Author:    "user",
			Timestamp: now,
			LLMResponse: model.LLMResponse{
				Content: genai.NewContentFromText("运行 store 集成测试并修复所有报错。", genai.RoleUser),
			},
		},
		{
			ID:        "fail-ev-2",
			Author:    "cycle_agent",
			Timestamp: now.Add(time.Second),
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleUser,
					Parts: []*genai.Part{
						genai.NewPartFromFunctionResponse("call_mcp_tool", map[string]any{
							"status":       "failed",
							"artifact_ref": "artifact://workspace/card/fail-log-1",
							"version":      1,
							"preview":      `ERROR: new row for relation "workspace_model_routes" violates check constraint "workspace_model_routes_limits_source_check" (SQLSTATE 23514)` + "\n" + strings.Repeat("verbose stack line\n", 300),
						}),
					},
				},
			},
		},
		{
			ID:        "fail-ev-3",
			Author:    "cycle_agent",
			Timestamp: now.Add(2 * time.Second),
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleUser,
					Parts: []*genai.Part{
						genai.NewPartFromFunctionResponse("call_mcp_tool", map[string]any{
							"status":       "failed",
							"artifact_ref": "artifact://workspace/card/fail-log-2",
							"version":      1,
							"preview":      `baseline_migration_integration_test.go:81: migration version = 25, want 24` + "\n" + strings.Repeat("verbose test output\n", 300),
						}),
					},
				},
			},
		},
	}

	compactedEvent, err := summarizer.SummarizeEvents(context.Background(), events)
	if err != nil {
		t.Fatal(err)
	}

	// 验证压缩输入保留了两次失败的精确错误头与两个 artifact_ref。
	for _, needle := range []string{
		"workspace_model_routes_limits_source_check",
		"artifact://workspace/card/fail-log-1",
		"baseline_migration_integration_test.go:81: migration version = 25, want 24",
		"artifact://workspace/card/fail-log-2",
		`Only operations that were actually executed and confirmed by tool responses or verifiable outputs may be listed under "### Completed"`,
		`Preserve raw failure evidence when repeated technical failures occur; never summarize a failed command, build, or test as succeeded.`,
	} {
		if !strings.Contains(llm.lastPrompt, needle) {
			t.Fatalf("summarizer prompt missing failure evidence %q", needle)
		}
	}

	summaryText := textFromADKContent(compactedEvent.Content)
	completedIdx := strings.Index(summaryText, "### Completed")
	activeIdx := strings.Index(summaryText, "### Active")
	blockedIdx := strings.Index(summaryText, "### Blocked")
	if completedIdx < 0 || activeIdx < 0 || blockedIdx < 0 {
		t.Fatalf("missing Work State subsections in summary:\n%s", summaryText)
	}
	completedBlock := summaryText[completedIdx:activeIdx]
	blockedBlock := summaryText[blockedIdx:]
	if strings.Contains(completedBlock, "已完成修复") || strings.Contains(completedBlock, "测试通过") {
		t.Fatalf("failed operations must not be summarized as completed:\n%s", completedBlock)
	}
	if !strings.Contains(blockedBlock, "artifact://workspace/card/fail-log-1") ||
		!strings.Contains(blockedBlock, "artifact://workspace/card/fail-log-2") {
		t.Fatalf("Blocked section must retain failure artifact_ref evidence:\n%s", blockedBlock)
	}
}

func TestCancelledOrSupersededAttemptLateCompactionCannotPolluteSession(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	workspace, err := dataStore.CreateWorkspace(ctx, "Late Compaction Guard")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Runtime", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(
		ctx, workspace.ID, project.ID, human.ID,
		"Guard late compaction", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(
		ctx, workspace.ID, testRunnerSettings("http://127.0.0.1:9", "guard-model"),
	)
	if err != nil {
		t.Fatal(err)
	}

	turn1, err := dataStore.CreateHumanTurn(
		ctx, workspace.ID, card.ID, human.ID, "第一轮任务", "guard-turn-1", domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	claim1, err := dataStore.ClaimNextTurnAttempt(
		ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim1.Turn.ID != turn1.Turn.ID {
		t.Fatalf("claimed=%s want=%s", claim1.Turn.ID, turn1.Turn.ID)
	}

	sessionSvc := agentsession.NewPostgresService(pool)
	appName, userID, sessionID := agentsession.Identity(workspace.ID, card.ID)
	attempt1Ctx := agentsession.WithAttempt(ctx, claim1.Attempt.ID)

	created, err := sessionSvc.Create(attempt1Ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := created.Session

	baseTime := time.Now().UTC().Add(-time.Minute)
	ev1 := &session.Event{
		ID:           "attempt1-ev-1",
		InvocationID: "attempt1-inv",
		Author:       "user",
		Timestamp:    baseTime,
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromText("第一轮任务输入", genai.RoleUser),
		},
	}
	if err := sessionSvc.AppendEvent(attempt1Ctx, sess, ev1); err != nil {
		t.Fatal(err)
	}

	// 用户发起 interrupt Turn 替代 Attempt 1。
	if _, err := dataStore.CreateHumanTurn(
		ctx, workspace.ID, card.ID, human.ID, "中断并切换新方向", "guard-turn-2", domain.DispatchModeInterrupt,
	); err != nil {
		t.Fatal(err)
	}

	// 此时 Attempt 1 已处于 superseded 终态，其迟到的 Compaction 摘要事件尝试写入 Session 必须被拒绝。
	lateCompactionEvent := &session.Event{
		ID:        "late-compaction-ev",
		Author:    "user",
		Timestamp: time.Now().UTC(),
		Actions: session.EventActions{
			Compaction: &session.EventCompaction{
				StartTimestamp:   baseTime,
				EndTimestamp:     baseTime,
				CompactedContent: genai.NewContentFromText("## Objective\n被替代旧 Attempt 的迟到摘要", genai.RoleModel),
			},
		},
	}
	err = sessionSvc.AppendEvent(attempt1Ctx, sess, lateCompactionEvent)
	if !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("expected domain.ErrAttemptNotCurrent for superseded attempt late compaction, got: %v", err)
	}

	reloaded, err := sessionSvc.Get(ctx, &session.GetRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < reloaded.Session.Events().Len(); i++ {
		ev := reloaded.Session.Events().At(i)
		if ev.Actions.Compaction != nil {
			t.Fatalf("superseded attempt late compaction polluted session: %+v", ev.Actions.Compaction)
		}
	}
}

func TestCompactionSummarizerFailurePreservesOriginalSessionHistory(t *testing.T) {
	dataStore, pool := newAgentIntegrationStore(t)
	ctx := context.Background()

	var summaryFails atomic.Bool
	summaryFails.Store(true)

	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body modelRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Summarizer 请求不带 Tools。
		if len(body.Tools) == 0 && summaryFails.Load() {
			http.Error(w, "summarizer backend failure", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "已正常处理本轮请求並保留完整历史。", nil)
	}))
	defer modelHTTP.Close()

	workspace, err := dataStore.CreateWorkspace(ctx, "Summarizer Failure Recovery")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Runtime", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(
		ctx, workspace.ID, project.ID, human.ID,
		"Summarizer failure safety", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := dataStore.SaveAgentSettings(
		ctx, workspace.ID, testRunnerSettings(modelHTTP.URL, "recovery-model"),
	)
	if err != nil {
		t.Fatal(err)
	}

	sessionSvc := agentsession.NewPostgresService(pool)
	runner := NewRunner(dataStore, modelclient.New(), nil, RunnerConfig{
		SessionService:    sessionSvc,
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     time.Minute,
	})
	runner.compactionTokenThreshold = 20
	runner.compactionEventRetention = 1

	turn1, err := dataStore.CreateHumanTurn(
		ctx, workspace.ID, card.ID, human.ID,
		"第一轮關鍵信息：数据库迁移版本号是 000025_model_route_limits.sql。",
		"sum-fail-turn-1",
		domain.DispatchModeEnqueue,
	)
	if err != nil {
		t.Fatal(err)
	}
	claim1, err := dataStore.ClaimNextTurnAttempt(
		ctx, workspace.ID, settings.SourceID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim1.Turn.ID != turn1.Turn.ID {
		t.Fatalf("claimed=%s want=%s", claim1.Turn.ID, turn1.Turn.ID)
	}
	// 即便压缩模型返回 500，原始 Session History 也不得损坏。
	_ = runner.RunAttempt(ctx, *claim1)

	appName, userID, sessionID := agentsession.Identity(workspace.ID, card.ID)
	gotSession, err := sessionSvc.Get(ctx, &session.GetRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.Session.Events().Len() < 2 {
		t.Fatalf("expected original user and model events to be preserved in PostgreSQL, got %d events", gotSession.Session.Events().Len())
	}

	foundOriginalFact := false
	for i := 0; i < gotSession.Session.Events().Len(); i++ {
		ev := gotSession.Session.Events().At(i)
		if strings.Contains(textFromADKContent(ev.Content), "000025_model_route_limits.sql") {
			foundOriginalFact = true
		}
		if ev.Actions.Compaction != nil {
			t.Fatalf("failed summarizer must not write corrupted compaction event: %+v", ev.Actions.Compaction)
		}
	}
	if !foundOriginalFact {
		t.Fatal("original session history fact was lost after summarizer failure")
	}
}
