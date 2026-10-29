package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestProjectActivityFiltersRunnerBeforeLimitAndScopesAgents(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	client, err := s.CreateSource(ctx, f.WorkspaceID, domain.SourceKindOAuthClient, "AI Client")
	if err != nil {
		t.Fatal(err)
	}
	idleClient, err := s.CreateSource(ctx, f.WorkspaceID, domain.SourceKindOAuthClient, "AI Client")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "External work", IdempotencyKey: "external", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, client.ID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.AddAttemptEvent(ctx, AttemptEventParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: client.ID, Type: domain.EventTypeFinding,
		Content: "External finding", IdempotencyKey: "finding",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 用更新的其他 Agent 记录挤出全量列表，验证筛选发生在 LIMIT 之前。
	otherCard, err := s.CreateTodoCard(ctx, f.WorkspaceID, f.ProjectID, f.HumanID, "Cycle work", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: otherCard.ID, AuthorSourceID: f.HumanID,
		Content: "Newer work", IdempotencyKey: "cycle", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, otherCard.ID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.ListProjectActivity(ctx, f.WorkspaceID, f.ProjectID, 1, uuid.NullUUID{})
	if err != nil || len(all) != 1 || all[0].SourceID != f.AgentID.String() {
		t.Fatalf("latest activity: rows=%+v err=%v", all, err)
	}
	filtered, err := s.ListProjectActivity(ctx, f.WorkspaceID, f.ProjectID, 1, uuid.NullUUID{UUID: client.ID, Valid: true})
	if err != nil || len(filtered) != 1 || filtered[0].SourceID != client.ID.String() || filtered[0].SourceKind != "oauth_client" {
		t.Fatalf("external activity before limit: rows=%+v err=%v", filtered, err)
	}
	matching, err := s.ListProjectActivity(ctx, f.WorkspaceID, f.ProjectID, 20, uuid.NullUUID{UUID: client.ID, Valid: true})
	if err != nil || len(matching) != 2 {
		t.Fatalf("external attempt and finding: rows=%+v err=%v", matching, err)
	}
	for _, row := range matching {
		if row.SourceID != client.ID.String() || row.SourceKind != "oauth_client" {
			t.Fatalf("activity attributed to input author instead of runner: %+v", row)
		}
	}
	idle, err := s.ListProjectActivity(ctx, f.WorkspaceID, f.ProjectID, 20, uuid.NullUUID{UUID: idleClient.ID, Valid: true})
	if err != nil || len(idle) != 0 {
		t.Fatalf("same-name idle client leaked activity: rows=%+v err=%v", idle, err)
	}

	otherWorkspace, err := s.CreateWorkspace(ctx, "Other workspace")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateSource(ctx, otherWorkspace.ID, domain.SourceKindOAuthClient, "Private client")
	if err != nil {
		t.Fatal(err)
	}
	agents, err := s.ListProjectActivityAgents(ctx, f.WorkspaceID, f.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	var sawClient, sawIdle bool
	for _, agent := range agents {
		if agent.ID == foreign.ID || agent.Kind == "human" {
			t.Fatalf("agent list escaped scope: %+v", agent)
		}
		sawClient = sawClient || agent.ID == client.ID
		sawIdle = sawIdle || agent.ID == idleClient.ID
	}
	if !sawClient || !sawIdle {
		t.Fatalf("authorized sources missing from agent list: %+v", agents)
	}
	foreignRows, err := s.ListProjectActivity(ctx, f.WorkspaceID, f.ProjectID, 20, uuid.NullUUID{UUID: foreign.ID, Valid: true})
	if err != nil || len(foreignRows) != 0 {
		t.Fatalf("foreign source activity: rows=%+v err=%v", foreignRows, err)
	}
	if _, err := s.ListProjectActivityAgents(ctx, otherWorkspace.ID, f.ProjectID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-workspace project agent list: %v", err)
	}
}
