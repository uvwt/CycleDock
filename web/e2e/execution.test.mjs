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

test("排队提交的人工引导输入与 Agent 回复按轮次交错展示（一问一答）", () => {
  // 模拟用户在执行过程中连续排队发送了两个问题：
  // Q1 (seq 1) -> 对应 Attempt 1 (created 10:00:00) -> 产生回复 A1 (seq 4)
  // Q2 (seq 2, 排队) -> 对应 Attempt 2 (created 10:00:10) -> 产生回复 A2 (seq 5)
  // Q3 (seq 3, 排队) -> 对应 Attempt 3 (created 10:00:20) -> 产生回复 A3 (seq 6)
  const q1 = { id: "q1", sequence: 1, type: "comment", actor_kind: "human", created_at: "2026-10-31T10:00:00Z" };
  const q2 = { id: "q2", sequence: 2, type: "comment", actor_kind: "human", created_at: "2026-10-31T10:00:02Z" };
  const q3 = { id: "q3", sequence: 3, type: "comment", actor_kind: "human", created_at: "2026-10-31T10:00:05Z" };

  const a1 = { id: "a1", sequence: 4, type: "message", actor_kind: "cycle_agent", attempt_id: "att1", created_at: "2026-10-31T10:00:08Z" };
  const a2 = { id: "a2", sequence: 5, type: "message", actor_kind: "cycle_agent", attempt_id: "att2", created_at: "2026-10-31T10:00:15Z" };
  const a3 = { id: "a3", sequence: 6, type: "message", actor_kind: "cycle_agent", attempt_id: "att3", created_at: "2026-10-31T10:00:25Z" };

  const attempts = [
    { id: "att1", source_event_id: "q1", created_at: "2026-10-31T10:00:00Z", attempt_number: 1 },
    { id: "att2", source_event_id: "q2", created_at: "2026-10-31T10:00:10Z", attempt_number: 1 },
    { id: "att3", source_event_id: "q3", created_at: "2026-10-31T10:00:20Z", attempt_number: 1 },
  ];

  // 原始数据库物理 sequence 排序中所有提问在前面：q1, q2, q3, a1, a2, a3
  const rawEvents = [q1, q2, q3, a1, a2, a3];
  const ordered = orderTimelineEvents(rawEvents, attempts);

  // 经过业务轮次排序后必须呈现真实对话流（一问一答）：q1 -> a1 -> q2 -> a2 -> q3 -> a3
  assert.deepEqual(
    ordered.map((e) => e.id),
    ["q1", "a1", "q2", "a2", "q3", "a3"],
  );
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
