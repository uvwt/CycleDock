package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	adkagent "google.golang.org/adk/v2/agent"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func scheduleToolID(args map[string]any, key string) (uuid.UUID, error) {
	id, err := uuid.Parse(stringArg(args, key))
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s must be a valid UUID", key)
	}
	return id, nil
}

func scheduleToolRule(value any) (domain.ScheduleRule, error) {
	var input struct {
		Frequency  string `json:"frequency"`
		DayOfWeek  *int   `json:"day_of_week"`
		DayOfMonth *int   `json:"day_of_month"`
		Hour       int    `json:"hour"`
		Minute     int    `json:"minute"`
		Timezone   string `json:"timezone"`
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return domain.ScheduleRule{}, err
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return domain.ScheduleRule{}, fmt.Errorf("invalid schedule rule: %w", err)
	}
	rule := domain.ScheduleRule{
		Frequency:  domain.ScheduleFrequency(input.Frequency),
		DayOfMonth: input.DayOfMonth,
		Hour:       input.Hour, Minute: input.Minute, Timezone: strings.TrimSpace(input.Timezone),
	}
	if input.DayOfWeek != nil {
		day := time.Weekday(*input.DayOfWeek)
		rule.DayOfWeek = &day
	}
	if err := rule.Validate(); err != nil {
		return domain.ScheduleRule{}, fmt.Errorf("invalid schedule rule: %w", err)
	}
	return rule, nil
}

func scheduleToolResult(item store.CardSchedule) map[string]any {
	rule := map[string]any{
		"frequency": item.Rule.Frequency,
		"hour":      item.Rule.Hour,
		"minute":    item.Rule.Minute,
		"timezone":  item.Rule.Timezone,
	}
	if item.Rule.DayOfWeek != nil {
		rule["day_of_week"] = int(*item.Rule.DayOfWeek)
	}
	if item.Rule.DayOfMonth != nil {
		rule["day_of_month"] = *item.Rule.DayOfMonth
	}
	result := map[string]any{
		"id": item.ID.String(), "card_id": item.CardID.String(),
		"rule": rule, "enabled": item.Enabled,
		"next_trigger_at": item.NextTriggerAt.UTC().Format(time.RFC3339),
	}
	if item.LastTriggered != nil {
		result["last_triggered_at"] = item.LastTriggered.UTC().Format(time.RFC3339)
	}
	return result
}

func scheduleToolRow(item db.CardSchedule) map[string]any {
	micros := item.TimeOfDay.Microseconds
	rule := domain.ScheduleRule{
		Frequency: domain.ScheduleFrequency(item.Frequency),
		Hour:      int(micros / int64(time.Hour/time.Microsecond)),
		Minute:    int((micros % int64(time.Hour/time.Microsecond)) / int64(time.Minute/time.Microsecond)),
		Timezone:  item.Timezone,
	}
	if item.DayOfWeek.Valid {
		day := time.Weekday(item.DayOfWeek.Int16)
		rule.DayOfWeek = &day
	}
	if item.DayOfMonth.Valid {
		day := int(item.DayOfMonth.Int16)
		rule.DayOfMonth = &day
	}
	result := scheduleToolResult(store.CardSchedule{
		ID: item.ID, CardID: item.CardID, WorkspaceID: item.WorkspaceID,
		Rule: rule, Enabled: item.Enabled, NextTriggerAt: item.NextTriggerAt.Time,
	})
	if item.LastTriggeredAt.Valid {
		result["last_triggered_at"] = item.LastTriggeredAt.Time.UTC().Format(time.RFC3339)
	}
	return result
}

func (r attemptToolRuntime) listSchedulesTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	cardID, err := scheduleToolID(args, "card_id")
	if err != nil {
		return nil, err
	}
	items, err := r.store.ListSchedules(ctx, r.context.WorkspaceID, cardID)
	if err != nil {
		return nil, err
	}
	schedules := make([]map[string]any, 0, len(items))
	for _, item := range items {
		schedules = append(schedules, scheduleToolRow(item))
	}
	return map[string]any{"schedules": schedules}, nil
}

func (r attemptToolRuntime) createScheduleTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	cardID, err := scheduleToolID(args, "card_id")
	if err != nil {
		return nil, err
	}
	rule, err := scheduleToolRule(args["rule"])
	if err != nil {
		return nil, err
	}
	schedule, err := r.store.CreateSchedule(ctx, r.context.WorkspaceID, cardID, rule, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return map[string]any{"schedule": scheduleToolResult(schedule)}, nil
}

func (r attemptToolRuntime) updateScheduleTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	cardID, err := scheduleToolID(args, "card_id")
	if err != nil {
		return nil, err
	}
	scheduleID, err := scheduleToolID(args, "schedule_id")
	if err != nil {
		return nil, err
	}
	rule, err := scheduleToolRule(args["rule"])
	if err != nil {
		return nil, err
	}
	enabled, ok := args["enabled"].(bool)
	if !ok {
		return nil, fmt.Errorf("enabled must be true or false")
	}
	schedule, err := r.store.UpdateSchedule(ctx, store.UpdateScheduleParams{
		WorkspaceID: r.context.WorkspaceID, CardID: cardID, ScheduleID: scheduleID,
		Rule: rule, Enabled: enabled, After: time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"schedule": scheduleToolResult(schedule)}, nil
}

func (r attemptToolRuntime) deleteScheduleTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	cardID, err := scheduleToolID(args, "card_id")
	if err != nil {
		return nil, err
	}
	scheduleID, err := scheduleToolID(args, "schedule_id")
	if err != nil {
		return nil, err
	}
	// 有 Turn 历史的 Schedule 不可删除，只能更新为 disabled。
	if err := r.store.DeleteSchedule(ctx, r.context.WorkspaceID, cardID, scheduleID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}
