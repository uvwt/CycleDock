import assert from "node:assert/strict";
import test from "node:test";
import {
  computeLatestVisibleMessageSequence,
  mergeCardReadSnapshots,
  resolveIncomingUnreadDividerAnchor,
  resolveInitialUnreadDividerAnchor,
  resolveUnreadDividerPlacement,
  shouldTriggerStayAutoRead,
} from "../src/features/timeline/unreadDivider.ts";

function makeEvent(overrides = {}) {
  return {
    id: overrides.id ?? `evt-${overrides.sequence ?? 1}`,
    sequence: overrides.sequence ?? 1,
    type: overrides.type ?? "message",
    actor_kind: overrides.actor_kind ?? "agent",
    actor_name: overrides.actor_name ?? "Cycle Agent",
    content: overrides.content ?? "hello",
    created_at: overrides.created_at ?? "2026-04-17T08:00:00Z",
    ...overrides,
  };
}

function makeTimelineItems(events) {
  return events.map((event) => ({
    kind: "event",
    key: event.id,
    displayTimestamp: event.created_at,
    row: {
      key: event.id,
      event,
    },
  }));
}

test("已读历史后收到新消息时锁定进入前的已读水位线", () => {
  const events = [
    makeEvent({ id: "e1", sequence: 1, type: "comment", actor_kind: "human" }),
    makeEvent({ id: "e2", sequence: 2, type: "message", actor_kind: "agent" }),
    makeEvent({ id: "e3", sequence: 3, type: "message", actor_kind: "agent" }),
    makeEvent({ id: "e4", sequence: 4, type: "message", actor_kind: "agent" }),
  ];

  const anchor = resolveInitialUnreadDividerAnchor(
    {
      unread: true,
      marked_unread: false,
      last_read_message_sequence: 2,
      latest_message_sequence: 4,
    },
    events,
  );

  assert.equal(anchor, 2);

  const placement = resolveUnreadDividerPlacement(makeTimelineItems(events), anchor);
  assert.deepEqual(placement, {
    targetItemKey: "e3",
    targetEventID: "e3",
    targetIndex: 2,
    unreadCount: 2,
  });
});

test("全部已读或全新从未读过的卡片不误显示未读分隔符", () => {
  const events = [
    makeEvent({ id: "e1", sequence: 1, type: "comment", actor_kind: "human" }),
    makeEvent({ id: "e2", sequence: 2, type: "message", actor_kind: "agent" }),
  ];

  assert.equal(
    resolveInitialUnreadDividerAnchor(
      {
        unread: false,
        marked_unread: false,
        last_read_message_sequence: 2,
        latest_message_sequence: 2,
      },
      events,
    ),
    null,
  );

  assert.equal(
    resolveInitialUnreadDividerAnchor(
      {
        unread: true,
        marked_unread: false,
        last_read_message_sequence: 0,
        latest_message_sequence: 2,
      },
      events,
    ),
    null,
  );
});

test("手动标为未读且没有更晚新消息时不伪造错误分隔线", () => {
  const events = [
    makeEvent({ id: "e1", sequence: 1, type: "comment", actor_kind: "human" }),
    makeEvent({ id: "e2", sequence: 2, type: "message", actor_kind: "agent" }),
  ];

  const anchor = resolveInitialUnreadDividerAnchor(
    {
      unread: true,
      marked_unread: true,
      last_read_message_sequence: 2,
      latest_message_sequence: 2,
    },
    events,
  );

  assert.equal(anchor, null);
});

test("非消息系统事件不推进可见消息游标也不触发分隔符偏移", () => {
  const events = [
    makeEvent({ id: "e1", sequence: 1, type: "comment", actor_kind: "human" }),
    makeEvent({ id: "e2", sequence: 2, type: "status_changed", actor_kind: "system" }),
    makeEvent({ id: "e3", sequence: 3, type: "message", actor_kind: "agent" }),
    makeEvent({ id: "e4", sequence: 4, type: "checkin", actor_kind: "agent" }),
    makeEvent({ id: "e5", sequence: 5, type: "message", actor_kind: "agent" }),
  ];

  assert.equal(computeLatestVisibleMessageSequence(events), 5);

  const placement = resolveUnreadDividerPlacement(makeTimelineItems(events), 1);
  assert.deepEqual(placement, {
    targetItemKey: "e3",
    targetEventID: "e3",
    targetIndex: 2,
    unreadCount: 2,
  });
});

test("分页加载只剩未读且上方存在更早历史时允许在首条未读上方展示分隔符", () => {
  const events = [
    makeEvent({ id: "e10", sequence: 10, type: "message", actor_kind: "agent" }),
    makeEvent({ id: "e11", sequence: 11, type: "message", actor_kind: "agent" }),
  ];

  assert.equal(
    resolveUnreadDividerPlacement(makeTimelineItems(events), 8, false),
    null,
  );
  assert.deepEqual(
    resolveUnreadDividerPlacement(makeTimelineItems(events), 8, true),
    {
      targetItemKey: "e10",
      targetEventID: "e10",
      targetIndex: 0,
      unreadCount: 2,
    },
  );
});

test("停留期间翻看历史或后台切回收到新回复时记录一次水位线且不跳动", () => {
  const eventsAfterAgentReply = [
    makeEvent({ id: "e1", sequence: 1, type: "comment", actor_kind: "human" }),
    makeEvent({ id: "e2", sequence: 2, type: "message", actor_kind: "agent" }),
    makeEvent({ id: "e3", sequence: 3, type: "message", actor_kind: "agent" }),
  ];

  const firstIncoming = resolveIncomingUnreadDividerAnchor({
    currentAnchor: null,
    previousReadSequence: 2,
    events: eventsAfterAgentReply,
  });
  assert.equal(firstIncoming, 2);

  // 后续再来第 4 条回复时，已有锚点保持在 2 不跳动
  const secondIncoming = resolveIncomingUnreadDividerAnchor({
    currentAnchor: firstIncoming,
    previousReadSequence: 3,
    events: [
      ...eventsAfterAgentReply,
      makeEvent({ id: "e4", sequence: 4, type: "message", actor_kind: "agent" }),
    ],
  });
  assert.equal(secondIncoming, 2);

  // 自己发的消息不会触发新未读分隔符
  const ownMessageOnly = resolveIncomingUnreadDividerAnchor({
    currentAnchor: null,
    previousReadSequence: 2,
    events: [
      makeEvent({ id: "e1", sequence: 1, type: "comment", actor_kind: "human" }),
      makeEvent({ id: "e2", sequence: 2, type: "message", actor_kind: "agent" }),
      makeEvent({ id: "e3", sequence: 3, type: "comment", actor_kind: "human" }),
    ],
  });
  assert.equal(ownMessageOnly, null);
});

test("合并单卡详情与列表缓存时优先保留更新的未读快照", () => {
  const merged = mergeCardReadSnapshots(
    {
      unread: false,
      marked_unread: false,
      last_read_message_sequence: 3,
      latest_message_sequence: 3,
    },
    {
      unread: true,
      marked_unread: false,
      last_read_message_sequence: 3,
      latest_message_sequence: 5,
    },
  );
  assert.equal(merged?.unread, true);
  assert.equal(merged?.latest_message_sequence, 5);
  assert.equal(merged?.last_read_message_sequence, 3);
});

test("停留期间自动已读防重入：同一消息序号不重复发 POST /read，新消息到达或后台首读后再推进", () => {
  // 首次进入已以 seq=2 发过一次 POST /read：后续缓存刷新或详情重渲染（即使旧 fixture 仍带 unread: true）都不得重复发
  assert.equal(
    shouldTriggerStayAutoRead({
      lastMarkedSequence: 2,
      effectiveLatestSequence: 2,
      suppressed: false,
    }),
    false,
  );
  // 即使是空时间线（seq=0）首次已标记过，也不得重复触发
  assert.equal(
    shouldTriggerStayAutoRead({
      lastMarkedSequence: 0,
      effectiveLatestSequence: 0,
      suppressed: false,
    }),
    false,
  );
  // 进入时处于后台隐藏未发送过（lastMarkedSequence = -1），切回前台后补发一次
  assert.equal(
    shouldTriggerStayAutoRead({
      lastMarkedSequence: -1,
      effectiveLatestSequence: 2,
      suppressed: false,
    }),
    true,
  );
  // 停留期间来了新消息（seq 推进到 3），再次触发已读推进
  assert.equal(
    shouldTriggerStayAutoRead({
      lastMarkedSequence: 2,
      effectiveLatestSequence: 3,
      suppressed: false,
    }),
    true,
  );
  // 手动标未读处于抑制状态时，一律不自动触发
  assert.equal(
    shouldTriggerStayAutoRead({
      lastMarkedSequence: -1,
      effectiveLatestSequence: 2,
      suppressed: true,
    }),
    false,
  );
  assert.equal(
    shouldTriggerStayAutoRead({
      lastMarkedSequence: 2,
      effectiveLatestSequence: 3,
      suppressed: true,
    }),
    false,
  );
});
