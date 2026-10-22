package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func TestBuildDiscussionMetadataContextUsesFirstHumanComment(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	run := store.AgentRunContext{
		ProjectName:    "Release",
		TriggerContent: "SECOND_MESSAGE_SHOULD_NOT_WIN",
		Events: []store.AgentContextEvent{
			{
				Sequence: 1, Type: domain.EventTypeComment, ActorName: "Human",
				ActorKind: domain.SourceKindHuman, Content: "FIRST_HUMAN_MESSAGE", CreatedAt: now,
			},
			{
				Sequence: 2, Type: domain.EventTypeComment, ActorName: "Cycle Agent",
				ActorKind: domain.SourceKindCycleAgent, Content: "AGENT_REPLY", CreatedAt: now.Add(time.Second),
			},
			{
				Sequence: 3, Type: domain.EventTypeComment, ActorName: "Human",
				ActorKind: domain.SourceKindHuman, Content: "SECOND_MESSAGE_SHOULD_NOT_WIN", CreatedAt: now.Add(2 * time.Second),
			},
		},
	}

	messages := BuildDiscussionMetadataContext(run)
	if len(messages) != 2 {
		t.Fatalf("messages=%d, want 2", len(messages))
	}
	contextText := messages[1].Content
	if !strings.Contains(contextText, "FIRST_HUMAN_MESSAGE") {
		t.Fatalf("metadata context missing first human message:\n%s", contextText)
	}
	if strings.Contains(contextText, "SECOND_MESSAGE_SHOULD_NOT_WIN") {
		t.Fatalf("metadata context used a later trigger/comment:\n%s", contextText)
	}
}

func TestBuildContextSeparatesHistoricalFactsFromDerivedCompaction(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	run := store.AgentRunContext{
		WorkspaceID: uuid.New(), WorkspaceName: "Workspace",
		ProjectID: uuid.New(), ProjectName: "Project",
		CardID: uuid.New(), CardKind: domain.CardKindTodo,
		CardTitle: "Card", TodoState: string(domain.TodoStateWorking),
		RequiredConfirmations: 1,
		TriggerID:             uuid.New(), TriggerCause: domain.TriggerCauseManualRetry,
		TriggerContent: "retry the failed work", TriggeredAt: now,
		HistoricalFacts: []store.AgentContextEvent{{
			Sequence: 3, Type: domain.EventTypeFinding, ActorName: "Cycle Agent",
			ActorKind: domain.SourceKindCycleAgent, Content: "EARLY_DURABLE_FACT", CreatedAt: now.Add(-time.Hour),
		}},
		HistoryCompaction: store.AgentHistoryCompaction{
			BeforeSequence: 50, OlderCommentCount: 12, OlderProgressCount: 40,
			RecentContextEvents: []store.AgentContextEvent{{
				Sequence: 49, Type: domain.EventTypeComment, ActorName: "Human",
				ActorKind: domain.SourceKindHuman, Content: "RECENT_OLD_COMMENT", CreatedAt: now.Add(-time.Minute),
			}},
		},
		Events: []store.AgentContextEvent{{
			Sequence: 50, Type: domain.EventTypeProgress, ActorName: "Cycle Agent",
			ActorKind: domain.SourceKindCycleAgent, Content: "RECENT_PROGRESS", CreatedAt: now,
		}},
	}
	messages := BuildContext(run, db.WorkspaceAgentSetting{})
	if len(messages) != 2 {
		t.Fatalf("messages=%d", len(messages))
	}
	contextText := messages[1].Content
	for _, required := range []string{
		"EARLY_DURABLE_FACT",
		"Derived history compaction",
		"12 older comments, 40 older progress events",
		"RECENT_OLD_COMMENT",
		"RECENT_PROGRESS",
		"original Timeline remains the audit source",
		"retry the failed work",
	} {
		if !strings.Contains(contextText, required) {
			t.Fatalf("context missing %q:\n%s", required, contextText)
		}
	}
	if strings.Index(contextText, "EARLY_DURABLE_FACT") > strings.Index(contextText, "RECENT_PROGRESS") {
		t.Fatal("historical durable facts should precede the recent bounded Timeline")
	}
}
