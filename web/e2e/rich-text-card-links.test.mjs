import test, { after } from "node:test";
import assert from "node:assert/strict";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { createServer } from "vite";

const server = await createServer({
  server: { middlewareMode: true },
  appType: "custom",
});
after(() => server.close());

const { RichText } = await server.ssrLoadModule(
  "/src/features/timeline/RichText.tsx",
);
const { normalizeCardMentions } = await server.ssrLoadModule(
  "/src/features/timeline/cardLinkFormatting.ts",
);
const { stripMarkdownSummary } = await server.ssrLoadModule(
  "/src/shared/lib/markdown.ts",
);
const { ProjectContext } = await server.ssrLoadModule(
  "/src/app/project-context.ts",
);

const queryClient = new QueryClient();
queryClient.setQueryData(["card", "card-1"], {
  card: {
    id: "card-1",
    project_id: "proj-1",
    kind: "todo",
    title: "修复登录超时",
    description: "排查 OAuth 回调超时并补充重试逻辑",
    todo_state: "working",
    created_at: "2026-04-17T00:00:00Z",
    updated_at: "2026-04-17T00:00:00Z",
  },
});
queryClient.setQueryData(["card", "card-2"], {
  card: {
    id: "card-2",
    project_id: "proj-1",
    kind: "ongoing",
    title: "每日构建巡检",
    description: "每天上午 9 点检查主干 CI 状态",
    ongoing_state: "active",
    created_at: "2026-04-17T00:00:00Z",
    updated_at: "2026-04-17T00:00:00Z",
  },
});

function renderRichText(content) {
  return renderToStaticMarkup(
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
              projectID: "proj-1",
              isPending: false,
              refetch() {},
              setProjectID() {},
            },
          },
          createElement(RichText, { content }),
        ),
      ),
    ),
  );
}

test("normalizeCardMentions：清理卡片前后紧跟的冒号、逗号、顿号、句号及括号说明", () => {
  assert.equal(
    normalizeCardMentions("[卡片A](cycledock://card/card-1)：留档在待办池。"),
    "[卡片A](cycledock://card/card-1)\n\n留档在待办池。",
  );

  assert.equal(
    normalizeCardMentions("已派发：[卡片A](cycledock://card/card-1)。"),
    "已派发：\n\n[卡片A](cycledock://card/card-1)",
  );

  assert.equal(
    normalizeCardMentions("[卡片A](cycledock://card/card-1)（刚开工）"),
    "[卡片A](cycledock://card/card-1)\n\n刚开工",
  );

  assert.equal(
    normalizeCardMentions(
      "等 [卡片A](cycledock://card/card-1)、[卡片B](cycledock://card/card-2)，以及后续确认。",
    ),
    "等\n\n[卡片A](cycledock://card/card-1)\n\n[卡片B](cycledock://card/card-2)\n\n以及后续确认。",
  );

  assert.equal(
    normalizeCardMentions(
      "请查看：[卡片A](cycledock://card/card-1); Next step.",
    ),
    "请查看：\n\n[卡片A](cycledock://card/card-1)\n\nNext step.",
  );

  assert.equal(
    normalizeCardMentions("[卡片A](cycledock://card/card-1)."),
    "[卡片A](cycledock://card/card-1)",
  );

  assert.equal(
    normalizeCardMentions(
      "任务（[卡片A](cycledock://card/card-1)）。",
    ),
    "任务\n\n[卡片A](cycledock://card/card-1)",
  );
});

test("normalizeCardMentions：列表项、裸 URL、多行孤立标点与嵌套方括号标题处理正确", () => {
  assert.equal(
    normalizeCardMentions(
      "- [卡片A](cycledock://card/card-1)：留档在待办池。\n- [卡片B](cycledock://card/card-2)（已启动）",
    ),
    "- [卡片A](cycledock://card/card-1)\n  留档在待办池。\n- [卡片B](cycledock://card/card-2)\n  已启动",
  );

  assert.equal(
    normalizeCardMentions(
      "1. 已创建：[卡片A](cycledock://card/card-1)；等待确认。",
    ),
    "1. 已创建：\n   [卡片A](cycledock://card/card-1)\n   等待确认。",
  );

  assert.equal(
    normalizeCardMentions("请点击 cycledock://card/card-1。"),
    "请点击\n\n[查看卡片详情](cycledock://card/card-1)",
  );

  const uuid = "123e4567-e89b-12d3-a456-426614174000";
  assert.equal(
    normalizeCardMentions(`详见 card:${uuid}：已同步。`),
    `详见\n\n[查看卡片详情](card:${uuid})\n\n已同步。`,
  );

  assert.equal(
    normalizeCardMentions(
      "已派发：\n\n[卡片A](cycledock://card/card-1)\n。\n\n后续补充：",
    ),
    "已派发：\n\n[卡片A](cycledock://card/card-1)\n\n后续补充：",
  );

  assert.equal(
    normalizeCardMentions(
      "[[Bug] 修复登录](cycledock://card/card-1)：请排查。",
    ),
    "[[Bug] 修复登录](cycledock://card/card-1)\n\n请排查。",
  );
});

test("normalizeCardMentions：不误伤围栏代码块、行内代码与普通链接", () => {
  const withCode = [
    "示例写法：",
    "",
    "```markdown",
    "[示例卡](cycledock://card/card-1)：代码块内应保持原样。",
    "cycledock://card/card-2。",
    "```",
    "",
    "行内代码 `[示例](cycledock://card/card-1)：不变` 以及 [官方文档](https://example.com)：保持行内。",
    "",
    "[卡片A](cycledock://card/card-1)：正文这里应清洗冒号。",
  ].join("\n");

  const expected = [
    "示例写法：",
    "",
    "```markdown",
    "[示例卡](cycledock://card/card-1)：代码块内应保持原样。",
    "cycledock://card/card-2。",
    "```",
    "",
    "行内代码 `[示例](cycledock://card/card-1)：不变` 以及 [官方文档](https://example.com)：保持行内。",
    "",
    "[卡片A](cycledock://card/card-1)",
    "",
    "正文这里应清洗冒号。",
  ].join("\n");

  assert.equal(normalizeCardMentions(withCode), expected);
});

test("RichText SSR 渲染：卡片独立渲染为块级组件，消除 <p> 嵌套与孤立标点段落", () => {
  const html = renderRichText(
    [
      "已为你拆分并派发以下任务。",
      "",
      "[修复登录超时](cycledock://card/card-1)：排查 OAuth 回调超时。",
      "",
      "- [每日构建巡检](cycledock://card/card-2)（每天上午 9 点执行）。",
      "",
      "```md",
      "[代码内示例](cycledock://card/card-1)：保持原样",
      "```",
    ].join("\n"),
  );

  // 两张卡片均被渲染为独立的 ChatActionCard 块级容器，而非普通 <a> 链接
  assert.match(html, /修复登录超时/);
  assert.match(html, /每日构建巡检/);
  assert.doesNotMatch(html, /href="cycledock:\/\/card\//);

  // 不应出现孤立的 `<p>：</p>` / `<p>。</p>` 或以冒号开头的段落
  assert.doesNotMatch(html, /<p>[：:、，,；;。.]+<\/p>/);
  assert.doesNotMatch(html, /<p>[：:、，,；;]/);
  assert.match(html, /<p>排查 OAuth 回调超时。<\/p>/);
  assert.match(html, /每天上午 9 点执行/);

  // 不应出现 `<p><div` 非法块级嵌套
  assert.doesNotMatch(html, /<p>\s*<div/);

  // 代码块中的示例文本完好保留（rehype-highlight 会为 markdown token 包裹 span，此处校验保留原文关键片段）
  assert.match(html, /class="hljs language-md"/);
  assert.match(html, /代码内示例/);
  assert.match(html, /cycledock:\/\/card\/card-1/);
  assert.match(html, /保持原样/);
  assert.doesNotMatch(html, /代码内示例[\s\S]*role="button"/);
});

test("stripMarkdownSummary：清洗标题井号、加粗下划线、行内代码、无序与有序列表前缀及多余空白", () => {
  const raw = [
    "## 目标",
    "优化 `ChatActionCard` 在 **手机端** 与 __窄屏__ 下的展示空间。",
    "",
    "- 支持 [卡片链接](https://example.com) 摘要清洗",
    "- [x] 去掉任务列表标记",
    "1. 第一步校验布局",
    "2) 第二步校验点击防冒泡",
    "> 引用说明行",
  ].join("\n");

  const cleaned = stripMarkdownSummary(raw);
  assert.equal(
    cleaned,
    "目标 优化 ChatActionCard 在 手机端 与 窄屏 下的展示空间。 支持 卡片链接 摘要清洗 去掉任务列表标记 第一步校验布局 第二步校验点击防冒泡 引用说明行",
  );
  assert.doesNotMatch(cleaned, /##|\*\*|__|`|^\s*\d+[.)]/);

  // 仅包含 Markdown 装饰符的描述应清洗为空字符串，便于卡片回退到类型+短 ID
  assert.equal(stripMarkdownSummary("  ##   \n---\n   "), "");
});

test("ChatActionCard SSR 渲染：手机端上下两层布局、Markdown 摘要清洗与唯一快捷完成按钮", () => {
  queryClient.setQueryData(["card", "card-review"], {
    card: {
      id: "card-review",
      project_id: "proj-1",
      kind: "todo",
      title: "优化手机端聊天气泡引用卡片上下两层布局解决长标题挤压问题",
      description:
        "## 目标\n- 1. 优化 **ChatActionCard** 与 `stripMarkdownSummary`\n2) 验证点击防冒泡",
      todo_state: "review",
      created_at: "2026-04-17T00:00:00Z",
      updated_at: "2026-04-17T00:00:00Z",
    },
  });
  queryClient.setQueryData(["card", "card-done"], {
    card: {
      id: "card-done",
      project_id: "proj-1",
      kind: "todo",
      title: "已完成卡片示例",
      description: "已经完成不应再显示完成按钮",
      todo_state: "done",
      created_at: "2026-04-17T00:00:00Z",
      updated_at: "2026-04-17T00:00:00Z",
    },
  });
  queryClient.setQueryData(["card", "card-no-desc"], {
    card: {
      id: "card-no-desc",
      project_id: "proj-1",
      kind: "todo",
      title: "无描述待办卡片",
      description: "",
      todo_state: "working",
      created_at: "2026-04-17T00:00:00Z",
      updated_at: "2026-04-17T00:00:00Z",
    },
  });

  const reviewHtml = renderRichText(
    "[待验收卡片示例](cycledock://card/card-review)",
  );
  // 仅渲染唯一一个完成按钮，避免移动端/桌面端重复挂载破坏选择器与无障碍
  assert.equal(
    (reviewHtml.match(/data-chat-card-complete="true"/g) || []).length,
    1,
  );
  assert.match(reviewHtml, /data-chat-card-header="true"/);
  assert.match(reviewHtml, /<h4[^>]*line-clamp-2[^>]*sm:truncate/);
  assert.match(reviewHtml, /待确认/);
  assert.match(reviewHtml, /完成/);
  // 描述中的 Markdown 符号被清洗，不把 `## 目标` 原样暴露到摘要首行
  assert.match(
    reviewHtml,
    /目标 优化 ChatActionCard 与 stripMarkdownSummary 验证点击防冒泡/,
  );
  assert.doesNotMatch(
    reviewHtml,
    /## 目标|\*\*ChatActionCard\*\*|`stripMarkdownSummary`/,
  );

  const doneHtml = renderRichText(
    "[已完成卡片示例](cycledock://card/card-done)",
  );
  assert.doesNotMatch(doneHtml, /data-chat-card-complete="true"/);
  assert.match(doneHtml, /已完成/);

  const workingHtml = renderRichText(
    "[修复登录超时](cycledock://card/card-1)",
  );
  assert.doesNotMatch(workingHtml, /data-chat-card-complete="true"/);

  const noDescHtml = renderRichText(
    "[无描述待办卡片](cycledock://card/card-no-desc)",
  );
  assert.doesNotMatch(noDescHtml, /<p[^>]*class="[^"]*line-clamp-1/);
  assert.match(noDescHtml, /任务/);
  assert.match(noDescHtml, /card-no-/);
});
