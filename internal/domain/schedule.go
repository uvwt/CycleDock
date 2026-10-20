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
		if r.DayOfWeek == nil || r.DayOfMonth != nil {
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

// NextAfter 返回严格晚于 after 的下一次 occurrence。所有日历计算在计划时区内进行，
// 最后再转回带时区的绝对时间，避免把 UTC 日期错误地当成本地日期。
func (r ScheduleRule) NextAfter(after time.Time) (time.Time, error) {
	if err := r.Validate(); err != nil {
		return time.Time{}, err
	}
	loc, _ := time.LoadLocation(r.Timezone)
	local := after.In(loc)
	date := time.Date(local.Year(), local.Month(), local.Day(), r.Hour, r.Minute, 0, 0, loc)

	switch r.Frequency {
	case ScheduleDaily:
		if !date.After(local) {
			date = date.AddDate(0, 0, 1)
		}
	case ScheduleWeekly:
		delta := (int(*r.DayOfWeek) - int(local.Weekday()) + 7) % 7
		date = date.AddDate(0, 0, delta)
		if !date.After(local) {
			date = date.AddDate(0, 0, 7)
		}
	case ScheduleMonthly:
		date = time.Date(local.Year(), local.Month(), *r.DayOfMonth, r.Hour, r.Minute, 0, 0, loc)
		if !date.After(local) {
			next := local.AddDate(0, 1, 0)
			date = time.Date(next.Year(), next.Month(), *r.DayOfMonth, r.Hour, r.Minute, 0, 0, loc)
		}
	}
	return date, nil
}

func CanMoveNormalCard(from, to NormalColumn, actor SourceKind) error {
	if from == to {
		return nil
	}
	if to == NormalColumnDone {
		if actor != SourceKindHuman {
			return ErrHumanRequired
		}
		if from != NormalColumnReview {
			return ErrInvalidTransition
		}
		return nil
	}
	if from == NormalColumnDone {
		if actor != SourceKindHuman || to != NormalColumnReview {
			return ErrInvalidTransition
		}
		return nil
	}
	return nil
}
