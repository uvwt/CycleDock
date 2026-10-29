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
  const grouped = groupExecutionAttempts([comment, result], null, [attempt]);
  assert.equal(grouped.byEvent.size, 0);
  assert.equal(grouped.standalone.length, 0);
  assert.equal(grouped.statusByReply.get(result.id), attempt);
});

test("结果尚未到达或属于其他执行时，保留完成状态", () => {
  for (const events of [
    [comment],
    [comment, { ...result, attempt_id: "other" }],
  ]) {
    const grouped = groupExecutionAttempts(events, null, [attempt]);
    assert.equal(grouped.byEvent.get(comment.id)[0].value, attempt);
    assert.equal(grouped.statusByReply.size, 0);
  }
});

test("结果已写入但执行失败或过期时，保留执行提示", () => {
  for (const status of ["failed", "expired"]) {
    const current = { ...attempt, status };
    const grouped = groupExecutionAttempts([comment, result], current, []);
    assert.equal(grouped.byEvent.get(result.id)[0].value, current);
    assert.equal(grouped.statusByReply.size, 0);
  }
});

test("重试成功只合并成功执行，保留前一次失败", () => {
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
  const grouped = groupExecutionAttempts([comment, result], null, [
    failed,
    retried,
  ]);
  assert.deepEqual(
    new Set(grouped.byEvent.get(comment.id).map((item) => item.value.id)),
    new Set([failed.id]),
  );
  assert.equal(grouped.statusByReply.get(result.id), retried);
});

test("系统执行也按执行 ID 合并，多条结果仅最后一条显示完成状态", () => {
  const scheduled = { ...attempt, source_event_id: undefined };
  const last = { ...result, id: "last-answer" };
  const grouped = groupExecutionAttempts([result, last], null, [scheduled]);
  assert.equal(grouped.standalone.length, 0);
  assert.equal(grouped.statusByReply.size, 1);
  assert.equal(grouped.statusByReply.get(last.id), scheduled);
});

test("Agent 多条回复与人工输入按持久顺序交错，当前运行状态独立放到末尾", () => {
  const first = { ...result, id: "first", type: "comment", sequence: 2 };
  const input = { ...comment, id: "followup", sequence: 3 };
  const last = { ...result, sequence: 4 };
  const events = [{ ...comment, sequence: 1 }, first, input, last];
  assert.deepEqual(
    orderTimelineEvents([...events].reverse()).map((e) => e.id),
    events.map((e) => e.id),
  );
  const current = { ...attempt, status: "running" };
  const grouped = groupExecutionAttempts(events, current, []);
  assert.equal(grouped.running.value, current);
  assert.equal(grouped.byEvent.size, 0);
  assert.equal(grouped.standalone.length, 0);
});

test("等待回复、取消和替代的状态附在最后一条正文，系统进度不能抢占正文元信息", () => {
  for (const status of ["waiting_human", "cancelled", "superseded"]) {
    const execution = { ...attempt, status };
    const progress = { ...result, id: "progress", type: "progress" };
    const grouped = groupExecutionAttempts([comment, result, progress], null, [
      execution,
    ]);
    assert.equal(grouped.statusByReply.get(result.id), execution);
    assert.equal(grouped.byEvent.size, 0);
  }
});
