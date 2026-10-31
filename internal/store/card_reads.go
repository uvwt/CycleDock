package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

// MarkCardRead advances one human source's durable read cursor to the newest
// user-visible message currently committed on the Card. GREATEST in SQL keeps
// concurrent/retried reads monotonic.
func (s *Store) MarkCardRead(
	ctx context.Context,
	workspaceID, cardID, sourceID uuid.UUID,
) (int64, error) {
	sequence, err := s.queries.MarkCardRead(ctx, db.MarkCardReadParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
		SourceID:    sourceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	return sequence, err
}
