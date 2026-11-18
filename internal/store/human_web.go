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

const defaultProjectName = "默认项目"

func (s *Store) ListProjects(ctx context.Context, workspaceID uuid.UUID) ([]db.Project, error) {
	projects, err := s.queries.ListProjects(ctx, workspaceID)
	if err != nil || len(projects) > 0 {
		return projects, err
	}

	// 工作空间还没有项目时，通过工作空间行锁串行化创建默认项目，避免并发请求重复创建。
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var lockedWorkspaceID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM workspaces
		WHERE id = $1
		FOR UPDATE
	`, workspaceID).Scan(&lockedWorkspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return []db.Project{}, nil
	}
	if err != nil {
		return nil, err
	}

	q := db.New(tx)
	projects, err = q.ListProjects(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		created, err := q.CreateProject(ctx, db.CreateProjectParams{
			ID:          uuid.New(),
			WorkspaceID: workspaceID,
			Name:        defaultProjectName,
			Description: "",
		})
		if err != nil {
			return nil, err
		}
		projects = []db.Project{created}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return projects, nil
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
	name, description string,
) (db.Project, error) {
	project, err := s.queries.UpdateProject(ctx, db.UpdateProjectParams{
		Name:        name,
		Description: description,
		WorkspaceID: workspaceID,
		ID:          projectID,
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
	workspaceID, projectID, sourceID uuid.UUID,
) ([]db.ListProjectCardSummariesRow, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	return s.queries.ListProjectCardSummaries(ctx, db.ListProjectCardSummariesParams{
		SourceID:    sourceID,
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
	})
}

func (s *Store) ListProjectActiveCardSummaries(
	ctx context.Context,
	workspaceID, projectID, sourceID uuid.UUID,
) ([]db.ListProjectActiveCardSummariesRow, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	return s.queries.ListProjectActiveCardSummaries(ctx, db.ListProjectActiveCardSummariesParams{
		SourceID:    sourceID,
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
	})
}

func (s *Store) ListProjectDoneCardsPaged(
	ctx context.Context,
	workspaceID, projectID, sourceID uuid.UUID,
	limit, offset int32,
) ([]db.ListProjectDoneCardsPagedRow, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 30
	} else if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return s.queries.ListProjectDoneCardsPaged(ctx, db.ListProjectDoneCardsPagedParams{
		SourceID:    sourceID,
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		PageLimit:   limit,
		PageOffset:  offset,
	})
}

func (s *Store) CountProjectDoneCards(
	ctx context.Context,
	workspaceID, projectID uuid.UUID,
) (int64, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return 0, err
	}
	return s.queries.CountProjectDoneCards(ctx, db.CountProjectDoneCardsParams{
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
	sourceID uuid.NullUUID,
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
		AgentSourceID: sourceID,
	})
}

func (s *Store) ListProjectActivityAgents(
	ctx context.Context,
	workspaceID, projectID uuid.UUID,
) ([]db.ListActivityAgentsRow, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	return s.queries.ListActivityAgents(ctx, workspaceID)
}

func (s *Store) UpdateCardText(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	title, description string,
) (db.Card, error) {
	// 主讨论标题与描述固定，禁止修改信息。
	isMain, err := isMainDiscussion(ctx, s.pool, workspaceID, cardID)
	if err != nil {
		return db.Card{}, err
	}
	if isMain {
		return db.Card{}, fmt.Errorf("%w: main discussion text cannot be modified", domain.ErrConflict)
	}
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

func (s *Store) SetCardPinned(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	pinned bool,
) (db.Card, error) {
	// 主讨论始终置顶，禁止取消置顶。
	if !pinned {
		isMain, err := isMainDiscussion(ctx, s.pool, workspaceID, cardID)
		if err != nil {
			return db.Card{}, err
		}
		if isMain {
			return db.Card{}, fmt.Errorf("%w: main discussion is always pinned", domain.ErrConflict)
		}
	}
	var pinnedAt pgtype.Timestamptz
	if pinned {
		pinnedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	}
	card, err := s.queries.SetCardPinned(ctx, db.SetCardPinnedParams{
		PinnedAt:    pinnedAt,
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

func (s *Store) ListCardTimelineBefore(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	beforeSequence int64,
	limit int32,
) ([]db.ListCardTimelineBeforeRow, error) {
	if _, err := s.GetCard(ctx, workspaceID, cardID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	} else if limit > 100 {
		limit = 100
	}
	if beforeSequence <= 0 {
		beforeSequence = 1<<62 - 1
	}
	return s.queries.ListCardTimelineBefore(ctx, db.ListCardTimelineBeforeParams{
		WorkspaceID:    workspaceID,
		CardID:         cardID,
		BeforeSequence: pgtype.Int8{Int64: beforeSequence, Valid: true},
		PageLimit:      limit,
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
		Enabled:         p.Enabled,
		Frequency:       string(p.Rule.Frequency),
		DayOfWeek:       weekdayValue(p.Rule.DayOfWeek),
		DayOfMonth:      dayOfMonthValue(p.Rule.DayOfMonth),
		TimeOfDay:       scheduleTimeValue(p.Rule.Frequency, p.Rule.Hour, p.Rule.Minute),
		IntervalMinutes: intervalMinutesValue(p.Rule.IntervalMinutes),
		Timezone:        p.Rule.Timezone,
		NextTriggerAt:   pgtype.Timestamptz{Time: next, Valid: true},
		WorkspaceID:     p.WorkspaceID,
		CardID:          p.CardID,
		ID:              p.ScheduleID,
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
	if _, err := q.GetCardSchedule(ctx, db.GetCardScheduleParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
		ID:          scheduleID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return err
	}
	hasHistory, err := q.ScheduleHasTurns(ctx, db.ScheduleHasTurnsParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
		ScheduleID:  scheduleID,
	})
	if err != nil {
		return err
	}
	var affected int64
	if hasHistory {
		// 已有 Turn 历史的 Schedule 不能物理删除，否则会破坏 Turn/Attempt 审计链；
		// 软归档同时停用并隐藏规则，使历史关联完整保留且后续不再触发。
		affected, err = q.ArchiveCardSchedule(ctx, db.ArchiveCardScheduleParams{
			WorkspaceID: workspaceID,
			CardID:      cardID,
			ID:          scheduleID,
		})
	} else {
		affected, err = q.DeleteCardSchedule(ctx, db.DeleteCardScheduleParams{
			WorkspaceID: workspaceID,
			CardID:      cardID,
			ID:          scheduleID,
		})
	}
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

func intervalMinutesValue(minutes *int) pgtype.Int4 {
	if minutes == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*minutes), Valid: true}
}

func scheduleTimeValue(freq domain.ScheduleFrequency, hour, minute int) pgtype.Time {
	if freq == domain.ScheduleInterval {
		return pgtype.Time{}
	}
	micros := int64(hour)*int64(time.Hour/time.Microsecond) +
		int64(minute)*int64(time.Minute/time.Microsecond)
	return pgtype.Time{Microseconds: micros, Valid: true}
}
