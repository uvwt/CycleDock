package agentsession

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/sessiontestsuite"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

var integrationPool *pgxpool.Pool

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

	integrationPool, err = pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		panic(err)
	}
	if err := integrationPool.Ping(context.Background()); err != nil {
		panic(err)
	}

	code := m.Run()
	integrationPool.Close()
	_ = sqlDB.Close()
	os.Exit(code)
}

func TestIdentityUsesWorkspaceAndCard(t *testing.T) {
	workspaceID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	cardID := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	appName, userID, sessionID := Identity(workspaceID, cardID)
	if appName != AppName {
		t.Fatalf("appName=%q, want %q", appName, AppName)
	}
	if userID != workspaceID.String() {
		t.Fatalf("userID=%q, want workspace ID", userID)
	}
	if sessionID != cardID.String() {
		t.Fatalf("sessionID=%q, want card ID", sessionID)
	}
}

func TestPostgresServiceConformance(t *testing.T) {
	if integrationPool == nil {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	sessiontestsuite.RunServiceTests(t, sessiontestsuite.SuiteOptions{
		SupportsUserProvidedSessionID: true,
		AppName:                       AppName,
	}, func(t *testing.T) session.Service {
		resetAgentSessions(t)
		return NewPostgresService(integrationPool)
	})
}

func TestToolResultSurvivesTheNextTurn(t *testing.T) {
	service := requirePostgresService(t)
	ctx := context.Background()
	workspaceID := uuid.New()
	cardID := uuid.New()
	appName, userID, sessionID := Identity(workspaceID, cardID)

	created, err := service.Create(ctx, &session.CreateRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}

	firstTurn := &session.Event{
		ID:           "turn-1-tool-result",
		InvocationID: "invocation-1",
		Author:       "cycle_agent",
		Timestamp:    time.Now().UTC().Add(-time.Second),
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromParts([]*genai.Part{
				genai.NewPartFromFunctionResponse("docker_ps", map[string]any{
					"containers": []any{
						map[string]any{"name": "nginx", "image": "nginx:1.29"},
						map[string]any{"name": "api", "image": "cycledock-api:dev"},
					},
				}),
			}, genai.RoleUser),
		},
	}
	if err := service.AppendEvent(ctx, created.Session, firstTurn); err != nil {
		t.Fatal(err)
	}

	secondTurn := &session.Event{
		ID:           "turn-2-user",
		InvocationID: "invocation-2",
		Author:       "user",
		Timestamp:    time.Now().UTC(),
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromText("刚才 nginx 用的是什么镜像？", genai.RoleUser),
		},
	}
	if err := service.AppendEvent(ctx, created.Session, secondTurn); err != nil {
		t.Fatal(err)
	}

	got, err := service.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session.Events().Len() != 2 {
		t.Fatalf("events=%d, want 2", got.Session.Events().Len())
	}
	response := got.Session.Events().At(0).Content.Parts[0].FunctionResponse
	if response == nil {
		t.Fatal("previous tool response was not restored")
	}
	if response.Name != "docker_ps" {
		t.Fatalf("tool response name=%q, want docker_ps", response.Name)
	}
	containers, ok := response.Response["containers"].([]any)
	if !ok || len(containers) != 2 {
		t.Fatalf("tool response containers=%#v, want two persisted entries", response.Response["containers"])
	}
}

func TestStructuredSecretsAreRedactedFromSessionAndHistory(t *testing.T) {
	service := requirePostgresService(t)
	ctx := context.Background()
	workspaceID := uuid.New()
	cardID := uuid.New()
	appName, userID, sessionID := Identity(workspaceID, cardID)

	created, err := service.Create(ctx, &session.CreateRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	event := &session.Event{
		ID: "sanitized-event", InvocationID: "sanitize-invocation",
		Author: "cycle_agent", Timestamp: time.Now().UTC(),
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromParts([]*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						ID: "call-1", Name: "remote_tool",
						Args: map[string]any{
							"access_token": "ACCESS_SECRET_123",
							"resource":     "server-a",
						},
					},
					ThoughtSignature: []byte("signature-on-call"),
				},
				genai.NewPartFromFunctionResponse("remote_tool", map[string]any{
					"password": "PASSWORD_SECRET_456",
					"value":    "precise-evidence-789",
				}),
			}, genai.RoleModel),
		},
	}
	if err := service.AppendEvent(ctx, created.Session, event); err != nil {
		t.Fatal(err)
	}

	got, err := service.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := got.Session.Events().At(0).Content.Parts
	if len(parts) != 2 {
		t.Fatalf("persisted parts=%d, want 2", len(parts))
	}
	if len(parts[0].ThoughtSignature) != 0 {
		t.Fatalf("thought signature persisted: %q", parts[0].ThoughtSignature)
	}
	if parts[0].FunctionCall.Args["access_token"] != "[REDACTED]" ||
		parts[0].FunctionCall.Args["resource"] != "server-a" {
		t.Fatalf("function call args=%#v", parts[0].FunctionCall.Args)
	}
	response := parts[1].FunctionResponse.Response
	if response["password"] != "[REDACTED]" ||
		response["value"] != "precise-evidence-789" {
		t.Fatalf("function response=%#v", response)
	}

	var persistedText string
	if err := integrationPool.QueryRow(ctx, `
		SELECT event::text
		FROM agent_session_events
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3 AND id=$4
	`, appName, userID, sessionID, event.ID).Scan(&persistedText); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"signature-on-call", "ACCESS_SECRET_123", "PASSWORD_SECRET_456",
	} {
		if strings.Contains(persistedText, secret) {
			t.Fatalf("persisted event leaked %q: %s", secret, persistedText)
		}
	}

	hits, err := service.SearchHistory(ctx, workspaceID, cardID, "precise-evidence-789", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Content, "precise-evidence-789") {
		t.Fatalf("history hits=%+v", hits)
	}
	secretHits, err := service.SearchHistory(ctx, workspaceID, cardID, "ACCESS_SECRET_123", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(secretHits) != 0 {
		t.Fatalf("history exposed secret: %+v", secretHits)
	}
}

func TestAttemptOwnershipBindsInvocationAndRejectsStaleWriter(t *testing.T) {
	service := requirePostgresService(t)
	ctx := context.Background()
	dataStore := store.New(integrationPool)

	workspace, err := dataStore.CreateWorkspace(ctx, "Session guard workspace")
	if err != nil {
		t.Fatal(err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatal(err)
	}
	agentSource, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindCycleAgent, "Cycle Agent")
	if err != nil {
		t.Fatal(err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Guard project", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateTodoCard(
		ctx, workspace.ID, project.ID, human.ID,
		"Guarded card", "", uuid.NullUUID{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateHumanTurn(
		ctx, workspace.ID, card.ID, human.ID,
		"inspect the environment", "guard-turn", domain.DispatchModeEnqueue,
	); err != nil {
		t.Fatal(err)
	}
	first, err := dataStore.ClaimNextTurnAttempt(
		ctx, workspace.ID, agentSource.ID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}

	appName, userID, sessionID := Identity(workspace.ID, card.ID)
	created, err := service.Create(ctx, &session.CreateRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}

	firstEvent := &session.Event{
		ID: "guard-event-1", InvocationID: "adk-invocation-1",
		Author: "user", Timestamp: time.Now().UTC(),
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromText("inspect the environment", genai.RoleUser),
		},
	}
	firstCtx := WithAttempt(ctx, first.Attempt.ID)
	if err := service.AppendEvent(firstCtx, created.Session, firstEvent); err != nil {
		t.Fatal(err)
	}

	bound, err := dataStore.GetAttempt(ctx, workspace.ID, first.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bound.InvocationID != "adk-invocation-1" {
		t.Fatalf("bound invocation=%q, want adk-invocation-1", bound.InvocationID)
	}

	compactionEvent := &session.Event{
		ID: "guard-compaction-1", InvocationID: "adk-compaction-bookkeeping",
		Author: "user", Timestamp: time.Now().UTC().Add(time.Microsecond),
		Actions: session.EventActions{
			Compaction: &session.EventCompaction{
				StartTimestamp: firstEvent.Timestamp,
				EndTimestamp:   firstEvent.Timestamp,
				CompactedContent: genai.NewContentFromText(
					"Earlier context asked to inspect the environment.",
					genai.RoleModel,
				),
			},
		},
	}
	if err := service.AppendEvent(firstCtx, created.Session, compactionEvent); err != nil {
		t.Fatalf("valid compaction bookkeeping append: %v", err)
	}

	wrongInvocation := &session.Event{
		ID: "guard-wrong-invocation", InvocationID: "adk-invocation-other",
		Author: "cycle_agent", Timestamp: time.Now().UTC().Add(2 * time.Microsecond),
	}
	if err := service.AppendEvent(firstCtx, created.Session, wrongInvocation); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("wrong invocation append error=%v, want ErrAttemptNotCurrent", err)
	}

	if err := dataStore.FinishAttempt(
		ctx, workspace.ID, card.ID, first.Attempt.ID,
		domain.AttemptStatusFailed, "retryable failure", time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	lateCompaction := &session.Event{
		ID: "guard-late-compaction", InvocationID: "another-bookkeeping-id",
		Author: "user", Timestamp: time.Now().UTC().Add(3 * time.Microsecond),
		Actions: session.EventActions{
			Compaction: &session.EventCompaction{
				StartTimestamp: firstEvent.Timestamp,
				EndTimestamp:   firstEvent.Timestamp,
				CompactedContent: genai.NewContentFromText(
					"Late summary must not be stored after terminal Attempt.",
					genai.RoleModel,
				),
			},
		},
	}
	if err := service.AppendEvent(firstCtx, created.Session, lateCompaction); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("late compaction append error=%v, want ErrAttemptNotCurrent", err)
	}
	retry, err := dataStore.RetryAttempt(
		ctx, workspace.ID, card.ID, first.Attempt.ID, agentSource.ID,
		domain.AttemptReasonRetry,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := dataStore.ClaimNextTurnAttempt(
		ctx, workspace.ID, agentSource.ID, time.Now().UTC(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Attempt.ID != retry.Attempt.ID {
		t.Fatalf("claimed retry=%s want=%s", second.Attempt.ID, retry.Attempt.ID)
	}

	lateOldEvent := &session.Event{
		ID: "guard-late-old", InvocationID: "adk-invocation-1",
		Author: "cycle_agent", Timestamp: time.Now().UTC().Add(4 * time.Microsecond),
	}
	if err := service.AppendEvent(firstCtx, created.Session, lateOldEvent); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("late old append error=%v, want ErrAttemptNotCurrent", err)
	}

	secondCtx := WithAttempt(ctx, second.Attempt.ID)
	secondEvent := &session.Event{
		ID: "guard-event-2", InvocationID: "adk-invocation-2",
		Author: "cycle_agent", Timestamp: time.Now().UTC().Add(5 * time.Microsecond),
	}
	if err := service.AppendEvent(secondCtx, created.Session, secondEvent); err != nil {
		t.Fatal(err)
	}

	got, err := service.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session.Events().Len() != 3 {
		t.Fatalf("events=%d, want only accepted events", got.Session.Events().Len())
	}
	if got.Session.Events().At(0).ID != "guard-event-1" ||
		got.Session.Events().At(1).ID != "guard-compaction-1" ||
		got.Session.Events().At(2).ID != "guard-event-2" {
		t.Fatalf(
			"persisted events=%s,%s,%s",
			got.Session.Events().At(0).ID,
			got.Session.Events().At(1).ID,
			got.Session.Events().At(2).ID,
		)
	}
}

func TestConcurrentAppendOnSameSession(t *testing.T) {
	service := requirePostgresService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, &session.CreateRequest{
		AppName: AppName, UserID: uuid.NewString(), SessionID: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	timestamp := time.Now().UTC()
	for _, id := range []string{"parallel-1", "parallel-2"} {
		id := id
		go func() {
			<-start
			results <- service.AppendEvent(ctx, created.Session, &session.Event{
				ID: id, Author: "cycle_agent", InvocationID: "parallel", Timestamp: timestamp,
			})
		}()
	}
	close(start)

	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent AppendEvent: %v", err)
		}
	}

	got, err := service.Get(ctx, &session.GetRequest{
		AppName:   created.Session.AppName(),
		UserID:    created.Session.UserID(),
		SessionID: created.Session.ID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session.Events().Len() != 2 {
		t.Fatalf("events=%d, want both concurrent appends", got.Session.Events().Len())
	}
}

func TestThoughtPartsAreNotPersisted(t *testing.T) {
	service := requirePostgresService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, &session.CreateRequest{
		AppName: AppName, UserID: uuid.NewString(), SessionID: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	event := &session.Event{
		ID:        "model-response",
		Author:    "cycle_agent",
		Timestamp: time.Now().UTC(),
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{Text: "private reasoning", Thought: true},
					{Text: "user-visible answer"},
				},
			},
		},
	}
	if err := service.AppendEvent(ctx, created.Session, event); err != nil {
		t.Fatal(err)
	}

	got, err := service.Get(ctx, &session.GetRequest{
		AppName:   created.Session.AppName(),
		UserID:    created.Session.UserID(),
		SessionID: created.Session.ID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := got.Session.Events().At(0).Content.Parts
	if len(parts) != 1 || parts[0].Text != "user-visible answer" {
		t.Fatalf("persisted parts=%#v, want only user-visible content", parts)
	}
}

func TestDeletedSessionRejectsLateEvent(t *testing.T) {
	service := requirePostgresService(t)
	ctx := context.Background()

	created, err := service.Create(ctx, &session.CreateRequest{
		AppName: AppName, UserID: uuid.NewString(), SessionID: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, &session.DeleteRequest{
		AppName:   created.Session.AppName(),
		UserID:    created.Session.UserID(),
		SessionID: created.Session.ID(),
	}); err != nil {
		t.Fatal(err)
	}

	err = service.AppendEvent(ctx, created.Session, &session.Event{
		Author: "cycle_agent", InvocationID: "late-invocation",
	})
	if !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("late AppendEvent error=%v, want session.ErrNotFound", err)
	}
}

func requirePostgresService(t *testing.T) *PostgresService {
	t.Helper()
	if integrationPool == nil {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	resetAgentSessions(t)
	return NewPostgresService(integrationPool)
}

func resetAgentSessions(t *testing.T) {
	t.Helper()
	if _, err := integrationPool.Exec(context.Background(), `
		TRUNCATE TABLE
			agent_session_events,
			agent_sessions,
			agent_user_state,
			agent_app_state
		RESTART IDENTITY CASCADE
	`); err != nil {
		t.Fatalf("reset agent sessions: %v", err)
	}
}
