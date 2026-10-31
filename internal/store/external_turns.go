package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

type SubmitAgentTurnResult struct {
	EventID uuid.UUID
	TurnID  uuid.UUID
	Reused  bool
}

func (s *Store) SubmitAgentTurn(
	ctx context.Context,
	workspaceID, cardID, sourceID uuid.UUID,
	content, idempotencyKey string,
) (SubmitAgentTurnResult, error) {
	content = strings.TrimSpace(content)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if content == "" || idempotencyKey == "" {
		return SubmitAgentTurnResult{}, fmt.Errorf("%w: content and idempotency key are required", domain.ErrConflict)
	}

	requestHash := stableRequestHash(
		"agent_turn",
		workspaceID.String(),
		cardID.String(),
		sourceID.String(),
		content,
	)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SubmitAgentTurnResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockRequestKey(ctx, tx, workspaceID, sourceID, idempotencyKey); err != nil {
		return SubmitAgentTurnResult{}, err
	}
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return SubmitAgentTurnResult{}, err
	}
	if err := ensureAgentSource(ctx, tx, workspaceID, sourceID); err != nil {
		return SubmitAgentTurnResult{}, err
	}
	if err := ensureDiscussionAcceptsWork(ctx, tx, workspaceID, cardID); err != nil {
		return SubmitAgentTurnResult{}, err
	}

	if existing, found, err := findSessionComment(
		ctx, tx, workspaceID, sourceID, idempotencyKey,
	); err != nil {
		return SubmitAgentTurnResult{}, err
	} else if found {
		if existing.RequestHash != requestHash || existing.CardID != cardID {
			return SubmitAgentTurnResult{}, domain.ErrIdempotencyMismatch
		}
		if err := tx.Commit(ctx); err != nil {
			return SubmitAgentTurnResult{}, err
		}
		s.notifyWorkReady()
		return SubmitAgentTurnResult{
			EventID: existing.EventID,
			TurnID:  existing.TurnID,
			Reused:  true,
		}, nil
	}

	eventID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id,
			content, idempotency_key, request_hash
		)
		VALUES ($1,$2,$3,'comment',$4,$5,$6,$7)
	`, eventID, workspaceID, cardID, sourceID, content, idempotencyKey, requestHash)
	if err != nil {
		return SubmitAgentTurnResult{}, mapConstraintError(err)
	}

	turn, err := insertTurn(ctx, tx, insertTurnParams{
		WorkspaceID:       workspaceID,
		CardID:            cardID,
		Source:            domain.TurnSourceSystem,
		Content:           content,
		CreatedBySourceID: uuid.NullUUID{UUID: sourceID, Valid: true},
		SourceEventID:     uuid.NullUUID{UUID: eventID, Valid: true},
		DispatchMode:      domain.DispatchModeEnqueue,
		IdempotencyKey:    idempotencyKey,
		RequestHash:       requestHash,
	})
	if err != nil {
		return SubmitAgentTurnResult{}, err
	}
	if err := relayVisibleMessage(ctx, tx, workspaceID, cardID, eventID, content); err != nil {
		return SubmitAgentTurnResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubmitAgentTurnResult{}, err
	}
	s.notifyWorkReady()
	return SubmitAgentTurnResult{EventID: eventID, TurnID: turn.ID}, nil
}
