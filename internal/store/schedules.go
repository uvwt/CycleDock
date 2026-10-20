package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/uvwt/CycleDock/internal/domain"
)

type CardSchedule struct {
	ID            uuid.UUID
	WorkspaceID   uuid.UUID
	CardID        uuid.UUID
	Rule          domain.ScheduleRule
	Enabled       bool
	NextTriggerAt time.Time
	LastTriggered *time.Time
}

func (s *Store) CreateSchedule(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	rule domain.ScheduleRule,
	after time.Time,
) (CardSchedule, error) {
	if err := rule.Validate(); err != nil {
		return CardSchedule{}, err
	}
	next, err := rule.NextAfter(after)
	if err != nil {
		return CardSchedule{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CardSchedule{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return CardSchedule{}, err
	}
	var kind string
	if err := tx.QueryRow(ctx,
		`SELECT kind FROM cards WHERE workspace_id = $1 AND id = $2`,
		workspaceID, cardID,
	).Scan(&kind); err != nil {
		return CardSchedule{}, err
	}
	if kind != string(domain.CardKindOngoing) {
		return CardSchedule{}, fmt.Errorf("%w: schedule requires ongoing card", domain.ErrInvalidTransition)
	}

	scheduleID := uuid.New()
	var dayOfWeek any
	if rule.DayOfWeek != nil {
		dayOfWeek = int16(*rule.DayOfWeek)
	}
	var dayOfMonth any
	if rule.DayOfMonth != nil {
		dayOfMonth = int16(*rule.DayOfMonth)
	}
	timeOfDay := fmt.Sprintf("%02d:%02d:00", rule.Hour, rule.Minute)
	_, err = tx.Exec(ctx, `
		INSERT INTO card_schedules (
			id, workspace_id, card_id, enabled, frequency,
			day_of_week, day_of_month, time_of_day, timezone, next_trigger_at
		)
		VALUES ($1, $2, $3, true, $4, $5, $6, $7::time, $8, $9)
	`, scheduleID, workspaceID, cardID, string(rule.Frequency), dayOfWeek, dayOfMonth, timeOfDay, rule.Timezone, next)
	if err != nil {
		return CardSchedule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CardSchedule{}, err
	}
	return CardSchedule{
		ID: scheduleID, WorkspaceID: workspaceID, CardID: cardID,
		Rule: rule, Enabled: true, NextTriggerAt: next,
	}, nil
}

func (s *Store) ProcessDueSchedules(ctx context.Context, workspaceID uuid.UUID, now time.Time, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id
		FROM card_schedules
		WHERE workspace_id = $1 AND enabled AND next_trigger_at <= $2
		ORDER BY next_trigger_at, id
		LIMIT $3
	`, workspaceID, now, limit)
	if err != nil {
		return nil, err
	}
	var scheduleIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		scheduleIDs = append(scheduleIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	created := make([]uuid.UUID, 0, len(scheduleIDs))
	for _, scheduleID := range scheduleIDs {
		triggerID, ok, err := s.ProcessDueSchedule(ctx, workspaceID, scheduleID, now)
		if err != nil {
			return created, err
		}
		if ok {
			created = append(created, triggerID)
		}
	}
	return created, nil
}

// ProcessDueSchedule 与其他 Card 状态事务一样先锁 Card、再锁 Schedule，
// 避免 Scheduler 与暂停/恢复操作使用相反锁顺序。数据库 unique index 再兜底 occurrence 去重。
// next occurrence 总是基于 now 计算，因此服务离线多周期后固定 skip，不会批量补历史 Trigger。
func (s *Store) ProcessDueSchedule(
	ctx context.Context,
	workspaceID, scheduleID uuid.UUID,
	now time.Time,
) (uuid.UUID, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)

	var cardID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT card_id
		FROM card_schedules
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, scheduleID).Scan(&cardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, domain.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return uuid.Nil, false, err
	}

	row, err := loadScheduleForUpdate(ctx, tx, workspaceID, scheduleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, domain.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if !row.Enabled || row.NextTriggerAt.After(now) {
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, false, err
		}
		return uuid.Nil, false, nil
	}

	rule, err := row.Rule()
	if err != nil {
		return uuid.Nil, false, err
	}
	next, err := rule.NextAfter(now)
	if err != nil {
		return uuid.Nil, false, err
	}

	var triggerID uuid.UUID
	created := false
	active := row.CardKind == domain.CardKindOngoing && row.OngoingState == domain.OngoingStateActive
	if active {
		triggerID = uuid.New()
		err = tx.QueryRow(ctx, `
			INSERT INTO triggers (
				id, workspace_id, card_id, cause, dispatch_mode, schedule_id, scheduled_for
			)
			VALUES ($1, $2, $3, 'scheduled_followup', 'enqueue', $4, $5)
			ON CONFLICT (schedule_id, scheduled_for)
			WHERE schedule_id IS NOT NULL
			DO NOTHING
			RETURNING id
		`, triggerID, workspaceID, row.CardID, scheduleID, row.NextTriggerAt).Scan(&triggerID)
		if err == nil {
			created = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, err
		}
	}

	if active {
		_, err = tx.Exec(ctx, `
			UPDATE card_schedules
			SET last_triggered_at = $3, next_trigger_at = $4, updated_at = now()
			WHERE workspace_id = $1 AND id = $2
		`, workspaceID, scheduleID, row.NextTriggerAt, next)
	} else {
		// Paused/Archived 仍向未来推进 next_trigger_at，避免 due scanner 热循环；恢复 Active 时会再次重算。
		_, err = tx.Exec(ctx, `
			UPDATE card_schedules
			SET next_trigger_at = $3, updated_at = now()
			WHERE workspace_id = $1 AND id = $2
		`, workspaceID, scheduleID, next)
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return triggerID, created, nil
}

type scheduleRow struct {
	CardID        uuid.UUID
	Enabled       bool
	Frequency     domain.ScheduleFrequency
	DayOfWeek     pgtype.Int2
	DayOfMonth    pgtype.Int2
	TimeOfDay     pgtype.Time
	Timezone      string
	NextTriggerAt time.Time
	CardKind      domain.CardKind
	OngoingState  domain.OngoingState
}

func loadScheduleForUpdate(ctx context.Context, tx pgx.Tx, workspaceID, scheduleID uuid.UUID) (scheduleRow, error) {
	var row scheduleRow
	err := tx.QueryRow(ctx, `
		SELECT cs.card_id, cs.enabled, cs.frequency, cs.day_of_week, cs.day_of_month,
		       cs.time_of_day, cs.timezone, cs.next_trigger_at, c.kind, c.ongoing_state
		FROM card_schedules cs
		JOIN cards c ON c.workspace_id = cs.workspace_id AND c.id = cs.card_id
		WHERE cs.workspace_id = $1 AND cs.id = $2
		FOR UPDATE OF cs
	`, workspaceID, scheduleID).Scan(
		&row.CardID, &row.Enabled, &row.Frequency, &row.DayOfWeek, &row.DayOfMonth,
		&row.TimeOfDay, &row.Timezone, &row.NextTriggerAt, &row.CardKind, &row.OngoingState,
	)
	return row, err
}

func (r scheduleRow) Rule() (domain.ScheduleRule, error) {
	micros := r.TimeOfDay.Microseconds
	hour := int(micros / int64(time.Hour/time.Microsecond))
	minute := int((micros % int64(time.Hour/time.Microsecond)) / int64(time.Minute/time.Microsecond))
	rule := domain.ScheduleRule{
		Frequency: r.Frequency,
		Hour:      hour,
		Minute:    minute,
		Timezone:  r.Timezone,
	}
	if r.DayOfWeek.Valid {
		weekday := time.Weekday(r.DayOfWeek.Int16)
		rule.DayOfWeek = &weekday
	}
	if r.DayOfMonth.Valid {
		day := int(r.DayOfMonth.Int16)
		rule.DayOfMonth = &day
	}
	return rule, rule.Validate()
}

func recalculateCardSchedulesTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID uuid.UUID,
	now time.Time,
) error {
	rows, err := tx.Query(ctx, `
		SELECT id, enabled, frequency, day_of_week, day_of_month, time_of_day, timezone, next_trigger_at
		FROM card_schedules
		WHERE workspace_id = $1 AND card_id = $2
		FOR UPDATE
	`, workspaceID, cardID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type pendingUpdate struct {
		id   uuid.UUID
		next time.Time
	}
	var updates []pendingUpdate
	for rows.Next() {
		var id uuid.UUID
		var row scheduleRow
		if err := rows.Scan(
			&id, &row.Enabled, &row.Frequency, &row.DayOfWeek, &row.DayOfMonth,
			&row.TimeOfDay, &row.Timezone, &row.NextTriggerAt,
		); err != nil {
			return err
		}
		if !row.Enabled {
			continue
		}
		rule, err := row.Rule()
		if err != nil {
			return err
		}
		next, err := rule.NextAfter(now)
		if err != nil {
			return err
		}
		updates = append(updates, pendingUpdate{id: id, next: next})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, update := range updates {
		if _, err := tx.Exec(ctx, `
			UPDATE card_schedules SET next_trigger_at = $3, updated_at = now()
			WHERE workspace_id = $1 AND id = $2
		`, workspaceID, update.id, update.next); err != nil {
			return err
		}
	}
	return nil
}
