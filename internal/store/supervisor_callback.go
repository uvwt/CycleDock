package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
)

const supervisorProgressBudget = 5

type supervisorTarget struct {
	CardID    uuid.UUID
	ProjectID uuid.UUID
	Kind      domain.CardKind
	Title     string
}

type supervisorDecision struct {
	Callback      bool
	CircuitOpen   bool
	AttemptNumber int
	SameCount     int
}

func notifySupervisorReview(ctx context.Context, tx pgx.Tx, workspaceID, cardID, actorSourceID uuid.UUID) error {
	target, err := loadSupervisorTarget(ctx, tx, workspaceID, cardID)
	if err != nil || target.Kind != domain.CardKindTodo {
		return err
	}
	content := fmt.Sprintf(
		"子任务终态回调：%s（%s）已进入 Review。\ncard_id=%s\nkind=todo\noutcome=review\n请向用户总结结果。这是完成回调，不要自动重开。",
		displayCardTitle(target.Title, target.CardID), target.CardID, target.CardID,
	)
	return enqueueSupervisorCallback(ctx, tx, workspaceID, target, actorSourceID, "review:"+cardID.String(), content)
}

func decideSupervisorFailure(ctx context.Context, tx pgx.Tx, workspaceID, cardID, turnID uuid.UUID, summary string) (supervisorDecision, error) {
	summary, _ = agentdata.NormalizeText(strings.TrimSpace(summary))
	var attemptNumber int
	var previous string
	err := tx.QueryRow(ctx, `
		SELECT a.attempt_number, COALESCE(prev.error_summary, '')
		FROM turn_attempts a
		LEFT JOIN turn_attempts prev
		  ON prev.workspace_id=a.workspace_id AND prev.id=a.retry_of_attempt_id
		WHERE a.workspace_id=$1 AND a.card_id=$2 AND a.turn_id=$3
		ORDER BY a.attempt_number DESC
		LIMIT 1
	`, workspaceID, cardID, turnID).Scan(&attemptNumber, &previous)
	if err != nil {
		return supervisorDecision{}, err
	}
	sameCount := 1
	if previous != "" && previous == summary {
		sameCount = 2
	}
	return supervisorDecision{
		Callback:      true,
		CircuitOpen:   sameCount >= 2 || attemptNumber >= supervisorProgressBudget,
		AttemptNumber: attemptNumber,
		SameCount:     sameCount,
	}, nil
}

func notifySupervisorFailure(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, cardID, turnID, attemptID, actorSourceID uuid.UUID,
	status domain.AttemptStatus,
	summary string,
	decision supervisorDecision,
) error {
	if !decision.Callback {
		return nil
	}
	target, err := loadSupervisorTarget(ctx, tx, workspaceID, cardID)
	if err != nil || (target.Kind != domain.CardKindTodo && target.Kind != domain.CardKindOngoing) {
		return err
	}
	action := "错误与上次不同，可调整方案后用 cycledock_send_card_message 让子任务继续；连续两次相同错误必须停止自动追聊。"
	if decision.CircuitOpen {
		if decision.SameCount >= 2 {
			action = "熔断：连续两次错误摘要相同，停止自动追聊，改为向用户总结。"
		} else {
			action = "熔断：已达到 5 次动态自愈预算，停止自动追聊，改为向用户总结。"
		}
	}
	content := fmt.Sprintf(
		"子任务终态回调：%s（%s）执行失败。\ncard_id=%s\nturn_id=%s\nattempt_id=%s\nkind=%s\noutcome=%s\nattempt=%d/%d\nidentical_error_count=%d\nerror_summary=%s\n%s",
		displayCardTitle(target.Title, target.CardID), target.CardID,
		target.CardID, turnID, attemptID, target.Kind, status,
		decision.AttemptNumber, supervisorProgressBudget, decision.SameCount,
		summary, action,
	)
	return enqueueSupervisorCallback(
		ctx, tx, workspaceID, target, actorSourceID,
		"attempt:"+attemptID.String(), content,
	)
}

func enqueueSupervisorCallback(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID uuid.UUID,
	target supervisorTarget,
	actorSourceID uuid.UUID,
	idempotencySuffix, content string,
) error {
	discussionID, err := supervisorDiscussionID(ctx, tx, workspaceID, target)
	if err != nil || discussionID == uuid.Nil || discussionID == target.CardID {
		return err
	}
	if err := lockCard(ctx, tx, workspaceID, discussionID); err != nil {
		return err
	}
	sourceID := actorSourceID
	if sourceID == uuid.Nil {
		sourceID, err = ensureCycleAgentSource(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
	} else if err := ensureAgentSource(ctx, tx, workspaceID, sourceID); err != nil {
		if !errors.Is(err, domain.ErrHumanRequired) {
			return err
		}
		sourceID, err = ensureCycleAgentSource(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
	}
	if err := ensureDiscussionAcceptsWork(ctx, tx, workspaceID, discussionID); err != nil {
		return nil
	}
	content, _ = agentdata.NormalizeText(strings.TrimSpace(content))
	key := "supervisor-callback:" + target.CardID.String() + ":" + idempotencySuffix
	requestHash := stableRequestHash(
		"supervisor_callback", workspaceID.String(), discussionID.String(),
		sourceID.String(), target.CardID.String(), content,
	)
	if _, found, err := findTurnByIdempotency(ctx, tx, workspaceID, sourceID, key); err != nil || found {
		return err
	}
	_, err = insertTurn(ctx, tx, insertTurnParams{
		WorkspaceID:       workspaceID,
		CardID:            discussionID,
		Source:            domain.TurnSourceSystem,
		Content:           content,
		CreatedBySourceID: uuid.NullUUID{UUID: sourceID, Valid: true},
		DispatchMode:      domain.DispatchModeEnqueue,
		IdempotencyKey:    key,
		RequestHash:       requestHash,
	})
	return err
}

func supervisorDiscussionID(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, target supervisorTarget) (uuid.UUID, error) {
	var sourceDiscussionID uuid.NullUUID
	err := tx.QueryRow(ctx, `
		SELECT source_discussion_id
		FROM cards
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, target.CardID).Scan(&sourceDiscussionID)
	if err != nil {
		return uuid.Nil, err
	}
	if sourceDiscussionID.Valid {
		active, err := discussionActive(ctx, tx, workspaceID, sourceDiscussionID.UUID)
		if err != nil || active {
			return sourceDiscussionID.UUID, err
		}
	}
	var mainID uuid.NullUUID
	err = tx.QueryRow(ctx, `
		SELECT main_discussion_id
		FROM projects
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, target.ProjectID).Scan(&mainID)
	if errors.Is(err, pgx.ErrNoRows) || !mainID.Valid {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	active, err := discussionActive(ctx, tx, workspaceID, mainID.UUID)
	if err != nil || !active {
		return uuid.Nil, err
	}
	return mainID.UUID, nil
}

func loadSupervisorTarget(ctx context.Context, tx pgx.Tx, workspaceID, cardID uuid.UUID) (supervisorTarget, error) {
	var target supervisorTarget
	err := tx.QueryRow(ctx, `
		SELECT id, project_id, kind, title
		FROM cards
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, cardID).Scan(&target.CardID, &target.ProjectID, &target.Kind, &target.Title)
	if errors.Is(err, pgx.ErrNoRows) {
		return supervisorTarget{}, domain.ErrNotFound
	}
	return target, err
}

func discussionActive(ctx context.Context, tx pgx.Tx, workspaceID, cardID uuid.UUID) (bool, error) {
	var kind, state string
	err := tx.QueryRow(ctx, `
		SELECT kind, COALESCE(discussion_state, '')
		FROM cards
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, cardID).Scan(&kind, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return kind == string(domain.CardKindDiscussion) && state == string(domain.DiscussionStateActive), nil
}

func displayCardTitle(title string, cardID uuid.UUID) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "未命名卡片 " + cardID.String()
	}
	return title
}
