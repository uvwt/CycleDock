import test from "node:test";
import assert from "node:assert/strict";
import {
  buildFocusSummary,
  getCardFocusReason,
  getCardVisualStatus,
  getFocusCardVisualStatus,
  getKindVisualStatus,
  groupFocusCards,
} from "../src/shared/lib/card-focus.ts";

function makeCard(overrides) {
  return {
    id: "card-default",
    project_id: "proj-1",
    kind: "discussion",
    title: "默认卡片",
    description: "",
    created_at: "2026-11-08T01:00:00Z",
    updated_at: "2026-11-08T01:00:00Z",
    ...overrides,
  };
}

test("跨类型焦点聚合按 运行中 > 未读回复 > 待验收 > 进行中 排序，且过滤静默与已归档卡片", () => {
  const cards = [
    makeCard({
      id: "todo-working",
      kind: "todo",
      todo_state: "working",
      title: "进行中任务",
      updated_at: "2026-11-08T05:00:00Z",
    }),
    makeCard({
      id: "todo-review",
      kind: "todo",
      todo_state: "review",
      title: "待验收任务",
      updated_at: "2026-11-08T04:00:00Z",
    }),
    makeCard({
      id: "disc-unread",
      kind: "discussion",
      discussion_state: "active",
      unread: true,
      title: "有新消息的讨论",
      updated_at: "2026-11-08T03:00:00Z",
    }),
    makeCard({
      id: "ongoing-running",
      kind: "ongoing",
      ongoing_state: "active",
      current_attempt_status: "running",
      title: "正在执行的定时巡检",
      updated_at: "2026-11-08T02:00:00Z",
    }),
    makeCard({
      id: "disc-archived-unread",
      kind: "discussion",
      discussion_state: "archived",
      unread: true,
      title: "已归档的旧讨论",
      updated_at: "2026-11-08T06:00:00Z",
    }),
    makeCard({
      id: "todo-idle",
      kind: "todo",
      todo_state: "todo",
      title: "普通待办",
      updated_at: "2026-11-08T07:00:00Z",
    }),
  ];

  const summary = buildFocusSummary(cards);

  assert.deepEqual(
    summary.cards.map((c) => c.id),
    ["ongoing-running", "disc-unread", "todo-review", "todo-working"],
  );
  assert.equal(summary.answeringCount, 1);
  assert.equal(summary.unreadCount, 1);
  assert.equal(summary.reviewCount, 1);
  assert.equal(summary.workingCount, 1);
  assert.equal(summary.highestVisualStatus, "answering");
});

test("正在查看当前卡片时抑制 unread 焦点，但保留实时运行状态与业务状态", () => {
  const unreadDiscussion = makeCard({
    id: "disc-1",
    kind: "discussion",
    unread: true,
  });
  const unreadReviewTodo = makeCard({
    id: "todo-1",
    kind: "todo",
    todo_state: "review",
    unread: true,
  });

  // 未处于当前查看态时，二者均因为 unread 进入焦点
  assert.equal(getCardFocusReason(unreadDiscussion, false), "unread");
  assert.equal(getCardFocusReason(unreadReviewTodo, false), "unread");

  // 正在查看时，普通讨论不再算作未读焦点，而待验收任务仍保留 review 焦点原因
  assert.equal(getCardFocusReason(unreadDiscussion, true), null);
  assert.equal(getCardFocusReason(unreadReviewTodo, true), "review");
  assert.equal(
    getCardFocusReason(unreadDiscussion, true, "running"),
    "answering",
  );
});

test("导航分类状态点按 answering > unread > idle 聚合对应类型的卡片", () => {
  const cards = [
    makeCard({ id: "d1", kind: "discussion", unread: true }),
    makeCard({
      id: "t1",
      kind: "todo",
      todo_state: "working",
      current_attempt_status: "queued",
    }),
    makeCard({ id: "o1", kind: "ongoing", ongoing_state: "active" }),
  ];

  assert.equal(getKindVisualStatus(cards, "discussion"), "unread");
  // 若用户正在查看 d1，则讨论分类不再亮未读蓝点
  assert.equal(getKindVisualStatus(cards, "discussion", "d1"), "idle");
  assert.equal(getKindVisualStatus(cards, "todo"), "answering");
  assert.equal(getKindVisualStatus(cards, "ongoing"), "idle");
});

test("工具调用挂起等待人工确认的卡片进入焦点列表，并排在运行中之前", () => {
  const cards = [
    makeCard({
      id: "ongoing-running",
      kind: "ongoing",
      ongoing_state: "active",
      current_attempt_status: "running",
      updated_at: "2026-11-08T09:00:00Z",
    }),
    // 挂起后 Attempt 已结束，current_attempt_status 为空，只剩待确认数量
    makeCard({
      id: "todo-awaiting-approval",
      kind: "todo",
      todo_state: "working",
      pending_tool_approval_count: 1,
      updated_at: "2026-11-08T02:00:00Z",
    }),
    makeCard({
      id: "disc-awaiting-approval",
      kind: "discussion",
      discussion_state: "active",
      pending_tool_approval_count: 2,
      updated_at: "2026-11-08T03:00:00Z",
    }),
    makeCard({
      id: "disc-unread",
      kind: "discussion",
      unread: true,
      updated_at: "2026-11-08T08:00:00Z",
    }),
  ];

  const summary = buildFocusSummary(cards);

  assert.deepEqual(
    summary.cards.map((c) => c.id),
    [
      "disc-awaiting-approval",
      "todo-awaiting-approval",
      "ongoing-running",
      "disc-unread",
    ],
  );
  assert.equal(summary.approvalCount, 2);
  assert.equal(summary.answeringCount, 1);
  assert.equal(summary.unreadCount, 1);
  assert.equal(summary.workingCount, 0);
  // 有卡片仍在运行时角标保持绿色；只剩待确认时角标为橙色
  assert.equal(summary.highestVisualStatus, "answering");
  assert.equal(
    buildFocusSummary(cards.slice(1)).highestVisualStatus,
    "approval",
  );
});

test("待确认状态在查看中、后台已有新运行时都保留；归档或审批处理完后退出焦点", () => {
  const pending = makeCard({
    id: "disc-pending",
    kind: "discussion",
    discussion_state: "active",
    pending_tool_approval_count: 1,
  });

  // 正在查看该卡片也不能抑制，否则用户会错过需要处理的审批
  assert.equal(getCardFocusReason(pending, true), "approval");
  assert.equal(getCardVisualStatus(pending, true), "approval");
  // 同卡片又有新的 Turn 在运行时，焦点原因仍是待确认，状态点展示运行中
  const runningAgain = { ...pending, current_attempt_status: "running" };
  assert.equal(getCardFocusReason(runningAgain), "approval");
  assert.equal(getCardVisualStatus(runningAgain), "answering");

  assert.equal(
    getCardFocusReason({ ...pending, discussion_state: "archived" }),
    null,
  );
  assert.equal(
    getCardFocusReason({ ...pending, pending_tool_approval_count: 0 }),
    null,
  );
});

test("导航分类状态点：待确认优先于未读，低于运行中", () => {
  const cards = [
    makeCard({ id: "d1", kind: "discussion", unread: true }),
    makeCard({ id: "d2", kind: "discussion", pending_tool_approval_count: 1 }),
    makeCard({
      id: "t1",
      kind: "todo",
      todo_state: "working",
      pending_tool_approval_count: 1,
    }),
    makeCard({
      id: "t2",
      kind: "todo",
      todo_state: "working",
      current_attempt_status: "running",
    }),
  ];

  assert.equal(getKindVisualStatus(cards, "discussion"), "approval");
  assert.equal(getKindVisualStatus(cards, "todo"), "answering");
});

test("已静音卡片忽略运行中、未读、待验收和进行中，不进入焦点列表但保留自身真实状态点", () => {
  const mutedRunning = makeCard({
    id: "muted-running",
    kind: "ongoing",
    ongoing_state: "active",
    current_attempt_status: "running",
    notifications_muted: true,
  });
  const mutedUnread = makeCard({
    id: "muted-unread",
    kind: "discussion",
    discussion_state: "active",
    unread: true,
    notifications_muted: true,
  });
  const mutedReview = makeCard({
    id: "muted-review",
    kind: "todo",
    todo_state: "review",
    notifications_muted: true,
  });
  const mutedWorking = makeCard({
    id: "muted-working",
    kind: "todo",
    todo_state: "working",
    notifications_muted: true,
  });

  // 均不进入焦点（包括正在查看且活跃运行中的场景）
  assert.equal(getCardFocusReason(mutedRunning), null);
  assert.equal(getCardFocusReason(mutedUnread, true, "running"), null);
  assert.equal(getCardFocusReason(mutedUnread), null);
  assert.equal(getCardFocusReason(mutedReview), null);
  assert.equal(getCardFocusReason(mutedWorking), null);

  // 分类列表自身仍依赖 getCardVisualStatus 展示真实状态
  assert.equal(getCardVisualStatus(mutedRunning), "answering");
  assert.equal(getCardVisualStatus(mutedUnread), "unread");

  const summary = buildFocusSummary([
    mutedRunning,
    mutedUnread,
    mutedReview,
    mutedWorking,
  ]);
  assert.deepEqual(summary.cards, []);
  assert.equal(summary.approvalCount, 0);
  assert.equal(summary.answeringCount, 0);
  assert.equal(summary.unreadCount, 0);
  assert.equal(summary.reviewCount, 0);
  assert.equal(summary.workingCount, 0);
  assert.equal(summary.highestVisualStatus, "idle");
});

test("已静音卡片存在待人工确认审批时仍进入焦点并触发橙色状态，归档后退出", () => {
  const mutedApproval = makeCard({
    id: "muted-approval",
    kind: "todo",
    todo_state: "working",
    pending_tool_approval_count: 1,
    notifications_muted: true,
    updated_at: "2026-11-08T03:00:00Z",
  });
  // 静音卡同时有待审批与后台新运行 Turn，仍只按 approval 豁免，不计为 answering
  const mutedApprovalRunning = makeCard({
    id: "muted-approval-running",
    kind: "discussion",
    discussion_state: "active",
    current_attempt_status: "running",
    pending_tool_approval_count: 2,
    notifications_muted: true,
    updated_at: "2026-11-08T04:00:00Z",
  });
  const mutedUnread = makeCard({
    id: "muted-unread",
    kind: "discussion",
    discussion_state: "active",
    unread: true,
    notifications_muted: true,
    updated_at: "2026-11-08T05:00:00Z",
  });
  const mutedArchivedApproval = makeCard({
    id: "muted-archived-approval",
    kind: "discussion",
    discussion_state: "archived",
    pending_tool_approval_count: 1,
    notifications_muted: true,
    updated_at: "2026-11-08T06:00:00Z",
  });

  assert.equal(getCardFocusReason(mutedApproval), "approval");
  assert.equal(getCardFocusReason(mutedApprovalRunning), "approval");
  assert.equal(getCardFocusReason(mutedArchivedApproval), null);

  const summary = buildFocusSummary([
    mutedApproval,
    mutedApprovalRunning,
    mutedUnread,
    mutedArchivedApproval,
  ]);
  assert.deepEqual(
    summary.cards.map((c) => c.id),
    ["muted-approval-running", "muted-approval"],
  );
  assert.equal(summary.approvalCount, 2);
  assert.equal(summary.answeringCount, 0);
  assert.equal(summary.unreadCount, 0);
  assert.equal(summary.workingCount, 0);
  assert.equal(summary.highestVisualStatus, "approval");
});

test("导航分类状态点：已静音卡片不亮绿/蓝点，仅待审批挂起触发橙点", () => {
  const mutedOnlyCards = [
    makeCard({
      id: "d-muted-unread",
      kind: "discussion",
      unread: true,
      notifications_muted: true,
    }),
    makeCard({
      id: "d-muted-running",
      kind: "discussion",
      current_attempt_status: "running",
      notifications_muted: true,
    }),
    makeCard({
      id: "t-muted-queued",
      kind: "todo",
      todo_state: "working",
      current_attempt_status: "queued",
      notifications_muted: true,
    }),
    makeCard({
      id: "o-muted-approval-running",
      kind: "ongoing",
      ongoing_state: "active",
      current_attempt_status: "running",
      pending_tool_approval_count: 1,
      notifications_muted: true,
    }),
  ];

  assert.equal(getKindVisualStatus(mutedOnlyCards, "discussion"), "idle");
  assert.equal(getKindVisualStatus(mutedOnlyCards, "todo"), "idle");
  // 静音卡同时运行中 + 待审批：忽略运行中绿点，仍保留待审批橙点
  assert.equal(getKindVisualStatus(mutedOnlyCards, "ongoing"), "approval");

  // 混排场景：静音卡的审批优先于未静音卡的未读；未静音卡的运行中仍正常亮绿点
  const mixedCards = [
    ...mutedOnlyCards,
    makeCard({
      id: "o-unmuted-unread",
      kind: "ongoing",
      ongoing_state: "active",
      unread: true,
    }),
    makeCard({
      id: "t-unmuted-running",
      kind: "todo",
      todo_state: "working",
      current_attempt_status: "running",
    }),
  ];
  assert.equal(getKindVisualStatus(mixedCards, "ongoing"), "approval");
  assert.equal(getKindVisualStatus(mixedCards, "todo"), "answering");
});

test("定时任务巡检异常（critical）与需留意（warning）进入焦点并穿透静音，确认或恢复正常后退出", () => {
  const mutedCritical = makeCard({
    id: "ongoing-muted-critical",
    kind: "ongoing",
    ongoing_state: "active",
    health: "critical",
    notifications_muted: true,
    updated_at: "2026-11-08T03:00:00Z",
  });
  const mutedWarningRunning = makeCard({
    id: "ongoing-muted-warning-running",
    kind: "ongoing",
    ongoing_state: "active",
    health: "warning",
    current_attempt_status: "running",
    notifications_muted: true,
    updated_at: "2026-11-08T04:00:00Z",
  });
  const mutedHealthyUnread = makeCard({
    id: "ongoing-muted-healthy",
    kind: "ongoing",
    ongoing_state: "active",
    health: "healthy",
    unread: true,
    notifications_muted: true,
    updated_at: "2026-11-08T05:00:00Z",
  });
  const mutedAcknowledged = makeCard({
    id: "ongoing-muted-ack",
    kind: "ongoing",
    ongoing_state: "active",
    health: "acknowledged",
    unread: true,
    notifications_muted: true,
    updated_at: "2026-11-08T06:00:00Z",
  });
  const mutedArchivedCritical = makeCard({
    id: "ongoing-muted-archived-critical",
    kind: "ongoing",
    ongoing_state: "archived",
    health: "critical",
    notifications_muted: true,
    updated_at: "2026-11-08T07:00:00Z",
  });
  const unmutedUnread = makeCard({
    id: "disc-unread",
    kind: "discussion",
    discussion_state: "active",
    unread: true,
    updated_at: "2026-11-08T08:00:00Z",
  });

  // 正在查看中也不抑制未确认的巡检告警，只有已确认 / 恢复正常 / 归档才退出
  assert.equal(getCardFocusReason(mutedCritical, true), "critical");
  assert.equal(getCardVisualStatus(mutedCritical, true), "critical");
  assert.equal(getCardFocusReason(mutedWarningRunning, true), "warning");
  assert.equal(getCardVisualStatus(mutedWarningRunning, false), "answering");
  assert.equal(getCardFocusReason(mutedHealthyUnread), null);
  assert.equal(getCardFocusReason(mutedAcknowledged), null);
  assert.equal(getCardFocusReason(mutedArchivedCritical), null);

  const summary = buildFocusSummary([
    mutedWarningRunning,
    unmutedUnread,
    mutedCritical,
    mutedHealthyUnread,
    mutedAcknowledged,
    mutedArchivedCritical,
  ]);

  assert.deepEqual(
    summary.cards.map((c) => c.id),
    ["ongoing-muted-critical", "ongoing-muted-warning-running", "disc-unread"],
  );
  assert.equal(summary.criticalCount, 1);
  assert.equal(summary.warningCount, 1);
  assert.equal(summary.unreadCount, 1);
  assert.equal(summary.answeringCount, 0);
  assert.equal(summary.highestVisualStatus, "critical");

  // 只剩 warning + unread 时最高视觉状态为 warning
  const warningSummary = buildFocusSummary([mutedWarningRunning, unmutedUnread]);
  assert.equal(warningSummary.highestVisualStatus, "warning");
});

test("导航分类状态点：静音定时卡按 approval > critical > warning 穿透，忽略 running 与 unread", () => {
  const mutedWarningRunning = makeCard({
    id: "o-muted-warning",
    kind: "ongoing",
    ongoing_state: "active",
    health: "warning",
    current_attempt_status: "running",
    notifications_muted: true,
  });
  const mutedCritical = makeCard({
    id: "o-muted-critical",
    kind: "ongoing",
    ongoing_state: "active",
    health: "critical",
    notifications_muted: true,
  });
  const mutedAckRunning = makeCard({
    id: "o-muted-ack",
    kind: "ongoing",
    ongoing_state: "active",
    health: "acknowledged",
    current_attempt_status: "running",
    unread: true,
    notifications_muted: true,
  });

  assert.equal(getKindVisualStatus([mutedAckRunning], "ongoing"), "idle");
  assert.equal(getKindVisualStatus([mutedWarningRunning], "ongoing"), "warning");
  assert.equal(
    getKindVisualStatus([mutedWarningRunning, mutedCritical], "ongoing"),
    "critical",
  );
  assert.equal(
    getKindVisualStatus(
      [mutedWarningRunning, mutedCritical, { ...mutedCritical, pending_tool_approval_count: 1 }],
      "ongoing",
    ),
    "approval",
  );
});

test("所有未归档的置顶卡片均进入焦点列表，纯置顶按 pinned_at 倒序排在 working 之前且不触发额外状态点", () => {
  const pinnedDiscussion = makeCard({
    id: "disc-pinned-older",
    kind: "discussion",
    discussion_state: "active",
    pinned_at: "2026-11-08T01:00:00Z",
    updated_at: "2026-11-08T09:00:00Z",
  });
  const pinnedTodo = makeCard({
    id: "todo-pinned-newer",
    kind: "todo",
    todo_state: "todo",
    pinned_at: "2026-11-08T03:00:00Z",
    updated_at: "2026-11-08T02:00:00Z",
  });
  const pinnedDone = makeCard({
    id: "todo-pinned-done",
    kind: "todo",
    todo_state: "done",
    pinned_at: "2026-11-08T02:00:00Z",
    updated_at: "2026-11-08T08:00:00Z",
  });
  const pinnedOngoing = makeCard({
    id: "ongoing-pinned-healthy",
    kind: "ongoing",
    ongoing_state: "active",
    health: "healthy",
    pinned_at: "2026-11-08T04:00:00Z",
    updated_at: "2026-11-08T01:00:00Z",
  });
  const pinnedWorking = makeCard({
    id: "todo-pinned-working",
    kind: "todo",
    todo_state: "working",
    pinned_at: "2026-11-08T00:30:00Z",
    updated_at: "2026-11-08T01:00:00Z",
  });
  const unpinnedWorking = makeCard({
    id: "todo-unpinned-working",
    kind: "todo",
    todo_state: "working",
    updated_at: "2026-11-08T10:00:00Z",
  });
  const pinnedArchivedDiscussion = makeCard({
    id: "disc-pinned-archived",
    kind: "discussion",
    discussion_state: "archived",
    pinned_at: "2026-11-08T05:00:00Z",
  });
  const pinnedArchivedOngoing = makeCard({
    id: "ongoing-pinned-archived",
    kind: "ongoing",
    ongoing_state: "archived",
    pinned_at: "2026-11-08T06:00:00Z",
  });

  assert.equal(getCardFocusReason(pinnedDiscussion), "pinned");
  assert.equal(getCardFocusReason(pinnedTodo), "pinned");
  assert.equal(getCardFocusReason(pinnedDone), "pinned");
  assert.equal(getCardFocusReason(pinnedOngoing), "pinned");
  // 同时具备更高动态原因时保留更高原因，不降级为 pinned
  assert.equal(getCardFocusReason(pinnedWorking), "working");
  // 已归档卡片即使置顶也不进入日常焦点
  assert.equal(getCardFocusReason(pinnedArchivedDiscussion), null);
  assert.equal(getCardFocusReason(pinnedArchivedOngoing), null);

  const summary = buildFocusSummary([
    pinnedDiscussion,
    pinnedTodo,
    pinnedDone,
    pinnedOngoing,
    unpinnedWorking,
    pinnedWorking,
    pinnedArchivedDiscussion,
    pinnedArchivedOngoing,
  ]);

  // 纯置顶层（priority 6）排在进行中层（priority 7）之前且按 pinned_at 倒序；
  // 进行中层内已置顶任务优先于未置顶任务
  assert.deepEqual(
    summary.cards.map((c) => c.id),
    [
      "ongoing-pinned-healthy",
      "todo-pinned-newer",
      "todo-pinned-done",
      "disc-pinned-older",
      "todo-pinned-working",
      "todo-unpinned-working",
    ],
  );
  assert.equal(summary.workingCount, 2);
  assert.equal(summary.pinnedCount, 4);
  // 纯置顶与进行中不触发呼吸/未读/告警视觉状态点
  assert.equal(summary.highestVisualStatus, "idle");
  assert.equal(getKindVisualStatus([pinnedDiscussion], "discussion"), "idle");
  assert.equal(getKindVisualStatus([pinnedOngoing], "ongoing"), "idle");
});

test("焦点排序按紧急度分层（静默置顶不跨层抢占动态焦点），同层置顶优先，纯置顶按 pinned_at 倒序", () => {
  const pinnedApprovalOlderUpdate = makeCard({
    id: "pinned-approval-older-update",
    kind: "todo",
    todo_state: "working",
    pending_tool_approval_count: 1,
    pinned_at: "2026-11-08T02:00:00Z",
    updated_at: "2026-11-08T01:00:00Z",
  });
  const unpinnedApproval = makeCard({
    id: "unpinned-approval",
    kind: "todo",
    todo_state: "working",
    pending_tool_approval_count: 1,
    updated_at: "2026-11-08T10:00:00Z",
  });
  const pinnedCriticalOlderUpdate = makeCard({
    id: "pinned-critical-older-update",
    kind: "ongoing",
    ongoing_state: "active",
    health: "critical",
    pinned_at: "2026-11-08T02:00:00Z",
    updated_at: "2026-11-08T03:00:00Z",
  });
  const unpinnedCritical = makeCard({
    id: "unpinned-critical",
    kind: "ongoing",
    ongoing_state: "active",
    health: "critical",
    updated_at: "2026-11-08T10:00:00Z",
  });
  const unpinnedWarning = makeCard({
    id: "unpinned-warning",
    kind: "ongoing",
    ongoing_state: "active",
    health: "warning",
    updated_at: "2026-11-08T10:00:00Z",
  });
  const unpinnedAnswering = makeCard({
    id: "unpinned-answering",
    kind: "discussion",
    discussion_state: "active",
    current_attempt_status: "running",
    updated_at: "2026-11-08T10:00:00Z",
  });
  const pinnedUnreadNewerUpdate = makeCard({
    id: "pinned-unread-newer-update",
    kind: "discussion",
    discussion_state: "active",
    unread: true,
    pinned_at: "2026-11-08T02:00:00Z",
    updated_at: "2026-11-08T09:00:00Z",
  });
  const pinnedUnreadSameUpdateNewerPin = makeCard({
    id: "pinned-unread-same-update-newer-pin",
    kind: "discussion",
    discussion_state: "active",
    unread: true,
    pinned_at: "2026-11-08T08:00:00Z",
    updated_at: "2026-11-08T05:00:00Z",
  });
  const pinnedUnreadSameUpdateOlderPin = makeCard({
    id: "pinned-unread-same-update-older-pin",
    kind: "discussion",
    discussion_state: "active",
    unread: true,
    pinned_at: "2026-11-08T04:00:00Z",
    updated_at: "2026-11-08T05:00:00Z",
  });
  const unpinnedUnread = makeCard({
    id: "unpinned-unread",
    kind: "discussion",
    discussion_state: "active",
    unread: true,
    updated_at: "2026-11-08T10:00:00Z",
  });
  const unpinnedReview = makeCard({
    id: "unpinned-review",
    kind: "todo",
    todo_state: "review",
    updated_at: "2026-11-08T10:00:00Z",
  });
  const pinnedPlainNewer = makeCard({
    id: "pinned-plain-newer",
    kind: "discussion",
    discussion_state: "active",
    pinned_at: "2026-11-08T08:00:00Z",
    updated_at: "2026-11-08T01:00:00Z",
  });
  const pinnedPlainSamePinNewerUpdate = makeCard({
    id: "pinned-plain-same-pin-newer-update",
    kind: "discussion",
    discussion_state: "active",
    pinned_at: "2026-11-08T06:00:00Z",
    updated_at: "2026-11-08T09:00:00Z",
  });
  const pinnedPlainSamePinOlderUpdate = makeCard({
    id: "pinned-plain-same-pin-older-update",
    kind: "discussion",
    discussion_state: "active",
    pinned_at: "2026-11-08T06:00:00Z",
    updated_at: "2026-11-08T03:00:00Z",
  });
  const pinnedWorkingOlderUpdate = makeCard({
    id: "pinned-working-older-update",
    kind: "todo",
    todo_state: "working",
    pinned_at: "2026-11-08T01:00:00Z",
    updated_at: "2026-11-08T01:00:00Z",
  });
  const unpinnedWorking = makeCard({
    id: "unpinned-working",
    kind: "todo",
    todo_state: "working",
    updated_at: "2026-11-08T10:00:00Z",
  });

  // 置顶卡片仍保留其真实状态理由与视觉状态，不被降级
  assert.equal(getCardFocusReason(pinnedPlainNewer), "pinned");
  assert.equal(getCardFocusReason(pinnedCriticalOlderUpdate), "critical");
  assert.equal(getCardFocusReason(pinnedUnreadNewerUpdate), "unread");
  assert.equal(getCardFocusReason(pinnedWorkingOlderUpdate), "working");

  const summary = buildFocusSummary([
    unpinnedWorking,
    pinnedWorkingOlderUpdate,
    unpinnedReview,
    pinnedPlainSamePinOlderUpdate,
    pinnedPlainSamePinNewerUpdate,
    pinnedPlainNewer,
    unpinnedUnread,
    pinnedUnreadSameUpdateOlderPin,
    pinnedUnreadSameUpdateNewerPin,
    pinnedUnreadNewerUpdate,
    unpinnedAnswering,
    unpinnedWarning,
    unpinnedCritical,
    pinnedCriticalOlderUpdate,
    unpinnedApproval,
    pinnedApprovalOlderUpdate,
  ]);

  assert.deepEqual(
    summary.cards.map((c) => c.id),
    [
      "pinned-approval-older-update",
      "unpinned-approval",
      "pinned-critical-older-update",
      "unpinned-critical",
      "unpinned-warning",
      "unpinned-answering",
      "pinned-unread-newer-update",
      "pinned-unread-same-update-newer-pin",
      "pinned-unread-same-update-older-pin",
      "unpinned-unread",
      "unpinned-review",
      "pinned-plain-newer",
      "pinned-plain-same-pin-newer-update",
      "pinned-plain-same-pin-older-update",
      "pinned-working-older-update",
      "unpinned-working",
    ],
  );
  assert.equal(summary.pinnedCount, 3);
  assert.equal(summary.approvalCount, 2);
  assert.equal(summary.criticalCount, 2);
  assert.equal(summary.warningCount, 1);
  assert.equal(summary.answeringCount, 1);
  assert.equal(summary.unreadCount, 4);
  assert.equal(summary.reviewCount, 1);
  assert.equal(summary.workingCount, 2);
  assert.equal(summary.highestVisualStatus, "answering");
});

test("已静音但被置顶的未归档卡片仍保留在焦点列表中（按 pinned 入列，不误报运行中/未读）", () => {
  const mutedPinnedRunning = makeCard({
    id: "disc-muted-pinned-running",
    kind: "discussion",
    discussion_state: "active",
    current_attempt_status: "running",
    unread: true,
    notifications_muted: true,
    pinned_at: "2026-11-08T04:00:00Z",
  });
  const mutedPinnedUnread = makeCard({
    id: "disc-muted-pinned-unread",
    kind: "discussion",
    discussion_state: "active",
    unread: true,
    notifications_muted: true,
    pinned_at: "2026-11-08T03:00:00Z",
  });
  const mutedPinnedArchived = makeCard({
    id: "disc-muted-pinned-archived",
    kind: "discussion",
    discussion_state: "archived",
    notifications_muted: true,
    pinned_at: "2026-11-08T05:00:00Z",
  });

  assert.equal(getCardFocusReason(mutedPinnedRunning), "pinned");
  assert.equal(getCardFocusReason(mutedPinnedUnread), "pinned");
  assert.equal(getCardFocusReason(mutedPinnedArchived), null);

  // 焦点列表左侧视觉状态点与焦点降级保持一致：静音置顶不亮绿点或蓝点，消除“蓝点 + 静音 + 已置顶”冲突
  assert.equal(getFocusCardVisualStatus(mutedPinnedRunning), "idle");
  assert.equal(getFocusCardVisualStatus(mutedPinnedUnread), "idle");
  // 普通分类视图仍依赖 getCardVisualStatus 展示真实状态
  assert.equal(getCardVisualStatus(mutedPinnedRunning), "answering");
  assert.equal(getCardVisualStatus(mutedPinnedUnread), "unread");

  const summary = buildFocusSummary([
    mutedPinnedUnread,
    mutedPinnedRunning,
    mutedPinnedArchived,
  ]);
  assert.deepEqual(
    summary.cards.map((c) => c.id),
    ["disc-muted-pinned-running", "disc-muted-pinned-unread"],
  );
  assert.equal(summary.pinnedCount, 2);
  assert.equal(summary.answeringCount, 0);
  assert.equal(summary.unreadCount, 0);
  assert.equal(summary.highestVisualStatus, "idle");
});

test("焦点视觉状态点在静音硬豁免场景（待审批 / 异常 / 需留意）下保留对应状态点，不破坏未静音卡片", () => {
  const mutedPinnedApprovalRunning = makeCard({
    id: "muted-pinned-approval-running",
    kind: "todo",
    todo_state: "working",
    current_attempt_status: "running",
    pending_tool_approval_count: 1,
    notifications_muted: true,
    pinned_at: "2026-11-08T04:00:00Z",
  });
  const mutedPinnedCriticalRunning = makeCard({
    id: "muted-pinned-critical-running",
    kind: "ongoing",
    ongoing_state: "active",
    health: "critical",
    current_attempt_status: "running",
    notifications_muted: true,
    pinned_at: "2026-11-08T03:00:00Z",
  });
  const mutedPinnedWarning = makeCard({
    id: "muted-pinned-warning",
    kind: "ongoing",
    ongoing_state: "active",
    health: "warning",
    notifications_muted: true,
    pinned_at: "2026-11-08T02:00:00Z",
  });
  const unmutedPinnedUnread = makeCard({
    id: "unmuted-pinned-unread",
    kind: "discussion",
    discussion_state: "active",
    unread: true,
    pinned_at: "2026-11-08T01:00:00Z",
  });

  assert.equal(getFocusCardVisualStatus(mutedPinnedApprovalRunning), "approval");
  assert.equal(getFocusCardVisualStatus(mutedPinnedCriticalRunning), "critical");
  assert.equal(getFocusCardVisualStatus(mutedPinnedWarning), "warning");
  assert.equal(getFocusCardVisualStatus(unmutedPinnedUnread), "unread");
  // 当前正在查看未静音未读卡时依然按既有规则抑制蓝点
  assert.equal(getFocusCardVisualStatus(unmutedPinnedUnread, true), "idle");
});

test("groupFocusCards 按 需要关注 / 置顶常驻 / 进行中 切分轻量分组，单组时不产生冗余空组", () => {
  const unread = makeCard({
    id: "unread-1",
    kind: "discussion",
    discussion_state: "active",
    unread: true,
    updated_at: "2026-11-08T05:00:00Z",
  });
  const review = makeCard({
    id: "review-1",
    kind: "todo",
    todo_state: "review",
    updated_at: "2026-11-08T04:00:00Z",
  });
  const pinned = makeCard({
    id: "pinned-1",
    kind: "discussion",
    discussion_state: "active",
    pinned_at: "2026-11-08T06:00:00Z",
    notifications_muted: true,
    unread: true,
    updated_at: "2026-11-08T07:00:00Z",
  });
  const working = makeCard({
    id: "working-1",
    kind: "todo",
    todo_state: "working",
    updated_at: "2026-11-08T03:00:00Z",
  });

  const summary = buildFocusSummary([working, pinned, review, unread]);
  const sections = groupFocusCards(summary.cards);
  assert.deepEqual(
    sections.map((section) => ({
      key: section.key,
      ids: section.cards.map((c) => c.id),
    })),
    [
      { key: "attention", ids: ["unread-1", "review-1"] },
      { key: "pinned", ids: ["pinned-1"] },
      { key: "working", ids: ["working-1"] },
    ],
  );

  // 只有动态焦点时仅返回一个分组
  const attentionOnly = groupFocusCards(buildFocusSummary([unread, review]).cards);
  assert.deepEqual(
    attentionOnly.map((s) => s.key),
    ["attention"],
  );

  // 只有静默置顶时仅返回一个分组
  const pinnedOnly = groupFocusCards(buildFocusSummary([pinned]).cards);
  assert.deepEqual(
    pinnedOnly.map((s) => s.key),
    ["pinned"],
  );
});
