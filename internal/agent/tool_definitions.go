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
		"frequency":        map[string]any{"type": "string", "enum": []string{"daily", "weekly", "monthly", "interval"}},
		"day_of_week":      map[string]any{"type": "integer", "minimum": 0, "maximum": 6, "description": "Weekly only; Sunday=0."},
		"day_of_month":     map[string]any{"type": "integer", "minimum": 1, "maximum": 28, "description": "Monthly only."},
		"hour":             map[string]any{"type": "integer", "minimum": 0, "maximum": 23, "description": "Hour in plan timezone."},
		"minute":           map[string]any{"type": "integer", "minimum": 0, "maximum": 59, "description": "Minute in plan timezone."},
		"interval_minutes": map[string]any{"type": "integer", "minimum": 1, "maximum": 1440, "description": "Interval in minutes (1-1440) for interval frequency."},
		"timezone":         stringField("IANA timezone such as Asia/Shanghai; not a UTC offset."),
	}, "frequency", "timezone")
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
				Description: "Remove a schedule from an Ongoing Card. Schedules without Turn history are deleted permanently; schedules with execution history are archived (disabled and hidden) while preserving historical Turns.",
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
				Description: "Generate one work Card from the current active Discussion. Classify finite work as todo and long-term, periodic, or continuously monitored work as ongoing. Never call this from Todo or Ongoing cards. By default auto_start is true and immediately queues the initial Turn for the generated Card; when the user explicitly asks to archive/schedule the work in the backlog without starting execution now, pass auto_start=false. Always mention the generated card in your user-visible reply using [Card Title](cycledock://card/<card_id>) so the UI displays an interactive card widget with direct navigation.",
				Parameters: object(map[string]any{
					"kind":                   map[string]any{"type": "string", "enum": []string{"todo", "ongoing"}},
					"title":                  stringField("Concise generated work title."),
					"description":            stringField("Durable scope and expected outcome for the generated work."),
					"required_confirmations": map[string]any{"type": "integer", "minimum": 1, "description": "Todo confirmation threshold; use 1 unless the discussion requires more."},
					"auto_start":             map[string]any{"type": "boolean", "description": "Whether to immediately enqueue the initial execution Turn for the generated Card. Defaults to true; pass false when the user explicitly wants to create/backlog the card without running the Agent yet."},
					"idempotency_key":        stringField("Stable unique key for this logical generated work item."),
				}, "kind", "title", "description", "idempotency_key"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "reply_message",
				Description: "Publish exactly one user-visible message bubble. For text, send type, content, end_turn and idempotency_key; omit widget and url. Only type=choice accepts widget. Set end_turn=true for your last message. Choice messages also complete this Turn; the human answer starts a new Turn. Do not send a second final answer.",
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
					"end_turn":        map[string]any{"type": "boolean", "description": "Finish this Turn, including choice messages."},
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
				Name:        "cycledock_send_card_message",
				Description: "Send a message to another Card in the same Workspace and queue a Turn for its Agent. Optionally set dispatch_mode to interrupt to immediately supersede the target Card's current running or queued Attempt and redirect work. Defaults to enqueue.",
				Parameters: object(map[string]any{
					"card_id":         stringField("Target Card UUID, different from the current Card."),
					"content":         stringField("Relevant context and requested work for the target Agent."),
					"idempotency_key": stringField("Stable key for this message; reuse for retries to avoid duplicate Turns."),
					"dispatch_mode": map[string]any{
						"type":        "string",
						"enum":        []string{"enqueue", "interrupt"},
						"description": "Optional dispatch mode: 'enqueue' to queue normally after current work, or 'interrupt' to immediately cancel running/queued attempts and prioritize this turn. Defaults to 'enqueue'.",
					},
				}, "card_id", "content", "idempotency_key"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_confirm_card",
				Description: "Confirm verified work on a Working Todo. CycleDock moves to Review only after the required confirmation threshold. A human alone can move Review to Done.",
				Parameters: object(map[string]any{
					"content":         stringField("What was verified."),
					"idempotency_key": stringField("Stable key for this logical confirmation. Use a new key for newly verified work after the card re-enters Working."),
				}, "content", "idempotency_key"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_update_card",
				Description: "Update editable information on a Card in the current Workspace (Todo, Ongoing, or non-main Discussion). Omit card_id to update the current Card, or supply a target Card UUID. Supports updating title, description, pinned, required_confirmations (Todo only), and allowed lifecycle state (Todo: todo/working; Ongoing: active/paused/archived; Discussion: active/archived). Moving a Todo to review requires cycledock_confirm_card, and moving to done requires a human. The project main Discussion cannot be modified.",
				Parameters: object(map[string]any{
					"card_id": stringField("Target Card UUID in the current Workspace. Omit to update the current Card."),
					"title":   stringField("Updated non-empty Card title."),
					"description": stringField(
						"Updated Card description (scope, notes, or expected outcome). Pass an empty string to clear.",
					),
					"pinned": map[string]any{
						"type":        "boolean",
						"description": "Pin or unpin the Card in its Project.",
					},
					"todo_state": map[string]any{
						"type":        "string",
						"enum":        []string{"todo", "working"},
						"description": "Allowed only for Todo cards: 'todo' or 'working'. Use cycledock_confirm_card for 'review'; 'done' is human-only.",
					},
					"ongoing_state": map[string]any{
						"type":        "string",
						"enum":        []string{"active", "paused", "archived"},
						"description": "Allowed only for Ongoing cards: 'active', 'paused', or 'archived'.",
					},
					"discussion_state": map[string]any{
						"type":        "string",
						"enum":        []string{"active", "archived"},
						"description": "Allowed only for non-main Discussion cards: 'active' or 'archived'.",
					},
					"required_confirmations": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"description": "Allowed only for Todo cards not yet in review/done: confirmation threshold (>= 1) required to enter review.",
					},
				}),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_list_cards",
				Description: "Read-only query to list cards in the current Project (default) or across the entire Workspace. Exclusively available to Discussion cards (including built-in proactive main chat turns). Useful for reviewing active work, finding abnormal ongoing checks (health=abnormal), or locating card_ids outside recent context. Results are strictly scoped to the current Workspace and sorted by abnormal/pending-approval priority, pinned status, and most recently updated.",
				Parameters: object(map[string]any{
					"scope": map[string]any{
						"type":        "string",
						"enum":        []string{"project", "workspace"},
						"description": "Query scope: 'project' (default, current Project only) or 'workspace' (all Projects in the current Workspace).",
					},
					"kind": map[string]any{
						"type":        "string",
						"enum":        []string{"all", "todo", "ongoing", "discussion"},
						"description": "Filter by card kind: 'all' (default), 'todo', 'ongoing', or 'discussion'.",
					},
					"state": map[string]any{
						"type":        "string",
						"enum":        []string{"todo", "working", "review", "done", "active", "paused", "archived"},
						"description": "Optional lifecycle state filter. Specifying 'done' or 'archived' automatically includes completed/archived cards.",
					},
					"health": map[string]any{
						"type":        "string",
						"enum":        []string{"healthy", "warning", "critical", "acknowledged", "unknown", "abnormal"},
						"description": "Optional Ongoing health filter (applies only to ongoing cards). 'abnormal' matches both 'warning' and 'critical'.",
					},
					"query": stringField("Optional case-insensitive substring match against card titles."),
					"include_done": map[string]any{
						"type":        "boolean",
						"description": "Whether to include done and archived cards. Defaults to false unless state is explicitly 'done' or 'archived'.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     200,
						"description": "Maximum number of cards to return (1-200). Defaults to 50.",
					},
				}),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_inspect_card_session",
				Description: "Inspect the conversation history/session of a non-discussion Card (todo or ongoing) within the current Workspace. Exclusively available to Discussion cards. Supports 'simple' mode (visible user messages and reply bubbles, matching what users see) and 'full' mode (detailed turns including thinking/reasoning and tool calls/results).",
				Parameters: object(map[string]any{
					"card_id": stringField("Target Card UUID. Defaults to the current Card if omitted."),
					"mode": map[string]any{
						"type":        "string",
						"enum":        []string{"simple", "full"},
						"description": "Inspection mode: 'simple' for user-visible conversation bubbles, 'full' for complete turns with thinking and tool executions. Defaults to 'simple'.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     200,
						"description": "Maximum number of bubbles or events to return (1-200). Defaults to 50.",
					},
				}),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_watch_event",
				Description: "Register a one-shot external event watcher on the current Card (for example waiting for a GitHub PR CI completion or commit SHA CI completion via Webhook) so you can end this Turn and be automatically woken up when the matching event arrives. If no matching webhook arrives before timeout_minutes, CycleDock automatically wakes this Card once as a timeout fallback.",
				Parameters: object(map[string]any{
					"match_key": stringField("Normalized match key to watch, e.g. 'github:owner/repo:pr:78:ci_completed', 'github:owner/repo:sha:<commit_sha>:ci_completed', 'github:owner/repo:branch:<branch>:ci_completed', or 'circleci:org/repo:branch:<branch>:ci_completed'."),
					"note":      stringField("Short context note reminding your future self what to verify or continue when woken up."),
					"timeout_minutes": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     10080,
						"description": "Timeout fallback duration in minutes (default 60, max 10080). If no webhook matches before timeout, the Card is woken up once to check status.",
					},
					"dispatch_mode": map[string]any{
						"type":        "string",
						"enum":        []string{"enqueue", "interrupt"},
						"description": "How to dispatch the wakeup Turn when triggered: 'enqueue' (default) or 'interrupt'.",
					},
					"idempotency_key": stringField("Stable unique key for this watcher registration so retries do not create duplicates."),
				}, "match_key", "idempotency_key"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_schedule_wakeup",
				Description: "Schedule a one-shot delayed self-wakeup on the current Card after after_minutes (for example to re-check a deployment, logs, or external status) and end this Turn instead of sleeping or busy-polling.",
				Parameters: object(map[string]any{
					"after_minutes": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     43200,
						"description": "Delay in minutes before waking this Card (1 to 43200).",
					},
					"note": stringField("What to check or continue when this Card wakes up."),
					"dispatch_mode": map[string]any{
						"type":        "string",
						"enum":        []string{"enqueue", "interrupt"},
						"description": "How to dispatch the wakeup Turn: 'enqueue' (default) or 'interrupt'.",
					},
					"idempotency_key": stringField("Stable unique key for this wakeup registration."),
				}, "after_minutes", "note", "idempotency_key"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_list_watchers",
				Description: "List one-shot event watchers and delayed self-wakeups on the current Card.",
				Parameters: object(map[string]any{
					"armed_only": map[string]any{
						"type":        "boolean",
						"description": "When true (default), return only currently armed watchers; when false, also include triggered, expired, and cancelled watchers.",
					},
				}),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "cycledock_cancel_watcher",
				Description: "Cancel an armed event watcher or delayed self-wakeup on the current Card.",
				Parameters: object(map[string]any{
					"watcher_id": stringField("Watcher UUID returned by cycledock_watch_event, cycledock_schedule_wakeup, or cycledock_list_watchers."),
					"reason":     stringField("Optional short cancellation reason."),
				}, "watcher_id"),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "search_mcp_tools",
				Description: "Search available external MCP tools by task description, keyword, or exact tool ID. Returns matching tools with their stable tool_id, connection name, tool name, description, and full input_schema. Call this before call_mcp_tool when you need external third-party tools not already in your context.",
				Parameters: object(map[string]any{
					"query":           stringField("Natural language query describing the desired capability, keyword, or task description."),
					"connection_name": stringField("Optional filter to search tools only from a specific MCP connection."),
					"tool_id":         stringField("Optional exact tool ID (format '<connection_name>:<tool_name>') to retrieve its schema."),
					"limit": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     15,
						"description": "Maximum number of matched tools to return (default 5, max 15).",
					},
				}),
			},
		},
		{
			Type: "function",
			Function: modelclient.ToolDefinitionFunc{
				Name:        "call_mcp_tool",
				Description: "Call an external MCP tool by its stable tool_id (format: '<connection_name>:<tool_name>') with arguments matching its input_schema. If you already have the tool schema from earlier in this Card Session, call this directly without searching again. Parameter errors, disabled connections, or schema mismatches return actionable errors to guide fixing parameters or re-searching.",
				Parameters: object(map[string]any{
					"tool_id": stringField("The stable tool_id returned by search_mcp_tools (format: '<connection_name>:<tool_name>')."),
					"arguments": map[string]any{
						"type":        "object",
						"description": "Arguments to pass to the MCP tool, matching its input_schema.",
					},
				}, "tool_id"),
			},
		},
	}
}
