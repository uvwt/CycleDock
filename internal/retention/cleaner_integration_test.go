package retention

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentartifact"
	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/sqldb"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/testdb"
)

var retentionIntegrationPool *sqldb.DB

func TestMain(m *testing.M) {
	os.Exit(runRetentionTests(m))
}

func runRetentionTests(m *testing.M) int {
	pool, cleanup, err := testdb.SetupPackagePool("retention")
	if err != nil {
		panic(err)
	}
	defer cleanup()
	retentionIntegrationPool = pool

	return m.Run()
}

type retentionFixture struct {
	t               *testing.T
	pool            *sqldb.DB
	dataStore       *store.Store
	sessionService  *agentsession.PostgresService
	artifactService *agentartifact.PostgresService
	workspaceID     uuid.UUID
	projectID       uuid.UUID
	humanSourceID   uuid.UUID
	agentSourceID   uuid.UUID
	mainCardID      uuid.UUID
}

func newRetentionFixture(t *testing.T) *retentionFixture {
	t.Helper()
	if retentionIntegrationPool == nil {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	testdb.TruncateAll(t, retentionIntegrationPool)
	dataStore := store.New(retentionIntegrationPool)
	workspace, err := dataStore.CreateWorkspace(ctx, "Retention Test Workspace")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	human, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindHuman, "Human")
	if err != nil {
		t.Fatalf("create human source: %v", err)
	}
	agentSource, err := dataStore.CreateSource(ctx, workspace.ID, domain.SourceKindCycleAgent, "Cycle Agent")
	if err != nil {
		t.Fatalf("create agent source: %v", err)
	}
	project, err := dataStore.CreateProject(ctx, workspace.ID, "Retention Test Project", "")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	f := &retentionFixture{
		t:               t,
		pool:            retentionIntegrationPool,
		dataStore:       dataStore,
		sessionService:  agentsession.NewPostgresService(retentionIntegrationPool),
		artifactService: agentartifact.NewPostgresService(retentionIntegrationPool),
		workspaceID:     workspace.ID,
		projectID:       project.ID,
		humanSourceID:   human.ID,
		agentSourceID:   agentSource.ID,
		mainCardID:      project.MainDiscussionID.UUID,
	}
	return f
}

func (f *retentionFixture) insertCard(kind domain.CardKind, state string, updatedAt time.Time) uuid.UUID {
	f.t.Helper()
	ctx := context.Background()
	cardID := uuid.New()
	var todoState, ongoingState, discussionState any
	switch kind {
	case domain.CardKindTodo:
		todoState = state
	case domain.CardKindOngoing:
		ongoingState = state
	case domain.CardKindDiscussion:
		discussionState = state
	default:
		f.t.Fatalf("unsupported card kind: %s", kind)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO cards (
			id, workspace_id, project_id, kind, title, description,
			todo_state, ongoing_state, discussion_state,
			created_by_source_id, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, '', $6, $7, $8, $9, $10, $10)
	`, cardID, f.workspaceID, f.projectID, string(kind), "card-"+cardID.String()[:8],
		todoState, ongoingState, discussionState, f.humanSourceID, updatedAt.UTC()); err != nil {
		f.t.Fatalf("insert card: %v", err)
	}
	appName, userID, sessionID := agentsession.Identity(f.workspaceID, cardID)
	if _, err := f.sessionService.Create(ctx, &session.CreateRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	}); err != nil {
		f.t.Fatalf("create agent session: %v", err)
	}
	return cardID
}

func (f *retentionFixture) insertBusinessRecords(cardID uuid.UUID, createdAt time.Time) (uuid.UUID, uuid.UUID) {
	f.t.Helper()
	ctx := context.Background()
	turn, err := f.dataStore.CreateHumanTurn(
		ctx, f.workspaceID, cardID, f.humanSourceID,
		"hello", "turn-"+uuid.NewString(), domain.DispatchModeEnqueue,
	)
	if err != nil {
		f.t.Fatalf("create human turn: %v", err)
	}
	claimed, err := f.dataStore.ClaimNextTurnAttempt(
		ctx, f.workspaceID, f.agentSourceID, createdAt.UTC(), time.Minute,
	)
	if err != nil {
		f.t.Fatalf("claim attempt: %v", err)
	}
	if _, _, err := f.dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    f.workspaceID,
		CardID:         cardID,
		AttemptID:      claimed.Attempt.ID,
		RunnerSourceID: f.agentSourceID,
		IdempotencyKey: "reply-" + uuid.NewString(),
		Message:        store.ReplyMessage{Type: "text", Content: "done", EndTurn: true},
	}); err != nil {
		f.t.Fatalf("publish reply from attempt: %v", err)
	}
	if err := f.dataStore.FinishAttempt(
		ctx, f.workspaceID, cardID, claimed.Attempt.ID,
		domain.AttemptStatusCompleted, "", createdAt.UTC(),
	); err != nil {
		f.t.Fatalf("finish attempt: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO agent_model_calls (
			id, workspace_id, card_id, turn_id, attempt_id, invocation_id,
			call_index, purpose, model_id, provider,
			started_at, finished_at, duration_ms, status,
			input_tokens, output_tokens, usage_source, created_at
		) VALUES ($1, $2, $3, $4, $5, 'inv-test', 1, 'agent', 'gpt-test', 'openai', $6, $6, 200, 'success', 100, 50, 'actual', $6)
	`, uuid.New(), f.workspaceID, cardID, turn.Turn.ID, claimed.Attempt.ID, createdAt.UTC()); err != nil {
		f.t.Fatalf("insert model call: %v", err)
	}
	return turn.Turn.ID, claimed.Attempt.ID
}

func (f *retentionFixture) insertRawEvent(cardID uuid.UUID, eventID, invocationID, author, text string, occurredAt time.Time) {
	f.t.Helper()
	ev := session.NewEvent(context.Background(), invocationID)
	ev.ID = eventID
	ev.Author = author
	ev.Timestamp = occurredAt.UTC()
	ev.Content = genai.NewContentFromText(text, genai.RoleUser)
	f.insertSessionEventRow(cardID, ev)
}

func (f *retentionFixture) insertCompactionEvent(cardID uuid.UUID, eventID string, startTS, endTS, occurredAt time.Time, summary string) {
	f.t.Helper()
	ev := session.NewEvent(context.Background(), "compaction-inv-"+eventID)
	ev.ID = eventID
	ev.Author = "user"
	ev.Timestamp = occurredAt.UTC()
	ev.Actions.Compaction = &session.EventCompaction{
		StartTimestamp:   startTS.UTC(),
		EndTimestamp:     endTS.UTC(),
		CompactedContent: genai.NewContentFromText(summary, genai.RoleModel),
	}
	f.insertSessionEventRow(cardID, ev)
}

func (f *retentionFixture) insertSessionEventRow(cardID uuid.UUID, ev *session.Event) {
	f.t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(ev)
	if err != nil {
		f.t.Fatalf("marshal event: %v", err)
	}
	appName, userID, sessionID := agentsession.Identity(f.workspaceID, cardID)
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO agent_session_events (app_name, user_id, session_id, id, invocation_id, occurred_at, event)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
	`, appName, userID, sessionID, ev.ID, ev.InvocationID, ev.Timestamp.UTC(), raw); err != nil {
		f.t.Fatalf("insert agent_session_events: %v", err)
	}
}

func (f *retentionFixture) insertArtifact(cardID uuid.UUID, fileName, mimeType string, payload []byte, createdAt time.Time) int64 {
	f.t.Helper()
	ctx := context.Background()
	appName, userID, sessionID := agentsession.Identity(f.workspaceID, cardID)
	saved, err := f.artifactService.Save(ctx, &artifact.SaveRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
		FileName: fileName,
		Part:     genai.NewPartFromBytes(payload, mimeType),
	})
	if err != nil {
		f.t.Fatalf("save artifact %s: %v", fileName, err)
	}
	if _, err := f.pool.Exec(ctx, `
		UPDATE agent_artifacts
		SET created_at = $5
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3 AND file_name = $4 AND version = $6
	`, appName, userID, sessionID, fileName, createdAt.UTC(), saved.Version); err != nil {
		f.t.Fatalf("backdate artifact %s: %v", fileName, err)
	}
	return saved.Version
}

func (f *retentionFixture) sessionEventIDs(cardID uuid.UUID) []string {
	f.t.Helper()
	ctx := context.Background()
	appName, userID, sessionID := agentsession.Identity(f.workspaceID, cardID)
	rows, err := f.pool.Query(ctx, `
		SELECT id FROM agent_session_events
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		ORDER BY sequence ASC
	`, appName, userID, sessionID)
	if err != nil {
		f.t.Fatalf("query session events: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			f.t.Fatalf("scan event id: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func (f *retentionFixture) artifactFileNames(cardID uuid.UUID) []string {
	f.t.Helper()
	ctx := context.Background()
	appName, userID, sessionID := agentsession.Identity(f.workspaceID, cardID)
	rows, err := f.pool.Query(ctx, `
		SELECT file_name FROM agent_artifacts
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		ORDER BY file_name ASC
	`, appName, userID, sessionID)
	if err != nil {
		f.t.Fatalf("query artifacts: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			f.t.Fatalf("scan artifact file_name: %v", err)
		}
		names = append(names, name)
	}
	return names
}

func TestTieredRetentionCleanupAndContinuity(t *testing.T) {
	f := newRetentionFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	// 1. 活跃卡（todo/working）：两轮 compaction + 被覆盖的老事件 + 保留窗口内事件 + 尾部事件 + 图片/非图片产物。
	activeCard := f.insertCard(domain.CardKindTodo, "working", now.Add(-1*time.Hour))
	f.insertRawEvent(activeCard, "act-old-1", "inv-1", "user", "legacy secret token 111", now.Add(-20*24*time.Hour))
	f.insertCompactionEvent(activeCard, "act-comp-old", now.Add(-20*24*time.Hour), now.Add(-19*24*time.Hour), now.Add(-19*24*time.Hour), "older summary")
	f.insertRawEvent(activeCard, "act-old-2", "inv-2", "user", "covered old event 222", now.Add(-12*24*time.Hour))
	// 下面这条虽然也被最新 compaction 覆盖，但发生时间仍在 7 天保留窗口内（5 天前），必须保留。
	f.insertRawEvent(activeCard, "act-recent-covered", "inv-3", "user", "recent covered event", now.Add(-5*24*time.Hour))
	f.insertCompactionEvent(activeCard, "act-comp-latest", now.Add(-20*24*time.Hour), now.Add(-4*24*time.Hour), now.Add(-4*24*time.Hour), "latest active summary")
	// 严格晚于最新 compaction 的尾部事件，必须保留。
	f.insertRawEvent(activeCard, "act-tail-1", "inv-4", "user", "tail event after compaction", now.Add(-2*24*time.Hour))
	f.insertRawEvent(activeCard, "act-tail-2", "inv-4", "cycle_agent", "tail reply", now.Add(-2*24*time.Hour+time.Minute))

	f.insertArtifact(activeCard, "active-old-tool.json", "application/json", []byte(`{"old":true}`), now.Add(-15*24*time.Hour))
	f.insertArtifact(activeCard, "active-old-image.jpg", "image/jpeg", []byte("jpeg-bytes"), now.Add(-15*24*time.Hour))
	f.insertArtifact(activeCard, "active-recent-tool.json", "application/json", []byte(`{"recent":true}`), now.Add(-5*24*time.Hour))
	f.insertArtifact(activeCard, "active-tail-tool.json", "application/json", []byte(`{"tail":true}`), now.Add(-2*24*time.Hour))

	// 2. 已完结卡片（todo/done，15 天前更新）：应清理过期非图片产物和除最新 compaction 外的过期事件，但保留图片和业务表。
	// 先以 working 状态生成真实 Turn/Attempt/Event/ModelCall 记录，再将卡片置为 done 并回拨 updated_at。
	doneCard := f.insertCard(domain.CardKindTodo, "working", now.Add(-15*24*time.Hour))
	f.insertBusinessRecords(doneCard, now.Add(-15*24*time.Hour))
	if _, err := f.pool.Exec(ctx, `
		UPDATE cards SET todo_state = 'done', updated_at = $2 WHERE id = $1
	`, doneCard, now.Add(-15*24*time.Hour).UTC()); err != nil {
		t.Fatalf("mark done card completed: %v", err)
	}
	f.insertRawEvent(doneCard, "done-old-1", "inv-d1", "user", "done card detail", now.Add(-16*24*time.Hour))
	f.insertCompactionEvent(doneCard, "done-comp-latest", now.Add(-16*24*time.Hour), now.Add(-15*24*time.Hour), now.Add(-15*24*time.Hour), "done card summary")
	f.insertRawEvent(doneCard, "done-old-2", "inv-d2", "cycle_agent", "done card tail", now.Add(-14*24*time.Hour))
	f.insertArtifact(doneCard, "done-old-tool.json", "application/json", []byte(`{"done":true}`), now.Add(-15*24*time.Hour))
	f.insertArtifact(doneCard, "done-old-image.png", "image/png", []byte("png-bytes"), now.Add(-15*24*time.Hour))

	// 3. 刚完结不久的卡片（todo/done，3 天前更新，在 7 天保护期内）：不应清理。
	recentDoneCard := f.insertCard(domain.CardKindTodo, "done", now.Add(-3*24*time.Hour))
	f.insertRawEvent(recentDoneCard, "recent-done-1", "inv-rd1", "user", "recent done", now.Add(-10*24*time.Hour))
	f.insertArtifact(recentDoneCard, "recent-done-tool.json", "application/json", []byte(`{"keep":true}`), now.Add(-10*24*time.Hour))

	// 4. 正在运行 Attempt 的活跃卡：本轮必须整体跳过，避免和实时写入竞争。
	runningCard := f.insertCard(domain.CardKindOngoing, "active", now.Add(-1*time.Hour))
	if _, err := f.dataStore.CreateHumanTurn(
		ctx, f.workspaceID, runningCard, f.humanSourceID,
		"running", "running-turn", domain.DispatchModeEnqueue,
	); err != nil {
		t.Fatalf("create running turn: %v", err)
	}
	if _, err := f.dataStore.ClaimNextTurnAttempt(
		ctx, f.workspaceID, f.agentSourceID, now.Add(-1*time.Minute), time.Minute,
	); err != nil {
		t.Fatalf("claim running attempt: %v", err)
	}
	f.insertRawEvent(runningCard, "run-old-1", "inv-r1", "user", "old running", now.Add(-20*24*time.Hour))
	f.insertCompactionEvent(runningCard, "run-comp", now.Add(-20*24*time.Hour), now.Add(-10*24*time.Hour), now.Add(-10*24*time.Hour), "running summary")
	f.insertArtifact(runningCard, "run-old-tool.json", "application/json", []byte(`{"skip":true}`), now.Add(-15*24*time.Hour))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// 先执行 dry-run，验证只统计不删除。
	dryCleaner := NewCleaner(f.pool, Config{
		Enabled:       true,
		DryRun:        true,
		RetentionDays: 7,
		BatchSize:     2,
		MaxBatches:    50,
	}, logger)
	dryCleaner.now = func() time.Time { return now }

	dryStats, err := dryCleaner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if dryStats.ClosedArtifactsDeleted != 1 || dryStats.ClosedSessionEventsDeleted != 2 ||
		dryStats.ActiveArtifactsDeleted != 1 || dryStats.ActiveSessionEventsDeleted != 3 {
		t.Fatalf("unexpected dry-run stats: %+v", dryStats)
	}
	if got := len(f.sessionEventIDs(activeCard)); got != 7 {
		t.Fatalf("dry-run mutated active card events: got %d want 7", got)
	}

	// 再执行真实分批清理（故意将 BatchSize 设为 2，覆盖多批次循环逻辑）。
	realCleaner := NewCleaner(f.pool, Config{
		Enabled:       true,
		DryRun:        false,
		RetentionDays: 7,
		BatchSize:     2,
		MaxBatches:    50,
	}, logger)
	realCleaner.now = func() time.Time { return now }

	stats, err := realCleaner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("real cleanup failed: %v", err)
	}
	if stats.ClosedArtifactsDeleted != 1 || stats.ClosedSessionEventsDeleted != 2 ||
		stats.ActiveArtifactsDeleted != 1 || stats.ActiveSessionEventsDeleted != 3 {
		t.Fatalf("unexpected real cleanup stats: %+v", stats)
	}
	if stats.BatchesExecuted < 4 {
		t.Fatalf("expected multi-batch execution, got %d", stats.BatchesExecuted)
	}

	// 校验幂等性：连续再次执行应删除 0 条。
	secondStats, err := realCleaner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("second cleanup failed: %v", err)
	}
	if secondStats.TotalDeleted() != 0 {
		t.Fatalf("expected idempotent 0 deletions on second run, got %+v", secondStats)
	}

	// 校验活跃卡保留结果：保留最近 covered、最新 compaction、以及尾部事件；旧产物已清、图片与近期产物保留。
	gotActiveEvents := f.sessionEventIDs(activeCard)
	wantActiveEvents := []string{"act-recent-covered", "act-comp-latest", "act-tail-1", "act-tail-2"}
	if fmt.Sprint(gotActiveEvents) != fmt.Sprint(wantActiveEvents) {
		t.Fatalf("active events = %v, want %v", gotActiveEvents, wantActiveEvents)
	}
	gotActiveArtifacts := f.artifactFileNames(activeCard)
	wantActiveArtifacts := []string{"active-old-image.jpg", "active-recent-tool.json", "active-tail-tool.json"}
	if fmt.Sprint(gotActiveArtifacts) != fmt.Sprint(wantActiveArtifacts) {
		t.Fatalf("active artifacts = %v, want %v", gotActiveArtifacts, wantActiveArtifacts)
	}

	// 校验已完结卡：仅留最新 compaction 摘要 + 图片产物；业务表（events / agent_model_calls / card_turns / turn_attempts）完好无损。
	if got := f.sessionEventIDs(doneCard); fmt.Sprint(got) != "[done-comp-latest]" {
		t.Fatalf("done card events = %v, want [done-comp-latest]", got)
	}
	if got := f.artifactFileNames(doneCard); fmt.Sprint(got) != "[done-old-image.png]" {
		t.Fatalf("done card artifacts = %v, want [done-old-image.png]", got)
	}
	var eventsCount, modelCallsCount int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE card_id = $1`, doneCard).Scan(&eventsCount); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM agent_model_calls WHERE card_id = $1`, doneCard).Scan(&modelCallsCount); err != nil {
		t.Fatal(err)
	}
	if eventsCount < 1 || modelCallsCount != 1 {
		t.Fatalf("business tables mutated: events=%d model_calls=%d", eventsCount, modelCallsCount)
	}

	// 校验保护期内完结卡和 running 卡完全不受影响。
	if got := len(f.sessionEventIDs(recentDoneCard)); got != 1 {
		t.Fatalf("recent done card events = %d, want 1", got)
	}
	if got := len(f.artifactFileNames(recentDoneCard)); got != 1 {
		t.Fatalf("recent done card artifacts = %d, want 1", got)
	}
	if got := len(f.sessionEventIDs(runningCard)); got != 2 {
		t.Fatalf("running card events = %d, want 2", got)
	}
	if got := len(f.artifactFileNames(runningCard)); got != 1 {
		t.Fatalf("running card artifacts = %d, want 1", got)
	}

	// 校验已清理 artifact 读取返回 ErrNotFound（可被 cycledock_read_artifact 映射为明确过期提示）。
	appName, userID, sessionID := agentsession.Identity(f.workspaceID, activeCard)
	if _, err := f.artifactService.Load(ctx, &artifact.LoadRequest{
		AppName: appName, UserID: userID, SessionID: sessionID, FileName: "active-old-tool.json",
	}); !errors.Is(err, agentartifact.ErrNotFound) {
		t.Fatalf("load pruned artifact err = %v, want errors.Is(_, agentartifact.ErrNotFound)", err)
	}

	// 校验 SearchHistory 和 InspectFullSession 在历史清理后优雅运行不报错。
	hits, err := f.sessionService.SearchHistory(ctx, f.workspaceID, activeCard, "legacy secret token", 5)
	if err != nil {
		t.Fatalf("SearchHistory after cleanup failed: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits for pruned event, got %d", len(hits))
	}
	rounds, err := f.sessionService.InspectFullSession(ctx, f.workspaceID, doneCard, 10)
	if err != nil {
		t.Fatalf("InspectFullSession after cleanup failed: %v", err)
	}
	if len(rounds) != 0 {
		t.Fatalf("expected 0 detailed rounds on pruned done card, got %d", len(rounds))
	}
}

func TestContinuityAndReCompactionAfterPruning(t *testing.T) {
	f := newRetentionFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	// 构造一种边界场景：最新 compaction 覆盖的所有原始事件都已经早于 7 天（会被全部删空），
	// 且在 compaction 之后有尾部事件；同时故意让一条尾部事件的 sequence 位于 compaction 之前（模拟异步/乱序落库），
	// 验证清理后 SessionService.Get 返回顺序正确，并且能继续追加新事件和再次触发 compaction（guardCompactionAppend 通过）。
	cardID := f.insertCard(domain.CardKindDiscussion, "active", now.Add(-1*time.Hour))
	f.insertRawEvent(cardID, "ev-1", "inv-1", "user", "ancient turn", now.Add(-20*24*time.Hour))
	f.insertRawEvent(cardID, "ev-tail", "inv-2", "user", "recent tail turn", now.Add(-2*24*time.Hour))
	f.insertCompactionEvent(cardID, "comp-1", now.Add(-20*24*time.Hour), now.Add(-15*24*time.Hour), now.Add(-15*24*time.Hour), "summary of ancient turns")

	cleaner := NewCleaner(f.pool, Config{
		Enabled:       true,
		RetentionDays: 7,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cleaner.now = func() time.Time { return now }

	stats, err := cleaner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if stats.ActiveSessionEventsDeleted != 1 {
		t.Fatalf("expected 1 pruned ancient event, got %+v", stats)
	}

	// 读取会话：alignPrunedCompactionEvents 必须把 comp-1 放在 ev-tail 之前。
	appName, userID, sessionID := agentsession.Identity(f.workspaceID, cardID)
	loaded, err := f.sessionService.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("session get failed: %v", err)
	}
	var loadedIDs []string
	for ev := range loaded.Session.Events().All() {
		loadedIDs = append(loadedIDs, ev.ID)
	}
	if fmt.Sprint(loadedIDs) != "[comp-1 ev-tail]" {
		t.Fatalf("loaded event order = %v, want [comp-1 ev-tail]", loadedIDs)
	}

	// 模拟新的一轮 running Attempt：续聊追加事件 + 再次写入新一轮 compaction（校验 guardCompactionAppend 不报错）。
	if _, err := f.dataStore.CreateHumanTurn(
		ctx, f.workspaceID, cardID, f.humanSourceID,
		"follow up", "follow-up-turn", domain.DispatchModeEnqueue,
	); err != nil {
		t.Fatalf("create follow-up turn: %v", err)
	}
	claimed, err := f.dataStore.ClaimNextTurnAttempt(
		ctx, f.workspaceID, f.agentSourceID, now, time.Minute,
	)
	if err != nil {
		t.Fatalf("claim follow-up attempt: %v", err)
	}
	attemptID := claimed.Attempt.ID

	attemptCtx := agentsession.WithAttempt(ctx, attemptID)
	followUpEv := session.NewEvent(ctx, "inv-followup")
	followUpEv.ID = "ev-followup"
	followUpEv.Author = "user"
	followUpEv.Timestamp = now
	followUpEv.Content = genai.NewContentFromText("follow up question", genai.RoleUser)
	if err := f.sessionService.AppendEvent(attemptCtx, loaded.Session, followUpEv); err != nil {
		t.Fatalf("append follow-up event after pruning: %v", err)
	}

	nextComp := session.NewEvent(ctx, "compaction-inv-2")
	nextComp.ID = "comp-2"
	nextComp.Author = "user"
	nextComp.Timestamp = now.Add(time.Second)
	nextComp.Actions.Compaction = &session.EventCompaction{
		StartTimestamp:   now.Add(-15 * 24 * time.Hour),
		EndTimestamp:     now,
		CompactedContent: genai.NewContentFromText("second compacted summary", genai.RoleModel),
	}
	if err := f.sessionService.AppendEvent(attemptCtx, loaded.Session, nextComp); err != nil {
		t.Fatalf("append second compaction after pruning (guardCompactionAppend): %v", err)
	}
}

func TestMainDiscussionProtectedAndAdvisoryLockSkipsOverlap(t *testing.T) {
	f := newRetentionFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	// 1. 项目主聊天永远按活跃卡策略处理：未产生 compaction 时不会因 updated_at 陈旧被当成归档卡误清；
	//    同时验证已归档的 ongoing/archived 与 discussion/archived 卡片在超过保留窗口后会被清理。
	if _, err := f.pool.Exec(ctx, `UPDATE cards SET updated_at = $2 WHERE id = $1`, f.mainCardID, now.Add(-30*24*time.Hour).UTC()); err != nil {
		t.Fatalf("backdate main card: %v", err)
	}
	appName, userID, mainSessionID := agentsession.Identity(f.workspaceID, f.mainCardID)
	if _, err := f.sessionService.Create(ctx, &session.CreateRequest{
		AppName: appName, UserID: userID, SessionID: mainSessionID,
	}); err != nil {
		t.Fatalf("create main session: %v", err)
	}
	f.insertRawEvent(f.mainCardID, "main-old-uncompacted", "inv-m1", "user", "keep me until compacted", now.Add(-20*24*time.Hour))
	f.insertArtifact(f.mainCardID, "main-old-uncompacted.json", "application/json", []byte(`{"keep":true}`), now.Add(-20*24*time.Hour))

	archivedOngoing := f.insertCard(domain.CardKindOngoing, "archived", now.Add(-15*24*time.Hour))
	f.insertRawEvent(archivedOngoing, "ong-old-1", "inv-o1", "user", "archived ongoing detail", now.Add(-16*24*time.Hour))
	f.insertArtifact(archivedOngoing, "ong-old.json", "application/json", []byte(`{"prune":true}`), now.Add(-16*24*time.Hour))

	archivedDiscussion := f.insertCard(domain.CardKindDiscussion, "archived", now.Add(-15*24*time.Hour))
	f.insertRawEvent(archivedDiscussion, "disc-old-1", "inv-ds1", "user", "archived discussion detail", now.Add(-16*24*time.Hour))
	f.insertArtifact(archivedDiscussion, "disc-old.json", "application/json", []byte(`{"prune":true}`), now.Add(-16*24*time.Hour))

	cleaner := NewCleaner(f.pool, Config{
		Enabled:       true,
		RetentionDays: 7,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cleaner.now = func() time.Time { return now }

	// 2. 验证并发保护：当另一协程已持有 session lock 时，本轮安全跳过而不报错。
	lockHeld := make(chan struct{})
	releaseLock := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		err := f.pool.WithSessionLock(ctx, advisoryLockKey, func(context.Context) error {
			close(lockHeld)
			<-releaseLock
			return nil
		})
		holderDone <- err
	}()
	<-lockHeld

	skipped, err := cleaner.RunOnce(ctx)
	if err != nil {
		close(releaseLock)
		t.Fatalf("RunOnce under lock failed: %v", err)
	}
	if !skipped.SkippedByLock || skipped.TotalDeleted() != 0 {
		close(releaseLock)
		t.Fatalf("expected skipped by lock, got %+v", skipped)
	}

	close(releaseLock)
	if err := <-holderDone; err != nil {
		t.Fatalf("release advisory lock: %v", err)
	}

	// 3. 锁释放后正常清理：主聊天未压缩数据保留，两个已归档卡片的过期事件与非图片产物全部清空。
	stats, err := cleaner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce after unlock failed: %v", err)
	}
	if stats.ClosedSessionEventsDeleted != 2 || stats.ClosedArtifactsDeleted != 2 {
		t.Fatalf("unexpected closed cleanup stats: %+v", stats)
	}
	if got := f.sessionEventIDs(f.mainCardID); fmt.Sprint(got) != "[main-old-uncompacted]" {
		t.Fatalf("main card events mutated: %v", got)
	}
	if got := f.artifactFileNames(f.mainCardID); fmt.Sprint(got) != "[main-old-uncompacted.json]" {
		t.Fatalf("main card artifacts mutated: %v", got)
	}
	if got := len(f.sessionEventIDs(archivedOngoing)); got != 0 {
		t.Fatalf("archived ongoing events not cleaned: %d", got)
	}
	if got := len(f.sessionEventIDs(archivedDiscussion)); got != 0 {
		t.Fatalf("archived discussion events not cleaned: %d", got)
	}
}

func TestEditSessionCommentAfterPruningDoesNotFail(t *testing.T) {
	f := newRetentionFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	// 构造活跃卡：先有一轮历史对话（含可见 comment 事件与旧的 agent_session_events + compaction），
	// 清理掉旧原始事件后，用户编辑该历史消息并回滚重跑，store.EditSessionCommentAndRerun 必须成功且不报错。
	cardID := f.insertCard(domain.CardKindDiscussion, "active", now.Add(-1*time.Hour))
	submitted, err := f.dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID:    f.workspaceID,
		CardID:         cardID,
		AuthorSourceID: f.humanSourceID,
		Content:        "old human prompt",
		IdempotencyKey: "edit-turn-1",
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatalf("submit session comment: %v", err)
	}
	claimed, err := f.dataStore.ClaimNextTurnAttempt(
		ctx, f.workspaceID, f.agentSourceID, now.Add(-15*24*time.Hour), time.Minute,
	)
	if err != nil {
		t.Fatalf("claim attempt: %v", err)
	}
	if _, _, err := f.dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    f.workspaceID,
		CardID:         cardID,
		AttemptID:      claimed.Attempt.ID,
		RunnerSourceID: f.agentSourceID,
		IdempotencyKey: "edit-reply-1",
		Message:        store.ReplyMessage{Type: "text", Content: "old reply", EndTurn: true},
	}); err != nil {
		t.Fatalf("publish reply: %v", err)
	}
	if err := f.dataStore.FinishAttempt(
		ctx, f.workspaceID, cardID, claimed.Attempt.ID,
		domain.AttemptStatusCompleted, "", now.Add(-15*24*time.Hour),
	); err != nil {
		t.Fatalf("finish attempt: %v", err)
	}
	// 绑定一个旧 invocation_id 并插入过期的 session events + compaction，然后执行清理。
	if _, err := f.pool.Exec(ctx, `UPDATE turn_attempts SET invocation_id = 'inv-old-edit' WHERE id = $1`, claimed.Attempt.ID); err != nil {
		t.Fatalf("bind invocation_id: %v", err)
	}
	f.insertRawEvent(cardID, "old-edit-ev", "inv-old-edit", "user", "old human prompt", now.Add(-15*24*time.Hour))
	f.insertCompactionEvent(cardID, "old-edit-comp", now.Add(-15*24*time.Hour), now.Add(-14*24*time.Hour), now.Add(-14*24*time.Hour), "old summary")

	cleaner := NewCleaner(f.pool, Config{
		Enabled:       true,
		RetentionDays: 7,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cleaner.now = func() time.Time { return now }

	stats, err := cleaner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("cleanup before edit failed: %v", err)
	}
	if stats.ActiveSessionEventsDeleted != 1 {
		t.Fatalf("expected 1 pruned event before edit, got %+v", stats)
	}

	if _, err := f.dataStore.EditSessionCommentAndRerun(ctx, store.EditSessionCommentParams{
		WorkspaceID:    f.workspaceID,
		CardID:         cardID,
		EventID:        submitted.EventID,
		AuthorSourceID: f.humanSourceID,
		Content:        "edited prompt after pruning",
	}); err != nil {
		t.Fatalf("EditSessionCommentAndRerun after pruning failed: %v", err)
	}
}
