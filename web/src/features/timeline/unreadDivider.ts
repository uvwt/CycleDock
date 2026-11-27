import type { Card, TimelineEvent } from "../../shared/api/types.ts";
import type { TimelineItem, TimelineRowLike } from "./execution.ts";

export type CardReadSnapshot = Pick<
  Card,
  "unread" | "marked_unread" | "last_read_message_sequence" | "latest_message_sequence"
>;

export type UnreadDividerPlacement = {
  /** 首个未读项在 timelineItems 中的 key，用于在它正上方渲染分隔符。 */
  targetItemKey: string;
  /** 首个未读事件的 id，方便高亮或测试断言定位。 */
  targetEventID: string;
  /** 首个未读项在 timelineItems 中的索引。 */
  targetIndex: number;
  /** 本次水位线之后的可读未读消息条数。 */
  unreadCount: number;
};

/**
 * 与后端 MarkCardRead / message_state 对齐：
 * 只有 comment 与 message 两类可见对话气泡推进已读消息游标并计入未读消息数。
 */
export function isReadCursorMessageEvent(
  event: Pick<TimelineEvent, "type" | "sequence">,
): boolean {
  return (
    (event.type === "comment" || event.type === "message") &&
    Number.isFinite(event.sequence) &&
    event.sequence > 0
  );
}

/**
 * 计算时间线中最新一条计入已读游标的可见消息序号。
 */
export function computeLatestVisibleMessageSequence(
  events: ReadonlyArray<Pick<TimelineEvent, "type" | "sequence">>,
): number {
  let latest = 0;
  for (const event of events) {
    if (!isReadCursorMessageEvent(event)) continue;
    if (event.sequence > latest) {
      latest = event.sequence;
    }
  }
  return latest;
}

/**
 * 合并单卡详情与列表缓存中的已读元数据：
 * 优先采用携带完整已读游标且消息序号更鲜活的一方，避免切卡瞬间被尚未刷新的旧缓存掩盖未读状态。
 */
export function mergeCardReadSnapshots(
  detailCard?: CardReadSnapshot | null,
  cachedCard?: CardReadSnapshot | null,
): CardReadSnapshot | null {
  const detailHasReadMeta =
    detailCard != null &&
    (typeof detailCard.last_read_message_sequence === "number" ||
      typeof detailCard.unread === "boolean");
  const cachedHasReadMeta =
    cachedCard != null &&
    (typeof cachedCard.last_read_message_sequence === "number" ||
      typeof cachedCard.unread === "boolean");

  if (!detailHasReadMeta && !cachedHasReadMeta) {
    return null;
  }
  if (!detailHasReadMeta) {
    return cachedCard ?? null;
  }
  if (!cachedHasReadMeta) {
    return detailCard ?? null;
  }

  const detailLatest = detailCard.latest_message_sequence ?? 0;
  const cachedLatest = cachedCard.latest_message_sequence ?? 0;
  if (cachedLatest > detailLatest) {
    return cachedCard;
  }
  if (detailLatest > cachedLatest) {
    return detailCard;
  }
  // 两者最新消息序号相同时，若列表缓存已知处于未读且具有有效水位线，优先保留未读视图。
  if (
    cachedCard.unread &&
    !detailCard.unread &&
    (cachedCard.last_read_message_sequence ?? 0) > 0
  ) {
    return cachedCard;
  }
  return detailCard;
}

/**
 * 计算进入卡片 / 打开浮窗瞬间应锁定的「未读消息分隔符水位线」。
 *
 * 规则：
 * 1. 卡片进入时必须处于未读状态（unread === true）；
 * 2. 必须存在有效的历史已读水位线（last_read_message_sequence > 0）：
 *    全新从未读过的卡片首次打开从头阅读，不在顶部误画分隔符；
 * 3. 必须存在真实晚于已读水位线的消息（effectiveLatest > lastRead）：
 *    用户手动标未读（marked_unread === true）但没有更新消息时，后端保留原游标不变，
 *    此时不伪造一条错误的「以下为未读消息」边界。
 */
export function resolveInitialUnreadDividerAnchor(
  card: CardReadSnapshot | null | undefined,
  timelineEvents: ReadonlyArray<Pick<TimelineEvent, "type" | "sequence">> = [],
): number | null {
  if (!card?.unread) return null;
  const lastRead = card.last_read_message_sequence;
  if (typeof lastRead !== "number" || !Number.isFinite(lastRead) || lastRead <= 0) {
    return null;
  }
  const effectiveLatest = Math.max(
    card.latest_message_sequence ?? 0,
    computeLatestVisibleMessageSequence(timelineEvents),
  );
  if (effectiveLatest <= lastRead) {
    return null;
  }
  return lastRead;
}

/**
 * 当用户停留在当前卡片期间（向上翻看历史未在底部，或切出标签页到后台后再切回），
 * 若收到非当前人类发送的新消息且尚未设置未读分隔符，则锁定新消息到达前的游标作为水位线；
 * 已有水位线时保持原地不动，避免连续到达多条新消息时分隔符跳动闪烁。
 */
export function resolveIncomingUnreadDividerAnchor(params: {
  currentAnchor: number | null;
  previousReadSequence: number;
  events: ReadonlyArray<Pick<TimelineEvent, "type" | "sequence" | "actor_kind">>;
}): number | null {
  const { currentAnchor, previousReadSequence, events } = params;
  if (currentAnchor !== null) {
    return currentAnchor;
  }
  if (!Number.isFinite(previousReadSequence) || previousReadSequence <= 0) {
    return null;
  }
  const hasNewIncomingMessage = events.some(
    (event) =>
      isReadCursorMessageEvent(event) &&
      event.actor_kind !== "human" &&
      event.sequence > previousReadSequence,
  );
  return hasNewIncomingMessage ? previousReadSequence : null;
}

/**
 * 在最终待渲染的 timelineItems 中计算未读分隔符的落位与未读消息计数。
 *
 * 规则：
 * - 分隔符落在第一条 sequence > anchorSequence 的可见对话消息正上方；
 * - 若不存在任何 sequence > anchorSequence 的消息事件，返回 null；
 * - 若首个未读消息就是当前时间线第 0 条且上方不存在未加载历史（!hasMoreBefore），
 *   说明上方没有已读消息可区分，同样不展示顶置分隔符。
 */
export function resolveUnreadDividerPlacement<TRow extends TimelineRowLike>(
  timelineItems: ReadonlyArray<TimelineItem<TRow>>,
  anchorSequence: number | null | undefined,
  hasMoreBefore: boolean = false,
): UnreadDividerPlacement | null {
  if (
    typeof anchorSequence !== "number" ||
    !Number.isFinite(anchorSequence) ||
    anchorSequence <= 0
  ) {
    return null;
  }

  let firstTarget: {
    key: string;
    eventID: string;
    index: number;
  } | null = null;
  let unreadCount = 0;

  for (let index = 0; index < timelineItems.length; index++) {
    const item = timelineItems[index];
    if (item.kind !== "event") continue;
    const event = item.row.event;
    if (!isReadCursorMessageEvent(event)) continue;
    if (event.sequence <= anchorSequence) continue;

    unreadCount += 1;
    if (!firstTarget) {
      firstTarget = {
        key: item.key,
        eventID: event.id,
        index,
      };
    }
  }

  if (!firstTarget || unreadCount <= 0) {
    return null;
  }

  // 上方完全没有已读消息也没有更多历史分页时，不在顶部硬塞分隔符。
  const hasReadEventAbove = timelineItems
    .slice(0, firstTarget.index)
    .some(
      (item) =>
        item.kind === "event" &&
        item.row.event.sequence > 0 &&
        item.row.event.sequence <= anchorSequence,
    );
  if (!hasReadEventAbove && !hasMoreBefore) {
    return null;
  }

  return {
    targetItemKey: firstTarget.key,
    targetEventID: firstTarget.eventID,
    targetIndex: firstTarget.index,
    unreadCount,
  };
}

/**
 * 判断处于停留态（已完成首次进入初始化）的卡片是否需要再次触发自动已读。
 *
 * 关键不变量：
 * 1. 同一消息水位线（effectiveLatestSequence <= lastMarkedSequence）绝不重复发送 POST /read：
 *    即使列表缓存 / 详情接口返回了尚未刷新的旧 unread: true，也不应触发二次或多次请求；
 * 2. 只有首次进入尚未标过（lastMarkedSequence < 0，例如进入时处于后台隐藏或刚被手动标未读抑制）
 *    或者可见消息序号严格推进（effectiveLatestSequence > lastMarkedSequence）时，才再次标已读。
 */
export function shouldTriggerStayAutoRead(params: {
  lastMarkedSequence: number;
  effectiveLatestSequence: number;
  suppressed: boolean;
}): boolean {
  const { lastMarkedSequence, effectiveLatestSequence, suppressed } = params;
  if (suppressed) {
    return false;
  }
  return lastMarkedSequence < 0 || effectiveLatestSequence > lastMarkedSequence;
}
