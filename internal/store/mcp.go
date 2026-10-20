package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type AgentCardCreateParams struct {
	WorkspaceID           uuid.UUID
	ProjectID             uuid.UUID
	SourceID              uuid.UUID
	Kind                  domain.CardKind
	Title                 string
	Description           string
	RequiredConfirmations int32
	IdempotencyKey        string
}

type AgentCardCreateResult struct {
	Card   db.Card
	Reused bool
}

func (s *Store) ListWorkspaceCardSummaries(ctx context.Context, workspaceID uuid.UUID) ([]db.ListWorkspaceCardSummariesRow, error) {
	return s.queries.ListWorkspaceCardSummaries(ctx, workspaceID)
}

func (s *Store) ListRecentCardTimeline(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	limit int32,
) ([]db.ListRecentCardTimelineRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if _, err := s.GetCard(ctx, workspaceID, cardID); err != nil {
		return nil, err
	}
	return s.queries.ListRecentCardTimeline(ctx, db.ListRecentCardTimelineParams{
		WorkspaceID: workspaceID,
		CardID:      cardID,
		EventLimit:  limit,
	})
}

func (s *Store) GetRun(ctx context.Context, workspaceID, runID uuid.UUID) (db.AgentRun, error) {
	run, err := s.queries.GetRunByID(ctx, db.GetRunByIDParams{WorkspaceID: workspaceID, ID: runID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.AgentRun{}, domain.ErrNotFound
	}
	return run, err
}

func (s *Store) CreateAgentCard(ctx context.Context, p AgentCardCreateParams) (AgentCardCreateResult, error) {
	p.Title = strings.TrimSpace(p.Title)
	p.Description = strings.TrimSpace(p.Description)
	p.IdempotencyKey = strings.TrimSpace(p.IdempotencyKey)
	if p.Title == "" || p.IdempotencyKey == "" {
		return AgentCardCreateResult{}, fmt.Errorf("%w: title and idempotency key are required", domain.ErrConflict)
	}
	if p.RequiredConfirmations <= 0 {
		p.RequiredConfirmations = 1
	}
	if p.Kind != domain.CardKindNormal && p.Kind != domain.CardKindOngoing {
		return AgentCardCreateResult{}, fmt.Errorf("%w: unsupported card kind", domain.ErrInvalidTransition)
	}
	requestHash := stableRequestHash(
		"create_card", p.WorkspaceID.String(), p.ProjectID.String(), p.SourceID.String(),
		string(p.Kind), p.Title, p.Description, fmt.Sprint(p.RequiredConfirmations),
	)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AgentCardCreateResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockRequestKey(ctx, tx, p.WorkspaceID, p.SourceID, p.IdempotencyKey); err != nil {
		return AgentCardCreateResult{}, err
	}
	if err := ensureAgentSource(ctx, tx, p.WorkspaceID, p.SourceID); err != nil {
		return AgentCardCreateResult{}, err
	}
	var projectID uuid.UUID
	if err := tx.QueryRow(ctx,
		"SELECT id FROM projects WHERE workspace_id = $1 AND id = $2",
		p.WorkspaceID, p.ProjectID,
	).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentCardCreateResult{}, domain.ErrNotFound
		}
		return AgentCardCreateResult{}, err
	}

	q := db.New(tx)
	existing, err := q.GetCreatedCardRequest(ctx, db.GetCreatedCardRequestParams{
		WorkspaceID:          p.WorkspaceID,
		CreatedBySourceID:    p.SourceID,
		CreateIdempotencyKey: pgtype.Text{String: p.IdempotencyKey, Valid: true},
	})
	if err == nil {
		if !existing.CreateRequestHash.Valid || existing.CreateRequestHash.String != requestHash {
			return AgentCardCreateResult{}, domain.ErrIdempotencyMismatch
		}
		if err := tx.Commit(ctx); err != nil {
			return AgentCardCreateResult{}, err
		}
		return AgentCardCreateResult{Card: existing, Reused: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AgentCardCreateResult{}, err
	}

	var normalColumn, ongoingState pgtype.Text
	if p.Kind == domain.CardKindNormal {
		normalColumn = pgtype.Text{String: string(domain.NormalColumnDiscussion), Valid: true}
	} else {
		ongoingState = pgtype.Text{String: string(domain.OngoingStateActive), Valid: true}
	}
	card, err := q.CreateAgentCard(ctx, db.CreateAgentCardParams{
		ID: uuid.New(), WorkspaceID: p.WorkspaceID, ProjectID: p.ProjectID,
		Kind: string(p.Kind), Title: p.Title, Description: p.Description,
		NormalColumn: normalColumn, OngoingState: ongoingState,
		RequiredConfirmations: p.RequiredConfirmations, CreatedBySourceID: p.SourceID,
		CreateIdempotencyKey: pgtype.Text{String: p.IdempotencyKey, Valid: true},
		CreateRequestHash:    pgtype.Text{String: requestHash, Valid: true},
	})
	if err != nil {
		return AgentCardCreateResult{}, mapConstraintError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentCardCreateResult{}, err
	}
	return AgentCardCreateResult{Card: card}, nil
}

func (s *Store) AddAgentComment(
	ctx context.Context,
	workspaceID, cardID, sourceID uuid.UUID,
	content, idempotencyKey string,
) (uuid.UUID, bool, error) {
	content = strings.TrimSpace(content)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if content == "" || idempotencyKey == "" {
		return uuid.Nil, false, fmt.Errorf("%w: content and idempotency key are required", domain.ErrConflict)
	}
	requestHash := stableRequestHash("comment", workspaceID.String(), cardID.String(), sourceID.String(), content)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)
	if err := lockRequestKey(ctx, tx, workspaceID, sourceID, idempotencyKey); err != nil {
		return uuid.Nil, false, err
	}
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return uuid.Nil, false, err
	}
	if err := ensureAgentSource(ctx, tx, workspaceID, sourceID); err != nil {
		return uuid.Nil, false, err
	}

	q := db.New(tx)
	event, err := q.GetAgentEventRequest(ctx, db.GetAgentEventRequestParams{
		WorkspaceID: workspaceID, ActorSourceID: sourceID,
		EventType:      string(domain.EventTypeComment),
		IdempotencyKey: pgtype.Text{String: idempotencyKey, Valid: true},
	})
	if err == nil {
		if !event.RequestHash.Valid || event.RequestHash.String != requestHash || event.CardID != cardID {
			return uuid.Nil, false, domain.ErrIdempotencyMismatch
		}
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, false, err
		}
		return event.ID, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}

	event, err = q.CreateAgentEvent(ctx, db.CreateAgentEventParams{
		ID: uuid.New(), WorkspaceID: workspaceID, CardID: cardID,
		EventType: string(domain.EventTypeComment), ActorSourceID: sourceID,
		Content:        content,
		IdempotencyKey: pgtype.Text{String: idempotencyKey, Valid: true},
		RequestHash:    pgtype.Text{String: requestHash, Valid: true},
	})
	if err != nil {
		return uuid.Nil, false, mapConstraintError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return event.ID, false, nil
}

type FollowupResult struct {
	EventID   uuid.UUID
	TriggerID uuid.UUID
	Reused    bool
}

func (s *Store) CreateAgentFollowup(
	ctx context.Context,
	workspaceID, cardID, sourceID uuid.UUID,
	content, idempotencyKey string,
) (FollowupResult, error) {
	content = strings.TrimSpace(content)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if content == "" || idempotencyKey == "" {
		return FollowupResult{}, fmt.Errorf("%w: content and idempotency key are required", domain.ErrConflict)
	}
	requestHash := stableRequestHash("followup", workspaceID.String(), cardID.String(), sourceID.String(), content)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FollowupResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockRequestKey(ctx, tx, workspaceID, sourceID, idempotencyKey); err != nil {
		return FollowupResult{}, err
	}
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return FollowupResult{}, err
	}
	if err := ensureAgentSource(ctx, tx, workspaceID, sourceID); err != nil {
		return FollowupResult{}, err
	}
	q := db.New(tx)
	existing, err := q.GetAgentFollowupRequest(ctx, db.GetAgentFollowupRequestParams{
		WorkspaceID:       workspaceID,
		CreatedBySourceID: uuid.NullUUID{UUID: sourceID, Valid: true},
		IdempotencyKey:    pgtype.Text{String: idempotencyKey, Valid: true},
	})
	if err == nil {
		if !existing.RequestHash.Valid || existing.RequestHash.String != requestHash || existing.CardID != cardID {
			return FollowupResult{}, domain.ErrIdempotencyMismatch
		}
		if err := tx.Commit(ctx); err != nil {
			return FollowupResult{}, err
		}
		return FollowupResult{
			EventID: existing.CauseEventID.UUID, TriggerID: existing.ID, Reused: true,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return FollowupResult{}, err
	}

	event, err := q.CreateAgentEvent(ctx, db.CreateAgentEventParams{
		ID: uuid.New(), WorkspaceID: workspaceID, CardID: cardID,
		EventType: string(domain.EventTypeComment), ActorSourceID: sourceID,
		Content:        content,
		IdempotencyKey: pgtype.Text{String: idempotencyKey, Valid: true},
		RequestHash:    pgtype.Text{String: requestHash, Valid: true},
	})
	if err != nil {
		return FollowupResult{}, mapConstraintError(err)
	}
	trigger, err := q.CreateAgentFollowupTrigger(ctx, db.CreateAgentFollowupTriggerParams{
		ID: uuid.New(), WorkspaceID: workspaceID, CardID: cardID,
		Cause:             string(domain.TriggerCauseSystemFollowup),
		CauseEventID:      uuid.NullUUID{UUID: event.ID, Valid: true},
		CreatedBySourceID: uuid.NullUUID{UUID: sourceID, Valid: true},
		IdempotencyKey:    pgtype.Text{String: idempotencyKey, Valid: true},
		RequestHash:       pgtype.Text{String: requestHash, Valid: true},
	})
	if err != nil {
		return FollowupResult{}, mapConstraintError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return FollowupResult{}, err
	}
	return FollowupResult{EventID: event.ID, TriggerID: trigger.ID}, nil
}

type ClaimWorkResult struct {
	Run    RunSnapshot
	Reused bool
}

func (s *Store) ClaimNextTriggerAndStart(
	ctx context.Context,
	workspaceID, cardID, runnerSourceID uuid.UUID,
	leaseDuration time.Duration,
) (ClaimWorkResult, error) {
	if leaseDuration <= 0 {
		leaseDuration = DefaultClaimLease
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ClaimWorkResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return ClaimWorkResult{}, err
	}
	if err := ensureAgentSource(ctx, tx, workspaceID, runnerSourceID); err != nil {
		return ClaimWorkResult{}, err
	}

	var current RunSnapshot
	var status string
	err = tx.QueryRow(ctx, "SELECT id, card_id, trigger_id, runner_source_id, status FROM agent_runs WHERE workspace_id = $1 AND card_id = $2 AND status IN ('queued','running','waiting_human') FOR UPDATE",
		workspaceID, cardID,
	).Scan(&current.ID, &current.CardID, &current.TriggerID, &current.RunnerSourceID, &status)
	if err == nil {
		current.Status = domain.RunStatus(status)
		if current.RunnerSourceID == runnerSourceID {
			if err := tx.Commit(ctx); err != nil {
				return ClaimWorkResult{}, err
			}
			return ClaimWorkResult{Run: current, Reused: true}, nil
		}
		return ClaimWorkResult{}, fmt.Errorf("%w: card already has a current run", domain.ErrConflict)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ClaimWorkResult{}, err
	}

	var triggerID uuid.UUID
	err = tx.QueryRow(ctx, "SELECT t.id FROM triggers t WHERE t.workspace_id = $1 AND t.card_id = $2 AND t.cancelled_at IS NULL AND NOT EXISTS (SELECT 1 FROM agent_runs r WHERE r.workspace_id = t.workspace_id AND r.trigger_id = t.id) ORDER BY t.sequence LIMIT 1 FOR UPDATE",
		workspaceID, cardID,
	).Scan(&triggerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimWorkResult{}, domain.ErrNotFound
	}
	if err != nil {
		return ClaimWorkResult{}, err
	}

	now := time.Now().UTC()
	runID := uuid.New()
	_, err = tx.Exec(ctx, "INSERT INTO agent_runs (id, workspace_id, card_id, trigger_id, runner_source_id, status, started_at, heartbeat_at, lease_expires_at) VALUES ($1,$2,$3,$4,$5,'running',$6,$6,$7)",
		runID, workspaceID, cardID, triggerID, runnerSourceID, now, now.Add(leaseDuration),
	)
	if err != nil {
		return ClaimWorkResult{}, mapConstraintError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ClaimWorkResult{}, err
	}
	return ClaimWorkResult{Run: RunSnapshot{
		ID: runID, CardID: cardID, TriggerID: triggerID,
		RunnerSourceID: runnerSourceID, Status: domain.RunStatusRunning,
	}}, nil
}

type ProgressParams struct {
	WorkspaceID    uuid.UUID
	RunID          uuid.UUID
	RunnerSourceID uuid.UUID
	Stage          string
	Summary        string
	EventContent   string
	IdempotencyKey string
	LeaseDuration  time.Duration
}

func (s *Store) ReportProgress(ctx context.Context, p ProgressParams) (uuid.UUID, bool, error) {
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
	cardID, status, actualRunner, err := lockRunCard(ctx, tx, p.WorkspaceID, p.RunID)
	if err != nil {
		return uuid.Nil, false, err
	}
	if actualRunner != p.RunnerSourceID || status != domain.RunStatusRunning {
		return uuid.Nil, false, domain.ErrRunNotCurrent
	}

	var eventID uuid.UUID
	var reused bool
	if p.EventContent != "" {
		requestHash := stableRequestHash("progress", p.WorkspaceID.String(), p.RunID.String(), p.RunnerSourceID.String(), p.Stage, p.Summary, p.EventContent)
		q := db.New(tx)
		existing, err := q.GetAgentEventRequest(ctx, db.GetAgentEventRequestParams{
			WorkspaceID: p.WorkspaceID, ActorSourceID: p.RunnerSourceID,
			EventType:      string(domain.EventTypeProgress),
			IdempotencyKey: pgtype.Text{String: p.IdempotencyKey, Valid: true},
		})
		if err == nil {
			if !existing.RequestHash.Valid || existing.RequestHash.String != requestHash ||
				existing.CardID != cardID || !existing.RunID.Valid || existing.RunID.UUID != p.RunID {
				return uuid.Nil, false, domain.ErrIdempotencyMismatch
			}
			eventID, reused = existing.ID, true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, err
		} else {
			event, err := q.CreateAgentEvent(ctx, db.CreateAgentEventParams{
				ID: uuid.New(), WorkspaceID: p.WorkspaceID, CardID: cardID,
				EventType: string(domain.EventTypeProgress), ActorSourceID: p.RunnerSourceID,
				RunID: uuid.NullUUID{UUID: p.RunID, Valid: true}, Content: p.EventContent,
				IdempotencyKey: pgtype.Text{String: p.IdempotencyKey, Valid: true},
				RequestHash:    pgtype.Text{String: requestHash, Valid: true},
			})
			if err != nil {
				return uuid.Nil, false, mapConstraintError(err)
			}
			eventID = event.ID
		}
	}

	now := time.Now().UTC()
	command, err := tx.Exec(ctx, "UPDATE agent_runs SET current_stage=$4, progress_summary=$5, heartbeat_at=$6, lease_expires_at=$7, updated_at=$6 WHERE workspace_id=$1 AND id=$2 AND runner_source_id=$3 AND status='running'",
		p.WorkspaceID, p.RunID, p.RunnerSourceID, p.Stage, p.Summary, now, now.Add(p.LeaseDuration),
	)
	if err != nil {
		return uuid.Nil, false, err
	}
	if command.RowsAffected() != 1 {
		return uuid.Nil, false, domain.ErrRunNotCurrent
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return eventID, reused, nil
}

type ConfirmResult struct {
	EventID           uuid.UUID
	ConfirmationCount int64
	MovedToReview     bool
	Reused            bool
}

func (s *Store) ConfirmCard(
	ctx context.Context,
	workspaceID, cardID, sourceID uuid.UUID,
	content, idempotencyKey string,
) (ConfirmResult, error) {
	content = strings.TrimSpace(content)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if content == "" || idempotencyKey == "" {
		return ConfirmResult{}, fmt.Errorf("%w: content and idempotency key are required", domain.ErrConflict)
	}
	requestHash := stableRequestHash("confirm", workspaceID.String(), cardID.String(), sourceID.String(), content)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ConfirmResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockCard(ctx, tx, workspaceID, cardID); err != nil {
		return ConfirmResult{}, err
	}
	if err := ensureAgentSource(ctx, tx, workspaceID, sourceID); err != nil {
		return ConfirmResult{}, err
	}
	q := db.New(tx)

	existing, err := q.GetAgentEventRequest(ctx, db.GetAgentEventRequestParams{
		WorkspaceID: workspaceID, ActorSourceID: sourceID,
		EventType:      string(domain.EventTypeConfirm),
		IdempotencyKey: pgtype.Text{String: idempotencyKey, Valid: true},
	})
	if err == nil {
		if !existing.RequestHash.Valid || existing.RequestHash.String != requestHash || existing.CardID != cardID {
			return ConfirmResult{}, domain.ErrIdempotencyMismatch
		}
		count, err := q.CountCardConfirmations(ctx, db.CountCardConfirmationsParams{
			WorkspaceID: workspaceID, CardID: cardID,
		})
		if err != nil {
			return ConfirmResult{}, err
		}
		var column string
		if err := tx.QueryRow(ctx,
			"SELECT normal_column FROM cards WHERE workspace_id=$1 AND id=$2",
			workspaceID, cardID,
		).Scan(&column); err != nil {
			return ConfirmResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ConfirmResult{}, err
		}
		return ConfirmResult{
			EventID: existing.ID, ConfirmationCount: count,
			MovedToReview: column == string(domain.NormalColumnReview), Reused: true,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ConfirmResult{}, err
	}

	var kind, column string
	var required int32
	if err := tx.QueryRow(ctx,
		"SELECT kind, normal_column, required_confirmations FROM cards WHERE workspace_id=$1 AND id=$2",
		workspaceID, cardID,
	).Scan(&kind, &column, &required); err != nil {
		return ConfirmResult{}, err
	}
	if kind != string(domain.CardKindNormal) || column != string(domain.NormalColumnWorking) {
		return ConfirmResult{}, fmt.Errorf("%w: confirm requires a Working normal card", domain.ErrInvalidTransition)
	}

	event, err := q.CreateAgentEvent(ctx, db.CreateAgentEventParams{
		ID: uuid.New(), WorkspaceID: workspaceID, CardID: cardID,
		EventType: string(domain.EventTypeConfirm), ActorSourceID: sourceID,
		Content:        content,
		IdempotencyKey: pgtype.Text{String: idempotencyKey, Valid: true},
		RequestHash:    pgtype.Text{String: requestHash, Valid: true},
	})
	if err != nil {
		return ConfirmResult{}, mapConstraintError(err)
	}
	count, err := q.CountCardConfirmations(ctx, db.CountCardConfirmationsParams{
		WorkspaceID: workspaceID, CardID: cardID,
	})
	if err != nil {
		return ConfirmResult{}, err
	}
	moved := count >= int64(required)
	if moved {
		command, err := tx.Exec(ctx,
			"UPDATE cards SET normal_column='review', updated_at=now() WHERE workspace_id=$1 AND id=$2 AND normal_column='working'",
			workspaceID, cardID,
		)
		if err != nil {
			return ConfirmResult{}, err
		}
		if command.RowsAffected() != 1 {
			return ConfirmResult{}, domain.ErrConflict
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO events (id, workspace_id, card_id, event_type, actor_source_id, content) VALUES ($1,$2,$3,'status_change',$4,'working -> review')",
			uuid.New(), workspaceID, cardID, sourceID,
		); err != nil {
			return ConfirmResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ConfirmResult{}, err
	}
	return ConfirmResult{
		EventID: event.ID, ConfirmationCount: count, MovedToReview: moved,
	}, nil
}

func ensureAgentSource(ctx context.Context, tx pgx.Tx, workspaceID, sourceID uuid.UUID) error {
	var kind string
	err := tx.QueryRow(ctx, "SELECT kind FROM sources WHERE workspace_id=$1 AND id=$2", workspaceID, sourceID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if kind != string(domain.SourceKindOAuthClient) && kind != string(domain.SourceKindCycleAgent) {
		return domain.ErrHumanRequired
	}
	return nil
}

func lockRequestKey(ctx context.Context, tx pgx.Tx, workspaceID, sourceID uuid.UUID, key string) error {
	lockKey := "cycledock/request/" + workspaceID.String() + "/" + sourceID.String() + "/" + key
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey)
	return err
}

func stableRequestHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
