import test from "node:test";
import assert from "node:assert/strict";
import {
  buildFocusSummary,
  getCardFocusReason,
  getKindVisualStatus,
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
