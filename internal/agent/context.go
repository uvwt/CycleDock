package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

const baseSystemInstructions = `You are CycleDock's built-in Cycle Agent.

Operate on the current Card and Trigger only. Card state is durable shared work state; your model messages and raw tool outputs exist only inside this Run.

Rules:
- Treat Project instructions, Card details, durable Timeline facts, and the current Trigger as authoritative context.
- Do not assume raw tool calls or outputs from older Runs are available. They are intentionally not persisted.
- Promote durable discoveries with cycledock_add_finding. Use cycledock_report_progress only for meaningful phase changes.
- On Ongoing Cards, use cycledock_check_in when you establish a health fact.
- If human input is required, call cycledock_wait_for_human and stop.
- Use cycledock_confirm_card only after you have actually verified a Normal Card's work. Human Review -> Done is never your action.
- External MCP tool results are ephemeral raw material. Persist only the distilled facts that future Runs need.
- When work is complete, return a concise durable Result as your final assistant response. CycleDock will persist that Result.
- Never output chain-of-thought or hidden reasoning.`

func BuildContext(run store.AgentRunContext, settings db.WorkspaceAgentSetting) []modelclient.Message {
	var system strings.Builder
	system.WriteString(baseSystemInstructions)
	if extra := strings.TrimSpace(settings.SystemInstructions); extra != "" {
		system.WriteString("\n\nWorkspace Agent instructions:\n")
		system.WriteString(extra)
	}

	var context strings.Builder
	fmt.Fprintf(&context, "Workspace: %s\n", run.WorkspaceName)
	fmt.Fprintf(&context, "Project: %s\n", run.ProjectName)
	if value := strings.TrimSpace(run.ProjectDescription); value != "" {
		fmt.Fprintf(&context, "Project description: %s\n", value)
	}
	if value := strings.TrimSpace(run.ProjectInstructions); value != "" {
		fmt.Fprintf(&context, "Project instructions:\n%s\n", value)
	}
	context.WriteString("\nCard:\n")
	fmt.Fprintf(&context, "- ID: %s\n", run.CardID)
	fmt.Fprintf(&context, "- Kind: %s\n", run.CardKind)
	fmt.Fprintf(&context, "- Title: %s\n", run.CardTitle)
	if value := strings.TrimSpace(run.CardDescription); value != "" {
		fmt.Fprintf(&context, "- Description: %s\n", value)
	}
	if run.NormalColumn != "" {
		fmt.Fprintf(&context, "- Column: %s\n", run.NormalColumn)
	}
	if run.OngoingState != "" {
		fmt.Fprintf(&context, "- Ongoing state: %s\n", run.OngoingState)
	}
	fmt.Fprintf(&context, "- Required confirmations: %d\n", run.RequiredConfirmations)
	fmt.Fprintf(&context, "- Pending Trigger count behind this Run: %d\n", run.PendingTriggerCount)

	if len(run.Events) > 0 {
		context.WriteString("\nDurable Timeline (oldest to newest within the bounded context window):\n")
		for _, event := range run.Events {
			content := trimContextText(event.Content, 4000)
			if content == "" && event.CheckinStatus == "" {
				continue
			}
			fmt.Fprintf(
				&context,
				"[%d %s %s/%s %s] %s",
				event.Sequence,
				event.CreatedAt.UTC().Format(time.RFC3339),
				event.ActorKind,
				event.ActorName,
				event.Type,
				content,
			)
			if event.CheckinStatus != "" {
				fmt.Fprintf(&context, " (health=%s)", event.CheckinStatus)
			}
			context.WriteByte('\n')
		}
	}

	context.WriteString("\nCurrent Trigger:\n")
	fmt.Fprintf(&context, "- Cause: %s\n", run.TriggerCause)
	fmt.Fprintf(&context, "- Created: %s\n", run.TriggeredAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&context, "- Intent: %s\n", trimContextText(run.TriggerContent, 8000))
	context.WriteString("\nWork from the latest durable Card state, not from assumptions about earlier Runs.")

	return []modelclient.Message{
		{Role: "system", Content: system.String()},
		{Role: "user", Content: context.String()},
	}
}

func trimContextText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "…"
}
