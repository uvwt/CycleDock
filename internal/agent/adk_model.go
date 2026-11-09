package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentconfig"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type TokenUsageSource string

const (
	TokenUsageSourceUnknown   TokenUsageSource = "unknown"
	TokenUsageSourceActual    TokenUsageSource = "actual"
	TokenUsageSourceEstimated TokenUsageSource = "estimated"

	tokenUsageSourceKey = "cycledock.token_usage_source"
	modelRouteIDKey     = "cycledock.model_route_id"
	modelProviderKey    = "cycledock.model_provider"

	estimatedCharsPerToken = 4
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

// adkRouteModel adapts CycleDock's production model transports and ordered
// credential routes to ADK's model.LLM interface. ADK owns the conversation and
// tool loop; CycleDock keeps credential refresh, route failover and converts
// provider argument deltas into ADK partial responses.
type adkRouteModel struct {
	client       *modelclient.Client
	modelAuth    *modelauth.Service
	store        *store.Store
	workspaceID  uuid.UUID
	routes       []db.WorkspaceModelRoute
	budgetPolicy CompactionBudgetPolicy

	mu              sync.Mutex
	activeRoute     int
	modelCalls      int
	lastObservation RouteCallObservation
	hasObservation  bool
}

func prioritizeRoutes(
	routes []db.WorkspaceModelRoute,
	preferredID uuid.NullUUID,
	kindDefaultID uuid.NullUUID,
	workspaceDefaultID uuid.NullUUID,
) []db.WorkspaceModelRoute {
	if len(routes) == 0 {
		return routes
	}
	routeByID := make(map[uuid.UUID]db.WorkspaceModelRoute, len(routes))
	for _, r := range routes {
		routeByID[r.ID] = r
	}

	ordered := make([]db.WorkspaceModelRoute, 0, len(routes))
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
	routes []db.WorkspaceModelRoute,
) (*adkRouteModel, error) {
	if client == nil {
		client = modelclient.New()
	}
	enabled := make([]db.WorkspaceModelRoute, 0, len(routes))
	for _, route := range routes {
		if route.Enabled {
			enabled = append(enabled, route)
		}
	}
	if len(enabled) == 0 {
		return nil, errors.New("built-in agent has no enabled model route")
	}
	return &adkRouteModel{
		client:       client,
		modelAuth:    auth,
		store:        dataStore,
		workspaceID:  workspaceID,
		routes:       enabled,
		budgetPolicy: DefaultCompactionBudgetPolicy(),
	}, nil
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

func (m *adkRouteModel) ActiveRoute() (db.WorkspaceModelRoute, bool) {
	if m == nil {
		return db.WorkspaceModelRoute{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeRoute >= 0 && m.activeRoute < len(m.routes) {
		return m.routes[m.activeRoute], true
	}
	if len(m.routes) > 0 {
		return m.routes[0], true
	}
	return db.WorkspaceModelRoute{}, false
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
		messages, tools, err := adkRequestToModelClient(req)
		if err != nil {
			yield(nil, err)
			return
		}

		outputSchema, err := adkOutputSchema(req.Config)
		if err != nil {
			yield(nil, err)
			return
		}

		m.mu.Lock()
		start := m.activeRoute
		m.modelCalls++
		modelCall := m.modelCalls
		m.mu.Unlock()

		// 依托 repeatedToolLoop 死循环熔断保障长任务顺利推进；历史中的新 Turn、不同参数或结果都会打断重复计数。
		if repeatedToolLoop(messages) {
			yield(nil, errRunawayToolLoop)
			return
		}

		estimatedPromptTokens := estimateADKRequestTokens(req)
		var lastErr error
		for i := start; i < len(m.routes); i++ {
			routeLimits := ResolveRouteModelLimits(m.routes[i], m.client)
			routeBudget := ComputeRouteCompactionBudget(routeLimits, m.budgetPolicy)
			if capErr := ValidateRoutePromptCapacity(
				m.routes[i],
				routeBudget,
				estimatedPromptTokens,
				i > start,
				m.budgetPolicy,
			); capErr != nil {
				lastErr = capErr
				if i+1 < len(m.routes) && stream && !yield(modelStageResponse("备用模型上下文窗口不足，正在尝试下一模型"), nil) {
					return
				}
				continue
			}

			config, err := m.routeConfig(ctx, m.routes[i])
			if err != nil {
				if !isRetryableModelRouteError(ctx, err) {
					yield(nil, err)
					return
				}
				lastErr = err
				if i+1 < len(m.routes) && stream && !yield(modelStageResponse("当前模型不可用，正在切换备用模型"), nil) {
					return
				}
				continue
			}

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
				completion, err := m.client.Complete(requestCtx, config, messages, tools)
				cancel()
				if stopped {
					return
				}
				var response *model.LLMResponse
				if err == nil {
					response, err = modelClientCompletionToADK(completion)
				}
				if err == nil {
					matchedRoute := m.routes[i]
					usage, usageSource, usageMetadata := resolveADKTokenUsage(req, completion)
					response.UsageMetadata = usageMetadata
					response.ModelVersion = strings.TrimSpace(matchedRoute.Model)
					response.CustomMetadata = buildModelResponseMetadata(matchedRoute, usageSource)

					m.mu.Lock()
					m.activeRoute = i
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
					yield(response, nil)
					return
				}
				if modelclient.IsContextTooLargeError(err) {
					lastErr = fmt.Errorf("%w: %v", ErrRouteContextCapacityExceeded, err)
					break
				}
				if !isRetryableModelRouteError(ctx, err) {
					yield(nil, err)
					return
				}
				lastErr = err
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

			if i+1 < len(m.routes) && stream && !yield(modelStageResponse("当前模型不可用，正在切换备用模型"), nil) {
				return
			}
		}

		if lastErr == nil {
			lastErr = errors.New("no model route was available")
		}
		yield(nil, fmt.Errorf("all configured model routes failed: %w", lastErr))
	}
}

func (m *adkRouteModel) routeConfig(
	ctx context.Context,
	route db.WorkspaceModelRoute,
) (modelclient.Config, error) {
	timeout := agentconfig.DefaultModelRequestTimeout
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
	if req == nil {
		return nil, nil, errors.New("ADK model request is nil")
	}

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

	for _, content := range req.Contents {
		if content == nil {
			continue
		}
		var textParts []string
		var calls []modelclient.ToolCall
		var toolResponses []modelclient.Message
		for _, part := range content.Parts {
			if part == nil || part.Thought {
				continue
			}
			switch {
			case part.Text != "":
				textParts = append(textParts, part.Text)
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
				rawResponse, err := json.Marshal(part.FunctionResponse.Response)
				if err != nil {
					return nil, nil, fmt.Errorf("encode function response %q: %w", part.FunctionResponse.Name, err)
				}
				toolResponses = append(toolResponses, modelclient.Message{
					Role:       "tool",
					ToolCallID: part.FunctionResponse.ID,
					Name:       part.FunctionResponse.Name,
					Content:    string(rawResponse),
				})
			default:
				return nil, nil, errors.New("CycleDock ADK model adapter does not yet support non-text media parts")
			}
		}

		if len(toolResponses) > 0 {
			if len(textParts) > 0 || len(calls) > 0 {
				return nil, nil, errors.New("mixed function responses and ordinary content are unsupported")
			}
			messages = append(messages, toolResponses...)
			continue
		}

		role := "user"
		if content.Role == string(genai.RoleModel) {
			role = "assistant"
		}
		messages = append(messages, modelclient.Message{
			Role:      role,
			Content:   strings.Join(textParts, ""),
			ToolCalls: calls,
		})
	}

	tools, err := adkToolsToModelClient(req.Config)
	if err != nil {
		return nil, nil, err
	}
	return messages, tools, nil
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

func buildModelResponseMetadata(route db.WorkspaceModelRoute, usageSource TokenUsageSource) map[string]any {
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

func estimateADKRequestTokens(req *model.LLMRequest) int {
	if req == nil || len(req.Contents) == 0 {
		return 0
	}
	chars := 0
	for _, content := range req.Contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part == nil || part.Thought {
				continue
			}
			chars += utf8.RuneCountInString(part.Text)
			if fc := part.FunctionCall; fc != nil {
				chars += utf8.RuneCountInString(fc.Name) + estimateJSONMapChars(fc.Args)
			}
			if fr := part.FunctionResponse; fr != nil {
				chars += utf8.RuneCountInString(fr.Name) + estimateJSONMapChars(fr.Response)
			}
		}
	}
	return charsToEstimatedTokens(chars)
}

func estimateCompletionOutputTokens(completion modelclient.Completion) int {
	chars := utf8.RuneCountInString(completion.Content)
	for _, call := range completion.ToolCalls {
		chars += utf8.RuneCountInString(call.Function.Name) + utf8.RuneCountInString(call.Function.Arguments)
	}
	return charsToEstimatedTokens(chars)
}

func estimateJSONMapChars(v map[string]any) int {
	if len(v) == 0 {
		return 0
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return utf8.RuneCount(raw)
}

func charsToEstimatedTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + estimatedCharsPerToken - 1) / estimatedCharsPerToken
}
