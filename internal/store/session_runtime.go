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

	"github.com/uvwt/CycleDock/internal/domain"
)

type AgentCardContext struct {
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
	DiscussionState       string
	TodoState             string
	OngoingState          string
	RequiredConfirmations int32

	TurnID        uuid.UUID
	TurnSource    domain.TurnSource
	TurnContent   string
	DispatchMode  domain.DispatchMode
	TurnCreatedAt time.Time

	AttemptID        uuid.UUID
	AttemptNumber    int
	AttemptReason    domain.AttemptReason
	RunnerSourceID   uuid.UUID
	PendingTurnCount int64
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
	var turnSource, dispatchMode, attemptReason string
	var createdAt pgtype.Timestamptz

	err := s.pool.QueryRow(ctx, `
		SELECT
			w.id, w.name,
			p.id, p.name, p.description, p.instructions,
			c.id, c.kind, c.title, c.description,
			c.discussion_state, c.todo_state, c.ongoing_state,
			c.required_confirmations,
			t.id, t.source, t.content, t.dispatch_mode, t.created_at,
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
		&out.ProjectID, &out.ProjectName, &out.ProjectDescription, &out.ProjectInstructions,
		&out.CardID, &out.CardKind, &out.CardTitle, &out.CardDescription,
		&discussionState, &todoState, &ongoingState, &out.RequiredConfirmations,
		&out.TurnID, &turnSource, &out.TurnContent, &dispatchMode, &createdAt,
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
	return out, nil
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
	p.Stage = strings.TrimSpace(p.Stage)
	p.Summary = strings.TrimSpace(p.Summary)
	p.EventContent = strings.TrimSpace(p.EventContent)
	p.IdempotencyKey = strings.TrimSpace(p.IdempotencyKey)
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
	case domain.EventTypeFinding, domain.EventTypeCheckin:
	default:
		return uuid.Nil, false, fmt.Errorf("unsupported attempt event type %q", p.Type)
	}
	p.Content = strings.TrimSpace(p.Content)
	p.IdempotencyKey = strings.TrimSpace(p.IdempotencyKey)
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

func (s *Store) WaitAttemptForHumanWithPrompt(
	ctx context.Context,
	workspaceID, cardID, attemptID, runnerSourceID uuid.UUID,
	stage, summary string,
	now time.Time,
) error {
	stage = strings.TrimSpace(stage)
	summary = strings.TrimSpace(summary)
	if stage == "" || summary == "" {
		return fmt.Errorf("%w: stage and summary are required", domain.ErrConflict)
	}
	now = now.UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	var status domain.AttemptStatus
	var actualRunner uuid.UUID
	var currentStage, progressSummary string
	err = tx.QueryRow(ctx, `
		SELECT status, runner_source_id, current_stage, progress_summary
		FROM turn_attempts
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		FOR UPDATE
	`, workspaceID, cardID, attemptID).Scan(
		&status, &actualRunner, &currentStage, &progressSummary,
	)
	if errors.Is(err, pgx.ErrNoRows) || actualRunner != runnerSourceID {
		return domain.ErrAttemptNotCurrent
	}
	if err != nil {
		return err
	}
	if status == domain.AttemptStatusWaitingHuman &&
		currentStage == stage && progressSummary == summary {
		return tx.Commit(ctx)
	}
	if status != domain.AttemptStatusRunning {
		return domain.ErrAttemptNotCurrent
	}
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status='waiting_human',
		    current_stage=$5,
		    progress_summary=$6,
		    heartbeat_at=NULL,
		    lease_expires_at=NULL,
		    updated_at=$7
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND runner_source_id=$4 AND status='running'
	`, workspaceID, cardID, attemptID, runnerSourceID, stage, summary, now)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrAttemptNotCurrent
	}
	return tx.Commit(ctx)
}

func (s *Store) CompleteAttempt(
	ctx context.Context,
	workspaceID, cardID, attemptID, runnerSourceID uuid.UUID,
	result string,
	now time.Time,
) error {
	result = strings.TrimSpace(result)
	now = now.UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return err
	}
	if err := requireCurrentAttempt(
		ctx, tx, workspaceID, cardID, attemptID, runnerSourceID,
		domain.AttemptStatusRunning,
	); err != nil {
		return err
	}
	if result != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO events (
				id, workspace_id, card_id, event_type, actor_source_id, attempt_id, content
			)
			VALUES ($1,$2,$3,'result',$4,$5,$6)
		`, uuid.New(), workspaceID, cardID, runnerSourceID, attemptID, result); err != nil {
			return err
		}
	}
	command, err := tx.Exec(ctx, `
		UPDATE turn_attempts
		SET status='completed',
		    result_summary=$5,
		    heartbeat_at=NULL,
		    lease_expires_at=NULL,
		    finished_at=$6,
		    updated_at=$6
		WHERE workspace_id=$1 AND card_id=$2 AND id=$3
		  AND runner_source_id=$4 AND status='running'
	`, workspaceID, cardID, attemptID, runnerSourceID, result, now)
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
