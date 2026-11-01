package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestBuiltInProactiveTriggerWindow_三个时段均在前后一小时内浮动(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	slots := BuiltInProactiveSlots()
	if len(slots) != 3 || slots[0].Hour != 10 || slots[1].Hour != 14 || slots[2].Hour != 20 {
		t.Fatalf("期望 10点/14点/20点 3 个内置时段，实际: %+v", slots)
	}

	projectID := uuid.New()
	day := time.Date(2026, 11, 1, 8, 0, 0, 0, loc)
	for _, slot := range slots {
		triggerAt, windowEnd, dateKey, err := BuiltInProactiveTriggerWindow(projectID, day, slot)
		if err != nil {
			t.Fatal(err)
		}
		if dateKey != "2026-11-01" {
			t.Fatalf("dateKey=%q, want 2026-11-01", dateKey)
		}
		base := time.Date(2026, 11, 1, slot.Hour, 0, 0, 0, loc)
		minTime := base.Add(-time.Hour)
		maxTime := base.Add(time.Hour)
		if triggerAt.Before(minTime) || triggerAt.After(maxTime) {
			t.Fatalf("slot %s triggerAt=%s 超出 [%s, %s] 浮动范围", slot.Key, triggerAt.In(loc), minTime, maxTime)
		}
		if !windowEnd.After(maxTime) {
			t.Fatalf("slot %s windowEnd=%s 应晚于浮动上限 %s", slot.Key, windowEnd.In(loc), maxTime)
		}
		// 同一项目同一天重复计算必须保持稳定。
		again, _, _, err := BuiltInProactiveTriggerWindow(projectID, day.Add(3*time.Hour), slot)
		if err != nil || !again.Equal(triggerAt) {
			t.Fatalf("同一天触发时间不稳定: first=%s second=%s err=%v", triggerAt, again, err)
		}
	}
}

func TestProcessBuiltInProactiveTurns_三时段触发且不在前端展示队列或日程(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	f := newFixture(t, s, domain.CardKindTodo)

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	baseDay := time.Date(2026, 11, 1, 7, 0, 0, 0, loc)
	if _, err := s.pool.Exec(ctx, `UPDATE projects SET created_at = $3 WHERE workspace_id = $1 AND id = $2`,
		f.WorkspaceID, f.ProjectID, baseDay.UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAgentSettings(ctx, f.WorkspaceID, AgentSettingsInput{
		Routes: []ModelRouteInput{{
			Enabled:         true,
			ProviderType:    "openai_compatible",
			ProviderBaseURL: "https://example.com/v1",
			Model:           "gpt-test",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	// 用户在项目中留下近期关注消息，供内置定时任务提取兴趣上下文。
	userComment, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AuthorSourceID: f.HumanID,
		Content:        "最近在关注 Mac mini 节点的磁盘空间与 Go 编译缓存清理",
		IdempotencyKey: "recent-interest-msg",
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	userClaim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, baseDay.Add(5*time.Minute).UTC(), time.Minute)
	if err != nil || userClaim.Turn.ID != userComment.TurnID {
		t.Fatalf("claim user turn: %+v err=%v", userClaim, err)
	}
	if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, userClaim.Attempt.ID, domain.AttemptStatusCompleted, "", baseDay.Add(6*time.Minute).UTC()); err != nil {
		t.Fatal(err)
	}

	for _, slot := range BuiltInProactiveSlots() {
		triggerAt, _, _, err := BuiltInProactiveTriggerWindow(f.ProjectID, baseDay, slot)
		if err != nil {
			t.Fatal(err)
		}
		// 到点前不触发。
		before, err := s.ProcessBuiltInProactiveTurns(ctx, f.WorkspaceID, triggerAt.Add(-time.Second))
		if err != nil || len(before) != 0 {
			t.Fatalf("slot %s 到点前不应触发: created=%v err=%v", slot.Key, before, err)
		}

		// 到点触发 1 条主聊天系统 Turn；重复扫描幂等不重复入队。
		dueNow := triggerAt.Add(time.Minute)
		created, err := s.ProcessBuiltInProactiveTurns(ctx, f.WorkspaceID, dueNow)
		if err != nil || len(created) != 1 {
			t.Fatalf("slot %s 到点应创建 1 条 Turn: created=%v err=%v", slot.Key, created, err)
		}
		duplicated, err := s.ProcessBuiltInProactiveTurns(ctx, f.WorkspaceID, dueNow.Add(time.Minute))
		if err != nil || len(duplicated) != 0 {
			t.Fatalf("slot %s 同一天不应重复触发: duplicated=%v err=%v", slot.Key, duplicated, err)
		}

		mainChat, err := s.MainDiscussion(ctx, f.WorkspaceID, f.ProjectID)
		if err != nil {
			t.Fatalf("自动确保主聊天存在失败: %v", err)
		}

		// 前端日程列表与待执行队列均不展示该内置定时任务。
		projectSchedules, err := s.ListProjectSchedules(ctx, f.WorkspaceID, f.ProjectID)
		if err != nil || len(projectSchedules) != 0 {
			t.Fatalf("内置定时任务不应出现在项目日程中: %+v err=%v", projectSchedules, err)
		}
		pendingTurns, err := s.ListPendingTurns(ctx, f.WorkspaceID, mainChat.ID)
		if err != nil || len(pendingTurns) != 0 {
			t.Fatalf("内置定时任务排队阶段不应在前端待执行队列展示: %+v err=%v", pendingTurns, err)
		}

		claim, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, dueNow, time.Minute)
		if err != nil {
			t.Fatalf("认领内置定时任务 Turn 失败: %v", err)
		}
		if claim.Turn.ID != created[0] || claim.Turn.CardID != mainChat.ID {
			t.Fatalf("认领到的 Turn 不匹配主聊天: %+v", claim)
		}
		if !strings.Contains(claim.Turn.Content, "Mac mini 节点的磁盘空间") {
			t.Fatalf("Turn 内容应包含用户最近可能感兴趣的消息，实际: %s", claim.Turn.Content)
		}

		// 运行期间不在前端展示 current_attempt 运行态。
		currentView, err := s.CurrentAttemptView(ctx, f.WorkspaceID, mainChat.ID)
		if err != nil || currentView != nil {
			t.Fatalf("内置定时任务运行期间不应暴露 current_attempt: %+v err=%v", currentView, err)
		}

		cardCtx, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim.Attempt.ID, f.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if !cardCtx.IsMainDiscussion || !cardCtx.IsBuiltInProactive {
			t.Fatalf("期望识别为主聊天内置主动任务，实际: IsMainDiscussion=%v IsBuiltInProactive=%v",
				cardCtx.IsMainDiscussion, cardCtx.IsBuiltInProactive)
		}

		// 最终发 1 条气泡到主聊天并完成 Attempt。
		if _, _, err := s.PublishReply(ctx, PublishReplyParams{
			WorkspaceID:    f.WorkspaceID,
			CardID:         mainChat.ID,
			AttemptID:      claim.Attempt.ID,
			RunnerSourceID: f.AgentID,
			Message:        ReplyMessage{Type: "text", Content: "顺手帮你看了一眼 Mac mini 的磁盘状态，目前剩余空间充足。", EndTurn: true},
			IdempotencyKey: "proactive-reply-" + slot.Key,
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteAttempt(ctx, f.WorkspaceID, mainChat.ID, claim.Attempt.ID, f.AgentID, dueNow.Add(10*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	mainChat, err := s.MainDiscussion(ctx, f.WorkspaceID, f.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	timeline, err := s.ListCardTimeline(ctx, f.WorkspaceID, mainChat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 3 {
		t.Fatalf("3 个时段执行后主聊天时间线应只有 3 条最终回复气泡（不含触发提示词），实际 %d 条: %+v", len(timeline), timeline)
	}
}
