package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func registerTools(server *mcp.Server, dataStore *store.Store) {
	mcp.AddTool(server, &mcp.Tool{Name: "list_projects", Description: "List Projects in the authenticated CycleDock Workspace. A Project is the board."}, listProjectsHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "list_cards", Description: "List Cards in the authenticated Workspace with current Attempt status, pending Turn count, and Ongoing health."}, listCardsHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "get_card", Description: "Get one Card with bounded durable Timeline, pending Turns, current Attempt, and recent Attempt history."}, getCardHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "create_discussion", Description: "Create a Discussion as the entry point for new work. idempotency_key is required for safe retries."}, createDiscussionHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "generate_work", Description: "Generate one Todo or Ongoing Card from an active Discussion. kind is the agent's semantic classification; Todo/Ongoing Cards cannot generate more work."}, generateWorkHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "update_card", Description: "Update a Card title and/or description without changing workflow state."}, updateCardHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "move_card", Description: "Move a Todo between todo and working. Agents cannot move directly to Review or Done; use confirm_card for Review and a human for Done."}, moveCardHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "add_comment", Description: "Append durable context to a Card without creating a new Turn. Use create_turn when new work should be queued."}, addCommentHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "create_turn", Description: "Create a durable queued Turn on a Card. It never interrupts the current Attempt and is idempotent by source/key."}, createTurnHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "claim_attempt", Description: "Atomically claim/start the oldest pending Turn for this Card. Repeated claim by the same OAuth Source reuses its current Attempt."}, claimAttemptHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "heartbeat_attempt", Description: "Extend the lease of a running Attempt owned by this OAuth Source. Heartbeat never writes Timeline."}, heartbeatAttemptHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "reply_message", Description: "Publish one separate user-visible message bubble for the authenticated Attempt; may be called multiple times. end_turn finishes the Attempt or, with choice, waits for a human."}, replyMessageHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "check_in", Description: "Record healthy, warning, or critical status for an Ongoing Card through the current Attempt."}, checkInHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "finish_attempt", Description: "Finish the authenticated Source's current Attempt as completed or failed, or enter waiting_human."}, finishAttemptHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "confirm_card", Description: "Confirm a Working Todo after verification. When required confirmations are reached, CycleDock moves it to Review; only a human can move Review to Done."}, confirmCardHandler(dataStore))
}

func listProjectsHandler(dataStore *store.Store) mcp.ToolHandlerFor[EmptyInput, ListProjectsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, ListProjectsOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, ListProjectsOutput{}, err
		}
		projects, err := dataStore.ListProjects(ctx, actor.WorkspaceID)
		if err != nil {
			return nil, ListProjectsOutput{}, stableToolError(err)
		}
		output := ListProjectsOutput{Projects: make([]ProjectView, 0, len(projects))}
		for _, project := range projects {
			output.Projects = append(output.Projects, ProjectView{
				ID: project.ID.String(), Name: project.Name,
				Description: project.Description, Instructions: project.Instructions,
			})
		}
		return nil, output, nil
	}
}

func listCardsHandler(dataStore *store.Store) mcp.ToolHandlerFor[ListCardsInput, ListCardsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ListCardsInput) (*mcp.CallToolResult, ListCardsOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, ListCardsOutput{}, err
		}
		cards, err := dataStore.ListWorkspaceCardSummaries(ctx, actor.WorkspaceID)
		if err != nil {
			return nil, ListCardsOutput{}, stableToolError(err)
		}
		output := ListCardsOutput{Cards: make([]CardView, 0, len(cards))}
		for _, card := range cards {
			output.Cards = append(output.Cards, cardSummaryView(card))
		}
		return nil, output, nil
	}
}

func getCardHandler(dataStore *store.Store) mcp.ToolHandlerFor[GetCardInput, GetCardOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input GetCardInput) (*mcp.CallToolResult, GetCardOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, GetCardOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, GetCardOutput{}, err
		}
		card, err := dataStore.GetCard(ctx, actor.WorkspaceID, cardID)
		if err != nil {
			return nil, GetCardOutput{}, stableToolError(err)
		}
		timeline, err := dataStore.ListRecentCardTimeline(ctx, actor.WorkspaceID, cardID, input.EventLimit)
		if err != nil {
			return nil, GetCardOutput{}, stableToolError(err)
		}
		pendingTurns, err := dataStore.ListPendingTurns(ctx, actor.WorkspaceID, cardID)
		if err != nil {
			return nil, GetCardOutput{}, stableToolError(err)
		}
		currentAttempt, err := dataStore.CurrentAttemptView(ctx, actor.WorkspaceID, cardID)
		if err != nil {
			return nil, GetCardOutput{}, stableToolError(err)
		}
		attemptHistory, err := dataStore.ListRecentAttempts(ctx, actor.WorkspaceID, cardID, 50)
		if err != nil {
			return nil, GetCardOutput{}, stableToolError(err)
		}
		health := domain.CheckinUnknown
		if card.Kind == string(domain.CardKindOngoing) {
			health, err = dataStore.Health(ctx, actor.WorkspaceID, cardID)
			if err != nil {
				return nil, GetCardOutput{}, stableToolError(err)
			}
		}
		output := GetCardOutput{
			Card:           cardView(card, "", string(health)),
			Timeline:       make([]EventView, 0, len(timeline)),
			PendingTurns:   make([]PendingTurnView, 0, len(pendingTurns)),
			AttemptHistory: make([]AttemptView, 0, len(attemptHistory)),
		}
		for _, event := range timeline {
			output.Timeline = append(output.Timeline, recentEventView(event))
		}
		for _, item := range pendingTurns {
			output.PendingTurns = append(output.PendingTurns, pendingTurnView(item))
		}
		for _, item := range attemptHistory {
			output.AttemptHistory = append(output.AttemptHistory, attemptView(item, cardID))
		}
		if currentAttempt != nil {
			view := attemptView(*currentAttempt, cardID)
			output.CurrentAttempt = &view
		}
		return nil, output, nil
	}
}

func createDiscussionHandler(dataStore *store.Store) mcp.ToolHandlerFor[CreateDiscussionInput, CardOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input CreateDiscussionInput) (*mcp.CallToolResult, CardOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, CardOutput{}, err
		}
		projectID, err := parseUUID(input.ProjectID, "project_id")
		if err != nil {
			return nil, CardOutput{}, err
		}
		result, err := dataStore.CreateAgentDiscussion(ctx, store.AgentDiscussionCreateParams{
			WorkspaceID: actor.WorkspaceID, ProjectID: projectID, SourceID: actor.SourceID,
			IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return nil, CardOutput{}, stableToolError(err)
		}
		return nil, CardOutput{Card: cardView(result.Card, "", ""), Reused: result.Reused}, nil
	}
}

func generateWorkHandler(dataStore *store.Store) mcp.ToolHandlerFor[GenerateWorkInput, CardOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input GenerateWorkInput) (*mcp.CallToolResult, CardOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, CardOutput{}, err
		}
		discussionID, err := parseUUID(input.DiscussionID, "discussion_id")
		if err != nil {
			return nil, CardOutput{}, err
		}
		result, err := dataStore.GenerateWorkFromDiscussion(ctx, store.GenerateWorkParams{
			WorkspaceID: actor.WorkspaceID, DiscussionID: discussionID, SourceID: actor.SourceID,
			Kind: domain.CardKind(input.Kind), Title: input.Title, Description: input.Description,
			RequiredConfirmations: input.RequiredConfirmations, IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return nil, CardOutput{}, stableToolError(err)
		}
		health := ""
		if result.Card.Kind == string(domain.CardKindOngoing) {
			health = string(domain.CheckinUnknown)
		}
		return nil, CardOutput{Card: cardView(result.Card, "", health), Reused: result.Reused}, nil
	}
}

func updateCardHandler(dataStore *store.Store) mcp.ToolHandlerFor[UpdateCardInput, CardOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input UpdateCardInput) (*mcp.CallToolResult, CardOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, CardOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, CardOutput{}, err
		}
		current, err := dataStore.GetCard(ctx, actor.WorkspaceID, cardID)
		if err != nil {
			return nil, CardOutput{}, stableToolError(err)
		}
		title, description := current.Title, current.Description
		if input.Title != nil {
			title = strings.TrimSpace(*input.Title)
		}
		if input.Description != nil {
			description = strings.TrimSpace(*input.Description)
		}
		if title == "" {
			return nil, CardOutput{}, errors.New("INVALID_REQUEST: title cannot be empty")
		}
		card, err := dataStore.UpdateCardText(ctx, actor.WorkspaceID, cardID, title, description)
		if err != nil {
			return nil, CardOutput{}, stableToolError(err)
		}
		return nil, CardOutput{Card: cardView(card, "", "")}, nil
	}
}

func moveCardHandler(dataStore *store.Store) mcp.ToolHandlerFor[MoveCardInput, CardOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input MoveCardInput) (*mcp.CallToolResult, CardOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, CardOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, CardOutput{}, err
		}
		state := domain.TodoState(input.State)
		switch state {
		case domain.TodoStateTodo, domain.TodoStateWorking:
		default:
			return nil, CardOutput{}, errors.New("INVALID_REQUEST: agents may move Todo cards only to todo or working; use confirm_card for Review")
		}
		if err := dataStore.MoveTodo(ctx, actor.WorkspaceID, cardID, actor.SourceID, state); err != nil {
			return nil, CardOutput{}, stableToolError(err)
		}
		card, err := dataStore.GetCard(ctx, actor.WorkspaceID, cardID)
		if err != nil {
			return nil, CardOutput{}, stableToolError(err)
		}
		return nil, CardOutput{Card: cardView(card, "", "")}, nil
	}
}

func addCommentHandler(dataStore *store.Store) mcp.ToolHandlerFor[AddCommentInput, EventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input AddCommentInput) (*mcp.CallToolResult, EventOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, EventOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, EventOutput{}, err
		}
		eventID, reused, err := dataStore.AddAgentComment(
			ctx, actor.WorkspaceID, cardID, actor.SourceID, input.Content, input.IdempotencyKey,
		)
		if err != nil {
			return nil, EventOutput{}, stableToolError(err)
		}
		return nil, EventOutput{EventID: eventID.String(), Reused: reused}, nil
	}
}

func createTurnHandler(dataStore *store.Store) mcp.ToolHandlerFor[CreateTurnInput, TurnOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input CreateTurnInput) (*mcp.CallToolResult, TurnOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, TurnOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, TurnOutput{}, err
		}
		result, err := dataStore.SubmitAgentTurn(
			ctx, actor.WorkspaceID, cardID, actor.SourceID, input.Content, input.IdempotencyKey,
		)
		if err != nil {
			return nil, TurnOutput{}, stableToolError(err)
		}
		return nil, TurnOutput{
			EventID: result.EventID.String(), TurnID: result.TurnID.String(), Reused: result.Reused,
		}, nil
	}
}

func claimAttemptHandler(dataStore *store.Store) mcp.ToolHandlerFor[ClaimAttemptInput, AttemptOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input ClaimAttemptInput) (*mcp.CallToolResult, AttemptOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, AttemptOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, AttemptOutput{}, err
		}
		result, err := dataStore.ClaimCardTurnAttempt(
			ctx, actor.WorkspaceID, cardID, actor.SourceID, time.Now().UTC(), store.DefaultClaimLease,
		)
		if err != nil {
			return nil, AttemptOutput{}, stableToolError(err)
		}
		view, err := attemptViewFromClaim(dataStore, ctx, actor.WorkspaceID, result)
		if err != nil {
			return nil, AttemptOutput{}, stableToolError(err)
		}
		return nil, AttemptOutput{Attempt: view, Reused: result.Reused}, nil
	}
}

func heartbeatAttemptHandler(dataStore *store.Store) mcp.ToolHandlerFor[HeartbeatAttemptInput, AttemptOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input HeartbeatAttemptInput) (*mcp.CallToolResult, AttemptOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, AttemptOutput{}, err
		}
		attemptID, err := parseUUID(input.AttemptID, "attempt_id")
		if err != nil {
			return nil, AttemptOutput{}, err
		}
		attempt, err := dataStore.GetAttempt(ctx, actor.WorkspaceID, attemptID)
		if err != nil {
			return nil, AttemptOutput{}, stableToolError(err)
		}
		if err := dataStore.HeartbeatAttemptOwned(
			ctx, actor.WorkspaceID, attempt.CardID, attemptID, actor.SourceID,
			time.Now().UTC(), store.DefaultClaimLease,
		); err != nil {
			return nil, AttemptOutput{}, stableToolError(err)
		}
		view, err := dataStore.GetAttemptView(ctx, actor.WorkspaceID, attemptID)
		if err != nil {
			return nil, AttemptOutput{}, stableToolError(err)
		}
		return nil, AttemptOutput{Attempt: attemptView(view, attempt.CardID)}, nil
	}
}

func reportProgressHandler(dataStore *store.Store) mcp.ToolHandlerFor[ReportProgressInput, ProgressOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input ReportProgressInput) (*mcp.CallToolResult, ProgressOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, ProgressOutput{}, err
		}
		attemptID, err := parseUUID(input.AttemptID, "attempt_id")
		if err != nil {
			return nil, ProgressOutput{}, err
		}
		attempt, err := dataStore.GetAttempt(ctx, actor.WorkspaceID, attemptID)
		if err != nil {
			return nil, ProgressOutput{}, stableToolError(err)
		}
		eventID, reused, err := dataStore.ReportAttemptProgress(ctx, store.AttemptProgressParams{
			WorkspaceID: actor.WorkspaceID, CardID: attempt.CardID,
			AttemptID: attemptID, RunnerSourceID: actor.SourceID,
			Stage: input.Stage, Summary: input.Summary, EventContent: input.EventContent,
			IdempotencyKey: input.IdempotencyKey, LeaseDuration: store.DefaultClaimLease,
		})
		if err != nil {
			return nil, ProgressOutput{}, stableToolError(err)
		}
		output := ProgressOutput{Reused: reused}
		if eventID != uuid.Nil {
			output.EventID = eventID.String()
		}
		return nil, output, nil
	}
}

func addFindingHandler(dataStore *store.Store) mcp.ToolHandlerFor[AddFindingInput, EventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input AddFindingInput) (*mcp.CallToolResult, EventOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, EventOutput{}, err
		}
		attemptID, err := parseUUID(input.AttemptID, "attempt_id")
		if err != nil {
			return nil, EventOutput{}, err
		}
		attempt, err := dataStore.GetAttempt(ctx, actor.WorkspaceID, attemptID)
		if err != nil {
			return nil, EventOutput{}, stableToolError(err)
		}
		eventID, reused, err := dataStore.AddAttemptEvent(ctx, store.AttemptEventParams{
			WorkspaceID: actor.WorkspaceID, CardID: attempt.CardID,
			AttemptID: attemptID, RunnerSourceID: actor.SourceID,
			Type: domain.EventTypeFinding, Content: input.Content,
			IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return nil, EventOutput{}, stableToolError(err)
		}
		return nil, EventOutput{EventID: eventID.String(), Reused: reused}, nil
	}
}

func checkInHandler(dataStore *store.Store) mcp.ToolHandlerFor[CheckInInput, EventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input CheckInInput) (*mcp.CallToolResult, EventOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, EventOutput{}, err
		}
		attemptID, err := parseUUID(input.AttemptID, "attempt_id")
		if err != nil {
			return nil, EventOutput{}, err
		}
		attempt, err := dataStore.GetAttempt(ctx, actor.WorkspaceID, attemptID)
		if err != nil {
			return nil, EventOutput{}, stableToolError(err)
		}
		status := domain.CheckinStatus(input.Status)
		if status != domain.CheckinHealthy && status != domain.CheckinWarning && status != domain.CheckinCritical {
			return nil, EventOutput{}, errors.New("INVALID_REQUEST: status must be healthy, warning, or critical")
		}
		eventID, reused, err := dataStore.AddAttemptEvent(ctx, store.AttemptEventParams{
			WorkspaceID: actor.WorkspaceID, CardID: attempt.CardID,
			AttemptID: attemptID, RunnerSourceID: actor.SourceID,
			Type: domain.EventTypeCheckin, Content: input.Content, Checkin: &status,
			IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return nil, EventOutput{}, stableToolError(err)
		}
		return nil, EventOutput{EventID: eventID.String(), Reused: reused}, nil
	}
}

func finishAttemptHandler(dataStore *store.Store) mcp.ToolHandlerFor[FinishAttemptInput, FinishAttemptOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input FinishAttemptInput) (*mcp.CallToolResult, FinishAttemptOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, FinishAttemptOutput{}, err
		}
		attemptID, err := parseUUID(input.AttemptID, "attempt_id")
		if err != nil {
			return nil, FinishAttemptOutput{}, err
		}
		attempt, err := dataStore.GetAttempt(ctx, actor.WorkspaceID, attemptID)
		if err != nil {
			return nil, FinishAttemptOutput{}, stableToolError(err)
		}
		now := time.Now().UTC()
		switch input.Outcome {
		case "completed":
			err = dataStore.CompleteAttempt(
				ctx, actor.WorkspaceID, attempt.CardID, attemptID, actor.SourceID,
				now,
			)
		case "failed":
			if strings.TrimSpace(input.Summary) == "" {
				return nil, FinishAttemptOutput{}, errors.New("INVALID_REQUEST: failed requires summary")
			}
			err = dataStore.FailAttemptOwned(
				ctx, actor.WorkspaceID, attempt.CardID, attemptID, actor.SourceID,
				input.Summary, now,
			)
		case "waiting_human":
			if strings.TrimSpace(input.Stage) == "" || strings.TrimSpace(input.Summary) == "" {
				return nil, FinishAttemptOutput{}, errors.New("INVALID_REQUEST: waiting_human requires stage and summary")
			}
			err = dataStore.WaitAttemptForHumanWithPrompt(
				ctx, actor.WorkspaceID, attempt.CardID, attemptID, actor.SourceID,
				input.Stage, input.Summary, now,
			)
		default:
			return nil, FinishAttemptOutput{}, errors.New("INVALID_REQUEST: outcome must be completed, failed, or waiting_human")
		}
		if err != nil {
			return nil, FinishAttemptOutput{}, stableToolError(err)
		}
		view, err := dataStore.GetAttemptView(ctx, actor.WorkspaceID, attemptID)
		if err != nil {
			return nil, FinishAttemptOutput{}, stableToolError(err)
		}
		return nil, FinishAttemptOutput{Attempt: attemptView(view, attempt.CardID)}, nil
	}
}

func confirmCardHandler(dataStore *store.Store) mcp.ToolHandlerFor[ConfirmCardInput, ConfirmCardOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input ConfirmCardInput) (*mcp.CallToolResult, ConfirmCardOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, ConfirmCardOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, ConfirmCardOutput{}, err
		}
		result, err := dataStore.ConfirmCard(
			ctx, actor.WorkspaceID, cardID, actor.SourceID, input.Content, input.IdempotencyKey,
		)
		if err != nil {
			return nil, ConfirmCardOutput{}, stableToolError(err)
		}
		return nil, ConfirmCardOutput{
			EventID: result.EventID.String(), ConfirmationCount: result.ConfirmationCount,
			MovedToReview: result.MovedToReview, Reused: result.Reused,
		}, nil
	}
}

func cardView(card db.Card, projectName, health string) CardView {
	view := CardView{
		ID: card.ID.String(), ProjectID: card.ProjectID.String(), ProjectName: projectName,
		Kind: card.Kind, Title: card.Title, Description: card.Description,
		RequiredConfirmations: card.RequiredConfirmations, Health: health,
	}
	if card.DiscussionState.Valid {
		view.DiscussionState = card.DiscussionState.String
	}
	if card.TodoState.Valid {
		view.TodoState = card.TodoState.String
	}
	if card.OngoingState.Valid {
		view.OngoingState = card.OngoingState.String
	}
	if card.SourceDiscussionID.Valid {
		view.SourceDiscussionID = card.SourceDiscussionID.UUID.String()
	}
	return view
}

func cardSummaryView(card db.ListWorkspaceCardSummariesRow) CardView {
	view := CardView{
		ID: card.ID.String(), ProjectID: card.ProjectID.String(), ProjectName: card.ProjectName,
		Kind: card.Kind, Title: card.Title, Description: card.Description,
		RequiredConfirmations: card.RequiredConfirmations, Health: card.Health,
		PendingTurnCount: card.PendingTurnCount,
	}
	if card.DiscussionState.Valid {
		view.DiscussionState = card.DiscussionState.String
	}
	if card.TodoState.Valid {
		view.TodoState = card.TodoState.String
	}
	if card.OngoingState.Valid {
		view.OngoingState = card.OngoingState.String
	}
	if card.SourceDiscussionID.Valid {
		view.SourceDiscussionID = card.SourceDiscussionID.UUID.String()
	}
	if card.CurrentAttemptStatus.Valid {
		view.CurrentAttemptStatus = card.CurrentAttemptStatus.String
	}
	return view
}

func recentEventView(event db.ListRecentCardTimelineRow) EventView {
	view := EventView{
		ID: event.ID.String(), Sequence: event.Sequence, Type: event.EventType,
		ActorName: event.ActorName, ActorKind: event.ActorKind, Content: event.Content,
		CreatedAt: event.CreatedAt.Time.UTC().Format(time.RFC3339Nano),
	}
	if event.CheckinStatus.Valid {
		view.CheckinStatus = event.CheckinStatus.String
	}
	if event.EventType == "message" && len(event.Message) > 0 {
		if err := json.Unmarshal(event.Message, &view.Message); err != nil {
			view.Message = nil
		}
	}
	if event.AttemptID.Valid {
		view.AttemptID = event.AttemptID.UUID.String()
	}
	return view
}

func pendingTurnView(item store.PendingTurnView) PendingTurnView {
	view := PendingTurnView{
		DeviceNodeID: item.DeviceNodeID, DeviceName: item.DeviceName,
		ID: item.ID.String(), Sequence: item.Sequence, Source: string(item.Source),
		DispatchMode: string(item.DispatchMode), Content: item.Content,
		CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if item.SourceEventID.Valid {
		view.SourceEventID = item.SourceEventID.UUID.String()
	}
	return view
}

func attemptView(item store.SessionAttemptView, cardID uuid.UUID) AttemptView {
	view := AttemptView{
		DeviceNodeID: item.DeviceNodeID, DeviceName: item.DeviceName,
		ID: item.ID.String(), CardID: cardID.String(), TurnID: item.TurnID.String(),
		AttemptNumber: item.AttemptNumber, Reason: string(item.Reason), Status: string(item.Status),
		CurrentStage: item.CurrentStage, ProgressSummary: item.ProgressSummary,
		ErrorSummary: item.ErrorSummary,
	}
	if item.SourceEventID.Valid {
		view.SourceEventID = item.SourceEventID.UUID.String()
	}
	if item.RetryOfAttemptID.Valid {
		view.RetryOfAttemptID = item.RetryOfAttemptID.UUID.String()
	}
	if item.StartedAt != nil {
		view.StartedAt = item.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if item.FinishedAt != nil {
		view.FinishedAt = item.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	return view
}

func attemptViewFromClaim(
	dataStore *store.Store,
	ctx context.Context,
	workspaceID uuid.UUID,
	claim store.CardAttemptClaimResult,
) (AttemptView, error) {
	item, err := dataStore.GetAttemptView(ctx, workspaceID, claim.Attempt.ID)
	if err != nil {
		return AttemptView{}, err
	}
	return attemptView(item, claim.Attempt.CardID), nil
}
