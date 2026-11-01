package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/CycleDock/internal/store"
)

type ReplyMessageInput struct {
	AttemptID      string              `json:"attempt_id" jsonschema:"Current Attempt UUID"`
	Type           string              `json:"type" jsonschema:"text, attachment, or choice"`
	Content        string              `json:"content,omitempty"`
	URL            string              `json:"url,omitempty"`
	Alt            string              `json:"alt,omitempty"`
	Images         []string            `json:"images,omitempty"`
	Widget         *store.ChoiceWidget `json:"widget,omitempty"`
	EndTurn        bool                `json:"end_turn,omitempty" jsonschema:"Optional; finish the Attempt, or wait for human with choice"`
	IdempotencyKey string              `json:"idempotency_key" jsonschema:"Stable unique key for this bubble"`
}

func replyMessageHandler(dataStore *store.Store) mcp.ToolHandlerFor[ReplyMessageInput, EventOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input ReplyMessageInput) (*mcp.CallToolResult, EventOutput, error) {
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
		eventID, reused, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
			WorkspaceID: actor.WorkspaceID, CardID: attempt.CardID,
			AttemptID: attemptID, RunnerSourceID: actor.SourceID,
			Message: store.ReplyMessage{
				Type: input.Type, Content: input.Content, URL: input.URL,
				Alt: input.Alt, Images: input.Images, Widget: input.Widget, EndTurn: input.EndTurn,
			},
			IdempotencyKey: input.IdempotencyKey,
			FinishOnEnd:    true,
		})
		if err != nil {
			return nil, EventOutput{}, stableToolError(err)
		}
		return nil, EventOutput{EventID: eventID.String(), Reused: reused}, nil
	}
}
