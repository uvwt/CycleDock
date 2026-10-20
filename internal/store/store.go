package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

const DefaultClaimLease = 2 * time.Minute

type Store struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, queries: db.New(pool)}
}

func (s *Store) CreateWorkspace(ctx context.Context, name string) (db.Workspace, error) {
	return s.queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: uuid.New(), Name: name})
}

func (s *Store) CreateSource(ctx context.Context, workspaceID uuid.UUID, kind domain.SourceKind, name string) (db.Source, error) {
	return s.queries.CreateSource(ctx, db.CreateSourceParams{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		Kind:        string(kind),
		Name:        name,
	})
}

func (s *Store) CreateProject(ctx context.Context, workspaceID uuid.UUID, name, description, instructions string) (db.Project, error) {
	return s.queries.CreateProject(ctx, db.CreateProjectParams{
		ID:           uuid.New(),
		WorkspaceID:  workspaceID,
		Name:         name,
		Description:  description,
		Instructions: instructions,
	})
}

func (s *Store) CreateNormalCard(ctx context.Context, workspaceID, projectID, sourceID uuid.UUID, title, description string) (db.Card, error) {
	return s.queries.CreateCard(ctx, db.CreateCardParams{
		ID:                    uuid.New(),
		WorkspaceID:           workspaceID,
		ProjectID:             projectID,
		Kind:                  string(domain.CardKindNormal),
		Title:                 title,
		Description:           description,
		NormalColumn:          pgtype.Text{String: string(domain.NormalColumnDiscussion), Valid: true},
		OngoingState:          pgtype.Text{},
		RequiredConfirmations: 1,
		CreatedBySourceID:     sourceID,
	})
}

func (s *Store) CreateOngoingCard(ctx context.Context, workspaceID, projectID, sourceID uuid.UUID, title, description string) (db.Card, error) {
	return s.queries.CreateCard(ctx, db.CreateCardParams{
		ID:                    uuid.New(),
		WorkspaceID:           workspaceID,
		ProjectID:             projectID,
		Kind:                  string(domain.CardKindOngoing),
		Title:                 title,
		Description:           description,
		NormalColumn:          pgtype.Text{},
		OngoingState:          pgtype.Text{String: string(domain.OngoingStateActive), Valid: true},
		RequiredConfirmations: 1,
		CreatedBySourceID:     sourceID,
	})
}

func (s *Store) GetCard(ctx context.Context, workspaceID, cardID uuid.UUID) (db.Card, error) {
	card, err := s.queries.GetCard(ctx, db.GetCardParams{WorkspaceID: workspaceID, ID: cardID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Card{}, domain.ErrNotFound
	}
	return card, err
}

func (s *Store) ListCardEvents(ctx context.Context, workspaceID, cardID uuid.UUID) ([]db.Event, error) {
	return s.queries.ListCardEvents(ctx, db.ListCardEventsParams{WorkspaceID: workspaceID, CardID: cardID})
}

func mapConstraintError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.ConstraintName {
	case "agent_runs_trigger_uq", "agent_runs_one_current_per_card_uq":
		return fmt.Errorf("%w: %s", domain.ErrConflict, pgErr.ConstraintName)
	case "events_comment_idempotency_uq",
		"triggers_cause_event_uq",
		"triggers_schedule_occurrence_uq",
		"cards_source_create_idempotency_uq",
		"events_source_type_idempotency_uq",
		"events_confirm_source_uq",
		"triggers_source_followup_idempotency_uq",
		"triggers_retry_once_uq":
		return fmt.Errorf("%w: %s", domain.ErrConflict, pgErr.ConstraintName)
	default:
		return err
	}
}
