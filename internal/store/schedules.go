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

const scheduledTurnContent = "Run the scheduled follow-up for this ongoing Card. Check the current state, perform the ongoing work that is due now, and record an evidence-based check-in."

func (s *Store) ProcessDueScheduleTurns(
	ctx context.Context,
	workspaceID uuid.UUID,
	now time.Time,
	limit int,
) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id
		FROM card_schedules
		WHERE workspace_id=$1 AND enabled AND next_trigger_at <= $2
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
		turnID, ok, err := s.ProcessDueScheduleTurn(ctx, workspaceID, scheduleID, now)
		if err != nil {
			return created, err
		}
		if ok {
			created = append(created, turnID)
		}
	}
	if len(created) > 0 {
		s.notifyWorkReady()
	}
	return created, nil
}

func (s *Store) ProcessDueScheduleTurn(
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
		WHERE workspace_id=$1 AND id=$2
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

	var turnID uuid.UUID
	created := false
	active := row.CardKind == domain.CardKindOngoing &&
		row.OngoingState == domain.OngoingStateActive
	if active {
		turnID = uuid.New()
		err = tx.QueryRow(ctx, `
			INSERT INTO card_turns (
				id, workspace_id, card_id, source, content,
				dispatch_mode, schedule_id, scheduled_for
			)
			VALUES ($1,$2,$3,'schedule',$4,'enqueue',$5,$6)
			ON CONFLICT (schedule_id, scheduled_for)
			WHERE schedule_id IS NOT NULL
			DO NOTHING
			RETURNING id
		`, turnID, workspaceID, row.CardID, scheduledTurnContent,
			scheduleID, row.NextTriggerAt).Scan(&turnID)
		if err == nil {
			created = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, err
		}
	}

	if active {
		_, err = tx.Exec(ctx, `
			UPDATE card_schedules
			SET last_triggered_at=$3, next_trigger_at=$4, updated_at=now()
			WHERE workspace_id=$1 AND id=$2
		`, workspaceID, scheduleID, row.NextTriggerAt, next)
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE card_schedules
			SET next_trigger_at=$3, updated_at=now()
			WHERE workspace_id=$1 AND id=$2
		`, workspaceID, scheduleID, next)
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return turnID, created, nil
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
