package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	adkagent "google.golang.org/adk/v2/agent"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	defaultEventWatchTimeoutMinutes = 60
	maxEventWatchTimeoutMinutes     = 7 * 24 * 60
	maxWakeupAfterMinutes           = 30 * 24 * 60
)

type watchEventToolInput struct {
	MatchKey       string `json:"match_key"`
	Note           string `json:"note"`
	TimeoutMinutes int    `json:"timeout_minutes"`
	DispatchMode   string `json:"dispatch_mode"`
	IdempotencyKey string `json:"idempotency_key"`
}

type scheduleWakeupToolInput struct {
	AfterMinutes   int    `json:"after_minutes"`
	Note           string `json:"note"`
	DispatchMode   string `json:"dispatch_mode"`
	IdempotencyKey string `json:"idempotency_key"`
}

type listWatchersToolInput struct {
	ArmedOnly *bool `json:"armed_only"`
}

type cancelWatcherToolInput struct {
	WatcherID string `json:"watcher_id"`
	Reason    string `json:"reason"`
}

func watcherToolView(w store.CardWatcher) map[string]any {
	out := map[string]any{
		"id":            w.ID.String(),
		"card_id":       w.CardID.String(),
		"kind":          string(w.Kind),
		"match_key":     w.MatchKey,
		"note":          w.Note,
		"dispatch_mode": string(w.DispatchMode),
		"status":        string(w.Status),
		"expires_at":    w.ExpiresAt.UTC().Format(time.RFC3339),
		"created_at":    w.CreatedAt.UTC().Format(time.RFC3339),
	}
	if w.TriggeredTurnID.Valid {
		out["triggered_turn_id"] = w.TriggeredTurnID.UUID.String()
	}
	if w.ResolvedReason != "" {
		out["resolved_reason"] = w.ResolvedReason
	}
	if w.ResolvedAt != nil {
		out["resolved_at"] = w.ResolvedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (r attemptToolRuntime) watchEventTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	if r.context.IsBuiltInProactive {
		return nil, errors.New("cycledock_watch_event is disabled during built-in proactive turns")
	}
	if r.store == nil {
		return nil, errors.New("store is not configured")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var input watchEventToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("invalid watch_event arguments: %w", err)
	}
	timeoutMinutes := input.TimeoutMinutes
	if timeoutMinutes <= 0 {
		timeoutMinutes = defaultEventWatchTimeoutMinutes
	}
	if timeoutMinutes > maxEventWatchTimeoutMinutes {
		return nil, fmt.Errorf("timeout_minutes must be between 1 and %d", maxEventWatchTimeoutMinutes)
	}
	dispatchMode := domain.DispatchMode(strings.TrimSpace(input.DispatchMode))
	if dispatchMode == "" {
		dispatchMode = domain.DispatchModeEnqueue
	}

	res, err := r.store.CreateCardWatcher(ctx, store.CreateCardWatcherParams{
		WorkspaceID:        r.context.WorkspaceID,
		CardID:             r.context.CardID,
		Kind:               domain.WatcherKindEvent,
		MatchKey:           input.MatchKey,
		Note:               input.Note,
		DispatchMode:       dispatchMode,
		ExpiresAt:          time.Now().UTC().Add(time.Duration(timeoutMinutes) * time.Minute),
		CreatedBySourceID:  uuid.NullUUID{UUID: r.context.RunnerSourceID, Valid: r.context.RunnerSourceID != uuid.Nil},
		CreatedByAttemptID: uuid.NullUUID{UUID: r.context.AttemptID, Valid: r.context.AttemptID != uuid.Nil},
		IdempotencyKey:     input.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	if !res.Reused && r.live != nil {
		r.live.Publish(r.context.CardID, realtime.Event{Type: "card.updated"})
	}
	return map[string]any{
		"ok":      true,
		"reused":  res.Reused,
		"watcher": watcherToolView(res.Watcher),
	}, nil
}

func (r attemptToolRuntime) scheduleWakeupTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	if r.context.IsBuiltInProactive {
		return nil, errors.New("cycledock_schedule_wakeup is disabled during built-in proactive turns")
	}
	if r.store == nil {
		return nil, errors.New("store is not configured")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var input scheduleWakeupToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("invalid schedule_wakeup arguments: %w", err)
	}
	if input.AfterMinutes < 1 || input.AfterMinutes > maxWakeupAfterMinutes {
		return nil, fmt.Errorf("after_minutes must be between 1 and %d", maxWakeupAfterMinutes)
	}
	dispatchMode := domain.DispatchMode(strings.TrimSpace(input.DispatchMode))
	if dispatchMode == "" {
		dispatchMode = domain.DispatchModeEnqueue
	}

	res, err := r.store.CreateCardWatcher(ctx, store.CreateCardWatcherParams{
		WorkspaceID:        r.context.WorkspaceID,
		CardID:             r.context.CardID,
		Kind:               domain.WatcherKindWakeup,
		Note:               input.Note,
		DispatchMode:       dispatchMode,
		ExpiresAt:          time.Now().UTC().Add(time.Duration(input.AfterMinutes) * time.Minute),
		CreatedBySourceID:  uuid.NullUUID{UUID: r.context.RunnerSourceID, Valid: r.context.RunnerSourceID != uuid.Nil},
		CreatedByAttemptID: uuid.NullUUID{UUID: r.context.AttemptID, Valid: r.context.AttemptID != uuid.Nil},
		IdempotencyKey:     input.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	if !res.Reused && r.live != nil {
		r.live.Publish(r.context.CardID, realtime.Event{Type: "card.updated"})
	}
	return map[string]any{
		"ok":      true,
		"reused":  res.Reused,
		"watcher": watcherToolView(res.Watcher),
	}, nil
}

func (r attemptToolRuntime) listWatchersTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	if r.store == nil {
		return nil, errors.New("store is not configured")
	}
	armedOnly := true
	if len(args) > 0 {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		var input listWatchersToolInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, fmt.Errorf("invalid list_watchers arguments: %w", err)
		}
		if input.ArmedOnly != nil {
			armedOnly = *input.ArmedOnly
		}
	}
	items, err := r.store.ListCardWatchers(ctx, r.context.WorkspaceID, r.context.CardID, armedOnly, 50)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, watcherToolView(item))
	}
	return map[string]any{
		"ok":       true,
		"watchers": out,
	}, nil
}

func (r attemptToolRuntime) cancelWatcherTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	if r.context.IsBuiltInProactive {
		return nil, errors.New("cycledock_cancel_watcher is disabled during built-in proactive turns")
	}
	if r.store == nil {
		return nil, errors.New("store is not configured")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var input cancelWatcherToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("invalid cancel_watcher arguments: %w", err)
	}
	watcherID, err := uuid.Parse(strings.TrimSpace(input.WatcherID))
	if err != nil {
		return nil, errors.New("watcher_id must be a valid UUID")
	}
	updated, err := r.store.CancelCardWatcher(ctx, r.context.WorkspaceID, r.context.CardID, watcherID, input.Reason)
	if err != nil {
		return nil, err
	}
	if r.live != nil {
		r.live.Publish(r.context.CardID, realtime.Event{Type: "card.updated"})
	}
	return map[string]any{
		"ok":      true,
		"watcher": watcherToolView(updated),
	}, nil
}
