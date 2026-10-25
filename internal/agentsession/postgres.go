package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
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

// PostgresService persists ADK sessions without introducing GORM or a second
// migration system into CycleDock.
type PostgresService struct {
	pool *pgxpool.Pool
}

func NewPostgresService(pool *pgxpool.Pool) *PostgresService {
	return &PostgresService{pool: pool}
}

func (s *PostgresService) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	if req == nil || strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" {
		return nil, errors.New("app_name and user_id are required")
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	appDelta, userDelta, sessionState := splitStateDelta(req.State)
	now := time.Now().UTC().Truncate(time.Microsecond)

	tx, err := s.pool.Begin(ctx)
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

	rawState, err := json.Marshal(sessionState)
	if err != nil {
		return nil, fmt.Errorf("encode session state: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO agent_sessions(app_name, user_id, id, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4::jsonb, $5, $5)
	`, req.AppName, req.UserID, sessionID, rawState, now)
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

func (s *PostgresService) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	if req == nil || strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.SessionID) == "" {
		return nil, errors.New("app_name, user_id, session_id are required")
	}

	var rawState []byte
	var updatedAt time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT state, updated_at
		FROM agent_sessions
		WHERE app_name=$1 AND user_id=$2 AND id=$3
	`, req.AppName, req.UserID, req.SessionID).Scan(&rawState, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
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

func (s *PostgresService) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
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

	rows, err := s.pool.Query(ctx, query, args...)
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

func (s *PostgresService) Delete(ctx context.Context, req *session.DeleteRequest) error {
	if req == nil || strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.SessionID) == "" {
		return errors.New("app_name, user_id, session_id are required")
	}
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM agent_sessions
		WHERE app_name=$1 AND user_id=$2 AND id=$3
	`, req.AppName, req.UserID, req.SessionID); err != nil {
		return fmt.Errorf("delete session %q: %w", req.SessionID, err)
	}
	return nil
}

func (s *PostgresService) AppendEvent(ctx context.Context, cur session.Session, event *session.Event) error {
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

	persistedEvent := persistentEvent(event)
	rawEvent, err := json.Marshal(persistedEvent)
	if err != nil {
		return fmt.Errorf("encode session event: %w", err)
	}
	appDelta, userDelta, sessionDelta := splitStateDelta(event.Actions.StateDelta)
	rawSessionDelta, err := json.Marshal(sessionDelta)
	if err != nil {
		return fmt.Errorf("encode session state delta: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
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

	var storedUpdated time.Time
	err = tx.QueryRow(ctx, `
		SELECT updated_at
		FROM agent_sessions
		WHERE app_name=$1 AND user_id=$2 AND id=$3
		FOR UPDATE
	`, local.appName, local.userID, local.id).Scan(&storedUpdated)
	if errors.Is(err, pgx.ErrNoRows) {
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
	if _, err := tx.Exec(ctx, `
		UPDATE agent_sessions
		SET state = state || $4::jsonb, updated_at=$5
		WHERE app_name=$1 AND user_id=$2 AND id=$3
	`, local.appName, local.userID, local.id, rawSessionDelta, event.Timestamp); err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_session_events(
			app_name, user_id, session_id, id, invocation_id, occurred_at, event
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
	`, local.appName, local.userID, local.id, event.ID, event.InvocationID, event.Timestamp, rawEvent); err != nil {
		return fmt.Errorf("append session event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit append event: %w", err)
	}

	local.append(event, persistedEvent)
	return nil
}

func guardCompactionAppend(
	ctx context.Context,
	tx pgx.Tx,
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
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		FOR UPDATE
	`, workspaceID, cardID, attemptID).Scan(&status, &invocationID)
	if errors.Is(err, pgx.ErrNoRows) {
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
	tx pgx.Tx,
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
		WHERE workspace_id=$1 AND id=$2
		FOR UPDATE
	`, workspaceID, cardID).Scan(&lockedCard)
	if errors.Is(err, pgx.ErrNoRows) {
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
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		FOR UPDATE
	`, workspaceID, cardID, attemptID).Scan(&status, &invocationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrAttemptNotCurrent
	}
	if err != nil {
		return fmt.Errorf("lock attempt for session append: %w", err)
	}
	if status != string(domain.AttemptStatusRunning) &&
		status != string(domain.AttemptStatusWaitingHuman) {
		return domain.ErrAttemptNotCurrent
	}

	if invocationID == nil {
		// The first ADK event must bind while the Attempt is still running.
		// waiting_human may only persist tail events from an already-bound
		// invocation (for example the FunctionResponse of wait_for_human).
		if status != string(domain.AttemptStatusRunning) {
			return domain.ErrAttemptNotCurrent
		}
		command, err := tx.Exec(ctx, `
			UPDATE turn_attempts
			SET invocation_id=$4, updated_at=now()
			WHERE workspace_id=$1 AND card_id=$2 AND id=$3
			  AND status='running'
			  AND invocation_id IS NULL
		`, workspaceID, cardID, attemptID, event.InvocationID)
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

func (s *PostgresService) loadEvents(ctx context.Context, req *session.GetRequest) ([]*session.Event, error) {
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

	rows, err := s.pool.Query(ctx, query, args...)
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
	return reverse, nil
}

func (s *PostgresService) loadMergedState(
	ctx context.Context,
	appName, userID string,
	sessionState map[string]any,
) (map[string]any, error) {
	appState, err := loadScopedState(ctx, s.pool, "agent_app_state", appName, "")
	if err != nil {
		return nil, err
	}
	userState, err := loadScopedState(ctx, s.pool, "agent_user_state", appName, userID)
	if err != nil {
		return nil, err
	}
	return mergeStates(appState, userState, sessionState), nil
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
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
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", table, err)
	}
	return decodeState(raw)
}

func mergeScopedState(
	ctx context.Context,
	tx pgx.Tx,
	table, appName, userID string,
	delta map[string]any,
	updatedAt time.Time,
) error {
	if len(delta) == 0 {
		return nil
	}
	raw, err := json.Marshal(delta)
	if err != nil {
		return fmt.Errorf("encode %s delta: %w", table, err)
	}
	switch table {
	case "agent_app_state":
		_, err = tx.Exec(ctx, `
			INSERT INTO agent_app_state(app_name, state, updated_at)
			VALUES ($1, $2::jsonb, $3)
			ON CONFLICT (app_name) DO UPDATE
			SET state = agent_app_state.state || EXCLUDED.state,
			    updated_at = EXCLUDED.updated_at
		`, appName, raw, updatedAt)
	case "agent_user_state":
		_, err = tx.Exec(ctx, `
			INSERT INTO agent_user_state(app_name, user_id, state, updated_at)
			VALUES ($1, $2, $3::jsonb, $4)
			ON CONFLICT (app_name, user_id) DO UPDATE
			SET state = agent_user_state.state || EXCLUDED.state,
			    updated_at = EXCLUDED.updated_at
		`, appName, userID, raw, updatedAt)
	default:
		return fmt.Errorf("unsupported state table %q", table)
	}
	if err != nil {
		return fmt.Errorf("merge %s: %w", table, err)
	}
	return nil
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

func persistentEvent(event *session.Event) *session.Event {
	out := *event

	if len(event.Actions.StateDelta) > 0 {
		filtered := make(map[string]any, len(event.Actions.StateDelta))
		for key, value := range event.Actions.StateDelta {
			if !strings.HasPrefix(key, session.KeyPrefixTemp) {
				filtered[key] = value
			}
		}
		out.Actions.StateDelta = filtered
	}

	if event.Content != nil {
		content := *event.Content
		content.Parts = make([]*genai.Part, 0, len(event.Content.Parts))
		for _, part := range event.Content.Parts {
			if part == nil || part.Thought {
				continue
			}
			copyPart := *part
			// Thought signatures are opaque reasoning metadata and are not needed
			// to resume a persisted CycleDock Session.
			copyPart.ThoughtSignature = nil
			if part.FunctionCall != nil {
				call := *part.FunctionCall
				if part.FunctionCall.Args != nil {
					call.Args = agentdata.Sanitize(part.FunctionCall.Args).(map[string]any)
				}
				copyPart.FunctionCall = &call
			}
			if part.FunctionResponse != nil {
				response := *part.FunctionResponse
				if part.FunctionResponse.Response != nil {
					response.Response = agentdata.Sanitize(part.FunctionResponse.Response).(map[string]any)
				}
				copyPart.FunctionResponse = &response
			}
			content.Parts = append(content.Parts, &copyPart)
		}
		out.Content = &content
	}

	return &out
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
