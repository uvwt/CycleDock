package agent

import "github.com/uvwt/CycleDock/internal/modelclient"

func internalToolDefinitions() []modelclient.ToolDefinition {
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{
			"type": "object", "properties": properties, "additionalProperties": false,
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	stringField := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return []modelclient.ToolDefinition{
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_generate_work",
				Description: "Generate one work Card from the current active Discussion. Classify finite work as todo and long-term, periodic, or continuously monitored work as ongoing. Never call this from Todo or Ongoing cards.",
				Parameters: object(map[string]any{
					"kind":                   map[string]any{"type": "string", "enum": []string{"todo", "ongoing"}},
					"title":                  stringField("Concise generated work title."),
					"description":            stringField("Durable scope and expected outcome for the generated work."),
					"required_confirmations": map[string]any{"type": "integer", "minimum": 1, "description": "Todo confirmation threshold; use 1 unless the discussion requires more."},
					"idempotency_key":        stringField("Stable unique key for this logical generated work item."),
				}, "kind", "title", "description", "idempotency_key"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "reply_message",
				Description: "Publish exactly one user-visible message bubble. Call multiple times to send multiple bubbles. Set end_turn=true for your last message. Use choice and end_turn=true to wait for the human; do not send a second final answer.",
				Parameters: object(map[string]any{
					"type":    map[string]any{"type": "string", "enum": []string{"text", "attachment", "choice"}},
					"content": stringField("Markdown content. Required for text."),
					"url":     stringField("HTTPS attachment URL, for attachment."),
					"alt":     stringField("Attachment description."),
					"images":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 8},
					"widget": map[string]any{"type": "object", "properties": map[string]any{
						"question": stringField("Question shown to the human."),
						"options": map[string]any{"type": "array", "items": object(map[string]any{
							"value": stringField("Value submitted as the human reply."),
							"label": stringField("Visible option label."),
						}, "value", "label"), "minItems": 2, "maxItems": 8},
					}, "required": []string{"question", "options"}, "additionalProperties": false},
					"end_turn":        map[string]any{"type": "boolean", "description": "Finish this Turn; choice waits for human."},
					"idempotency_key": stringField("Unique stable key for this bubble."),
				}, "type", "idempotency_key"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_check_in",
				Description: "Persist the latest health fact for an Ongoing Card.",
				Parameters: object(map[string]any{
					"status":          map[string]any{"type": "string", "enum": []string{"healthy", "warning", "critical"}},
					"content":         stringField("Evidence-based health summary."),
					"idempotency_key": stringField("Stable key for this logical check-in."),
				}, "status", "content", "idempotency_key"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_confirm_card",
				Description: "Confirm verified work on a Working Todo. CycleDock moves to Review only after the required confirmation threshold. A human alone can move Review to Done.",
				Parameters: object(map[string]any{
					"content":         stringField("What was verified."),
					"idempotency_key": stringField("Stable key for this logical confirmation."),
				}, "content", "idempotency_key"),
			},
		},
	}
}

func discussionMetadataToolDefinition() modelclient.ToolDefinition {
	return modelclient.ToolDefinition{
		Type: "function",
		Function: modelclient.ToolDefinitionFunc{
			Name:        "cycledock_set_discussion_metadata",
			Description: "Set the generated title and description for a new Discussion whose title is still empty.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Concise discussion title derived from the first conversation.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Short durable summary of what the discussion is about.",
					},
				},
				"required":             []string{"title", "description"},
				"additionalProperties": false,
			},
		},
	}
}
