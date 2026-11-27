package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

// TestPendingToolApprovalBlocksQueuedTurnsAndResumesAfterResolve 验证：
// 1. 当卡片存在 status='pending' 的工具审批时，ClaimNextBuiltInAttempt / ClaimNextTurnAttempt 会跳过该卡片，
//    不再抢跑消费同卡片队列中已排队的其他 Turn（例如子卡回调或并发系统消息），同时不影响同工作区其他正常卡片。
// 2. 当审批被 resolved（approved/rejected）后，会触发 workReady 唤醒信号并立即解除阻塞，恢复消费该卡片队列。
// 3. 当用户发送人工新消息（interrupt/enqueue）作废（superseded）旧 pending 审批后，卡片也立即解除阻塞可被调度。
func TestPendingToolApprovalBlocksQueuedTurnsAndResumesAfterResolve(t *testing.T) {
	ctx := context.Background()

	t.Run("resolve_unblocks_card_without_blocking_sibling_card", func(t *testing.T) {
		s := requireIntegration(t)
		var workReadyCount atomic.Int64
		s.SetWorkReadyNotifier(func() {
			workReadyCount.Add(1)
		})

		f := newFixture(t, s, domain.CardKindTodo)
		settings, err := s.SaveAgentSettings(ctx, f.WorkspaceID, testAgentSettings("https://example.com", "gpt-5.4", ""))
		if err != nil {
			t.Fatalf("save agent settings: %v", err)
		}
		f.AgentID = settings.SourceID

		// 1. 卡片 A 触发工具审批挂起并完成当前 Attempt
		approvalID := createPendingApprovalFixture(t, s, f, "exec_command")

		// 2. 在卡片 A 上排队一个后续系统 Turn（模拟子卡回调或并发系统通知到达）
		queuedTurn, err := s.EnqueueToolApprovalTurn(ctx, EnqueueToolApprovalTurnParams{
			WorkspaceID:    f.WorkspaceID,
			CardID:         f.CardID,
			Content:        "子卡回调：上一批构建已完成",
			IdempotencyKey: "queued-callback-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("enqueue follow-up turn: %v", err)
		}

		// 3. 在存在 pending 审批期间，直接调度接口与内置调度器均不得 claim 卡片 A 的排队 Turn
		if claimed, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute); !errors.Is(err, domain.ErrNotFound) || claimed != nil {
			t.Fatalf("expected ErrNotFound while pending approval exists, got claim=%+v err=%v", claimed, err)
		}
		if claimed, err := s.ClaimNextBuiltInAttempt(ctx, 2, time.Now().UTC(), time.Minute); !errors.Is(err, domain.ErrNotFound) || claimed != nil {
			t.Fatalf("expected ClaimNextBuiltInAttempt ErrNotFound while pending approval exists, got claim=%+v err=%v", claimed, err)
		}

		// 4. 同工作区另一张没有 pending 审批的卡片 B 仍可被正常调度，互不阻塞
		cardB, err := s.CreateDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
		if err != nil {
			t.Fatalf("create sibling card: %v", err)
		}
		siblingTurn, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
			WorkspaceID:    f.WorkspaceID,
			CardID:         cardB.ID,
			AuthorSourceID: f.HumanID,
			Content:        "另一张卡的正常消息",
			IdempotencyKey: "sibling-msg-" + uuid.NewString(),
			DispatchMode:   domain.DispatchModeEnqueue,
		})
		if err != nil {
			t.Fatalf("submit sibling comment: %v", err)
		}
		claimedSibling, err := s.ClaimNextBuiltInAttempt(ctx, 2, time.Now().UTC(), time.Minute)
		if err != nil || claimedSibling == nil {
			t.Fatalf("expected sibling card to be claimed normally, got claim=%+v err=%v", claimedSibling, err)
		}
		if claimedSibling.Turn.CardID != cardB.ID || claimedSibling.Turn.ID != siblingTurn.TurnID {
			t.Fatalf("claimed unexpected card/turn: card=%s turn=%s", claimedSibling.Turn.CardID, claimedSibling.Turn.ID)
		}
		if err := s.FinishAttempt(ctx, f.WorkspaceID, cardB.ID, claimedSibling.Attempt.ID, domain.AttemptStatusCompleted, "", time.Now().UTC()); err != nil {
			t.Fatalf("finish sibling attempt: %v", err)
		}

		// 5. 解决卡片 A 的 pending 审批后，应触发 workReady 钩子并解除阻塞
		beforeSignals := workReadyCount.Load()
		if _, err := s.ResolveToolApproval(ctx, ResolveToolApprovalParams{
			WorkspaceID:        f.WorkspaceID,
			CardID:             f.CardID,
			ApprovalID:         approvalID,
			Status:             string(domain.ToolApprovalStatusApproved),
			ResolvedBySourceID: f.HumanID,
		}); err != nil {
			t.Fatalf("resolve tool approval: %v", err)
		}
		if workReadyCount.Load() <= beforeSignals {
			t.Fatalf("expected ResolveToolApproval to notify workReady hook")
		}

		// 6. 现在应可以正常 claim 卡片 A 队列中的 Turn
		claimedAfter, err := s.ClaimNextBuiltInAttempt(ctx, 2, time.Now().UTC(), time.Minute)
		if err != nil || claimedAfter == nil {
			t.Fatalf("expected queued turn to become claimable after resolve, got claim=%+v err=%v", claimedAfter, err)
		}
		if claimedAfter.Turn.CardID != f.CardID || claimedAfter.Turn.ID != queuedTurn.Turn.ID {
			t.Fatalf("claimed wrong turn after resolve: card=%s turn=%s want_turn=%s",
				claimedAfter.Turn.CardID, claimedAfter.Turn.ID, queuedTurn.Turn.ID)
		}
	})

	t.Run("human_interrupt_supersedes_pending_and_unblocks_immediately", func(t *testing.T) {
		s := requireIntegration(t)
		f := newFixture(t, s, domain.CardKindTodo)
		approvalID := createPendingApprovalFixture(t, s, f, "exec_command")

		// 人工通过 interrupt 发来新指令，自动 supersede 旧 pending 审批并让新 Turn 立即可被 claim
		interruptRes, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
			WorkspaceID:    f.WorkspaceID,
			CardID:         f.CardID,
			AuthorSourceID: f.HumanID,
			Content:        "取消部署，先检查容器日志",
			IdempotencyKey: "interrupt-" + uuid.NewString(),
			DispatchMode:   domain.DispatchModeInterrupt,
		})
		if err != nil {
			t.Fatalf("submit interrupt comment: %v", err)
		}

		approval, err := s.GetToolApproval(ctx, f.WorkspaceID, f.CardID, approvalID)
		if err != nil {
			t.Fatalf("get tool approval: %v", err)
		}
		if approval.Status != string(domain.ToolApprovalStatusSuperseded) {
			t.Fatalf("expected superseded approval, got %s", approval.Status)
		}

		claimed, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatalf("expected interrupt turn to be immediately claimable, got %v", err)
		}
		if claimed.Turn.ID != interruptRes.TurnID {
			t.Fatalf("claimed turn=%s want=%s", claimed.Turn.ID, interruptRes.TurnID)
		}
	})
}

// TestConsumeToolApproval_AtomicSingleUse 验证单次审批凭证在并发抢占与重复调用下严格只能消费一次。
func TestConsumeToolApproval_AtomicSingleUse(t *testing.T) {
	ctx := context.Background()
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)

	approvalID := createPendingApprovalFixture(t, s, f, "exec_command")
	if _, err := s.ResolveToolApproval(ctx, ResolveToolApprovalParams{
		WorkspaceID:        f.WorkspaceID,
		CardID:             f.CardID,
		ApprovalID:         approvalID,
		Status:             string(domain.ToolApprovalStatusApproved),
		ResolvedBySourceID: f.HumanID,
	}); err != nil {
		t.Fatalf("resolve tool approval: %v", err)
	}

	unconsumed, err := s.ListUnconsumedApprovedToolApprovals(ctx, f.WorkspaceID, f.CardID, "exec_command")
	if err != nil {
		t.Fatalf("list unconsumed approvals: %v", err)
	}
	if len(unconsumed) != 1 || unconsumed[0].ID != approvalID || unconsumed[0].ConsumedAt.Valid {
		t.Fatalf("expected 1 unconsumed approval, got %+v", unconsumed)
	}

	// 并发 8 协程同时尝试消费同一审批记录，严格仅允许 1 个成功
	const workers = 8
	var consumedCount atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, ok, consumeErr := s.ConsumeToolApproval(ctx, f.WorkspaceID, f.CardID, approvalID)
			if consumeErr != nil {
				t.Errorf("consume error: %v", consumeErr)
				return
			}
			if ok {
				consumedCount.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := consumedCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 concurrent consumer to succeed, got %d", got)
	}

	// 消费后再次查询未消费列表必须为空，且数据库已落库 consumed_at
	afterList, err := s.ListUnconsumedApprovedToolApprovals(ctx, f.WorkspaceID, f.CardID, "exec_command")
	if err != nil {
		t.Fatalf("list unconsumed after consume: %v", err)
	}
	if len(afterList) != 0 {
		t.Fatalf("expected 0 unconsumed approvals after consumption, got %d", len(afterList))
	}
	afterRow, err := s.GetToolApproval(ctx, f.WorkspaceID, f.CardID, approvalID)
	if err != nil {
		t.Fatalf("get approval after consume: %v", err)
	}
	if !afterRow.ConsumedAt.Valid {
		t.Fatalf("expected consumed_at to be populated after consumption")
	}
}
