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
