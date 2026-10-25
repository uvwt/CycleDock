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
	"github.com/uvwt/CycleDock/internal/store/db"
)

func (s *Store) ListProjects(ctx context.Context, workspaceID uuid.UUID) ([]db.Project, error) {
	return s.queries.ListProjects(ctx, workspaceID)
}

func (s *Store) GetProject(ctx context.Context, workspaceID, projectID uuid.UUID) (db.Project, error) {
	project, err := s.queries.GetProject(ctx, db.GetProjectParams{WorkspaceID: workspaceID, ID: projectID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Project{}, domain.ErrNotFound
	}
	return project, err
}

func (s *Store) UpdateProject(
	ctx context.Context,
	workspaceID, projectID uuid.UUID,
	name, description, instructions string,
) (db.Project, error) {
	project, err := s.queries.UpdateProject(ctx, db.UpdateProjectParams{
		Name:         name,
		Description:  description,
		Instructions: instructions,
		WorkspaceID:  workspaceID,
		ID:           projectID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Project{}, domain.ErrNotFound
	}
	return project, err
}

func (s *Store) ListProjectCards(ctx context.Context, workspaceID, projectID uuid.UUID) ([]db.Card, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	return s.queries.ListProjectCards(ctx, db.ListProjectCardsParams{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
	})
}

func (s *Store) ListProjectCardSummaries(
	ctx context.Context,
	workspaceID, projectID uuid.UUID,
) ([]db.ListProjectCardSummariesRow, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	return s.queries.ListProjectCardSummaries(ctx, db.ListProjectCardSummariesParams{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
	})
}

func (s *Store) ListProjectSchedules(
	ctx context.Context,
	workspaceID, projectID uuid.UUID,
) ([]db.ListProjectSchedulesRow, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	return s.queries.ListProjectSchedules(ctx, db.ListProjectSchedulesParams{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
	})
}

func (s *Store) ListProjectActivity(
	ctx context.Context,
	workspaceID, projectID uuid.UUID,
	limit int32,
) ([]db.ListProjectActivityRow, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 60
	}
	return s.queries.ListProjectActivity(ctx, db.ListProjectActivityParams{
		WorkspaceID:   workspaceID,
		ProjectID:     projectID,
		ActivityLimit: limit,
	})
}

func (s *Store) UpdateCardText(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	title, description string,
) (db.Card, error) {
	card, err := s.queries.UpdateCardText(ctx, db.UpdateCardTextParams{
		Title:       title,
		Description: description,
		WorkspaceID: workspaceID,
		ID:          cardID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Card{}, domain.ErrNotFound
	}
	return card, err
}

func (s *Store) ListSchedules(ctx context.Context, workspaceID, cardID uuid.UUID) ([]db.CardSchedule, error) {
	if _, err := s.GetCard(ctx, workspaceID, cardID); err != nil {
		return nil, err
	}
	return s.queries.ListCardSchedules(ctx, db.ListCardSchedulesParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
	})
}

func (s *Store) LatestCheckin(ctx context.Context, workspaceID, cardID uuid.UUID) (*db.Event, error) {
	event, err := s.queries.LatestCheckinForCard(ctx, db.LatestCheckinForCardParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &event, nil
}

func (s *Store) ListCardTimeline(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
) ([]db.ListCardTimelineRow, error) {
	if _, err := s.GetCard(ctx, workspaceID, cardID); err != nil {
		return nil, err
	}
	return s.queries.ListCardTimeline(ctx, db.ListCardTimelineParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
	})
}

type UpdateScheduleParams struct {
	WorkspaceID uuid.UUID
	CardID      uuid.UUID
	ScheduleID  uuid.UUID
	Rule        domain.ScheduleRule
	Enabled     bool
	After       time.Time
}

func (s *Store) UpdateSchedule(ctx context.Context, p UpdateScheduleParams) (CardSchedule, error) {
	if err := p.Rule.Validate(); err != nil {
		return CardSchedule{}, err
	}
	next, err := p.Rule.NextAfter(p.After)
	if err != nil {
		return CardSchedule{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CardSchedule{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return CardSchedule{}, err
	}
	var kind string
	if err := tx.QueryRow(ctx,
		"SELECT kind FROM cards WHERE workspace_id = $1 AND id = $2",
		p.WorkspaceID, p.CardID,
	).Scan(&kind); err != nil {
		return CardSchedule{}, err
	}
	if kind != string(domain.CardKindOngoing) {
		return CardSchedule{}, fmt.Errorf("%w: schedule requires ongoing card", domain.ErrInvalidTransition)
	}

	q := db.New(tx)
	affected, err := q.UpdateCardSchedule(ctx, db.UpdateCardScheduleParams{
		Enabled:       p.Enabled,
		Frequency:     string(p.Rule.Frequency),
		DayOfWeek:     weekdayValue(p.Rule.DayOfWeek),
		DayOfMonth:    dayOfMonthValue(p.Rule.DayOfMonth),
		TimeOfDay:     scheduleTimeValue(p.Rule.Hour, p.Rule.Minute),
		Timezone:      p.Rule.Timezone,
		NextTriggerAt: pgtype.Timestamptz{Time: next, Valid: true},
		WorkspaceID:   p.WorkspaceID,
		CardID:        p.CardID,
		ID:            p.ScheduleID,
	})
	if err != nil {
		return CardSchedule{}, err
	}
	if affected != 1 {
		return CardSchedule{}, domain.ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return CardSchedule{}, err
	}
	return CardSchedule{
		ID:            p.ScheduleID,
		WorkspaceID:   p.WorkspaceID,
		CardID:        p.CardID,
		Rule:          p.Rule,
		Enabled:       p.Enabled,
		NextTriggerAt: next,
	}, nil
}

func (s *Store) DeleteSchedule(ctx context.Context, workspaceID, cardID, scheduleID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	q := db.New(tx)
	hasHistory, err := q.ScheduleHasTurns(ctx, db.ScheduleHasTurnsParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
		ScheduleID:  scheduleID,
	})
	if err != nil {
		return err
	}
	if hasHistory {
		// Scheduled Turn 是长期任务事实。已有历史后不允许级联删除 Schedule，
		// 否则会破坏 Turn/Attempt 审计链；此时应把 Schedule disabled。
		return fmt.Errorf("%w: schedule has turn history; disable it instead", domain.ErrConflict)
	}
	affected, err := q.DeleteCardSchedule(ctx, db.DeleteCardScheduleParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
		ID:          scheduleID,
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return domain.ErrNotFound
	}
	return tx.Commit(ctx)
}

func weekdayValue(day *time.Weekday) pgtype.Int2 {
	if day == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*day), Valid: true}
}

func dayOfMonthValue(day *int) pgtype.Int2 {
	if day == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*day), Valid: true}
}

func scheduleTimeValue(hour, minute int) pgtype.Time {
	micros := int64(hour)*int64(time.Hour/time.Microsecond) +
		int64(minute)*int64(time.Minute/time.Microsecond)
	return pgtype.Time{Microseconds: micros, Valid: true}
}
