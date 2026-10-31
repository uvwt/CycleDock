package agent

import (
	"testing"
)

func TestBuiltInScheduleToolsDiscoverableAndValidateRules(t *testing.T) {
	tools, err := buildADKTools(attemptToolRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{
		"cycledock_list_schedules":  false,
		"cycledock_create_schedule": false,
		"cycledock_update_schedule": false,
		"cycledock_delete_schedule": false,
	}
	for _, tool := range tools {
		if _, ok := expected[tool.Name()]; ok {
			expected[tool.Name()] = true
		}
	}
	for name, exists := range expected {
		if !exists {
			t.Errorf("built-in agent is missing %s", name)
		}
	}

	rule, err := scheduleToolRule(map[string]any{
		"frequency": "monthly", "day_of_month": 28,
		"hour": 9, "minute": 15, "timezone": "Asia/Shanghai",
	})
	if err != nil || rule.DayOfMonth == nil || *rule.DayOfMonth != 28 {
		t.Fatalf("monthly rule=%+v err=%v", rule, err)
	}
	if _, err := scheduleToolRule(map[string]any{
		"frequency": "weekly", "day_of_week": 8,
		"hour": 9, "minute": 0, "timezone": "UTC",
	}); err == nil {
		t.Fatal("out-of-range weekday accepted")
	}
	if _, err := scheduleToolRule(map[string]any{
		"frequency": "daily", "hour": 25, "minute": 0, "timezone": "UTC",
	}); err == nil {
		t.Fatal("invalid local hour accepted")
	}

}
