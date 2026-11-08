package agentsession

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
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/sessiontestsuite"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

var integrationPool *pgxpool.Pool

func TestMain(m *testing.M) {
	os.Exit(runSessionTests(m))
}

func runSessionTests(m *testing.M) int {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		return m.Run()
	}

	adminPool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		panic(err)
	}
	defer adminPool.Close()
	// 集成测试必须在独立临时 schema 中执行迁移与清表，严禁污染或清空开发库 public schema。
	schema := "agentsession_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	integrationPool = pool

	return m.Run()
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
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Guard project", "")
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

func TestPostgresServiceNormalizesUnsupportedUnicodeWithoutCorruptingValidText(t *testing.T) {
	service := requirePostgresService(t)
	ctx := context.Background()
	workspaceID := uuid.New()
	cardID := uuid.New()
	appName, userID, sessionID := Identity(workspaceID, cardID)

	// 先验证真实 PostgreSQL jsonb 对未规范化的 \u0000 确实拒绝并返回 SQLSTATE 22P05。
	var rejected string
	rawNullJSONErr := integrationPool.QueryRow(ctx, `SELECT '{"stdout":"before\u0000after"}'::jsonb::text`).Scan(&rejected)
	if rawNullJSONErr == nil || !strings.Contains(rawNullJSONErr.Error(), "22P05") {
		t.Fatalf("expected PostgreSQL jsonb to reject \\u0000 with SQLSTATE 22P05, got: %v", rawNullJSONErr)
	}

	created, err := service.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		State: map[string]any{
			"init_state":         "正常初始化\x00状态🚀",
			"app:global_marker":  "global\x00marker",
			"user:scoped_marker": "user\x00" + string([]byte{0xff}) + "marker",
		},
	})
	if err != nil {
		t.Fatalf("Create session with abnormal unicode state: %v", err)
	}

	largeChunk := strings.Repeat("日志输出 line with 中文 and 🚀\t", 600)
	invalidUTF8 := string([]byte{0xff, 0xfe})
	loneSurrogateUTF8 := string([]byte{0xed, 0xa0, 0x80})

	toolEvent := &session.Event{
		ID:           "unicode-tool-event",
		InvocationID: "unicode-invocation-1",
		Author:       "cycle_agent",
		Timestamp:    time.Now().UTC(),
		Actions: session.EventActions{
			StateDelta: map[string]any{
				"last_tool\x00output": "state\x00value\n\t中文✅",
			},
		},
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromParts([]*genai.Part{
				{
					Text: "模型说明：执行命令检查日志 🚀\n\t第一行\x00第二行" + invalidUTF8 + `保留字面量 \u0000`,
				},
				{
					FunctionCall: &genai.FunctionCall{
						ID:   "exec-call-1",
						Name: "exec_command",
						Args: map[string]any{
							"cmd": "printf 'hello\\0world' && echo '中文🚀'",
						},
					},
				},
				genai.NewPartFromFunctionResponse("exec_command", map[string]any{
					"stdout":        "stdout-prefix\x00stdout-suffix\n\t中文🚀",
					"stderr":        "stderr-prefix\x00" + invalidUTF8 + loneSurrogateUTF8 + "stderr-suffix",
					"literal_check": `literal \u0000 should stay literal`,
					"nested_json":   `{"content":[{"type":"text","text":"nested\u0000stdout\n\t中文🚀"}],"access_token":"secret-123"}`,
					"large_stdout":  largeChunk + "\x00tail-marker",
				}),
			}, genai.RoleModel),
		},
	}

	if err := service.AppendEvent(ctx, created.Session, toolEvent); err != nil {
		t.Fatalf("AppendEvent with NUL and invalid UTF-8 failed: %v", err)
	}

	got, err := service.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Get session after abnormal unicode append: %v", err)
	}
	if got.Session.Events().Len() != 1 {
		t.Fatalf("events=%d, want 1", got.Session.Events().Len())
	}

	persisted := got.Session.Events().At(0)
	if len(persisted.Content.Parts) != 3 {
		t.Fatalf("parts=%d, want 3", len(persisted.Content.Parts))
	}

	textPart := persisted.Content.Parts[0].Text
	wantText := "模型说明：执行命令检查日志 🚀\n\t第一行\uFFFD第二行\uFFFD" + `保留字面量 \u0000`
	if textPart != wantText {
		t.Fatalf("text part=%q, want %q", textPart, wantText)
	}

	respMap := persisted.Content.Parts[2].FunctionResponse.Response
	if respMap["stdout"] != "stdout-prefix\uFFFDstdout-suffix\n\t中文🚀" {
		t.Fatalf("stdout=%q", respMap["stdout"])
	}
	if respMap["stderr"] != "stderr-prefix\uFFFD\uFFFDstderr-suffix" {
		t.Fatalf("stderr=%q", respMap["stderr"])
	}
	if respMap["literal_check"] != `literal \u0000 should stay literal` {
		t.Fatalf("literal_check=%q", respMap["literal_check"])
	}
	if !strings.Contains(respMap["nested_json"].(string), "nested\uFFFDstdout") ||
		!strings.Contains(respMap["nested_json"].(string), "[REDACTED]") {
		t.Fatalf("nested_json=%q", respMap["nested_json"])
	}
	if respMap["large_stdout"] != largeChunk+"\uFFFDtail-marker" {
		t.Fatalf("large_stdout mismatch")
	}

	stateVal, err := got.Session.State().Get("last_tool\uFFFDoutput")
	if err != nil || stateVal != "state\uFFFDvalue\n\t中文✅" {
		t.Fatalf("state last_tool output=%v (err=%v)", stateVal, err)
	}

	hits, err := service.SearchHistory(ctx, workspaceID, cardID, "tail-marker", 4)
	if err != nil {
		t.Fatalf("SearchHistory after abnormal unicode append: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("SearchHistory hits=%d, want 1", len(hits))
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
