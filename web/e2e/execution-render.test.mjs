import test, { after } from "node:test";
import assert from "node:assert/strict";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import { renderToStaticMarkup as renderStatic } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { createServer } from "vite";

const server = await createServer({
  server: { middlewareMode: true },
  appType: "custom",
});
after(() => server.close());
const { ConversationView } = await server.ssrLoadModule(
  "/src/features/timeline/ConversationView.tsx",
);
const { PendingTurnQueue } = await server.ssrLoadModule(
  "/src/features/cards/PendingTurnQueue.tsx",
);
const { ConversationComposer } = await server.ssrLoadModule(
  "/src/features/cards/ConversationComposer.tsx",
);
const { CardHeaderInfoCard } = await server.ssrLoadModule(
  "/src/features/cards/CardHeaderInfoCard.tsx",
);
const { WorkSidebar } = await server.ssrLoadModule(
  "/src/features/board/WorkSidebar.tsx",
);
const { stripMarkdownSummary } = await server.ssrLoadModule(
  "/src/shared/lib/markdown.ts",
);
const { TooltipProvider } = await server.ssrLoadModule(
  "/src/shared/ui/tooltip.tsx",
);
const { ProjectContext } = await server.ssrLoadModule(
  "/src/app/project-context.ts",
);
const { ModelSelection } = await server.ssrLoadModule(
  "/src/features/models/model-selection.ts",
);
const { default: SettingsPage } = await server.ssrLoadModule(
  "/src/features/settings/SettingsPage.tsx",
);
const { default: NexusDockPage } = await server.ssrLoadModule(
  "/src/features/nexusdock/NexusDockPage.tsx",
);
const queryClient = new QueryClient();
queryClient.setQueryData(["agent-status"], { model_configured: true });
function renderToStaticMarkup(element) {
  return renderStatic(
    createElement(
      MemoryRouter,
      null,
      createElement(
        QueryClientProvider,
        { client: queryClient },
        createElement(
          ProjectContext.Provider,
          {
            value: {
              projects: [],
              projectID: "project",
              isPending: false,
              refetch() {},
              setProjectID() {},
            },
          },
          createElement(
            ModelSelection.Provider,
            { value: { selectedRouteId: null, selectRouteId() {} } },
            createElement(TooltipProvider, null, element),
          ),
        ),
      ),
    ),
  );
}
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
function render(events, execution = attempt, live = null) {
  const current = ["queued", "running"].includes(execution.status);
  return renderToStaticMarkup(
    createElement(ConversationView, {
      events,
      live,
      cardKind: "discussion",
      pendingTurns: [],
      currentAttempt: current ? execution : null,
      attemptHistory: current ? [] : [execution],
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

test("已完成和被替代执行的问题正文仍可见", () => {
  for (const status of ["completed", "superseded"]) {
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

test("最终 Message 展示完成耗时，过程消息不重复附加状态", () => {
  const progress = {
    ...result,
    id: "progress-message",
    type: "message",
    content: "检查中",
    message: { type: "text", content: "检查中", end_turn: false },
  };
  const final = {
    ...result,
    type: "message",
    sequence: 3,
    message: { type: "text", content: result.content, end_turn: true },
  };
  const html = render([comment, progress, final]);
  assert.match(html, /检查中/);
  assert.match(html, /已生成卡片/);
  assert.match(html, /0:12/);
  assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
});

test("选择消息只展示一次问题正文", () => {
  const choice = {
    ...result,
    type: "message",
    content: "",
    message: {
      type: "choice",
      end_turn: true,
      widget: {
        question: "请选择部署目标",
        options: [
          { value: "a", label: "目标 A" },
          { value: "b", label: "目标 B" },
        ],
      },
    },
  };
  const html = render([comment, choice], {
    ...attempt,
    status: "completed",
    progress_summary: "请选择部署目标",
  });
  assert.equal((html.match(/请选择部署目标/g) || []).length, 1);
  assert.match(html, /目标 A/);
  assert.match(html, /目标 B/);
});

function chatEvent(
  sequence,
  actor_kind = "agent",
  actor_name = "Agent A",
  minutes = sequence,
) {
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
    chatEvent(1),
    chatEvent(2),
    chatEvent(3, "human", "小明"),
    chatEvent(4, "human", "小明"),
    chatEvent(5),
    chatEvent(6, "agent", "Agent B"),
  ]);
  assert.deepEqual(groupStarts(html), [true, false, true, false, true, true]);
  assert.equal((html.match(/>Agent A</g) || []).length, 2);
  assert.doesNotMatch(html, /小明|lucide-user|pr-12/);
  for (let i = 1; i <= 6; i++) assert.match(html, new RegExp(`消息 ${i}`));
});

test("显示名同为 Cycle 的连续气泡合并，不再重复名称", () => {
  const html = render([
    chatEvent(1, "cycle_agent", "Cycle Agent", 0),
    chatEvent(2, "cycle_agent", "Cycle", 1),
  ]);
  assert.deepEqual(groupStarts(html), [true, false]);
  assert.equal((html.match(/>Cycle</g) || []).length, 1);
  assert.doesNotMatch(html, /lucide-bot/);
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
  const system = render([
    chatEvent(1),
    { ...chatEvent(2), type: "status_change" },
    chatEvent(3),
  ]);
  assert.deepEqual(groupStarts(system), [true, true]);
  const unknownTime = render([
    chatEvent(1),
    { ...chatEvent(2), created_at: "invalid" },
  ]);
  assert.deepEqual(groupStarts(unknownTime), [true, true]);
});

test("状态变化事件转为中文并带有状态圆点，不再展示英文原词", () => {
  const html = render([
    {
      id: "status-1",
      sequence: 1,
      type: "status_change",
      content: "review -> working",
      actor_kind: "cycle_agent",
      actor_name: "Cycle Agent",
      created_at: "2026-10-29T00:00:00Z",
    },
  ]);
  assert.match(html, /状态变化/);
  assert.match(html, /待审核/);
  assert.match(html, /进行中/);
  assert.doesNotMatch(html, /review -> working/);
});

test("完成状态和耗时位于消息组的 Agent 名称右侧", () => {
  const first = { ...chatEvent(1), attempt_id: attempt.id };
  const final = {
    ...chatEvent(2),
    attempt_id: attempt.id,
    message: { type: "text", content: "已完成", end_turn: true },
  };
  const html = render([first, final]);
  const header = html.match(
    /<span[^>]*>Agent A<\/span>(.*?)<div class="flex flex-wrap items-end/s,
  )?.[1];
  assert.ok(header, "消息组名称旁应包含完成信息");
  assert.match(header, /执行完成/);
  assert.match(header, /0:12/);
  assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
  assert.deepEqual(groupStarts(html), [true, false]);
});

test("正在操作的设备位于正文气泡右侧，耗时保留在名称旁", () => {
  const html = render(
    [
      comment,
      {
        ...result,
        id: "preview-event",
        type: "message",
        content: "正在生成",
        message: {
          type: "text",
          content: "正在生成",
          preview: true,
          preview_id: "preview",
          end_turn: false,
        },
      },
    ],
    { ...attempt, status: "running" },
    {
      attemptID: attempt.id,
      stage: "正在执行",
      startedAt: attempt.started_at,
      deviceName: "RN",
      replies: [{ id: "preview", order: 0, text: "正在生成的正文" }],
    },
  );
  const bubbleAt = html.indexOf("正在生成的正文");
  const deviceAt = html.indexOf('data-operating-device="RN"');
  const durationAt = html.indexOf("tabular-nums");
  assert.ok(durationAt >= 0 && durationAt < bubbleAt && deviceAt > bubbleAt);
  assert.match(html.slice(bubbleAt), /正在操作 <strong[^>]*>RN<\/strong>/);
  assert.equal((html.match(/正在生成的正文/g) || []).length, 1);
  assert.doesNotMatch(html, /aria-label="正在思考并生成回复"/);
});

test("尚未输出正文时，正在操作位于运行气泡右侧", () => {
  const html = render(
    [comment],
    { ...attempt, status: "running", progress_summary: "" },
    {
      attemptID: attempt.id,
      stage: "正在执行",
      deviceName: "RN",
      replies: [],
    },
  );
  const dotsAt = html.indexOf('aria-label="正在思考并生成回复"');
  const deviceAt = html.indexOf('data-operating-device="RN"');
  assert.ok(dotsAt >= 0 && deviceAt > dotsAt);
  assert.match(html.slice(dotsAt), /正在操作 <strong[^>]*>RN<\/strong>/);
});

test("同一轮 Cycle 回复后面的运行气泡不再重复名称", () => {
  const progress = {
    ...result,
    id: "progress-message",
    type: "message",
    actor_kind: "cycle_agent",
    actor_name: "Cycle Agent",
    content: "正在分析磁盘",
    created_at: new Date().toISOString(),
    message: { type: "text", content: "正在分析磁盘", end_turn: false },
  };
  const html = render(
    [comment, progress],
    { ...attempt, status: "running", progress_summary: "" },
    {
      attemptID: attempt.id,
      startedAt: attempt.started_at,
      deviceName: "xxdeMacBook-Air.local",
      replies: [],
    },
  );
  assert.match(html, /正在分析磁盘/);
  assert.doesNotMatch(html, /lucide-bot/);
  assert.equal((html.match(/>Cycle</g) || []).length, 1);

  const dotsAt = html.indexOf('aria-label="正在思考并生成回复"');
  const deviceAt = html.indexOf(
    'data-operating-device="xxdeMacBook-Air.local"',
  );
  assert.ok(dotsAt >= 0 && deviceAt > dotsAt);
  assert.match(
    html.slice(dotsAt),
    /正在操作 <strong[^>]*>xxdeMacBook-Air.local<\/strong>/,
  );
});

test("新一轮运行紧挨上一轮回复时仍展示名称", () => {
  const html = render(
    [comment, { ...result, created_at: new Date().toISOString() }],
    {
      ...attempt,
      id: "next",
      status: "running",
      source_event_id: "input",
      progress_summary: "",
    },
    { attemptID: "next", startedAt: attempt.started_at, replies: [] },
  );
  assert.equal((html.match(/>Cycle</g) || []).length, 2);
  assert.doesNotMatch(html, /lucide-bot/);
});

test("正文流式输出时只展示正文，隐藏呼吸圆点和重复占位", () => {
  const html = render(
    [comment],
    { ...attempt, status: "running" },
    {
      attemptID: attempt.id,
      stage: "正在执行",
      replies: [{ id: "preview", order: 0, text: "正在生成的正文" }],
    },
  );
  assert.equal((html.match(/正在生成的正文/g) || []).length, 1);
  assert.doesNotMatch(html, /aria-label="正在思考并生成回复"/);
  assert.match(html, /Cycle/);
});

test("持久消息接替同一预览，不重复展示正文或残留呼吸圆点", () => {
  const html = render([comment, result], attempt, {
    attemptID: attempt.id,
    stage: "正在执行",
    finished: true,
    replies: [
      { id: "preview", order: 0, text: "已生成卡片", eventID: result.id },
    ],
  });
  assert.equal((html.match(/已生成卡片/g) || []).length, 1);
  assert.doesNotMatch(html, /aria-label="正在思考并生成回复"/);
  assert.match(html, /执行完成/);
  assert.doesNotMatch(html, /message-enter/);
});

test("失败的临时回复明确标记未发送", () => {
  const html = render(
    [comment],
    { ...attempt, status: "failed", error_summary: "连接中断" },
    {
      attemptID: attempt.id,
      stage: "正在执行",
      finished: true,
      replies: [
        { id: "preview", order: 0, text: "未完成的正文", failed: true },
      ],
    },
  );
  assert.match(html, /未完成的正文/);
  assert.match(html, /消息未发送/);
  assert.doesNotMatch(html, /aria-label="正在思考并生成回复"/);
});

test("未领取的输入只在紧凑队列中显示，领取后恢复为用户消息", () => {
  const input = {
    ...comment,
    id: "queued-input",
    content: "稍后检查磁盘空间",
    sequence: 3,
  };
  const pending = {
    id: "queued-turn",
    source_event_id: input.id,
    source: "human",
    content: input.content,
    sequence: 3,
    dispatch_mode: "enqueue",
  };
  const props = {
    events: [comment, input],
    live: null,
    cardKind: "todo",
    currentAttempt: { ...attempt, status: "running" },
    attemptHistory: [],
    onRetryAttempt() {},
  };
  const queuedTimeline = renderToStaticMarkup(
    createElement(ConversationView, { ...props, pendingTurns: [pending] }),
  );
  assert.doesNotMatch(queuedTimeline, /queued-input|稍后检查磁盘空间/);
  const queue = renderToStaticMarkup(
    createElement(PendingTurnQueue, {
      turns: [pending],
      cancelingTurnID: "",
      steeringTurnID: "",
      onCancel() {},
      onSteer() {},
    }),
  );
  assert.match(queue, /data-queued-turn="queued-turn"/);
  assert.match(queue, /引导/);
  assert.match(queue, /取消排队/);
  assert.match(queue, /aria-label="编辑内容"/);
  assert.doesNotMatch(queue, /排队消息操作|查看完整内容/);
  assert.doesNotMatch(queue, /<article/);
  const claimedTimeline = renderToStaticMarkup(
    createElement(ConversationView, {
      ...props,
      pendingTurns: [],
      currentAttempt: {
        ...attempt,
        status: "running",
        source_event_id: input.id,
      },
    }),
  );
  assert.match(claimedTimeline, /id="event-queued-input"/);
  assert.match(claimedTimeline, /稍后检查磁盘空间/);
});

test("引导后明确显示优先等待，保留编辑和取消入口", () => {
  const html = renderToStaticMarkup(
    createElement(PendingTurnQueue, {
      turns: [
        {
          id: "steered-turn",
          source: "human",
          content: "补充要求",
          sequence: 1,
          dispatch_mode: "interrupt",
        },
      ],
      cancelingTurnID: "",
      steeringTurnID: "",
      onCancel() {},
      onSteer() {},
      onEdit() {},
    }),
  );
  assert.match(html, /role="status"[^>]*[\s\S]*?优先等待执行/);
  assert.doesNotMatch(html, /aria-label="引导"/);
  assert.match(html, /<button[^>]*aria-label="取消排队"[^>]*>/);
  assert.match(html, /<button[^>]*aria-label="编辑内容"[^>]*>/);
  assert.doesNotMatch(html, /disabled=""/);
});

test("下一轮的运行气泡位于迟到的上一轮回复下方、排队列表上方", () => {
  const nextInput = {
    ...comment,
    id: "next-input",
    sequence: 2,
    content: "继续处理",
  };
  const previousReply = {
    ...result,
    sequence: 3,
    content: "上一轮已经发送的回复",
  };
  const queuedInput = {
    ...comment,
    id: "queued-input",
    sequence: 4,
    content: "稍后处理",
  };
  const pending = {
    id: "queued-turn",
    source_event_id: queuedInput.id,
    source: "human",
    content: queuedInput.content,
    sequence: 4,
    dispatch_mode: "enqueue",
  };
  const html = renderToStaticMarkup(
    createElement(
      "div",
      null,
      createElement(ConversationView, {
        events: [comment, nextInput, previousReply, queuedInput],
        live: null,
        cardKind: "discussion",
        pendingTurns: [pending],
        currentAttempt: {
          ...attempt,
          id: "next-execution",
          source_event_id: nextInput.id,
          status: "running",
          progress_summary: "",
          finished_at: undefined,
        },
        attemptHistory: [attempt],
        onRetryAttempt() {},
      }),
      createElement(PendingTurnQueue, {
        turns: [pending],
        cancelingTurnID: "",
        steeringTurnID: "",
        onCancel() {},
        onSteer() {},
      }),
    ),
  );
  const inputAt = html.indexOf('id="event-next-input"');
  const replyAt = html.indexOf("上一轮已经发送的回复");
  const runningAt = html.indexOf('data-execution-id="next-execution"');
  const queueAt = html.indexOf('data-queued-turn="queued-turn"');
  assert.ok(inputAt >= 0 && inputAt < replyAt);
  assert.ok(replyAt < runningAt, "当前运行气泡必须在已经发送的回复下方");
  assert.ok(runningAt < queueAt, "当前运行气泡必须在排队输入上方");
  assert.equal(
    (html.match(/aria-label="正在思考并生成回复"/g) || []).length,
    1,
  );
});

test("尚无消息的运行会话只显示运行状态，不展示开始协作占位", () => {
  const html = render([], { ...attempt, status: "running" });
  assert.match(html, /aria-label="正在思考并生成回复"/);
  assert.doesNotMatch(html, /从第一句话开始|开始协作|系统触发/);
});

test("输入框只保留一个主按钮，执行中空输入用于中止，输入后用于发送", () => {
  const base = {
    comment: "",
    setComment() {},
    blocked: false,
    hasCurrent: true,
    sendPending: false,
    streamConnected: true,
    cancelPending: false,
    onSubmit() {},
    onCancel() {},
  };
  for (const [overrides, label, type, disabled] of [
    [{}, "中止", "button", false],
    [{ comment: "   " }, "中止", "button", false],
    [{ cancelPending: true }, "中止", "button", true],
    [{ comment: "继续检查" }, "发送", "submit", false],
    [{ hasCurrent: false }, "发送", "submit", true],
    [{ blocked: true, comment: "已归档" }, "发送", "submit", true],
  ]) {
    const html = renderToStaticMarkup(
      createElement(ConversationComposer, { ...base, ...overrides }),
    );
    const buttons = (html.match(/<button\b[^>]*>/g) || []).filter((button) =>
      /aria-label="(?:中止|发送)"/.test(button),
    );
    assert.equal(buttons.length, 1);
    assert.match(buttons[0], new RegExp(`aria-label="${label}"`));
    assert.match(buttons[0], new RegExp(`type="${type}"`));
    assert.equal(buttons[0].includes("disabled="), disabled);
    assert.doesNotMatch(html, /<select|取消执行/);
  }
});

test("取消和替代仍显示正文，状态与耗时只出现在气泡上方", () => {
  for (const [status, label] of [
    ["cancelled", "已取消执行"],
    ["superseded", "已被新要求替代"],
  ]) {
    const html = render([comment, result], {
      ...attempt,
      status,
      can_retry: status === "cancelled",
    });
    assert.match(html, /已生成卡片/);
    const header = html.match(
      /<span[^>]*>Cycle<\/span>(.*?)<div class="flex flex-wrap items-end/s,
    )?.[1];
    assert.ok(header);
    assert.ok(header.includes(label));
    assert.match(header, /0:12/);
    assert.doesNotMatch(html, /border-l-2/);
    assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
    if (status === "cancelled") assert.match(header, /重试/);
  }
});

test("上一轮被替代的流式正文保留在新输入前，取消状态不替换气泡", () => {
  const nextInput = { ...comment, id: "next", sequence: 2, content: "新要求" };
  const next = {
    ...attempt,
    id: "next-attempt",
    source_event_id: nextInput.id,
    status: "running",
  };
  const html = renderToStaticMarkup(
    createElement(ConversationView, {
      events: [comment, nextInput],
      pendingTurns: [],
      cardKind: "discussion",
      currentAttempt: next,
      attemptHistory: [{ ...attempt, status: "superseded" }],
      live: {
        attemptID: next.id,
        replies: [],
        previous: [
          {
            attemptID: attempt.id,
            startedAt: attempt.started_at,
            replies: [
              {
                id: "old-preview",
                order: 0,
                text: "尚未完成的长文",
                failed: true,
              },
            ],
          },
        ],
      },
      onRetryAttempt() {},
    }),
  );
  assert.match(html, /尚未完成的长文/);
  assert.ok(html.indexOf("尚未完成的长文") < html.indexOf('id="event-next"'));
  assert.match(html, /已被新要求替代/);
  assert.doesNotMatch(html, /消息未发送|border-l-2/);
});

test("没有正文的取消和替代也保留名称与气泡", () => {
  for (const status of ["cancelled", "superseded"]) {
    const html = render([comment], { ...attempt, status });
    assert.doesNotMatch(html, /lucide-bot/);
    assert.match(html, />Cycle</);
    assert.match(html, /rounded-2xl bg-muted/);
    assert.match(html, /暂无已保存的正文/);
    assert.doesNotMatch(html, /border-l-2/);
    assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
  }
});

test("选择气泡正常完成并展示耗时，不占用等待状态", () => {
  const choice = {
    ...result,
    type: "message",
    content: "",
    message: {
      type: "choice",
      end_turn: true,
      widget: {
        question: "请选择主题",
        options: [{ value: "a", label: "生活" }],
      },
    },
  };
  const html = render([comment, choice], {
    ...attempt,
    status: "completed",
    progress_summary: "请选择主题",
  });
  assert.match(html, /执行完成/);
  assert.doesNotMatch(html, /等待你的回复|系统触发/);
  assert.match(html, /请选择主题/);
  assert.match(html, /生活/);
  assert.doesNotMatch(html, /border-l-2/);
  assert.equal((html.match(/执行完成/g) || []).length, 1);
  assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
});

test("刷新后从持久预览恢复取消和替代的正文，无需页面内存", () => {
  for (const status of ["cancelled", "superseded"]) {
    const preview = {
      ...result,
      type: "message",
      content: "刷新后仍然可见的半篇正文",
      message: {
        type: "text",
        content: "刷新后仍然可见的半篇正文",
        end_turn: false,
        preview_id: "generation:call",
        preview: true,
      },
    };
    const html = render([comment, preview], { ...attempt, status });
    assert.equal((html.match(/刷新后仍然可见的半篇正文/g) || []).length, 1);
    assert.doesNotMatch(html, /lucide-bot/);
    assert.match(html, />Cycle</);
    assert.doesNotMatch(html, /暂无已保存的正文|border-l-2/);
  }
});

test("持久预览与 SSE 使用同一气泡，实时正文接替快照且不重复运行占位", () => {
  const preview = {
    ...result,
    type: "message",
    content: "已保存",
    message: {
      type: "text",
      content: "已保存",
      end_turn: false,
      preview_id: "generation:call",
      preview: true,
    },
  };
  const execution = { ...attempt, status: "running", finished_at: undefined };
  const live = {
    attemptID: attempt.id,
    startedAt: attempt.started_at,
    replies: [
      { id: "generation:call", order: 0, text: "已保存以及最新的实时片段" },
    ],
  };
  const html = render([comment, preview], execution, live);
  assert.equal((html.match(/已保存/g) || []).length, 1);
  assert.match(html, /已保存以及最新的实时片段/);
  assert.doesNotMatch(html, /aria-label="正在思考并生成回复"/);
  assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
  const refreshed = render([comment, preview], execution);
  assert.match(refreshed, /已保存/);
  assert.doesNotMatch(refreshed, /aria-label="正在思考并生成回复"/);
});

test("数据库快照比 SSE 更新时正文不倒退", () => {
  const preview = {
    ...result,
    type: "message",
    content: "数据库已经保存了完整前缀",
    message: {
      type: "text",
      content: "数据库已经保存了完整前缀",
      end_turn: false,
      preview_id: "generation:call",
      preview: true,
    },
  };
  const html = render(
    [comment, preview],
    { ...attempt, status: "running" },
    {
      attemptID: attempt.id,
      replies: [{ id: "generation:call", order: 0, text: "数据库已经保存" }],
    },
  );
  assert.match(html, /数据库已经保存了完整前缀/);
  assert.equal((html.match(/数据库已经保存/g) || []).length, 1);
});

test("提交验收气泡采用与普通气泡一致的自适应宽度，仅在待验收的最新确认气泡上展示通过验收按钮", () => {
  const oldConfirm = {
    id: "confirm-old",
    type: "confirm",
    actor_kind: "cycle_agent",
    actor_name: "Cycle Agent",
    content: "第一轮验证说明：`npm run build` 通过",
    sequence: 2,
  };
  const latestConfirm = {
    id: "confirm-latest",
    type: "confirm",
    actor_kind: "cycle_agent",
    actor_name: "Cycle Agent",
    content: "第二轮验证说明：**全部测试通过**",
    sequence: 3,
  };
  const reviewHtml = renderToStaticMarkup(
    createElement(ConversationView, {
      events: [comment, oldConfirm, latestConfirm],
      live: null,
      cardKind: "todo",
      pendingTurns: [],
      currentAttempt: null,
      attemptHistory: [attempt],
      canApproveReview: true,
      onApproveReview() {},
      onRetryAttempt() {},
    }),
  );
  assert.match(reviewHtml, /<code>npm run build<\/code>/);
  assert.match(reviewHtml, /<strong>全部测试通过<\/strong>/);
  assert.doesNotMatch(reviewHtml, /calc\(85% -/);
  assert.equal((reviewHtml.match(/通过验收/g) || []).length, 1);

  const workingHtml = renderToStaticMarkup(
    createElement(ConversationView, {
      events: [comment, oldConfirm, latestConfirm],
      live: null,
      cardKind: "todo",
      pendingTurns: [],
      currentAttempt: null,
      attemptHistory: [attempt],
      canApproveReview: false,
      onApproveReview() {},
      onRetryAttempt() {},
    }),
  );
  assert.doesNotMatch(workingHtml, /通过验收/);
});

test("巡检事件采用与待验收一致的卡片气泡渲染，展示状态徽标与自适应富文本内容", () => {
  const checkinWarning = {
    id: "checkin-1",
    type: "checkin",
    checkin_status: "warning",
    actor_kind: "cycle_agent",
    actor_name: "Cycle Agent",
    content: "下午已经过了，没有喝水记录。**按要求不补记成已喝**。",
    sequence: 2,
    created_at: "2026-11-02T15:00:00Z",
  };
  const html = renderToStaticMarkup(
    createElement(ConversationView, {
      events: [comment, checkinWarning],
      live: null,
      cardKind: "ongoing",
      pendingTurns: [],
      currentAttempt: null,
      attemptHistory: [attempt],
      onRetryAttempt() {},
    }),
  );
  assert.match(html, /巡检/);
  assert.match(html, /需留意/);
  assert.match(html, /<strong>按要求不补记成已喝<\/strong>/);
  assert.doesNotMatch(html, /巡检 ·/);
});

test("右侧卡片详情面板独立渲染标题、描述、来源讨论与实时进度", () => {
  const html = renderToStaticMarkup(
    createElement(CardHeaderInfoCard, {
      card: {
        id: "card-1",
        project_id: "project-1",
        title: "优化提交审核展示UI",
        description: "提交审核的气泡采用与其他气泡一致的宽度",
        kind: "todo",
        todo_state: "review",
        ongoing_state: "",
        source_discussion_id: "disc-1",
      },
      projectCards: [
        {
          id: "disc-1",
          project_id: "project-1",
          title: "UI体验讨论",
          description: "",
          kind: "discussion",
        },
      ],
      task: {
        title: "优化提交审核展示UI",
        status: "in_progress",
        step_count: 2,
        completed_step_count: 1,
        steps: [
          { id: "s1", title: "实现气泡宽度自适应", status: "completed" },
          { id: "s2", title: "验证审核按钮闭环", status: "in_progress" },
        ],
      },
    }),
  );
  assert.match(html, /<section[^>]*aria-label="任务详情"/);
  assert.match(html, /优化提交审核展示UI/);
  assert.match(html, /提交审核的气泡采用与其他气泡一致的宽度/);
  assert.match(html, /UI体验讨论/);
  assert.match(html, /\(50%\)/);
  assert.match(html, /验证审核按钮闭环/);
});

test("尚无消息的待办卡片空白状态渲染完整标题与 Markdown 描述，侧边栏摘要自动清理原始符号", () => {
  const card = {
    id: "todo-empty-1",
    project_id: "project-1",
    title: "重构前端空白状态（Empty State）布局与完整长标题展示，不再硬性截断",
    description:
      "问题背景：\n需要支持 `ReactMarkdown` 渲染与 **层级强化**，清理列表预览里的 `inline_code` 反引号。",
    kind: "todo",
    todo_state: "todo",
    ongoing_state: "",
    updated_at: "2026-11-01T00:00:00Z",
  };
  const emptyHtml = renderToStaticMarkup(
    createElement(ConversationView, {
      card,
      events: [],
      live: null,
      cardKind: "todo",
      pendingTurns: [],
      currentAttempt: null,
      attemptHistory: [],
      onRetryAttempt() {},
    }),
  );
  assert.match(
    emptyHtml,
    /重构前端空白状态（Empty State）布局与完整长标题展示，不再硬性截断/,
  );
  assert.doesNotMatch(emptyHtml, /line-clamp-2[^>]*>重构前端空白状态/);
  assert.match(emptyHtml, /<code>ReactMarkdown<\/code>/);
  assert.match(emptyHtml, /<strong>层级强化<\/strong>/);

  const summary = stripMarkdownSummary(card.description);
  assert.ok(!summary.includes("`"));
  assert.ok(!summary.includes("**"));
  assert.match(summary, /ReactMarkdown/);
  assert.match(summary, /层级强化/);

  const sidebarHtml = renderToStaticMarkup(
    createElement(WorkSidebar, {
      cards: [card],
      activeCardID: card.id,
      onSelect() {},
    }),
  );
  assert.doesNotMatch(
    sidebarHtml,
    /`ReactMarkdown`|`inline_code`|\*\*层级强化\*\*/,
  );
  assert.match(sidebarHtml, /ReactMarkdown/);
});

test("缺少模型时保留草稿并提示设置，恢复配置后允许发送", () => {
  try {
    for (const role of ["owner", "member"]) {
      queryClient.setQueryData(["agent-status"], { model_configured: false });
      queryClient.setQueryData(["me"], { role });
      const html = renderToStaticMarkup(createElement(ConversationComposer, {
        comment: "保留这份草稿", setComment() {}, blocked: false,
        hasCurrent: false, sendPending: false, streamConnected: true,
        cancelPending: false, onSubmit() {}, onCancel() {},
      }));
      assert.match(html, /尚未配置可用模型/);
      assert.match(html, /保留这份草稿/);
      const sendButton = (html.match(/<button\b[^>]*>/g) || []).find((button) => button.includes('aria-label="发送"'));
      assert.match(sendButton, /disabled=""/);
      if (role === "owner") assert.match(html, /href="\/settings\/models"/);
      else {
        assert.match(html, /请联系工作区所有者配置并启用模型/);
        assert.doesNotMatch(html, /href="\/settings\/models"/);
      }
    }
    queryClient.setQueryData(["agent-status"], { model_configured: true });
    const restored = renderToStaticMarkup(createElement(ConversationComposer, {
      comment: "保留这份草稿", setComment() {}, blocked: false,
      hasCurrent: false, sendPending: false, streamConnected: true,
      cancelPending: false, onSubmit() {}, onCancel() {},
    }));
    assert.doesNotMatch(restored, /尚未配置可用模型/);
    const sendButton = (restored.match(/<button\b[^>]*>/g) || []).find((button) => button.includes('aria-label="发送"'));
    assert.doesNotMatch(sendButton, /disabled=/);
  } finally {
    queryClient.setQueryData(["agent-status"], { model_configured: true });
    queryClient.removeQueries({ queryKey: ["me"] });
  }
});

test("设置页移除设备守卫入口，远程设备页按当前选中设备展示守卫状态与权限控制", () => {
  try {
    queryClient.setQueryData(["me"], {
      email: "owner@example.com",
      role: "owner",
      workspace: { id: "ws-1", name: "Demo" },
    });
    queryClient.setQueryData(["authorized-ai-clients"], []);
    const settingsHtml = renderStatic(
      createElement(
        MemoryRouter,
        { initialEntries: ["/settings/clients"] },
        createElement(
          QueryClientProvider,
          { client: queryClient },
          createElement(SettingsPage, { role: "owner" }),
        ),
      ),
    );
    assert.match(settingsHtml, /MCP 服务/);
    assert.match(settingsHtml, /AI 客户端/);
    assert.doesNotMatch(settingsHtml, /设备守卫/);
    assert.doesNotMatch(settingsHtml, /\/settings\/device-guards/);

    queryClient.setQueryData(["nexus"], { available: true, connected: true });
    queryClient.setQueryData(["nexus", "fleet", "ws-1"], {
      available: true,
      connected: true,
      devices: [
        {
          id: "node-rn",
          name: "RN Server",
          online: true,
          os: "linux",
          arch: "amd64",
          skills: [],
          dynamic_mcp: [],
        },
      ],
    });
    queryClient.setQueryData(["device-tool-guards", "ws-1"], [
      {
        id: "guard-rn",
        device_node_id: "node-rn",
        device_name: "RN Server",
        mode: "block_high_risk",
        timeout_seconds: 10,
        custom_rules: "严禁直接执行 docker rm",
      },
    ]);
    queryClient.setQueryData(["agent-settings"], { configured: true, routes: [] });

    const ownerNexusHtml = renderToStaticMarkup(createElement(NexusDockPage));
    assert.match(ownerNexusHtml, /data-device-guard-row="node-rn"/);
    assert.match(ownerNexusHtml, /设备安全守卫/);
    assert.match(ownerNexusHtml, /仅拦截高危/);
    assert.match(ownerNexusHtml, /严禁直接执行 docker rm/);
    assert.match(ownerNexusHtml, /独立配置/);

    queryClient.setQueryData(["me"], {
      email: "member@example.com",
      role: "member",
      workspace: { id: "ws-1", name: "Demo" },
    });
    const memberNexusHtml = renderToStaticMarkup(createElement(NexusDockPage));
    assert.match(memberNexusHtml, /设备安全守卫/);
    assert.match(memberNexusHtml, /仅拦截高危/);
    assert.match(memberNexusHtml, /仅所有者可修改/);
    assert.doesNotMatch(memberNexusHtml, /独立配置/);
  } finally {
    queryClient.removeQueries({ queryKey: ["me"] });
    queryClient.removeQueries({ queryKey: ["nexus"] });
    queryClient.removeQueries({ queryKey: ["device-tool-guards"] });
    queryClient.removeQueries({ queryKey: ["agent-settings"] });
  }
});
