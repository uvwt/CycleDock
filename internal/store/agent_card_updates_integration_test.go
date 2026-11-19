package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestUpdateCardFromAgentAttempt_RespectsDomainAndScopeBoundaries(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	mainDiscussion, _, err := s.EnsureMainDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil {
		t.Fatal(err)
	}
	subDiscussion, err := s.CreateDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil {
		t.Fatal(err)
	}
	ongoingCard, err := s.CreateOngoingCard(ctx, f.WorkspaceID, f.ProjectID, f.HumanID, "长期监控", "巡检日志", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "更新卡片信息", "turn-update-card", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 修改当前 Todo 卡片标题、描述、置顶、确认阈值及状态（todo -> working）
	newTitle := "整理后的待办标题"
	newDesc := "已补充明确验收口径"
	pinTrue := true
	reqConfirm := int32(2)
	workingState := domain.TodoStateWorking

	todoRes, err := s.UpdateCardFromAgentAttempt(ctx, UpdateCardFromAttemptParams{
		WorkspaceID:           f.WorkspaceID,
		AttemptCardID:         f.CardID,
		AttemptID:             claim.Attempt.ID,
		RunnerSourceID:        f.AgentID,
		TargetCardID:          f.CardID,
		Title:                 &newTitle,
		Description:           &newDesc,
		Pinned:                &pinTrue,
		RequiredConfirmations: &reqConfirm,
		TodoState:             &workingState,
		Now:                   now,
	})
	if err != nil {
		t.Fatalf("update current todo card: %v", err)
	}
	if !todoRes.Changed || !todoRes.StatusChanged {
		t.Fatalf("expected changed=true statusChanged=true, got %+v", todoRes)
	}
	if todoRes.Card.Title != newTitle ||
		todoRes.Card.Description != newDesc ||
		!todoRes.Card.PinnedAt.Valid ||
		todoRes.Card.RequiredConfirmations != 2 ||
		todoRes.Card.TodoState.String != string(domain.TodoStateWorking) {
		t.Fatalf("unexpected updated todo card: %+v", todoRes.Card)
	}

	// 2. 禁止 Agent 直接把 Todo 置为 review 或 done
	reviewState := domain.TodoStateReview
	if _, err := s.UpdateCardFromAgentAttempt(ctx, UpdateCardFromAttemptParams{
		WorkspaceID:    f.WorkspaceID,
		AttemptCardID:  f.CardID,
		AttemptID:      claim.Attempt.ID,
		RunnerSourceID: f.AgentID,
		TargetCardID:   f.CardID,
		TodoState:      &reviewState,
		Now:            now,
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition when moving todo directly to review, got %v", err)
	}

	doneState := domain.TodoStateDone
	if _, err := s.UpdateCardFromAgentAttempt(ctx, UpdateCardFromAttemptParams{
		WorkspaceID:    f.WorkspaceID,
		AttemptCardID:  f.CardID,
		AttemptID:      claim.Attempt.ID,
		RunnerSourceID: f.AgentID,
		TargetCardID:   f.CardID,
		TodoState:      &doneState,
		Now:            now,
	}); !errors.Is(err, domain.ErrHumanRequired) {
		t.Fatalf("expected ErrHumanRequired when moving todo to done, got %v", err)
	}

	// 3. 修改同工作区内的长期 Ongoing 卡片（标题、描述、暂停与重新激活）
	ongoingApproval, err := s.CreateToolApproval(ctx, CreateToolApprovalParams{
		WorkspaceID: f.WorkspaceID,
		CardID:      ongoingCard.ID,
		ToolCallID:  "call-ongoing-" + uuid.NewString(),
		ToolName:    "exec_command",
		Arguments:   map[string]any{"cmd": "systemctl restart demo"},
		RiskLevel:   "high",
		Reversible:  true,
		Reason:      "重启服务需确认",
	})
	if err != nil {
		t.Fatalf("create ongoing pending approval: %v", err)
	}
	ongoingTitle := "长期监控（已更新）"
	ongoingDesc := "每小时检查队列堆积"
	pausedState := domain.OngoingStatePaused
	ongoingRes, err := s.UpdateCardFromAgentAttempt(ctx, UpdateCardFromAttemptParams{
		WorkspaceID:    f.WorkspaceID,
		AttemptCardID:  f.CardID,
		AttemptID:      claim.Attempt.ID,
		RunnerSourceID: f.AgentID,
		TargetCardID:   ongoingCard.ID,
		Title:          &ongoingTitle,
		Description:    &ongoingDesc,
		OngoingState:   &pausedState,
		Now:            now,
	})
	if err != nil {
		t.Fatalf("update ongoing card: %v", err)
	}
	if !ongoingRes.Changed || !ongoingRes.StatusChanged ||
		ongoingRes.Card.Title != ongoingTitle ||
		ongoingRes.Card.OngoingState.String != string(domain.OngoingStatePaused) {
		t.Fatalf("unexpected ongoing card result: %+v", ongoingRes)
	}
	gotOngoingApproval, err := s.GetToolApproval(ctx, f.WorkspaceID, ongoingCard.ID, ongoingApproval.ID)
	if err != nil {
		t.Fatalf("get ongoing approval: %v", err)
	}
	if gotOngoingApproval.Status != string(domain.ToolApprovalStatusSuperseded) {
		t.Fatalf("ongoing approval status=%q want superseded", gotOngoingApproval.Status)
	}

	activeState := domain.OngoingStateActive
	reactivated, err := s.UpdateCardFromAgentAttempt(ctx, UpdateCardFromAttemptParams{
		WorkspaceID:    f.WorkspaceID,
		AttemptCardID:  f.CardID,
		AttemptID:      claim.Attempt.ID,
		RunnerSourceID: f.AgentID,
		TargetCardID:   ongoingCard.ID,
		OngoingState:   &activeState,
		Now:            now.Add(5 * time.Minute),
	})
	if err != nil || reactivated.Card.OngoingState.String != string(domain.OngoingStateActive) {
		t.Fatalf("reactivate ongoing card: res=%+v err=%v", reactivated, err)
	}

	// 4. 修改普通 Discussion（允许更新标题和归档），但严禁修改项目主讨论
	discApproval, err := s.CreateToolApproval(ctx, CreateToolApprovalParams{
		WorkspaceID: f.WorkspaceID,
		CardID:      subDiscussion.ID,
		ToolCallID:  "call-disc-" + uuid.NewString(),
		ToolName:    "exec_command",
		Arguments:   map[string]any{"cmd": "rm -rf /tmp/disc"},
		RiskLevel:   "high",
		Reversible:  false,
		Reason:      "清理目录需确认",
	})
	if err != nil {
		t.Fatalf("create discussion pending approval: %v", err)
	}
	discTitle := "归档前的专题讨论"
	archivedDisc := domain.DiscussionStateArchived
	discRes, err := s.UpdateCardFromAgentAttempt(ctx, UpdateCardFromAttemptParams{
		WorkspaceID:     f.WorkspaceID,
		AttemptCardID:   f.CardID,
		AttemptID:       claim.Attempt.ID,
		RunnerSourceID:  f.AgentID,
		TargetCardID:    subDiscussion.ID,
		Title:           &discTitle,
		DiscussionState: &archivedDisc,
		Now:             now,
	})
	if err != nil {
		t.Fatalf("update regular discussion card: %v", err)
	}
	if discRes.Card.Title != discTitle || discRes.Card.DiscussionState.String != string(domain.DiscussionStateArchived) {
		t.Fatalf("unexpected discussion card result: %+v", discRes.Card)
	}
	gotDiscApproval, err := s.GetToolApproval(ctx, f.WorkspaceID, subDiscussion.ID, discApproval.ID)
	if err != nil {
		t.Fatalf("get discussion approval: %v", err)
	}
	if gotDiscApproval.Status != string(domain.ToolApprovalStatusSuperseded) {
		t.Fatalf("discussion approval status=%q want superseded", gotDiscApproval.Status)
	}

	if _, err := s.UpdateCardFromAgentAttempt(ctx, UpdateCardFromAttemptParams{
		WorkspaceID:    f.WorkspaceID,
		AttemptCardID:  f.CardID,
		AttemptID:      claim.Attempt.ID,
		RunnerSourceID: f.AgentID,
		TargetCardID:   mainDiscussion.ID,
		Title:          &discTitle,
		Now:            now,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict when mutating main discussion, got %v", err)
	}

	// 5. 跨 Workspace 卡片不可见/不可改
	otherWS, err := s.CreateWorkspace(ctx, "Other WS")
	if err != nil {
		t.Fatal(err)
	}
	otherHuman, err := s.CreateSource(ctx, otherWS.ID, domain.SourceKindHuman, "Other Human")
	if err != nil {
		t.Fatal(err)
	}
	otherProj, err := s.CreateProject(ctx, otherWS.ID, "Other Proj", "")
	if err != nil {
		t.Fatal(err)
	}
	otherTodo, err := s.CreateTodoCard(ctx, otherWS.ID, otherProj.ID, otherHuman.ID, "Foreign Todo", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateCardFromAgentAttempt(ctx, UpdateCardFromAttemptParams{
		WorkspaceID:    f.WorkspaceID,
		AttemptCardID:  f.CardID,
		AttemptID:      claim.Attempt.ID,
		RunnerSourceID: f.AgentID,
		TargetCardID:   otherTodo.ID,
		Title:          &newTitle,
		Now:            now,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-workspace target card, got %v", err)
	}

	// 6. Attempt 结束后（completed/superseded）拒绝滞后写操作
	if err := s.CompleteAttempt(ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID, f.AgentID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	lateTitle := "滞后覆盖"
	if _, err := s.UpdateCardFromAgentAttempt(ctx, UpdateCardFromAttemptParams{
		WorkspaceID:    f.WorkspaceID,
		AttemptCardID:  f.CardID,
		AttemptID:      claim.Attempt.ID,
		RunnerSourceID: f.AgentID,
		TargetCardID:   f.CardID,
		Title:          &lateTitle,
		Now:            now.Add(2 * time.Minute),
	}); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("expected ErrAttemptNotCurrent for finished attempt, got %v", err)
	}
}
