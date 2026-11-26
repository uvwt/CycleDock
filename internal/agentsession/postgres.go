package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/sqldb"
)

const AppName = "cycledock"

type attemptContextKey struct{}

// WithAttempt binds an ADK Runner invocation to the CycleDock Attempt that owns
// the right to append to this Card Session. Runner propagates the caller context
// through SessionService.AppendEvent.
func WithAttempt(ctx context.Context, attemptID uuid.UUID) context.Context {
	return context.WithValue(ctx, attemptContextKey{}, attemptID)
}

func attemptFromContext(ctx context.Context) (uuid.UUID, bool) {
	attemptID, ok := ctx.Value(attemptContextKey{}).(uuid.UUID)
	return attemptID, ok && attemptID != uuid.Nil
}

// Identity maps CycleDock's product ownership directly onto ADK's session identity.
// A Card is the session; Workspace is the collaborative user scope.
func Identity(workspaceID, cardID uuid.UUID) (appName, userID, sessionID string) {
	return AppName, workspaceID.String(), cardID.String()
}

// Service 提供基于 sqldb 的双数据库（PostgreSQL / SQLite）ADK 会话持久化实现。
type Service struct {
	db *sqldb.DB
}

// PostgresService 为历史名称别名，便于既有调用方平滑过渡。
type PostgresService = Service

func NewService(db *sqldb.DB) *Service {
	return &Service{db: db}
}

func NewPostgresService(db *sqldb.DB) *Service {
	return NewService(db)
}

func (s *Service) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	if req == nil || strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" {
		return nil, errors.New("app_name and user_id are required")
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	appDelta, userDelta, sessionState := splitStateDelta(req.State)
	now := time.Now().UTC().Truncate(time.Microsecond)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create session: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := mergeScopedState(ctx, tx, "agent_app_state", req.AppName, "", appDelta, now); err != nil {
		return nil, err
	}
	if err := mergeScopedState(ctx, tx, "agent_user_state", req.AppName, req.UserID, userDelta, now); err != nil {
		return nil, err
	}

	rawState, _, err := agentdata.MarshalJSONB(agentdata.Sanitize(sessionState))
	if err != nil {
		return nil, fmt.Errorf("encode session state: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO agent_sessions(app_name, user_id, id, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
	`, req.AppName, req.UserID, sessionID, sqldb.JSON(rawState), now)
	if err != nil {
		return nil, fmt.Errorf("create session %q: %w", sessionID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create session: %w", err)
	}

	merged, err := s.loadMergedState(ctx, req.AppName, req.UserID, sessionState)
	if err != nil {
		return nil, err
	}
	return &session.CreateResponse{Session: newStoredSession(req.AppName, req.UserID, sessionID, merged, nil, now)}, nil
}

func (s *Service) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	if req == nil || strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.SessionID) == "" {
		return nil, errors.New("app_name, user_id, session_id are required")
	}

	var rawState []byte
	var updatedAt time.Time
	err := s.db.QueryRow(ctx, `
		SELECT state, updated_at
		FROM agent_sessions
		WHERE app_name=$1 AND user_id=$2 AND id=$3
	`, req.AppName, req.UserID, req.SessionID).Scan(&rawState, &updatedAt)
	if sqldb.IsNoRows(err) {
		return nil, fmt.Errorf("%w: %q", session.ErrNotFound, req.SessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("get session %q: %w", req.SessionID, err)
	}

	sessionState, err := decodeState(rawState)
	if err != nil {
		return nil, fmt.Errorf("decode session state: %w", err)
	}
	merged, err := s.loadMergedState(ctx, req.AppName, req.UserID, sessionState)
	if err != nil {
		return nil, err
	}
	events, err := s.loadEvents(ctx, req)
	if err != nil {
		return nil, err
	}

	return &session.GetResponse{
		Session: newStoredSession(req.AppName, req.UserID, req.SessionID, merged, events, updatedAt),
	}, nil
}

func (s *Service) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	if req == nil || strings.TrimSpace(req.AppName) == "" {
		return nil, errors.New("app_name is required")
	}

	query := `
		SELECT user_id, id, state, updated_at
		FROM agent_sessions
		WHERE app_name=$1`
	args := []any{req.AppName}
	if req.UserID != "" {
		query += ` AND user_id=$2`
		args = append(args, req.UserID)
	}
	query += ` ORDER BY updated_at DESC, id`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	out := make([]session.Session, 0)
	for rows.Next() {
		var userID, sessionID string
		var rawState []byte
		var updatedAt time.Time
		if err := rows.Scan(&userID, &sessionID, &rawState, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessionState, err := decodeState(rawState)
		if err != nil {
			return nil, fmt.Errorf("decode session %q state: %w", sessionID, err)
		}
		merged, err := s.loadMergedState(ctx, req.AppName, userID, sessionState)
		if err != nil {
			return nil, err
		}
		out = append(out, newStoredSession(req.AppName, userID, sessionID, merged, nil, updatedAt))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return &session.ListResponse{Sessions: out}, nil
}

func (s *Service) Delete(ctx context.Context, req *session.DeleteRequest) error {
	if req == nil || strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.SessionID) == "" {
		return errors.New("app_name, user_id, session_id are required")
	}
	if _, err := s.db.Exec(ctx, `
		DELETE FROM agent_sessions
		WHERE app_name=$1 AND user_id=$2 AND id=$3
	`, req.AppName, req.UserID, req.SessionID); err != nil {
		return fmt.Errorf("delete session %q: %w", req.SessionID, err)
	}
	return nil
}

func (s *Service) AppendEvent(ctx context.Context, cur session.Session, event *session.Event) error {
	if cur == nil {
		return errors.New("session is nil")
	}
	if event == nil {
		return errors.New("event is nil")
	}
	if event.Partial {
		return nil
	}
	local, ok := cur.(*storedSession)
	if !ok {
		return fmt.Errorf("unexpected session type %T", cur)
	}

	// ADK normally appends a session's events sequentially, but parallel agents
	// can produce concurrent callbacks. Serialize one in-memory session object
	// across the database transaction and local snapshot update so a sibling
	// append cannot observe the database commit before local.updatedAt advances.
	local.appendMu.Lock()
	defer local.appendMu.Unlock()

	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	event.Timestamp = event.Timestamp.UTC().Truncate(time.Microsecond)

	persistedEvent, eventNormalized := persistentEvent(event)
	rawEvent, eventJSONNormalized, err := agentdata.MarshalJSONB(persistedEvent)
	if err != nil {
		return fmt.Errorf("encode session event: %w", err)
	}
	appDelta, userDelta, sessionDelta := splitStateDelta(persistedEvent.Actions.StateDelta)
	rawSessionDelta, deltaJSONNormalized, err := agentdata.MarshalJSONB(sessionDelta)
	if err != nil {
		return fmt.Errorf("encode session state delta: %w", err)
	}
	if eventNormalized || eventJSONNormalized || deltaJSONNormalized {
		slog.Warn(
			"normalized unsupported unicode in session event before persistence",
			"session_id", local.id,
			"event_id", persistedEvent.ID,
			"invocation_id", persistedEvent.InvocationID,
			"author", persistedEvent.Author,
		)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin append event: %w", err)
	}
	defer tx.Rollback(ctx)

	if attemptID, guarded := attemptFromContext(ctx); guarded {
		var guardErr error
		if event.Actions.Compaction != nil {
			guardErr = guardCompactionAppend(ctx, tx, local, event, attemptID)
		} else {
			guardErr = guardAttemptAppend(ctx, tx, local, event, attemptID)
		}
		if guardErr != nil {
			return guardErr
		}
	}

	var storedStateRaw []byte
	var storedUpdated time.Time
	err = tx.QueryRow(ctx, `
		SELECT state, updated_at
		FROM agent_sessions
		WHERE app_name=$1 AND user_id=$2 AND id=$3`+tx.ForUpdate(),
		local.appName, local.userID, local.id).Scan(&storedStateRaw, &storedUpdated)
	if sqldb.IsNoRows(err) {
		return fmt.Errorf("%w: %q, cannot apply event", session.ErrNotFound, local.id)
	}
	if err != nil {
		return fmt.Errorf("lock session %q: %w", local.id, err)
	}
	localUpdated := local.LastUpdateTime().UTC().Truncate(time.Microsecond)
	if storedUpdated.UTC().Truncate(time.Microsecond).After(localUpdated) {
		return fmt.Errorf(
			"stale session: request last update %s is older than database %s",
			localUpdated.Format(time.RFC3339Nano),
			storedUpdated.Format(time.RFC3339Nano),
		)
	}

	if err := mergeScopedState(ctx, tx, "agent_app_state", local.appName, "", appDelta, event.Timestamp); err != nil {
		return err
	}
	if err := mergeScopedState(ctx, tx, "agent_user_state", local.appName, local.userID, userDelta, event.Timestamp); err != nil {
		return err
	}
	mergedSessionRaw := storedStateRaw
	if len(sessionDelta) > 0 {
		mergedSessionRaw, err = mergeJSONObjects(storedStateRaw, rawSessionDelta)
		if err != nil {
			return fmt.Errorf("merge session state: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_sessions
		SET state = $4, updated_at=$5
		WHERE app_name=$1 AND user_id=$2 AND id=$3
	`, local.appName, local.userID, local.id, sqldb.JSON(mergedSessionRaw), event.Timestamp); err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_session_events(
			app_name, user_id, session_id, id, invocation_id, occurred_at, event
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, local.appName, local.userID, local.id, persistedEvent.ID, persistedEvent.InvocationID, persistedEvent.Timestamp, sqldb.JSON(rawEvent)); err != nil {
		return fmt.Errorf("append session event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit append event: %w", err)
	}

	// 数据库持久化使用已脱敏的 persistedEvent；当前内存会话保留规范化后的原始工具返回，
	// 以便模型在同一轮调用（如刚生成一次性 webhook_secret 后立即调用 gh api 配置仓库）中读取。
	// 下一轮从数据库重新加载会话时仅看到已脱敏版本。
	liveEvent := liveSessionEvent(event, persistedEvent)
	local.append(event, liveEvent)
	return nil
}

func guardCompactionAppend(
	ctx context.Context,
	tx *sqldb.Tx,
	local *storedSession,
	event *session.Event,
	attemptID uuid.UUID,
) error {
	if local.appName != AppName {
		return fmt.Errorf("%w: guarded compaction requires CycleDock session", domain.ErrAttemptNotCurrent)
	}
	workspaceID, err := uuid.Parse(local.userID)
	if err != nil {
		return fmt.Errorf("%w: invalid workspace session identity", domain.ErrAttemptNotCurrent)
	}
	cardID, err := uuid.Parse(local.id)
	if err != nil {
		return fmt.Errorf("%w: invalid card session identity", domain.ErrAttemptNotCurrent)
	}
	record := event.Actions.Compaction
	if record == nil || event.Author != "user" ||
		record.StartTimestamp.IsZero() || record.EndTimestamp.IsZero() ||
		record.EndTimestamp.Before(record.StartTimestamp) ||
		!safeCompactionContent(record.CompactedContent) {
		return fmt.Errorf("%w: invalid compaction bookkeeping event", domain.ErrAttemptNotCurrent)
	}

	// Compaction is framework bookkeeping, not an Agent action. It must never
	// become the first write that claims an Attempt; a normal invocation event
	// binds invocation_id first. ADK gives summary records their own bookkeeping
	// invocation identity, so that identity is deliberately not compared with
	// the business Attempt invocation below.
	var status string
	var invocationID *string
	err = tx.QueryRow(ctx, `
		SELECT status, invocation_id
		FROM turn_attempts
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3`+tx.ForUpdate(),
		workspaceID, cardID, attemptID).Scan(&status, &invocationID)
	if sqldb.IsNoRows(err) {
		return domain.ErrAttemptNotCurrent
	}
	if err != nil {
		return fmt.Errorf("lock attempt for compaction append: %w", err)
	}
	if status != string(domain.AttemptStatusRunning) {
		return fmt.Errorf("%w: compaction requires running attempt", domain.ErrAttemptNotCurrent)
	}
	if invocationID == nil {
		return fmt.Errorf("%w: compaction requires bound invocation", domain.ErrAttemptNotCurrent)
	}

	// The range must cover evidence already persisted in this exact Card
	// Session. This prevents a synthetic bookkeeping event from introducing an
	// arbitrary summary range even if it somehow reaches the SessionService.
	var covered int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_session_events
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3
		  AND occurred_at >= $4 AND occurred_at <= $5
	`, local.appName, local.userID, local.id,
		record.StartTimestamp.UTC(), record.EndTimestamp.UTC()).Scan(&covered); err != nil {
		return fmt.Errorf("validate compaction range: %w", err)
	}
	if covered == 0 {
		return fmt.Errorf("%w: compaction range covers no persisted events", domain.ErrAttemptNotCurrent)
	}
	return nil
}

func safeCompactionContent(content *genai.Content) bool {
	if content == nil || len(content.Parts) == 0 {
		return false
	}
	hasText := false
	for _, part := range content.Parts {
		if part == nil || part.Thought || part.FunctionCall != nil || part.FunctionResponse != nil {
			return false
		}
		if len(part.ThoughtSignature) != 0 {
			return false
		}
		if strings.TrimSpace(part.Text) != "" {
			hasText = true
		}
	}
	return hasText
}

func guardAttemptAppend(
	ctx context.Context,
	tx *sqldb.Tx,
	local *storedSession,
	event *session.Event,
	attemptID uuid.UUID,
) error {
	if local.appName != AppName {
		return fmt.Errorf("%w: guarded append requires CycleDock session", domain.ErrAttemptNotCurrent)
	}
	workspaceID, err := uuid.Parse(local.userID)
	if err != nil {
		return fmt.Errorf("%w: invalid workspace session identity", domain.ErrAttemptNotCurrent)
	}
	cardID, err := uuid.Parse(local.id)
	if err != nil {
		return fmt.Errorf("%w: invalid card session identity", domain.ErrAttemptNotCurrent)
	}
	if strings.TrimSpace(event.InvocationID) == "" {
		return fmt.Errorf("%w: event has no invocation id", domain.ErrAttemptNotCurrent)
	}

	// Use the same lock order as interrupt/cancel: Card -> Attempt -> Session.
	// This makes the ownership check and Event persistence one atomic decision.
	var lockedCard uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM cards
		WHERE workspace_id=$1 AND id=$2`+tx.ForUpdate(),
		workspaceID, cardID).Scan(&lockedCard)
	if sqldb.IsNoRows(err) {
		return domain.ErrAttemptNotCurrent
	}
	if err != nil {
		return fmt.Errorf("lock card for session append: %w", err)
	}

	var status string
	var invocationID *string
	err = tx.QueryRow(ctx, `
		SELECT status, invocation_id
		FROM turn_attempts
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3`+tx.ForUpdate(),
		workspaceID, cardID, attemptID).Scan(&status, &invocationID)
	if sqldb.IsNoRows(err) {
		return domain.ErrAttemptNotCurrent
	}
	if err != nil {
		return fmt.Errorf("lock attempt for session append: %w", err)
	}
	if status != string(domain.AttemptStatusRunning) {
		return domain.ErrAttemptNotCurrent
	}

	if invocationID == nil {
		command, err := tx.Exec(ctx, `
			UPDATE turn_attempts
			SET invocation_id=$4, updated_at=$5
			WHERE workspace_id=$1 AND card_id=$2 AND id=$3
			  AND status='running'
			  AND invocation_id IS NULL
		`, workspaceID, cardID, attemptID, event.InvocationID, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("bind attempt invocation: %w", err)
		}
		if command.RowsAffected() != 1 {
			return domain.ErrAttemptNotCurrent
		}
		return nil
	}
	if *invocationID != event.InvocationID {
		return domain.ErrAttemptNotCurrent
	}
	return nil
}

func (s *Service) loadEvents(ctx context.Context, req *session.GetRequest) ([]*session.Event, error) {
	query := `
		SELECT event
		FROM agent_session_events
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3`
	args := []any{req.AppName, req.UserID, req.SessionID}
	if !req.After.IsZero() {
		args = append(args, req.After)
		query += fmt.Sprintf(" AND occurred_at >= $%d", len(args))
	}
	query += ` ORDER BY occurred_at DESC, id DESC`
	if req.NumRecentEvents > 0 {
		args = append(args, req.NumRecentEvents)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load session events: %w", err)
	}
	defer rows.Close()

	reverse := make([]*session.Event, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan session event: %w", err)
		}
		var event session.Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, fmt.Errorf("decode session event: %w", err)
		}
		reverse = append(reverse, &event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load session events: %w", err)
	}
	slices.Reverse(reverse)
	return alignPrunedCompactionEvents(reverse), nil
}

// alignPrunedCompactionEvents 保证当早期被 compaction 覆盖的原始事件被保留策略清理后，
// 残留的 compaction 摘要事件在返回切片中的位置依然早于其 EndTimestamp 之后的未压缩尾部事件。
//
// ADK v2 的 compactioninternal.Apply 在遍历事件时会把 compaction 摘要插入到
// “最后一个时间戳 <= EndTimestamp 的原始事件”所在槽位；一旦旧原始事件全部被删除，
// 该槽位将退化到 compaction 事件自身在切片中的索引位置。如果 compaction 事件的 sequence
// 晚于部分尾部事件（例如后台按时间阈值清理掉早期原始事件后，或者 compaction 晚于异步写入落库），
// 摘要会被错误地排到尾部事件之后，从而颠倒模型上下文时间顺序。
// 该调整仅作用于内存中的返回切片，不修改数据库中的 sequence 与原始记录。
func alignPrunedCompactionEvents(events []*session.Event) []*session.Event {
	if len(events) < 2 {
		return events
	}
	var result []*session.Event
	var pendingCompactions []*session.Event
	for _, ev := range events {
		if ev != nil && ev.Actions.Compaction != nil {
			pendingCompactions = append(pendingCompactions, ev)
			continue
		}
		result = append(result, ev)
	}
	if len(pendingCompactions) == 0 {
		return events
	}
	for _, comp := range pendingCompactions {
		endTS := comp.Actions.Compaction.EndTimestamp
		insertAt := len(result)
		for i, ev := range result {
			if ev == nil {
				continue
			}
			// 将 compaction 放在第一条严格晚于其覆盖窗口的事件之前；
			// 若仍存在未被清理的覆盖范围内事件，insertAt 会落在该批事件末尾，与原始顺序一致。
			evTime := ev.Timestamp
			if ev.Actions.Compaction != nil {
				evTime = ev.Actions.Compaction.EndTimestamp
			}
			if evTime.After(endTS) {
				insertAt = i
				break
			}
		}
		result = slices.Insert(result, insertAt, comp)
	}
	return result
}

func (s *Service) loadMergedState(
	ctx context.Context,
	appName, userID string,
	sessionState map[string]any,
) (map[string]any, error) {
	appState, err := loadScopedState(ctx, s.db, "agent_app_state", appName, "")
	if err != nil {
		return nil, err
	}
	userState, err := loadScopedState(ctx, s.db, "agent_user_state", appName, userID)
	if err != nil {
		return nil, err
	}
	return mergeStates(appState, userState, sessionState), nil
}

type queryer interface {
	QueryRow(context.Context, string, ...any) *sqldb.Row
}

func loadScopedState(ctx context.Context, q queryer, table, appName, userID string) (map[string]any, error) {
	var query string
	var args []any
	switch table {
	case "agent_app_state":
		query = `SELECT state FROM agent_app_state WHERE app_name=$1`
		args = []any{appName}
	case "agent_user_state":
		query = `SELECT state FROM agent_user_state WHERE app_name=$1 AND user_id=$2`
		args = []any{appName, userID}
	default:
		return nil, fmt.Errorf("unsupported state table %q", table)
	}
	var raw []byte
	err := q.QueryRow(ctx, query, args...).Scan(&raw)
	if sqldb.IsNoRows(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", table, err)
	}
	return decodeState(raw)
}

func mergeScopedState(
	ctx context.Context,
	tx *sqldb.Tx,
	table, appName, userID string,
	delta map[string]any,
	updatedAt time.Time,
) error {
	if len(delta) == 0 {
		return nil
	}
	raw, _, err := agentdata.MarshalJSONB(agentdata.Sanitize(delta))
	if err != nil {
		return fmt.Errorf("encode %s delta: %w", table, err)
	}
	// PostgreSQL 原生使用 jsonb || 合并；SQLite 在写事务内读取旧 JSON 并在 Go 层浅合并，
	// 两者都严格保留“覆盖顶层键、显式保留 null 值”的语义（SQLite json_patch 会删掉 null 键）。
	switch table {
	case "agent_app_state":
		if tx.Dialect() == sqldb.DialectPostgres {
			_, err = tx.Exec(ctx, `
				INSERT INTO agent_app_state(app_name, state, updated_at)
				VALUES ($1, $2, $3)
				ON CONFLICT (app_name) DO UPDATE
				SET state = agent_app_state.state || EXCLUDED.state,
				    updated_at = EXCLUDED.updated_at
			`, appName, sqldb.JSON(raw), updatedAt)
		} else {
			var existing []byte
			qErr := tx.QueryRow(ctx, `SELECT state FROM agent_app_state WHERE app_name=$1`, appName).Scan(&existing)
			if qErr != nil && !sqldb.IsNoRows(qErr) {
				return fmt.Errorf("load %s before merge: %w", table, qErr)
			}
			merged, mErr := mergeJSONObjects(existing, raw)
			if mErr != nil {
				return fmt.Errorf("merge %s: %w", table, mErr)
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO agent_app_state(app_name, state, updated_at)
				VALUES ($1, $2, $3)
				ON CONFLICT (app_name) DO UPDATE
				SET state = EXCLUDED.state,
				    updated_at = EXCLUDED.updated_at
			`, appName, sqldb.JSON(merged), updatedAt)
		}
	case "agent_user_state":
		if tx.Dialect() == sqldb.DialectPostgres {
			_, err = tx.Exec(ctx, `
				INSERT INTO agent_user_state(app_name, user_id, state, updated_at)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (app_name, user_id) DO UPDATE
				SET state = agent_user_state.state || EXCLUDED.state,
				    updated_at = EXCLUDED.updated_at
			`, appName, userID, sqldb.JSON(raw), updatedAt)
		} else {
			var existing []byte
			qErr := tx.QueryRow(ctx, `SELECT state FROM agent_user_state WHERE app_name=$1 AND user_id=$2`, appName, userID).Scan(&existing)
			if qErr != nil && !sqldb.IsNoRows(qErr) {
				return fmt.Errorf("load %s before merge: %w", table, qErr)
			}
			merged, mErr := mergeJSONObjects(existing, raw)
			if mErr != nil {
				return fmt.Errorf("merge %s: %w", table, mErr)
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO agent_user_state(app_name, user_id, state, updated_at)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (app_name, user_id) DO UPDATE
				SET state = EXCLUDED.state,
				    updated_at = EXCLUDED.updated_at
			`, appName, userID, sqldb.JSON(merged), updatedAt)
		}
	default:
		return fmt.Errorf("unsupported state table %q", table)
	}
	if err != nil {
		return fmt.Errorf("merge %s: %w", table, err)
	}
	return nil
}

// mergeJSONObjects 以 PostgreSQL `jsonb || jsonb` 语义浅合并两个顶层 JSON 对象。
func mergeJSONObjects(baseRaw, deltaRaw []byte) ([]byte, error) {
	base, err := decodeState(baseRaw)
	if err != nil {
		return nil, err
	}
	delta, err := decodeState(deltaRaw)
	if err != nil {
		return nil, err
	}
	for k, v := range delta {
		base[k] = v
	}
	out, _, err := agentdata.MarshalJSONB(base)
	return out, err
}

func splitStateDelta(delta map[string]any) (appState, userState, sessionState map[string]any) {
	appState = make(map[string]any)
	userState = make(map[string]any)
	sessionState = make(map[string]any)
	for key, value := range delta {
		switch {
		case strings.HasPrefix(key, session.KeyPrefixApp):
			appState[strings.TrimPrefix(key, session.KeyPrefixApp)] = value
		case strings.HasPrefix(key, session.KeyPrefixUser):
			userState[strings.TrimPrefix(key, session.KeyPrefixUser)] = value
		case strings.HasPrefix(key, session.KeyPrefixTemp):
			continue
		default:
			sessionState[key] = value
		}
	}
	return appState, userState, sessionState
}

func mergeStates(appState, userState, sessionState map[string]any) map[string]any {
	out := make(map[string]any, len(appState)+len(userState)+len(sessionState))
	maps.Copy(out, sessionState)
	for key, value := range appState {
		out[session.KeyPrefixApp+key] = value
	}
	for key, value := range userState {
		out[session.KeyPrefixUser+key] = value
	}
	return out
}

func decodeState(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func persistentEvent(event *session.Event) (*session.Event, bool) {
	out := *event
	normalized := false

	normStr := func(s string) string {
		clean, changed := agentdata.NormalizeText(s)
		if changed {
			normalized = true
		}
		return clean
	}

	out.ID = normStr(out.ID)
	out.InvocationID = normStr(out.InvocationID)
	out.Author = normStr(out.Author)
	out.Branch = normStr(out.Branch)
	out.IsolationScope = normStr(out.IsolationScope)
	out.ErrorCode = normStr(out.ErrorCode)
	out.ErrorMessage = normStr(out.ErrorMessage)
	out.Actions.TransferToAgent = normStr(out.Actions.TransferToAgent)

	if len(out.CustomMetadata) > 0 {
		sanitizedMeta, changed := agentdata.SanitizeWithReport(out.CustomMetadata)
		if changed {
			normalized = true
		}
		out.CustomMetadata = sanitizedMeta.(map[string]any)
	}

	if len(event.Actions.StateDelta) > 0 {
		filtered := make(map[string]any, len(event.Actions.StateDelta))
		for key, value := range event.Actions.StateDelta {
			if !strings.HasPrefix(key, session.KeyPrefixTemp) {
				filtered[key] = value
			}
		}
		sanitizedDelta, changed := agentdata.SanitizeWithReport(filtered)
		if changed {
			normalized = true
		}
		out.Actions.StateDelta = sanitizedDelta.(map[string]any)
	}

	if event.Actions.Compaction != nil && event.Actions.Compaction.CompactedContent != nil {
		compactionCopy := *event.Actions.Compaction
		compactedContent, changed := normalizeGenaiContent(event.Actions.Compaction.CompactedContent, true)
		if changed {
			normalized = true
		}
		compactionCopy.CompactedContent = compactedContent
		out.Actions.Compaction = &compactionCopy
	}

	if event.Content != nil {
		content, changed := normalizeGenaiContent(event.Content, true)
		if changed {
			normalized = true
		}
		out.Content = content
	}

	return &out, normalized
}

// liveSessionEvent 构造仅存在于当前 Attempt 内存会话中的事件副本：
// 剥离 Thought/ThoughtSignature 并做 UTF-8 规范化，但保留 FunctionResponse 中的一次性字段，
// 避免同一轮工具返回的一次性密钥在传递给模型前就被覆盖为 [REDACTED]。
func liveSessionEvent(original, persisted *session.Event) *session.Event {
	if original == nil || original.Content == nil {
		return persisted
	}
	hasFunctionResponse := false
	for _, part := range original.Content.Parts {
		if part != nil && part.FunctionResponse != nil {
			hasFunctionResponse = true
			break
		}
	}
	if !hasFunctionResponse {
		return persisted
	}
	live := *persisted
	live.Content, _ = normalizeGenaiContent(original.Content, false)
	return &live
}

func normalizeGenaiContent(input *genai.Content, redactSecrets bool) (*genai.Content, bool) {
	if input == nil {
		return nil, false
	}
	normalized := false
	normStr := func(s string) string {
		clean, changed := agentdata.NormalizeText(s)
		if changed {
			normalized = true
		}
		return clean
	}
	processMap := func(m map[string]any) map[string]any {
		var processed any
		var changed bool
		if redactSecrets {
			processed, changed = agentdata.SanitizeWithReport(m)
		} else {
			processed, changed = agentdata.NormalizeValueWithReport(m)
		}
		if changed {
			normalized = true
		}
		return processed.(map[string]any)
	}

	content := *input
	content.Role = normStr(content.Role)
	content.Parts = make([]*genai.Part, 0, len(input.Parts))
	for _, part := range input.Parts {
		if part == nil || part.Thought {
			continue
		}
		copyPart := *part
		// Thought signatures are opaque reasoning metadata and are not needed
		// to resume a persisted CycleDock Session.
		copyPart.ThoughtSignature = nil
		copyPart.Text = normStr(copyPart.Text)
		if part.FunctionCall != nil {
			call := *part.FunctionCall
			call.ID = normStr(call.ID)
			call.Name = normStr(call.Name)
			if part.FunctionCall.Args != nil {
				call.Args = processMap(part.FunctionCall.Args)
			}
			copyPart.FunctionCall = &call
		}
		if part.FunctionResponse != nil {
			response := *part.FunctionResponse
			response.ID = normStr(response.ID)
			response.Name = normStr(response.Name)
			if part.FunctionResponse.Response != nil {
				response.Response = processMap(part.FunctionResponse.Response)
			}
			copyPart.FunctionResponse = &response
		}
		content.Parts = append(content.Parts, &copyPart)
	}
	return &content, normalized
}

type storedSession struct {
	appName string
	userID  string
	id      string

	appendMu  sync.Mutex
	mu        sync.RWMutex
	state     map[string]any
	events    []*session.Event
	updatedAt time.Time
}

func newStoredSession(
	appName, userID, id string,
	state map[string]any,
	events []*session.Event,
	updatedAt time.Time,
) *storedSession {
	return &storedSession{
		appName:   appName,
		userID:    userID,
		id:        id,
		state:     maps.Clone(state),
		events:    slices.Clone(events),
		updatedAt: updatedAt,
	}
}

func (s *storedSession) ID() string      { return s.id }
func (s *storedSession) AppName() string { return s.appName }
func (s *storedSession) UserID() string  { return s.userID }

func (s *storedSession) State() session.State {
	return &stateView{session: s}
}

func (s *storedSession) Events() session.Events {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return eventList(slices.Clone(s.events))
}

func (s *storedSession) LastUpdateTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}

func (s *storedSession) append(original, persisted *session.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = make(map[string]any)
	}
	maps.Copy(s.state, original.Actions.StateDelta)
	s.events = append(s.events, persisted)
	s.updatedAt = original.Timestamp
}

type stateView struct {
	session *storedSession
}

func (s *stateView) Get(key string) (any, error) {
	s.session.mu.RLock()
	defer s.session.mu.RUnlock()
	value, ok := s.session.state[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return value, nil
}

func (s *stateView) Set(key string, value any) error {
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if s.session.state == nil {
		s.session.state = make(map[string]any)
	}
	s.session.state[key] = value
	return nil
}

func (s *stateView) All() iter.Seq2[string, any] {
	s.session.mu.RLock()
	copy := maps.Clone(s.session.state)
	s.session.mu.RUnlock()
	return func(yield func(string, any) bool) {
		for key, value := range copy {
			if !yield(key, value) {
				return
			}
		}
	}
}

type eventList []*session.Event

func (e eventList) All() iter.Seq[*session.Event] {
	return func(yield func(*session.Event) bool) {
		for _, event := range e {
			if !yield(event) {
				return
			}
		}
	}
}

func (e eventList) Len() int { return len(e) }

func (e eventList) At(i int) *session.Event {
	if i < 0 || i >= len(e) {
		return nil
	}
	return e[i]
}

var (
	_ session.Service = (*PostgresService)(nil)
	_ session.Session = (*storedSession)(nil)
	_ session.State   = (*stateView)(nil)
	_ session.Events  = eventList(nil)
)
