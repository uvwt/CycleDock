import test from "node:test";
import assert from "node:assert/strict";
import {
  groupExecutionAttempts,
  orderTimelineEvents,
} from "../src/features/timeline/execution.ts";

const comment = { id: "input", type: "comment", actor_kind: "human" };
const attempt = {
  id: "attempt",
  source_event_id: comment.id,
  status: "completed",
  created_at: "2026-10-29T00:00:00Z",
};
const result = {
  id: "answer",
  type: "result",
  actor_kind: "agent",
  attempt_id: attempt.id,
};

test("已完成的执行合并到对应回答，不再产生独立状态气泡", () => {
  const grouped = groupExecutionAttempts([comment, result], [], null, [
    attempt,
  ]);
  assert.equal(grouped.byEvent.size, 0);
  assert.equal(grouped.standalone.length, 0);
  assert.equal(grouped.completedByResult.get(result.id), attempt);
});

test("结果尚未到达或属于其他执行时，保留完成状态", () => {
  for (const events of [
    [comment],
    [comment, { ...result, attempt_id: "other" }],
  ]) {
    const grouped = groupExecutionAttempts(events, [], null, [attempt]);
    assert.equal(grouped.byEvent.get(comment.id)[0].value, attempt);
    assert.equal(grouped.completedByResult.size, 0);
  }
});

test("结果已写入但执行仍在运行、等待回复或失败时，保留执行提示", () => {
  for (const status of [
    "running",
    "waiting_human",
    "failed",
    "cancelled",
    "superseded",
    "expired",
  ]) {
    const current = { ...attempt, status };
    const grouped = groupExecutionAttempts([comment, result], [], current, []);
    assert.equal(grouped.byEvent.get(result.id)[0].value, current);
    assert.equal(grouped.completedByResult.size, 0);
  }
});

test("重试成功只合并成功执行，保留前一次失败和排队项", () => {
  const failed = {
    ...attempt,
    id: "failed",
    status: "failed",
    can_retry: false,
  };
  const retried = {
    ...attempt,
    reason: "retry",
    retry_of_attempt_id: failed.id,
  };
  const pending = { id: "pending", source_event_id: comment.id, sequence: 2 };
  const grouped = groupExecutionAttempts([comment, result], [pending], null, [
    failed,
    retried,
  ]);
  assert.deepEqual(
    new Set(grouped.byEvent.get(comment.id).map((item) => item.value.id)),
    new Set([failed.id, pending.id]),
  );
  assert.equal(grouped.completedByResult.get(result.id), retried);
});

test("系统执行也按执行 ID 合并，多条结果仅最后一条显示完成状态", () => {
  const scheduled = { ...attempt, source_event_id: undefined };
  const last = { ...result, id: "last-answer" };
  const grouped = groupExecutionAttempts([result, last], [], null, [scheduled]);
  assert.equal(grouped.standalone.length, 0);
  assert.equal(grouped.completedByResult.size, 1);
  assert.equal(grouped.completedByResult.get(last.id), scheduled);
});

test("Agent 多条回复与人工输入按持久顺序交错，状态挂在该执行最后一条消息后", () => {
  const first = { ...result, id: "first", type: "comment", sequence: 2 };
  const input = { ...comment, id: "followup", sequence: 3 };
  const last = { ...result, sequence: 4 };
  const events = [{ ...comment, sequence: 1 }, first, input, last];
  assert.deepEqual(
    orderTimelineEvents([...events].reverse()).map((e) => e.id),
    events.map((e) => e.id),
  );
  const current = { ...attempt, status: "running" };
  const grouped = groupExecutionAttempts(events, [], current, []);
  assert.equal(grouped.byEvent.get(last.id)[0].value, current);
  assert.equal(grouped.byEvent.has(comment.id), false);
});
