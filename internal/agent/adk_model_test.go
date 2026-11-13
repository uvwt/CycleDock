package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestADKRouteModelFallsBackAfterHTTP402(t *testing.T) {
	var primaryCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		http.Error(w, "subscription quota exhausted", http.StatusPaymentRequired)
	}))
	defer primary.Close()

	var backupCalls atomic.Int32
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backupCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		writeModelResponse(t, w, "backup route answered", nil)
	}))
	defer backup.Close()

	llm, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), []store.ModelRoute{
		{Enabled: true, ProviderBaseUrl: primary.URL, Model: "primary"},
		{Enabled: true, ProviderBaseUrl: backup.URL, Model: "backup"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var answer string
	for response, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("你好", genai.RoleUser)},
	}, true) {
		if err != nil {
			t.Fatal(err)
		}
		if response.Partial {
			continue
		}
		if response.Content != nil {
			for _, part := range response.Content.Parts {
				answer += part.Text
			}
		}
	}

	if primaryCalls.Load() != 1 || backupCalls.Load() != 1 {
		t.Fatalf("route calls: primary=%d backup=%d, want 1/1", primaryCalls.Load(), backupCalls.Load())
	}
	if answer != "backup route answered" {
		t.Fatalf("answer=%q, want backup route answer", answer)
	}
}

// 通过真实 HTTP 模型边界验证无进展死循环熔断与长任务正常推进，不人为截断工具调用预算。
func TestADKRouteModelToolLoopLimits(t *testing.T) {
	for _, test := range []struct {
		name         string
		stableArgs   bool
		stableResult bool
		finishAt     int
		wantCalls    int
		wantErr      error
	}{
		{name: "changing arguments can continue beyond 64 rounds", stableResult: true, finishAt: 71, wantCalls: 71},
		{name: "polling with changing results can continue beyond 64 rounds", stableArgs: true, finishAt: 71, wantCalls: 71},
		{name: "unchanged calls and results stop early", stableArgs: true, stableResult: true, wantCalls: 8, wantErr: errRunawayToolLoop},
		{name: "long tasks continue beyond former 256 limit without budget error", finishAt: 260, wantCalls: 260},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", test.name, stream), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					call := int(calls.Add(1))
					var request struct {
						Messages []modelclient.Message `json:"messages"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						http.Error(w, "invalid request", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if call == test.finishAt {
						writeModelResponse(t, w, "本轮完成，已说明验证情况。", nil)
						return
					}
					argument := call
					if test.stableArgs {
						argument = 1
					}
					writeModelResponse(t, w, "", []modelclient.ToolCall{{
						ID: fmt.Sprintf("call-%d", call), Type: "function",
						Function: modelclient.ToolFunction{Name: "inspect", Arguments: fmt.Sprintf(`{"item":%d}`, argument)},
					}})
				}))
				defer server.Close()
				llm, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), []store.ModelRoute{
					{Enabled: true, ProviderBaseUrl: server.URL, Model: "test"},
				})
				if err != nil {
					t.Fatal(err)
				}
				req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("执行任务", genai.RoleUser)}}
				var runErr error
				finished := false
				for round := 0; round <= 300; round++ {
					var content *genai.Content
					for response, err := range llm.GenerateContent(context.Background(), req, stream) {
						if err != nil {
							runErr = err
							break
						}
						if !response.Partial {
							content = response.Content
						}
					}
					if runErr != nil {
						break
					}
					if content == nil {
						t.Fatal("model returned no content")
					}
					req.Contents = append(req.Contents, content)
					var responses []*genai.Part
					for _, part := range content.Parts {
						if part.FunctionCall == nil {
							continue
						}
						result := int(calls.Load())
						if test.stableResult {
							result = 1
						}
						responses = append(responses, &genai.Part{FunctionResponse: &genai.FunctionResponse{
							ID: part.FunctionCall.ID, Name: part.FunctionCall.Name, Response: map[string]any{"result": result},
						}})
					}
					if len(responses) == 0 {
						finished = true
						break
					}
					req.Contents = append(req.Contents, genai.NewContentFromParts(responses, genai.RoleUser))
				}
				if !errors.Is(runErr, test.wantErr) {
					t.Fatalf("error=%v, want %v", runErr, test.wantErr)
				}
				if int(calls.Load()) != test.wantCalls {
					t.Fatalf("provider calls=%d, want %d", calls.Load(), test.wantCalls)
				}
				if finished != (test.wantErr == nil) {
					t.Fatalf("finished=%v, error=%v", finished, runErr)
				}
				if test.wantErr != nil && publicRunError(runErr) == publicRunError(errors.New("generic")) {
					t.Fatal("loop failure must have a specific public message")
				}
			})
		}
	}
}

func TestADKRouteModelRetriesCurrentRouteBeforeFailover(t *testing.T) {
	for _, test := range []struct {
		name        string
		failure     string
		status      int
		persistent  bool
		backup      bool
		wantPrimary int32
		wantBackup  int32
		wantError   bool
	}{
		{name: "malformed then success", failure: "{invalid", wantPrimary: 2},
		{name: "empty content then success", failure: `{"choices":[{"message":{"content":""}}]}`, wantPrimary: 2},
		{name: "empty then success", failure: `{"choices":[]}`, wantPrimary: 2},
		{name: "interrupted stream then success", failure: "stream", wantPrimary: 2},
		{name: "server error then success", status: 503, wantPrimary: 2},
		{name: "rate limit then success", status: 429, wantPrimary: 2},
		{name: "request timeout then success", status: 408, wantPrimary: 2},
		{name: "one route fails twice", failure: "{invalid", persistent: true, wantPrimary: 2, wantError: true},
		{name: "both requests fail before backup", status: 503, persistent: true, backup: true, wantPrimary: 2, wantBackup: 1},
		{name: "bad request stops immediately", status: 400, persistent: true, backup: true, wantPrimary: 1, wantError: true},
		{name: "invalid authentication skips to backup", status: 401, persistent: true, backup: true, wantPrimary: 1, wantBackup: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var primaryCalls, backupCalls atomic.Int32
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := primaryCalls.Add(1)
				var input struct {
					Messages []modelclient.Message `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if len(input.Messages) != 1 || input.Messages[0].Content != "retry me" {
					t.Errorf("retry changed input: %+v", input.Messages)
				}
				if call == 1 || test.persistent {
					if test.status != 0 {
						http.Error(w, "provider unavailable", test.status)
						return
					}
					if test.failure == "stream" {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintln(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"draft","function":{"name":"reply_message","arguments":"{\"content\":\"draft"}}]}}]}`)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, test.failure)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				writeModelResponse(t, w, "current route recovered", nil)
			}))
			defer primary.Close()
			backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				backupCalls.Add(1)
				if primaryCalls.Load() != test.wantPrimary {
					t.Errorf("backup called before primary exhausted retries")
				}
				w.Header().Set("Content-Type", "application/json")
				writeModelResponse(t, w, "backup recovered", nil)
			}))
			defer backup.Close()
			routes := []store.ModelRoute{{Enabled: true, ProviderBaseUrl: primary.URL, Model: "primary"}}
			if test.backup {
				routes = append(routes, store.ModelRoute{Enabled: true, ProviderBaseUrl: backup.URL, Model: "backup"})
			}
			llm, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), routes)
			if err != nil {
				t.Fatal(err)
			}
			var runErr error
			var answer string
			var generations []string
			for response, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
				Contents: []*genai.Content{genai.NewContentFromText("retry me", genai.RoleUser)},
			}, true) {
				if err != nil {
					runErr = err
					break
				}
				if generation, ok := response.CustomMetadata[replyGenerationKey].(string); ok && response.Content != nil && len(response.Content.Parts) == 0 {
					generations = append(generations, generation)
				}
				if !response.Partial {
					answer = textFromADKContent(response.Content)
				}
			}
			if (runErr != nil) != test.wantError {
				t.Fatalf("error=%v, wantError=%v", runErr, test.wantError)
			}
			if primaryCalls.Load() != test.wantPrimary || backupCalls.Load() != test.wantBackup {
				t.Fatalf("calls=%d/%d, want %d/%d", primaryCalls.Load(), backupCalls.Load(), test.wantPrimary, test.wantBackup)
			}
			if !test.wantError && answer == "" {
				t.Fatal("missing recovered answer")
			}
			if test.wantPrimary == 2 && (len(generations) < 2 || generations[0] == generations[1]) {
				t.Fatalf("retry must reset preview generation: %v", generations)
			}
		})
	}
}

func TestADKRouteModelCancellationStopsRetryBackoff(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "temporary error", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	llm, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), []store.ModelRoute{{Enabled: true, ProviderBaseUrl: server.URL, Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runErr error
	for response, err := range llm.GenerateContent(ctx, &model.LLMRequest{}, true) {
		if err != nil {
			runErr = err
			break
		}
		if response.CustomMetadata[modelStageKey] == "模型请求暂时失败，正在重试当前模型" {
			cancel()
		}
	}
	if !errors.Is(runErr, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("error=%v calls=%d", runErr, calls.Load())
	}
}

func TestPrioritizeRoutes(t *testing.T) {
	id1 := uuid.New()
	id2 := uuid.New()
	id3 := uuid.New()

	routes := []store.ModelRoute{
		{ID: id1, Model: "model-1"},
		{ID: id2, Model: "model-2"},
		{ID: id3, Model: "model-3"},
	}

	// 1. 无任何优先配置，保持原顺序
	res := prioritizeRoutes(routes, uuid.NullUUID{}, uuid.NullUUID{}, uuid.NullUUID{})
	if len(res) != 3 || res[0].ID != id1 || res[1].ID != id2 || res[2].ID != id3 {
		t.Fatalf("expected original order, got %+v", res)
	}

	// 2. 只有工作区默认 id3
	res = prioritizeRoutes(routes, uuid.NullUUID{}, uuid.NullUUID{}, uuid.NullUUID{UUID: id3, Valid: true})
	if len(res) != 3 || res[0].ID != id3 || res[1].ID != id1 || res[2].ID != id2 {
		t.Fatalf("expected id3 first, got %+v", res)
	}

	// 3. 场景默认 id2 覆盖工作区默认 id3
	res = prioritizeRoutes(routes, uuid.NullUUID{}, uuid.NullUUID{UUID: id2, Valid: true}, uuid.NullUUID{UUID: id3, Valid: true})
	if len(res) != 3 || res[0].ID != id2 || res[1].ID != id3 || res[2].ID != id1 {
		t.Fatalf("expected id2 first, id3 second, got %+v", res)
	}

	// 4. 用户指定 preferredID(id1) 覆盖场景默认 id2 与工作区默认 id3
	res = prioritizeRoutes(routes, uuid.NullUUID{UUID: id1, Valid: true}, uuid.NullUUID{UUID: id2, Valid: true}, uuid.NullUUID{UUID: id3, Valid: true})
	if len(res) != 3 || res[0].ID != id1 || res[1].ID != id2 || res[2].ID != id3 {
		t.Fatalf("expected id1 first, id2 second, id3 third, got %+v", res)
	}
}

func TestADKRouteModelUsageActualEstimatedAndFailoverRouteTracking(t *testing.T) {
	primaryID := uuid.New()
	backupID := uuid.New()
	relayID := uuid.New()

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "quota exhausted", http.StatusPaymentRequired)
	}))
	defer primary.Close()

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"backup response with usage"}}],
			"usage":{
				"prompt_tokens":420,
				"completion_tokens":85,
				"prompt_tokens_details":{"cached_tokens":120},
				"completion_tokens_details":{"reasoning_tokens":30}
			}
		}`))
	}))
	defer backup.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"relay response without usage"}}]}`))
	}))
	defer relay.Close()

	llm, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), []store.ModelRoute{
		{ID: primaryID, Enabled: true, ProviderType: "openai_compatible", ProviderBaseUrl: primary.URL, Model: "primary-model"},
		{ID: backupID, Enabled: true, ProviderType: "openai_compatible", ProviderBaseUrl: backup.URL, Model: "backup-model"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if obs, ok := llm.LastObservation(); ok || obs.UsageSource != TokenUsageSourceUnknown {
		t.Fatalf("initial observation=%+v ok=%v, want unknown/false", obs, ok)
	}

	var finalResp *model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("测试故障切换后的真实 Token 统计与路由记录", genai.RoleUser)},
	}, false) {
		if err != nil {
			t.Fatal(err)
		}
		if !resp.Partial {
			finalResp = resp
		}
	}
	if finalResp == nil || finalResp.UsageMetadata == nil {
		t.Fatalf("finalResp=%+v, expected UsageMetadata", finalResp)
	}
	if finalResp.UsageMetadata.PromptTokenCount != 420 ||
		finalResp.UsageMetadata.CandidatesTokenCount != 85 ||
		finalResp.UsageMetadata.CachedContentTokenCount != 120 ||
		finalResp.UsageMetadata.ThoughtsTokenCount != 30 ||
		finalResp.UsageMetadata.TotalTokenCount != 505 {
		t.Fatalf("unexpected UsageMetadata=%+v", finalResp.UsageMetadata)
	}
	if finalResp.ModelVersion != "backup-model" {
		t.Fatalf("ModelVersion=%q, want backup-model", finalResp.ModelVersion)
	}
	if finalResp.CustomMetadata[tokenUsageSourceKey] != string(TokenUsageSourceActual) {
		t.Fatalf("token_usage_source=%v, want actual", finalResp.CustomMetadata[tokenUsageSourceKey])
	}
	if finalResp.CustomMetadata[modelRouteIDKey] != backupID.String() {
		t.Fatalf("model_route_id=%v, want %s", finalResp.CustomMetadata[modelRouteIDKey], backupID)
	}

	activeRoute, ok := llm.ActiveRoute()
	if !ok || activeRoute.ID != backupID || llm.ActiveModel() != "backup-model" {
		t.Fatalf("ActiveRoute=%+v ActiveModel=%q, want backup route %s", activeRoute, llm.ActiveModel(), backupID)
	}
	obs, ok := llm.LastObservation()
	if !ok || obs.RouteIndex != 1 || obs.RouteID != backupID || obs.Model != "backup-model" || obs.UsageSource != TokenUsageSourceActual || obs.Usage.InputTokens != 420 {
		t.Fatalf("LastObservation=%+v ok=%v", obs, ok)
	}

	// 验证中转不返回 Usage 时，回退为本地估算（estimated）并同样写入 UsageMetadata。
	relayLLM, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), []store.ModelRoute{
		{ID: relayID, Enabled: true, ProviderType: "openai_compatible", ProviderBaseUrl: relay.URL, Model: "relay-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var relayResp *model.LLMResponse
	for resp, err := range relayLLM.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("这是一段需要本地估算 Token 用量的输入内容", genai.RoleUser)},
	}, false) {
		if err != nil {
			t.Fatal(err)
		}
		if !resp.Partial {
			relayResp = resp
		}
	}
	if relayResp == nil || relayResp.UsageMetadata == nil {
		t.Fatalf("relayResp=%+v, expected estimated UsageMetadata", relayResp)
	}
	if relayResp.UsageMetadata.PromptTokenCount <= 0 || relayResp.UsageMetadata.CandidatesTokenCount <= 0 {
		t.Fatalf("estimated UsageMetadata must be positive: %+v", relayResp.UsageMetadata)
	}
	if relayResp.CustomMetadata[tokenUsageSourceKey] != string(TokenUsageSourceEstimated) {
		t.Fatalf("token_usage_source=%v, want estimated", relayResp.CustomMetadata[tokenUsageSourceKey])
	}
	relayObs, ok := relayLLM.LastObservation()
	if !ok || relayObs.RouteID != relayID || relayObs.UsageSource != TokenUsageSourceEstimated || relayObs.Usage.InputTokens <= 0 {
		t.Fatalf("relay LastObservation=%+v ok=%v", relayObs, ok)
	}
}

func TestEstimateADKRequestTokens_SystemToolsAndNonASCII(t *testing.T) {
	if got := estimateADKRequestTokens(nil); got != 0 {
		t.Fatalf("nil request tokens=%d, want 0", got)
	}

	// 即使 Contents 为空，SystemInstruction 与 Tools 定义也应计入估算。
	reqOnlyConfig := &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(strings.Repeat("S", 400), genai.RoleUser),
			Tools: []*genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:        "exec_command",
							Description: strings.Repeat("D", 400),
							ParametersJsonSchema: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"cmd": map[string]any{
										"type":        "string",
										"description": strings.Repeat("P", 400),
									},
								},
							},
						},
					},
				},
			},
		},
	}
	configTokens := estimateADKRequestTokens(reqOnlyConfig)
	if configTokens < 300 {
		t.Fatalf("configTokens=%d, want >= 300 when SystemInstruction and Tools schema are present", configTokens)
	}

	// 非 ASCII（中文）字符密度高于纯 ASCII 1/4 token，1000 个汉字应估算为 1500 tokens（而非 250）。
	reqChinese := &model.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText(strings.Repeat("中", 1000), genai.RoleUser),
		},
	}
	chineseTokens := estimateADKRequestTokens(reqChinese)
	if chineseTokens != 1500 {
		t.Fatalf("chineseTokens=%d, want 1500 for 1000 Chinese runes", chineseTokens)
	}

	// Thought part 不应计入输入估算。
	thoughtPart := genai.NewPartFromText(strings.Repeat("中", 1000))
	thoughtPart.Thought = true
	reqWithThought := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: genai.RoleModel, Parts: []*genai.Part{thoughtPart, genai.NewPartFromText("ok")}},
		},
	}
	if thoughtTokens := estimateADKRequestTokens(reqWithThought); thoughtTokens != 1 {
		t.Fatalf("thoughtTokens=%d, want 1 (thought parts must be ignored)", thoughtTokens)
	}
}

func TestADKRouteModelCapacityInterception_SystemToolsAndChinese(t *testing.T) {
	t.Run("SystemInstruction 与 Tools Schema 叠加超限时在发送前拦截", func(t *testing.T) {
		var routeCalls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			routeCalls.Add(1)
			http.Error(w, "should not be reached", http.StatusBadRequest)
		}))
		defer srv.Close()

		// EffectiveInputLimit = min(10000, 16000-4000) = 10000
		llm, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), []store.ModelRoute{
			{
				ID:                  uuid.New(),
				Enabled:             true,
				ProviderBaseUrl:     srv.URL,
				Model:               "capped-10k-model",
				ContextWindowTokens: pgtype.Int4{Int32: 16000, Valid: true},
				MaxInputTokens:      pgtype.Int4{Int32: 10000, Valid: true},
				MaxOutputTokens:     pgtype.Int4{Int32: 4000, Valid: true},
				LimitsSource:        modelclient.LimitsSourceUser,
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		// Contents 很小（~500 tokens），但 SystemInstruction (~6000) + Tools (~6000) 总计超过 10000
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				genai.NewContentFromText(strings.Repeat("A", 2000), genai.RoleUser),
			},
			Config: &genai.GenerateContentConfig{
				SystemInstruction: genai.NewContentFromText(strings.Repeat("S", 24000), genai.RoleUser),
				Tools: []*genai.Tool{
					{
						FunctionDeclarations: []*genai.FunctionDeclaration{
							{
								Name:        "large_schema_tool",
								Description: strings.Repeat("T", 12000),
								ParametersJsonSchema: map[string]any{
									"type": "object",
									"properties": map[string]any{
										"payload": map[string]any{
											"type":        "string",
											"description": strings.Repeat("J", 12000),
										},
									},
								},
							},
						},
					},
				},
			},
		}

		var gotErr error
		for _, err := range llm.GenerateContent(context.Background(), req, false) {
			if err != nil {
				gotErr = err
				break
			}
		}
		if !errors.Is(gotErr, ErrRouteContextCapacityExceeded) {
			t.Fatalf("gotErr=%v, want ErrRouteContextCapacityExceeded", gotErr)
		}
		if routeCalls.Load() != 0 {
			t.Fatalf("routeCalls=%d, want 0 (oversized SystemInstruction+Tools must be blocked before HTTP request)", routeCalls.Load())
		}
	})

	t.Run("高密度中文输入不再因按4字符除算漏判而绕过容量校验", func(t *testing.T) {
		var smallCalls, largeCalls atomic.Int32
		smallSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			smallCalls.Add(1)
			http.Error(w, "small route should not be called", http.StatusBadRequest)
		}))
		defer smallSrv.Close()

		largeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			largeCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			writeModelResponse(t, w, "大窗口备用路由成功接管中文长上下文", nil)
		}))
		defer largeSrv.Close()

		// 30,000 个汉字按 1.5 token/字符 ≈ 45,000 tokens；
		// 旧逻辑按 rune/4 仅算 7,500 tokens，会错误命中 32K 小路由；新逻辑应跳过 32K 路由直接命中 200K 路由。
		llm, err := newADKRouteModel(modelclient.New(), nil, nil, uuid.New(), []store.ModelRoute{
			{
				ID:                  uuid.New(),
				Enabled:             true,
				ProviderBaseUrl:     smallSrv.URL,
				Model:               "small-32k",
				ContextWindowTokens: pgtype.Int4{Int32: 40000, Valid: true},
				MaxInputTokens:      pgtype.Int4{Int32: 32000, Valid: true},
				MaxOutputTokens:     pgtype.Int4{Int32: 4000, Valid: true},
				LimitsSource:        modelclient.LimitsSourceUser,
			},
			{
				ID:              uuid.New(),
				Enabled:         true,
				ProviderBaseUrl: largeSrv.URL,
				Model:           "claude-sonnet-4-5", // 200K registry window
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		var answer string
		for resp, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
			Contents: []*genai.Content{
				genai.NewContentFromText(strings.Repeat("汉", 30000), genai.RoleUser),
			},
		}, false) {
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !resp.Partial {
				answer = textFromADKContent(resp.Content)
			}
		}
		if smallCalls.Load() != 0 {
			t.Fatalf("smallCalls=%d, want 0 (45K estimated Chinese prompt must skip 32K route)", smallCalls.Load())
		}
		if largeCalls.Load() != 1 {
			t.Fatalf("largeCalls=%d, want 1", largeCalls.Load())
		}
		if answer != "大窗口备用路由成功接管中文长上下文" {
			t.Fatalf("answer=%q", answer)
		}
	})
}
