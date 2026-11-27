package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

// CardReadState summarizes one human source's read cursor and unread flag for a single Card.
type CardReadState struct {
	LatestMessageSequence   int64
	LastReadMessageSequence int64
	MarkedUnread            bool
	Unread                  bool
}

// GetCardReadState loads one human source's current read cursor and unread state for a Card.
func (s *Store) GetCardReadState(
	ctx context.Context,
	workspaceID, cardID, sourceID uuid.UUID,
) (CardReadState, error) {
	row, err := s.queries.GetCardReadState(ctx, db.GetCardReadStateParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
		SourceID:    sourceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CardReadState{}, domain.ErrNotFound
	}
	if err != nil {
		return CardReadState{}, err
	}
	return CardReadState{
		LatestMessageSequence:   row.LatestMessageSequence,
		LastReadMessageSequence: row.LastReadMessageSequence,
		MarkedUnread:            row.MarkedUnread,
		Unread:                  row.Unread,
	}, nil
}

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

// MarkCardUnread explicitly marks one Card as unread for one human source,
// preserving its existing read cursor sequence so the card remains visibly
// unread even when it has no new messages or no messages at all.
func (s *Store) MarkCardUnread(
	ctx context.Context,
	workspaceID, cardID, sourceID uuid.UUID,
) (int64, error) {
	sequence, err := s.queries.MarkCardUnread(ctx, db.MarkCardUnreadParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
		SourceID:    sourceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	return sequence, err
}
