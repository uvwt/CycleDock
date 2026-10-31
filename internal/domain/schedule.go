package domain

import (
	"fmt"
	"time"
)

// ScheduleRule 只表达第一版明确支持的日/周/月规则；月计划限定 1-28，避免隐式月末语义。
type ScheduleRule struct {
	Frequency  ScheduleFrequency
	DayOfWeek  *time.Weekday
	DayOfMonth *int
	Hour       int
	Minute     int
	Timezone   string
}

func (r ScheduleRule) Validate() error {
	if r.Hour < 0 || r.Hour > 23 || r.Minute < 0 || r.Minute > 59 {
		return fmt.Errorf("invalid time of day")
	}
	if _, err := time.LoadLocation(r.Timezone); err != nil {
		return fmt.Errorf("invalid IANA timezone %q: %w", r.Timezone, err)
	}

	switch r.Frequency {
	case ScheduleDaily:
		if r.DayOfWeek != nil || r.DayOfMonth != nil {
			return fmt.Errorf("daily schedule cannot include day fields")
		}
	case ScheduleWeekly:
		if r.DayOfWeek == nil || *r.DayOfWeek < time.Sunday || *r.DayOfWeek > time.Saturday || r.DayOfMonth != nil {
			return fmt.Errorf("weekly schedule requires day_of_week only")
		}
	case ScheduleMonthly:
		if r.DayOfWeek != nil || r.DayOfMonth == nil || *r.DayOfMonth < 1 || *r.DayOfMonth > 28 {
			return fmt.Errorf("monthly schedule requires day_of_month in 1..28")
		}
	default:
		return fmt.Errorf("unsupported schedule frequency %q", r.Frequency)
	}
	return nil
}

// NextAfter 返回严格晚于 after 的下一次 occurrence。所有日历计算在计划时区内进行。
// DST 语义显式固定：
//   - spring-forward 导致目标 wall time 不存在时，跳过该日/周/月 occurrence；
//   - fall-back 导致目标 wall time 出现两次时，只取第一次，保证一个日历 occurrence 只触发一次。
func (r ScheduleRule) NextAfter(after time.Time) (time.Time, error) {
	if err := r.Validate(); err != nil {
		return time.Time{}, err
	}
	loc, _ := time.LoadLocation(r.Timezone)
	local := after.In(loc)
	// 使用中午作为纯日历日期载体，避免绝大多数时区在午夜附近的 offset transition
	// 影响 AddDate；真正的 wall-clock occurrence 由 resolveWallTime 单独解析。
	date := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, loc)

	switch r.Frequency {
	case ScheduleWeekly:
		delta := (int(*r.DayOfWeek) - int(local.Weekday()) + 7) % 7
		date = date.AddDate(0, 0, delta)
	case ScheduleMonthly:
		date = time.Date(local.Year(), local.Month(), *r.DayOfMonth, 12, 0, 0, 0, loc)
	}

	for range 3700 {
		candidate, exists := resolveWallTime(
			loc, date.Year(), date.Month(), date.Day(), r.Hour, r.Minute,
		)
		if exists && candidate.After(after) {
			return candidate, nil
		}
		switch r.Frequency {
		case ScheduleDaily:
			date = date.AddDate(0, 0, 1)
		case ScheduleWeekly:
			date = date.AddDate(0, 0, 7)
		case ScheduleMonthly:
			date = date.AddDate(0, 1, 0)
		}
	}
	return time.Time{}, fmt.Errorf("could not resolve next schedule occurrence")
}

func resolveWallTime(
	loc *time.Location,
	year int,
	month time.Month,
	day, hour, minute int,
) (time.Time, bool) {
	seed := time.Date(year, month, day, hour, minute, 0, 0, loc)
	var first time.Time
	// time.Date 对 DST gap 会做 normalization，对 overlap 则只会挑其中一个 offset。
	// 在 seed 周围枚举绝对时间，重新映射回本地 wall time，既能识别 gap，
	// 也能稳定选择 overlap 中最早的那个 instant。四小时覆盖现代 IANA DST shift。
	for delta := -4 * 60; delta <= 4*60; delta++ {
		candidate := seed.Add(time.Duration(delta) * time.Minute)
		local := candidate.In(loc)
		if local.Year() != year || local.Month() != month || local.Day() != day ||
			local.Hour() != hour || local.Minute() != minute || local.Second() != 0 {
			continue
		}
		if first.IsZero() || candidate.Before(first) {
			first = candidate
		}
	}
	return first, !first.IsZero()
}

func CanMoveTodo(from, to TodoState, actor SourceKind) error {
	switch to {
	case TodoStateTodo, TodoStateWorking, TodoStateReview, TodoStateDone:
	default:
		return ErrInvalidTransition
	}
	if from == to {
		return nil
	}
	if to == TodoStateDone {
		if actor != SourceKindHuman {
			return ErrHumanRequired
		}
		if from != TodoStateReview {
			return ErrInvalidTransition
		}
		return nil
	}
	if from == TodoStateDone {
		if actor != SourceKindHuman || to != TodoStateReview {
			return ErrInvalidTransition
		}
	}
	return nil
}
