package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
)

// lockCard is the shared row-lock primitive for every Card mutation. It is not
// tied to the retired Trigger/Run runtime.
func lockCard(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID uuid.UUID,
) error {
	var id uuid.UUID
	err := tx.QueryRow(
		ctx,
		"SELECT id FROM cards WHERE workspace_id=$1 AND id=$2 FOR UPDATE",
		workspaceID,
		cardID,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// requireHumanSource protects product transitions that remain Human-only.
func requireHumanSource(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sourceID uuid.UUID,
) error {
	var kind string
	err := tx.QueryRow(ctx, `
		SELECT kind
		FROM sources
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, sourceID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if kind != string(domain.SourceKindHuman) {
		return domain.ErrHumanRequired
	}
	return nil
}
