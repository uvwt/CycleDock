package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/domain"
)

var integrationStore *Store

func TestMain(m *testing.M) {
	os.Exit(runStoreTests(m))
}

func runStoreTests(m *testing.M) int {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		return m.Run()
	}

	adminPool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		panic(err)
	}
	defer adminPool.Close()
	// 所有迁移和清表都只能发生在本次测试的独立 schema 中，
	// 即使误传开发库连接串，也不得修改已有业务表。
	schema := "store_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(context.Background(), "CREATE SCHEMA "+quotedSchema); err != nil {
		panic(err)
	}
	defer func() {
		if _, err := adminPool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			panic(err)
		}
	}()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		panic(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	sqlDB := stdlib.OpenDB(*config.ConnConfig)
	defer sqlDB.Close()
	_, filename, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		panic(err)
	}
	if err := goose.Up(sqlDB, migrations); err != nil {
		panic(err)
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		panic(err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		panic(err)
	}
	defer pool.Close()
	integrationStore = New(pool)

	return m.Run()
}

type testFixture struct {
	WorkspaceID uuid.UUID
	HumanID     uuid.UUID
	AgentID     uuid.UUID
	Agent2ID    uuid.UUID
	ProjectID   uuid.UUID
	CardID      uuid.UUID
}

func testAgentSettings(baseURL, model, apiKeyEnv string) AgentSettingsInput {
	return AgentSettingsInput{
		Routes: []ModelRouteInput{{
			Enabled: true, ProviderType: "openai_compatible",
			ProviderBaseURL: baseURL, Model: model, APIKeyEnv: apiKeyEnv,
		}},
	}
}

func requireIntegration(t *testing.T) *Store {
	t.Helper()
	if integrationStore == nil {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	resetDatabase(t)
	return integrationStore
}

func resetDatabase(t *testing.T) {
	t.Helper()
	_, err := integrationStore.pool.Exec(context.Background(), `
		TRUNCATE TABLE
			agent_artifacts, agent_session_events, agent_sessions,
			turn_attempts, card_turns, events, card_schedules,
			cards, projects, sources, workspaces
		RESTART IDENTITY CASCADE
	`)
	if err != nil {
		t.Fatalf("reset database: %v", err)
	}
}

func newFixture(t *testing.T, s *Store, kind domain.CardKind) testFixture {
	t.Helper()
	ctx := context.Background()
	workspace, err := s.CreateWorkspace(ctx, "Test Workspace")
	if err != nil {
		t.Fatal(err)
	}
	human, err := s.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateSource(ctx, workspace.ID, domain.SourceKindCycleAgent, "Agent A")
	if err != nil {
		t.Fatal(err)
	}
	agent2, err := s.CreateSource(ctx, workspace.ID, domain.SourceKindCycleAgent, "Agent B")
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, workspace.ID, "Project", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var cardID uuid.UUID
	if kind == domain.CardKindTodo {
		card, err := s.CreateTodoCard(ctx, workspace.ID, project.ID, human.ID, "Normal", "", uuid.NullUUID{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		cardID = card.ID
	} else {
		card, err := s.CreateOngoingCard(ctx, workspace.ID, project.ID, human.ID, "Ongoing", "", uuid.NullUUID{})
		if err != nil {
			t.Fatal(err)
		}
		cardID = card.ID
	}
	return testFixture{
		WorkspaceID: workspace.ID,
		HumanID:     human.ID,
		AgentID:     agent.ID,
		Agent2ID:    agent2.ID,
		ProjectID:   project.ID,
		CardID:      cardID,
	}
}

func TestSchedulerDuplicateOccurrenceCreatesOneTurnAndPausedSkips(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindOngoing)
	ctx := context.Background()
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	rule := domain.ScheduleRule{Frequency: domain.ScheduleDaily, Hour: 9, Minute: 0, Timezone: "Asia/Shanghai"}
	schedule, err := s.CreateSchedule(ctx, f.WorkspaceID, f.CardID, rule, now)
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Minute)
	if _, err := s.pool.Exec(ctx, `
		UPDATE card_schedules SET next_trigger_at = $2 WHERE id = $1
	`, schedule.ID, due); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	type scheduleResult struct {
		created bool
		err     error
	}
	results := make(chan scheduleResult, 2)
	for range 2 {
		go func() {
			<-start
			_, created, err := s.ProcessDueScheduleTurn(ctx, f.WorkspaceID, schedule.ID, now)
			results <- scheduleResult{created: created, err: err}
		}()
	}
	close(start)
	var createdCount int
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count=%d, want 1", createdCount)
	}
	var turnCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM card_turns WHERE schedule_id = $1 AND scheduled_for = $2
	`, schedule.ID, due).Scan(&turnCount); err != nil {
		t.Fatal(err)
	}
	if turnCount != 1 {
		t.Fatalf("duplicate occurrence turn count=%d, want 1", turnCount)
	}
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM card_schedules
		WHERE workspace_id = $1 AND card_id = $2 AND id = $3
	`, f.WorkspaceID, f.CardID, schedule.ID); err == nil {
		t.Fatal("database cascade-deleted schedule turn history")
	}
	if err := s.DeleteSchedule(ctx, f.WorkspaceID, f.CardID, schedule.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("delete schedule with turn history error=%v, want ErrConflict", err)
	}

	if err := s.SetOngoingState(ctx, f.WorkspaceID, f.CardID, f.HumanID, domain.OngoingStatePaused, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE card_schedules SET next_trigger_at = $2 WHERE id = $1`, schedule.ID, due); err != nil {
		t.Fatal(err)
	}
	_, created, err := s.ProcessDueScheduleTurn(ctx, f.WorkspaceID, schedule.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("paused ongoing card created a scheduled turn")
	}
}

func TestWorkspaceScopeRejectsCrossTenantCardRead(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	other, err := s.CreateWorkspace(context.Background(), "Other Workspace")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GetCard(context.Background(), other.ID, f.CardID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-workspace GetCard error=%v, want ErrNotFound", err)
	}
}

func TestDatabaseRejectsCrossCardReferencesInsideWorkspace(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	otherCard, err := s.CreateTodoCard(
		ctx, f.WorkspaceID, f.ProjectID, f.HumanID,
		"Other todo card", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}

	eventID := uuid.New()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO events (id, workspace_id, card_id, event_type, actor_source_id, content)
		VALUES ($1, $2, $3, 'comment', $4, 'card A event')
	`, eventID, f.WorkspaceID, f.CardID, f.HumanID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO card_turns (
			id, workspace_id, card_id, source, content,
			created_by_source_id, source_event_id, dispatch_mode
		)
		VALUES ($1, $2, $3, 'human', 'cross-card event', $4, $5, 'enqueue')
	`, uuid.New(), f.WorkspaceID, otherCard.ID, f.HumanID, eventID); err == nil {
		t.Fatal("database accepted a Turn whose source Event belongs to another card")
	}

	validTurn, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AuthorSourceID: f.HumanID,
		Content:        "valid turn on card A",
		IdempotencyKey: "cross-card-valid-turn",
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO turn_attempts (
			id, workspace_id, card_id, turn_id, attempt_number, reason,
			runner_source_id, status
		)
		VALUES ($1, $2, $3, $4, 1, 'initial', $5, 'queued')
	`, uuid.New(), f.WorkspaceID, otherCard.ID, validTurn.TurnID, f.AgentID); err == nil {
		t.Fatal("database accepted an Attempt whose Turn belongs to another card")
	}

	claim, err := s.ClaimCardTurnAttempt(
		ctx, f.WorkspaceID, f.CardID, f.AgentID,
		time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id, attempt_id, content
		)
		VALUES ($1, $2, $3, 'finding', $4, $5, 'cross-card attempt event')
	`, uuid.New(), f.WorkspaceID, otherCard.ID, f.AgentID, claim.Attempt.ID); err == nil {
		t.Fatal("database accepted an Event whose Attempt belongs to another card")
	}

	ongoingA, err := s.CreateOngoingCard(
		ctx, f.WorkspaceID, f.ProjectID, f.HumanID,
		"Ongoing A", "", uuid.NullUUID{},
	)
	if err != nil {
		t.Fatal(err)
	}
	ongoingB, err := s.CreateOngoingCard(
		ctx, f.WorkspaceID, f.ProjectID, f.HumanID,
		"Ongoing B", "", uuid.NullUUID{},
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	schedule, err := s.CreateSchedule(ctx, f.WorkspaceID, ongoingA.ID, domain.ScheduleRule{
		Frequency: domain.ScheduleDaily,
		Hour:      9,
		Minute:    0,
		Timezone:  "Asia/Shanghai",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO card_turns (
			id, workspace_id, card_id, source, content,
			dispatch_mode, schedule_id, scheduled_for
		)
		VALUES ($1, $2, $3, 'schedule', 'cross-card schedule', 'enqueue', $4, $5)
	`, uuid.New(), f.WorkspaceID, ongoingB.ID, schedule.ID, schedule.NextTriggerAt); err == nil {
		t.Fatal("database accepted a Schedule Turn whose schedule belongs to another card")
	}
}

func TestDiscussionLifecycleGeneratesWorkAndPreservesDerivedCards(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	discussion, err := s.CreateDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil {
		t.Fatal(err)
	}
	if discussion.Kind != string(domain.CardKindDiscussion) ||
		!discussion.DiscussionState.Valid ||
		discussion.DiscussionState.String != string(domain.DiscussionStateActive) ||
		discussion.Title != "" {
		t.Fatalf("unexpected new discussion: %+v", discussion)
	}

	first, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: discussion.ID, AuthorSourceID: f.HumanID,
		Content:        "Plan the release and monitor it every day.",
		IdempotencyKey: "discussion-first", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatalf("first discussion message: result=%+v err=%v", first, err)
	}
	claim, err := s.ClaimCardTurnAttempt(
		ctx, f.WorkspaceID, discussion.ID, f.AgentID, time.Now().UTC(), time.Minute,
	)
	if err != nil || claim.Turn.ID != first.TurnID {
		t.Fatalf("claim discussion turn: claim=%+v err=%v", claim, err)
	}
	if _, err := s.SaveGeneratedDiscussionMetadata(
		ctx, f.WorkspaceID, discussion.ID,
		"Release planning", "Plan the release and decide the follow-up monitoring work.",
	); err != nil {
		t.Fatalf("set generated discussion metadata: %v", err)
	}
	updatedDiscussion, err := s.GetCard(ctx, f.WorkspaceID, discussion.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedDiscussion.Title != "Release planning" ||
		updatedDiscussion.Description != "Plan the release and decide the follow-up monitoring work." {
		t.Fatalf("discussion metadata was not persisted: %+v", updatedDiscussion)
	}

	todo, err := s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID: f.WorkspaceID, DiscussionID: discussion.ID, SourceID: f.AgentID,
		Kind: domain.CardKindTodo, Title: "Ship release", Description: "Complete the release.",
		IdempotencyKey: "generate-todo",
	})
	if err != nil {
		t.Fatal(err)
	}
	ongoing, err := s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID: f.WorkspaceID, DiscussionID: discussion.ID, SourceID: f.AgentID,
		Kind: domain.CardKindOngoing, Title: "Daily release health", Description: "Monitor release health every day.",
		IdempotencyKey: "generate-ongoing",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []uuid.NullUUID{todo.Card.SourceDiscussionID, ongoing.Card.SourceDiscussionID} {
		if !source.Valid || source.UUID != discussion.ID {
			t.Fatalf("generated card lost source discussion: %+v", source)
		}
	}

	_, err = s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID: f.WorkspaceID, DiscussionID: todo.Card.ID, SourceID: f.AgentID,
		Kind: domain.CardKindTodo, Title: "Nested work", IdempotencyKey: "nested-work",
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Todo generated nested work: %v", err)
	}
	_, err = s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID: f.WorkspaceID, DiscussionID: ongoing.Card.ID, SourceID: f.AgentID,
		Kind: domain.CardKindTodo, Title: "Nested ongoing work", IdempotencyKey: "nested-ongoing-work",
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Ongoing generated nested work: %v", err)
	}

	second, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: discussion.ID, AuthorSourceID: f.HumanID,
		Content: "Queue another thought.", IdempotencyKey: "discussion-second",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil || second.TurnID == uuid.Nil {
		t.Fatalf("queue second discussion direction: result=%+v err=%v", second, err)
	}
	if err := s.ArchiveDiscussion(ctx, f.WorkspaceID, discussion.ID, f.HumanID); err != nil {
		t.Fatalf("archive discussion: %v", err)
	}
	current, err := s.CurrentAttemptView(ctx, f.WorkspaceID, discussion.ID)
	if err != nil || current != nil {
		t.Fatalf("archived discussion still has current attempt: attempt=%+v err=%v", current, err)
	}
	attempt, err := s.GetAttempt(ctx, f.WorkspaceID, claim.Attempt.ID)
	if err != nil || attempt.Status != domain.AttemptStatusCancelled {
		t.Fatalf("archive did not cancel current attempt: attempt=%+v err=%v", attempt, err)
	}
	queue, err := s.ListPendingTurns(ctx, f.WorkspaceID, discussion.ID)
	if err != nil || len(queue) != 0 {
		t.Fatalf("archive did not cancel pending queue: queue=%+v err=%v", queue, err)
	}
	_, err = s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: discussion.ID, AuthorSourceID: f.HumanID,
		Content: "should be rejected", IdempotencyKey: "after-archive",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("archived discussion accepted new direction: %v", err)
	}
	_, err = s.GenerateWorkFromDiscussion(ctx, GenerateWorkParams{
		WorkspaceID: f.WorkspaceID, DiscussionID: discussion.ID, SourceID: f.AgentID,
		Kind: domain.CardKindTodo, Title: "After archive", IdempotencyKey: "after-archive-work",
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("archived discussion generated work: %v", err)
	}

	if err := s.DeleteDiscussion(ctx, f.WorkspaceID, discussion.ID, f.HumanID); err != nil {
		t.Fatalf("delete discussion: %v", err)
	}
	if _, err := s.GetCard(ctx, f.WorkspaceID, discussion.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted discussion still exists: %v", err)
	}
	for _, generatedID := range []uuid.UUID{todo.Card.ID, ongoing.Card.ID} {
		generated, err := s.GetCard(ctx, f.WorkspaceID, generatedID)
		if err != nil {
			t.Fatalf("generated card was deleted with discussion: %v", err)
		}
		if generated.SourceDiscussionID.Valid {
			t.Fatalf("generated card kept dangling source discussion: %+v", generated.SourceDiscussionID)
		}
	}
}
