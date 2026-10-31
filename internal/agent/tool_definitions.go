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
	scheduleRule := object(map[string]any{
		"frequency":    map[string]any{"type": "string", "enum": []string{"daily", "weekly", "monthly"}},
		"day_of_week":  map[string]any{"type": "integer", "minimum": 0, "maximum": 6, "description": "Weekly only; Sunday=0."},
		"day_of_month": map[string]any{"type": "integer", "minimum": 1, "maximum": 28, "description": "Monthly only."},
		"hour":         map[string]any{"type": "integer", "minimum": 0, "maximum": 23, "description": "Hour in plan timezone."},
		"minute":       map[string]any{"type": "integer", "minimum": 0, "maximum": 59, "description": "Minute in plan timezone."},
		"timezone":     stringField("IANA timezone such as Asia/Shanghai; not a UTC offset."),
	}, "frequency", "hour", "minute", "timezone")
	return []modelclient.ToolDefinition{
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_list_schedules",
				Description: "List recurring schedules and next execution times on an Ongoing Card. Inspect before creating or changing a schedule.",
				Parameters:  object(map[string]any{"card_id": stringField("Ongoing Card UUID, usually the current Card.")}, "card_id"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_create_schedule",
				Description: "Create a recurring schedule on an Ongoing Card (including a Card newly generated from Discussion). List existing schedules first to avoid duplicates. Creation is not retry-idempotent.",
				Parameters: object(map[string]any{
					"card_id": stringField("Ongoing Card UUID."),
					"rule":    scheduleRule,
				}, "card_id", "rule"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_update_schedule",
				Description: "Update an Ongoing Card schedule. Supply the entire rule from cycledock_list_schedules, keeping unchanged fields; enabled=false pauses without destroying execution history.",
				Parameters: object(map[string]any{
					"card_id":     stringField("Ongoing Card UUID."),
					"schedule_id": stringField("Schedule UUID from cycledock_list_schedules."),
					"rule":        scheduleRule,
					"enabled":     map[string]any{"type": "boolean", "description": "Whether the schedule is enabled."},
				}, "card_id", "schedule_id", "rule", "enabled"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_delete_schedule",
				Description: "Delete a schedule only before it produces any Turn; if there is history, pause with cycledock_update_schedule instead.",
				Parameters: object(map[string]any{
					"card_id":     stringField("Ongoing Card UUID."),
					"schedule_id": stringField("Schedule UUID."),
				}, "card_id", "schedule_id"),
			},
		},
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
				Description: "Publish exactly one user-visible message bubble. For text, send type, content, end_turn and idempotency_key; omit widget and url. Only type=choice accepts widget. Set end_turn=true for your last message. Use choice and end_turn=true to wait for the human; do not send a second final answer.",
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
