package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentdata"
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
	sourceCardID uuid.NullUUID,
	dispatchMode domain.DispatchMode,
) (SubmitAgentTurnResult, error) {
	content, _ = agentdata.NormalizeText(strings.TrimSpace(content))
	idempotencyKey, _ = agentdata.NormalizeText(strings.TrimSpace(idempotencyKey))
	if content == "" || idempotencyKey == "" {
		return SubmitAgentTurnResult{}, fmt.Errorf("%w: content and idempotency key are required", domain.ErrConflict)
	}
	if sourceCardID.Valid && sourceCardID.UUID == cardID {
		return SubmitAgentTurnResult{}, fmt.Errorf("%w: target card must differ from source card", domain.ErrConflict)
	}
	if dispatchMode == "" {
		dispatchMode = domain.DispatchModeEnqueue
	}
	if dispatchMode != domain.DispatchModeEnqueue && dispatchMode != domain.DispatchModeInterrupt {
		return SubmitAgentTurnResult{}, fmt.Errorf("%w: invalid dispatch mode %q", domain.ErrConflict, dispatchMode)
	}

	var sourceCardIDStr string
	if sourceCardID.Valid {
		sourceCardIDStr = sourceCardID.UUID.String()
	}

	requestHash := stableRequestHash(
		"agent_turn",
		workspaceID.String(),
		cardID.String(),
		sourceID.String(),
		content,
		sourceCardIDStr,
		string(dispatchMode),
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
			content, idempotency_key, request_hash, source_card_id
		)
		VALUES ($1,$2,$3,'comment',$4,$5,$6,$7,$8)
	`, eventID, workspaceID, cardID, sourceID, content, idempotencyKey, requestHash, sourceCardID)
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
		DispatchMode:      dispatchMode,
		IdempotencyKey:    idempotencyKey,
		RequestHash:       requestHash,
	})
	if err != nil {
		return SubmitAgentTurnResult{}, err
	}
	if err := supersedeAttemptForTurn(
		ctx, tx, workspaceID, cardID, dispatchMode, "superseded by interrupting agent turn", turn.CreatedAt,
	); err != nil {
		return SubmitAgentTurnResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubmitAgentTurnResult{}, err
	}
	s.notifyWorkReady()
	return SubmitAgentTurnResult{EventID: eventID, TurnID: turn.ID}, nil
}
