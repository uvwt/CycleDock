import test from "node:test";
import assert from "node:assert/strict";
import {
  cardRouteForDockPeek,
  resolveDockCardMessage,
  resolveDockPeekClickAction,
} from "../src/features/chat/main-chat-dock-peek.ts";

function sampleDetail(overrides = {}) {
  return {
    id: "card-todo-1",
    project_id: "proj-1",
    kind: "todo",
    title: "修复部署流水线",
    revision: "rev-2",
    event_sequence: "12",
    message_preview: "已完成本地验证，请验收",
    body: "已完成本地验证，请验收",
    attempt_status: "completed",
    viewing: false,
    notifications_muted: false,
    notification_suppressed: false,
    ...overrides,
  };
}

test("悬浮球预览路由拼装覆盖讨论、待办、定时任务与特殊字符转义", () => {
  assert.equal(
    cardRouteForDockPeek("proj-1", "disc-2", "discussion"),
    "/chat/proj-1/disc-2",
  );
  assert.equal(
    cardRouteForDockPeek("proj-1", "plan-3", "ongoing"),
    "/plans/proj-1/plan-3",
  );
  assert.equal(
    cardRouteForDockPeek("proj-1", "todo-4", "todo"),
    "/cards/proj-1/todo-4",
  );
  assert.equal(
    cardRouteForDockPeek("proj/a b", "card#1?", "todo"),
    "/cards/proj%2Fa%20b/card%231%3F",
  );
});

test("主聊天静音不影响同项目其他未静音卡片弹预览，也不触发无关主聊天缓存失效", () => {
  const decision = resolveDockCardMessage(sampleDetail(), {
    projectID: "proj-1",
    mainCardID: "main-chat-1",
    isMainMuted: true,
    isDockOpen: false,
  });

  assert.equal(decision.shouldInvalidateMainChat, false);
  assert.deepEqual(decision.peek, {
    cardID: "card-todo-1",
    projectID: "proj-1",
    kind: "todo",
    title: "修复部署流水线",
    body: "已完成本地验证，请验收",
    isMainDiscussion: false,
    isToolApproval: false,
  });

  assert.deepEqual(resolveDockPeekClickAction(decision.peek, "main-chat-1"), {
    type: "navigate",
    to: "/cards/proj-1/card-todo-1",
  });
});

test("主聊天自身消息会失效主聊天缓存，且静音屏蔽普通消息但放行工具审批", () => {
  const normalWhileMuted = resolveDockCardMessage(
    sampleDetail({
      id: "main-chat-1",
      kind: "discussion",
      title: "主聊天",
      body: "普通进度同步",
      notifications_muted: false,
    }),
    {
      projectID: "proj-1",
      mainCardID: "main-chat-1",
      isMainMuted: true,
      isDockOpen: false,
    },
  );
  assert.equal(normalWhileMuted.shouldInvalidateMainChat, true);
  assert.equal(normalWhileMuted.peek, null);

  const approvalWhileMuted = resolveDockCardMessage(
    sampleDetail({
      id: "main-chat-1",
      kind: "discussion",
      title: "主聊天",
      body: "需要确认高风险操作",
      notification_kind: "tool_approval",
      notifications_muted: true,
    }),
    {
      projectID: "proj-1",
      mainCardID: "main-chat-1",
      isMainMuted: true,
      isDockOpen: false,
    },
  );
  assert.equal(approvalWhileMuted.shouldInvalidateMainChat, true);
  assert.equal(approvalWhileMuted.peek?.isMainDiscussion, true);
  assert.equal(approvalWhileMuted.peek?.isToolApproval, true);
  assert.deepEqual(
    resolveDockPeekClickAction(approvalWhileMuted.peek, "main-chat-1"),
    { type: "open_main_chat" },
  );
});

test("其他卡片静音时屏蔽普通消息，但工具审批穿透静音且点击直达对应路由", () => {
  const mutedNormal = resolveDockCardMessage(
    sampleDetail({
      id: "plan-1",
      kind: "ongoing",
      title: "夜间巡检",
      notifications_muted: true,
    }),
    {
      projectID: "proj-1",
      mainCardID: "main-chat-1",
      isMainMuted: false,
      isDockOpen: false,
    },
  );
  assert.equal(mutedNormal.shouldInvalidateMainChat, false);
  assert.equal(mutedNormal.peek, null);

  const mutedApproval = resolveDockCardMessage(
    sampleDetail({
      id: "plan-1",
      kind: "ongoing",
      title: "夜间巡检",
      body: "等待人工审批重启服务",
      notifications_muted: true,
      notification_kind: "tool_approval",
    }),
    {
      projectID: "proj-1",
      mainCardID: "main-chat-1",
      isMainMuted: false,
      isDockOpen: false,
    },
  );
  assert.equal(mutedApproval.shouldInvalidateMainChat, false);
  assert.equal(mutedApproval.peek?.isToolApproval, true);
  assert.equal(mutedApproval.peek?.isMainDiscussion, false);
  assert.deepEqual(
    resolveDockPeekClickAction(mutedApproval.peek, "main-chat-1"),
    { type: "navigate", to: "/plans/proj-1/plan-1" },
  );
});

test("浮窗已打开、跨项目、正在查看当前卡片或被静默收口时不展示预览气泡", () => {
  const openMain = resolveDockCardMessage(
    sampleDetail({ id: "main-chat-1", kind: "discussion" }),
    {
      projectID: "proj-1",
      mainCardID: "main-chat-1",
      isMainMuted: false,
      isDockOpen: true,
    },
  );
  assert.equal(openMain.shouldInvalidateMainChat, true);
  assert.equal(openMain.peek, null);

  const openOther = resolveDockCardMessage(
    sampleDetail({ id: "disc-other", kind: "discussion", title: "架构讨论" }),
    {
      projectID: "proj-1",
      mainCardID: "main-chat-1",
      isMainMuted: false,
      isDockOpen: true,
    },
  );
  assert.equal(openOther.shouldInvalidateMainChat, false);
  assert.equal(openOther.peek, null);

  const otherProject = resolveDockCardMessage(
    sampleDetail({ project_id: "proj-2" }),
    {
      projectID: "proj-1",
      mainCardID: "main-chat-1",
      isMainMuted: false,
      isDockOpen: false,
    },
  );
  assert.equal(otherProject.shouldInvalidateMainChat, false);
  assert.equal(otherProject.peek, null);

  const viewingCard = resolveDockCardMessage(sampleDetail({ viewing: true }), {
    projectID: "proj-1",
    mainCardID: "main-chat-1",
    isMainMuted: false,
    isDockOpen: false,
  });
  assert.equal(viewingCard.shouldInvalidateMainChat, false);
  assert.equal(viewingCard.peek, null);

  const suppressed = resolveDockCardMessage(
    sampleDetail({ notification_suppressed: true }),
    {
      projectID: "proj-1",
      mainCardID: "main-chat-1",
      isMainMuted: false,
      isDockOpen: false,
    },
  );
  assert.equal(suppressed.peek, null);
});

test("连续收到内容相同的消息也返回新对象引用以刷新 5 秒消失定时器", () => {
  const first = resolveDockCardMessage(
    sampleDetail({ id: "disc-2", kind: "discussion", title: "前端方案讨论" }),
    {
      projectID: "proj-1",
      mainCardID: "main-chat-1",
      isMainMuted: false,
      isDockOpen: false,
    },
  );
  const second = resolveDockCardMessage(
    sampleDetail({ id: "disc-2", kind: "discussion", title: "前端方案讨论" }),
    {
      projectID: "proj-1",
      mainCardID: "main-chat-1",
      isMainMuted: false,
      isDockOpen: false,
    },
  );
  assert.ok(first.peek);
  assert.ok(second.peek);
  assert.notEqual(first.peek, second.peek);
  assert.deepEqual(resolveDockPeekClickAction(second.peek, "main-chat-1"), {
    type: "navigate",
    to: "/chat/proj-1/disc-2",
  });
});
