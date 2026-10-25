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
				Name:        "cycledock_report_progress",
				Description: "Update the current Attempt stage/progress and refresh its lease. Add event_content only when the progress itself is a durable Card fact.",
				Parameters: object(map[string]any{
					"stage":           stringField("Short execution stage."),
					"summary":         stringField("Concise current progress."),
					"event_content":   stringField("Optional durable progress Timeline fact."),
					"idempotency_key": stringField("Required when event_content is set; reuse only for the same logical write."),
				}, "stage", "summary"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_add_finding",
				Description: "Persist a verified finding that future Turns should inherit. Never store raw tool output.",
				Parameters: object(map[string]any{
					"content":         stringField("Distilled durable finding."),
					"idempotency_key": stringField("Stable key for this logical finding."),
				}, "content", "idempotency_key"),
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
				Name:        "cycledock_wait_for_human",
				Description: "Stop the current Attempt in waiting_human when human input is required. Do not continue working after this call.",
				Parameters: object(map[string]any{
					"stage":   stringField("Stage blocked on human input."),
					"summary": stringField("Exactly what input or decision is needed."),
				}, "stage", "summary"),
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
