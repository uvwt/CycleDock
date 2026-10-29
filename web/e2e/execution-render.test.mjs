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
