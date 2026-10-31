package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func (input ScheduleRuleInput) domainRule() (domain.ScheduleRule, error) {
	rule := domain.ScheduleRule{
		Frequency: domain.ScheduleFrequency(input.Frequency),
		Hour:      input.Hour, Minute: input.Minute, Timezone: strings.TrimSpace(input.Timezone),
		DayOfMonth: input.DayOfMonth,
	}
	if input.DayOfWeek != nil {
		day := time.Weekday(*input.DayOfWeek)
		rule.DayOfWeek = &day
	}
	if err := rule.Validate(); err != nil {
		return domain.ScheduleRule{}, fmt.Errorf("INVALID_REQUEST: %w", err)
	}
	return rule, nil
}

func scheduleRuleView(rule domain.ScheduleRule) ScheduleRuleInput {
	view := ScheduleRuleInput{
		Frequency: string(rule.Frequency), Hour: rule.Hour,
		Minute: rule.Minute, Timezone: rule.Timezone, DayOfMonth: rule.DayOfMonth,
	}
	if rule.DayOfWeek != nil {
		day := int(*rule.DayOfWeek)
		view.DayOfWeek = &day
	}
	return view
}

func scheduleView(item store.CardSchedule) ScheduleView {
	view := ScheduleView{
		ID: item.ID.String(), CardID: item.CardID.String(),
		Rule: scheduleRuleView(item.Rule), Enabled: item.Enabled,
		NextTriggerAt: item.NextTriggerAt.UTC().Format(time.RFC3339),
	}
	if item.LastTriggered != nil {
		view.LastTriggeredAt = item.LastTriggered.UTC().Format(time.RFC3339)
	}
	return view
}

func scheduleRowView(item db.CardSchedule) ScheduleView {
	hour := int(item.TimeOfDay.Microseconds / int64(time.Hour/time.Microsecond))
	minute := int((item.TimeOfDay.Microseconds % int64(time.Hour/time.Microsecond)) / int64(time.Minute/time.Microsecond))
	rule := ScheduleRuleInput{
		Frequency: item.Frequency, Hour: hour, Minute: minute, Timezone: item.Timezone,
	}
	if item.DayOfWeek.Valid {
		day := int(item.DayOfWeek.Int16)
		rule.DayOfWeek = &day
	}
	if item.DayOfMonth.Valid {
		day := int(item.DayOfMonth.Int16)
		rule.DayOfMonth = &day
	}
	view := ScheduleView{
		ID: item.ID.String(), CardID: item.CardID.String(),
		Rule: rule, Enabled: item.Enabled,
		NextTriggerAt: item.NextTriggerAt.Time.UTC().Format(time.RFC3339),
	}
	if item.LastTriggeredAt.Valid {
		view.LastTriggeredAt = item.LastTriggeredAt.Time.UTC().Format(time.RFC3339)
	}
	return view
}

func listSchedulesHandler(dataStore *store.Store) mcp.ToolHandlerFor[ListSchedulesInput, ListSchedulesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input ListSchedulesInput) (*mcp.CallToolResult, ListSchedulesOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, ListSchedulesOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, ListSchedulesOutput{}, err
		}
		rows, err := dataStore.ListSchedules(ctx, actor.WorkspaceID, cardID)
		if err != nil {
			return nil, ListSchedulesOutput{}, stableToolError(err)
		}
		result := ListSchedulesOutput{Schedules: make([]ScheduleView, 0, len(rows))}
		for _, row := range rows {
			result.Schedules = append(result.Schedules, scheduleRowView(row))
		}
		return nil, result, nil
	}
}

func createScheduleHandler(dataStore *store.Store) mcp.ToolHandlerFor[CreateScheduleInput, ScheduleOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input CreateScheduleInput) (*mcp.CallToolResult, ScheduleOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, ScheduleOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, ScheduleOutput{}, err
		}
		rule, err := input.Rule.domainRule()
		if err != nil {
			return nil, ScheduleOutput{}, err
		}
		schedule, err := dataStore.CreateSchedule(ctx, actor.WorkspaceID, cardID, rule, time.Now().UTC())
		if err != nil {
			return nil, ScheduleOutput{}, stableToolError(err)
		}
		return nil, ScheduleOutput{Schedule: scheduleView(schedule)}, nil
	}
}

func updateScheduleHandler(dataStore *store.Store) mcp.ToolHandlerFor[UpdateScheduleInput, ScheduleOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input UpdateScheduleInput) (*mcp.CallToolResult, ScheduleOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, ScheduleOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, ScheduleOutput{}, err
		}
		scheduleID, err := parseUUID(input.ScheduleID, "schedule_id")
		if err != nil {
			return nil, ScheduleOutput{}, err
		}
		rule, err := input.Rule.domainRule()
		if err != nil {
			return nil, ScheduleOutput{}, err
		}
		schedule, err := dataStore.UpdateSchedule(ctx, store.UpdateScheduleParams{
			WorkspaceID: actor.WorkspaceID, CardID: cardID, ScheduleID: scheduleID,
			Rule: rule, Enabled: input.Enabled, After: time.Now().UTC(),
		})
		if err != nil {
			return nil, ScheduleOutput{}, stableToolError(err)
		}
		return nil, ScheduleOutput{Schedule: scheduleView(schedule)}, nil
	}
}

func deleteScheduleHandler(dataStore *store.Store) mcp.ToolHandlerFor[DeleteScheduleInput, DeleteScheduleOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input DeleteScheduleInput) (*mcp.CallToolResult, DeleteScheduleOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, DeleteScheduleOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, DeleteScheduleOutput{}, err
		}
		scheduleID, err := parseUUID(input.ScheduleID, "schedule_id")
		if err != nil {
			return nil, DeleteScheduleOutput{}, err
		}
		// 保留 Schedule 产生过的 Turn 审计链；Store 会拒绝删除已有历史的规则。
		if err := dataStore.DeleteSchedule(ctx, actor.WorkspaceID, cardID, scheduleID); err != nil {
			return nil, DeleteScheduleOutput{}, stableToolError(err)
		}
		return nil, DeleteScheduleOutput{Deleted: true}, nil
	}
}
