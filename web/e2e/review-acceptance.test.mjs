import test from "node:test";
import assert from "node:assert/strict";
import {
  isAllReviewSelected,
  isReviewTodoCard,
  reconcileReviewSelection,
  resolveCardSwipeActionSlots,
  runBatchReviewAcceptance,
  toggleReviewSelection,
  toggleSelectAllReview,
} from "../src/shared/lib/review-acceptance.ts";

test("isReviewTodoCard：仅 review 状态的 Todo 卡片允许人工快捷验收", () => {
  assert.equal(isReviewTodoCard({ kind: "todo", todo_state: "review" }), true);
  assert.equal(isReviewTodoCard({ kind: "todo", todo_state: "todo" }), false);
  assert.equal(isReviewTodoCard({ kind: "todo", todo_state: "working" }), false);
  assert.equal(isReviewTodoCard({ kind: "todo", todo_state: "done" }), false);
  assert.equal(isReviewTodoCard({ kind: "ongoing" }), false);
  assert.equal(isReviewTodoCard({ kind: "discussion" }), false);
  assert.equal(isReviewTodoCard(null), false);
});

test("resolveCardSwipeActionSlots：review 卡片左滑首位展示验收完成且最多 3 个按钮", () => {
  assert.deepEqual(
    resolveCardSwipeActionSlots({
      card: { kind: "todo", todo_state: "review" },
      hasComplete: true,
      hasPin: true,
      hasDelete: true,
    }),
    ["complete", "read-state", "delete"],
  );

  // 无删除动作时回退展示置顶，仍保持 3 个以内
  assert.deepEqual(
    resolveCardSwipeActionSlots({
      card: { kind: "todo", todo_state: "review" },
      hasComplete: true,
      hasPin: true,
      hasDelete: false,
    }),
    ["complete", "read-state", "pin"],
  );

  // 未提供 onComplete 回调时维持原有 3 槽位
  assert.deepEqual(
    resolveCardSwipeActionSlots({
      card: { kind: "todo", todo_state: "review" },
      hasComplete: false,
      hasPin: true,
      hasDelete: true,
    }),
    ["read-state", "pin", "delete"],
  );

  // 非 review 状态 Todo 即使传入 onComplete 也不显示验收按钮
  assert.deepEqual(
    resolveCardSwipeActionSlots({
      card: { kind: "todo", todo_state: "working" },
      hasComplete: true,
      hasPin: true,
      hasDelete: true,
    }),
    ["read-state", "pin", "delete"],
  );

  // 主讨论仅保留已读/未读 + 静音
  assert.deepEqual(
    resolveCardSwipeActionSlots({
      card: { kind: "discussion" },
      isMain: true,
      hasComplete: true,
      hasPin: true,
      hasMute: true,
      hasDelete: true,
    }),
    ["read-state", "mute"],
  );
});

test("批量勾选纯逻辑：单选切换、全选/取消全选与幽灵选中裁剪", () => {
  const available = ["card-1", "card-2", "card-3"];

  let selected = new Set();
  assert.equal(isAllReviewSelected(selected, available), false);

  // 单选切换不原地污染旧 Set
  const afterAdd = toggleReviewSelection(selected, "card-1");
  assert.equal(selected.size, 0);
  assert.deepEqual(Array.from(afterAdd), ["card-1"]);

  // 部分选中时触发全选
  selected = toggleSelectAllReview(afterAdd, available);
  assert.equal(isAllReviewSelected(selected, available), true);
  assert.deepEqual(Array.from(selected), available);

  // 已全选时再次触发取消全选
  const afterDeselectAll = toggleSelectAllReview(selected, available);
  assert.equal(afterDeselectAll.size, 0);
  assert.equal(isAllReviewSelected(afterDeselectAll, available), false);

  // 实时更新或搜索过滤移除 card-2 后，自动裁剪失效勾选
  const reconciled = reconcileReviewSelection(selected, ["card-1", "card-3"]);
  assert.deepEqual(Array.from(reconciled), ["card-1", "card-3"]);
  assert.equal(isAllReviewSelected(reconciled, ["card-1", "card-3"]), true);
  assert.equal(isAllReviewSelected(new Set(), []), false);
});

test("runBatchReviewAcceptance：严格限制并发上限、自动去重并精准结算部分失败", async () => {
  let inFlight = 0;
  let maxInFlight = 0;
  const calledIDs = [];

  const ids = [
    "c-1",
    "c-2",
    "c-3",
    "c-2", // 重复 ID 应去重
    "c-4",
    "c-5",
    "c-6",
    "c-7",
    "c-8",
    "c-9",
  ];

  const result = await runBatchReviewAcceptance(
    ids,
    async (id) => {
      calledIDs.push(id);
      inFlight += 1;
      maxInFlight = Math.max(maxInFlight, inFlight);
      await new Promise((resolve) => setTimeout(resolve, 10));
      inFlight -= 1;

      if (id === "c-3" || id === "c-7") {
        throw new Error(`boom:${id}`);
      }
      return {
        id,
        project_id: "proj-1",
        title: `Card ${id}`,
        description: "",
        kind: "todo",
        todo_state: "done",
        created_at: "2026-04-17T00:00:00Z",
        updated_at: "2026-04-17T00:00:00Z",
      };
    },
    3,
  );

  assert.equal(calledIDs.length, 9);
  assert.ok(maxInFlight <= 3, `expected maxInFlight <= 3, got ${maxInFlight}`);
  assert.deepEqual(result.succeededIDs, [
    "c-1",
    "c-2",
    "c-4",
    "c-5",
    "c-6",
    "c-8",
    "c-9",
  ]);
  assert.deepEqual(result.failedIDs, ["c-3", "c-7"]);
  assert.equal(result.succeeded.length, 7);
  assert.equal(result.failed.length, 2);

  // 空数组不触发请求
  let emptyCalls = 0;
  const emptyResult = await runBatchReviewAcceptance([], async () => {
    emptyCalls += 1;
    throw new Error("should not be called");
  });
  assert.equal(emptyCalls, 0);
  assert.deepEqual(emptyResult.succeededIDs, []);
  assert.deepEqual(emptyResult.failedIDs, []);
});
