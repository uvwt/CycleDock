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

func TestScheduleRuleSkipsNonexistentSpringForwardWallTime(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	rule := ScheduleRule{
		Frequency: ScheduleDaily, Hour: 2, Minute: 30, Timezone: "America/Los_Angeles",
	}
	// 2026-03-08 02:30 在洛杉矶不存在。该日 occurrence 必须跳过，不能被
	// time.Date normalization 偷偷改成 01:30 或 03:30。
	next, err := rule.NextAfter(time.Date(2026, 3, 7, 3, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	local := next.In(loc)
	if local.Year() != 2026 || local.Month() != time.March || local.Day() != 9 ||
		local.Hour() != 2 || local.Minute() != 30 {
		t.Fatalf("spring-forward next=%s, want Mar 9 02:30 local", local)
	}
}

func TestScheduleRuleUsesFirstFallBackOccurrenceOnlyOnce(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	rule := ScheduleRule{
		Frequency: ScheduleDaily, Hour: 1, Minute: 30, Timezone: "America/Los_Angeles",
	}
	first, err := rule.NextAfter(time.Date(2026, 11, 1, 0, 30, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	firstLocal := first.In(loc)
	_, offset := firstLocal.Zone()
	if firstLocal.Day() != 1 || firstLocal.Hour() != 1 || firstLocal.Minute() != 30 ||
		offset != -7*60*60 {
		t.Fatalf("fall-back first=%s offset=%d, want first 01:30 PDT", firstLocal, offset)
	}

	// 第一次 01:30 后 45 分钟已经进入回拨后的 01:15 PST；当天第二个 01:30
	// 不应再次触发同一个 daily occurrence。
	next, err := rule.NextAfter(first.Add(45 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	nextLocal := next.In(loc)
	if nextLocal.Day() != 2 || nextLocal.Hour() != 1 || nextLocal.Minute() != 30 {
		t.Fatalf("fall-back second calculation=%s, want Nov 2 01:30 local", nextLocal)
	}
}
