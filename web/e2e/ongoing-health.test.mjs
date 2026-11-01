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

await server.ssrLoadModule("/src/shared/preferences.ts");
const { CardHeaderInfoCard } = await server.ssrLoadModule(
  "/src/features/cards/CardHeaderInfoCard.tsx",
);
const { TaskDetailsPanel } = await server.ssrLoadModule(
  "/src/features/cards/TaskDetailsPanel.tsx",
);
const { PlanSidebar } = await server.ssrLoadModule(
  "/src/features/plans/PlanSidebar.tsx",
);
const { OngoingHealthBadge } = await server.ssrLoadModule(
  "/src/shared/ui/OngoingHealthBadge.tsx",
);
const { TooltipProvider } = await server.ssrLoadModule(
  "/src/shared/ui/tooltip.tsx",
);

const queryClient = new QueryClient();
function renderToStaticMarkup(element) {
  return renderStatic(
    createElement(
      MemoryRouter,
      null,
      createElement(
        QueryClientProvider,
        { client: queryClient },
        createElement(TooltipProvider, null, element),
      ),
    ),
  );
}

test("Ongoing 健康状态徽章将底层枚举映射为用户友好文案，不直接暴露原始英文值", () => {
  for (const [raw, label] of [
    ["healthy", "正常"],
    ["warning", "需留意"],
    ["critical", "异常"],
    ["unknown", "待首次巡检"],
    ["", "待首次巡检"],
    [undefined, "待首次巡检"],
  ]) {
    const html = renderToStaticMarkup(
      createElement(OngoingHealthBadge, { health: raw }),
    );
    assert.match(html, new RegExp(label));
    assert.doesNotMatch(html, />(unknown|healthy|warning|critical)</i);
  }

  const hiddenHtml = renderToStaticMarkup(
    createElement(OngoingHealthBadge, {
      health: "unknown",
      hideWhenUnknown: true,
    }),
  );
  assert.equal(hiddenHtml, "");
});

test("TaskDetailsPanel、CardHeaderInfoCard 与 PlanSidebar 展示友好健康状态且不直接暴露 unknown", () => {
  const ongoingCard = {
    id: "ongoing-1",
    project_id: "proj-1",
    kind: "ongoing",
    title: "每日服务器健康巡检",
    description: "检查 CPU 与磁盘使用率",
    ongoing_state: "active",
    health: "unknown",
    created_at: "2026-11-01T00:00:00Z",
    updated_at: "2026-11-01T00:00:00Z",
  };
  const healthyCard = {
    ...ongoingCard,
    id: "ongoing-2",
    title: "正常运行巡检计划",
    health: "healthy",
  };
  const detail = {
    card: ongoingCard,
    current_attempt: null,
    timeline: [],
    pending_turns: [],
    attempt_history: [],
    schedules: [],
    health: "unknown",
    last_checkin: null,
  };

  const panelHtml = renderToStaticMarkup(
    createElement(TaskDetailsPanel, {
      detail,
      projectCards: [ongoingCard],
      onChange: async () => {},
    }),
  );
  assert.match(panelHtml, /健康状态：/);
  assert.match(panelHtml, /待首次巡检/);
  assert.doesNotMatch(panelHtml, /健康状态：unknown/);

  const headerHtml = renderToStaticMarkup(
    createElement(CardHeaderInfoCard, {
      card: ongoingCard,
      detail: { ...detail, health: "warning" },
      projectCards: [ongoingCard],
    }),
  );
  assert.match(headerHtml, /需留意/);
  assert.doesNotMatch(headerHtml, />warning</);

  const planHtml = renderToStaticMarkup(
    createElement(PlanSidebar, {
      cards: [ongoingCard, healthyCard],
      activeCardID: "ongoing-1",
      onSelect() {},
    }),
  );
  assert.match(planHtml, /每日服务器健康巡检/);
  assert.match(planHtml, /正常运行巡检计划/);
  assert.match(planHtml, /正常/);
  assert.doesNotMatch(planHtml, />(unknown|healthy|warning|critical)</i);
});
