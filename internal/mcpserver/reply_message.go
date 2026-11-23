package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/CycleDock/internal/store"
)

type ReplyMessageInput struct {
	AttemptID      string              `json:"attempt_id" jsonschema:"Current Attempt UUID"`
	Type           string              `json:"type" jsonschema:"text, attachment, or choice"`
	Content        string              `json:"content,omitempty" jsonschema:"Markdown content. Required for type=text unless images is provided (omit or leave empty for an image-only bubble). Do not embed images via Markdown ![alt](url); use the images field instead."`
	URL            string              `json:"url,omitempty" jsonschema:"HTTPS file download URL for type=attachment only. Do not use attachment for images; use the images field instead."`
	Alt            string              `json:"alt,omitempty" jsonschema:"Attachment or image description"`
	Images         []string            `json:"images,omitempty" jsonschema:"Direct HTTPS image URLs (up to 8). Always use this field to send images (alone with type=text for an image-only bubble, or with short content) instead of Markdown ![alt](url) or type=attachment."`
	Widget         *store.ChoiceWidget `json:"widget,omitempty"`
	EndTurn        bool                `json:"end_turn,omitempty" jsonschema:"Optional; finish the Attempt, including choice messages"`
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
