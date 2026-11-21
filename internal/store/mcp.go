package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type AgentDiscussionCreateParams struct {
	WorkspaceID    uuid.UUID
	ProjectID      uuid.UUID
	SourceID       uuid.UUID
	IdempotencyKey string
}

type GenerateWorkParams struct {
	WorkspaceID           uuid.UUID
	DiscussionID          uuid.UUID
	SourceID              uuid.UUID
	Kind                  domain.CardKind
	Title                 string
	Description           string
	RequiredConfirmations int32
	// AutoStart 控制派生卡片后是否立即排入首轮系统执行；nil 默认为 true，传 false 时仅建卡留档不唤醒调度器。
	AutoStart      *bool
	IdempotencyKey string
}

// buildInitialWorkTurnContent 生成待办/定时卡片首轮系统执行的统一输入文案。
func buildInitialWorkTurnContent(title, description string) string {
	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)
	if description == "" {
		return fmt.Sprintf("# %s", title)
	}
	return fmt.Sprintf("# %s\n\n%s", title, description)
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

func (s *Store) CreateAgentDiscussion(
	ctx context.Context,
	p AgentDiscussionCreateParams,
) (AgentCardCreateResult, error) {
	p.IdempotencyKey = strings.TrimSpace(p.IdempotencyKey)
	if p.IdempotencyKey == "" {
		return AgentCardCreateResult{}, fmt.Errorf("%w: idempotency key is required", domain.ErrConflict)
	}
	requestHash := stableRequestHash(
		"create_discussion", p.WorkspaceID.String(), p.ProjectID.String(), p.SourceID.String(),
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

	q := db.New(tx)
	if existing, reused, err := findCreatedCardRetry(
		ctx, q, p.WorkspaceID, p.SourceID, p.IdempotencyKey, requestHash,
	); err != nil || reused {
		if err != nil {
			return AgentCardCreateResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return AgentCardCreateResult{}, err
		}
		return AgentCardCreateResult{Card: existing, Reused: true}, nil
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

	card, err := q.CreateAgentCard(ctx, db.CreateAgentCardParams{
		ID:                    uuid.New(),
		WorkspaceID:           p.WorkspaceID,
		ProjectID:             p.ProjectID,
		Kind:                  string(domain.CardKindDiscussion),
		DiscussionState:       pgtype.Text{String: string(domain.DiscussionStateActive), Valid: true},
		RequiredConfirmations: 1,
		CreatedBySourceID:     p.SourceID,
		CreateIdempotencyKey:  pgtype.Text{String: p.IdempotencyKey, Valid: true},
		CreateRequestHash:     pgtype.Text{String: requestHash, Valid: true},
	})
	if err != nil {
		return AgentCardCreateResult{}, mapConstraintError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentCardCreateResult{}, err
	}
	return AgentCardCreateResult{Card: card}, nil
}

func (s *Store) GenerateWorkFromDiscussion(
	ctx context.Context,
	p GenerateWorkParams,
) (AgentCardCreateResult, error) {
	p.Title, _ = agentdata.NormalizeText(strings.TrimSpace(p.Title))
	p.Description, _ = agentdata.NormalizeText(strings.TrimSpace(p.Description))
	p.IdempotencyKey, _ = agentdata.NormalizeText(strings.TrimSpace(p.IdempotencyKey))
	if p.Title == "" || p.IdempotencyKey == "" {
		return AgentCardCreateResult{}, fmt.Errorf("%w: title and idempotency key are required", domain.ErrConflict)
	}
	if p.Kind != domain.CardKindTodo && p.Kind != domain.CardKindOngoing {
		return AgentCardCreateResult{}, fmt.Errorf("%w: generated work must be todo or ongoing", domain.ErrInvalidTransition)
	}
	if p.RequiredConfirmations <= 0 || p.Kind == domain.CardKindOngoing {
		p.RequiredConfirmations = 1
	}
	autoStart := true
	if p.AutoStart != nil {
		autoStart = *p.AutoStart
	}
	requestHash := stableRequestHash(
		"generate_work", p.WorkspaceID.String(), p.DiscussionID.String(), p.SourceID.String(),
		string(p.Kind), p.Title, p.Description, fmt.Sprint(p.RequiredConfirmations),
	)
	if !autoStart {
		// 保持 auto_start=true / 未传与历史哈希一致，仅在显式关闭自动启动时区分幂等哈希。
		requestHash = stableRequestHash(requestHash, "auto_start", "false")
	}

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

	q := db.New(tx)
	if existing, reused, err := findCreatedCardRetry(
		ctx, q, p.WorkspaceID, p.SourceID, p.IdempotencyKey, requestHash,
	); err != nil || reused {
		if err != nil {
			return AgentCardCreateResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return AgentCardCreateResult{}, err
		}
		return AgentCardCreateResult{Card: existing, Reused: true}, nil
	}

	var projectID uuid.UUID
	var kind, discussionState string
	if err := tx.QueryRow(ctx, `
		SELECT project_id, kind, COALESCE(discussion_state, '')
		FROM cards
		WHERE workspace_id = $1 AND id = $2
		FOR UPDATE
	`, p.WorkspaceID, p.DiscussionID).Scan(&projectID, &kind, &discussionState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentCardCreateResult{}, domain.ErrNotFound
		}
		return AgentCardCreateResult{}, err
	}
	if kind != string(domain.CardKindDiscussion) ||
		discussionState != string(domain.DiscussionStateActive) {
		return AgentCardCreateResult{}, fmt.Errorf(
			"%w: generated work requires an active discussion", domain.ErrInvalidTransition,
		)
	}

	var todoState, ongoingState pgtype.Text
	if p.Kind == domain.CardKindTodo {
		todoState = pgtype.Text{String: string(domain.TodoStateTodo), Valid: true}
	} else {
		ongoingState = pgtype.Text{String: string(domain.OngoingStateActive), Valid: true}
	}
	card, err := q.CreateAgentCard(ctx, db.CreateAgentCardParams{
		ID:                    uuid.New(),
		WorkspaceID:           p.WorkspaceID,
		ProjectID:             projectID,
		Kind:                  string(p.Kind),
		Title:                 p.Title,
		Description:           p.Description,
		TodoState:             todoState,
		OngoingState:          ongoingState,
		SourceDiscussionID:    uuid.NullUUID{UUID: p.DiscussionID, Valid: true},
		RequiredConfirmations: p.RequiredConfirmations,
		CreatedBySourceID:     p.SourceID,
		CreateIdempotencyKey:  pgtype.Text{String: p.IdempotencyKey, Valid: true},
		CreateRequestHash:     pgtype.Text{String: requestHash, Valid: true},
	})
	if err != nil {
		return AgentCardCreateResult{}, mapConstraintError(err)
	}

	var sourceKind string
	if err := tx.QueryRow(ctx, "SELECT kind FROM sources WHERE workspace_id=$1 AND id=$2", p.WorkspaceID, p.SourceID).Scan(&sourceKind); err != nil {
		return AgentCardCreateResult{}, err
	}
	shouldStart := autoStart && sourceKind == string(domain.SourceKindCycleAgent)
	if shouldStart {
		initialTurnContent := buildInitialWorkTurnContent(p.Title, p.Description)
		turnRequestHash := stableRequestHash(
			"agent_turn", p.WorkspaceID.String(), card.ID.String(), p.SourceID.String(),
			string(domain.DispatchModeEnqueue), initialTurnContent,
		)
		if _, err := insertTurn(ctx, tx, insertTurnParams{
			WorkspaceID:       p.WorkspaceID,
			CardID:            card.ID,
			Source:            domain.TurnSourceSystem,
			Content:           initialTurnContent,
			CreatedBySourceID: uuid.NullUUID{UUID: p.SourceID, Valid: true},
			DispatchMode:      domain.DispatchModeEnqueue,
			IdempotencyKey:    p.IdempotencyKey + ":initial_turn",
			RequestHash:       turnRequestHash,
		}); err != nil {
			return AgentCardCreateResult{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return AgentCardCreateResult{}, err
	}
	if shouldStart {
		s.notifyWorkReady()
	}
	return AgentCardCreateResult{Card: card}, nil
}

// StartCardExecutionParams 描述人工一键启动待办/定时卡片首轮执行的参数。
type StartCardExecutionParams struct {
	WorkspaceID           uuid.UUID
	CardID                uuid.UUID
	AuthorSourceID        uuid.UUID
	IdempotencyKey        string
	DeviceTarget          *DeviceTarget
	PreferredModelRouteID *uuid.UUID
}

// StartCardExecution 为尚未开始任何执行轮次的待办或定时卡片插入首轮系统 Turn 并唤醒内置 Agent。
// 若卡片已经存在未取消的 Turn 或历史 Attempt，或当前状态不允许启动，则拒绝重复启动。
func (s *Store) StartCardExecution(
	ctx context.Context,
	p StartCardExecutionParams,
) (CreateTurnResult, error) {
	p.IdempotencyKey, _ = agentdata.NormalizeText(strings.TrimSpace(p.IdempotencyKey))
	if p.IdempotencyKey == "" {
		return CreateTurnResult{}, fmt.Errorf("%w: idempotency key is required", domain.ErrConflict)
	}

	var deviceNodeID, deviceName string
	if p.DeviceTarget != nil {
		deviceNodeID, _ = agentdata.NormalizeText(strings.TrimSpace(p.DeviceTarget.NodeID))
		deviceName, _ = agentdata.NormalizeText(strings.TrimSpace(p.DeviceTarget.Name))
		if deviceNodeID == "" || len([]rune(deviceNodeID)) > 200 || len([]rune(deviceName)) > 200 {
			return CreateTurnResult{}, fmt.Errorf("%w: invalid device target", domain.ErrConflict)
		}
	}

	requestHash := stableRequestHash(
		"start_card_execution",
		p.WorkspaceID.String(),
		p.CardID.String(),
		p.AuthorSourceID.String(),
	)
	if deviceNodeID != "" {
		requestHash = stableRequestHash(requestHash, "device_target", deviceNodeID, deviceName)
	}
	if p.PreferredModelRouteID != nil && *p.PreferredModelRouteID != uuid.Nil {
		requestHash = stableRequestHash(requestHash, "preferred_model_route", p.PreferredModelRouteID.String())
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateTurnResult{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockCard(ctx, tx, p.WorkspaceID, p.CardID); err != nil {
		return CreateTurnResult{}, err
	}
	if err := requireHumanSource(ctx, tx, p.WorkspaceID, p.AuthorSourceID); err != nil {
		return CreateTurnResult{}, err
	}
	if existing, found, err := findTurnByIdempotency(
		ctx, tx, p.WorkspaceID, p.AuthorSourceID, p.IdempotencyKey,
	); err != nil {
		return CreateTurnResult{}, err
	} else if found {
		if existing.requestHash != requestHash {
			return CreateTurnResult{}, domain.ErrIdempotencyMismatch
		}
		if err := tx.Commit(ctx); err != nil {
			return CreateTurnResult{}, err
		}
		return CreateTurnResult{Turn: existing.turn, Reused: true}, nil
	}

	var kind, title, description, todoState, ongoingState string
	err = tx.QueryRow(ctx, `
		SELECT kind, title, description, COALESCE(todo_state, ''), COALESCE(ongoing_state, '')
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, p.WorkspaceID, p.CardID).Scan(&kind, &title, &description, &todoState, &ongoingState)
	if errors.Is(err, pgx.ErrNoRows) {
		return CreateTurnResult{}, domain.ErrNotFound
	}
	if err != nil {
		return CreateTurnResult{}, err
	}

	switch domain.CardKind(kind) {
	case domain.CardKindTodo:
		if todoState != string(domain.TodoStateTodo) {
			return CreateTurnResult{}, fmt.Errorf("%w: only todo cards in todo state can be started", domain.ErrInvalidTransition)
		}
	case domain.CardKindOngoing:
		if ongoingState != string(domain.OngoingStateActive) {
			return CreateTurnResult{}, fmt.Errorf("%w: only active ongoing cards can be started", domain.ErrInvalidTransition)
		}
	default:
		return CreateTurnResult{}, fmt.Errorf("%w: only todo or ongoing cards support one-click start", domain.ErrInvalidTransition)
	}

	var existingTurnCount int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM card_turns
		WHERE workspace_id = $1 AND card_id = $2 AND cancelled_at IS NULL
	`, p.WorkspaceID, p.CardID).Scan(&existingTurnCount); err != nil {
		return CreateTurnResult{}, err
	}
	if existingTurnCount > 0 {
		return CreateTurnResult{}, fmt.Errorf("%w: card has already started or queued execution", domain.ErrConflict)
	}

	var preferredModelRouteID uuid.NullUUID
	if p.PreferredModelRouteID != nil && *p.PreferredModelRouteID != uuid.Nil {
		preferredModelRouteID = uuid.NullUUID{UUID: *p.PreferredModelRouteID, Valid: true}
	}

	turn, err := insertTurn(ctx, tx, insertTurnParams{
		WorkspaceID:           p.WorkspaceID,
		CardID:                p.CardID,
		Source:                domain.TurnSourceSystem,
		Content:               buildInitialWorkTurnContent(title, description),
		CreatedBySourceID:     uuid.NullUUID{UUID: p.AuthorSourceID, Valid: true},
		DispatchMode:          domain.DispatchModeEnqueue,
		DeviceNodeID:          deviceNodeID,
		DeviceName:            deviceName,
		PreferredModelRouteID: preferredModelRouteID,
		IdempotencyKey:        p.IdempotencyKey,
		RequestHash:           requestHash,
	})
	if err != nil {
		return CreateTurnResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateTurnResult{}, err
	}
	s.notifyWorkReady()
	return CreateTurnResult{Turn: turn}, nil
}

func findCreatedCardRetry(
	ctx context.Context,
	q *db.Queries,
	workspaceID, sourceID uuid.UUID,
	idempotencyKey, requestHash string,
) (db.Card, bool, error) {
	existing, err := q.GetCreatedCardRequest(ctx, db.GetCreatedCardRequestParams{
		WorkspaceID:          workspaceID,
		CreatedBySourceID:    sourceID,
		CreateIdempotencyKey: pgtype.Text{String: idempotencyKey, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Card{}, false, nil
	}
	if err != nil {
		return db.Card{}, false, err
	}
	if !existing.CreateRequestHash.Valid || existing.CreateRequestHash.String != requestHash {
		return db.Card{}, false, domain.ErrIdempotencyMismatch
	}
	return existing, true, nil
}

func (s *Store) AddAgentComment(
	ctx context.Context,
	workspaceID, cardID, sourceID uuid.UUID,
	content, idempotencyKey string,
) (uuid.UUID, bool, error) {
	content, _ = agentdata.NormalizeText(strings.TrimSpace(content))
	idempotencyKey, _ = agentdata.NormalizeText(strings.TrimSpace(idempotencyKey))
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
	if err := ensureDiscussionAcceptsWork(ctx, tx, workspaceID, cardID); err != nil {
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
	content, _ = agentdata.NormalizeText(strings.TrimSpace(content))
	idempotencyKey, _ = agentdata.NormalizeText(strings.TrimSpace(idempotencyKey))
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
			"SELECT todo_state FROM cards WHERE workspace_id=$1 AND id=$2",
			workspaceID, cardID,
		).Scan(&column); err != nil {
			return ConfirmResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ConfirmResult{}, err
		}
		return ConfirmResult{
			EventID: existing.ID, ConfirmationCount: count,
			MovedToReview: column == string(domain.TodoStateReview), Reused: true,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ConfirmResult{}, err
	}

	var kind, column string
	var required int32
	var confirmationRound int64
	if err := tx.QueryRow(ctx,
		"SELECT kind, todo_state, required_confirmations, confirmation_round FROM cards WHERE workspace_id=$1 AND id=$2",
		workspaceID, cardID,
	).Scan(&kind, &column, &required, &confirmationRound); err != nil {
		return ConfirmResult{}, err
	}
	if kind != string(domain.CardKindTodo) || column != string(domain.TodoStateWorking) {
		return ConfirmResult{}, fmt.Errorf("%w: confirm requires a Working Todo card", domain.ErrInvalidTransition)
	}

	event, err := q.CreateAgentEvent(ctx, db.CreateAgentEventParams{
		ID: uuid.New(), WorkspaceID: workspaceID, CardID: cardID,
		EventType: string(domain.EventTypeConfirm), ActorSourceID: sourceID,
		ConfirmationRound: confirmationRound,
		Content:           content,
		IdempotencyKey:    pgtype.Text{String: idempotencyKey, Valid: true},
		RequestHash:       pgtype.Text{String: requestHash, Valid: true},
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
			"UPDATE cards SET todo_state='review', updated_at=now() WHERE workspace_id=$1 AND id=$2 AND todo_state='working'",
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
		if err := notifySupervisorReview(ctx, tx, workspaceID, cardID, sourceID); err != nil {
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
