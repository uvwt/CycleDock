package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	adkagent "google.golang.org/adk/v2/agent"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/trigger"
)

type triggerCreateCache struct {
	mu      sync.Mutex
	byKey   map[string]uuid.UUID
}

func newTriggerCreateCache() *triggerCreateCache {
	return &triggerCreateCache{byKey: make(map[string]uuid.UUID)}
}

func (c *triggerCreateCache) get(key string) (uuid.UUID, bool) {
	if c == nil || key == "" {
		return uuid.Nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.byKey[key]
	return id, ok
}

func (c *triggerCreateCache) set(key string, id uuid.UUID) {
	if c == nil || key == "" || id == uuid.Nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byKey[key] = id
}

type listTriggersToolInput struct {
	Provider    string `json:"provider"`
	EnabledOnly *bool  `json:"enabled_only"`
}

type createTriggerToolInput struct {
	Provider        string                `json:"provider"`
	Name            string                `json:"name"`
	AuthMode        string                `json:"auth_mode"`
	SignatureHeader string                `json:"signature_header"`
	Enabled         *bool                 `json:"enabled"`
	RoutingMode     string                `json:"routing_mode"`
	TargetProjectID string                `json:"target_project_id"`
	TargetCardID    string                `json:"target_card_id"`
	DispatchMode    string                `json:"dispatch_mode"`
	CooldownSeconds *int                  `json:"cooldown_seconds"`
	FilterConfig    *trigger.FilterConfig `json:"filter_config"`
	IdempotencyKey  string                `json:"idempotency_key"`
}

type updateTriggerToolInput struct {
	TriggerID       string                `json:"trigger_id"`
	Name            *string               `json:"name"`
	Enabled         *bool                 `json:"enabled"`
	SignatureHeader *string               `json:"signature_header"`
	RoutingMode     *string               `json:"routing_mode"`
	TargetProjectID *string               `json:"target_project_id"`
	TargetCardID    *string               `json:"target_card_id"`
	DispatchMode    *string               `json:"dispatch_mode"`
	CooldownSeconds *int                  `json:"cooldown_seconds"`
	FilterConfig    *trigger.FilterConfig `json:"filter_config"`
}

type deleteTriggerToolInput struct {
	TriggerID string `json:"trigger_id"`
}

func isTriggerWriteToolName(name string) bool {
	switch name {
	case "cycledock_create_trigger", "cycledock_update_trigger", "cycledock_delete_trigger":
		return true
	default:
		return false
	}
}

func triggerWriteAllowedForCard(kind domain.CardKind) bool {
	return kind == domain.CardKindDiscussion || kind == domain.CardKindTodo
}

func triggerWebhookPath(triggerID uuid.UUID) string {
	return "/api/v1/triggers/" + triggerID.String() + "/webhook"
}

func triggerWebhookURL(publicBaseURL string, triggerID uuid.UUID) string {
	path := triggerWebhookPath(triggerID)
	base := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if base == "" {
		return path
	}
	return base + path
}

func defaultTriggerName(provider domain.TriggerProvider, filter trigger.FilterConfig) string {
	var prefix string
	switch provider {
	case domain.TriggerProviderGitHub:
		prefix = "GitHub"
	case domain.TriggerProviderCircleCI:
		prefix = "CircleCI"
	default:
		prefix = "Generic"
	}
	if len(filter.Repositories) == 1 && strings.TrimSpace(filter.Repositories[0]) != "" {
		return prefix + " · " + strings.TrimSpace(filter.Repositories[0])
	}
	return prefix + " Webhook"
}

func filterConfigEmpty(cfg trigger.FilterConfig) bool {
	return len(cfg.Events) == 0 &&
		len(cfg.Actions) == 0 &&
		len(cfg.Conclusions) == 0 &&
		len(cfg.Branches) == 0 &&
		len(cfg.Repositories) == 0 &&
		len(cfg.JSONMatches) == 0 &&
		strings.TrimSpace(cfg.MatchKeyPath) == ""
}

func canonicalFilterJSON(cfg trigger.FilterConfig) string {
	cfg.Normalize()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(raw)
}

func triggersMatchForDedup(
	existing store.WorkspaceTrigger,
	name string,
	provider domain.TriggerProvider,
	routingMode domain.TriggerRoutingMode,
	targetProjectID, targetCardID uuid.NullUUID,
	filter trigger.FilterConfig,
) bool {
	if existing.Provider != provider || existing.RoutingMode != routingMode {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(name)) {
		return false
	}
	if existing.TargetProjectID != targetProjectID || existing.TargetCardID != targetCardID {
		return false
	}
	return canonicalFilterJSON(existing.FilterConfig) == canonicalFilterJSON(filter)
}

// triggerToolView 返回给模型的触发器视图：绝不包含密文或明文 webhook_secret。
func triggerToolView(publicBaseURL string, t store.WorkspaceTrigger) map[string]any {
	out := map[string]any{
		"id":               t.ID.String(),
		"name":             t.Name,
		"provider":         string(t.Provider),
		"auth_mode":        string(t.AuthMode),
		"secret_hint":      t.SecretHint,
		"webhook_path":     triggerWebhookPath(t.ID),
		"webhook_url":      triggerWebhookURL(publicBaseURL, t.ID),
		"enabled":          t.Enabled,
		"routing_mode":     string(t.RoutingMode),
		"dispatch_mode":    string(t.DispatchMode),
		"cooldown_seconds": t.CooldownSeconds,
		"created_at":       t.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":       t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.SignatureHeader != "" {
		out["signature_header"] = t.SignatureHeader
	}
	if t.TargetProjectID.Valid {
		out["target_project_id"] = t.TargetProjectID.UUID.String()
	}
	if t.TargetProjectName != "" {
		out["target_project_name"] = t.TargetProjectName
	}
	if t.TargetCardID.Valid {
		out["target_card_id"] = t.TargetCardID.UUID.String()
	}
	if t.TargetCardTitle != "" {
		out["target_card_title"] = t.TargetCardTitle
	}
	if t.LastTriggeredAt != nil {
		out["last_triggered_at"] = t.LastTriggeredAt.UTC().Format(time.RFC3339)
	}
	if t.LastDeliveryStatus != "" {
		out["last_delivery_status"] = string(t.LastDeliveryStatus)
	}
	if t.LastDeliveryAt != nil {
		out["last_delivery_at"] = t.LastDeliveryAt.UTC().Format(time.RFC3339)
	}
	if !filterConfigEmpty(t.FilterConfig) {
		out["filter_config"] = t.FilterConfig
	}
	return out
}

func (r attemptToolRuntime) ensureTriggerWriteAllowed(toolName string) error {
	if r.context.IsBuiltInProactive {
		return fmt.Errorf("%s is disabled during built-in proactive turns", toolName)
	}
	if !triggerWriteAllowedForCard(r.context.CardKind) {
		return fmt.Errorf("%s is only allowed on discussion or todo cards", toolName)
	}
	if r.store == nil {
		return errors.New("store is not configured")
	}
	return nil
}

func (r attemptToolRuntime) listTriggersTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	if r.store == nil {
		return nil, errors.New("store is not configured")
	}
	var input listTriggersToolInput
	if len(args) > 0 {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, fmt.Errorf("invalid list_triggers arguments: %w", err)
		}
	}
	providerFilter := strings.ToLower(strings.TrimSpace(input.Provider))
	enabledOnly := input.EnabledOnly != nil && *input.EnabledOnly

	items, err := r.store.ListTriggers(ctx, r.context.WorkspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if providerFilter != "" && string(item.Provider) != providerFilter {
			continue
		}
		if enabledOnly && !item.Enabled {
			continue
		}
		out = append(out, triggerToolView(r.publicBaseURL, item))
	}
	return map[string]any{
		"ok":                         true,
		"triggers":                   out,
		"count":                      len(out),
		"public_base_url_configured": strings.TrimSpace(r.publicBaseURL) != "",
	}, nil
}

func (r attemptToolRuntime) createTriggerTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	if err := r.ensureTriggerWriteAllowed("cycledock_create_trigger"); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var input createTriggerToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("invalid create_trigger arguments: %w", err)
	}

	provider := domain.TriggerProvider(strings.ToLower(strings.TrimSpace(input.Provider)))
	if provider == "" {
		return nil, errors.New("provider is required (github, circleci, or generic)")
	}
	routingMode := domain.TriggerRoutingMode(strings.TrimSpace(input.RoutingMode))
	if routingMode == "" {
		routingMode = domain.TriggerRoutingMatchWatchers
	}
	dispatchMode := domain.DispatchMode(strings.TrimSpace(input.DispatchMode))
	if dispatchMode == "" {
		dispatchMode = domain.DispatchModeEnqueue
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	cooldownSeconds := 0
	if input.CooldownSeconds != nil {
		cooldownSeconds = *input.CooldownSeconds
	}
	var filterCfg trigger.FilterConfig
	if input.FilterConfig != nil {
		filterCfg = *input.FilterConfig
		filterCfg.Normalize()
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = defaultTriggerName(provider, filterCfg)
	}

	var targetProjectID uuid.NullUUID
	if trimmed := strings.TrimSpace(input.TargetProjectID); trimmed != "" {
		parsed, err := uuid.Parse(trimmed)
		if err != nil {
			return nil, errors.New("target_project_id must be a valid UUID")
		}
		targetProjectID = uuid.NullUUID{UUID: parsed, Valid: true}
	} else if routingMode == domain.TriggerRoutingCreateTodo && r.context.ProjectID != uuid.Nil {
		targetProjectID = uuid.NullUUID{UUID: r.context.ProjectID, Valid: true}
	}

	var targetCardID uuid.NullUUID
	if trimmed := strings.TrimSpace(input.TargetCardID); trimmed != "" {
		parsed, err := uuid.Parse(trimmed)
		if err != nil {
			return nil, errors.New("target_card_id must be a valid UUID")
		}
		targetCardID = uuid.NullUUID{UUID: parsed, Valid: true}
	}

	// 同 Attempt 幂等键防重：避免工具重试创建多个完全相同的触发器。
	idemKey := strings.TrimSpace(input.IdempotencyKey)
	if existingID, ok := r.triggerCreateCache.get(idemKey); ok {
		if existing, err := r.store.GetTrigger(ctx, r.context.WorkspaceID, existingID); err == nil {
			return map[string]any{
				"ok":          true,
				"reused":      true,
				"trigger":     triggerToolView(r.publicBaseURL, existing),
				"webhook_url": triggerWebhookURL(r.publicBaseURL, existing.ID),
				"note":        "Reused trigger created earlier with the same idempotency_key. Plaintext webhook_secret is only returned on initial creation.",
			}, nil
		}
	}

	// 工作区级配置去重：若已存在同来源、同名、同路由且同过滤条件的触发器，直接复用而不重复新建。
	existingList, err := r.store.ListTriggers(ctx, r.context.WorkspaceID)
	if err != nil {
		return nil, err
	}
	for _, existing := range existingList {
		if triggersMatchForDedup(existing, name, provider, routingMode, targetProjectID, targetCardID, filterCfg) {
			r.triggerCreateCache.set(idemKey, existing.ID)
			return map[string]any{
				"ok":          true,
				"reused":      true,
				"trigger":     triggerToolView(r.publicBaseURL, existing),
				"webhook_url": triggerWebhookURL(r.publicBaseURL, existing.ID),
				"note":        "A matching trigger already exists in this workspace and was reused without creating a duplicate. Plaintext webhook_secret is only returned on initial creation; rotate secret in Settings or delete and recreate if you need a fresh secret.",
			}, nil
		}
	}

	created, plainSecret, err := r.store.CreateTrigger(ctx, r.context.WorkspaceID, r.context.RunnerSourceID, store.CreateTriggerInput{
		Name:            name,
		Provider:        provider,
		AuthMode:        domain.TriggerAuthMode(strings.TrimSpace(input.AuthMode)),
		SignatureHeader: strings.TrimSpace(input.SignatureHeader),
		Enabled:         &enabled,
		RoutingMode:     routingMode,
		TargetProjectID: targetProjectID,
		TargetCardID:    targetCardID,
		DispatchMode:    dispatchMode,
		FilterConfig:    filterCfg,
		CooldownSeconds: cooldownSeconds,
	})
	if err != nil {
		return nil, err
	}
	r.triggerCreateCache.set(idemKey, created.ID)

	webhookURL := triggerWebhookURL(r.publicBaseURL, created.ID)
	return map[string]any{
		"ok":                         true,
		"reused":                     false,
		"trigger":                    triggerToolView(r.publicBaseURL, created),
		"webhook_url":                webhookURL,
		"webhook_secret":             plainSecret,
		"public_base_url_configured": strings.TrimSpace(r.publicBaseURL) != "",
		"security_notice":            "One-time webhook_secret for immediate automated webhook setup (e.g. gh api). NEVER print, echo, write to files, or save this secret in user replies, logs, or Recall memory.",
	}, nil
}

func (r attemptToolRuntime) updateTriggerTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	if err := r.ensureTriggerWriteAllowed("cycledock_update_trigger"); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var input updateTriggerToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("invalid update_trigger arguments: %w", err)
	}
	triggerID, err := uuid.Parse(strings.TrimSpace(input.TriggerID))
	if err != nil {
		return nil, errors.New("trigger_id must be a valid UUID")
	}

	update := store.UpdateTriggerInput{
		Name:            input.Name,
		Enabled:         input.Enabled,
		SignatureHeader: input.SignatureHeader,
		CooldownSeconds: input.CooldownSeconds,
		FilterConfig:    input.FilterConfig,
	}
	if input.RoutingMode != nil {
		mode := domain.TriggerRoutingMode(strings.TrimSpace(*input.RoutingMode))
		update.RoutingMode = &mode
	}
	if input.DispatchMode != nil {
		mode := domain.DispatchMode(strings.TrimSpace(*input.DispatchMode))
		update.DispatchMode = &mode
	}
	if input.TargetProjectID != nil {
		trimmed := strings.TrimSpace(*input.TargetProjectID)
		if trimmed == "" {
			empty := uuid.NullUUID{}
			update.TargetProjectID = &empty
		} else {
			parsed, err := uuid.Parse(trimmed)
			if err != nil {
				return nil, errors.New("target_project_id must be a valid UUID")
			}
			val := uuid.NullUUID{UUID: parsed, Valid: true}
			update.TargetProjectID = &val
		}
	}
	if input.TargetCardID != nil {
		trimmed := strings.TrimSpace(*input.TargetCardID)
		if trimmed == "" {
			empty := uuid.NullUUID{}
			update.TargetCardID = &empty
		} else {
			parsed, err := uuid.Parse(trimmed)
			if err != nil {
				return nil, errors.New("target_card_id must be a valid UUID")
			}
			val := uuid.NullUUID{UUID: parsed, Valid: true}
			update.TargetCardID = &val
		}
	}

	updated, err := r.store.UpdateTrigger(ctx, r.context.WorkspaceID, triggerID, update)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":      true,
		"trigger": triggerToolView(r.publicBaseURL, updated),
	}, nil
}

func (r attemptToolRuntime) deleteTriggerTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	if err := r.ensureTriggerWriteAllowed("cycledock_delete_trigger"); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var input deleteTriggerToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("invalid delete_trigger arguments: %w", err)
	}
	triggerID, err := uuid.Parse(strings.TrimSpace(input.TriggerID))
	if err != nil {
		return nil, errors.New("trigger_id must be a valid UUID")
	}
	if err := r.store.DeleteTrigger(ctx, r.context.WorkspaceID, triggerID); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":         true,
		"deleted":    true,
		"trigger_id": triggerID.String(),
	}, nil
}
