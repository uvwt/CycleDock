import test, { after } from "node:test";
import assert from "node:assert/strict";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement } from "react";
import { renderToStaticMarkup as renderStatic } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { createServer } from "vite";

const server = await createServer({
  server: { middlewareMode: true, hmr: false },
  appType: "custom",
});
after(() => server.close());

await server.ssrLoadModule("/src/shared/preferences.ts");
const {
  buildPlanSidebarGroups,
  buildPlanSidebarSubtitle,
  getDefaultExpandedPlanGroups,
  isPlanAttentionCard,
} = await server.ssrLoadModule("/src/features/plans/plan-sidebar-model.ts");
const { PlanSidebar } = await server.ssrLoadModule(
  "/src/features/plans/PlanSidebar.tsx",
);
const { QUICK_SWITCHER_TAB_ORDER } = await server.ssrLoadModule(
  "/src/shared/lib/quick-switcher-tabs.ts",
);
const { TooltipProvider } = await server.ssrLoadModule(
  "/src/shared/ui/tooltip.tsx",
);
const { ProjectContext } = await server.ssrLoadModule(
  "/src/app/project-context.ts",
);
const { keys } = await server.ssrLoadModule("/src/app/query.ts");

function localISO(year, month, day, hour, minute) {
  return new Date(year, month - 1, day, hour, minute, 0, 0).toISOString();
}

function makeCard(overrides = {}) {
  return {
    id: overrides.id || "plan-1",
    project_id: overrides.project_id || "proj-1",
    kind: overrides.kind || "ongoing",
    title: overrides.title ?? "测试定时任务",
    description: overrides.description ?? "",
    ongoing_state: overrides.ongoing_state ?? "active",
    health: overrides.health ?? "healthy",
    created_at: overrides.created_at || "2026-11-20T08:00:00Z",
    updated_at: overrides.updated_at || "2026-11-22T08:00:00Z",
    ...overrides,
  };
}

function makeSchedule(overrides = {}) {
  return {
    id: overrides.id || "sched-1",
    card_id: overrides.card_id || "plan-1",
    card_title: overrides.card_title || "测试定时任务",
    ongoing_state: overrides.ongoing_state || "active",
    enabled: true,
    frequency: "daily",
    hour: 8,
    minute: 17,
    timezone: "Asia/Shanghai",
    next_trigger_at: localISO(2026, 11, 22, 13, 17),
    ...overrides,
  };
}

test("分组与组间顺序严格为 需关注 -> 运行中 -> 已暂停 / 已归档，且忽略非定时卡和空组", () => {
  const cards = [
    makeCard({ id: "todo-1", kind: "todo", title: "普通待办" }),
    makeCard({ id: "disc-1", kind: "discussion", title: "讨论" }),
    makeCard({
      id: "paused-crit",
      ongoing_state: "paused",
      health: "critical",
      title: "已暂停但历史异常",
    }),
    makeCard({
      id: "archived-1",
      ongoing_state: "archived",
      health: "healthy",
      title: "已归档巡检",
    }),
    makeCard({
      id: "ack-1",
      ongoing_state: "active",
      health: "acknowledged",
      title: "已确认巡检",
    }),
    makeCard({
      id: "warn-1",
      ongoing_state: "active",
      health: "warning",
      title: "需留意巡检",
    }),
    makeCard({
      id: "crit-1",
      ongoing_state: "active",
      health: "critical",
      title: "严重异常巡检",
    }),
  ];

  assert.equal(isPlanAttentionCard(cards[2]), false);
  assert.equal(isPlanAttentionCard(cards[4]), false);
  assert.equal(isPlanAttentionCard(cards[5]), true);
  assert.equal(isPlanAttentionCard(cards[6]), true);

  const groups = buildPlanSidebarGroups(cards);
  assert.deepEqual(
    groups.map((g) => g.key),
    ["attention", "active", "inactive"],
  );
  assert.deepEqual(
    groups[0].cards.map((c) => c.id),
    ["crit-1", "warn-1"],
  );
  assert.deepEqual(
    groups[1].cards.map((c) => c.id),
    ["ack-1"],
  );
  assert.deepEqual(
    groups[2].cards.map((c) => c.id),
    ["paused-crit", "archived-1"],
  );

  // 仅存在运行中健康卡时，不显示空的「需关注」和「已暂停 / 已归档」分组
  const onlyActive = buildPlanSidebarGroups([
    makeCard({ id: "ok-1", health: "healthy" }),
  ]);
  assert.deepEqual(
    onlyActive.map((g) => g.key),
    ["active"],
  );
});

test("需关注组内排序：critical > warning -> 置顶 -> 正在执行/未读 -> 最近更新时间倒序", () => {
  const cards = [
    makeCard({
      id: "warn-pinned",
      health: "warning",
      pinned_at: "2026-11-22T09:00:00Z",
      updated_at: "2026-11-22T12:00:00Z",
    }),
    makeCard({
      id: "crit-idle-old",
      health: "critical",
      updated_at: "2026-11-22T08:00:00Z",
    }),
    makeCard({
      id: "crit-idle-new",
      health: "critical",
      updated_at: "2026-11-22T11:00:00Z",
    }),
    makeCard({
      id: "crit-unread",
      health: "critical",
      unread: true,
      updated_at: "2026-11-22T07:00:00Z",
    }),
    makeCard({
      id: "crit-running",
      health: "critical",
      current_attempt_status: "running",
      updated_at: "2026-11-22T06:00:00Z",
    }),
    makeCard({
      id: "crit-pinned",
      health: "critical",
      pinned_at: "2026-11-22T05:00:00Z",
      updated_at: "2026-11-22T05:00:00Z",
    }),
  ];

  const groups = buildPlanSidebarGroups(cards);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].key, "attention");
  assert.deepEqual(
    groups[0].cards.map((c) => c.id),
    [
      "crit-pinned",
      "crit-running",
      "crit-unread",
      "crit-idle-new",
      "crit-idle-old",
      "warn-pinned",
    ],
  );
});

test("运行中组内排序：置顶 -> 正在执行/未读 -> 最近更新时间倒序", () => {
  const cards = [
    makeCard({ id: "idle-old", updated_at: "2026-11-22T08:00:00Z" }),
    makeCard({ id: "idle-new", updated_at: "2026-11-22T11:00:00Z" }),
    makeCard({
      id: "unread-1",
      unread: true,
      updated_at: "2026-11-22T07:00:00Z",
    }),
    makeCard({
      id: "active-running",
      updated_at: "2026-11-22T06:00:00Z",
    }),
    makeCard({
      id: "pinned-1",
      pinned_at: "2026-11-22T05:00:00Z",
      updated_at: "2026-11-22T05:00:00Z",
    }),
  ];

  const groups = buildPlanSidebarGroups(cards, {
    activeCardID: "active-running",
    activeAttemptStatus: "running",
  });
  assert.deepEqual(
    groups[0].cards.map((c) => c.id),
    ["pinned-1", "active-running", "unread-1", "idle-new", "idle-old"],
  );
});

test("已暂停 / 已归档组内排序：paused > archived -> 置顶 -> 最近更新时间倒序", () => {
  const cards = [
    makeCard({
      id: "archived-pinned",
      ongoing_state: "archived",
      pinned_at: "2026-11-22T10:00:00Z",
      updated_at: "2026-11-22T12:00:00Z",
    }),
    makeCard({
      id: "archived-plain",
      ongoing_state: "archived",
      updated_at: "2026-11-22T13:00:00Z",
    }),
    makeCard({
      id: "paused-old",
      ongoing_state: "paused",
      updated_at: "2026-11-22T08:00:00Z",
    }),
    makeCard({
      id: "paused-new",
      ongoing_state: "paused",
      updated_at: "2026-11-22T11:00:00Z",
    }),
    makeCard({
      id: "paused-pinned",
      ongoing_state: "paused",
      pinned_at: "2026-11-22T09:00:00Z",
      updated_at: "2026-11-22T07:00:00Z",
    }),
  ];

  const groups = buildPlanSidebarGroups(cards);
  assert.equal(groups[0].key, "inactive");
  assert.deepEqual(
    groups[0].cards.map((c) => c.id),
    [
      "paused-pinned",
      "paused-new",
      "paused-old",
      "archived-pinned",
      "archived-plain",
    ],
  );
});

test("搜索按标题与描述过滤，且默认展开规则正确处理激活项和搜索态", () => {
  const cards = [
    makeCard({
      id: "att-1",
      title: "生产证书巡检",
      description: "检查 TLS 到期时间",
      health: "critical",
    }),
    makeCard({
      id: "act-1",
      title: "每日简报",
      description: "汇总 GitHub Trending",
      health: "healthy",
    }),
    makeCard({
      id: "paused-1",
      title: "旧版备份核查",
      description: "归档数据库校验",
      ongoing_state: "paused",
    }),
  ];

  const allGroups = buildPlanSidebarGroups(cards);
  // 默认：需关注 + 运行中展开，已暂停/已归档折叠
  assert.deepEqual(getDefaultExpandedPlanGroups(allGroups), [
    "attention",
    "active",
  ]);
  // 当前激活卡在已暂停组内：自动展开已暂停/已归档
  assert.deepEqual(
    getDefaultExpandedPlanGroups(allGroups, { activeCardID: "paused-1" }),
    ["attention", "active", "inactive"],
  );
  // 搜索关键字命中描述：仅保留命中组，且自动展开已暂停/已归档
  const searched = buildPlanSidebarGroups(cards, { searchQuery: "数据库" });
  assert.deepEqual(
    searched.map((g) => g.key),
    ["inactive"],
  );
  assert.deepEqual(
    getDefaultExpandedPlanGroups(searched, { searchQuery: "数据库" }),
    ["inactive"],
  );
});

test("排程副标题复用 schedule-summary 正确输出下次时间、空规则、已停用、已暂停/已归档和失败提示", () => {
  const now = new Date(2026, 10, 22, 10, 0, 0);

  // 运行中多条每日规则
  const running = buildPlanSidebarSubtitle(
    { ongoing_state: "active" },
    [
      makeSchedule({
        id: "s1",
        hour: 8,
        minute: 17,
        next_trigger_at: localISO(2026, 11, 23, 8, 17),
      }),
      makeSchedule({
        id: "s2",
        hour: 13,
        minute: 17,
        next_trigger_at: localISO(2026, 11, 22, 13, 17),
      }),
      makeSchedule({
        id: "s3",
        hour: 20,
        minute: 17,
        next_trigger_at: localISO(2026, 11, 22, 20, 17),
      }),
    ],
    { now },
  );
  assert.equal(running.text, "下次 今天 13:17 · 每天 08:17、13:17、20:17");
  assert.equal(running.hasFailedAttempt, false);

  // 无排程
  const empty = buildPlanSidebarSubtitle({ ongoing_state: "active" }, [], {
    now,
  });
  assert.equal(empty.text, "未设置自动执行时间");
  assert.equal(empty.hasFailedAttempt, false);

  // 全部停用
  const disabled = buildPlanSidebarSubtitle(
    { ongoing_state: "active" },
    [makeSchedule({ enabled: false, hour: 9, minute: 0 })],
    { now },
  );
  assert.equal(disabled.text, "已停用 · 每天 09:00");
  assert.equal(disabled.hasFailedAttempt, false);

  // 已暂停与已归档
  const paused = buildPlanSidebarSubtitle({ ongoing_state: "paused" }, [], {
    now,
  });
  assert.equal(paused.text, "已暂停");
  const archived = buildPlanSidebarSubtitle(
    { ongoing_state: "archived" },
    [makeSchedule({ hour: 9, minute: 0 })],
    { now },
  );
  assert.equal(archived.text, "已归档 · 每天 09:00");

  // 最近一次执行失败
  const failed = buildPlanSidebarSubtitle(
    { ongoing_state: "active" },
    [
      makeSchedule({
        hour: 13,
        minute: 17,
        next_trigger_at: localISO(2026, 11, 22, 13, 17),
        last_attempt_status: "failed",
      }),
    ],
    { now },
  );
  assert.equal(failed.hasFailedAttempt, true);
  assert.equal(failed.failedLabel, "上次执行失败");
  assert.equal(failed.text, "下次 今天 13:17 · 每天 13:17");
});

test("PlanSidebar SSR 渲染：顶栏搜索与总览可见、运行中胶囊降噪、已暂停默认折叠且激活时展开", () => {
  const now = new Date();
  const laterToday = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate(),
    23,
    17,
    0,
    0,
  ).toISOString();

  const cards = [
    makeCard({
      id: "card-crit",
      title: "线上节点告警巡检",
      health: "critical",
      ongoing_state: "active",
    }),
    makeCard({
      id: "card-ok",
      title: "每日技术资讯汇总",
      health: "healthy",
      ongoing_state: "active",
    }),
    makeCard({
      id: "card-ack",
      title: "磁盘水位核对",
      health: "acknowledged",
      ongoing_state: "active",
    }),
    makeCard({
      id: "card-paused",
      title: "临时停用的测试巡检",
      health: "healthy",
      ongoing_state: "paused",
    }),
  ];

  const schedules = [
    makeSchedule({
      id: "s-crit",
      card_id: "card-crit",
      hour: 23,
      minute: 17,
      next_trigger_at: laterToday,
      last_attempt_status: "failed",
    }),
    makeSchedule({
      id: "s-ok",
      card_id: "card-ok",
      hour: 8,
      minute: 17,
      next_trigger_at: laterToday,
    }),
  ];

  function renderSidebar(activeCardID) {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    queryClient.setQueryData(keys.schedules("proj-1"), schedules);

    const projectValue = {
      projects: [
        {
          id: "proj-1",
          name: "默认项目",
          description: "",
          created_at: "2026-11-01T00:00:00Z",
          updated_at: "2026-11-01T00:00:00Z",
        },
      ],
      projectID: "proj-1",
      project: {
        id: "proj-1",
        name: "默认项目",
        description: "",
        created_at: "2026-11-01T00:00:00Z",
        updated_at: "2026-11-01T00:00:00Z",
      },
      isPending: false,
      error: null,
      refetch: () => {},
      setProjectID: () => {},
    };

    return renderStatic(
      createElement(
        QueryClientProvider,
        { client: queryClient },
        createElement(
          MemoryRouter,
          { initialEntries: ["/plans/proj-1/card-ok"] },
          createElement(
            ProjectContext.Provider,
            { value: projectValue },
            createElement(
              TooltipProvider,
              null,
              createElement(PlanSidebar, {
                cards,
                projectID: "proj-1",
                activeCardID,
                onSelect: () => {},
              }),
            ),
          ),
        ),
      ),
    );
  }

  const htmlDefault = renderSidebar("card-ok");
  // 顶栏搜索与总览按钮渲染，不隐藏移动端顶栏
  assert.ok(htmlDefault.includes("搜索定时任务…"));
  assert.ok(htmlDefault.includes("总览"));
  assert.ok(!htmlDefault.includes("hidden md:block"));

  // 三组存在，顺序正确
  const idxAttention = htmlDefault.indexOf('data-plan-group="attention"');
  const idxActive = htmlDefault.indexOf('data-plan-group="active"');
  const idxInactive = htmlDefault.indexOf('data-plan-group="inactive"');
  assert.ok(idxAttention > -1 && idxActive > idxAttention && idxInactive > idxActive);

  // 需关注保留异常徽章与失败红字
  assert.ok(htmlDefault.includes("线上节点告警巡检"));
  assert.ok(htmlDefault.includes("异常"));
  assert.ok(htmlDefault.includes("上次执行失败"));

  // 运行中展示排程副标题，不再展示「正常」/「已确认」胶囊
  assert.ok(htmlDefault.includes("每日技术资讯汇总"));
  assert.ok(htmlDefault.includes("每天 08:17"));
  assert.ok(htmlDefault.includes("未设置自动执行时间"));
  assert.ok(!htmlDefault.includes('data-health-status="healthy"'));
  assert.ok(!htmlDefault.includes('data-health-status="acknowledged"'));

  // 已暂停组默认折叠，不渲染组内卡片标题
  assert.ok(!htmlDefault.includes("临时停用的测试巡检"));

  // 当 activeCardID 属于已暂停组时自动展开，并展示轻量「已暂停」标签
  const htmlPausedActive = renderSidebar("card-paused");
  assert.ok(htmlPausedActive.includes("临时停用的测试巡检"));
  assert.ok(htmlPausedActive.includes("已暂停"));
});

test("快速切换器 Tab 顺序严格满足 焦点 -> 讨论 -> 定时 排在前三位", () => {
  assert.equal(QUICK_SWITCHER_TAB_ORDER[0], "focus");
  assert.equal(QUICK_SWITCHER_TAB_ORDER[1], "discussion");
  assert.equal(QUICK_SWITCHER_TAB_ORDER[2], "ongoing");
  assert.deepEqual([...QUICK_SWITCHER_TAB_ORDER], [
    "focus",
    "discussion",
    "ongoing",
    "todo",
    "working",
    "review",
    "done",
  ]);
});
