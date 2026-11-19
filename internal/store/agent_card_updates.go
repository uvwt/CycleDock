package store

import (
	"bytes"
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
	"github.com/uvwt/CycleDock/internal/store/db"
)

// UpdateCardFromAttemptParams 描述内置 Agent 在当前运行中的 Attempt 内修改卡片信息的请求。
// 所有字段均为可选，但每次调用至少需要指定一项变更。
type UpdateCardFromAttemptParams struct {
	WorkspaceID           uuid.UUID
	AttemptCardID         uuid.UUID
	AttemptID             uuid.UUID
	RunnerSourceID        uuid.UUID
	TargetCardID          uuid.UUID
	Title                 *string
	Description           *string
	Pinned                *bool
	TodoState             *domain.TodoState
	OngoingState          *domain.OngoingState
	DiscussionState       *domain.DiscussionState
	RequiredConfirmations *int32
	Now                   time.Time
}

// UpdateCardFromAttemptResult 返回更新后的卡片快照及是否发生了持久化变更。
type UpdateCardFromAttemptResult struct {
	Card          db.Card
	Changed       bool
	StatusChanged bool
}

// UpdateCardFromAgentAttempt 在单个数据库事务中校验当前运行中的 Attempt，
// 并按领域不变量更新同工作区内的 Todo、Ongoing 或普通 Discussion 卡片信息。
func (s *Store) UpdateCardFromAgentAttempt(
	ctx context.Context,
	p UpdateCardFromAttemptParams,
) (UpdateCardFromAttemptResult, error) {
	if p.Title == nil &&
		p.Description == nil &&
		p.Pinned == nil &&
		p.TodoState == nil &&
		p.OngoingState == nil &&
		p.DiscussionState == nil &&
		p.RequiredConfirmations == nil {
		return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: at least one card field must be provided", domain.ErrConflict)
	}

	stateFieldCount := 0
	if p.TodoState != nil {
		stateFieldCount++
	}
	if p.OngoingState != nil {
		stateFieldCount++
	}
	if p.DiscussionState != nil {
		stateFieldCount++
	}
	if stateFieldCount > 1 {
		return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: provide at most one state field matching the card kind", domain.ErrConflict)
	}

	var normalizedTitle string
	if p.Title != nil {
		normalizedTitle, _ = agentdata.NormalizeText(strings.TrimSpace(*p.Title))
		if normalizedTitle == "" {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: title cannot be empty", domain.ErrConflict)
		}
	}

	var normalizedDesc string
	if p.Description != nil {
		normalizedDesc, _ = agentdata.NormalizeText(strings.TrimSpace(*p.Description))
	}

	if p.RequiredConfirmations != nil && *p.RequiredConfirmations < 1 {
		return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: required_confirmations must be >= 1", domain.ErrConflict)
	}

	now := p.Now.UTC()
	if p.Now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UpdateCardFromAttemptResult{}, err
	}
	defer tx.Rollback(ctx)

	// 按固定 UUID 顺序锁定当前执行卡片与目标卡片，避免两张卡片并发互改时产生死锁。
	firstCardID, secondCardID := p.AttemptCardID, p.TargetCardID
	if bytes.Compare(firstCardID[:], secondCardID[:]) > 0 {
		firstCardID, secondCardID = secondCardID, firstCardID
	}
	if err := lockCard(ctx, tx, p.WorkspaceID, firstCardID); err != nil {
		return UpdateCardFromAttemptResult{}, err
	}
	if secondCardID != firstCardID {
		if err := lockCard(ctx, tx, p.WorkspaceID, secondCardID); err != nil {
			return UpdateCardFromAttemptResult{}, err
		}
	}

	if err := requireCurrentAttempt(
		ctx, tx, p.WorkspaceID, p.AttemptCardID, p.AttemptID, p.RunnerSourceID,
		domain.AttemptStatusRunning,
	); err != nil {
		return UpdateCardFromAttemptResult{}, err
	}
	if err := ensureAgentSource(ctx, tx, p.WorkspaceID, p.RunnerSourceID); err != nil {
		return UpdateCardFromAttemptResult{}, err
	}

	q := s.queries.WithTx(tx)
	current, err := q.GetCard(ctx, db.GetCardParams{
		WorkspaceID: p.WorkspaceID,
		ID:          p.TargetCardID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UpdateCardFromAttemptResult{}, domain.ErrNotFound
	}
	if err != nil {
		return UpdateCardFromAttemptResult{}, err
	}

	// 项目主讨论的标题、描述、置顶与激活态受项目级约束保护，不允许修改。
	if err := rejectMainDiscussionMutation(ctx, tx, p.WorkspaceID, p.TargetCardID); err != nil {
		return UpdateCardFromAttemptResult{}, err
	}

	kind := domain.CardKind(current.Kind)
	switch kind {
	case domain.CardKindTodo:
		if p.OngoingState != nil || p.DiscussionState != nil {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: todo card only accepts todo_state", domain.ErrInvalidTransition)
		}
	case domain.CardKindOngoing:
		if p.TodoState != nil || p.DiscussionState != nil {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: ongoing card only accepts ongoing_state", domain.ErrInvalidTransition)
		}
		if p.RequiredConfirmations != nil {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: required_confirmations only applies to todo cards", domain.ErrInvalidTransition)
		}
	case domain.CardKindDiscussion:
		if p.TodoState != nil || p.OngoingState != nil {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: discussion card only accepts discussion_state", domain.ErrInvalidTransition)
		}
		if p.RequiredConfirmations != nil {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: required_confirmations only applies to todo cards", domain.ErrInvalidTransition)
		}
	default:
		return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: unknown card kind %q", domain.ErrInvalidTransition, current.Kind)
	}

	newTitle := current.Title
	metadataChanged := false
	if p.Title != nil && normalizedTitle != current.Title {
		newTitle = normalizedTitle
		metadataChanged = true
	}

	newDesc := current.Description
	if p.Description != nil && normalizedDesc != current.Description {
		newDesc = normalizedDesc
		metadataChanged = true
	}

	newPinnedAt := current.PinnedAt
	if p.Pinned != nil {
		currentlyPinned := current.PinnedAt.Valid
		if *p.Pinned != currentlyPinned {
			if *p.Pinned {
				newPinnedAt = pgtype.Timestamptz{Time: now, Valid: true}
			} else {
				newPinnedAt = pgtype.Timestamptz{}
			}
			metadataChanged = true
		}
	}

	newRequiredConfirmations := current.RequiredConfirmations
	if p.RequiredConfirmations != nil && *p.RequiredConfirmations != current.RequiredConfirmations {
		// 已进入待验收或已完成的 Todo 不允许在未退回执行前篡改验收阈值。
		if current.TodoState.String == string(domain.TodoStateReview) || current.TodoState.String == string(domain.TodoStateDone) {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: cannot change required_confirmations while todo card is in %s", domain.ErrInvalidTransition, current.TodoState.String)
		}
		newRequiredConfirmations = *p.RequiredConfirmations
		metadataChanged = true
	}

	statusChanged := false
	var statusTransitionContent string

	if p.TodoState != nil {
		from := domain.TodoState(current.TodoState.String)
		to := *p.TodoState
		// Agent 只能将 Todo 在 todo 与 working 之间流转：
		// - Review 必须经由 cycledock_confirm_card 满足确认阈值；
		// - Done（以及从 Done 退回）属于人类专属验收操作。
		if to == domain.TodoStateDone || from == domain.TodoStateDone {
			return UpdateCardFromAttemptResult{}, domain.ErrHumanRequired
		}
		if to == domain.TodoStateReview {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: todo cards can only enter review via cycledock_confirm_card", domain.ErrInvalidTransition)
		}
		if to != domain.TodoStateTodo && to != domain.TodoStateWorking {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: invalid todo_state %q", domain.ErrInvalidTransition, to)
		}
		if err := domain.CanMoveTodo(from, to, domain.SourceKindCycleAgent); err != nil {
			return UpdateCardFromAttemptResult{}, err
		}
		if from != to {
			if _, err := tx.Exec(ctx, `
				UPDATE cards
				SET todo_state = $3,
				    confirmation_round = confirmation_round + CASE WHEN $3 = 'working' THEN 1 ELSE 0 END,
				    updated_at = now()
				WHERE workspace_id = $1 AND id = $2
			`, p.WorkspaceID, p.TargetCardID, string(to)); err != nil {
				return UpdateCardFromAttemptResult{}, err
			}
			statusChanged = true
			statusTransitionContent = fmt.Sprintf("%s -> %s", from, to)
		}
	}

	if p.OngoingState != nil {
		from := domain.OngoingState(current.OngoingState.String)
		to := *p.OngoingState
		if to != domain.OngoingStateActive && to != domain.OngoingStatePaused && to != domain.OngoingStateArchived {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: invalid ongoing_state %q", domain.ErrInvalidTransition, to)
		}
		if from != to {
			if _, err := tx.Exec(ctx, `
				UPDATE cards
				SET ongoing_state = $3,
				    updated_at = now()
				WHERE workspace_id = $1 AND id = $2
			`, p.WorkspaceID, p.TargetCardID, string(to)); err != nil {
				return UpdateCardFromAttemptResult{}, err
			}
			if to == domain.OngoingStateActive {
				if err := recalculateCardSchedulesTx(ctx, tx, p.WorkspaceID, p.TargetCardID, now); err != nil {
					return UpdateCardFromAttemptResult{}, err
				}
			}
			statusChanged = true
			statusTransitionContent = fmt.Sprintf("%s -> %s", from, to)
		}
	}

	if p.DiscussionState != nil {
		from := domain.DiscussionState(current.DiscussionState.String)
		to := *p.DiscussionState
		if to != domain.DiscussionStateActive && to != domain.DiscussionStateArchived {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: invalid discussion_state %q", domain.ErrInvalidTransition, to)
		}
		if to == domain.DiscussionStateArchived && p.TargetCardID == p.AttemptCardID {
			return UpdateCardFromAttemptResult{}, fmt.Errorf("%w: cannot archive the currently executing discussion card", domain.ErrConflict)
		}
		if from != to {
			if to == domain.DiscussionStateArchived {
				if _, err := tx.Exec(ctx, `
					UPDATE turn_attempts
					SET status = 'cancelled',
					    error_summary = 'discussion archived',
					    heartbeat_at = NULL,
					    lease_expires_at = NULL,
					    finished_at = now(),
					    updated_at = now()
					WHERE workspace_id = $1 AND card_id = $2
					  AND status IN ('queued', 'running')
				`, p.WorkspaceID, p.TargetCardID); err != nil {
					return UpdateCardFromAttemptResult{}, err
				}
				if _, err := tx.Exec(ctx, `
					UPDATE card_turns pending
					SET cancelled_at = now(),
					    cancelled_by_source_id = $3,
					    cancel_reason = 'discussion archived'
					WHERE pending.workspace_id = $1
					  AND pending.card_id = $2
					  AND pending.cancelled_at IS NULL
					  AND NOT EXISTS (
						SELECT 1 FROM turn_attempts existing
						WHERE existing.workspace_id = pending.workspace_id
						  AND existing.turn_id = pending.id
					  )
				`, p.WorkspaceID, p.TargetCardID, p.RunnerSourceID); err != nil {
					return UpdateCardFromAttemptResult{}, err
				}
			}
			if _, err := tx.Exec(ctx, `
				UPDATE cards
				SET discussion_state = $3,
				    updated_at = now()
				WHERE workspace_id = $1 AND id = $2
			`, p.WorkspaceID, p.TargetCardID, string(to)); err != nil {
				return UpdateCardFromAttemptResult{}, err
			}
			statusChanged = true
			statusTransitionContent = fmt.Sprintf("%s -> %s", from, to)
		}
	}

	if metadataChanged {
		if _, err := tx.Exec(ctx, `
			UPDATE cards
			SET title = $3,
			    description = $4,
			    pinned_at = $5,
			    required_confirmations = $6,
			    updated_at = now()
			WHERE workspace_id = $1 AND id = $2
		`, p.WorkspaceID, p.TargetCardID, newTitle, newDesc, newPinnedAt, newRequiredConfirmations); err != nil {
			return UpdateCardFromAttemptResult{}, mapConstraintError(err)
		}
	}

	if statusChanged {
		// events_attempt_fk 要求 (workspace_id, card_id, attempt_id) 落在同一张卡片上；
		// 修改当前卡片时记录 attempt_id，跨卡修改时将 attempt_id 置空并通过 source_card_id 记录发起方卡片。
		eventAttemptID := uuid.NullUUID{}
		sourceCardID := uuid.NullUUID{}
		if p.TargetCardID == p.AttemptCardID {
			eventAttemptID = uuid.NullUUID{UUID: p.AttemptID, Valid: true}
		} else {
			sourceCardID = uuid.NullUUID{UUID: p.AttemptCardID, Valid: true}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO events (id, workspace_id, card_id, event_type, actor_source_id, attempt_id, source_card_id, content)
			VALUES ($1, $2, $3, 'status_change', $4, $5, $6, $7)
		`, uuid.New(), p.WorkspaceID, p.TargetCardID, p.RunnerSourceID, eventAttemptID, sourceCardID, statusTransitionContent); err != nil {
			return UpdateCardFromAttemptResult{}, err
		}
	}

	updated, err := q.GetCard(ctx, db.GetCardParams{
		WorkspaceID: p.WorkspaceID,
		ID:          p.TargetCardID,
	})
	if err != nil {
		return UpdateCardFromAttemptResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return UpdateCardFromAttemptResult{}, err
	}

	return UpdateCardFromAttemptResult{
		Card:          updated,
		Changed:       metadataChanged || statusChanged,
		StatusChanged: statusChanged,
	}, nil
}
