package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/trigger"
)

const maxWebhookBodyBytes = 1 << 20 // 1 MiB

type triggerCardRefResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
}

type triggerResponse struct {
	ID                  string               `json:"id"`
	Name                string               `json:"name"`
	Provider            string               `json:"provider"`
	AuthMode            string               `json:"auth_mode"`
	SignatureHeader     string               `json:"signature_header"`
	SecretHint          string               `json:"secret_hint"`
	WebhookPath         string               `json:"webhook_path"`
	Enabled             bool                 `json:"enabled"`
	RoutingMode         string               `json:"routing_mode"`
	TargetProjectID     string               `json:"target_project_id,omitempty"`
	TargetProjectName   string               `json:"target_project_name,omitempty"`
	TargetCardID        string               `json:"target_card_id,omitempty"`
	TargetCardProjectID string               `json:"target_card_project_id,omitempty"`
	TargetCardKind      string               `json:"target_card_kind,omitempty"`
	TargetCardTitle     string               `json:"target_card_title,omitempty"`
	DispatchMode        string               `json:"dispatch_mode"`
	FilterConfig        trigger.FilterConfig `json:"filter_config"`
	CooldownSeconds     int                  `json:"cooldown_seconds"`
	LastTriggeredAt     string               `json:"last_triggered_at,omitempty"`
	LastDeliveryStatus  string               `json:"last_delivery_status,omitempty"`
	LastDeliveryReason  string               `json:"last_delivery_reason,omitempty"`
	LastDeliveryAt      string               `json:"last_delivery_at,omitempty"`
	CreatedAt           string               `json:"created_at"`
	UpdatedAt           string               `json:"updated_at"`
}

type triggerDeliveryResponse struct {
	ID                      string                   `json:"id"`
	TriggerID               string                   `json:"trigger_id"`
	TriggerName             string                   `json:"trigger_name,omitempty"`
	DeliveryID              string                   `json:"delivery_id"`
	Provider                string                   `json:"provider"`
	EventType               string                   `json:"event_type"`
	EventAction             string                   `json:"event_action"`
	SignatureValid          bool                     `json:"signature_valid"`
	Status                  string                   `json:"status"`
	Reason                  string                   `json:"reason"`
	MatchKeys               []string                 `json:"match_keys"`
	PayloadSummary          string                   `json:"payload_summary"`
	TriggeredTurnID         string                   `json:"triggered_turn_id,omitempty"`
	TriggeredWatcherTurnIDs []string                 `json:"triggered_watcher_turn_ids"`
	CreatedCardID           string                   `json:"created_card_id,omitempty"`
	LinkedCards             []triggerCardRefResponse `json:"linked_cards,omitempty"`
	ReceivedAt              string                   `json:"received_at"`
}

type cardWatcherResponse struct {
	ID              string `json:"id"`
	CardID          string `json:"card_id"`
	ProjectID       string `json:"project_id,omitempty"`
	ProjectName     string `json:"project_name,omitempty"`
	CardTitle       string `json:"card_title,omitempty"`
	CardKind        string `json:"card_kind,omitempty"`
	Kind            string `json:"kind"`
	MatchKey        string `json:"match_key,omitempty"`
	Note            string `json:"note"`
	DispatchMode    string `json:"dispatch_mode"`
	Status          string `json:"status"`
	ExpiresAt       string `json:"expires_at"`
	TriggeredTurnID string `json:"triggered_turn_id,omitempty"`
	ResolvedReason  string `json:"resolved_reason,omitempty"`
	ResolvedAt      string `json:"resolved_at,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

func makeTriggerResponse(t store.WorkspaceTrigger) triggerResponse {
	resp := triggerResponse{
		ID:                 t.ID.String(),
		Name:               t.Name,
		Provider:           string(t.Provider),
		AuthMode:           string(t.AuthMode),
		SignatureHeader:    t.SignatureHeader,
		SecretHint:         t.SecretHint,
		WebhookPath:        "/api/v1/triggers/" + t.ID.String() + "/webhook",
		Enabled:            t.Enabled,
		RoutingMode:        string(t.RoutingMode),
		TargetProjectName:  t.TargetProjectName,
		TargetCardKind:     string(t.TargetCardKind),
		TargetCardTitle:    t.TargetCardTitle,
		DispatchMode:       string(t.DispatchMode),
		FilterConfig:       t.FilterConfig,
		CooldownSeconds:    t.CooldownSeconds,
		LastDeliveryStatus: string(t.LastDeliveryStatus),
		LastDeliveryReason: t.LastDeliveryReason,
		CreatedAt:          t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:          t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.TargetProjectID.Valid {
		resp.TargetProjectID = t.TargetProjectID.UUID.String()
	}
	if t.TargetCardID.Valid {
		resp.TargetCardID = t.TargetCardID.UUID.String()
	}
	if t.TargetCardProjectID.Valid {
		resp.TargetCardProjectID = t.TargetCardProjectID.UUID.String()
	}
	if t.LastTriggeredAt != nil {
		resp.LastTriggeredAt = t.LastTriggeredAt.UTC().Format(time.RFC3339)
	}
	if t.LastDeliveryAt != nil {
		resp.LastDeliveryAt = t.LastDeliveryAt.UTC().Format(time.RFC3339)
	}
	return resp
}

func makeTriggerDeliveryResponse(d store.TriggerDelivery) triggerDeliveryResponse {
	watcherTurnIDs := make([]string, 0, len(d.TriggeredWatcherTurnIDs))
	for _, id := range d.TriggeredWatcherTurnIDs {
		watcherTurnIDs = append(watcherTurnIDs, id.String())
	}
	matchKeys := d.MatchKeys
	if matchKeys == nil {
		matchKeys = []string{}
	}
	resp := triggerDeliveryResponse{
		ID:                      d.ID.String(),
		TriggerID:               d.TriggerID.String(),
		TriggerName:             d.TriggerName,
		DeliveryID:              d.DeliveryID,
		Provider:                string(d.Provider),
		EventType:               d.EventType,
		EventAction:             d.EventAction,
		SignatureValid:          d.SignatureValid,
		Status:                  string(d.Status),
		Reason:                  d.Reason,
		MatchKeys:               matchKeys,
		PayloadSummary:          d.PayloadSummary,
		TriggeredWatcherTurnIDs: watcherTurnIDs,
		ReceivedAt:              d.ReceivedAt.UTC().Format(time.RFC3339),
	}
	if d.TriggeredTurnID.Valid {
		resp.TriggeredTurnID = d.TriggeredTurnID.UUID.String()
	}
	if d.CreatedCardID.Valid {
		resp.CreatedCardID = d.CreatedCardID.UUID.String()
	}
	if len(d.LinkedCards) > 0 {
		resp.LinkedCards = make([]triggerCardRefResponse, 0, len(d.LinkedCards))
		for _, c := range d.LinkedCards {
			resp.LinkedCards = append(resp.LinkedCards, triggerCardRefResponse{
				ID:        c.ID.String(),
				ProjectID: c.ProjectID.String(),
				Kind:      string(c.Kind),
				Title:     c.Title,
			})
		}
	}
	return resp
}

func makeWorkspaceCardWatcherResponse(w store.WorkspaceCardWatcher) cardWatcherResponse {
	resp := makeCardWatcherResponse(w.CardWatcher)
	resp.ProjectID = w.ProjectID.String()
	resp.ProjectName = w.ProjectName
	resp.CardTitle = w.CardTitle
	resp.CardKind = string(w.CardKind)
	return resp
}

func makeCardWatcherResponse(w store.CardWatcher) cardWatcherResponse {
	resp := cardWatcherResponse{
		ID:             w.ID.String(),
		CardID:         w.CardID.String(),
		Kind:           string(w.Kind),
		MatchKey:       w.MatchKey,
		Note:           w.Note,
		DispatchMode:   string(w.DispatchMode),
		Status:         string(w.Status),
		ExpiresAt:      w.ExpiresAt.UTC().Format(time.RFC3339),
		ResolvedReason: w.ResolvedReason,
		CreatedAt:      w.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      w.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if w.TriggeredTurnID.Valid {
		resp.TriggeredTurnID = w.TriggeredTurnID.UUID.String()
	}
	if w.ResolvedAt != nil {
		resp.ResolvedAt = w.ResolvedAt.UTC().Format(time.RFC3339)
	}
	return resp
}

func parseOptionalNullUUID(raw *string) (*uuid.NullUUID, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		empty := uuid.NullUUID{}
		return &empty, nil
	}
	parsed, err := uuid.Parse(trimmed)
	if err != nil {
		return nil, err
	}
	val := uuid.NullUUID{UUID: parsed, Valid: true}
	return &val, nil
}

func (api *API) listTriggers(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	items, err := api.store.ListTriggers(r.Context(), session.WorkspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	out := make([]triggerResponse, 0, len(items))
	for _, item := range items {
		out = append(out, makeTriggerResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"triggers": out})
}

func (api *API) createTrigger(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	var body struct {
		Name            string               `json:"name"`
		Provider        string               `json:"provider"`
		AuthMode        string               `json:"auth_mode"`
		SignatureHeader string               `json:"signature_header"`
		Secret          string               `json:"secret"`
		Enabled         *bool                `json:"enabled"`
		RoutingMode     string               `json:"routing_mode"`
		TargetProjectID *string              `json:"target_project_id"`
		TargetCardID    *string              `json:"target_card_id"`
		DispatchMode    string               `json:"dispatch_mode"`
		FilterConfig    trigger.FilterConfig `json:"filter_config"`
		CooldownSeconds int                  `json:"cooldown_seconds"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	targetProjectID, err := parseOptionalNullUUID(body.TargetProjectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_target_project_id", "target_project_id must be a valid UUID")
		return
	}
	targetCardID, err := parseOptionalNullUUID(body.TargetCardID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_target_card_id", "target_card_id must be a valid UUID")
		return
	}
	var projUUID, cardUUID uuid.NullUUID
	if targetProjectID != nil {
		projUUID = *targetProjectID
	}
	if targetCardID != nil {
		cardUUID = *targetCardID
	}

	created, secret, err := api.store.CreateTrigger(r.Context(), session.WorkspaceID, session.SourceID, store.CreateTriggerInput{
		Name:            body.Name,
		Provider:        domain.TriggerProvider(strings.TrimSpace(body.Provider)),
		AuthMode:        domain.TriggerAuthMode(strings.TrimSpace(body.AuthMode)),
		SignatureHeader: body.SignatureHeader,
		Secret:          body.Secret,
		Enabled:         body.Enabled,
		RoutingMode:     domain.TriggerRoutingMode(strings.TrimSpace(body.RoutingMode)),
		TargetProjectID: projUUID,
		TargetCardID:    cardUUID,
		DispatchMode:    domain.DispatchMode(strings.TrimSpace(body.DispatchMode)),
		FilterConfig:    body.FilterConfig,
		CooldownSeconds: body.CooldownSeconds,
	})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
			api.handleError(w, err)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_trigger", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"trigger": makeTriggerResponse(created),
		// secret 仅在创建或轮换当次响应中展示一次，后续 GET/LIST 永不再返回明文。
		"secret": secret,
	})
}

func (api *API) updateTrigger(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	triggerID, err := uuid.Parse(chi.URLParam(r, "triggerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_trigger_id", "triggerID must be a valid UUID")
		return
	}
	var body struct {
		Name            *string               `json:"name"`
		Enabled         *bool                 `json:"enabled"`
		SignatureHeader *string               `json:"signature_header"`
		RoutingMode     *string               `json:"routing_mode"`
		TargetProjectID *string               `json:"target_project_id"`
		TargetCardID    *string               `json:"target_card_id"`
		DispatchMode    *string               `json:"dispatch_mode"`
		FilterConfig    *trigger.FilterConfig `json:"filter_config"`
		CooldownSeconds *int                  `json:"cooldown_seconds"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	targetProjectID, err := parseOptionalNullUUID(body.TargetProjectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_target_project_id", "target_project_id must be a valid UUID")
		return
	}
	targetCardID, err := parseOptionalNullUUID(body.TargetCardID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_target_card_id", "target_card_id must be a valid UUID")
		return
	}
	input := store.UpdateTriggerInput{
		Name:            body.Name,
		Enabled:         body.Enabled,
		SignatureHeader: body.SignatureHeader,
		TargetProjectID: targetProjectID,
		TargetCardID:    targetCardID,
		FilterConfig:    body.FilterConfig,
		CooldownSeconds: body.CooldownSeconds,
	}
	if body.RoutingMode != nil {
		mode := domain.TriggerRoutingMode(strings.TrimSpace(*body.RoutingMode))
		input.RoutingMode = &mode
	}
	if body.DispatchMode != nil {
		mode := domain.DispatchMode(strings.TrimSpace(*body.DispatchMode))
		input.DispatchMode = &mode
	}
	updated, err := api.store.UpdateTrigger(r.Context(), session.WorkspaceID, triggerID, input)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
			api.handleError(w, err)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_trigger", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trigger": makeTriggerResponse(updated)})
}

func (api *API) rotateTriggerSecret(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	triggerID, err := uuid.Parse(chi.URLParam(r, "triggerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_trigger_id", "triggerID must be a valid UUID")
		return
	}
	var body struct {
		Secret string `json:"secret"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSON(w, r, &body) {
			return
		}
	}
	updated, secret, err := api.store.RotateTriggerSecret(r.Context(), session.WorkspaceID, triggerID, body.Secret)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			api.handleError(w, err)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_secret", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"trigger": makeTriggerResponse(updated),
		"secret":  secret,
	})
}

func (api *API) deleteTrigger(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	triggerID, err := uuid.Parse(chi.URLParam(r, "triggerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_trigger_id", "triggerID must be a valid UUID")
		return
	}
	if err := api.store.DeleteTrigger(r.Context(), session.WorkspaceID, triggerID); err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (api *API) listTriggerDeliveries(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	triggerID, err := uuid.Parse(chi.URLParam(r, "triggerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_trigger_id", "triggerID must be a valid UUID")
		return
	}
	if _, err := api.store.GetTrigger(r.Context(), session.WorkspaceID, triggerID); err != nil {
		api.handleError(w, err)
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	items, err := api.store.ListTriggerDeliveries(r.Context(), session.WorkspaceID, triggerID, limit)
	if err != nil {
		api.handleError(w, err)
		return
	}
	out := make([]triggerDeliveryResponse, 0, len(items))
	for _, item := range items {
		out = append(out, makeTriggerDeliveryResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": out})
}

func (api *API) listWorkspaceTriggerDeliveries(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	items, err := api.store.ListWorkspaceTriggerDeliveries(r.Context(), session.WorkspaceID, limit)
	if err != nil {
		api.handleError(w, err)
		return
	}
	out := make([]triggerDeliveryResponse, 0, len(items))
	for _, item := range items {
		out = append(out, makeTriggerDeliveryResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": out})
}

func (api *API) listWorkspaceWatchers(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	armedOnly := true
	if raw := strings.TrimSpace(r.URL.Query().Get("armed_only")); raw != "" {
		armedOnly = !strings.EqualFold(raw, "false") && raw != "0"
	}
	var projectID uuid.NullUUID
	if raw := strings.TrimSpace(r.URL.Query().Get("project_id")); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_project_id", "project_id must be a valid UUID")
			return
		}
		projectID = uuid.NullUUID{UUID: parsed, Valid: true}
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	items, err := api.store.ListWorkspaceWatchers(r.Context(), session.WorkspaceID, projectID, armedOnly, limit)
	if err != nil {
		api.handleError(w, err)
		return
	}
	out := make([]cardWatcherResponse, 0, len(items))
	for _, item := range items {
		out = append(out, makeWorkspaceCardWatcherResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"watchers": out})
}

func (api *API) listCardWatchers(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	cardID, err := uuid.Parse(chi.URLParam(r, "cardID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_card_id", "cardID must be a valid UUID")
		return
	}
	armedOnly := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("armed_only")), "true")
	items, err := api.store.ListCardWatchers(r.Context(), session.WorkspaceID, cardID, armedOnly, 50)
	if err != nil {
		api.handleError(w, err)
		return
	}
	out := make([]cardWatcherResponse, 0, len(items))
	for _, item := range items {
		out = append(out, makeCardWatcherResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"watchers": out})
}

func (api *API) cancelCardWatcher(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	cardID, err := uuid.Parse(chi.URLParam(r, "cardID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_card_id", "cardID must be a valid UUID")
		return
	}
	watcherID, err := uuid.Parse(chi.URLParam(r, "watcherID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_watcher_id", "watcherID must be a valid UUID")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSON(w, r, &body) {
			return
		}
	}
	updated, err := api.store.CancelCardWatcher(r.Context(), session.WorkspaceID, cardID, watcherID, body.Reason)
	if err != nil {
		api.handleError(w, err)
		return
	}
	if api.live != nil {
		api.live.Publish(cardID, realtime.Event{Type: "card.updated"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"watcher": makeCardWatcherResponse(updated)})
}

// receiveTriggerWebhook 处理来自 GitHub / CircleCI / 通用外部系统的公开 Webhook 回调。
// 必须挂载在人类会话认证和 requireJSONWrites 之外，但严格执行：
// 1) 原始字节级签名/密钥校验（未通过直接 401 并落审计日志，不唤醒 Agent）；
// 2) 确定性事件解析与过滤（中间态如 queued/in_progress 直接 200 OK 忽略，零 Token 消耗）；
// 3) 事务内幂等去重、Watcher 唤醒与冷却防抖。
func (api *API) receiveTriggerWebhook(w http.ResponseWriter, r *http.Request) {
	triggerID, err := uuid.Parse(chi.URLParam(r, "triggerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_trigger_id", "triggerID must be a valid UUID")
		return
	}
	trig, err := api.store.GetTriggerByID(r.Context(), triggerID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "trigger not found")
			return
		}
		api.handleError(w, err)
		return
	}

	rawBody, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to read request body")
		return
	}
	if len(rawBody) > maxWebhookBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "webhook payload exceeds 1 MiB limit")
		return
	}

	secret, err := api.store.DecryptTriggerSecret(trig)
	if err != nil {
		api.logger.Error("failed to decrypt trigger secret", "trigger_id", trig.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "trigger_secret_unavailable", "trigger secret unavailable")
		return
	}

	now := time.Now().UTC()
	if !trigger.VerifySignature(trig.AuthMode, secret, trig.SignatureHeader, r.Header, rawBody) {
		deliveryID := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
		if deliveryID == "" {
			deliveryID = strings.TrimSpace(r.Header.Get("circleci-delivery-id"))
		}
		if deliveryID == "" {
			deliveryID = strings.TrimSpace(r.Header.Get("X-CircleCI-Delivery-ID"))
		}
		if deliveryID == "" {
			deliveryID = strings.TrimSpace(r.Header.Get("X-Delivery-Id"))
		}
		eventType := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
		if eventType == "" {
			eventType = strings.TrimSpace(r.Header.Get("circleci-event-type"))
		}
		_, _ = api.store.RecordRejectedWebhookDelivery(r.Context(), trig, deliveryID, eventType, "", "invalid_signature", now)
		writeError(w, http.StatusUnauthorized, "invalid_signature", "webhook signature verification failed")
		return
	}

	parsed, err := trigger.ParseAndFilter(trig.Provider, trig.FilterConfig, r.Header, rawBody)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_webhook_payload", err.Error())
		return
	}

	result, err := api.store.ProcessVerifiedWebhookDelivery(r.Context(), trig, parsed, now)
	if err != nil {
		api.handleError(w, err)
		return
	}
	if api.live != nil {
		for _, cardID := range result.WokenCardIDs {
			api.live.Publish(cardID, realtime.Event{Type: "card.updated"})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"reused":   result.Reused,
		"delivery": makeTriggerDeliveryResponse(result.Delivery),
	})
}
