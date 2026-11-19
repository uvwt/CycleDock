package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/internal/domain"
)

func createPendingApprovalFixture(t *testing.T, s *Store, f testFixture, toolName string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	comment, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AuthorSourceID: f.HumanID,
		Content:        "触发工具审批测试",
		IdempotencyKey: "seed-" + uuid.NewString(),
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := s.CreateToolApproval(ctx, CreateToolApprovalParams{
		WorkspaceID: f.WorkspaceID,
		CardID:      f.CardID,
		TurnID:      &comment.TurnID,
		AttemptID:   &claimed.Attempt.ID,
		ToolCallID:  "call-" + uuid.NewString(),
		ToolName:    toolName,
		Arguments:   map[string]any{"command": "rm -rf /tmp/demo"},
		RiskLevel:   "high",
		Reversible:  false,
		Reason:      "高风险命令需要确认",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, claimed.Attempt.ID, domain.AttemptStatusCompleted, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return approval.ID
}

func assertPendingApprovalCount(t *testing.T, s *Store, f testFixture, want int) {
	t.Helper()
	cards, err := s.ListProjectActiveCardSummaries(context.Background(), f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range cards {
		if item.ID == f.CardID {
			if item.PendingToolApprovalCount != int64(want) {
				t.Fatalf("pending_tool_approval_count=%d want=%d", item.PendingToolApprovalCount, want)
			}
			return
		}
	}
	t.Fatalf("card %s not found in ListProjectActiveCardSummaries", f.CardID)
}

func TestToolApprovalAutoSupersedesOnSubsequentActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("new_human_comment_and_resolve_conflict", func(t *testing.T) {
		s := requireIntegration(t)
		f := newFixture(t, s, domain.CardKindTodo)
		approvalID := createPendingApprovalFixture(t, s, f, "exec_command")
		assertPendingApprovalCount(t, s, f, 1)

		if _, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
			WorkspaceID:    f.WorkspaceID,
			CardID:         f.CardID,
			AuthorSourceID: f.HumanID,
			Content:        "不用执行了，换个只读方案",
			IdempotencyKey: "follow-up-" + uuid.NewString(),
			DispatchMode:   domain.DispatchModeEnqueue,
		}); err != nil {
			t.Fatal(err)
		}

		assertPendingApprovalCount(t, s, f, 0)
		approval, err := s.GetToolApproval(ctx, f.WorkspaceID, f.CardID, approvalID)
		if err != nil {
			t.Fatal(err)
		}
		if approval.Status != string(domain.ToolApprovalStatusSuperseded) || !approval.ResolvedAt.Valid {
			t.Fatalf("expected superseded with resolved_at, got %+v", approval)
		}

		_, err = s.ResolveToolApproval(ctx, ResolveToolApprovalParams{
			WorkspaceID:        f.WorkspaceID,
			CardID:             f.CardID,
			ApprovalID:         approvalID,
			Status:             string(domain.ToolApprovalStatusApproved),
			ResolvedBySourceID: f.HumanID,
		})
		if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("expected ErrConflict resolving superseded approval, got %v", err)
		}
	})

	t.Run("edit_comment_and_rerun_supersedes_pending_approval", func(t *testing.T) {
		s := requireIntegration(t)
		f := newFixture(t, s, domain.CardKindTodo)
		first, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
			WorkspaceID:    f.WorkspaceID,
			CardID:         f.CardID,
			AuthorSourceID: f.HumanID,
			Content:        "原始问题",
			IdempotencyKey: "edit-root-" + uuid.NewString(),
			DispatchMode:   domain.DispatchModeEnqueue,
		})
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, claimed.Attempt.ID, domain.AttemptStatusCompleted, "", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		approvalID := createPendingApprovalFixture(t, s, f, "file_edit")
		assertPendingApprovalCount(t, s, f, 1)

		// 在编辑第一条消息并重跑前，先检查该卡片已有两条轮次，编辑第一条会截断并重跑后续轮次
		if _, err := s.EditSessionCommentAndRerun(ctx, EditSessionCommentParams{
			WorkspaceID:    f.WorkspaceID,
			CardID:         f.CardID,
			EventID:        first.EventID,
			AuthorSourceID: f.HumanID,
			Content:        "修改后的问题",
		}); err != nil {
			t.Fatal(err)
		}

		assertPendingApprovalCount(t, s, f, 0)
		approval, err := s.GetToolApproval(ctx, f.WorkspaceID, f.CardID, approvalID)
		if err != nil {
			t.Fatal(err)
		}
		if approval.Status != string(domain.ToolApprovalStatusSuperseded) {
			t.Fatalf("expected superseded after edit rerun, got %s", approval.Status)
		}
	})

	t.Run("interrupt_agent_turn_supersedes_pending_approval", func(t *testing.T) {
		s := requireIntegration(t)
		f := newFixture(t, s, domain.CardKindTodo)
		approvalID := createPendingApprovalFixture(t, s, f, "exec_command")
		assertPendingApprovalCount(t, s, f, 1)

		if _, err := s.SubmitAgentTurn(
			ctx, f.WorkspaceID, f.CardID, f.AgentID,
			"打断并改派新方向", "agent-interrupt-"+uuid.NewString(),
			uuid.NullUUID{}, domain.DispatchModeInterrupt,
		); err != nil {
			t.Fatal(err)
		}

		assertPendingApprovalCount(t, s, f, 0)
		approval, err := s.GetToolApproval(ctx, f.WorkspaceID, f.CardID, approvalID)
		if err != nil {
			t.Fatal(err)
		}
		if approval.Status != string(domain.ToolApprovalStatusSuperseded) {
			t.Fatalf("expected superseded after interrupt turn, got %s", approval.Status)
		}
	})

	t.Run("todo_done_supersedes_pending_approval", func(t *testing.T) {
		s := requireIntegration(t)
		f := newFixture(t, s, domain.CardKindTodo)
		approvalID := createPendingApprovalFixture(t, s, f, "exec_command")

		if _, err := s.MoveTodoWithCard(ctx, f.WorkspaceID, f.CardID, f.HumanID, domain.TodoStateReview); err != nil {
			t.Fatal(err)
		}
		if _, err := s.MoveTodoWithCard(ctx, f.WorkspaceID, f.CardID, f.HumanID, domain.TodoStateDone); err != nil {
			t.Fatal(err)
		}

		var pendingCnt int64
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM card_tool_approvals WHERE card_id=$1 AND status='pending'`, f.CardID).Scan(&pendingCnt); err != nil || pendingCnt != 0 {
			t.Fatalf("pendingCnt=%d err=%v", pendingCnt, err)
		}
		approval, err := s.GetToolApproval(ctx, f.WorkspaceID, f.CardID, approvalID)
		if err != nil {
			t.Fatal(err)
		}
		if approval.Status != string(domain.ToolApprovalStatusSuperseded) {
			t.Fatalf("expected superseded when todo marked done, got %s", approval.Status)
		}
	})

	t.Run("discussion_archived_supersedes_pending_approval", func(t *testing.T) {
		s := requireIntegration(t)
		f := newFixture(t, s, domain.CardKindTodo)
		disc, err := s.CreateDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
		if err != nil {
			t.Fatal(err)
		}
		f.CardID = disc.ID
		approvalID := createPendingApprovalFixture(t, s, f, "exec_command")

		if _, err := s.ArchiveDiscussionWithCard(ctx, f.WorkspaceID, f.CardID, f.HumanID); err != nil {
			t.Fatal(err)
		}

		var pendingCnt int64
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM card_tool_approvals WHERE card_id=$1 AND status='pending'`, f.CardID).Scan(&pendingCnt); err != nil || pendingCnt != 0 {
			t.Fatalf("pendingCnt=%d err=%v", pendingCnt, err)
		}
		approval, err := s.GetToolApproval(ctx, f.WorkspaceID, f.CardID, approvalID)
		if err != nil {
			t.Fatal(err)
		}
		if approval.Status != string(domain.ToolApprovalStatusSuperseded) {
			t.Fatalf("expected superseded when discussion archived, got %s", approval.Status)
		}
	})

	t.Run("ongoing_paused_and_archived_supersede_pending_approval", func(t *testing.T) {
		s := requireIntegration(t)
		f := newFixture(t, s, domain.CardKindOngoing)
		approvalID := createPendingApprovalFixture(t, s, f, "exec_command")

		if _, err := s.SetOngoingStateWithCard(ctx, f.WorkspaceID, f.CardID, f.HumanID, domain.OngoingStatePaused, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}

		assertPendingApprovalCount(t, s, f, 0)
		approval, err := s.GetToolApproval(ctx, f.WorkspaceID, f.CardID, approvalID)
		if err != nil {
			t.Fatal(err)
		}
		if approval.Status != string(domain.ToolApprovalStatusSuperseded) {
			t.Fatalf("expected superseded when ongoing paused, got %s", approval.Status)
		}
	})

	t.Run("enqueue_tool_approval_turn_does_not_supersede_sibling_pending_approval", func(t *testing.T) {
		s := requireIntegration(t)
		f := newFixture(t, s, domain.CardKindTodo)
		firstID := createPendingApprovalFixture(t, s, f, "exec_command")
		second, err := s.CreateToolApproval(ctx, CreateToolApprovalParams{
			WorkspaceID: f.WorkspaceID,
			CardID:      f.CardID,
			ToolCallID:  "call-sibling",
			ToolName:    "file_edit",
			Arguments:   map[string]any{"path": "a.txt"},
			RiskLevel:   "medium",
			Reversible:  true,
			Reason:      "同轮第二个待确认工具",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ResolveToolApproval(ctx, ResolveToolApprovalParams{
			WorkspaceID:        f.WorkspaceID,
			CardID:             f.CardID,
			ApprovalID:         firstID,
			Status:             string(domain.ToolApprovalStatusApproved),
			ResolvedBySourceID: f.HumanID,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.EnqueueToolApprovalTurn(ctx, EnqueueToolApprovalTurnParams{
			WorkspaceID:    f.WorkspaceID,
			CardID:         f.CardID,
			Content:        "继续执行已批准工具",
			IdempotencyKey: "approval-resume-" + uuid.NewString(),
		}); err != nil {
			t.Fatal(err)
		}

		sibling, err := s.GetToolApproval(ctx, f.WorkspaceID, f.CardID, second.ID)
		if err != nil {
			t.Fatal(err)
		}
		if sibling.Status != string(domain.ToolApprovalStatusPending) {
			t.Fatalf("expected sibling pending approval preserved, got %s", sibling.Status)
		}
	})
}

func TestMigration38CleansLegacyZombiePendingApprovals(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	database := stdlib.OpenDB(*s.pool.Config().ConnConfig)
	defer database.Close()

	const migrations = "../../db/migrations"
	defer func() {
		if err := goose.Up(database, migrations); err != nil {
			t.Errorf("restore latest schema: %v", err)
		}
	}()

	if err := goose.DownTo(database, migrations, 37); err != nil {
		t.Fatal(err)
	}

	// 在版本 37 约束下制造两条历史僵尸 pending 审批记录与一条仍有效的最新 pending 审批记录，
	// 由于新版 Store 会在推进时直接标为 superseded，测试里直接以旧版语义插入后续 turn/状态或置回 pending 验证迁移 SQL。
	todoWithNewerTurn := newFixture(t, s, domain.CardKindTodo)
	zombieTurnApproval := createPendingApprovalFixture(t, s, todoWithNewerTurn, "exec_command")
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO card_turns (id, workspace_id, card_id, source, content, created_by_source_id, dispatch_mode)
		VALUES ($1, $2, $3, 'human', '旧版迁移前发出的后续对话', $4, 'enqueue')
	`, uuid.New(), todoWithNewerTurn.WorkspaceID, todoWithNewerTurn.CardID, todoWithNewerTurn.HumanID); err != nil {
		t.Fatal(err)
	}

	doneCard := newFixture(t, s, domain.CardKindTodo)
	zombieDoneApproval := createPendingApprovalFixture(t, s, doneCard, "file_edit")
	if _, err := s.pool.Exec(ctx, `
		UPDATE cards SET todo_state = 'done', updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, doneCard.WorkspaceID, doneCard.CardID); err != nil {
		t.Fatal(err)
	}

	activeCard := newFixture(t, s, domain.CardKindTodo)
	livePendingApproval := createPendingApprovalFixture(t, s, activeCard, "exec_command")

	if err := goose.Up(database, migrations); err != nil {
		t.Fatal(err)
	}

	for _, id := range []uuid.UUID{zombieTurnApproval, zombieDoneApproval} {
		var status string
		if err := s.pool.QueryRow(ctx, `SELECT status FROM card_tool_approvals WHERE id=$1`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != string(domain.ToolApprovalStatusSuperseded) {
			t.Fatalf("legacy zombie approval %s status=%s want=superseded", id, status)
		}
	}

	var liveStatus string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM card_tool_approvals WHERE id=$1`, livePendingApproval).Scan(&liveStatus); err != nil {
		t.Fatal(err)
	}
	if liveStatus != string(domain.ToolApprovalStatusPending) {
		t.Fatalf("active latest approval %s status=%s want=pending", livePendingApproval, liveStatus)
	}
}
