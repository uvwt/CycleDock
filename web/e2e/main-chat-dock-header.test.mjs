import test from "node:test";
import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(__dirname, "..");

const vite = await createServer({
  root: webRoot,
  server: { middlewareMode: true, hmr: false },
  appType: "custom",
  logLevel: "error",
});

test.after(async () => {
  await vite.close();
});

await vite.ssrLoadModule("/src/shared/preferences.ts");
const { MainChatDockHeader } = await vite.ssrLoadModule(
  "/src/features/chat/MainChatDock.tsx",
);
const { CardHeaderInfoCard } = await vite.ssrLoadModule(
  "/src/features/cards/CardHeaderInfoCard.tsx",
);
const { TooltipProvider } = await vite.ssrLoadModule(
  "/src/shared/ui/tooltip.tsx",
);

function makeCard(overrides = {}) {
  return {
    id: "card-1",
    workspace_id: "ws-1",
    project_id: "proj-1",
    kind: "todo",
    title: "示例卡片",
    description: "",
    todo_state: "working",
    unread: false,
    marked_unread: false,
    notifications_muted: false,
    updated_at: "2026-11-24T08:00:00Z",
    created_at: "2026-11-24T08:00:00Z",
    ...overrides,
  };
}

function renderCardHeaderInfo(card) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return renderToStaticMarkup(
    React.createElement(
      QueryClientProvider,
      { client: qc },
      React.createElement(
        MemoryRouter,
        { initialEntries: ["/cards/proj-1/" + card.id] },
        React.createElement(
          TooltipProvider,
          null,
          React.createElement(CardHeaderInfoCard, { card }),
        ),
      ),
    ),
  );
}

test("主聊天小窗顶栏不渲染通知静音/取消静音按钮，保留完整打开与关闭按钮", () => {
  const unmutedHtml = renderToStaticMarkup(
    React.createElement(MainChatDockHeader, {
      title: "项目主聊天",
      isMainMuted: false,
      canExpand: true,
      onExpand: () => {},
      onClose: () => {},
    }),
  );
  assert.match(unmutedHtml, /项目主聊天/);
  assert.match(unmutedHtml, /aria-label="在完整页面打开"/);
  assert.match(unmutedHtml, /aria-label="关闭主聊天"/);
  assert.doesNotMatch(unmutedHtml, /aria-label="静音通知"/);
  assert.doesNotMatch(unmutedHtml, /aria-label="取消静音"/);
  assert.doesNotMatch(unmutedHtml, /aria-label="通知已静音"/);

  // 已静音时顶栏标题仍保留只读静音图标提示，但不提供静音切换按钮
  const mutedHtml = renderToStaticMarkup(
    React.createElement(MainChatDockHeader, {
      title: "项目主聊天",
      isMainMuted: true,
      canExpand: true,
      onExpand: () => {},
      onClose: () => {},
    }),
  );
  assert.match(mutedHtml, /aria-label="通知已静音"/);
  assert.match(mutedHtml, /aria-label="在完整页面打开"/);
  assert.match(mutedHtml, /aria-label="关闭主聊天"/);
  assert.doesNotMatch(mutedHtml, /aria-label="静音通知"/);
  assert.doesNotMatch(mutedHtml, /aria-label="取消静音"/);
});

test("常规卡片与完整详情页的推送通知静音开关不受主聊天小窗改动影响", () => {
  const todoCard = makeCard({
    id: "todo-1",
    kind: "todo",
    title: "普通待办任务",
    notifications_muted: false,
  });
  const todoHtml = renderCardHeaderInfo(todoCard);
  assert.match(todoHtml, /role="switch"/);
  assert.match(todoHtml, /aria-label="静音通知"/);

  const mutedOngoingCard = makeCard({
    id: "ongoing-1",
    kind: "ongoing",
    ongoing_state: "active",
    title: "定时巡检任务",
    notifications_muted: true,
  });
  const ongoingHtml = renderCardHeaderInfo(mutedOngoingCard);
  assert.match(ongoingHtml, /role="switch"/);
  assert.match(ongoingHtml, /aria-label="取消静音"/);

  const fullMainChatCard = makeCard({
    id: "main-chat-1",
    kind: "discussion",
    is_main_discussion: true,
    discussion_state: "active",
    title: "项目主聊天",
    notifications_muted: false,
  });
  const fullMainHtml = renderCardHeaderInfo(fullMainChatCard);
  assert.match(fullMainHtml, /role="switch"/);
  assert.match(fullMainHtml, /aria-label="静音通知"/);
});
