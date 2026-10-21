package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestBuiltInAgentConcurrentClaimAndContextUsesLatestCardState(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	settings, err := s.SaveAgentSettings(ctx, f.WorkspaceID, AgentSettingsInput{
		Enabled: true, ProviderBaseURL: "http://127.0.0.1:18090/v1", Model: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstTrigger, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "inspect the original request", IdempotencyKey: "builtin-trigger-1",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstTrigger.RunID != nil {
		t.Fatal("pending trigger unexpectedly created a Run before Dispatcher claim")
	}

	start := make(chan struct{})
	type claimResult struct {
		claim *BuiltInRunClaim
		err   error
	}
	results := make(chan claimResult, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claim, err := s.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
			results <- claimResult{claim: claim, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var claimed *BuiltInRunClaim
	var successCount int
	for result := range results {
		if result.err == nil {
			claimed = result.claim
			successCount++
			continue
		}
		if !errors.Is(result.err, domain.ErrNotFound) {
			t.Fatalf("unexpected concurrent claim error: %v", result.err)
		}
	}
	if successCount != 1 || claimed == nil {
		t.Fatalf("success count=%d claim=%+v", successCount, claimed)
	}
	if claimed.SourceID != settings.SourceID || claimed.TriggerID != firstTrigger.TriggerID {
		t.Fatalf("claim=%+v settings=%+v trigger=%+v", claimed, settings, firstTrigger)
	}

	if _, err := s.AddRunEvent(ctx, RunEventParams{
		WorkspaceID: f.WorkspaceID, RunID: claimed.RunID, RunnerSourceID: settings.SourceID,
		Type: domain.EventTypeFinding, Content: "durable finding from run one",
		IdempotencyKey: "builtin-finding-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, FinishRunParams{
		WorkspaceID: f.WorkspaceID, RunID: claimed.RunID, RunnerSourceID: settings.SourceID,
		Result: "durable result from run one", IdempotencyKey: "builtin-finish-1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateCardText(ctx, f.WorkspaceID, f.CardID, "Normal", "description updated after run one"); err != nil {
		t.Fatal(err)
	}

	secondTrigger, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "use the latest card state", IdempotencyKey: "builtin-trigger-2",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.TriggerID != secondTrigger.TriggerID {
		t.Fatalf("claimed trigger=%s want=%s", second.TriggerID, secondTrigger.TriggerID)
	}
	contextSnapshot, err := s.LoadAgentRunContext(
		ctx, f.WorkspaceID, second.RunID, settings.SourceID, 50,
	)
	if err != nil {
		t.Fatal(err)
	}
	if contextSnapshot.CardDescription != "description updated after run one" ||
		contextSnapshot.TriggerContent != "use the latest card state" {
		t.Fatalf("context did not use latest state: %+v", contextSnapshot)
	}
	var sawFinding, sawResult bool
	for _, event := range contextSnapshot.Events {
		if strings.Contains(event.Content, "durable finding from run one") {
			sawFinding = true
		}
		if strings.Contains(event.Content, "durable result from run one") {
			sawResult = true
		}
	}
	if !sawFinding || !sawResult {
		t.Fatalf("durable prior knowledge missing: finding=%v result=%v events=%+v", sawFinding, sawResult, contextSnapshot.Events)
	}
}

func TestAgentContextCompactionRetainsHistoricalDurableFacts(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	settings, err := s.SaveAgentSettings(ctx, f.WorkspaceID, AgentSettingsInput{
		Enabled: true, ProviderBaseURL: "http://127.0.0.1:18090/v1", Model: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "original old comment", IdempotencyKey: "compaction-root",
		DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRunEvent(ctx, RunEventParams{
		WorkspaceID: f.WorkspaceID, RunID: claim.RunID, RunnerSourceID: settings.SourceID,
		Type: domain.EventTypeFinding, Content: "EARLY_DURABLE_FINDING",
		IdempotencyKey: "compaction-finding",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "EARLY_COMPACTED_COMMENT", IdempotencyKey: "compaction-comment",
		DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		if _, err := s.AddRunEvent(ctx, RunEventParams{
			WorkspaceID: f.WorkspaceID, RunID: claim.RunID, RunnerSourceID: settings.SourceID,
			Type:           domain.EventTypeProgress,
			Content:        "progress " + fmt.Sprint(i),
			IdempotencyKey: "compaction-progress-" + fmt.Sprint(i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := s.LoadAgentRunContext(
		ctx, f.WorkspaceID, claim.RunID, settings.SourceID, 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) != 10 {
		t.Fatalf("recent event window=%d, want 10", len(snapshot.Events))
	}
	var foundEarlyFact bool
	for _, event := range snapshot.HistoricalFacts {
		if event.Type == domain.EventTypeFinding && event.Content == "EARLY_DURABLE_FINDING" {
			foundEarlyFact = true
			break
		}
	}
	if !foundEarlyFact {
		t.Fatalf("historical finding was lost: facts=%+v", snapshot.HistoricalFacts)
	}
	if snapshot.HistoryCompaction.BeforeSequence == 0 ||
		snapshot.HistoryCompaction.OlderCommentCount < 1 ||
		snapshot.HistoryCompaction.OlderProgressCount < 1 {
		t.Fatalf("history compaction metadata=%+v", snapshot.HistoryCompaction)
	}
	if len(snapshot.HistoryCompaction.RecentContextEvents) == 0 ||
		len(snapshot.HistoryCompaction.RecentContextEvents) > 8 {
		t.Fatalf("compacted excerpts=%d", len(snapshot.HistoryCompaction.RecentContextEvents))
	}
	for _, event := range snapshot.Events {
		if event.Content == "EARLY_DURABLE_FINDING" {
			t.Fatal("early finding should be outside the bounded recent window")
		}
	}
}

func TestBuiltInAgentWaitingHumanBlocksUntilReplyAndLateRunCannotFinish(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	settings, err := s.SaveAgentSettings(ctx, f.WorkspaceID, AgentSettingsInput{
		Enabled: true, ProviderBaseURL: "http://127.0.0.1:18090/v1", Model: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "needs clarification", IdempotencyKey: "waiting-trigger",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WaitForHuman(
		ctx, f.WorkspaceID, first.RunID, settings.SourceID, "clarify", "choose target",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "queued while waiting", IdempotencyKey: "waiting-enqueue",
		DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	if claim, err := s.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute); !errors.Is(err, domain.ErrNotFound) || claim != nil {
		t.Fatalf("waiting_human should block auto claim: claim=%+v err=%v", claim, err)
	}

	reply, err := s.ReplyAndContinue(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "target is production", IdempotencyKey: "waiting-reply",
	})
	if err != nil || reply.RunID == nil {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
	if err := s.FinishRun(ctx, FinishRunParams{
		WorkspaceID: f.WorkspaceID, RunID: first.RunID, RunnerSourceID: settings.SourceID,
		Result: "late stale result", IdempotencyKey: "waiting-stale-finish",
	}); !errors.Is(err, domain.ErrRunNotCurrent) {
		t.Fatalf("superseded waiting run finish error=%v", err)
	}
	resumed, err := s.ClaimNextBuiltInRun(ctx, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.RunID != *reply.RunID || !resumed.Reused {
		t.Fatalf("queued reply was not resumed: %+v reply=%+v", resumed, reply)
	}
}

func TestAgentSettingsReuseOneCycleSourceAndMCPUpdateIsWorkspaceScoped(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, "Settings Workspace")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.SaveAgentSettings(ctx, workspace.ID, AgentSettingsInput{
		Enabled: true, ProviderBaseURL: "http://localhost:18090/v1", Model: "a",
		APIKeyEnv: "MODEL_KEY",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveAgentSettings(ctx, workspace.ID, AgentSettingsInput{
		Enabled: false, ProviderBaseURL: "http://localhost:18090/v1", Model: "b",
		APIKeyEnv: "MODEL_KEY_2",
	})
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := s.SaveAgentSettings(ctx, workspace.ID, AgentSettingsInput{
		Enabled: false, ProviderType: "chatgpt", ProviderBaseURL: "https://must-not-persist.example/v1",
		Model: "subscription-model", APIKeyEnv: "MUST_NOT_PERSIST",
	})
	if err != nil {
		t.Fatal(err)
	}
	if subscription.ProviderType != "chatgpt" || subscription.ProviderBaseUrl != "" || subscription.ApiKeyEnv != "" {
		t.Fatalf("subscription settings leaked API provider fields: %+v", subscription)
	}
	if subscription.SourceID != first.SourceID {
		t.Fatalf("subscription provider switch changed cycle_agent source: %s vs %s", subscription.SourceID, first.SourceID)
	}
	if first.SourceID != second.SourceID {
		t.Fatalf("settings created multiple cycle_agent sources: %s vs %s", first.SourceID, second.SourceID)
	}
	var sourceCount int
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM sources WHERE workspace_id=$1 AND kind='cycle_agent'",
		workspace.ID,
	).Scan(&sourceCount); err != nil {
		t.Fatal(err)
	}
	if sourceCount != 1 {
		t.Fatalf("cycle_agent source count=%d", sourceCount)
	}

	connection, err := s.SaveMCPConnection(ctx, workspace.ID, MCPConnectionInput{
		Name: "Tools", URL: "http://127.0.0.1:18091/mcp", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateWorkspace(ctx, "Other Workspace")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveMCPConnection(ctx, other.ID, MCPConnectionInput{
		ID: connection.ID, Name: "stolen", URL: "http://127.0.0.1:18091/mcp", Enabled: true,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-workspace MCP update error=%v", err)
	}
}
