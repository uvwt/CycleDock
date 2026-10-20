package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type BuiltInRunClaim struct {
	WorkspaceID uuid.UUID
	RunID       uuid.UUID
	CardID      uuid.UUID
	TriggerID   uuid.UUID
	SourceID    uuid.UUID
	Reused      bool
}

func (s *Store) ClaimNextBuiltInRun(
	ctx context.Context,
	now time.Time,
	leaseDuration time.Duration,
) (*BuiltInRunClaim, error) {
	if leaseDuration <= 0 {
		leaseDuration = DefaultClaimLease
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// queued Run 通常来自 Human interrupt / waiting_human reply。它已经绑定 runner，
	// 必须优先启动，不能把同一卡片的后续 pending Trigger 抢到前面。
	var queued BuiltInRunClaim
	err = tx.QueryRow(ctx, `
		SELECT r.workspace_id, r.id, r.card_id, r.trigger_id, r.runner_source_id
		FROM agent_runs r
		JOIN cards c
		  ON c.workspace_id = r.workspace_id
		 AND c.id = r.card_id
		JOIN workspace_agent_settings s
		  ON s.workspace_id = r.workspace_id
		 AND s.source_id = r.runner_source_id
		 AND s.enabled
		JOIN sources src
		  ON src.workspace_id = s.workspace_id
		 AND src.id = s.source_id
		 AND src.kind = 'cycle_agent'
		JOIN triggers t
		  ON t.workspace_id = r.workspace_id
		 AND t.id = r.trigger_id
		WHERE r.status = 'queued'
		  AND ((c.kind = 'normal' AND c.normal_column <> 'done') OR c.ongoing_state = 'active')
		ORDER BY t.sequence
		LIMIT 1
		FOR UPDATE OF c SKIP LOCKED
	`).Scan(&queued.WorkspaceID, &queued.RunID, &queued.CardID, &queued.TriggerID, &queued.SourceID)
	if err == nil {
		command, err := tx.Exec(ctx, `
			UPDATE agent_runs
			SET status='running',
				started_at=$3,
				heartbeat_at=$3,
				lease_expires_at=$4,
				updated_at=$3
			WHERE workspace_id=$1 AND id=$2 AND status='queued'
		`, queued.WorkspaceID, queued.RunID, now, now.Add(leaseDuration))
		if err != nil {
			return nil, err
		}
		if command.RowsAffected() != 1 {
			return nil, domain.ErrRunNotCurrent
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		queued.Reused = true
		return &queued, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// 没有已绑定的 queued Run 时，从所有启用内置 Agent 的 Workspace 中找最早
	// pending Trigger。锁 Card 而不是 Trigger，保持与 interrupt/finish 相同锁顺序。
	var claim BuiltInRunClaim
	err = tx.QueryRow(ctx, `
		SELECT c.workspace_id, c.id, pending.id, settings.source_id
		FROM cards c
		JOIN workspace_agent_settings settings
		  ON settings.workspace_id = c.workspace_id
		 AND settings.enabled
		JOIN sources src
		  ON src.workspace_id = settings.workspace_id
		 AND src.id = settings.source_id
		 AND src.kind = 'cycle_agent'
		JOIN LATERAL (
			SELECT t.id, t.sequence
			FROM triggers t
			WHERE t.workspace_id = c.workspace_id
			  AND t.card_id = c.id
			  AND t.cancelled_at IS NULL
			  AND NOT EXISTS (
				SELECT 1
				FROM agent_runs r
				WHERE r.workspace_id = t.workspace_id
				  AND r.trigger_id = t.id
			  )
			ORDER BY t.sequence
			LIMIT 1
		) pending ON true
		WHERE NOT EXISTS (
			SELECT 1
			FROM agent_runs current_run
			WHERE current_run.workspace_id = c.workspace_id
			  AND current_run.card_id = c.id
			  AND current_run.status IN ('queued','running','waiting_human')
		)
		  AND (c.kind = 'normal' OR c.ongoing_state = 'active')
		ORDER BY pending.sequence
		LIMIT 1
		FOR UPDATE OF c SKIP LOCKED
	`).Scan(&claim.WorkspaceID, &claim.CardID, &claim.TriggerID, &claim.SourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	claim.RunID = uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO agent_runs (
			id, workspace_id, card_id, trigger_id, runner_source_id,
			status, started_at, heartbeat_at, lease_expires_at
		)
		VALUES ($1,$2,$3,$4,$5,'running',$6,$6,$7)
	`, claim.RunID, claim.WorkspaceID, claim.CardID, claim.TriggerID, claim.SourceID, now, now.Add(leaseDuration))
	if err != nil {
		return nil, mapConstraintError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &claim, nil
}

type AgentContextEvent struct {
	Sequence      int64
	Type          domain.EventType
	ActorName     string
	ActorKind     domain.SourceKind
	Content       string
	CheckinStatus string
	CreatedAt     time.Time
}

type AgentRunContext struct {
	WorkspaceID           uuid.UUID
	WorkspaceName         string
	ProjectID             uuid.UUID
	ProjectName           string
	ProjectDescription    string
	ProjectInstructions   string
	CardID                uuid.UUID
	CardKind              domain.CardKind
	CardTitle             string
	CardDescription       string
	NormalColumn          string
	OngoingState          string
	RequiredConfirmations int32
	TriggerID             uuid.UUID
	TriggerCause          domain.TriggerCause
	TriggerContent        string
	TriggeredAt           time.Time
	Events                []AgentContextEvent
	HistoricalFacts       []AgentContextEvent
	HistoryCompaction     AgentHistoryCompaction
	PendingTriggerCount   int64
}

type AgentHistoryCompaction struct {
	BeforeSequence      int64
	OlderCommentCount   int64
	OlderProgressCount  int64
	RecentContextEvents []AgentContextEvent
}

func (s *Store) LoadAgentRunContext(
	ctx context.Context,
	workspaceID, runID, runnerSourceID uuid.UUID,
	eventLimit int,
) (AgentRunContext, error) {
	if eventLimit <= 0 || eventLimit > 100 {
		eventLimit = 50
	}
	var result AgentRunContext
	var normalColumn, ongoingState pgtype.Text
	var triggerEventContent pgtype.Text
	var scheduledFor pgtype.Timestamptz
	var triggerCause string
	err := s.pool.QueryRow(ctx, `
		SELECT
			w.id, w.name,
			p.id, p.name, p.description, p.instructions,
			c.id, c.kind, c.title, c.description, c.normal_column, c.ongoing_state,
			c.required_confirmations,
			t.id, t.cause, cause_event.content, t.scheduled_for, t.created_at,
			(
				SELECT count(*)
				FROM triggers pending
				WHERE pending.workspace_id = c.workspace_id
				  AND pending.card_id = c.id
				  AND pending.cancelled_at IS NULL
				  AND NOT EXISTS (
					SELECT 1 FROM agent_runs existing
					WHERE existing.workspace_id = pending.workspace_id
					  AND existing.trigger_id = pending.id
				  )
			)
		FROM agent_runs r
		JOIN workspaces w ON w.id = r.workspace_id
		JOIN cards c ON c.workspace_id = r.workspace_id AND c.id = r.card_id
		JOIN projects p ON p.workspace_id = c.workspace_id AND p.id = c.project_id
		JOIN triggers t ON t.workspace_id = r.workspace_id AND t.id = r.trigger_id
		LEFT JOIN events cause_event
		  ON cause_event.workspace_id = t.workspace_id
		 AND cause_event.card_id = t.card_id
		 AND cause_event.id = t.cause_event_id
		WHERE r.workspace_id=$1
		  AND r.id=$2
		  AND r.runner_source_id=$3
		  AND r.status='running'
	`, workspaceID, runID, runnerSourceID).Scan(
		&result.WorkspaceID, &result.WorkspaceName,
		&result.ProjectID, &result.ProjectName, &result.ProjectDescription, &result.ProjectInstructions,
		&result.CardID, &result.CardKind, &result.CardTitle, &result.CardDescription,
		&normalColumn, &ongoingState, &result.RequiredConfirmations,
		&result.TriggerID, &triggerCause, &triggerEventContent, &scheduledFor, &result.TriggeredAt,
		&result.PendingTriggerCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRunContext{}, domain.ErrRunNotCurrent
	}
	if err != nil {
		return AgentRunContext{}, err
	}
	result.TriggerCause = domain.TriggerCause(triggerCause)
	if normalColumn.Valid {
		result.NormalColumn = normalColumn.String
	}
	if ongoingState.Valid {
		result.OngoingState = ongoingState.String
	}
	if triggerEventContent.Valid {
		result.TriggerContent = triggerEventContent.String
	} else if scheduledFor.Valid {
		result.TriggerContent = fmt.Sprintf("Scheduled follow-up for %s", scheduledFor.Time.UTC().Format(time.RFC3339))
	} else {
		result.TriggerContent = "Continue the queued CycleDock work for this Card."
	}

	rows, err := s.pool.Query(ctx, `
		SELECT e.sequence, e.event_type, src.name, src.kind, e.content,
		       COALESCE(e.checkin_status, ''), e.created_at
		FROM events e
		JOIN sources src
		  ON src.workspace_id = e.workspace_id
		 AND src.id = e.actor_source_id
		WHERE e.workspace_id=$1
		  AND e.card_id=$2
		  AND e.event_type IN (
			'comment','progress','finding','result','checkin','confirm','reject','status_change'
		  )
		ORDER BY e.sequence DESC
		LIMIT $3
	`, workspaceID, result.CardID, eventLimit+1)
	if err != nil {
		return AgentRunContext{}, err
	}
	defer rows.Close()

	reverse := make([]AgentContextEvent, 0, eventLimit+1)
	for rows.Next() {
		var event AgentContextEvent
		var eventType, actorKind string
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(
			&event.Sequence, &eventType, &event.ActorName, &actorKind,
			&event.Content, &event.CheckinStatus, &createdAt,
		); err != nil {
			return AgentRunContext{}, err
		}
		event.Type = domain.EventType(eventType)
		event.ActorKind = domain.SourceKind(actorKind)
		event.CreatedAt = createdAt.Time
		reverse = append(reverse, event)
	}
	if err := rows.Err(); err != nil {
		return AgentRunContext{}, err
	}
	hasOlder := len(reverse) > eventLimit
	if hasOlder {
		reverse = reverse[:eventLimit]
	}
	result.Events = make([]AgentContextEvent, len(reverse))
	for i := range reverse {
		result.Events[len(reverse)-1-i] = reverse[i]
	}
	if hasOlder && len(result.Events) > 0 {
		if err := s.loadHistoricalAgentContext(
			ctx, workspaceID, result.CardID, result.Events[0].Sequence, &result,
		); err != nil {
			return AgentRunContext{}, err
		}
	}
	return result, nil
}

func (s *Store) loadHistoricalAgentContext(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	beforeSequence int64,
	result *AgentRunContext,
) error {
	result.HistoryCompaction.BeforeSequence = beforeSequence

	factRows, err := s.pool.Query(ctx, `
		SELECT e.sequence, e.event_type, src.name, src.kind, e.content,
		       COALESCE(e.checkin_status, ''), e.created_at
		FROM events e
		JOIN sources src
		  ON src.workspace_id = e.workspace_id
		 AND src.id = e.actor_source_id
		WHERE e.workspace_id=$1
		  AND e.card_id=$2
		  AND e.sequence < $3
		  AND e.event_type IN ('finding','result','checkin','confirm','reject','status_change')
		ORDER BY e.sequence
	`, workspaceID, cardID, beforeSequence)
	if err != nil {
		return err
	}
	result.HistoricalFacts, err = scanAgentContextEvents(factRows)
	factRows.Close()
	if err != nil {
		return err
	}

	if err := s.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE event_type = 'comment'),
			count(*) FILTER (WHERE event_type = 'progress')
		FROM events
		WHERE workspace_id=$1
		  AND card_id=$2
		  AND sequence < $3
		  AND event_type IN ('comment','progress')
	`, workspaceID, cardID, beforeSequence).Scan(
		&result.HistoryCompaction.OlderCommentCount,
		&result.HistoryCompaction.OlderProgressCount,
	); err != nil {
		return err
	}

	compactRows, err := s.pool.Query(ctx, `
		SELECT e.sequence, e.event_type, src.name, src.kind, e.content,
		       COALESCE(e.checkin_status, ''), e.created_at
		FROM events e
		JOIN sources src
		  ON src.workspace_id = e.workspace_id
		 AND src.id = e.actor_source_id
		WHERE e.workspace_id=$1
		  AND e.card_id=$2
		  AND e.sequence < $3
		  AND e.event_type IN ('comment','progress')
		ORDER BY e.sequence DESC
		LIMIT 8
	`, workspaceID, cardID, beforeSequence)
	if err != nil {
		return err
	}
	recentReverse, err := scanAgentContextEvents(compactRows)
	compactRows.Close()
	if err != nil {
		return err
	}
	result.HistoryCompaction.RecentContextEvents = make([]AgentContextEvent, len(recentReverse))
	for i := range recentReverse {
		result.HistoryCompaction.RecentContextEvents[len(recentReverse)-1-i] = recentReverse[i]
	}
	return nil
}

func scanAgentContextEvents(rows pgx.Rows) ([]AgentContextEvent, error) {
	events := make([]AgentContextEvent, 0)
	for rows.Next() {
		var event AgentContextEvent
		var eventType, actorKind string
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(
			&event.Sequence, &eventType, &event.ActorName, &actorKind,
			&event.Content, &event.CheckinStatus, &createdAt,
		); err != nil {
			return nil, err
		}
		event.Type = domain.EventType(eventType)
		event.ActorKind = domain.SourceKind(actorKind)
		event.CreatedAt = createdAt.Time
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) ListEnabledAgentSettings(ctx context.Context) ([]db.WorkspaceAgentSetting, error) {
	return s.queries.ListEnabledWorkspaceAgentSettings(ctx)
}

func (s *Store) ListWorkspaceIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, "SELECT id FROM workspaces ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
