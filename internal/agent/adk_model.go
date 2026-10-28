package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentconfig"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store/db"
)

// adkRouteModel adapts CycleDock's production model transports and ordered
// credential routes to ADK's model.LLM interface. ADK owns the conversation and
// tool loop; CycleDock keeps credential refresh, route failover and live-text
// semantics.
type adkRouteModel struct {
	client      *modelclient.Client
	modelAuth   *modelauth.Service
	live        *realtime.Hub
	workspaceID uuid.UUID
	cardID      uuid.UUID
	attemptID   uuid.UUID
	routes      []db.WorkspaceModelRoute

	mu          sync.Mutex
	activeRoute int
	modelCalls  int
}

func newADKRouteModel(
	client *modelclient.Client,
	auth *modelauth.Service,
	live *realtime.Hub,
	workspaceID, cardID, attemptID uuid.UUID,
	routes []db.WorkspaceModelRoute,
) (model.LLM, error) {
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
		client: client, modelAuth: auth, live: live,
		workspaceID: workspaceID, cardID: cardID, attemptID: attemptID,
		routes: enabled,
	}, nil
}

func (m *adkRouteModel) Name() string {
	return "cycledock-route"
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

		var lastErr error
		for i := start; i < len(m.routes); i++ {
			config, err := m.routeConfig(ctx, m.routes[i])
			if err != nil {
				if !isRetryableModelRouteError(ctx, err) {
					yield(nil, err)
					return
				}
				lastErr = err
				if i+1 < len(m.routes) {
					m.publishFailover(false)
				}
				continue
			}

			config.OutputSchema = outputSchema

			streamed := false
			if m.live != nil {
				// 新一轮模型回复从独立消息开始，不串接上一条工具前的说明。
				m.live.Publish(m.cardID, realtime.Event{
					Type: "attempt.text.reset", AttemptID: m.attemptID.String(),
				})
				m.publishStage("正在生成回复")
				config.OnTextDelta = func(text string) {
					streamed = true
					m.live.Publish(m.cardID, realtime.Event{
						Type: "attempt.text.delta", AttemptID: m.attemptID.String(), Text: text,
					})
				}
			}
			// Keep provider streaming when a browser is attached, but only yield the
			// final response to ADK. Partial text is ephemeral UI state and must not
			// become durable Session events.
			_ = stream
			completion, err := m.client.Complete(ctx, config, messages, tools)
			if err == nil {
				if len(completion.ToolCalls) > 0 && modelCall > maxRunToolRounds {
					yield(nil, errRunawayToolLoop)
					return
				}
				response, convertErr := modelClientCompletionToADK(completion)
				if convertErr != nil {
					yield(nil, convertErr)
					return
				}
				m.mu.Lock()
				m.activeRoute = i
				m.mu.Unlock()
				yield(response, nil)
				return
			}
			if !isRetryableModelRouteError(ctx, err) {
				yield(nil, err)
				return
			}
			lastErr = err
			if i+1 < len(m.routes) {
				m.publishFailover(streamed)
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
		apiKey, err := secretFromEnv(route.ApiKeyEnv, "model API key")
		if err != nil {
			return modelclient.Config{}, err
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

func (m *adkRouteModel) publishFailover(resetText bool) {
	if m.live == nil {
		return
	}
	if resetText {
		m.live.Publish(m.cardID, realtime.Event{
			Type: "attempt.text.reset", AttemptID: m.attemptID.String(),
		})
	}
	m.publishStage("当前模型不可用，正在切换备用模型")
}

func (m *adkRouteModel) publishStage(stage string) {
	if m.live == nil {
		return
	}
	m.live.Publish(m.cardID, realtime.Event{
		Type: "attempt.stage", AttemptID: m.attemptID.String(), Stage: stage,
	})
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
	return &model.LLMResponse{
		Content:      &genai.Content{Role: string(genai.RoleModel), Parts: parts},
		TurnComplete: true,
	}, nil
}
