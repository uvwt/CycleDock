package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/domain"
)

var integrationStore *Store

func TestMain(m *testing.M) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		os.Exit(m.Run())
	}

	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		panic(err)
	}
	_, filename, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(filename), "..", "..", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		panic(err)
	}
	if err := goose.Up(sqlDB, migrations); err != nil {
		panic(err)
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		panic(err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		panic(err)
	}
	integrationStore = New(pool)

	code := m.Run()
	pool.Close()
	_ = sqlDB.Close()
	os.Exit(code)
}

type testFixture struct {
	WorkspaceID uuid.UUID
	HumanID     uuid.UUID
	AgentID     uuid.UUID
	Agent2ID    uuid.UUID
	ProjectID   uuid.UUID
	CardID      uuid.UUID
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
			agent_runs, triggers, events, card_schedules, cards, projects, sources, workspaces
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

func TestSubmitCommentIsIdempotentAndEnqueuesBehindCurrentRun(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	first, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &f.AgentID, Content: "implement it", IdempotencyKey: "request-1",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.RunID == nil {
		t.Fatal("first comment should create a run when card is idle")
	}

	retry, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &f.AgentID, Content: "implement it", IdempotencyKey: "request-1",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !retry.Reused || retry.EventID != first.EventID || retry.TriggerID != first.TriggerID || retry.RunID == nil || *retry.RunID != *first.RunID {
		t.Fatalf("idempotent retry changed result: first=%+v retry=%+v", first, retry)
	}

	_, err = s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &f.AgentID, Content: "different", IdempotencyKey: "request-1",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if !errors.Is(err, domain.ErrIdempotencyMismatch) {
		t.Fatalf("mismatched retry error = %v, want ErrIdempotencyMismatch", err)
	}

	queued, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &f.AgentID, Content: "second direction", IdempotencyKey: "request-2",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if queued.RunID != nil {
		t.Fatal("enqueue behind a current run must remain a pending trigger")
	}
}

func TestTwoAgentsClaimOnlyOnePendingTrigger(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	_, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "pending work", IdempotencyKey: "claim-1", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, agentID := range []uuid.UUID{f.AgentID, f.Agent2ID} {
		agentID := agentID
		go func() {
			<-start
			_, err := s.ClaimNextTrigger(ctx, f.WorkspaceID, f.CardID, agentID, time.Minute)
			results <- err
		}()
	}
	close(start)
	var success int
	for range 2 {
		if err := <-results; err == nil {
			success++
		} else if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("unexpected claim error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("successful claims = %d, want 1", success)
	}

	var runs int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM agent_runs WHERE card_id = $1`, f.CardID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("run count = %d, want 1", runs)
	}
}

func TestDatabaseRejectsTwoConcurrentCurrentRuns(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	var triggerIDs []uuid.UUID
	for i := 0; i < 2; i++ {
		result, err := s.SubmitComment(ctx, SubmitCommentParams{
			WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
			Content: fmt.Sprintf("pending-%d", i), IdempotencyKey: fmt.Sprintf("current-%d", i),
			DispatchMode: domain.DispatchModeEnqueue,
		})
		if err != nil {
			t.Fatal(err)
		}
		triggerIDs = append(triggerIDs, result.TriggerID)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for i, triggerID := range triggerIDs {
		i, triggerID := i, triggerID
		go func() {
			<-start
			runner := f.AgentID
			if i == 1 {
				runner = f.Agent2ID
			}
			_, err := s.pool.Exec(ctx, `
				INSERT INTO agent_runs (
					id, workspace_id, card_id, trigger_id, runner_source_id, status, lease_expires_at
				)
				VALUES ($1, $2, $3, $4, $5, 'queued', $6)
			`, uuid.New(), f.WorkspaceID, f.CardID, triggerID, runner, time.Now().Add(time.Minute))
			errs <- err
		}()
	}
	close(start)
	var success, conflict int
	for range 2 {
		err := <-errs
		if err == nil {
			success++
			continue
		}
		if errors.Is(mapConstraintError(err), domain.ErrConflict) {
			conflict++
			continue
		}
		t.Fatalf("unexpected insert error: %v", err)
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d, want 1/1", success, conflict)
	}
}

func TestInterruptAndFinishRacePreservesLinearizedState(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	if err := s.MoveTodo(ctx, f.WorkspaceID, f.CardID, f.HumanID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}

	first, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &f.AgentID, Content: "old direction", IdempotencyKey: "race-old",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil || first.RunID == nil {
		t.Fatalf("create first run: result=%+v err=%v", first, err)
	}
	if err := s.StartRun(ctx, f.WorkspaceID, *first.RunID, f.AgentID, time.Minute); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var finishErr, interruptErr error
	var interrupt SubmitCommentResult
	go func() {
		defer wg.Done()
		<-start
		move := domain.TodoStateReview
		finishErr = s.FinishRun(ctx, FinishRunParams{
			WorkspaceID: f.WorkspaceID, RunID: *first.RunID, RunnerSourceID: f.AgentID,
			Result: "old result", MoveTo: &move,
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		interrupt, interruptErr = s.SubmitComment(ctx, SubmitCommentParams{
			WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
			RunnerSourceID: &f.Agent2ID, Content: "new direction", IdempotencyKey: "race-new",
			DispatchMode: domain.DispatchModeInterrupt,
		})
	}()
	close(start)
	wg.Wait()
	if interruptErr != nil || interrupt.RunID == nil {
		t.Fatalf("interrupt failed: result=%+v err=%v", interrupt, interruptErr)
	}

	var oldStatus, cardColumn string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM agent_runs WHERE id = $1`, *first.RunID).Scan(&oldStatus); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT todo_state FROM cards WHERE id = $1`, f.CardID).Scan(&cardColumn); err != nil {
		t.Fatal(err)
	}
	if oldStatus == string(domain.RunStatusSuperseded) {
		if !errors.Is(finishErr, domain.ErrRunNotCurrent) {
			t.Fatalf("late finish error = %v, want ErrRunNotCurrent", finishErr)
		}
		if cardColumn != string(domain.TodoStateWorking) {
			t.Fatalf("superseded run changed card to %s", cardColumn)
		}
	} else if oldStatus == string(domain.RunStatusCompleted) {
		if finishErr != nil || cardColumn != string(domain.TodoStateReview) {
			t.Fatalf("finish-first ordering inconsistent: finish=%v column=%s", finishErr, cardColumn)
		}
	} else {
		t.Fatalf("unexpected old run status %s", oldStatus)
	}

	var currentRun uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		SELECT id FROM agent_runs
		WHERE card_id = $1 AND status IN ('queued', 'running', 'waiting_human')
	`, f.CardID).Scan(&currentRun); err != nil {
		t.Fatal(err)
	}
	if currentRun != *interrupt.RunID {
		t.Fatalf("current run = %s, want new run %s", currentRun, *interrupt.RunID)
	}
}

func TestWaitingHumanReplySupersedesOldRunAndHeartbeatHasNoTimelineEvent(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	first, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &f.AgentID, Content: "need input", IdempotencyKey: "wait-1",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil || first.RunID == nil {
		t.Fatalf("create run: %+v %v", first, err)
	}
	if err := s.StartRun(ctx, f.WorkspaceID, *first.RunID, f.AgentID, time.Minute); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE card_id = $1`, f.CardID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, f.WorkspaceID, *first.RunID, f.AgentID, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE card_id = $1`, f.CardID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("heartbeat added timeline event: before=%d after=%d", before, after)
	}

	if err := s.WaitForHuman(ctx, f.WorkspaceID, *first.RunID, f.AgentID, "need_user_input", "choose target"); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, f.WorkspaceID, *first.RunID, f.AgentID, time.Minute); !errors.Is(err, domain.ErrRunNotCurrent) {
		t.Fatalf("waiting_human heartbeat error=%v, want ErrRunNotCurrent", err)
	}
	reply, err := s.ReplyAndContinue(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &f.Agent2ID, Content: "use target B", IdempotencyKey: "wait-2",
	})
	if err != nil || reply.RunID == nil {
		t.Fatalf("reply and continue: %+v %v", reply, err)
	}
	var oldStatus string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM agent_runs WHERE id = $1`, *first.RunID).Scan(&oldStatus); err != nil {
		t.Fatal(err)
	}
	if oldStatus != string(domain.RunStatusSuperseded) {
		t.Fatalf("old status=%s, want superseded", oldStatus)
	}
}

func TestLeaseExpiryHealthAndHumanOnlyDone(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()

	normal := newFixture(t, s, domain.CardKindTodo)
	result, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: normal.WorkspaceID, CardID: normal.CardID, AuthorSourceID: normal.HumanID,
		RunnerSourceID: &normal.AgentID, Content: "lease", IdempotencyKey: "lease-1",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil || result.RunID == nil {
		t.Fatalf("create run: %+v %v", result, err)
	}
	if err := s.StartRun(ctx, normal.WorkspaceID, *result.RunID, normal.AgentID, time.Minute); err != nil {
		t.Fatal(err)
	}
	expired, err := s.ExpireLeases(ctx, normal.WorkspaceID, time.Now().Add(2*time.Minute))
	if err != nil || expired != 1 {
		t.Fatalf("expired=%d err=%v", expired, err)
	}

	if err := s.MoveTodo(ctx, normal.WorkspaceID, normal.CardID, normal.HumanID, domain.TodoStateReview); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTodo(ctx, normal.WorkspaceID, normal.CardID, normal.AgentID, domain.TodoStateDone); !errors.Is(err, domain.ErrHumanRequired) {
		t.Fatalf("agent done error=%v, want ErrHumanRequired", err)
	}
	if err := s.MoveTodo(ctx, normal.WorkspaceID, normal.CardID, normal.HumanID, domain.TodoStateDone); err != nil {
		t.Fatal(err)
	}

	resetDatabase(t)
	ongoing := newFixture(t, s, domain.CardKindOngoing)
	run, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: ongoing.WorkspaceID, CardID: ongoing.CardID, AuthorSourceID: ongoing.HumanID,
		RunnerSourceID: &ongoing.AgentID, Content: "check health", IdempotencyKey: "health-1",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil || run.RunID == nil {
		t.Fatalf("create ongoing run: %+v %v", run, err)
	}
	if err := s.StartRun(ctx, ongoing.WorkspaceID, *run.RunID, ongoing.AgentID, time.Minute); err != nil {
		t.Fatal(err)
	}
	warning := domain.CheckinWarning
	if _, err := s.AddRunEvent(ctx, RunEventParams{
		WorkspaceID: ongoing.WorkspaceID, RunID: *run.RunID, RunnerSourceID: ongoing.AgentID,
		Type: domain.EventTypeCheckin, Content: "disk usage elevated", Checkin: &warning,
	}); err != nil {
		t.Fatal(err)
	}
	health, err := s.Health(ctx, ongoing.WorkspaceID, ongoing.CardID)
	if err != nil || health != domain.CheckinWarning {
		t.Fatalf("health=%s err=%v", health, err)
	}
}

func TestSchedulerDuplicateOccurrenceCreatesOneTriggerAndPausedSkips(t *testing.T) {
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
			_, created, err := s.ProcessDueSchedule(ctx, f.WorkspaceID, schedule.ID, now)
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
	var triggerCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM triggers WHERE schedule_id = $1 AND scheduled_for = $2
	`, schedule.ID, due).Scan(&triggerCount); err != nil {
		t.Fatal(err)
	}
	if triggerCount != 1 {
		t.Fatalf("duplicate occurrence trigger count=%d, want 1", triggerCount)
	}
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM card_schedules
		WHERE workspace_id = $1 AND card_id = $2 AND id = $3
	`, f.WorkspaceID, f.CardID, schedule.ID); err == nil {
		t.Fatal("database cascade-deleted schedule trigger history")
	}
	if err := s.DeleteSchedule(ctx, f.WorkspaceID, f.CardID, schedule.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("delete schedule with trigger history error=%v, want ErrConflict", err)
	}

	if err := s.SetOngoingState(ctx, f.WorkspaceID, f.CardID, f.HumanID, domain.OngoingStatePaused, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE card_schedules SET next_trigger_at = $2 WHERE id = $1`, schedule.ID, due); err != nil {
		t.Fatal(err)
	}
	_, created, err := s.ProcessDueSchedule(ctx, f.WorkspaceID, schedule.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("paused ongoing card created a scheduled trigger")
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

	otherCard, err := s.CreateTodoCard(ctx, f.WorkspaceID, f.ProjectID, f.HumanID, "Other todo card", "", uuid.NullUUID{}, 1)
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
		INSERT INTO triggers (
			id, workspace_id, card_id, cause, cause_event_id, created_by_source_id, dispatch_mode
		)
		VALUES ($1, $2, $3, 'human_comment', $4, $5, 'enqueue')
	`, uuid.New(), f.WorkspaceID, otherCard.ID, eventID, f.HumanID); err == nil {
		t.Fatal("database accepted a trigger whose cause event belongs to another card")
	}

	triggerID := uuid.New()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO triggers (id, workspace_id, card_id, cause, created_by_source_id, dispatch_mode)
		VALUES ($1, $2, $3, 'system_followup', $4, 'enqueue')
	`, triggerID, f.WorkspaceID, f.CardID, f.AgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO agent_runs (
			id, workspace_id, card_id, trigger_id, runner_source_id, status, lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, 'queued', $6)
	`, uuid.New(), f.WorkspaceID, otherCard.ID, triggerID, f.AgentID, time.Now().Add(time.Minute)); err == nil {
		t.Fatal("database accepted a run whose trigger belongs to another card")
	}

	runID := uuid.New()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO agent_runs (
			id, workspace_id, card_id, trigger_id, runner_source_id, status, lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, 'queued', $6)
	`, runID, f.WorkspaceID, f.CardID, triggerID, f.AgentID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id, run_id, content
		)
		VALUES ($1, $2, $3, 'progress', $4, $5, 'wrong card')
	`, uuid.New(), f.WorkspaceID, otherCard.ID, f.AgentID, runID); err == nil {
		t.Fatal("database accepted an event whose run belongs to another card")
	}

	ongoingA, err := s.CreateOngoingCard(ctx, f.WorkspaceID, f.ProjectID, f.HumanID, "Ongoing A", "", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	ongoingB, err := s.CreateOngoingCard(ctx, f.WorkspaceID, f.ProjectID, f.HumanID, "Ongoing B", "", uuid.NullUUID{})
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
		INSERT INTO triggers (
			id, workspace_id, card_id, cause, dispatch_mode, schedule_id, scheduled_for
		)
		VALUES ($1, $2, $3, 'scheduled_followup', 'enqueue', $4, $5)
	`, uuid.New(), f.WorkspaceID, ongoingB.ID, schedule.ID, schedule.NextTriggerAt); err == nil {
		t.Fatal("database accepted a scheduled trigger whose schedule belongs to another card")
	}
}

func TestDiscussionMetadataWritebackGuards(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	t.Run("completed run may finish delayed metadata", func(t *testing.T) {
		discussion, err := s.CreateDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
		if err != nil {
			t.Fatal(err)
		}
		first, err := s.SubmitComment(ctx, SubmitCommentParams{
			WorkspaceID: f.WorkspaceID, CardID: discussion.ID, AuthorSourceID: f.HumanID,
			RunnerSourceID: &f.AgentID, Content: "Plan a delayed metadata write.",
			IdempotencyKey: "metadata-completed-comment", DispatchMode: domain.DispatchModeEnqueue,
		})
		if err != nil || first.RunID == nil {
			t.Fatalf("submit first comment: result=%+v err=%v", first, err)
		}
		if err := s.StartRun(ctx, f.WorkspaceID, *first.RunID, f.AgentID, time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishRun(ctx, FinishRunParams{
			WorkspaceID: f.WorkspaceID, RunID: *first.RunID, RunnerSourceID: f.AgentID,
			Result: "Main reply completed first.", IdempotencyKey: "metadata-completed-finish",
		}); err != nil {
			t.Fatalf("finish main run: %v", err)
		}
		if err := s.SetDiscussionMetadataForRun(
			ctx, f.WorkspaceID, *first.RunID, f.AgentID,
			"Delayed title", "Metadata may arrive after the main reply.",
		); err != nil {
			t.Fatalf("completed run could not write delayed metadata: %v", err)
		}
		card, err := s.GetCard(ctx, f.WorkspaceID, discussion.ID)
		if err != nil {
			t.Fatal(err)
		}
		if card.Title != "Delayed title" {
			t.Fatalf("delayed metadata title=%q", card.Title)
		}
	})

	t.Run("superseded run cannot write metadata", func(t *testing.T) {
		discussion, err := s.CreateDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
		if err != nil {
			t.Fatal(err)
		}
		first, err := s.SubmitComment(ctx, SubmitCommentParams{
			WorkspaceID: f.WorkspaceID, CardID: discussion.ID, AuthorSourceID: f.HumanID,
			RunnerSourceID: &f.AgentID, Content: "Old direction.",
			IdempotencyKey: "metadata-superseded-old", DispatchMode: domain.DispatchModeEnqueue,
		})
		if err != nil || first.RunID == nil {
			t.Fatalf("submit old direction: result=%+v err=%v", first, err)
		}
		if err := s.StartRun(ctx, f.WorkspaceID, *first.RunID, f.AgentID, time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SubmitComment(ctx, SubmitCommentParams{
			WorkspaceID: f.WorkspaceID, CardID: discussion.ID, AuthorSourceID: f.HumanID,
			RunnerSourceID: &f.Agent2ID, Content: "New direction.",
			IdempotencyKey: "metadata-superseded-new", DispatchMode: domain.DispatchModeInterrupt,
		}); err != nil {
			t.Fatalf("interrupt old run: %v", err)
		}
		err = s.SetDiscussionMetadataForRun(
			ctx, f.WorkspaceID, *first.RunID, f.AgentID,
			"Stale title", "This stale metadata must not be written.",
		)
		if !errors.Is(err, domain.ErrRunNotCurrent) {
			t.Fatalf("superseded run metadata error=%v, want ErrRunNotCurrent", err)
		}
		card, err := s.GetCard(ctx, f.WorkspaceID, discussion.ID)
		if err != nil {
			t.Fatal(err)
		}
		if card.Title != "" {
			t.Fatalf("superseded run changed title to %q", card.Title)
		}
	})

	t.Run("existing human title wins", func(t *testing.T) {
		discussion, err := s.CreateDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
		if err != nil {
			t.Fatal(err)
		}
		first, err := s.SubmitComment(ctx, SubmitCommentParams{
			WorkspaceID: f.WorkspaceID, CardID: discussion.ID, AuthorSourceID: f.HumanID,
			RunnerSourceID: &f.AgentID, Content: "Keep my manual title.",
			IdempotencyKey: "metadata-manual-title-comment", DispatchMode: domain.DispatchModeEnqueue,
		})
		if err != nil || first.RunID == nil {
			t.Fatalf("submit manual-title comment: result=%+v err=%v", first, err)
		}
		if err := s.StartRun(ctx, f.WorkspaceID, *first.RunID, f.AgentID, time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateCardText(ctx, f.WorkspaceID, discussion.ID, "Human title", "Human description"); err != nil {
			t.Fatalf("set human title: %v", err)
		}
		if err := s.SetDiscussionMetadataForRun(
			ctx, f.WorkspaceID, *first.RunID, f.AgentID,
			"Generated title", "Generated description",
		); err != nil {
			t.Fatalf("metadata no-op on human title: %v", err)
		}
		card, err := s.GetCard(ctx, f.WorkspaceID, discussion.ID)
		if err != nil {
			t.Fatal(err)
		}
		if card.Title != "Human title" || card.Description != "Human description" {
			t.Fatalf("metadata overwrote human text: title=%q description=%q", card.Title, card.Description)
		}
	})
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

	first, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: discussion.ID, AuthorSourceID: f.HumanID,
		RunnerSourceID: &f.AgentID, Content: "Plan the release and monitor it every day.",
		IdempotencyKey: "discussion-first", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil || first.RunID == nil {
		t.Fatalf("first discussion message: result=%+v err=%v", first, err)
	}
	if err := s.StartRun(ctx, f.WorkspaceID, *first.RunID, f.AgentID, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDiscussionMetadataForRun(
		ctx, f.WorkspaceID, *first.RunID, f.AgentID,
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

	second, err := s.SubmitComment(ctx, SubmitCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: discussion.ID, AuthorSourceID: f.HumanID,
		Content: "Queue another thought.", IdempotencyKey: "discussion-second",
		DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil || second.TriggerID == uuid.Nil {
		t.Fatalf("queue second discussion direction: result=%+v err=%v", second, err)
	}
	if err := s.ArchiveDiscussion(ctx, f.WorkspaceID, discussion.ID, f.HumanID); err != nil {
		t.Fatalf("archive discussion: %v", err)
	}
	current, err := s.CurrentRun(ctx, f.WorkspaceID, discussion.ID)
	if err != nil || current != nil {
		t.Fatalf("archived discussion still has current run: run=%+v err=%v", current, err)
	}
	run, err := s.GetRun(ctx, f.WorkspaceID, *first.RunID)
	if err != nil || run.Status != string(domain.RunStatusCancelled) {
		t.Fatalf("archive did not cancel current run: run=%+v err=%v", run, err)
	}
	queue, err := s.ListPendingQueue(ctx, f.WorkspaceID, discussion.ID)
	if err != nil || len(queue) != 0 {
		t.Fatalf("archive did not cancel pending queue: queue=%+v err=%v", queue, err)
	}
	_, err = s.SubmitComment(ctx, SubmitCommentParams{
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
