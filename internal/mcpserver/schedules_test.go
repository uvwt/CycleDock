package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestScheduleRuleInputValidation(t *testing.T) {
	monday := 1
	valid := ScheduleRuleInput{
		Frequency: "weekly", DayOfWeek: &monday,
		Hour: 9, Minute: 30, Timezone: "Asia/Shanghai",
	}
	rule, err := valid.domainRule()
	if err != nil || rule.Frequency != domain.ScheduleWeekly || *rule.DayOfWeek != time.Monday {
		t.Fatalf("weekly schedule conversion: rule=%+v err=%v", rule, err)
	}
	view := scheduleRuleView(rule)
	if view.DayOfWeek == nil || *view.DayOfWeek != monday || view.Timezone != valid.Timezone {
		t.Fatalf("schedule roundtrip: %+v", view)
	}

	invalidDay := 9
	for name, input := range map[string]ScheduleRuleInput{
		"invalid weekday":  {Frequency: "weekly", DayOfWeek: &invalidDay, Hour: 8, Timezone: "UTC"},
		"invalid timezone": {Frequency: "daily", Hour: 8, Timezone: "PST"},
		"missing weekday":  {Frequency: "weekly", Hour: 8, Timezone: "UTC"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := input.domainRule()
			if err == nil || !strings.HasPrefix(err.Error(), "INVALID_REQUEST:") {
				t.Fatalf("invalid rule error=%v", err)
			}
		})
	}

	at := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	item := store.CardSchedule{ID: uuid.New(), CardID: uuid.New(), Rule: rule, Enabled: false, NextTriggerAt: at}
	result := scheduleView(item)
	if result.Enabled || result.Rule.DayOfWeek == nil || *result.Rule.DayOfWeek != 1 ||
		result.NextTriggerAt != "2026-10-31T09:00:00Z" {
		t.Fatalf("schedule result=%+v", result)
	}
}

func TestScheduleToolsDiscoveredByOfficialMCPClient(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "cycledock", Version: "test"}, nil)
	registerTools(server, nil) // ListTools 不需要数据库；验证真实 SDK 生成的输入契约。
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	discovered, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{
		"list_schedules": false, "create_schedule": false,
		"update_schedule": false, "delete_schedule": false,
	}
	for _, candidate := range discovered.Tools {
		if _, ok := names[candidate.Name]; ok {
			names[candidate.Name] = true
			if candidate.InputSchema == nil {
				t.Fatalf("%s is missing input schema", candidate.Name)
			}
		}
	}
	for name, found := range names {
		if !found {
			t.Errorf("missing %s in MCP tool discovery", name)
		}
	}
}
