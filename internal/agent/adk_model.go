package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentconfig"
	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

type TokenUsageSource string

const (
	TokenUsageSourceUnknown   TokenUsageSource = "unknown"
	TokenUsageSourceActual    TokenUsageSource = "actual"
	TokenUsageSourceEstimated TokenUsageSource = "estimated"

	tokenUsageSourceKey = "cycledock.token_usage_source"
	modelRouteIDKey     = "cycledock.model_route_id"
	modelProviderKey    = "cycledock.model_provider"

	// 按 quarter-token（1/4 token）为最小单位累加字符权重：
	// - ASCII 字符约 4 字符 ≈ 1 token（权重 1）
	// - 非 ASCII 字符（如中文、日文、全角标点、Emoji）通常占 1~2 token，按 1.5 token/字符（权重 6）估算，
	//   避免中文长会话或中文字段在发送前容量检查时被严重低估。
	estimatedTokenScale     = 4
	asciiCharTokenWeight    = 1
	nonASCIICharTokenWeight = 6

	// 视觉模型单张图片的默认保守 Token 估算（近似 OpenAI high-detail 2x2 tile ≈ 765~1100 tokens）。
	// 不按 base64 字符数估算（否则一张 1MB PNG 会被误算为 330k tokens 触发误拦截），也不计为 0。
	estimatedImageTokens = 1100
	// 每次模型请求最多随历史恢复的最近工具图片数量，避免多轮截图在历史中无限堆积撑爆上下文。
	maxPromptToolImageCount = 3
)

// RouteCallObservation 记录最近一次成功命中的模型路由及本次调用的 Token 统计与来源。
// 当请求发生故障切换（failover）时，它始终对应实际返回响应的路由，避免将备用路由的用量误算在原始路由上。
type RouteCallObservation struct {
	RouteIndex  int
	RouteID     uuid.UUID
	Provider    string
	Model       string
	Usage       modelclient.TokenUsage
	UsageSource TokenUsageSource
}

// RouteFailureEntry 记录多路由调用全部失败时单条路由的失败明细。
type RouteFailureEntry struct {
	RouteIndex int
	RouteID    uuid.UUID
	Model      string
	Provider   string
	Err        error
}

// MultiRouteError 聚合一次模型调用在多条候选路由上的全部失败摘要，
// 同时通过 Unwrap() []error 保持标准库 errors.Is / errors.As 按尝试顺序检索底层错误链。
type MultiRouteError struct {
	Entries []RouteFailureEntry
}

func (e *MultiRouteError) Error() string {
	if e == nil || len(e.Entries) == 0 {
		return "no model route was available"
	}
	parts := make([]string, 0, len(e.Entries))
	for _, entry := range e.Entries {
		modelName := strings.TrimSpace(entry.Model)
		if modelName == "" {
			modelName = entry.RouteID.String()
		}
		parts = append(parts, fmt.Sprintf("route#%d (%s, %s): %v", entry.RouteIndex, modelName, entry.Provider, entry.Err))
	}
	return "all configured model routes failed: " + strings.Join(parts, "; ")
}

func (e *MultiRouteError) Unwrap() []error {
	if e == nil || len(e.Entries) == 0 {
		return nil
	}
	errs := make([]error, 0, len(e.Entries))
	for _, entry := range e.Entries {
		if entry.Err != nil {
			errs = append(errs, entry.Err)
		}
	}
	return errs
}

// adkRouteModel adapts CycleDock's production model transports and ordered
// credential routes to ADK's model.LLM interface. ADK owns the conversation and
// tool loop; CycleDock keeps credential refresh, route failover and converts
// provider argument deltas into ADK partial responses.
type adkRouteModel struct {
	client             *modelclient.Client
	modelAuth          *modelauth.Service
	store              *store.Store
	artifactService    artifact.Service
	workspaceID        uuid.UUID
	cardID             uuid.UUID
	turnID             uuid.UUID
	attemptID          uuid.UUID
	purpose            string
	routes             []store.ModelRoute
	budgetPolicy       CompactionBudgetPolicy
	requestTimeout     time.Duration
	sharedActiveIndex  *atomic.Int32
	ownActiveIndexSlot atomic.Int32

	mu              sync.Mutex
	modelCalls      int
	lastObservation RouteCallObservation
	hasObservation  bool
}

func prioritizeRoutes(
	routes []store.ModelRoute,
	preferredID uuid.NullUUID,
	kindDefaultID uuid.NullUUID,
	workspaceDefaultID uuid.NullUUID,
) []store.ModelRoute {
	if len(routes) == 0 {
		return routes
	}
	routeByID := make(map[uuid.UUID]store.ModelRoute, len(routes))
	for _, r := range routes {
		routeByID[r.ID] = r
	}

	ordered := make([]store.ModelRoute, 0, len(routes))
	seen := make(map[uuid.UUID]bool, len(routes))

	candidates := []uuid.NullUUID{preferredID, kindDefaultID, workspaceDefaultID}
	for _, candidate := range candidates {
		if candidate.Valid && candidate.UUID != uuid.Nil && !seen[candidate.UUID] {
			if r, ok := routeByID[candidate.UUID]; ok {
				ordered = append(ordered, r)
				seen[candidate.UUID] = true
			}
		}
	}

	for _, r := range routes {
		if !seen[r.ID] {
			ordered = append(ordered, r)
			seen[r.ID] = true
		}
	}
	return ordered
}

func newADKRouteModel(
	client *modelclient.Client,
	auth *modelauth.Service,
	dataStore *store.Store,
	workspaceID uuid.UUID,
	routes []store.ModelRoute,
) (*adkRouteModel, error) {
	if client == nil {
		client = modelclient.New()
	}
	enabled := make([]store.ModelRoute, 0, len(routes))
	for _, route := range routes {
		if route.Enabled {
			enabled = append(enabled, route)
		}
	}
	if len(enabled) == 0 {
		return nil, errors.New("built-in agent has no enabled model route")
	}
	m := &adkRouteModel{
		client:       client,
		modelAuth:    auth,
		store:        dataStore,
		workspaceID:  workspaceID,
		purpose:      "agent",
		routes:       enabled,
		budgetPolicy: DefaultCompactionBudgetPolicy(),
	}
	m.sharedActiveIndex = &m.ownActiveIndexSlot
	return m, nil
}

// shareActiveRouteFrom 让同一 Attempt 内的辅助模型实例（如上下文压缩 summaryLLM）与主模型共享活跃路由索引和预算策略。
// 当主模型或摘要模型任一方发生故障切换（failover）并成功命中备用路由后，另一方后续请求直接从该健康路由起步，
// 避免每次压缩都重新撞向已超时/故障的首条路由。
func (m *adkRouteModel) shareActiveRouteFrom(peer *adkRouteModel) *adkRouteModel {
	if m == nil || peer == nil {
		return m
	}
	if peer.sharedActiveIndex != nil {
		m.sharedActiveIndex = peer.sharedActiveIndex
	} else {
		m.sharedActiveIndex = &peer.ownActiveIndexSlot
	}
	m.budgetPolicy = peer.budgetPolicy
	return m
}

func (m *adkRouteModel) loadActiveRouteIndex() int {
	if m == nil {
		return 0
	}
	slot := m.sharedActiveIndex
	if slot == nil {
		slot = &m.ownActiveIndexSlot
	}
	idx := int(slot.Load())
	if idx < 0 || idx >= len(m.routes) {
		return 0
	}
	return idx
}

func (m *adkRouteModel) storeActiveRouteIndex(idx int) {
	if m == nil || idx < 0 || idx >= len(m.routes) {
		return
	}
	slot := m.sharedActiveIndex
	if slot == nil {
		slot = &m.ownActiveIndexSlot
	}
	slot.Store(int32(idx))
}

func (m *adkRouteModel) withTraceContext(cardID, turnID, attemptID uuid.UUID, purpose string) *adkRouteModel {
	if m == nil {
		return nil
	}
	m.cardID = cardID
	m.turnID = turnID
	m.attemptID = attemptID
	if purpose != "" {
		m.purpose = purpose
	}
	return m
}

func (m *adkRouteModel) withArtifactService(service artifact.Service) *adkRouteModel {
	if m == nil {
		return nil
	}
	m.artifactService = service
	return m
}

func (m *adkRouteModel) Name() string {
	return "cycledock-route"
}

func (m *adkRouteModel) ActiveModel() string {
	route, ok := m.ActiveRoute()
	if !ok {
		return ""
	}
	return strings.TrimSpace(route.Model)
}

func (m *adkRouteModel) ActiveRoute() (store.ModelRoute, bool) {
	if m == nil || len(m.routes) == 0 {
		return store.ModelRoute{}, false
	}
	idx := m.loadActiveRouteIndex()
	return m.routes[idx], true
}

func (m *adkRouteModel) LastObservation() (RouteCallObservation, bool) {
	if m == nil {
		return RouteCallObservation{UsageSource: TokenUsageSourceUnknown}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasObservation {
		return RouteCallObservation{UsageSource: TokenUsageSourceUnknown}, false
	}
	return m.lastObservation, true
}

func (m *adkRouteModel) GenerateContent(
	ctx context.Context,
	req *model.LLMRequest,
	stream bool,
) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		messages, tools, err := adkRequestToModelClientWithArtifacts(
			ctx,
			req,
			m.artifactService,
			m.workspaceID,
			m.cardID,
		)
		if err != nil {
			yield(nil, err)
			return
		}

		outputSchema, err := adkOutputSchema(req.Config)
		if err != nil {
			yield(nil, err)
			return
		}

		start := m.loadActiveRouteIndex()
		m.mu.Lock()
		m.modelCalls++
		modelCall := m.modelCalls
		m.mu.Unlock()

		// 依托 repeatedToolLoop 死循环熔断保障长任务顺利推进；历史中的新 Turn、不同参数或结果都会打断重复计数。
		if repeatedToolLoop(messages) {
			yield(nil, errRunawayToolLoop)
			return
		}

		estimatedPromptTokens := estimateADKRequestTokens(req)
		routeOrder := make([]int, 0, len(m.routes))
		for i := start; i < len(m.routes); i++ {
			routeOrder = append(routeOrder, i)
		}
		for i := 0; i < start; i++ {
			routeOrder = append(routeOrder, i)
		}

		failures := make([]RouteFailureEntry, 0, len(routeOrder))
		for step, i := range routeOrder {
			matchedRoute := m.routes[i]
			recordRouteFailure := func(routeErr error) {
				failures = append(failures, RouteFailureEntry{
					RouteIndex: i,
					RouteID:    matchedRoute.ID,
					Model:      strings.TrimSpace(matchedRoute.Model),
					Provider:   normalizedProviderType(matchedRoute.ProviderType),
					Err:        routeErr,
				})
			}

			routeLimits := ResolveRouteModelLimits(matchedRoute, m.client)
			routeBudget := ComputeRouteCompactionBudget(routeLimits, m.budgetPolicy)
			if capErr := ValidateRoutePromptCapacity(
				matchedRoute,
				routeBudget,
				estimatedPromptTokens,
				step > 0,
				m.budgetPolicy,
			); capErr != nil {
				recordRouteFailure(capErr)
				if step+1 < len(routeOrder) && stream && !yield(modelStageResponse("备用模型上下文窗口不足，正在尝试下一模型"), nil) {
					return
				}
				continue
			}

			config, err := m.routeConfig(ctx, matchedRoute)
			if err != nil {
				if !isRetryableModelRouteError(ctx, err) {
					yield(nil, err)
					return
				}
				recordRouteFailure(err)
				if step+1 < len(routeOrder) && stream && !yield(modelStageResponse("当前模型不可用，正在切换备用模型"), nil) {
					return
				}
				continue
			}

			var routeErr error
			for requestAttempt := 0; requestAttempt < 2; requestAttempt++ {
				config.OutputSchema = outputSchema

				requestCtx, cancel := context.WithCancel(ctx)
				stopped := false
				emit := func(response *model.LLMResponse) bool {
					if stopped {
						return false
					}
					if !yield(response, nil) {
						stopped = true
						cancel()
						return false
					}
					return true
				}
				if stream {
					generation := uuid.NewString()
					// 批次开始和参数增量均经 ADK Partial 事件转发，模型适配器不接触 SSE Hub。
					if !emit(replyGenerationResponse(generation)) {
						cancel()
						return
					}
					config.OnToolCall = previewToolCalls(generation, modelCall, emit)
				}
				callStarted := time.Now().UTC()
				completion, err := m.client.Complete(requestCtx, config, messages, tools)
				cancel()
				if stopped {
					return
				}
				var response *model.LLMResponse
				if err == nil {
					response, err = modelClientCompletionToADK(completion)
				}
				callFinished := time.Now().UTC()
				durationMS := int(callFinished.Sub(callStarted).Milliseconds())
				if durationMS < 0 {
					durationMS = 0
				}
				if err == nil {
					usage, usageSource, usageMetadata := resolveADKTokenUsage(req, completion)
					response.UsageMetadata = usageMetadata
					response.ModelVersion = strings.TrimSpace(matchedRoute.Model)
					response.CustomMetadata = buildModelResponseMetadata(matchedRoute, usageSource)

					m.storeActiveRouteIndex(i)
					m.mu.Lock()
					m.lastObservation = RouteCallObservation{
						RouteIndex:  i,
						RouteID:     matchedRoute.ID,
						Provider:    normalizedProviderType(matchedRoute.ProviderType),
						Model:       strings.TrimSpace(matchedRoute.Model),
						Usage:       usage,
						UsageSource: usageSource,
					}
					m.hasObservation = true
					m.mu.Unlock()

					m.recordTraceModelCall(ctx, store.RecordModelCallParams{
						CallIndex:             modelCall,
						Purpose:               m.purpose,
						ModelRouteID:          uuid.NullUUID{UUID: matchedRoute.ID, Valid: true},
						ModelID:               strings.TrimSpace(matchedRoute.Model),
						Provider:              normalizedProviderType(matchedRoute.ProviderType),
						StartedAt:             callStarted,
						FinishedAt:            callFinished,
						DurationMS:            durationMS,
						Status:                "success",
						InputTokens:           usage.InputTokens,
						OutputTokens:          usage.OutputTokens,
						CachedInputTokens:     usage.CachedInputTokens,
						ReasoningOutputTokens: usage.ReasoningOutputTokens,
						UsageSource:           string(usageSource),
					})

					yield(response, nil)
					return
				}

				m.recordTraceModelCall(ctx, store.RecordModelCallParams{
					CallIndex:    modelCall,
					Purpose:      m.purpose,
					ModelRouteID: uuid.NullUUID{UUID: matchedRoute.ID, Valid: true},
					ModelID:      strings.TrimSpace(matchedRoute.Model),
					Provider:     normalizedProviderType(matchedRoute.ProviderType),
					StartedAt:    callStarted,
					FinishedAt:   callFinished,
					DurationMS:   durationMS,
					Status:       "failed",
					UsageSource:  "unknown",
					ErrorSummary: err.Error(),
				})
				if modelclient.IsContextTooLargeError(err) {
					routeErr = fmt.Errorf("%w: %v", ErrRouteContextCapacityExceeded, err)
					break
				}
				if !isRetryableModelRouteError(ctx, err) {
					yield(nil, err)
					return
				}
				routeErr = err
				if requestAttempt == 0 && isRetryableModelRequestError(ctx, err) {
					if stream && !yield(modelStageResponse("模型请求暂时失败，正在重试当前模型"), nil) {
						return
					}
					// 短暂退避响应取消；重试只重发模型请求，不重复已执行工具，也不增加工具轮数。
					timer := time.NewTimer(500 * time.Millisecond)
					select {
					case <-ctx.Done():
						timer.Stop()
						yield(nil, ctx.Err())
						return
					case <-timer.C:
					}
					continue
				}
				break
			}

			if routeErr != nil {
				recordRouteFailure(routeErr)
			}
			if step+1 < len(routeOrder) && stream && !yield(modelStageResponse("当前模型不可用，正在切换备用模型"), nil) {
				return
			}
		}

		if len(failures) == 0 {
			yield(nil, errors.New("all configured model routes failed: no model route was available"))
			return
		}
		yield(nil, &MultiRouteError{Entries: failures})
	}
}

func (m *adkRouteModel) recordTraceModelCall(ctx context.Context, params store.RecordModelCallParams) {
	if m == nil || m.store == nil || m.workspaceID == uuid.Nil || m.cardID == uuid.Nil || m.attemptID == uuid.Nil {
		return
	}
	params.WorkspaceID = m.workspaceID
	params.CardID = m.cardID
	params.TurnID = m.turnID
	params.AttemptID = m.attemptID
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = m.store.RecordModelCall(recordCtx, params)
}

func (m *adkRouteModel) routeConfig(
	ctx context.Context,
	route store.ModelRoute,
) (modelclient.Config, error) {
	timeout := agentconfig.DefaultModelRequestTimeout
	if m != nil && m.requestTimeout > 0 {
		timeout = m.requestTimeout
	}
	switch route.ProviderType {
	case "", "openai_compatible":
		var apiKey string
		if len(route.ApiKeyCiphertext) > 0 && m.store != nil {
			var err error
			apiKey, err = m.store.ModelRouteAPIKey(route)
			if err != nil {
				return modelclient.Config{}, fmt.Errorf("decrypt model API key: %w", err)
			}
		} else if route.ApiKeyEnv != "" {
			var err error
			apiKey, err = secretFromEnv(route.ApiKeyEnv, "model API key")
			if err != nil {
				return modelclient.Config{}, err
			}
		}
		return modelclient.Config{
			BaseURL:     route.ProviderBaseUrl,
			Model:       route.Model,
			APIKey:      apiKey,
			Timeout:     timeout,
			Temperature: 0,
		}, nil
	case string(modelauth.ProviderChatGPT):
		if m.modelAuth == nil || !route.CredentialID.Valid {
			return modelclient.Config{}, modelauth.ErrNotConnected
		}
		return m.modelAuth.ModelConfigForCredential(
			ctx, m.workspaceID, modelauth.ProviderChatGPT,
			route.CredentialID.UUID, route.Model, timeout,
		)
	case string(modelauth.ProviderGrok):
		if m.modelAuth == nil || !route.CredentialID.Valid {
			return modelclient.Config{}, modelauth.ErrNotConnected
		}
		return m.modelAuth.ModelConfigForCredential(
			ctx, m.workspaceID, modelauth.ProviderGrok,
			route.CredentialID.UUID, route.Model, timeout,
		)
	default:
		return modelclient.Config{}, fmt.Errorf("unsupported model provider %q", route.ProviderType)
	}
}

func adkRequestToModelClient(
	req *model.LLMRequest,
) ([]modelclient.Message, []modelclient.ToolDefinition, error) {
	return adkRequestToModelClientWithArtifacts(context.Background(), req, nil, uuid.Nil, uuid.Nil)
}

type resolvedToolImage struct {
	toolName string
	mimeType string
	data     []byte
	dataURL  string
}

func adkRequestToModelClientWithArtifacts(
	ctx context.Context,
	req *model.LLMRequest,
	artifactService artifact.Service,
	workspaceID, cardID uuid.UUID,
) ([]modelclient.Message, []modelclient.ToolDefinition, error) {
	if req == nil {
		return nil, nil, errors.New("ADK model request is nil")
	}

	// 预先计算历史中哪些工具结果需要水合图片，仅保留最近 maxPromptToolImageCount 张有效图片，
	// 避免长会话多轮截图/拍照无限堆积导致视觉上下文溢出。
	allowedToolImages := selectRecentToolImageTargets(req.Contents, maxPromptToolImageCount)

	messages := make([]modelclient.Message, 0, len(req.Contents)+1)
	if req.Config != nil && req.Config.SystemInstruction != nil {
		system, err := adkTextContent(req.Config.SystemInstruction)
		if err != nil {
			return nil, nil, fmt.Errorf("convert system instruction: %w", err)
		}
		if strings.TrimSpace(system) != "" {
			messages = append(messages, modelclient.Message{Role: "system", Content: system})
		}
	}

	for contentIdx, content := range req.Contents {
		if content == nil {
			continue
		}
		var parts []modelclient.ContentPart
		var hasImages bool
		var calls []modelclient.ToolCall
		var toolResponses []modelclient.Message
		var batchImages []resolvedToolImage
		for partIdx, part := range content.Parts {
			if part == nil || part.Thought {
				continue
			}
			switch {
			case part.Text != "":
				parts = append(parts, modelclient.NewTextPart(part.Text))
			case part.InlineData != nil && isImagePartBlob(part.InlineData):
				parts = append(parts, modelclient.NewImagePartFromBytes(part.InlineData.MIMEType, part.InlineData.Data))
				hasImages = true
			case part.FileData != nil && isImageFileData(part.FileData):
				parts = append(parts, modelclient.NewImagePartFromDataURL(part.FileData.FileURI))
				hasImages = true
			case part.FunctionCall != nil:
				rawArgs, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return nil, nil, fmt.Errorf("encode function call %q: %w", part.FunctionCall.Name, err)
				}
				calls = append(calls, modelclient.ToolCall{
					ID:   part.FunctionCall.ID,
					Type: "function",
					Function: modelclient.ToolFunction{
						Name:      part.FunctionCall.Name,
						Arguments: string(rawArgs),
					},
				})
			case part.FunctionResponse != nil:
				sanitizedResponse, extractedImages := splitFunctionResponseImages(
					ctx,
					part.FunctionResponse,
					allowedToolImages[toolPartKey{contentIdx: contentIdx, partIdx: partIdx}],
					artifactService,
					workspaceID,
					cardID,
				)
				rawResponse, err := json.Marshal(sanitizedResponse)
				if err != nil {
					return nil, nil, fmt.Errorf("encode function response %q: %w", part.FunctionResponse.Name, err)
				}
				toolResponses = append(toolResponses, modelclient.Message{
					Role:       "tool",
					ToolCallID: part.FunctionResponse.ID,
					Name:       part.FunctionResponse.Name,
					Content:    string(rawResponse),
				})
				batchImages = append(batchImages, extractedImages...)
			case part.InlineData != nil || part.FileData != nil:
				// 非图片二进制媒体保持优雅降级占位文本，避免历史中混入音频/未知附件时阻塞整个对话。
				parts = append(parts, modelclient.NewTextPart(formatUnsupportedMediaPlaceholder(part)))
			default:
				return nil, nil, errors.New("CycleDock ADK model adapter does not yet support non-text media parts")
			}
		}

		if len(toolResponses) > 0 {
			if len(parts) > 0 || len(calls) > 0 {
				return nil, nil, errors.New("mixed function responses and ordinary content are unsupported")
			}
			messages = append(messages, toolResponses...)
			// OpenAI Chat Completions 兼容接口与多数中转均不允许在 role="tool" 内放置 image_url，
			// 因此在整批 tool 响应完整配对结束之后，再紧跟追加一条合成 user 多模态消息，确保 tool_call_id 序列合法。
			if len(batchImages) > 0 {
				messages = append(messages, buildToolImagesUserMessage(batchImages))
			}
			continue
		}

		role := "user"
		if content.Role == string(genai.RoleModel) {
			role = "assistant"
		}
		msg := modelclient.Message{
			Role:      role,
			ToolCalls: calls,
		}
		if hasImages {
			msg.Parts = parts
		} else {
			var textBuilder strings.Builder
			for _, p := range parts {
				textBuilder.WriteString(p.Text)
			}
			msg.Content = textBuilder.String()
		}
		messages = append(messages, msg)
	}

	tools, err := adkToolsToModelClient(req.Config)
	if err != nil {
		return nil, nil, err
	}
	return messages, tools, nil
}

type toolPartKey struct {
	contentIdx int
	partIdx    int
}

func selectRecentToolImageTargets(contents []*genai.Content, maxImages int) map[toolPartKey]bool {
	selected := make(map[toolPartKey]bool)
	if maxImages <= 0 {
		return selected
	}
	remaining := maxImages
	for cIdx := len(contents) - 1; cIdx >= 0 && remaining > 0; cIdx-- {
		content := contents[cIdx]
		if content == nil {
			continue
		}
		for pIdx := len(content.Parts) - 1; pIdx >= 0 && remaining > 0; pIdx-- {
			part := content.Parts[pIdx]
			if part == nil || part.Thought || part.FunctionResponse == nil {
				continue
			}
			count := countFunctionResponseImages(part.FunctionResponse)
			if count == 0 {
				continue
			}
			selected[toolPartKey{contentIdx: cIdx, partIdx: pIdx}] = true
			remaining -= count
		}
	}
	return selected
}

func countFunctionResponseImages(fr *genai.FunctionResponse) int {
	if fr == nil {
		return 0
	}
	count := 0
	for _, fp := range fr.Parts {
		if fp != nil && ((fp.InlineData != nil && len(fp.InlineData.Data) > 0 && isImageMIMEType(fp.InlineData.MIMEType)) ||
			(fp.FileData != nil && isImageURI(fp.FileData.MIMEType, fp.FileData.FileURI))) {
			count++
		}
	}
	if fr.Response != nil {
		if refs, ok := fr.Response[imageArtifactsFieldKey]; ok {
			count += sliceLen(refs)
		}
		if inline, ok := fr.Response[imageInlineFieldKey]; ok {
			count += sliceLen(inline)
		}
	}
	return count
}

func sliceLen(v any) int {
	switch items := v.(type) {
	case []map[string]any:
		return len(items)
	case []any:
		return len(items)
	default:
		return 0
	}
}

func splitFunctionResponseImages(
	ctx context.Context,
	fr *genai.FunctionResponse,
	hydrateImages bool,
	artifactService artifact.Service,
	workspaceID, cardID uuid.UUID,
) (map[string]any, []resolvedToolImage) {
	if fr == nil {
		return nil, nil
	}
	var images []resolvedToolImage
	if hydrateImages {
		for _, fp := range fr.Parts {
			if fp == nil {
				continue
			}
			if fp.InlineData != nil && len(fp.InlineData.Data) > 0 && isImageMIMEType(fp.InlineData.MIMEType) {
				images = append(images, resolvedToolImage{
					toolName: fr.Name,
					mimeType: fp.InlineData.MIMEType,
					data:     fp.InlineData.Data,
				})
			} else if fp.FileData != nil && isImageURI(fp.FileData.MIMEType, fp.FileData.FileURI) {
				images = append(images, resolvedToolImage{
					toolName: fr.Name,
					mimeType: fp.FileData.MIMEType,
					dataURL:  fp.FileData.FileURI,
				})
			}
		}
	}

	if fr.Response == nil {
		return nil, images
	}
	// 若 Response 中存在内联图片字段（无 ArtifactService 时的兜底载体），绝不能将原始二进制/base64 塞进 tool 文本响应中，
	// 而是剥离为结构化元信息并将图片附到后置多模态消息中。
	_, hasInline := fr.Response[imageInlineFieldKey]
	_, hasArtifactRefs := fr.Response[imageArtifactsFieldKey]
	if !hasInline && !hasArtifactRefs {
		return fr.Response, images
	}

	clean := make(map[string]any, len(fr.Response))
	for k, v := range fr.Response {
		if k == imageInlineFieldKey {
			continue
		}
		clean[k] = v
	}

	if hydrateImages {
		if hasInline {
			images = append(images, decodeInlineImagesField(fr.Name, fr.Response[imageInlineFieldKey])...)
		}
		if hasArtifactRefs && artifactService != nil && workspaceID != uuid.Nil && cardID != uuid.Nil {
			appName, userID, sessionID := agentsession.Identity(workspaceID, cardID)
			for _, ref := range parseImageArtifactRefs(fr.Response[imageArtifactsFieldKey]) {
				if ref.ArtifactRef == "" {
					continue
				}
				loaded, err := artifactService.Load(ctx, &artifact.LoadRequest{
					AppName:   appName,
					UserID:    userID,
					SessionID: sessionID,
					FileName:  ref.ArtifactRef,
					Version:   ref.ArtifactVersion,
				})
				if err != nil || loaded == nil || loaded.Part == nil || loaded.Part.InlineData == nil || len(loaded.Part.InlineData.Data) == 0 {
					continue
				}
				mimeType := loaded.Part.InlineData.MIMEType
				if mimeType == "" {
					mimeType = ref.MIMEType
				}
				images = append(images, resolvedToolImage{
					toolName: fr.Name,
					mimeType: mimeType,
					data:     loaded.Part.InlineData.Data,
				})
			}
		}
	}
	return clean, images
}

func parseImageArtifactRefs(raw any) []imageArtifactRef {
	var out []imageArtifactRef
	switch items := raw.(type) {
	case []map[string]any:
		for _, item := range items {
			if ref, ok := parseOneImageArtifactRef(item); ok {
				out = append(out, ref)
			}
		}
	case []any:
		for _, rawItem := range items {
			item, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			if ref, ok := parseOneImageArtifactRef(item); ok {
				out = append(out, ref)
			}
		}
	}
	return out
}

func parseOneImageArtifactRef(item map[string]any) (imageArtifactRef, bool) {
	if item == nil {
		return imageArtifactRef{}, false
	}
	fileName, _ := item[imageArtifactRefKey].(string)
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return imageArtifactRef{}, false
	}
	mimeType, _ := item[imageArtifactMIMEKey].(string)
	return imageArtifactRef{
		ArtifactRef:     fileName,
		ArtifactVersion: int64FromAny(item[imageArtifactVersionKey]),
		MIMEType:        strings.TrimSpace(mimeType),
		SizeBytes:       int(int64FromAny(item[imageArtifactBytesKey])),
	}, true
}

func int64FromAny(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		parsed, _ := n.Int64()
		return parsed
	default:
		return 0
	}
}

func decodeInlineImagesField(toolName string, raw any) []resolvedToolImage {
	var out []resolvedToolImage
	appendItem := func(item map[string]any) {
		if item == nil {
			return
		}
		mimeType, _ := item[imageArtifactMIMEKey].(string)
		switch dataVal := item["data"].(type) {
		case []byte:
			if len(dataVal) > 0 {
				out = append(out, resolvedToolImage{toolName: toolName, mimeType: mimeType, data: dataVal})
			}
		case string:
			if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataVal)); err == nil && len(decoded) > 0 {
				out = append(out, resolvedToolImage{toolName: toolName, mimeType: mimeType, data: decoded})
			}
		}
	}
	switch items := raw.(type) {
	case []map[string]any:
		for _, item := range items {
			appendItem(item)
		}
	case []any:
		for _, rawItem := range items {
			if item, ok := rawItem.(map[string]any); ok {
				appendItem(item)
			}
		}
	}
	return out
}

func buildToolImagesUserMessage(images []resolvedToolImage) modelclient.Message {
	parts := make([]modelclient.ContentPart, 0, len(images)*2)
	for i, img := range images {
		label := fmt.Sprintf("Image #%d returned by tool %s:", i+1, img.toolName)
		if strings.TrimSpace(img.toolName) == "" {
			label = fmt.Sprintf("Image #%d returned by tool:", i+1)
		}
		parts = append(parts, modelclient.NewTextPart(label))
		if img.dataURL != "" {
			parts = append(parts, modelclient.NewImagePartFromDataURL(img.dataURL))
		} else {
			parts = append(parts, modelclient.NewImagePartFromBytes(img.mimeType, img.data))
		}
	}
	return modelclient.Message{
		Role:  "user",
		Parts: parts,
	}
}

func isImagePartBlob(blob *genai.Blob) bool {
	if blob == nil || len(blob.Data) == 0 {
		return false
	}
	return isImageMIMEType(blob.MIMEType)
}

func isImageFileData(fileData *genai.FileData) bool {
	if fileData == nil {
		return false
	}
	return isImageURI(fileData.MIMEType, fileData.FileURI)
}

func isImageURI(mimeType, uri string) bool {
	if strings.TrimSpace(uri) == "" {
		return false
	}
	if isImageMIMEType(mimeType) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(uri)), "data:image/")
}

func formatUnsupportedMediaPlaceholder(part *genai.Part) string {
	if part == nil {
		return "[media]"
	}
	if part.InlineData != nil {
		return fmt.Sprintf("[non-image inline media: mimeType=%s, bytes=%d]", part.InlineData.MIMEType, len(part.InlineData.Data))
	}
	if part.FileData != nil {
		return fmt.Sprintf("[non-image file media: mimeType=%s, uri=%s]", part.FileData.MIMEType, part.FileData.FileURI)
	}
	return "[unsupported media]"
}

func adkOutputSchema(config *genai.GenerateContentConfig) (*modelclient.OutputSchema, error) {
	if config == nil {
		return nil, nil
	}
	// 当前传输使用标准 JSON Schema，不能静默丢弃 Gemini/OpenAPI 格式的 Schema。
	if config.ResponseSchema != nil {
		return nil, errors.New("ADK model adapter requires ResponseJsonSchema instead of ResponseSchema")
	}
	if config.ResponseJsonSchema == nil {
		return nil, nil
	}
	if config.ResponseMIMEType != "application/json" {
		return nil, errors.New("ADK response JSON schema requires application/json MIME type")
	}
	schema, err := jsonObject(config.ResponseJsonSchema)
	if err != nil || schema == nil {
		return nil, errors.New("ADK response JSON schema must be an object")
	}
	return &modelclient.OutputSchema{Name: "response", Schema: schema, Strict: true}, nil
}

func adkToolsToModelClient(config *genai.GenerateContentConfig) ([]modelclient.ToolDefinition, error) {
	if config == nil || len(config.Tools) == 0 {
		return nil, nil
	}
	var out []modelclient.ToolDefinition
	for _, group := range config.Tools {
		if group == nil {
			continue
		}
		for _, declaration := range group.FunctionDeclarations {
			if declaration == nil {
				continue
			}
			parameters, err := functionParametersMap(declaration)
			if err != nil {
				return nil, fmt.Errorf("convert tool %q schema: %w", declaration.Name, err)
			}
			if parameters == nil {
				parameters = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			out = append(out, modelclient.ToolDefinition{
				Type: "function",
				Function: modelclient.ToolDefinitionFunc{
					Name:        declaration.Name,
					Description: declaration.Description,
					Parameters:  parameters,
				},
			})
		}
	}
	return out, nil
}

func functionParametersMap(declaration *genai.FunctionDeclaration) (map[string]any, error) {
	if declaration.ParametersJsonSchema != nil {
		return jsonObject(declaration.ParametersJsonSchema)
	}
	if declaration.Parameters != nil {
		return jsonObject(declaration.Parameters)
	}
	return nil, nil
}

func jsonObject(value any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func adkTextContent(content *genai.Content) (string, error) {
	if content == nil {
		return "", nil
	}
	var parts []string
	for _, part := range content.Parts {
		if part == nil || part.Thought {
			continue
		}
		if part.Text == "" {
			return "", errors.New("non-text system instruction is unsupported")
		}
		parts = append(parts, part.Text)
	}
	return strings.Join(parts, ""), nil
}

func modelClientCompletionToADK(completion modelclient.Completion) (*model.LLMResponse, error) {
	parts := make([]*genai.Part, 0, 1+len(completion.ToolCalls))
	if completion.Content != "" {
		parts = append(parts, genai.NewPartFromText(completion.Content))
	}
	for _, call := range completion.ToolCalls {
		args := map[string]any{}
		if strings.TrimSpace(call.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				return nil, fmt.Errorf("%w: invalid tool arguments for %s: %v", modelclient.ErrMalformedResponse, call.Function.Name, err)
			}
		}
		part := genai.NewPartFromFunctionCall(call.Function.Name, args)
		part.FunctionCall.ID = call.ID
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return nil, modelclient.ErrEmptyResponse
	}
	var usageMetadata *genai.GenerateContentResponseUsageMetadata
	if completion.Usage != nil {
		usageMetadata = tokenUsageToADKMetadata(*completion.Usage)
	}
	return &model.LLMResponse{
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: parts},
		UsageMetadata: usageMetadata,
		TurnComplete:  true,
	}, nil
}

func normalizedProviderType(provider string) string {
	trimmed := strings.TrimSpace(provider)
	if trimmed == "" {
		return "openai_compatible"
	}
	return trimmed
}

func buildModelResponseMetadata(route store.ModelRoute, usageSource TokenUsageSource) map[string]any {
	meta := map[string]any{
		tokenUsageSourceKey: string(usageSource),
		modelProviderKey:    normalizedProviderType(route.ProviderType),
	}
	if route.ID != uuid.Nil {
		meta[modelRouteIDKey] = route.ID.String()
	}
	return meta
}

// resolveADKTokenUsage 优先采用服务商返回的真实统计（actual）；
// 若中转未返回 Usage，则回退到本地字符估算（estimated）并写入 UsageMetadata，
// 确保压缩后的首次模型调用也能刷新 ADK Session 的 PromptTokenCount 基线。
func resolveADKTokenUsage(
	req *model.LLMRequest,
	completion modelclient.Completion,
) (modelclient.TokenUsage, TokenUsageSource, *genai.GenerateContentResponseUsageMetadata) {
	if completion.Usage != nil {
		actual := *completion.Usage
		if actual.InputTokens > 0 || actual.OutputTokens > 0 || actual.CachedInputTokens > 0 || actual.ReasoningOutputTokens > 0 {
			return actual, TokenUsageSourceActual, tokenUsageToADKMetadata(actual)
		}
	}
	estimatedInput := estimateADKRequestTokens(req)
	estimatedOutput := estimateCompletionOutputTokens(completion)
	if estimatedInput == 0 && estimatedOutput == 0 {
		return modelclient.TokenUsage{}, TokenUsageSourceUnknown, nil
	}
	estimated := modelclient.TokenUsage{
		InputTokens:  estimatedInput,
		OutputTokens: estimatedOutput,
	}
	return estimated, TokenUsageSourceEstimated, tokenUsageToADKMetadata(estimated)
}

func tokenUsageToADKMetadata(usage modelclient.TokenUsage) *genai.GenerateContentResponseUsageMetadata {
	input := max(usage.InputTokens, 0)
	output := max(usage.OutputTokens, 0)
	cached := max(usage.CachedInputTokens, 0)
	reasoning := max(usage.ReasoningOutputTokens, 0)
	if input == 0 && output == 0 && cached == 0 && reasoning == 0 {
		return nil
	}
	totalOutput := max(output, reasoning)
	return &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        int32(input),
		CandidatesTokenCount:    int32(output),
		CachedContentTokenCount: int32(cached),
		ThoughtsTokenCount:      int32(reasoning),
		TotalTokenCount:         int32(input + totalOutput),
	}
}

const (
	toolDefinitionJSONOverhead  = `{"type":"function","function":{"name":"","description":"","parameters":}}`
	defaultToolParametersSchema = `{"type":"object","properties":{}}`
)

func estimateADKRequestTokens(req *model.LLMRequest) int {
	if req == nil {
		return 0
	}
	units := 0
	if req.Config != nil {
		units += estimateContentTokenUnits(req.Config.SystemInstruction)
		units += estimateToolsTokenUnits(req.Config)
		units += estimateJSONValueTokenUnits(req.Config.ResponseJsonSchema)
	}
	allowedToolImages := selectRecentToolImageTargets(req.Contents, maxPromptToolImageCount)
	for contentIdx, content := range req.Contents {
		if content == nil {
			continue
		}
		units += estimateContentTokenUnitsWithImages(content, false)
		for partIdx, part := range content.Parts {
			if part == nil || part.Thought || part.FunctionResponse == nil {
				continue
			}
			if allowedToolImages[toolPartKey{contentIdx: contentIdx, partIdx: partIdx}] {
				if imgCount := countFunctionResponseImages(part.FunctionResponse); imgCount > 0 {
					units += imgCount * estimatedImageTokens * estimatedTokenScale
				}
			}
		}
	}
	return unitsToEstimatedTokens(units)
}

func estimateContentTokenUnits(content *genai.Content) int {
	return estimateContentTokenUnitsWithImages(content, true)
}

func estimateContentTokenUnitsWithImages(content *genai.Content, includeToolImages bool) int {
	if content == nil {
		return 0
	}
	units := 0
	for _, part := range content.Parts {
		if part == nil || part.Thought {
			continue
		}
		units += estimateTextTokenUnits(part.Text)
		if part.InlineData != nil && isImagePartBlob(part.InlineData) {
			units += estimatedImageTokens * estimatedTokenScale
		}
		if part.FileData != nil && isImageFileData(part.FileData) {
			units += estimatedImageTokens * estimatedTokenScale
		}
		if fc := part.FunctionCall; fc != nil {
			units += estimateTextTokenUnits(fc.Name) + estimateJSONValueTokenUnits(fc.Args)
		}
		if fr := part.FunctionResponse; fr != nil {
			respForEstimate := fr.Response
			if respForEstimate != nil {
				if _, hasInline := respForEstimate[imageInlineFieldKey]; hasInline {
					stripped := make(map[string]any, len(respForEstimate))
					for k, v := range respForEstimate {
						if k != imageInlineFieldKey {
							stripped[k] = v
						}
					}
					respForEstimate = stripped
				}
			}
			units += estimateTextTokenUnits(fr.Name) + estimateJSONValueTokenUnits(respForEstimate)
			if includeToolImages {
				if imgCount := countFunctionResponseImages(fr); imgCount > 0 {
					units += imgCount * estimatedImageTokens * estimatedTokenScale
				}
			}
		}
	}
	return units
}

func estimateToolsTokenUnits(config *genai.GenerateContentConfig) int {
	if config == nil || len(config.Tools) == 0 {
		return 0
	}
	overhead := estimateTextTokenUnits(toolDefinitionJSONOverhead)
	defaultSchema := estimateTextTokenUnits(defaultToolParametersSchema)
	units := 0
	for _, group := range config.Tools {
		if group == nil {
			continue
		}
		for _, decl := range group.FunctionDeclarations {
			if decl == nil {
				continue
			}
			units += overhead + estimateTextTokenUnits(decl.Name) + estimateTextTokenUnits(decl.Description)
			switch {
			case decl.ParametersJsonSchema != nil:
				units += estimateJSONValueTokenUnits(decl.ParametersJsonSchema)
			case decl.Parameters != nil:
				units += estimateJSONValueTokenUnits(decl.Parameters)
			default:
				units += defaultSchema
			}
		}
	}
	return units
}

func estimateCompletionOutputTokens(completion modelclient.Completion) int {
	units := estimateTextTokenUnits(completion.Content)
	for _, call := range completion.ToolCalls {
		units += estimateTextTokenUnits(call.Function.Name) + estimateTextTokenUnits(call.Function.Arguments)
	}
	return unitsToEstimatedTokens(units)
}

func estimateTextTokenUnits(s string) int {
	if s == "" {
		return 0
	}
	units := 0
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			units += asciiCharTokenWeight
			i++
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		units += nonASCIICharTokenWeight
		i += size
	}
	return units
}

func estimateJSONValueTokenUnits(v any) int {
	if v == nil {
		return 0
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	units := 0
	for i := 0; i < len(raw); {
		if raw[i] < utf8.RuneSelf {
			units += asciiCharTokenWeight
			i++
			continue
		}
		_, size := utf8.DecodeRune(raw[i:])
		units += nonASCIICharTokenWeight
		i += size
	}
	return units
}

func unitsToEstimatedTokens(units int) int {
	if units <= 0 {
		return 0
	}
	return (units + estimatedTokenScale - 1) / estimatedTokenScale
}
