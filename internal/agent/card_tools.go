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

type listCardsToolInput struct {
	Scope       string `json:"scope"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	Health      string `json:"health"`
	Query       string `json:"query"`
	IncludeDone bool   `json:"include_done"`
	Limit       *int   `json:"limit"`
}

func (r attemptToolRuntime) listCardsTool(ctx adkagent.Context, args map[string]any) (map[string]any, error) {
	// 与 cycledock_inspect_card_session 保持一致的自治隔离：仅 Discussion 会话可盘点工作区/项目卡片。
	if r.context.CardKind != domain.CardKindDiscussion {
		return nil, errors.New("only discussion cards can list workspace/project cards with cycledock_list_cards")
	}
	if r.store == nil {
		return nil, errors.New("store is not configured")
	}

	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var input listCardsToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("invalid list_cards arguments: %w", err)
	}

	limit := 0
	if input.Limit != nil {
		if *input.Limit < 1 {
			return nil, errors.New("limit must be between 1 and 200")
		}
		limit = *input.Limit
	}

	result, err := r.store.ListCardsForAgent(ctx, store.AgentListCardsParams{
		WorkspaceID: r.context.WorkspaceID,
		ProjectID:   r.context.ProjectID,
		Scope:       input.Scope,
		Kind:        input.Kind,
		State:       input.State,
		Health:      input.Health,
		Query:       input.Query,
		IncludeDone: input.IncludeDone,
		Limit:       limit,
	})
	if err != nil {
		return nil, err
	}

	cards := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		updatedAt := ""
		if !item.UpdatedAt.IsZero() {
			updatedAt = item.UpdatedAt.UTC().Format(time.RFC3339)
		}
		cards = append(cards, map[string]any{
			"card_id":                     item.CardID.String(),
			"project_id":                  item.ProjectID.String(),
			"project_name":                item.ProjectName,
			"kind":                        item.Kind,
			"title":                       item.Title,
			"description_summary":         item.DescriptionSummary,
			"state":                       item.State,
			"health":                      item.Health,
			"current_attempt_status":      item.CurrentAttemptStatus,
			"latest_attempt_status":       item.LatestAttemptStatus,
			"pending_tool_approval_count": item.PendingToolApprovalCount,
			"pinned":                      item.Pinned,
			"notifications_muted":         item.NotificationsMuted,
			"is_main_discussion":          item.IsMainDiscussion,
			"updated_at":                  updatedAt,
		})
	}

	return map[string]any{
		"cards":         cards,
		"count":         len(cards),
		"total_matched": result.TotalMatched,
		"truncated":     result.Truncated,
	}, nil
}
