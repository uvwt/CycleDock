package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestListCardsForAgent_过滤排序截断与多租户隔离(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()

	// Workspace A：包含 2 个项目（Project A1、Project A2）
	wsA, err := s.CreateWorkspace(ctx, "Workspace A")
	if err != nil {
		t.Fatal(err)
	}
	humanA, err := s.CreateSource(ctx, wsA.ID, domain.SourceKindHuman, "Human A")
	if err != nil {
		t.Fatal(err)
	}
	agentA, err := s.CreateSource(ctx, wsA.ID, domain.SourceKindCycleAgent, "Agent A")
	if err != nil {
		t.Fatal(err)
	}
	projA1, err := s.CreateProject(ctx, wsA.ID, "项目 Alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	projA2, err := s.CreateProject(ctx, wsA.ID, "项目 Beta", "")
	if err != nil {
		t.Fatal(err)
	}

	// Workspace B：用于验证严格多租户隔离
	wsB, err := s.CreateWorkspace(ctx, "Workspace B")
	if err != nil {
		t.Fatal(err)
	}
	humanB, err := s.CreateSource(ctx, wsB.ID, domain.SourceKindHuman, "Human B")
	if err != nil {
		t.Fatal(err)
	}
	projB, err := s.CreateProject(ctx, wsB.ID, "其他工作区项目", "")
	if err != nil {
		t.Fatal(err)
	}
	otherWsCard, err := s.CreateTodoCard(ctx, wsB.ID, projB.ID, humanB.ID, "WorkspaceB Secret Task", "不应跨租户泄露", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	// 在 Project A1 创建覆盖所有类型/状态/健康度/待审批/置顶/静音的卡片
	mainDisc, _, err := s.EnsureMainDiscussion(ctx, wsA.ID, projA1.ID, humanA.ID)
	if err != nil {
		t.Fatal(err)
	}

	longDesc := strings.Repeat("这是很长的描述用于验证截断摘要功能", 15)
	todoWorking, err := s.CreateTodoCard(ctx, wsA.ID, projA1.ID, humanA.ID, "Fix Login_100% Bug", longDesc, uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTodo(ctx, wsA.ID, todoWorking.ID, humanA.ID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}

	todoPendingApproval, err := s.CreateTodoCard(ctx, wsA.ID, projA1.ID, humanA.ID, "待审批部署任务", "需要审批", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateToolApproval(ctx, CreateToolApprovalParams{
		WorkspaceID: wsA.ID,
		CardID:      todoPendingApproval.ID,
		ToolCallID:  "call-approval-1",
		ToolName:    "exec_command",
		Arguments:   map[string]any{"cmd": "deploy"},
		RiskLevel:   "high",
		Reason:      "需要人工确认部署",
	}); err != nil {
		t.Fatal(err)
	}

	todoPinned, err := s.CreateTodoCard(ctx, wsA.ID, projA1.ID, humanA.ID, "置顶需求卡片", "已置顶且静音", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE cards
		SET pinned_at = now(), notifications_muted = true
		WHERE workspace_id = $1 AND id = $2
	`, wsA.ID, todoPinned.ID); err != nil {
		t.Fatal(err)
	}

	todoDone, err := s.CreateTodoCard(ctx, wsA.ID, projA1.ID, humanA.ID, "已完成历史任务", "已完成", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTodo(ctx, wsA.ID, todoDone.ID, humanA.ID, domain.TodoStateWorking); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTodo(ctx, wsA.ID, todoDone.ID, humanA.ID, domain.TodoStateReview); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveTodo(ctx, wsA.ID, todoDone.ID, humanA.ID, domain.TodoStateDone); err != nil {
		t.Fatal(err)
	}

	ongoingCritical, err := s.CreateOngoingCard(ctx, wsA.ID, projA1.ID, humanA.ID, "核心数据库巡检", "严重告警巡检", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	ongoingWarning, err := s.CreateOngoingCard(ctx, wsA.ID, projA1.ID, humanA.ID, "磁盘容量巡检", "告警巡检", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	ongoingAcked, err := s.CreateOngoingCard(ctx, wsA.ID, projA1.ID, humanA.ID, "已确认告警巡检", "已确认", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	ongoingHealthy, err := s.CreateOngoingCard(ctx, wsA.ID, projA1.ID, humanA.ID, "健康巡检任务", "一切正常", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	ongoingUnknown, err := s.CreateOngoingCard(ctx, wsA.ID, projA1.ID, humanA.ID, "未执行过的巡检", "unknown", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	ongoingArchived, err := s.CreateOngoingCard(ctx, wsA.ID, projA1.ID, humanA.ID, "已归档巡检", "archived", uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetOngoingState(ctx, wsA.ID, ongoingArchived.ID, humanA.ID, domain.OngoingStateArchived, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// 给 ongoing 卡片写入真实 checkin 与 attempt 状态
	recordCheckin := func(cardID uuid.UUID, status domain.CheckinStatus, finish bool, key string) {
		t.Helper()
		if _, err := s.CreateHumanTurn(ctx, wsA.ID, cardID, humanA.ID, "触发检查", "turn-"+key, domain.DispatchModeEnqueue); err != nil {
			t.Fatal(err)
		}
		claim, err := s.ClaimCardTurnAttempt(ctx, wsA.ID, cardID, agentA.ID, time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatalf("claim attempt for %s: %v", key, err)
		}
		if _, _, err := s.AddAttemptEvent(ctx, AttemptEventParams{
			WorkspaceID:    wsA.ID,
			CardID:         cardID,
			AttemptID:      claim.Attempt.ID,
			RunnerSourceID: agentA.ID,
			Type:           domain.EventTypeCheckin,
			Content:        "check-in " + string(status),
			Checkin:        &status,
			IdempotencyKey: "checkin-" + key,
		}); err != nil {
			t.Fatal(err)
		}
		if finish {
			if err := s.CompleteAttempt(ctx, wsA.ID, cardID, claim.Attempt.ID, agentA.ID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
	}

	recordCheckin(ongoingCritical.ID, domain.CheckinCritical, false, "crit") // 保持 running
	recordCheckin(ongoingWarning.ID, domain.CheckinWarning, true, "warn")
	recordCheckin(ongoingAcked.ID, domain.CheckinWarning, true, "ack")
	if err := s.AcknowledgeCheckin(ctx, wsA.ID, ongoingAcked.ID, humanA.ID, "已知悉"); err != nil {
		t.Fatal(err)
	}
	recordCheckin(ongoingHealthy.ID, domain.CheckinHealthy, true, "healthy")

	// Project A2 的卡片（用于验证 scope=project vs scope=workspace）
	projA2Todo, err := s.CreateTodoCard(ctx, wsA.ID, projA2.ID, humanA.ID, "项目 Beta 专属任务", "跨项目测试", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 默认 scope=project、include_done=false：不包含 done/archived、不包含 Project A2、不包含 Workspace B
	res, err := s.ListCardsForAgent(ctx, AgentListCardsParams{
		WorkspaceID: wsA.ID,
		ProjectID:   projA1.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Truncated || res.TotalMatched != len(res.Items) {
		t.Fatalf("unexpected pagination meta: total=%d len=%d truncated=%v", res.TotalMatched, len(res.Items), res.Truncated)
	}
	byID := make(map[uuid.UUID]AgentCardListItem, len(res.Items))
	for _, item := range res.Items {
		byID[item.CardID] = item
		if item.CardID == todoDone.ID || item.CardID == ongoingArchived.ID {
			t.Fatalf("default query must exclude done/archived card: %+v", item)
		}
		if item.CardID == projA2Todo.ID {
			t.Fatalf("default scope=project must exclude other project card: %+v", item)
		}
		if item.CardID == otherWsCard.ID {
			t.Fatalf("must never leak other workspace card: %+v", item)
		}
	}

	// 验证排序优先级：critical (0) -> warning (1) -> pending approval (2) -> pinned (3)
	if len(res.Items) < 4 {
		t.Fatalf("expected at least 4 cards, got %d", len(res.Items))
	}
	if res.Items[0].CardID != ongoingCritical.ID ||
		res.Items[1].CardID != ongoingWarning.ID ||
		res.Items[2].CardID != todoPendingApproval.ID ||
		res.Items[3].CardID != todoPinned.ID {
		t.Fatalf("unexpected priority order: got [%s, %s, %s, %s], want [critical, warning, pendingApproval, pinned]",
			res.Items[0].Title, res.Items[1].Title, res.Items[2].Title, res.Items[3].Title)
	}

	// 验证关键字段完整度
	critItem := byID[ongoingCritical.ID]
	if critItem.Health != "critical" || critItem.CurrentAttemptStatus != "running" || critItem.LatestAttemptStatus != "running" || critItem.ProjectName != "项目 Alpha" {
		t.Fatalf("unexpected critical card item: %+v", critItem)
	}
	pinnedItem := byID[todoPinned.ID]
	if !pinnedItem.Pinned || !pinnedItem.NotificationsMuted || pinnedItem.Health != "" {
		t.Fatalf("unexpected pinned card item: %+v", pinnedItem)
	}
	approvalItem := byID[todoPendingApproval.ID]
	if approvalItem.PendingToolApprovalCount != 1 {
		t.Fatalf("expected pending_tool_approval_count=1, got %d", approvalItem.PendingToolApprovalCount)
	}
	mainItem, ok := byID[mainDisc.ID]
	if !ok || !mainItem.IsMainDiscussion {
		t.Fatalf("expected main discussion card with is_main_discussion=true, got %+v (found=%v)", mainItem, ok)
	}
	workingItem := byID[todoWorking.ID]
	if !strings.HasSuffix(workingItem.DescriptionSummary, "…") || len([]rune(workingItem.DescriptionSummary)) > agentCardDescSummaryRunes+1 {
		t.Fatalf("expected truncated description_summary, got %q", workingItem.DescriptionSummary)
	}

	// 2. scope=workspace：包含 Project A2，仍严格排除 Workspace B
	wsRes, err := s.ListCardsForAgent(ctx, AgentListCardsParams{
		WorkspaceID: wsA.ID,
		ProjectID:   projA1.ID,
		Scope:       "workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	var foundProjA2 bool
	for _, item := range wsRes.Items {
		if item.CardID == projA2Todo.ID && item.ProjectName == "项目 Beta" {
			foundProjA2 = true
		}
		if item.CardID == otherWsCard.ID {
			t.Fatalf("scope=workspace leaked other workspace card")
		}
	}
	if !foundProjA2 {
		t.Fatalf("expected Project A2 card in scope=workspace results")
	}

	// 3. health=abnormal：仅匹配 warning 与 critical（不包含 acknowledged、healthy、unknown 或 todo）
	abnRes, err := s.ListCardsForAgent(ctx, AgentListCardsParams{
		WorkspaceID: wsA.ID,
		ProjectID:   projA1.ID,
		Health:      "abnormal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(abnRes.Items) != 2 || abnRes.Items[0].CardID != ongoingCritical.ID || abnRes.Items[1].CardID != ongoingWarning.ID {
		t.Fatalf("health=abnormal unexpected result: %+v", abnRes.Items)
	}

	// health=acknowledged 与 health=unknown
	ackRes, err := s.ListCardsForAgent(ctx, AgentListCardsParams{
		WorkspaceID: wsA.ID,
		ProjectID:   projA1.ID,
		Health:      "acknowledged",
	})
	if err != nil || len(ackRes.Items) != 1 || ackRes.Items[0].CardID != ongoingAcked.ID {
		t.Fatalf("health=acknowledged unexpected: %+v err=%v", ackRes.Items, err)
	}
	unkRes, err := s.ListCardsForAgent(ctx, AgentListCardsParams{
		WorkspaceID: wsA.ID,
		ProjectID:   projA1.ID,
		Health:      "unknown",
	})
	if err != nil || len(unkRes.Items) != 1 || unkRes.Items[0].CardID != ongoingUnknown.ID {
		t.Fatalf("health=unknown unexpected: %+v err=%v", unkRes.Items, err)
	}

	// 4. query 不区分大小写且正确转义 % 和 _
	queryRes, err := s.ListCardsForAgent(ctx, AgentListCardsParams{
		WorkspaceID: wsA.ID,
		ProjectID:   projA1.ID,
		Query:       "login_100%",
	})
	if err != nil || len(queryRes.Items) != 1 || queryRes.Items[0].CardID != todoWorking.ID {
		t.Fatalf("query filter unexpected: %+v err=%v", queryRes.Items, err)
	}

	// 5. include_done 与显式 state=done / archived
	doneByState, err := s.ListCardsForAgent(ctx, AgentListCardsParams{
		WorkspaceID: wsA.ID,
		ProjectID:   projA1.ID,
		State:       "done",
	})
	if err != nil || len(doneByState.Items) != 1 || doneByState.Items[0].CardID != todoDone.ID {
		t.Fatalf("state=done unexpected: %+v err=%v", doneByState.Items, err)
	}
	archivedByState, err := s.ListCardsForAgent(ctx, AgentListCardsParams{
		WorkspaceID: wsA.ID,
		ProjectID:   projA1.ID,
		State:       "archived",
	})
	if err != nil || len(archivedByState.Items) != 1 || archivedByState.Items[0].CardID != ongoingArchived.ID {
		t.Fatalf("state=archived unexpected: %+v err=%v", archivedByState.Items, err)
	}
	withDone, err := s.ListCardsForAgent(ctx, AgentListCardsParams{
		WorkspaceID: wsA.ID,
		ProjectID:   projA1.ID,
		IncludeDone: true,
	})
	if err != nil || withDone.TotalMatched != res.TotalMatched+2 {
		t.Fatalf("include_done=true expected total=%d, got %d (err=%v)", res.TotalMatched+2, withDone.TotalMatched, err)
	}

	// 6. kind 过滤 + limit 截断元数据
	limited, err := s.ListCardsForAgent(ctx, AgentListCardsParams{
		WorkspaceID: wsA.ID,
		ProjectID:   projA1.ID,
		Kind:        "ongoing",
		Limit:       2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Items) != 2 || limited.TotalMatched != 5 || !limited.Truncated {
		t.Fatalf("limit truncation unexpected: len=%d total=%d truncated=%v", len(limited.Items), limited.TotalMatched, limited.Truncated)
	}
}
