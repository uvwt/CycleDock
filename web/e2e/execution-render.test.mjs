import test, { after } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

const server = await createServer({
  server: { middlewareMode: true },
  appType: "custom",
});
after(() => server.close());
const { ConversationView } = await server.ssrLoadModule(
  "/src/features/timeline/ConversationView.tsx",
);
const attempt = {
  id: "execution",
  turn_id: "turn",
  source_event_id: "input",
  status: "completed",
  progress_summary: "正在生成卡片",
  started_at: "2026-10-29T00:00:00Z",
  finished_at: "2026-10-29T00:00:12Z",
  created_at: "2026-10-29T00:00:00Z",
};
const comment = {
  id: "input",
  type: "comment",
  actor_kind: "human",
  content: "生成卡片",
  sequence: 1,
};
const result = {
  id: "answer",
  type: "result",
  actor_kind: "agent",
  content: "已生成卡片",
  attempt_id: attempt.id,
  sequence: 2,
};
function render(events, execution = attempt) {
  const current = ["queued", "running", "waiting_human"].includes(
    execution.status,
  );
  return renderToStaticMarkup(
    createElement(ConversationView, {
      events,
      live: null,
      cardKind: "discussion",
      pendingTurns: [],
      currentAttempt: current ? execution : null,
      attemptHistory: current ? [] : [execution],
      waitingAttemptID: current ? execution.id : undefined,
      onChoose() {},
      onCancelTurn() {},
      onRetryAttempt() {},
    }),
  );
}

test("完成后只展示结果及耗时，清除旧进度", () => {
  const html = render([comment, result]);
  assert.match(html, /已生成卡片/);
  assert.match(html, /0:12/);
  assert.doesNotMatch(html, /正在生成卡片/);
  assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
});

test("完成但没有结果时保留状态，不展示旧进度", () => {
  const html = render([comment]);
  assert.match(html, /执行完成/);
  assert.doesNotMatch(html, /正在生成卡片/);
});

test("等待回复的问题和已收到回复的问题仍可见", () => {
  for (const status of ["waiting_human", "superseded"]) {
    const prompt = { ...result, type: "comment", content: "请确认卡片标题" };
    const html = render([comment, prompt], {
      ...attempt,
      status,
      progress_summary: "请确认卡片标题",
      error_summary: status === "superseded" ? "superseded by human reply" : "",
    });
    assert.equal((html.match(/请确认卡片标题/g) || []).length, 1);
    assert.doesNotMatch(html, /superseded by human reply/);
  }
});

test("失败展示错误与重试入口，不展示旧进度", () => {
  const html = render([comment], {
    ...attempt,
    status: "failed",
    error_summary: "连接失败",
    can_retry: true,
  });
  assert.match(html, /连接失败/);
  assert.match(html, /重试/);
  assert.doesNotMatch(html, /正在生成卡片/);
});

test("同次执行的多条消息独立展示，状态位于最新消息之后", () => {
  const update = { ...result, type: "comment", content: "我先检查已有卡片" };
  const progress = {
    ...result,
    id: "progress",
    sequence: 3,
    type: "progress",
    content: "检查结束",
  };
  const html = render([comment, update, progress], {
    ...attempt,
    status: "failed",
    error_summary: "连接失败",
  });
  assert.match(html, /我先检查已有卡片/);
  assert.ok(html.indexOf("检查结束") > html.indexOf("我先检查已有卡片"));
  assert.ok(html.indexOf("执行失败") > html.indexOf("检查结束"));
  assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
});

test("仅有执行摘要的旧提问在等待中和人工回复后仍显示一次", () => {
  for (const status of ["waiting_human", "superseded"]) {
    const html = render([comment], {
      ...attempt,
      status,
      progress_summary: "请选择部署目标",
      error_summary: status === "superseded" ? "superseded by human reply" : "",
    });
    assert.equal((html.match(/请选择部署目标/g) || []).length, 1);
    assert.doesNotMatch(html, /superseded by human reply/);
  }
});

test("同文但来自其他执行的消息不能隐藏当前提问", () => {
  const otherPrompt = {
    ...result,
    type: "comment",
    attempt_id: "other-execution",
    content: "请选择部署目标",
  };
  const html = render([comment, otherPrompt], {
    ...attempt,
    status: "waiting_human",
    progress_summary: "请选择部署目标",
  });
  assert.equal((html.match(/请选择部署目标/g) || []).length, 2);
});


test("最终 Message 展示完成耗时，过程消息不重复附加状态", () => {
  const progress = { ...result, id: "progress-message", type: "message", content: "检查中", message: { type: "text", content: "检查中", end_turn: false } };
  const final = { ...result, type: "message", sequence: 3, message: { type: "text", content: result.content, end_turn: true } };
  const html = render([comment, progress, final]);
  assert.match(html, /检查中/);
  assert.match(html, /已生成卡片/);
  assert.match(html, /0:12/);
  assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
});

test("选择 Message 与等待摘要同文时只显示一次问题", () => {
  const choice = { ...result, type: "message", content: "", message: { type: "choice", end_turn: true, widget: { question: "请选择部署目标", options: [{ value: "a", label: "目标 A" }, { value: "b", label: "目标 B" }] } } };
  const html = render([comment, choice], { ...attempt, status: "waiting_human", progress_summary: "请选择部署目标" });
  assert.equal((html.match(/请选择部署目标/g) || []).length, 1);
  assert.match(html, /目标 A/);
  assert.match(html, /目标 B/);
});

function chatEvent(sequence, actor_kind = "agent", actor_name = "Agent A", minutes = sequence) {
  return {
    id: `chat-${sequence}`,
    sequence,
    type: "message",
    actor_kind,
    actor_name,
    content: `消息 ${sequence}`,
    created_at: new Date(Date.UTC(2026, 9, 29, 0, minutes)).toISOString(),
  };
}

function groupStarts(html) {
  return [...html.matchAll(/data-message-group-start="(true|false)"/g)].map(
    (match) => match[1] === "true",
  );
}

test("连续消息只在组首展示发送者，切换后再次出现仍开启新组", () => {
  const html = render([
    chatEvent(1), chatEvent(2),
    chatEvent(3, "human", "小明"), chatEvent(4, "human", "小明"),
    chatEvent(5), chatEvent(6, "agent", "Agent B"),
  ]);
  assert.deepEqual(groupStarts(html), [true, false, true, false, true, true]);
  assert.equal((html.match(/>Agent A</g) || []).length, 2);
  assert.doesNotMatch(html, /小明|lucide-user|pr-12/);
  for (let i = 1; i <= 6; i++) assert.match(html, new RegExp(`消息 ${i}`));
});

test("同名但不同角色不能合并，五分钟内连续消息合并，超过五分钟重新展示发送者", () => {
  const html = render([
    chatEvent(1, "agent", "同名", 0),
    chatEvent(2, "agent", "同名", 5),
    chatEvent(3, "agent", "同名", 11),
    chatEvent(4, "human", "同名", 12),
  ]);
  assert.deepEqual(groupStarts(html), [true, false, true, true]);
});

test("系统事件和未知时间不会把不同轮次合并", () => {
  const html = render([
    chatEvent(1),
    { ...chatEvent(2), type: "status_change" },
    chatEvent(3),
    { ...chatEvent(4), created_at: "invalid" },
    chatEvent(5),
  ]);
  assert.deepEqual(groupStarts(html), [true, true, true, true]);
});

test("完成状态和耗时位于消息组的 Agent 名称右侧", () => {
  const first = chatEvent(1);
  const final = {
    ...chatEvent(2),
    attempt_id: attempt.id,
    message: { type: "text", content: "已完成", end_turn: true },
  };
  const html = render([first, final]);
  const header = html.match(/<p[^>]*><span>Agent A<\/span>(.*?)<\/p>/s)?.[1];
  assert.ok(header, "消息组名称旁应包含完成信息");
  assert.match(header, /执行完成/);
  assert.match(header, /0:12/);
  assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
  assert.deepEqual(groupStarts(html), [true, false]);
});
