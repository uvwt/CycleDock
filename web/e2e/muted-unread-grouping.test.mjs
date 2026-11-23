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
const { CardStatusDot } = await vite.ssrLoadModule(
  "/src/shared/ui/CardStatusDot.tsx",
);
const {
  buildPlanSidebarGroups,
  getDefaultExpandedPlanGroups,
  getUnreadPlanCardIDs,
} = await vite.ssrLoadModule("/src/features/plans/plan-sidebar-model.ts");
const {
  buildDiscussionListSections,
  getUnreadDiscussionCardIDs,
  shouldDefaultExpandMutedDiscussions,
} = await vite.ssrLoadModule("/src/features/chat/discussion-sidebar-model.ts");
const { PlanSidebar } = await vite.ssrLoadModule(
  "/src/features/plans/PlanSidebar.tsx",
);
const { DiscussionSidebar } = await vite.ssrLoadModule(
  "/src/features/chat/DiscussionSidebar.tsx",
);
const { default: PlanPage } = await vite.ssrLoadModule(
  "/src/features/plans/PlanPage.tsx",
);
const { ProjectContext } = await vite.ssrLoadModule(
  "/src/app/project-context.ts",
);
const { TooltipProvider } = await vite.ssrLoadModule(
  "/src/shared/ui/tooltip.tsx",
);
const { keys } = await vite.ssrLoadModule("/src/app/query.ts");

function makeOngoing(overrides = {}) {
  return {
    id: "plan-1",
    workspace_id: "ws-1",
    project_id: "proj-1",
    kind: "ongoing",
    title: "定时任务",
    description: "",
    ongoing_state: "active",
    health: "healthy",
    unread: false,
    marked_unread: false,
    notifications_muted: false,
    updated_at: "2026-11-22T08:00:00Z",
    created_at: "2026-11-20T08:00:00Z",
    ...overrides,
  };
}

function makeDiscussion(overrides = {}) {
  return {
    id: "disc-1",
    workspace_id: "ws-1",
    project_id: "proj-1",
    kind: "discussion",
    title: "普通讨论",
    description: "",
    discussion_state: "active",
    unread: false,
    marked_unread: false,
    notifications_muted: false,
    updated_at: "2026-11-22T08:00:00Z",
    created_at: "2026-11-20T08:00:00Z",
    ...overrides,
  };
}

test("CardStatusDot：免打扰未读降噪为浅灰点，普通未读保持亮蓝点，异常与待确认穿透静音", () => {
  const normalUnread = renderToStaticMarkup(
    React.createElement(CardStatusDot, { status: "unread" }),
  );
  assert.match(normalUnread, /bg-sky-500/);
  assert.doesNotMatch(normalUnread, /data-muted-unread="true"/);

  const mutedUnread = renderToStaticMarkup(
    React.createElement(CardStatusDot, { status: "unread", muted: true }),
  );
  assert.match(mutedUnread, /data-muted-unread="true"/);
  assert.match(mutedUnread, /bg-muted-foreground\/45/);
  assert.doesNotMatch(mutedUnread, /bg-sky-500/);

  // 强提醒与运行中即使 muted=true 也保持原有状态色
  const mutedCritical = renderToStaticMarkup(
    React.createElement(CardStatusDot, { status: "critical", muted: true }),
  );
  assert.match(mutedCritical, /bg-rose-500/);
  assert.doesNotMatch(mutedCritical, /data-muted-unread="true"/);

  const mutedApproval = renderToStaticMarkup(
    React.createElement(CardStatusDot, { status: "approval", muted: true }),
  );
  assert.match(mutedApproval, /bg-orange-500/);
});

test("定时任务分组：顺序为 需关注 -> 运行中 -> 免打扰 -> 已暂停/已归档，置顶免打扰留在运行中，异常免打扰穿透进需关注", () => {
  const cards = [
    makeOngoing({
      id: "muted-crit",
      title: "静音但异常",
      notifications_muted: true,
      health: "critical",
      unread: true,
    }),
    makeOngoing({
      id: "muted-warn",
      title: "静音但需留意",
      notifications_muted: true,
      health: "warning",
    }),
    makeOngoing({
      id: "muted-pinned",
      title: "置顶静音定时",
      notifications_muted: true,
      pinned_at: "2026-11-22T10:00:00Z",
      unread: true,
    }),
    makeOngoing({
      id: "normal-unread",
      title: "普通未读定时",
      unread: true,
      updated_at: "2026-11-22T09:00:00Z",
    }),
    makeOngoing({
      id: "muted-unread-1",
      title: "静音未读 1",
      notifications_muted: true,
      unread: true,
      updated_at: "2026-11-22T09:30:00Z",
    }),
    makeOngoing({
      id: "muted-read-2",
      title: "静音已读 2",
      notifications_muted: true,
      unread: false,
      updated_at: "2026-11-22T11:00:00Z",
    }),
    makeOngoing({
      id: "muted-paused",
      title: "静音已暂停",
      notifications_muted: true,
      ongoing_state: "paused",
    }),
  ];

  const groups = buildPlanSidebarGroups(cards);
  assert.deepEqual(
    groups.map((g) => g.key),
    ["attention", "active", "muted", "inactive"],
  );
  assert.deepEqual(
    groups[0].cards.map((c) => c.id),
    ["muted-crit", "muted-warn"],
  );
  // 置顶免打扰留在「运行中」首位，不被收进折叠组
  assert.deepEqual(
    groups[1].cards.map((c) => c.id),
    ["muted-pinned", "normal-unread"],
  );
  // 免打扰分组内未读优先于已读
  assert.deepEqual(
    groups[2].cards.map((c) => c.id),
    ["muted-unread-1", "muted-read-2"],
  );
  assert.deepEqual(
    groups[3].cards.map((c) => c.id),
    ["muted-paused"],
  );

  // 未读 ID 提取：激活且未手动标未读的卡片不计入批量已读
  assert.deepEqual(getUnreadPlanCardIDs(groups[2].cards), ["muted-unread-1"]);
  assert.deepEqual(getUnreadPlanCardIDs(groups[2].cards, "muted-unread-1"), []);

  // 默认折叠规则：muted 默认折叠，激活或搜索时自动展开
  assert.deepEqual(getDefaultExpandedPlanGroups(groups), [
    "attention",
    "active",
  ]);
  assert.deepEqual(
    getDefaultExpandedPlanGroups(groups, { activeCardID: "muted-unread-1" }),
    ["attention", "active", "muted"],
  );
  const searched = buildPlanSidebarGroups(cards, { searchQuery: "静音未读" });
  assert.deepEqual(
    getDefaultExpandedPlanGroups(searched, { searchQuery: "静音未读" }),
    ["muted"],
  );
});

test("讨论列表三段拆分：主聊天+置顶(含置顶免打扰)在顶部，未置顶免打扰收进紧邻下方的折叠组，其余普通讨论在后", () => {
  const discussions = [
    makeDiscussion({
      id: "disc-regular-old",
      title: "普通旧讨论",
      updated_at: "2026-11-21T08:00:00Z",
    }),
    makeDiscussion({
      id: "disc-muted-read",
      title: "免打扰已读",
      notifications_muted: true,
      unread: false,
      updated_at: "2026-11-22T12:00:00Z",
    }),
    makeDiscussion({
      id: "disc-muted-unread",
      title: "免打扰未读",
      notifications_muted: true,
      unread: true,
      updated_at: "2026-11-22T07:00:00Z",
    }),
    makeDiscussion({
      id: "disc-pinned-muted",
      title: "置顶但免打扰",
      notifications_muted: true,
      pinned_at: "2026-11-22T10:00:00Z",
      unread: true,
    }),
    makeDiscussion({
      id: "disc-main",
      title: "项目主聊天",
      notifications_muted: true,
    }),
    makeDiscussion({
      id: "disc-regular-new",
      title: "普通新讨论",
      updated_at: "2026-11-22T11:00:00Z",
    }),
  ];

  const sections = buildDiscussionListSections(discussions, {
    mainDiscussionID: "disc-main",
  });
  assert.deepEqual(
    sections.pinned.map((c) => c.id),
    ["disc-main", "disc-pinned-muted"],
  );
  assert.deepEqual(
    sections.muted.map((c) => c.id),
    ["disc-muted-unread", "disc-muted-read"],
  );
  assert.deepEqual(
    sections.regular.map((c) => c.id),
    ["disc-regular-new", "disc-regular-old"],
  );
  assert.equal(sections.totalCount, 6);
  assert.deepEqual(getUnreadDiscussionCardIDs(sections.muted), [
    "disc-muted-unread",
  ]);
  assert.equal(shouldDefaultExpandMutedDiscussions(sections.muted), false);
  assert.equal(
    shouldDefaultExpandMutedDiscussions(sections.muted, {
      activeCardID: "disc-muted-read",
    }),
    true,
  );
  assert.equal(
    shouldDefaultExpandMutedDiscussions(sections.muted, {
      searchQuery: "免打扰",
    }),
    true,
  );
});

test("SSR 渲染：PlanSidebar、DiscussionSidebar 与 PlanPage 正确展示免打扰折叠组、灰色未读计数与全部已读按钮", () => {
  const planCards = [
    makeOngoing({
      id: "p-active-unread",
      title: "常规告警推送",
      unread: true,
    }),
    makeOngoing({
      id: "p-pinned-muted",
      title: "置顶静音巡检",
      notifications_muted: true,
      pinned_at: "2026-11-22T10:00:00Z",
      unread: true,
    }),
    makeOngoing({
      id: "p-muted-1",
      title: "低频日志归档",
      notifications_muted: true,
      unread: true,
    }),
  ];

  const projectCtxValue = {
    projectID: "proj-1",
    project: { id: "proj-1", name: "测试项目" },
    projects: [{ id: "proj-1", name: "测试项目" }],
    isPending: false,
    isError: false,
    error: null,
    refetch: async () => {},
    setProjectID: () => {},
  };

  const renderPlanSidebar = (activeCardID) => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    queryClient.setQueryData(keys.schedules("proj-1"), []);
    return renderToStaticMarkup(
      React.createElement(
        TooltipProvider,
        null,
        React.createElement(
          QueryClientProvider,
          { client: queryClient },
          React.createElement(
            MemoryRouter,
            { initialEntries: [`/plans/proj-1/${activeCardID}`] },
            React.createElement(
              ProjectContext.Provider,
              { value: projectCtxValue },
              React.createElement(PlanSidebar, {
                cards: planCards,
                activeCardID,
                projectID: "proj-1",
              }),
            ),
          ),
        ),
      ),
    );
  };

  // 默认免打扰分组折叠：标题可见、未读计数与全部已读按钮可见，但组内卡片隐藏；置顶静音卡留在运行中且用浅灰点
  const planDefaultHtml = renderPlanSidebar("p-active-unread");
  assert.match(planDefaultHtml, /data-plan-group="muted"/);
  assert.match(planDefaultHtml, /data-muted-unread-summary="true"/);
  assert.match(planDefaultHtml, /1 未读/);
  assert.match(planDefaultHtml, /aria-label="全部已读"/);
  assert.match(planDefaultHtml, /置顶静音巡检/);
  assert.match(planDefaultHtml, /data-muted-unread="true"/);
  assert.doesNotMatch(planDefaultHtml, /低频日志归档/);

  // 激活免打扰组内卡片时自动展开
  const planMutedActiveHtml = renderPlanSidebar("p-muted-1");
  assert.match(planMutedActiveHtml, /低频日志归档/);

  // 讨论侧边栏 SSR：主聊天与置顶在免打扰分组之前，普通讨论在免打扰分组之后
  const discussionCards = [
    makeDiscussion({ id: "d-main", title: "主聊天" }),
    makeDiscussion({
      id: "d-muted-1",
      title: "机器人播报讨论",
      notifications_muted: true,
      unread: true,
    }),
    makeDiscussion({ id: "d-normal", title: "需求评审讨论", unread: true }),
  ];
  const discClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  discClient.setQueryData(keys.mainDiscussion("proj-1"), discussionCards[0]);
  const discHtml = renderToStaticMarkup(
    React.createElement(
      TooltipProvider,
      null,
      React.createElement(
        QueryClientProvider,
        { client: discClient },
        React.createElement(
          MemoryRouter,
          { initialEntries: ["/chat/proj-1/d-main"] },
          React.createElement(
            ProjectContext.Provider,
            { value: projectCtxValue },
            React.createElement(DiscussionSidebar, {
              discussions: discussionCards,
              activeCardID: "d-main",
              onSelect: () => {},
              onCreate: () => {},
            }),
          ),
        ),
      ),
    ),
  );
  assert.match(discHtml, /data-discussion-group="muted"/);
  assert.match(discHtml, /aria-label="全部已读"/);
  assert.match(discHtml, /主聊天/);
  assert.match(discHtml, /需求评审讨论/);
  assert.doesNotMatch(discHtml, /机器人播报讨论/);
  assert.ok(
    discHtml.indexOf("主聊天") >= 0 &&
      discHtml.indexOf("主聊天") <
        discHtml.indexOf('data-discussion-group="muted"'),
    `免打扰入口应位于主聊天下方 (main=${discHtml.indexOf("主聊天")}, muted=${discHtml.indexOf('data-discussion-group="muted"')})`,
  );
  assert.ok(
    discHtml.indexOf('data-discussion-group="muted"') <
      discHtml.indexOf("需求评审讨论"),
    "免打扰入口应位于普通讨论上方",
  );

  // PlanPage 总览页 SSR：置顶静音卡展示「静默未读」而非亮蓝「新消息」，未置顶免打扰收进折叠区并带「全部已读」
  const planPageClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  planPageClient.setQueryData(keys.cards("proj-1"), planCards);
  planPageClient.setQueryData(keys.schedules("proj-1"), []);
  const planPageHtml = renderToStaticMarkup(
    React.createElement(
      TooltipProvider,
      null,
      React.createElement(
        QueryClientProvider,
        { client: planPageClient },
        React.createElement(
          MemoryRouter,
          { initialEntries: ["/plans?project=proj-1"] },
          React.createElement(
            ProjectContext.Provider,
            {
              value: {
                projectID: "proj-1",
                project: { id: "proj-1", name: "测试项目" },
                projects: [{ id: "proj-1", name: "测试项目" }],
                isPending: false,
                isError: false,
                error: null,
                refetch: async () => {},
                setProjectID: () => {},
              },
            },
            React.createElement(PlanPage),
          ),
        ),
      ),
    ),
  );
  assert.match(planPageHtml, /data-plan-overview-group="muted"/);
  assert.match(planPageHtml, /data-muted-unread-badge="true"/);
  assert.match(planPageHtml, /静默未读/);
  assert.match(planPageHtml, /全部已读/);
  assert.doesNotMatch(planPageHtml, /低频日志归档/);
});
