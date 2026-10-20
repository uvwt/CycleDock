package domain

import (
	"testing"
	"time"
)

func TestScheduleRuleNextAfter(t *testing.T) {
	t.Parallel()

	shanghai := ScheduleRule{Frequency: ScheduleDaily, Hour: 9, Minute: 0, Timezone: "Asia/Shanghai"}
	after := time.Date(2026, 10, 20, 9, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	next, err := shanghai.NextAfter(after)
	if err != nil {
		t.Fatal(err)
	}
	if got := next.In(time.FixedZone("UTC+8", 8*60*60)); got.Day() != 21 || got.Hour() != 9 {
		t.Fatalf("daily next = %s, want next day 09:00", got)
	}

	weekday := time.Monday
	weekly := ScheduleRule{Frequency: ScheduleWeekly, DayOfWeek: &weekday, Hour: 10, Minute: 30, Timezone: "Asia/Shanghai"}
	next, err = weekly.NextAfter(time.Date(2026, 10, 20, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60)))
	if err != nil {
		t.Fatal(err)
	}
	if got := next.In(time.FixedZone("UTC+8", 8*60*60)); got.Weekday() != time.Monday || got.Hour() != 10 || got.Minute() != 30 {
		t.Fatalf("weekly next = %s", got)
	}

	day := 28
	monthly := ScheduleRule{Frequency: ScheduleMonthly, DayOfMonth: &day, Hour: 8, Minute: 15, Timezone: "Asia/Shanghai"}
	next, err = monthly.NextAfter(time.Date(2026, 10, 29, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60)))
	if err != nil {
		t.Fatal(err)
	}
	if got := next.In(time.FixedZone("UTC+8", 8*60*60)); got.Month() != time.November || got.Day() != 28 {
		t.Fatalf("monthly next = %s, want Nov 28", got)
	}
}

func TestScheduleRuleUsesIANATimezoneAcrossDST(t *testing.T) {
	t.Parallel()

	rule := ScheduleRule{Frequency: ScheduleDaily, Hour: 9, Minute: 0, Timezone: "America/Los_Angeles"}
	loc, err := time.LoadLocation(rule.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	// 2026-03-08 是洛杉矶 DST 切换日。计划仍应保持本地 09:00，而不是固定 UTC 偏移。
	after := time.Date(2026, 3, 7, 10, 0, 0, 0, loc)
	next, err := rule.NextAfter(after)
	if err != nil {
		t.Fatal(err)
	}
	if got := next.In(loc); got.Day() != 8 || got.Hour() != 9 {
		t.Fatalf("DST next = %s, want Mar 8 09:00 local", got)
	}
}
