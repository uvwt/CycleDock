package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
)

type CardStatusChange struct {
	ID        uuid.UUID `json:"id"`
	Sequence  int64     `json:"sequence"`
	FromState string    `json:"from_state"`
	ToState   string    `json:"to_state"`
	Content   string    `json:"content"`
	ActorName string    `json:"actor_name"`
	ActorKind string    `json:"actor_kind"`
	CreatedAt time.Time `json:"created_at"`
}

type AgentCardContext struct {
	DeviceNodeID          string
	DeviceName            string
	WorkspaceID           uuid.UUID
	WorkspaceName         string
	ProjectID             uuid.UUID
	ProjectName           string
	ProjectDescription    string
	CardID                uuid.UUID
	CardKind              domain.CardKind
	CardTitle             string
	CardDescription       string
	IsMainDiscussion      bool
	DiscussionState       string
	TodoState             string
	OngoingState          string
	RequiredConfirmations int32

	TurnID             uuid.UUID
	TurnSequence       int64
	TurnSource         domain.TurnSource
	TurnContent        string
	DispatchMode       domain.DispatchMode
	TurnCreatedAt      time.Time
	IsBuiltInProactive bool

	PreferredModelRouteID uuid.NullUUID

	AttemptID        uuid.UUID
	AttemptNumber    int
	AttemptReason    domain.AttemptReason
	RunnerSourceID   uuid.UUID
	PendingTurnCount int64

	// StatusChanges 记录自上一轮已执行 Turn/Attempt 以来卡片发生的外部状态变更（例如用户由 review 移回 working）。
	// 供 Agent Runner 在下次发消息时隐式注入模型上下文，聊天前端不产生可见气泡。
	StatusChanges []CardStatusChange
}

// LoadAgentCardContext returns only business/runtime metadata. Conversation
// history is deliberately absent: ADK SessionService is the sole source for
// prior user/model/tool events.
func (s *Store) LoadAgentCardContext(
	ctx context.Context,
	workspaceID, attemptID, runnerSourceID uuid.UUID,
) (AgentCardContext, error) {
	var out AgentCardContext
	var discussionState, todoState, ongoingState pgtype.Text
	var turnSource, dispatchMode, attemptReason, turnIdempotencyKey string
	var createdAt pgtype.Timestamptz

	err := s.pool.QueryRow(ctx, `
		SELECT
			w.id, w.name,
			p.id, p.name, p.description,
			c.id, c.kind, c.title, c.description, COALESCE(p.main_discussion_id = c.id, false),
			c.discussion_state, c.todo_state, c.ongoing_state,
			c.required_confirmations,
			t.id, t.sequence, t.source, t.content, t.dispatch_mode, t.created_at, t.device_node_id, t.device_name, COALESCE(t.idempotency_key, ''), COALESCE(t.preferred_model_route_id, c.preferred_model_route_id),
			a.id, a.attempt_number, a.reason, a.runner_source_id,
			(
				SELECT count(*)
				FROM card_turns pending
				WHERE pending.workspace_id=c.workspace_id
				  AND pending.card_id=c.id
				  AND pending.cancelled_at IS NULL
				  AND NOT EXISTS (
					SELECT 1
					FROM turn_attempts pa
					WHERE pa.workspace_id=pending.workspace_id
					  AND pa.turn_id=pending.id
				  )
			)
		FROM turn_attempts a
		JOIN card_turns t
		  ON t.workspace_id=a.workspace_id
		 AND t.card_id=a.card_id
		 AND t.id=a.turn_id
		JOIN cards c
		  ON c.workspace_id=a.workspace_id
		 AND c.id=a.card_id
		JOIN projects p
		  ON p.workspace_id=c.workspace_id
		 AND p.id=c.project_id
		JOIN workspaces w ON w.id=c.workspace_id
		WHERE a.workspace_id=$1
		  AND a.id=$2
		  AND a.runner_source_id=$3
		  AND a.status='running'
	`, workspaceID, attemptID, runnerSourceID).Scan(
		&out.WorkspaceID, &out.WorkspaceName,
		&out.ProjectID, &out.ProjectName, &out.ProjectDescription,
		&out.CardID, &out.CardKind, &out.CardTitle, &out.CardDescription, &out.IsMainDiscussion,
		&discussionState, &todoState, &ongoingState, &out.RequiredConfirmations,
		&out.TurnID, &out.TurnSequence, &turnSource, &out.TurnContent, &dispatchMode, &createdAt, &out.DeviceNodeID, &out.DeviceName, &turnIdempotencyKey, &out.PreferredModelRouteID,
		&out.AttemptID, &out.AttemptNumber, &attemptReason, &out.RunnerSourceID,
		&out.PendingTurnCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentCardContext{}, domain.ErrAttemptNotCurrent
	}
	if err != nil {
		return AgentCardContext{}, err
	}
	if discussionState.Valid {
		out.DiscussionState = discussionState.String
	}
	if todoState.Valid {
		out.TodoState = todoState.String
	}
	if ongoingState.Valid {
		out.OngoingState = ongoingState.String
	}
	out.TurnSource = domain.TurnSource(turnSource)
	out.DispatchMode = domain.DispatchMode(dispatchMode)
	out.AttemptReason = domain.AttemptReason(attemptReason)
	out.TurnCreatedAt = createdAt.Time
	out.IsBuiltInProactive = IsBuiltInProactiveTurnKey(turnIdempotencyKey)

	changes, err := s.loadStatusChangesSinceLastExecutedTurn(
		ctx, out.WorkspaceID, out.CardID, out.TurnID, out.TurnSequence, out.RunnerSourceID,
	)
	if err != nil {
		return AgentCardContext{}, fmt.Errorf("load status changes: %w", err)
	}
	out.StatusChanges = changes
	return out, nil
}

// loadStatusChangesSinceLastExecutedTurn 查询自上一次已执行 Turn/Attempt 以来卡片发生的外部状态变更（例如用户将 review 更改为 working）。
// 仅加载用户或外部操作触发的状态流转，排除 Agent Runner 自身生命周期触发的操作。
// 排队与重试轮次通过对齐当前 Turn 首个 Attempt 游标，保证上下文的一致性与确定性。
func (s *Store) loadStatusChangesSinceLastExecutedTurn(
	ctx context.Context,
	workspaceID, cardID, turnID uuid.UUID,
	turnSequence int64,
	runnerSourceID uuid.UUID,
) ([]CardStatusChange, error) {
	// 1. 查找在当前 Turn 之前真正执行过的最近一次 Attempt（属于前序 Turn，且 started_at 不为空）
	var prevAttemptID uuid.UUID
	var prevTurnCreatedAt time.Time
	var prevSourceEventID *uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT
			a_prev.id,
			t_prev.created_at,
			t_prev.source_event_id
		FROM turn_attempts a_prev
		JOIN card_turns t_prev
		  ON t_prev.workspace_id = a_prev.workspace_id
		 AND t_prev.card_id = a_prev.card_id
		 AND t_prev.id = a_prev.turn_id
		WHERE a_prev.workspace_id = $1
		  AND a_prev.card_id = $2
		  AND t_prev.sequence < $3
		  AND a_prev.started_at IS NOT NULL
		ORDER BY t_prev.sequence DESC, a_prev.attempt_number DESC
		LIMIT 1
	`, workspaceID, cardID, turnSequence).Scan(&prevAttemptID, &prevTurnCreatedAt, &prevSourceEventID)
	if errors.Is(err, pgx.ErrNoRows) {
		// 卡片首次执行，前序尚无执行过的 Turn/Attempt，不生成“自上一轮执行以来”的增量提示
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// 2. 计算前置已执行 Turn/Attempt 产出的最大 event sequence 游标基线：
	// 包含前置 Turn 自身的创建时间之前已存在的事件、前置 Turn 的输入事件、以及该 Attempt 执行产生的所有事件。
	var minSequence int64
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(e.sequence), 0)
		FROM events e
		WHERE e.workspace_id = $1
		  AND e.card_id = $2
		  AND (
			e.attempt_id = $3
			OR e.id = COALESCE($4, '00000000-0000-0000-0000-000000000000'::uuid)
			OR e.created_at <= $5
		  )
	`, workspaceID, cardID, prevAttemptID, prevSourceEventID, prevTurnCreatedAt).Scan(&minSequence)
	if err != nil {
		return nil, err
	}

	// 3. 计算当前 Turn 的起始截止时间。使用当前 Turn 的首个 Attempt 启动/创建时间，使多次技术重试与排队保持完全一致的快照
	var maxCreatedAt time.Time
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(started_at, created_at)
		FROM turn_attempts
		WHERE workspace_id = $1
		  AND card_id = $2
		  AND turn_id = $3
		  AND attempt_number = 1
	`, workspaceID, cardID, turnID).Scan(&maxCreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		maxCreatedAt = time.Now().UTC()
	} else if err != nil {
		return nil, err
	}

	// 4. 查询在基准游标之后、当前 Turn 执行前发生的所有 status_change 事件，排除 runner 自身
	rows, err := s.pool.Query(ctx, `
		SELECT
			e.id,
			e.sequence,
			e.content,
			e.created_at,
			s.name,
			s.kind
		FROM events e
		JOIN sources s
		  ON s.workspace_id = e.workspace_id
		 AND s.id = e.actor_source_id
		WHERE e.workspace_id = $1
		  AND e.card_id = $2
		  AND e.event_type = 'status_change'
		  AND e.sequence > $3
		  AND e.created_at <= $4
		  AND e.actor_source_id != $5
		ORDER BY e.sequence ASC
	`, workspaceID, cardID, minSequence, maxCreatedAt, runnerSourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var changes []CardStatusChange
	for rows.Next() {
		var item CardStatusChange
		var actorKind string
		if err := rows.Scan(
			&item.ID,
			&item.Sequence,
			&item.Content,
			&item.CreatedAt,
			&item.ActorName,
			&actorKind,
		); err != nil {
			return nil, err
		}
		item.ActorKind = actorKind
		parts := strings.Split(item.Content, " -> ")
		if len(parts) == 2 {
			item.FromState = strings.TrimSpace(parts[0])
			item.ToState = strings.TrimSpace(parts[1])
		}
		changes = append(changes, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return changes, nil
}

type AttemptProgressParams struct {
	WorkspaceID    uuid.UUID
	CardID         uuid.UUID
	AttemptID      uuid.UUID
	RunnerSourceID uuid.UUID
	Stage          string
	Summary        string
	EventContent   string
	IdempotencyKey string
	LeaseDuration  time.Duration
}

func (s *Store) ReportAttemptProgress(
	ctx context.Context,
	p AttemptProgressParams,
) (uuid.UUID, bool, error) {
	p.Stage, _ = agentdata.NormalizeText(strings.TrimSpace(p.Stage))
	p.Summary, _ = agentdata.NormalizeText(strings.TrimSpace(p.Summary))
	p.EventContent, _ = agentdata.NormalizeText(strings.TrimSpace(p.EventContent))
	p.IdempotencyKey, _ = agentdata.NormalizeText(strings.TrimSpace(p.IdempotencyKey))
	if p.Stage == "" || p.Summary == "" {
		return uuid.Nil, false, fmt.Errorf("%w: stage and summary are required", domain.ErrConflict)
	}
	if p.EventContent != "" && p.IdempotencyKey == "" {
		return uuid.Nil, false, fmt.Errorf("%w: progress event requires idempotency key", domain.ErrConflict)
	}
	if p.LeaseDuration <= 0 {
		p.LeaseDuration = DefaultClaimLease
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return uuid.Nil, false, err
	}
	if err := requireCurrentAttempt(
		ctx, tx, p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID,
		domain.AttemptStatusRunning,
	); err != nil {
		return uuid.Nil, false, err
	}

	var eventID uuid.UUID
	var reused bool
	if p.EventContent != "" {
		hash := stableRequestHash(
			"attempt_progress", p.WorkspaceID.String(), p.CardID.String(),
			p.AttemptID.String(), p.RunnerSourceID.String(),
			p.Stage, p.Summary, p.EventContent,
		)
		eventID, reused, err = appendAttemptActivity(
			ctx, tx, p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID,
			domain.EventTypeProgress, p.EventContent, nil, p.IdempotencyKey, hash,
		)
		if err != nil {
			return uuid.Nil, false, err
		}
	}

	now := time.Now().UTC()
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET current_stage=$5,
		    progress_summary=$6,
		    heartbeat_at=$7,
		    lease_expires_at=$8,
		    updated_at=$7
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND runner_source_id=$4 AND status='running'
	`, p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID,
		p.Stage, p.Summary, now, now.Add(p.LeaseDuration))
	if err != nil {
		return uuid.Nil, false, err
	}
	if command.RowsAffected() != 1 {
		return uuid.Nil, false, domain.ErrAttemptNotCurrent
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return eventID, reused, nil
}

type AttemptEventParams struct {
	WorkspaceID    uuid.UUID
	CardID         uuid.UUID
	AttemptID      uuid.UUID
	RunnerSourceID uuid.UUID
	Type           domain.EventType
	Content        string
	Checkin        *domain.CheckinStatus
	IdempotencyKey string
}

func (s *Store) AddAttemptEvent(ctx context.Context, p AttemptEventParams) (uuid.UUID, bool, error) {
	switch p.Type {
	case domain.EventTypeComment, domain.EventTypeFinding, domain.EventTypeCheckin:
	default:
		return uuid.Nil, false, fmt.Errorf("unsupported attempt event type %q", p.Type)
	}
	p.Content, _ = agentdata.NormalizeText(strings.TrimSpace(p.Content))
	p.IdempotencyKey, _ = agentdata.NormalizeText(strings.TrimSpace(p.IdempotencyKey))
	if p.Content == "" || p.IdempotencyKey == "" {
		return uuid.Nil, false, fmt.Errorf("%w: content and idempotency key are required", domain.ErrConflict)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return uuid.Nil, false, err
	}
	if err := requireCurrentAttempt(
		ctx, tx, p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID,
		domain.AttemptStatusRunning,
	); err != nil {
		return uuid.Nil, false, err
	}

	if p.Type == domain.EventTypeCheckin {
		if p.Checkin == nil ||
			(*p.Checkin != domain.CheckinHealthy &&
				*p.Checkin != domain.CheckinWarning &&
				*p.Checkin != domain.CheckinCritical) {
			return uuid.Nil, false, fmt.Errorf("valid check-in status required")
		}
		var kind string
		if err := tx.QueryRow(ctx, `
			SELECT kind FROM cards WHERE workspace_id=$1 AND id=$2
		`, p.WorkspaceID, p.CardID).Scan(&kind); err != nil {
			return uuid.Nil, false, err
		}
		if kind != string(domain.CardKindOngoing) {
			return uuid.Nil, false, fmt.Errorf("%w: check-in requires ongoing card", domain.ErrInvalidTransition)
		}
	}

	hash := stableRequestHash(
		"attempt_event", p.WorkspaceID.String(), p.CardID.String(),
		p.AttemptID.String(), p.RunnerSourceID.String(),
		string(p.Type), p.Content, fmt.Sprint(p.Checkin),
	)
	eventID, reused, err := appendAttemptActivity(
		ctx, tx, p.WorkspaceID, p.CardID, p.AttemptID, p.RunnerSourceID,
		p.Type, p.Content, p.Checkin, p.IdempotencyKey, hash,
	)
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return eventID, reused, nil
}

// CompleteAttempt 只结算执行状态；用户回复由 Message 独立保存，重试不追加气泡。
func (s *Store) CompleteAttempt(
	ctx context.Context,
	workspaceID, cardID, attemptID, runnerSourceID uuid.UUID,
	now time.Time,
) error {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}

	var actualRunner uuid.UUID
	var status domain.AttemptStatus
	err = tx.QueryRow(ctx, `
		SELECT runner_source_id, status
		FROM turn_attempts
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		FOR UPDATE
	`, workspaceID, cardID, attemptID).Scan(&actualRunner, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrAttemptNotCurrent
	}
	if err != nil {
		return err
	}
	if actualRunner != runnerSourceID {
		return domain.ErrAttemptNotCurrent
	}
	if status == domain.AttemptStatusCompleted {
		return tx.Commit(ctx)
	}
	if status != domain.AttemptStatusRunning {
		return domain.ErrAttemptNotCurrent
	}
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status='completed',
		    heartbeat_at=NULL,
		    lease_expires_at=NULL,
		    finished_at=$5,
		    updated_at=$5
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND runner_source_id=$4 AND status='running'
	`, workspaceID, cardID, attemptID, runnerSourceID, now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrAttemptNotCurrent
	}
	return tx.Commit(ctx)
}

func requireCurrentAttempt(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID, attemptID, runnerSourceID uuid.UUID,
	statuses ...domain.AttemptStatus,
) error {
	var status string
	var actualRunner uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT status, runner_source_id
		FROM turn_attempts
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		FOR UPDATE
	`, workspaceID, cardID, attemptID).Scan(&status, &actualRunner)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrAttemptNotCurrent
	}
	if err != nil {
		return err
	}
	if actualRunner != runnerSourceID {
		return domain.ErrAttemptNotCurrent
	}
	for _, allowed := range statuses {
		if status == string(allowed) {
			return nil
		}
	}
	return domain.ErrAttemptNotCurrent
}

func appendAttemptActivity(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID, attemptID, sourceID uuid.UUID,
	eventType domain.EventType,
	content string,
	checkin *domain.CheckinStatus,
	idempotencyKey, requestHash string,
) (uuid.UUID, bool, error) {
	var existingID, existingCardID uuid.UUID
	var existingAttemptID uuid.NullUUID
	var existingHash string
	err := tx.QueryRow(ctx, `
		SELECT id, card_id, attempt_id, COALESCE(request_hash,'')
		FROM events
		WHERE workspace_id=$1
		  AND actor_source_id=$2
		  AND event_type=$3
		  AND idempotency_key=$4
	`, workspaceID, sourceID, string(eventType), idempotencyKey).Scan(
		&existingID, &existingCardID, &existingAttemptID, &existingHash,
	)
	if err == nil {
		if existingCardID != cardID ||
			!existingAttemptID.Valid ||
			existingAttemptID.UUID != attemptID ||
			existingHash != requestHash {
			return uuid.Nil, false, domain.ErrIdempotencyMismatch
		}
		return existingID, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}

	var checkinValue any
	if checkin != nil {
		checkinValue = string(*checkin)
	}
	eventID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id, attempt_id,
			content, checkin_status, idempotency_key, request_hash
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, eventID, workspaceID, cardID, string(eventType), sourceID, attemptID,
		content, checkinValue, idempotencyKey, requestHash)
	if err != nil {
		return uuid.Nil, false, mapConstraintError(err)
	}
	return eventID, false, nil
}
