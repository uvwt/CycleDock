package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/mcpclient"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/nexusdock"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

var (
	errWaitingHuman          = errors.New("agent run is waiting for human")
	errMalformedModelResult  = errors.New("model returned an invalid final result")
	errMaxToolRoundsExceeded = errors.New("model exceeded max tool rounds")
)

type RunnerConfig struct {
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	ModelAuth         *modelauth.Service
	NexusDock         *nexusdock.Service
	Live              *realtime.Hub
}

type Runner struct {
	store             *store.Store
	model             *modelclient.Client
	modelAuth         *modelauth.Service
	nexusDock         *nexusdock.Service
	live              *realtime.Hub
	logger            *slog.Logger
	heartbeatInterval time.Duration
	leaseDuration     time.Duration
}

func NewRunner(dataStore *store.Store, model *modelclient.Client, logger *slog.Logger, config RunnerConfig) *Runner {
	if model == nil {
		model = modelclient.New()
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 30 * time.Second
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = store.DefaultClaimLease
	}
	return &Runner{
		store: dataStore, model: model, logger: logger,
		modelAuth:         config.ModelAuth,
		nexusDock:         config.NexusDock,
		live:              config.Live,
		heartbeatInterval: config.HeartbeatInterval,
		leaseDuration:     config.LeaseDuration,
	}
}

func (r *Runner) Run(ctx context.Context, claim store.BuiltInRunClaim) (runErr error) {
	if r.live != nil {
		r.live.Publish(claim.CardID, realtime.Event{Type: "run.started", RunID: claim.RunID.String(), StartedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		defer func() {
			eventType := "run.finished"
			if runErr != nil {
				eventType = "run.failed"
			}
			r.live.Publish(claim.CardID, realtime.Event{Type: eventType, RunID: claim.RunID.String()})
		}()
	}
	settings, err := r.store.GetAgentSettings(ctx, claim.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load agent settings: %w", err)
	}
	if !settings.Enabled || settings.SourceID != claim.SourceID {
		return errors.New("built-in agent settings are disabled or changed")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatErrors := make(chan error, 1)
	go r.heartbeat(runCtx, claim, heartbeatErrors, cancel)

	runContext, err := r.store.LoadAgentRunContext(
		runCtx, claim.WorkspaceID, claim.RunID, claim.SourceID, 50,
	)
	if err != nil {
		return err
	}
	if runContext.CardKind == domain.CardKindTodo && runContext.TodoState != string(domain.TodoStateWorking) {
		if err := r.store.MoveTodo(
			runCtx, claim.WorkspaceID, claim.CardID, claim.SourceID, domain.TodoStateWorking,
		); err != nil {
			return fmt.Errorf("move card to working: %w", err)
		}
		// 状态变化属于 durable context；重新读取，保证模型看到自己真正开始执行时的最新 Card。
		runContext, err = r.store.LoadAgentRunContext(
			runCtx, claim.WorkspaceID, claim.RunID, claim.SourceID, 50,
		)
		if err != nil {
			return err
		}
	}

	connections, err := r.store.ListEnabledMCPConnections(runCtx, claim.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load MCP connections: %w", err)
	}
	mcpConnections := make([]mcpclient.Connection, 0, len(connections))
	for _, connection := range connections {
		token, err := secretFromEnv(connection.BearerTokenEnv, "MCP bearer token for "+connection.Name)
		if err != nil {
			return err
		}
		mcpConnections = append(mcpConnections, mcpclient.Connection{
			Name: connection.Name, URL: connection.Url, BearerToken: token,
			Timeout: time.Duration(connection.TimeoutMs) * time.Millisecond,
		})
	}
	nexusDockMounted := false
	if r.nexusDock != nil {
		connection, err := r.nexusDock.Runtime(runCtx, claim.WorkspaceID)
		switch {
		case errors.Is(err, nexusdock.ErrNotConnected):
			// First-party remote devices are optional for each Workspace.
		case err != nil:
			return fmt.Errorf("load NexusDock remote device connection: %w", err)
		default:
			mcpConnections = append(mcpConnections, mcpclient.Connection{
				Name:            firstPartyNexusDockServerName,
				URL:             connection.URL,
				BearerToken:     connection.AccessToken,
				Timeout:         30 * time.Second,
				NativeToolNames: true,
				HiddenRemoteTools: map[string]bool{
					"agentdock_context": true,
				},
			})
			nexusDockMounted = true
		}
	}
	mcpSessions, err := mcpclient.Connect(runCtx, mcpConnections)
	if err != nil {
		return err
	}
	defer mcpSessions.Close()

	messages := BuildContext(runContext, settings.WorkspaceAgentSetting)
	if nexusDockMounted {
		bootstrap, err := nexusDockExecutionTargets(runCtx, mcpSessions)
		if err != nil {
			return fmt.Errorf("bootstrap NexusDock remote devices: %w", err)
		}
		if bootstrap != "" {
			messages[0].Content += "\n\n" + bootstrap
		}
	}
	tools, err := mergeToolDefinitions(internalToolDefinitions(), mcpSessions.ToolDefinitions())
	if err != nil {
		return err
	}
	routes := make([]db.WorkspaceModelRoute, 0, len(settings.Routes))
	for _, route := range settings.Routes {
		if route.Enabled {
			routes = append(routes, route)
		}
	}
	if len(routes) == 0 {
		return errors.New("built-in agent has no enabled model route")
	}
	activeRoute := 0

	for round := int32(0); ; round++ {
		if err := heartbeatFailure(heartbeatErrors); err != nil {
			return err
		}
		response, usedRoute, err := r.completeWithFailover(
			runCtx, claim, settings, routes, activeRoute, messages, tools,
		)
		if err != nil {
			if heartbeatErr := heartbeatFailure(heartbeatErrors); heartbeatErr != nil {
				return heartbeatErr
			}
			return err
		}
		activeRoute = usedRoute
		if len(response.ToolCalls) == 0 {
			result := strings.TrimSpace(response.Content)
			if result == "" {
				return errMalformedModelResult
			}
			return r.store.FinishRun(runCtx, store.FinishRunParams{
				WorkspaceID: claim.WorkspaceID, RunID: claim.RunID, RunnerSourceID: claim.SourceID,
				Result: result, IdempotencyKey: "builtin-finish:" + claim.RunID.String(),
			})
		}
		if round >= settings.MaxToolRounds {
			return fmt.Errorf("%w (%d)", errMaxToolRoundsExceeded, settings.MaxToolRounds)
		}

		messages = append(messages, modelclient.Message{
			Role: "assistant", Content: response.Content, ToolCalls: response.ToolCalls,
		})
		for _, call := range response.ToolCalls {
			if r.live != nil {
				r.live.Publish(claim.CardID, realtime.Event{Type: "run.stage", RunID: claim.RunID.String(), Stage: "正在执行工具"})
			}
			arguments, err := decodeToolArguments(call.Function.Arguments)
			if err != nil {
				messages = append(messages, toolMessage(call, `{"error":"invalid tool arguments"}`))
				continue
			}
			var result string
			if isInternalTool(call.Function.Name) {
				result, err = r.callInternalTool(runCtx, claim, runContext, call.Function.Name, arguments)
				if errors.Is(err, errWaitingHuman) {
					return nil
				}
				if errors.Is(err, domain.ErrRunNotCurrent) || errors.Is(err, context.Canceled) {
					return err
				}
			} else if mcpSessions.HasTool(call.Function.Name) {
				result, err = mcpSessions.Call(runCtx, call.Function.Name, arguments)
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) && runCtx.Err() != nil {
					return err
				}
				if err != nil {
					return fmt.Errorf("MCP tool call failed: %w", err)
				}
			} else {
				err = fmt.Errorf("unknown tool %q", call.Function.Name)
			}
			if err != nil {
				result = toolErrorResult(err)
			}
			messages = append(messages, toolMessage(call, result))
		}
	}
}

func (r *Runner) completeWithFailover(
	ctx context.Context,
	claim store.BuiltInRunClaim,
	settings store.AgentSettings,
	routes []db.WorkspaceModelRoute,
	start int,
	messages []modelclient.Message,
	tools []modelclient.ToolDefinition,
) (modelclient.Completion, int, error) {
	var lastErr error
	for i := start; i < len(routes); i++ {
		config, err := r.modelConfig(ctx, claim.WorkspaceID, settings, routes[i])
		if err != nil {
			if !isRetryableModelRouteError(ctx, err) {
				return modelclient.Completion{}, i, err
			}
			lastErr = err
			if i+1 < len(routes) {
				r.publishModelFailover(claim, false)
			}
			continue
		}
		streamed := false
		if r.live != nil {
			r.live.Publish(claim.CardID, realtime.Event{Type: "run.stage", RunID: claim.RunID.String(), Stage: "正在生成回复"})
			config.OnTextDelta = func(text string) {
				streamed = true
				r.live.Publish(claim.CardID, realtime.Event{Type: "run.text.delta", RunID: claim.RunID.String(), Text: text})
			}
		}
		response, err := r.model.Complete(ctx, config, messages, tools)
		if err == nil {
			return response, i, nil
		}
		if !isRetryableModelRouteError(ctx, err) {
			return modelclient.Completion{}, i, err
		}
		lastErr = err
		if i+1 < len(routes) {
			r.publishModelFailover(claim, streamed)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no model route was available")
	}
	return modelclient.Completion{}, len(routes) - 1, fmt.Errorf("all configured model routes failed: %w", lastErr)
}

func (r *Runner) publishModelFailover(claim store.BuiltInRunClaim, resetText bool) {
	if r.live == nil {
		return
	}
	if resetText {
		r.live.Publish(claim.CardID, realtime.Event{Type: "run.text.reset", RunID: claim.RunID.String()})
	}
	r.live.Publish(claim.CardID, realtime.Event{
		Type: "run.stage", RunID: claim.RunID.String(), Stage: "当前模型不可用，正在切换备用模型",
	})
}

func isRetryableModelRouteError(ctx context.Context, err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, modelauth.ErrUnavailable) ||
		errors.Is(err, modelauth.ErrNotConnected) ||
		errors.Is(err, modelauth.ErrReauthRequired) ||
		errors.Is(err, modelauth.ErrCredentialRefresh) ||
		errors.Is(err, modelclient.ErrMalformedResponse) ||
		errors.Is(err, modelclient.ErrEmptyResponse) {
		return true
	}
	var apiErr *modelclient.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 401 ||
			apiErr.StatusCode == 403 ||
			apiErr.StatusCode == 404 ||
			apiErr.StatusCode == 408 ||
			apiErr.StatusCode == 429 ||
			apiErr.StatusCode >= 500
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func (r *Runner) modelConfig(
	ctx context.Context,
	workspaceID uuid.UUID,
	settings store.AgentSettings,
	route db.WorkspaceModelRoute,
) (modelclient.Config, error) {
	timeout := time.Duration(settings.RequestTimeoutMs) * time.Millisecond
	switch route.ProviderType {
	case "", "openai_compatible":
		apiKey, err := secretFromEnv(route.ApiKeyEnv, "model API key")
		if err != nil {
			return modelclient.Config{}, err
		}
		return modelclient.Config{
			BaseURL: route.ProviderBaseUrl, Model: route.Model, APIKey: apiKey,
			Timeout: timeout, Temperature: 0,
		}, nil
	case string(modelauth.ProviderChatGPT):
		if r.modelAuth != nil && route.CredentialID.Valid {
			return r.modelAuth.ModelConfigForCredential(
				ctx, workspaceID, modelauth.ProviderChatGPT, route.CredentialID.UUID, route.Model, timeout,
			)
		}
		return modelclient.Config{}, modelauth.ErrNotConnected
	case string(modelauth.ProviderGrok):
		if r.modelAuth != nil && route.CredentialID.Valid {
			return r.modelAuth.ModelConfigForCredential(
				ctx, workspaceID, modelauth.ProviderGrok, route.CredentialID.UUID, route.Model, timeout,
			)
		}
		return modelclient.Config{}, modelauth.ErrNotConnected
	default:
		return modelclient.Config{}, fmt.Errorf("unsupported model provider %q", route.ProviderType)
	}
}

func (r *Runner) heartbeat(
	ctx context.Context,
	claim store.BuiltInRunClaim,
	failures chan<- error,
	cancel context.CancelFunc,
) {
	ticker := time.NewTicker(r.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := r.store.Heartbeat(
				ctx, claim.WorkspaceID, claim.RunID, claim.SourceID, r.leaseDuration,
			)
			if err == nil {
				continue
			}
			select {
			case failures <- err:
			default:
			}
			cancel()
			return
		}
	}
}

func heartbeatFailure(failures <-chan error) error {
	select {
	case err := <-failures:
		return err
	default:
		return nil
	}
}

func secretFromEnv(name, label string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s environment variable %s is not set", label, name)
	}
	return value, nil
}

func decodeToolArguments(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
		return nil, err
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	return arguments, nil
}

func toolMessage(call modelclient.ToolCall, content string) modelclient.Message {
	return modelclient.Message{
		Role: "tool", ToolCallID: call.ID, Name: call.Function.Name, Content: content,
	}
}

func toolErrorResult(err error) string {
	message := strings.TrimSpace(err.Error())
	if len(message) > 1000 {
		message = message[:1000] + "..."
	}
	raw, _ := json.Marshal(map[string]any{"error": message})
	return string(raw)
}

func isInternalTool(name string) bool {
	switch name {
	case "cycledock_set_discussion_metadata",
		"cycledock_generate_work",
		"cycledock_report_progress",
		"cycledock_add_finding",
		"cycledock_check_in",
		"cycledock_wait_for_human",
		"cycledock_confirm_card":
		return true
	default:
		return false
	}
}

func (r *Runner) callInternalTool(
	ctx context.Context,
	claim store.BuiltInRunClaim,
	runContext store.AgentRunContext,
	name string,
	arguments map[string]any,
) (string, error) {
	switch name {
	case "cycledock_set_discussion_metadata":
		if runContext.CardKind != domain.CardKindDiscussion {
			return "", errors.New("discussion metadata is only valid for Discussion cards")
		}
		var input struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if err := decodeArguments(arguments, &input); err != nil {
			return "", err
		}
		if err := r.store.SetDiscussionMetadataForRun(
			ctx, claim.WorkspaceID, claim.RunID, claim.SourceID,
			input.Title, input.Description,
		); err != nil {
			return "", err
		}
		return encodeToolResult(map[string]any{
			"ok": true, "title": strings.TrimSpace(input.Title),
			"description": strings.TrimSpace(input.Description),
		}), nil

	case "cycledock_generate_work":
		if runContext.CardKind != domain.CardKindDiscussion ||
			runContext.DiscussionState != string(domain.DiscussionStateActive) {
			return "", errors.New("generate_work is only valid for an active Discussion")
		}
		var input struct {
			Kind                  string `json:"kind"`
			Title                 string `json:"title"`
			Description           string `json:"description"`
			RequiredConfirmations int32  `json:"required_confirmations"`
			IdempotencyKey        string `json:"idempotency_key"`
		}
		if err := decodeArguments(arguments, &input); err != nil {
			return "", err
		}
		result, err := r.store.GenerateWorkFromDiscussion(ctx, store.GenerateWorkParams{
			WorkspaceID: claim.WorkspaceID, DiscussionID: claim.CardID, SourceID: claim.SourceID,
			Kind: domain.CardKind(input.Kind), Title: input.Title, Description: input.Description,
			RequiredConfirmations: input.RequiredConfirmations, IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return "", err
		}
		return encodeToolResult(map[string]any{
			"ok": true, "card_id": result.Card.ID.String(), "kind": result.Card.Kind,
			"title": result.Card.Title, "reused": result.Reused,
		}), nil

	case "cycledock_report_progress":
		var input struct {
			Stage          string `json:"stage"`
			Summary        string `json:"summary"`
			EventContent   string `json:"event_content"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := decodeArguments(arguments, &input); err != nil {
			return "", err
		}
		eventID, reused, err := r.store.ReportProgress(ctx, store.ProgressParams{
			WorkspaceID: claim.WorkspaceID, RunID: claim.RunID, RunnerSourceID: claim.SourceID,
			Stage: input.Stage, Summary: input.Summary, EventContent: input.EventContent,
			IdempotencyKey: input.IdempotencyKey, LeaseDuration: r.leaseDuration,
		})
		if err != nil {
			return "", err
		}
		return encodeToolResult(map[string]any{"ok": true, "event_id": nullableUUID(eventID), "reused": reused}), nil

	case "cycledock_add_finding":
		var input struct {
			Content        string `json:"content"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := decodeArguments(arguments, &input); err != nil {
			return "", err
		}
		if strings.TrimSpace(input.IdempotencyKey) == "" {
			return "", errors.New("idempotency_key is required")
		}
		eventID, err := r.store.AddRunEvent(ctx, store.RunEventParams{
			WorkspaceID: claim.WorkspaceID, RunID: claim.RunID, RunnerSourceID: claim.SourceID,
			Type: domain.EventTypeFinding, Content: input.Content, IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return "", err
		}
		return encodeToolResult(map[string]any{"ok": true, "event_id": eventID.String()}), nil

	case "cycledock_check_in":
		var input struct {
			Status         string `json:"status"`
			Content        string `json:"content"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := decodeArguments(arguments, &input); err != nil {
			return "", err
		}
		if strings.TrimSpace(input.IdempotencyKey) == "" {
			return "", errors.New("idempotency_key is required")
		}
		status := domain.CheckinStatus(input.Status)
		if status != domain.CheckinHealthy && status != domain.CheckinWarning && status != domain.CheckinCritical {
			return "", errors.New("status must be healthy, warning, or critical")
		}
		eventID, err := r.store.AddRunEvent(ctx, store.RunEventParams{
			WorkspaceID: claim.WorkspaceID, RunID: claim.RunID, RunnerSourceID: claim.SourceID,
			Type: domain.EventTypeCheckin, Content: input.Content, Checkin: &status,
			IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return "", err
		}
		return encodeToolResult(map[string]any{"ok": true, "event_id": eventID.String()}), nil

	case "cycledock_wait_for_human":
		var input struct {
			Stage   string `json:"stage"`
			Summary string `json:"summary"`
		}
		if err := decodeArguments(arguments, &input); err != nil {
			return "", err
		}
		if err := r.store.WaitForHuman(
			ctx, claim.WorkspaceID, claim.RunID, claim.SourceID, input.Stage, input.Summary,
		); err != nil {
			return "", err
		}
		return "", errWaitingHuman

	case "cycledock_confirm_card":
		var input struct {
			Content        string `json:"content"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := decodeArguments(arguments, &input); err != nil {
			return "", err
		}
		if runContext.CardKind != domain.CardKindTodo {
			return "", errors.New("confirm_card is only valid for Todo cards")
		}
		result, err := r.store.ConfirmCard(
			ctx, claim.WorkspaceID, claim.CardID, claim.SourceID,
			input.Content, input.IdempotencyKey,
		)
		if err != nil {
			return "", err
		}
		return encodeToolResult(map[string]any{
			"ok": true, "confirmation_count": result.ConfirmationCount,
			"moved_to_review": result.MovedToReview, "reused": result.Reused,
		}), nil
	default:
		return "", fmt.Errorf("unknown internal tool %q", name)
	}
}

func decodeArguments(input map[string]any, target any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func encodeToolResult(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id.String()
}

func internalToolDefinitions() []modelclient.ToolDefinition {
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{
			"type": "object", "properties": properties, "additionalProperties": false,
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	stringField := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return []modelclient.ToolDefinition{
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_set_discussion_metadata",
				Description: "Set the generated title and description for a new Discussion whose title is still empty. Call this once during the first Discussion Run before answering.",
				Parameters: object(map[string]any{
					"title":       stringField("Concise discussion title derived from the first conversation."),
					"description": stringField("Short durable summary of what the discussion is about."),
				}, "title", "description"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_generate_work",
				Description: "Generate one work Card from the current active Discussion. Classify finite work as todo and long-term, periodic, or continuously monitored work as ongoing. Never call this from Todo or Ongoing cards.",
				Parameters: object(map[string]any{
					"kind":                   map[string]any{"type": "string", "enum": []string{"todo", "ongoing"}},
					"title":                  stringField("Concise generated work title."),
					"description":            stringField("Durable scope and expected outcome for the generated work."),
					"required_confirmations": map[string]any{"type": "integer", "minimum": 1, "description": "Todo confirmation threshold; use 1 unless the discussion requires more."},
					"idempotency_key":        stringField("Stable unique key for this logical generated work item."),
				}, "kind", "title", "description", "idempotency_key"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_report_progress",
				Description: "Update the current Run stage/progress and refresh its lease. Add event_content only when the progress itself is a durable Card fact.",
				Parameters: object(map[string]any{
					"stage":           stringField("Short execution stage."),
					"summary":         stringField("Concise current progress."),
					"event_content":   stringField("Optional durable progress Timeline fact."),
					"idempotency_key": stringField("Required when event_content is set; reuse only for the same logical write."),
				}, "stage", "summary"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_add_finding",
				Description: "Persist a verified finding that future Runs should inherit. Never store raw tool output.",
				Parameters: object(map[string]any{
					"content":         stringField("Distilled durable finding."),
					"idempotency_key": stringField("Stable key for this logical finding."),
				}, "content", "idempotency_key"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_check_in",
				Description: "Persist the latest health fact for an Ongoing Card.",
				Parameters: object(map[string]any{
					"status":          map[string]any{"type": "string", "enum": []string{"healthy", "warning", "critical"}},
					"content":         stringField("Evidence-based health summary."),
					"idempotency_key": stringField("Stable key for this logical check-in."),
				}, "status", "content", "idempotency_key"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_wait_for_human",
				Description: "Stop the current Run in waiting_human when human input is required. Do not continue working after this call.",
				Parameters: object(map[string]any{
					"stage":   stringField("Stage blocked on human input."),
					"summary": stringField("Exactly what input or decision is needed."),
				}, "stage", "summary"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_confirm_card",
				Description: "Confirm verified work on a Working Todo. CycleDock moves to Review only after the required confirmation threshold. A human alone can move Review to Done.",
				Parameters: object(map[string]any{
					"content":         stringField("What was verified."),
					"idempotency_key": stringField("Stable key for this logical confirmation."),
				}, "content", "idempotency_key"),
			},
		},
	}
}

func mergeToolDefinitions(groups ...[]modelclient.ToolDefinition) ([]modelclient.ToolDefinition, error) {
	seen := make(map[string]struct{})
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	merged := make([]modelclient.ToolDefinition, 0, total)
	for _, group := range groups {
		for _, tool := range group {
			name := strings.TrimSpace(tool.Function.Name)
			if name == "" {
				return nil, errors.New("tool definition has empty name")
			}
			if _, exists := seen[name]; exists {
				return nil, fmt.Errorf("tool name collision: %q", name)
			}
			seen[name] = struct{}{}
			merged = append(merged, tool)
		}
	}
	return merged, nil
}
