package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func (s *Store) MoveNormalCard(
	ctx context.Context,
	workspaceID, cardID, actorSourceID uuid.UUID,
	to domain.NormalColumn,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	if err := moveNormalCardTx(ctx, tx, workspaceID, cardID, actorSourceID, to, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func moveNormalCardTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID, actorSourceID uuid.UUID,
	to domain.NormalColumn,
	runID *uuid.UUID,
) error {
	var kind string
	var from string
	if err := tx.QueryRow(ctx, `
		SELECT kind, normal_column
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID).Scan(&kind, &from); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return err
	}
	if kind != string(domain.CardKindNormal) {
		return fmt.Errorf("%w: normal card required", domain.ErrInvalidTransition)
	}

	var actorKind string
	if err := tx.QueryRow(ctx, `
		SELECT kind FROM sources WHERE workspace_id = $1 AND id = $2
	`, workspaceID, actorSourceID).Scan(&actorKind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return err
	}
	if err := domain.CanMoveNormalCard(domain.NormalColumn(from), to, domain.SourceKind(actorKind)); err != nil {
		return err
	}
	if from == string(to) {
		return nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE cards
		SET normal_column = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID, string(to)); err != nil {
		return err
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id, run_id, content
		)
		VALUES ($1, $2, $3, 'status_change', $4, $5, $6)
	`, uuid.New(), workspaceID, cardID, actorSourceID, runID, fmt.Sprintf("%s -> %s", from, to))
	return err
}

func (s *Store) SetOngoingState(
	ctx context.Context,
	workspaceID, cardID, actorSourceID uuid.UUID,
	to domain.OngoingState,
	now time.Time,
) error {
	if to != domain.OngoingStateActive && to != domain.OngoingStatePaused && to != domain.OngoingStateArchived {
		return fmt.Errorf("%w: invalid ongoing state %q", domain.ErrInvalidTransition, to)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	var kind, from string
	if err := tx.QueryRow(ctx, `
		SELECT kind, ongoing_state
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID).Scan(&kind, &from); err != nil {
		return err
	}
	if kind != string(domain.CardKindOngoing) {
		return fmt.Errorf("%w: ongoing card required", domain.ErrInvalidTransition)
	}
	if from == string(to) {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE cards SET ongoing_state = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID, string(to)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (id, workspace_id, card_id, event_type, actor_source_id, content)
		VALUES ($1, $2, $3, 'status_change', $4, $5)
	`, uuid.New(), workspaceID, cardID, actorSourceID, fmt.Sprintf("%s -> %s", from, to)); err != nil {
		return err
	}

	// Paused/Archived 期间不补跑。重新 Active 时从“现在”重新计算所有 enabled schedule 的未来 occurrence。
	if to == domain.OngoingStateActive {
		if err := recalculateCardSchedulesTx(ctx, tx, workspaceID, cardID, now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) Health(ctx context.Context, workspaceID, cardID uuid.UUID) (domain.CheckinStatus, error) {
	event, err := s.queries.LatestCheckin(ctx, db.LatestCheckinParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CheckinUnknown, nil
	}
	if err != nil {
		return "", err
	}
	if !event.CheckinStatus.Valid {
		return domain.CheckinUnknown, nil
	}
	return domain.CheckinStatus(event.CheckinStatus.String), nil
}
