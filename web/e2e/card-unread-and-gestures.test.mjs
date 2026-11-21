import test from "node:test";
import assert from "node:assert/strict";
import {
  createAutoReadSuppressionStore,
  isCardUnread,
} from "../src/shared/lib/card-read-state.ts";
import {
  buildFocusSummary,
  getCardFocusReason,
  getCardVisualStatus,
} from "../src/shared/lib/card-focus.ts";
import { createLongPressMachine } from "../src/shared/lib/swipe-long-press.ts";
import {
  cardNotificationBody,
  hasCardSnapshotChanged,
} from "../src/shared/realtime/cardUpdates.ts";

test("isCardUnread 与视觉红点：手动标为未读时即使在当前激活卡片也展示未读状态", () => {
  const normalUnreadCard = {
    kind: "discussion",
    discussion_state: "active",
    unread: true,
    marked_unread: false,
  };
  assert.equal(isCardUnread(normalUnreadCard, false), true);
  assert.equal(isCardUnread(normalUnreadCard, true), false);
  assert.equal(getCardVisualStatus(normalUnreadCard, false), "unread");
  assert.equal(getCardVisualStatus(normalUnreadCard, true), "idle");

  const manuallyUnreadEmptyCard = {
    kind: "todo",
    todo_state: "todo",
    unread: true,
    marked_unread: true,
  };
  assert.equal(isCardUnread(manuallyUnreadEmptyCard, false), true);
  assert.equal(isCardUnread(manuallyUnreadEmptyCard, true), true);
  assert.equal(getCardVisualStatus(manuallyUnreadEmptyCard, false), "unread");
  assert.equal(getCardVisualStatus(manuallyUnreadEmptyCard, true), "unread");
  assert.equal(getCardFocusReason(manuallyUnreadEmptyCard, true), "unread");

  const summary = buildFocusSummary(
    [
      {
        id: "card-active-unread",
        project_id: "proj-1",
        title: "当前卡片手动未读",
        description: "",
        kind: "discussion",
        discussion_state: "active",
        unread: true,
        marked_unread: true,
        created_at: "2026-04-17T00:00:00Z",
        updated_at: "2026-04-17T00:00:00Z",
      },
    ],
    "card-active-unread",
  );
  assert.equal(summary.unreadCount, 1);
  assert.equal(summary.highestVisualStatus, "unread");
});

test("当前打开卡片标为未读防回弹：基线消息未增长时抑制自动 markRead，新消息或重新进入时解除", () => {
  const store = createAutoReadSuppressionStore();

  assert.equal(store.shouldSuppressAutoRead("card-1", 12), false);

  // 用户在列表中将当前打开的 card-1 手动标为未读（当时最大消息序号为 12）
  store.markSuppressed("card-1", 12);
  assert.equal(store.shouldSuppressAutoRead("card-1", 12), true);
  assert.equal(store.shouldSuppressAutoRead("card-1", 10), true);

  // 收到或发送新消息（sequence 推进到 13），自动解除抑制并允许推进已读游标
  assert.equal(store.shouldSuppressAutoRead("card-1", 13), false);
  assert.equal(store.shouldSuppressAutoRead("card-1", 13), false);

  // 再次手动标未读后，切换到其他卡片（card-2）应自动清理旧卡片抑制标记
  store.markSuppressed("card-1", 13);
  assert.equal(store.shouldSuppressAutoRead("card-1", 13), true);
  store.clearAllExcept("card-2");
  assert.equal(store.shouldSuppressAutoRead("card-1", 13), false);

  // 手动点击「标为已读」或重新点击该卡片进入，显式清除抑制
  store.markSuppressed("card-2", 0);
  assert.equal(store.shouldSuppressAutoRead("card-2", 0), true);
  store.clearSuppressed("card-2");
  assert.equal(store.shouldSuppressAutoRead("card-2", 0), false);
});

test("SSE 多端已读/未读同步：read_revision 变化刷新缓存但不误弹新消息通知", () => {
  const prev = {
    id: "card-1",
    project_id: "proj-1",
    kind: "discussion",
    title: "架构讨论",
    revision: "rev-1",
    read_revision: "5:false",
    event_sequence: "5",
    message_preview: "好的",
    attempt_status: "",
  };
  const markedUnreadNext = {
    ...prev,
    read_revision: "5:true",
  };
  assert.equal(hasCardSnapshotChanged(markedUnreadNext, prev), true);
  assert.equal(hasCardSnapshotChanged(markedUnreadNext, markedUnreadNext), false);
  // event_sequence 未变，绝不弹出桌面/气泡新消息通知
  assert.equal(cardNotificationBody(markedUnreadNext, prev), "");
});

test("长按与左滑/滚动手势防冲突：按住满阈值触发且拦截误点击，滑动超容差或抽屉已展开时取消", () => {
  let scheduledCallback = null;
  let scheduledDelay = 0;
  let clearedCount = 0;
  let longPressFired = 0;

  const machine = createLongPressMachine({
    delayMs: 460,
    moveTolerancePx: 10,
    onLongPress: () => {
      longPressFired += 1;
    },
    setTimer: (cb, ms) => {
      scheduledCallback = cb;
      scheduledDelay = ms;
      return 1;
    },
    clearTimer: () => {
      clearedCount += 1;
      scheduledCallback = null;
    },
  });

  // 1) 正常长按：微小抖动 <= 10px 不取消，满 460ms 触发，并吞掉松手后的 click 与 contextmenu
  machine.start({ x: 100, y: 200 });
  assert.equal(scheduledDelay, 460);
  assert.equal(machine.isPressing(), true);
  assert.equal(machine.shouldPreventContextMenu(), true);
  machine.move({ x: 105, y: 204 }); // hypot(5, 4) = 6.4 <= 10
  assert.equal(machine.isPressing(), true);

  scheduledCallback();
  assert.equal(longPressFired, 1);
  assert.equal(machine.isPressing(), false);
  const endRes = machine.end();
  assert.equal(endRes.wasTriggered, true);
  assert.equal(machine.shouldPreventContextMenu(), true);
  assert.equal(machine.consumeClickSuppression(), true);
  // 消费一次后不影响下一次正常点击
  assert.equal(machine.consumeClickSuppression(), false);

  // 2) 左滑或上下滚动位移超出 10px 时，立即取消长按计时且不拦截正常手势
  machine.start({ x: 200, y: 200 });
  assert.equal(machine.isPressing(), true);
  machine.move({ x: 175, y: 202 }); // 左滑 25px > 10px
  assert.equal(machine.isPressing(), false);
  assert.equal(scheduledCallback, null);
  assert.ok(clearedCount >= 1);
  assert.equal(machine.end().wasTriggered, false);
  assert.equal(longPressFired, 1);
  assert.equal(machine.consumeClickSuppression(), false);

  // 3) 左滑抽屉已处于打开状态（isSwipeOpen=true）时，点击整行用于收起抽屉，不启动长按
  machine.start({ x: 120, y: 120 }, { isSwipeOpen: true });
  assert.equal(machine.isPressing(), false);
  assert.equal(scheduledCallback, null);
});
