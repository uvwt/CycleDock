package mcpserver

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func registerTools(server *mcp.Server, dataStore *store.Store) {
	mcp.AddTool(server, &mcp.Tool{Name: "list_projects", Description: "List Projects in the authenticated CycleDock Workspace. A Project is the board."}, listProjectsHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "list_cards", Description: "List Cards in the authenticated Workspace with current Run status, pending Trigger count, and Ongoing health."}, listCardsHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "get_card", Description: "Get one Card with bounded recent durable Timeline, pending Trigger queue, and current Run. Raw model/tool traces are intentionally absent."}, getCardHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "create_discussion", Description: "Create a Discussion as the entry point for new work. idempotency_key is required for safe retries."}, createDiscussionHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "generate_work", Description: "Generate one Todo or Ongoing Card from an active Discussion. kind is the agent's semantic classification; Todo/Ongoing Cards cannot generate more work."}, generateWorkHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "update_card", Description: "Update a Card title and/or description without changing workflow state."}, updateCardHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "move_card", Description: "Move a Todo between todo and working. Agents cannot move directly to Review or Done; use confirm_card for Review and a human for Done."}, moveCardHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "add_comment", Description: "Append durable context to a Card without creating new work. Use create_followup when a new Trigger should be queued."}, addCommentHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "create_followup", Description: "Explicitly create a new durable work direction and pending Trigger. It never interrupts current work and does not silently Claim."}, createFollowupHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "claim_work", Description: "Atomically Claim and start the oldest pending Trigger for a Card. A retry by the same OAuth Source returns its existing current Run."}, claimWorkHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "heartbeat_run", Description: "Extend the lease of a running Run owned by this OAuth Source. Heartbeat never writes Timeline."}, heartbeatRunHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "report_progress", Description: "Update current stage/progress and refresh the Run lease. event_content should only be set for meaningful durable progress."}, reportProgressHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "add_finding", Description: "Record a durable finding for the current Run. Findings are carried into future Card context; raw tool output is not."}, addFindingHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "check_in", Description: "Record healthy, warning, or critical status for an Ongoing Card through the current Run."}, checkInHandler(dataStore))
	mcp.AddTool(server, &mcp.Tool{Name: "finish_run", Description: "Finish the authenticated Source's current Run as completed or failed, or enter waiting_human. Completed result is durable Card context."}, finishRunHandler(dataStore))
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
		queue, err := dataStore.ListPendingQueue(ctx, actor.WorkspaceID, cardID)
		if err != nil {
			return nil, GetCardOutput{}, stableToolError(err)
		}
		current, err := dataStore.CurrentRun(ctx, actor.WorkspaceID, cardID)
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
			Card:     cardView(card, "", string(health)),
			Timeline: make([]EventView, 0, len(timeline)),
			Queue:    make([]QueueView, 0, len(queue)),
		}
		for _, event := range timeline {
			output.Timeline = append(output.Timeline, recentEventView(event))
		}
		for _, item := range queue {
			output.Queue = append(output.Queue, QueueView{
				ID: item.ID.String(), Sequence: item.Sequence, Cause: string(item.Cause),
				DispatchMode: string(item.DispatchMode), Content: item.Content,
			})
		}
		if current != nil {
			view := runView(*current)
			output.CurrentRun = &view
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

func createFollowupHandler(dataStore *store.Store) mcp.ToolHandlerFor[CreateFollowupInput, FollowupOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input CreateFollowupInput) (*mcp.CallToolResult, FollowupOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, FollowupOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, FollowupOutput{}, err
		}
		result, err := dataStore.CreateAgentFollowup(
			ctx, actor.WorkspaceID, cardID, actor.SourceID, input.Content, input.IdempotencyKey,
		)
		if err != nil {
			return nil, FollowupOutput{}, stableToolError(err)
		}
		return nil, FollowupOutput{
			EventID: result.EventID.String(), TriggerID: result.TriggerID.String(), Reused: result.Reused,
		}, nil
	}
}

func claimWorkHandler(dataStore *store.Store) mcp.ToolHandlerFor[ClaimWorkInput, RunOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input ClaimWorkInput) (*mcp.CallToolResult, RunOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, RunOutput{}, err
		}
		cardID, err := parseUUID(input.CardID, "card_id")
		if err != nil {
			return nil, RunOutput{}, err
		}
		result, err := dataStore.ClaimNextTriggerAndStart(ctx, actor.WorkspaceID, cardID, actor.SourceID, store.DefaultClaimLease)
		if err != nil {
			return nil, RunOutput{}, stableToolError(err)
		}
		run, err := dataStore.GetRun(ctx, actor.WorkspaceID, result.Run.ID)
		if err != nil {
			return nil, RunOutput{}, stableToolError(err)
		}
		return nil, RunOutput{Run: runView(run), Reused: result.Reused}, nil
	}
}

func heartbeatRunHandler(dataStore *store.Store) mcp.ToolHandlerFor[HeartbeatRunInput, RunOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input HeartbeatRunInput) (*mcp.CallToolResult, RunOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, RunOutput{}, err
		}
		runID, err := parseUUID(input.RunID, "run_id")
		if err != nil {
			return nil, RunOutput{}, err
		}
		if err := dataStore.Heartbeat(ctx, actor.WorkspaceID, runID, actor.SourceID, store.DefaultClaimLease); err != nil {
			return nil, RunOutput{}, stableToolError(err)
		}
		run, err := dataStore.GetRun(ctx, actor.WorkspaceID, runID)
		if err != nil {
			return nil, RunOutput{}, stableToolError(err)
		}
		return nil, RunOutput{Run: runView(run)}, nil
	}
}

func reportProgressHandler(dataStore *store.Store) mcp.ToolHandlerFor[ReportProgressInput, ProgressOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input ReportProgressInput) (*mcp.CallToolResult, ProgressOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, ProgressOutput{}, err
		}
		runID, err := parseUUID(input.RunID, "run_id")
		if err != nil {
			return nil, ProgressOutput{}, err
		}
		eventID, reused, err := dataStore.ReportProgress(ctx, store.ProgressParams{
			WorkspaceID: actor.WorkspaceID, RunID: runID, RunnerSourceID: actor.SourceID,
			Stage: input.Stage, Summary: input.Summary, EventContent: input.EventContent,
			IdempotencyKey: input.IdempotencyKey, LeaseDuration: store.DefaultClaimLease,
		})
		if err != nil {
			return nil, ProgressOutput{}, stableToolError(err)
		}
		output := ProgressOutput{Reused: reused}
		if eventID.String() != "00000000-0000-0000-0000-000000000000" {
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
		runID, err := parseUUID(input.RunID, "run_id")
		if err != nil {
			return nil, EventOutput{}, err
		}
		eventID, err := dataStore.AddRunEvent(ctx, store.RunEventParams{
			WorkspaceID: actor.WorkspaceID, RunID: runID, RunnerSourceID: actor.SourceID,
			Type: domain.EventTypeFinding, Content: input.Content, IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return nil, EventOutput{}, stableToolError(err)
		}
		return nil, EventOutput{EventID: eventID.String()}, nil
	}
}

func checkInHandler(dataStore *store.Store) mcp.ToolHandlerFor[CheckInInput, EventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input CheckInInput) (*mcp.CallToolResult, EventOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, EventOutput{}, err
		}
		runID, err := parseUUID(input.RunID, "run_id")
		if err != nil {
			return nil, EventOutput{}, err
		}
		status := domain.CheckinStatus(input.Status)
		if status != domain.CheckinHealthy && status != domain.CheckinWarning && status != domain.CheckinCritical {
			return nil, EventOutput{}, errors.New("INVALID_REQUEST: status must be healthy, warning, or critical")
		}
		eventID, err := dataStore.AddRunEvent(ctx, store.RunEventParams{
			WorkspaceID: actor.WorkspaceID, RunID: runID, RunnerSourceID: actor.SourceID,
			Type: domain.EventTypeCheckin, Content: input.Content, Checkin: &status,
			IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return nil, EventOutput{}, stableToolError(err)
		}
		return nil, EventOutput{EventID: eventID.String()}, nil
	}
}

func finishRunHandler(dataStore *store.Store) mcp.ToolHandlerFor[FinishRunInput, FinishRunOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input FinishRunInput) (*mcp.CallToolResult, FinishRunOutput, error) {
		actor, err := actorFromContext(ctx)
		if err != nil {
			return nil, FinishRunOutput{}, err
		}
		runID, err := parseUUID(input.RunID, "run_id")
		if err != nil {
			return nil, FinishRunOutput{}, err
		}
		switch input.Outcome {
		case "completed":
			if strings.TrimSpace(input.Result) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
				return nil, FinishRunOutput{}, errors.New("INVALID_REQUEST: completed requires result and idempotency_key")
			}
			err = dataStore.FinishRun(ctx, store.FinishRunParams{
				WorkspaceID: actor.WorkspaceID, RunID: runID, RunnerSourceID: actor.SourceID,
				Result: input.Result, IdempotencyKey: input.IdempotencyKey,
			})
		case "failed":
			if strings.TrimSpace(input.Summary) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
				return nil, FinishRunOutput{}, errors.New("INVALID_REQUEST: failed requires summary and idempotency_key")
			}
			err = dataStore.FailRun(ctx, actor.WorkspaceID, runID, actor.SourceID, input.Summary, input.IdempotencyKey)
		case "waiting_human":
			if strings.TrimSpace(input.Stage) == "" || strings.TrimSpace(input.Summary) == "" {
				return nil, FinishRunOutput{}, errors.New("INVALID_REQUEST: waiting_human requires stage and summary")
			}
			err = dataStore.WaitForHuman(ctx, actor.WorkspaceID, runID, actor.SourceID, input.Stage, input.Summary)
		default:
			return nil, FinishRunOutput{}, errors.New("INVALID_REQUEST: outcome must be completed, failed, or waiting_human")
		}
		if err != nil {
			return nil, FinishRunOutput{}, stableToolError(err)
		}
		run, err := dataStore.GetRun(ctx, actor.WorkspaceID, runID)
		if err != nil {
			return nil, FinishRunOutput{}, stableToolError(err)
		}
		return nil, FinishRunOutput{Run: runView(run)}, nil
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
		PendingTriggerCount: card.PendingTriggerCount,
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
	if card.CurrentRunStatus.Valid {
		view.CurrentRunStatus = card.CurrentRunStatus.String
	}
	return view
}

func recentEventView(event db.ListRecentCardTimelineRow) EventView {
	view := EventView{
		ID: event.ID.String(), Sequence: event.Sequence.Int64, Type: event.EventType,
		ActorName: event.ActorName, ActorKind: event.ActorKind, Content: event.Content,
		CreatedAt: event.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
	if event.CheckinStatus.Valid {
		view.CheckinStatus = event.CheckinStatus.String
	}
	if event.RunID.Valid {
		view.RunID = event.RunID.UUID.String()
	}
	return view
}

func runView(run db.AgentRun) RunView {
	return RunView{
		ID: run.ID.String(), CardID: run.CardID.String(), TriggerID: run.TriggerID.String(),
		Status: run.Status, CurrentStage: run.CurrentStage, ProgressSummary: run.ProgressSummary,
		ResultSummary: run.ResultSummary, ErrorSummary: run.ErrorSummary,
	}
}
