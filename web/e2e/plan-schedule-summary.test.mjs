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
const { summarizePlanSchedules, formatNextTriggerTime } =
  await server.ssrLoadModule("/src/features/plans/schedule-summary.ts");
const { default: PlanPage } = await server.ssrLoadModule(
  "/src/features/plans/PlanPage.tsx",
);
const { ProjectContext } = await server.ssrLoadModule(
  "/src/app/project-context.ts",
);
const { TooltipProvider } = await server.ssrLoadModule(
  "/src/shared/ui/tooltip.tsx",
);

function localISO(year, month, day, hour, minute) {
  return new Date(year, month - 1, day, hour, minute, 0, 0).toISOString();
}

function makeSchedule(overrides = {}) {
  return {
    id: overrides.id || "sched-1",
    card_id: overrides.card_id || "card-1",
    card_title: overrides.card_title || "测试定时卡",
    ongoing_state: overrides.ongoing_state || "active",
    enabled: true,
    frequency: "daily",
    hour: 9,
    minute: 0,
    timezone: "Asia/Shanghai",
    next_trigger_at: localISO(2026, 11, 22, 9, 0),
    ...overrides,
  };
}

test("单条每日、每周、每月与间隔规则展示正确", () => {
  const now = new Date(2026, 10, 22, 8, 0, 0);

  const daily = summarizePlanSchedules(
    [
      makeSchedule({
        frequency: "daily",
        hour: 9,
        minute: 30,
        next_trigger_at: localISO(2026, 11, 22, 9, 30),
      }),
    ],
    { ongoingState: "active", now },
  );
  assert.equal(daily.rulesText, "每天 09:30");
  assert.equal(daily.nextTriggerText, "下次 今天 09:30");
  assert.equal(daily.statusHint, null);
  assert.equal(daily.hasFailedAttempt, false);
  assert.equal(daily.isEmpty, false);

  const weekly = summarizePlanSchedules(
    [
      makeSchedule({
        frequency: "weekly",
        day_of_week: 3,
        hour: 9,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 25, 9, 0),
      }),
    ],
    { ongoingState: "active", now },
  );
  assert.equal(weekly.rulesText, "每周三 09:00");
  assert.equal(weekly.nextTriggerText, "下次 11/25 09:00");

  const monthly = summarizePlanSchedules(
    [
      makeSchedule({
        frequency: "monthly",
        day_of_month: 1,
        hour: 8,
        minute: 15,
        next_trigger_at: localISO(2026, 12, 1, 8, 15),
      }),
    ],
    { ongoingState: "active", now },
  );
  assert.equal(monthly.rulesText, "每月 1 日 08:15");
  assert.equal(monthly.nextTriggerText, "下次 12/01 08:15");

  const interval = summarizePlanSchedules(
    [
      makeSchedule({
        frequency: "interval",
        interval_minutes: 30,
        next_trigger_at: localISO(2026, 11, 22, 8, 30),
      }),
    ],
    { ongoingState: "active", now },
  );
  assert.equal(interval.rulesText, "每 30 分钟");
  assert.equal(interval.nextTriggerText, "下次 今天 08:30");
});

test("多条每日规则按时间正序合并且去重，只取最早的一次下次时间", () => {
  const now = new Date(2026, 10, 22, 10, 0, 0);
  const summary = summarizePlanSchedules(
    [
      makeSchedule({
        id: "d-1",
        hour: 13,
        minute: 17,
        next_trigger_at: localISO(2026, 11, 22, 13, 17),
      }),
      makeSchedule({
        id: "d-2",
        hour: 20,
        minute: 17,
        next_trigger_at: localISO(2026, 11, 22, 20, 17),
      }),
      makeSchedule({
        id: "d-3",
        hour: 8,
        minute: 17,
        next_trigger_at: localISO(2026, 11, 23, 8, 17),
      }),
      makeSchedule({
        id: "d-dup",
        hour: 13,
        minute: 17,
        next_trigger_at: localISO(2026, 11, 22, 13, 17),
      }),
    ],
    { ongoingState: "active", now },
  );

  assert.equal(summary.rulesText, "每天 08:17、13:17、20:17");
  assert.equal(summary.nextTriggerText, "下次 今天 13:17");
});

test("多条每周规则同时间合并并按周一到周日排序，不同时间用中点分隔", () => {
  const now = new Date(2026, 10, 22, 10, 0, 0);

  const sameTime = summarizePlanSchedules(
    [
      makeSchedule({
        id: "w-1",
        frequency: "weekly",
        day_of_week: 6,
        hour: 9,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 28, 9, 0),
      }),
      makeSchedule({
        id: "w-2",
        frequency: "weekly",
        day_of_week: 3,
        hour: 9,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 25, 9, 0),
      }),
    ],
    { ongoingState: "active", now },
  );
  assert.equal(sameTime.rulesText, "每周三、周六 09:00");
  assert.equal(sameTime.nextTriggerText, "下次 11/25 09:00");

  const weekend = summarizePlanSchedules(
    [
      makeSchedule({
        id: "w-sun",
        frequency: "weekly",
        day_of_week: 0,
        hour: 10,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 29, 10, 0),
      }),
      makeSchedule({
        id: "w-sat",
        frequency: "weekly",
        day_of_week: 6,
        hour: 10,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 28, 10, 0),
      }),
    ],
    { ongoingState: "active", now },
  );
  assert.equal(weekend.rulesText, "每周六、周日 10:00");

  const diffTime = summarizePlanSchedules(
    [
      makeSchedule({
        id: "w-3",
        frequency: "weekly",
        day_of_week: 6,
        hour: 18,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 28, 18, 0),
      }),
      makeSchedule({
        id: "w-4",
        frequency: "weekly",
        day_of_week: 3,
        hour: 9,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 25, 9, 0),
      }),
    ],
    { ongoingState: "active", now },
  );
  assert.equal(diffTime.rulesText, "每周三 09:00 · 每周六 18:00");
  assert.equal(diffTime.nextTriggerText, "下次 11/25 09:00");
});

test("混合频率规则按紧凑顺序拼接，且每月同时间也会自然合并", () => {
  const now = new Date(2026, 10, 22, 10, 0, 0);
  const summary = summarizePlanSchedules(
    [
      makeSchedule({
        id: "m-15",
        frequency: "monthly",
        day_of_month: 15,
        hour: 9,
        minute: 0,
        next_trigger_at: localISO(2026, 12, 15, 9, 0),
      }),
      makeSchedule({
        id: "m-1",
        frequency: "monthly",
        day_of_month: 1,
        hour: 9,
        minute: 0,
        next_trigger_at: localISO(2026, 12, 1, 9, 0),
      }),
      makeSchedule({
        id: "d-1",
        frequency: "daily",
        hour: 19,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 22, 19, 0),
      }),
      makeSchedule({
        id: "d-2",
        frequency: "daily",
        hour: 9,
        minute: 30,
        next_trigger_at: localISO(2026, 11, 23, 9, 30),
      }),
    ],
    { ongoingState: "active", now },
  );

  assert.equal(summary.rulesText, "每天 09:30、19:00 · 每月 1、15 日 09:00");
  assert.equal(summary.nextTriggerText, "下次 今天 19:00");
});

test("下次触发时间准确区分今天、明天、跨午夜和 MM/DD HH:mm，并支持英文词条", () => {
  const lateNight = new Date(2026, 10, 22, 23, 50, 0);
  assert.equal(
    formatNextTriggerTime(localISO(2026, 11, 22, 23, 55), lateNight),
    "下次 今天 23:55",
  );
  // 距离仅 20 分钟但已跨午夜，应显示“明天”而不是“今天”
  assert.equal(
    formatNextTriggerTime(localISO(2026, 11, 23, 0, 10), lateNight),
    "下次 明天 00:10",
  );
  assert.equal(
    formatNextTriggerTime(localISO(2026, 11, 25, 9, 0), lateNight),
    "下次 11/25 09:00",
  );
  assert.equal(formatNextTriggerTime("not-a-date", lateNight), null);
  assert.equal(formatNextTriggerTime("", lateNight), null);

  const enDict = { 下次: "Next", 今天: "Today", 明天: "Tomorrow" };
  const enT = (key) => enDict[key] || key;
  assert.equal(
    formatNextTriggerTime(localISO(2026, 11, 23, 3, 30), lateNight, enT),
    "Next Tomorrow 03:30",
  );
});

test("边界场景：部分停用、全部停用、卡片暂停/归档、无规则、非法时间与执行失败提示", () => {
  const now = new Date(2026, 10, 22, 10, 0, 0);

  // 无规则
  const empty = summarizePlanSchedules([], { ongoingState: "active", now });
  assert.deepEqual(empty, {
    rulesText: "",
    nextTriggerText: null,
    statusHint: null,
    hasFailedAttempt: false,
    isEmpty: true,
  });

  // 部分停用：忽略停用规则的时间、更早的 next_trigger_at 和失败状态
  const partial = summarizePlanSchedules(
    [
      makeSchedule({
        id: "disabled-earlier",
        enabled: false,
        hour: 11,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 22, 11, 0),
        last_attempt_status: "failed",
      }),
      makeSchedule({
        id: "enabled-invalid",
        enabled: true,
        hour: 14,
        minute: 0,
        next_trigger_at: "invalid-date",
      }),
      makeSchedule({
        id: "enabled-valid",
        enabled: true,
        hour: 19,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 22, 19, 0),
      }),
    ],
    { ongoingState: "active", now },
  );
  assert.equal(partial.rulesText, "每天 14:00、19:00");
  assert.equal(partial.nextTriggerText, "下次 今天 19:00");
  assert.equal(partial.statusHint, null);
  assert.equal(partial.hasFailedAttempt, false);

  // 全部停用：不显示“下次”，提示“已停用”
  const allDisabled = summarizePlanSchedules(
    [
      makeSchedule({
        id: "off-1",
        enabled: false,
        hour: 9,
        minute: 30,
        next_trigger_at: localISO(2026, 11, 23, 9, 30),
        last_attempt_status: "failed",
      }),
      makeSchedule({
        id: "off-2",
        enabled: false,
        hour: 19,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 22, 19, 0),
      }),
    ],
    { ongoingState: "active", now },
  );
  assert.equal(allDisabled.rulesText, "每天 09:30、19:00");
  assert.equal(allDisabled.nextTriggerText, null);
  assert.equal(allDisabled.statusHint, "已停用");
  assert.equal(allDisabled.hasFailedAttempt, false);

  // 卡片已暂停：不显示“下次”，提示“已暂停”
  const paused = summarizePlanSchedules(
    [
      makeSchedule({
        id: "p-1",
        enabled: true,
        hour: 9,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 23, 9, 0),
      }),
    ],
    { ongoingState: "paused", now },
  );
  assert.equal(paused.rulesText, "每天 09:00");
  assert.equal(paused.nextTriggerText, null);
  assert.equal(paused.statusHint, "已暂停");

  // 卡片已归档：不显示“下次”，提示“已归档”
  const archived = summarizePlanSchedules(
    [
      makeSchedule({
        id: "a-1",
        enabled: true,
        hour: 9,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 23, 9, 0),
      }),
    ],
    { ongoingState: "archived", now },
  );
  assert.equal(archived.nextTriggerText, null);
  assert.equal(archived.statusHint, "已归档");

  // 多条启用规则同时失败：仅返回一次失败标记
  const failed = summarizePlanSchedules(
    [
      makeSchedule({
        id: "f-1",
        hour: 8,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 23, 8, 0),
        last_attempt_status: "failed",
      }),
      makeSchedule({
        id: "f-2",
        hour: 20,
        minute: 0,
        next_trigger_at: localISO(2026, 11, 22, 20, 0),
        last_attempt_status: "failed",
      }),
    ],
    { ongoingState: "active", now },
  );
  assert.equal(failed.hasFailedAttempt, true);
  assert.equal(failed.nextTriggerText, "下次 今天 20:00");
});

test("PlanPage 渲染多规则定时卡时每卡仅展示唯一一个「下次」和单次失败提示", () => {
  const queryClient = new QueryClient();
  const futureA = new Date(Date.now() + 2 * 3600 * 1000).toISOString();
  const futureB = new Date(Date.now() + 6 * 3600 * 1000).toISOString();
  const futureC = new Date(Date.now() + 26 * 3600 * 1000).toISOString();

  queryClient.setQueryData(
    ["cards", "proj-1"],
    [
      {
        id: "ongoing-multi",
        project_id: "proj-1",
        kind: "ongoing",
        title: "每日三时段系统健康巡检任务",
        description: "",
        ongoing_state: "active",
        health: "warning",
        created_at: "2026-11-01T00:00:00Z",
        updated_at: "2026-11-22T09:00:00Z",
      },
      {
        id: "ongoing-paused",
        project_id: "proj-1",
        kind: "ongoing",
        title: "已暂停的备份检查",
        description: "",
        ongoing_state: "paused",
        health: "healthy",
        created_at: "2026-11-01T00:00:00Z",
        updated_at: "2026-11-22T08:00:00Z",
      },
    ],
  );
  queryClient.setQueryData(
    ["project-schedules", "proj-1"],
    [
      makeSchedule({
        id: "s1",
        card_id: "ongoing-multi",
        hour: 13,
        minute: 17,
        next_trigger_at: futureA,
        last_attempt_status: "failed",
      }),
      makeSchedule({
        id: "s2",
        card_id: "ongoing-multi",
        hour: 20,
        minute: 17,
        next_trigger_at: futureB,
        last_attempt_status: "failed",
      }),
      makeSchedule({
        id: "s3",
        card_id: "ongoing-multi",
        hour: 8,
        minute: 17,
        next_trigger_at: futureC,
      }),
      makeSchedule({
        id: "s4",
        card_id: "ongoing-paused",
        ongoing_state: "paused",
        hour: 3,
        minute: 30,
        next_trigger_at: futureC,
      }),
    ],
  );

  const html = renderStatic(
    createElement(
      MemoryRouter,
      { initialEntries: ["/plans/proj-1"] },
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
          createElement(TooltipProvider, null, createElement(PlanPage)),
        ),
      ),
    ),
  );

  assert.match(html, /每天 08:17、13:17、20:17/);
  assert.equal((html.match(/下次 /g) || []).length, 1);
  assert.equal((html.match(/上次执行失败/g) || []).length, 1);
  assert.match(html, /每天 03:30/);
  assert.match(html, /已暂停/);
});
