package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	adkagent "google.golang.org/adk/v2/agent"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type updateCardToolInput struct {
	CardID                string  `json:"card_id"`
	Title                 *string `json:"title"`
	Description           *string `json:"description"`
	Pinned                *bool   `json:"pinned"`
	TodoState             *string `json:"todo_state"`
	OngoingState          *string `json:"ongoing_state"`
	DiscussionState       *string `json:"discussion_state"`
	RequiredConfirmations *int32  `json:"required_confirmations"`
}

func cardToolView(card db.Card) map[string]any {
	out := map[string]any{
		"id":                     card.ID.String(),
		"project_id":             card.ProjectID.String(),
		"kind":                   card.Kind,
		"title":                  card.Title,
		"description":            card.Description,
		"pinned":                 card.PinnedAt.Valid,
		"required_confirmations": card.RequiredConfirmations,
	}
	if card.PinnedAt.Valid {
		out["pinned_at"] = card.PinnedAt.Time.UTC().Format(time.RFC3339)
	}
	if card.DiscussionState.Valid {
		out["discussion_state"] = card.DiscussionState.String
	}
	if card.TodoState.Valid {
		out["todo_state"] = card.TodoState.String
	}
	if card.OngoingState.Valid {
		out["ongoing_state"] = card.OngoingState.String
	}
	if card.UpdatedAt.Valid {
		out["updated_at"] = card.UpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	return out
}

func (r attemptToolRuntime) updateCardTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	if r.context.IsBuiltInProactive {
		return nil, errors.New("cycledock_update_card is disabled during built-in proactive turns")
	}
	if r.store == nil {
		return nil, errors.New("store is not configured")
	}

	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var input updateCardToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("invalid update_card arguments: %w", err)
	}

	targetCardID := r.context.CardID
	if trimmed := strings.TrimSpace(input.CardID); trimmed != "" {
		parsed, err := uuid.Parse(trimmed)
		if err != nil {
			return nil, errors.New("card_id must be a valid UUID")
		}
		targetCardID = parsed
	}

	params := store.UpdateCardFromAttemptParams{
		WorkspaceID:           r.context.WorkspaceID,
		AttemptCardID:         r.context.CardID,
		AttemptID:             r.context.AttemptID,
		RunnerSourceID:        r.context.RunnerSourceID,
		TargetCardID:          targetCardID,
		Title:                 input.Title,
		Description:           input.Description,
		Pinned:                input.Pinned,
		RequiredConfirmations: input.RequiredConfirmations,
		Now:                   time.Now().UTC(),
	}
	if input.TodoState != nil {
		state := domain.TodoState(strings.TrimSpace(*input.TodoState))
		params.TodoState = &state
	}
	if input.OngoingState != nil {
		state := domain.OngoingState(strings.TrimSpace(*input.OngoingState))
		params.OngoingState = &state
	}
	if input.DiscussionState != nil {
		state := domain.DiscussionState(strings.TrimSpace(*input.DiscussionState))
		params.DiscussionState = &state
	}

	result, err := r.store.UpdateCardFromAgentAttempt(ctx, params)
	if err != nil {
		return nil, err
	}
	if result.Changed && r.live != nil {
		r.live.Publish(targetCardID, realtime.Event{Type: "card.updated"})
	}

	return map[string]any{
		"ok":             true,
		"changed":        result.Changed,
		"status_changed": result.StatusChanged,
		"card":           cardToolView(result.Card),
	}, nil
}
