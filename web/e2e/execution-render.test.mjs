import test, { after } from "node:test";
import assert from "node:assert/strict";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import { renderToStaticMarkup as renderStatic } from "react-dom/server";
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
const { TooltipProvider } = await server.ssrLoadModule(
  "/src/shared/ui/tooltip.tsx",
);
const queryClient = new QueryClient();
function renderToStaticMarkup(element) {
  return renderStatic(
    createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(TooltipProvider, null, element),
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
  const current = ["queued", "running", "waiting_human"].includes(
    execution.status,
  );
  return renderToStaticMarkup(
    createElement(ConversationView, {
      events,
      live,
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

test("选择 Message 与等待摘要同文时只显示一次问题", () => {
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
    status: "waiting_human",
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
  assert.doesNotMatch(html, /aria-label="正在执行"|live-dot/);
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
  assert.doesNotMatch(html, /aria-label="正在执行"/);
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
  assert.doesNotMatch(html, /aria-label="正在执行"/);
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
    onChoose() {},
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
        onChoose() {},
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
  assert.equal((html.match(/aria-label="正在执行"/g) || []).length, 1);
});

test("尚无消息的运行会话只显示运行状态，不展示开始协作占位", () => {
  const html = render([], { ...attempt, status: "running" });
  assert.match(html, /aria-label="正在执行"/);
  assert.doesNotMatch(html, /从第一句话开始|开始协作|系统触发/);
});

test("输入框只保留一个主按钮，执行中空输入用于中止，输入后用于发送", () => {
  const base = {
    comment: "",
    setComment() {},
    blocked: false,
    waiting: false,
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
    [{ waiting: true, comment: "确认" }, "回复并继续", "submit", false],
    [{ blocked: true, comment: "已归档" }, "发送", "submit", true],
  ]) {
    const html = renderToStaticMarkup(
      createElement(ConversationComposer, { ...base, ...overrides }),
    );
    const buttons = html.match(/<button\b[^>]*>/g) || [];
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
    const header = html.match(/<p[^>]*><span>Cycle<\/span>(.*?)<\/p>/s)?.[1];
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
      onChoose() {},
      onRetryAttempt() {},
    }),
  );
  assert.match(html, /尚未完成的长文/);
  assert.ok(html.indexOf("尚未完成的长文") < html.indexOf('id="event-next"'));
  assert.match(html, /已被新要求替代/);
  assert.doesNotMatch(html, /消息未发送|border-l-2/);
});

test("没有正文的取消和替代也保留头像、名称与气泡", () => {
  for (const status of ["cancelled", "superseded"]) {
    const html = render([comment], { ...attempt, status });
    assert.match(html, /lucide-bot/);
    assert.match(html, /<span>Cycle<\/span>/);
    assert.match(html, /rounded-2xl bg-muted/);
    assert.match(html, /暂无已保存的正文/);
    assert.doesNotMatch(html, /border-l-2/);
    assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
  }
});

test("等待回复的选择气泡在名称旁展示状态，不再追加状态块", () => {
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
    status: "waiting_human",
    progress_summary: "请选择主题",
  });
  const header = html.match(/<p[^>]*><span>Cycle<\/span>(.*?)<\/p>/s)?.[1];
  assert.match(header || "", /等待你的回复/);
  assert.match(html, /lucide-bot/);
  assert.match(html, /请选择主题/);
  assert.match(html, /生活/);
  assert.doesNotMatch(html, /border-l-2/);
  assert.equal((html.match(/等待你的回复/g) || []).length, 1);
  assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
});

test("只有摘要的旧提问也在名称旁展示等待状态", () => {
  const html = render([comment], {
    ...attempt,
    status: "waiting_human",
    progress_summary: "请确认主题",
  });
  const header = html.match(/<p[^>]*><span>Cycle<\/span>(.*?)<\/p>/s)?.[1];
  assert.match(header || "", /等待你的回复/);
  assert.match(html, /lucide-bot/);
  assert.equal((html.match(/请确认主题/g) || []).length, 1);
  assert.doesNotMatch(html, /border-l-2/);
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
    assert.match(html, /lucide-bot/);
    assert.match(html, /<span>Cycle<\/span>/);
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
  assert.doesNotMatch(html, /aria-label="正在执行"|live-dot/);
  assert.equal((html.match(/data-execution-id=/g) || []).length, 1);
  const refreshed = render([comment, preview], execution);
  assert.match(refreshed, /已保存/);
  assert.doesNotMatch(refreshed, /aria-label="正在执行"|live-dot/);
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
