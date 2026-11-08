import type { Card, CardKind } from "../api/types.ts";

export type CardVisualStatus = "answering" | "unread" | "idle";

// 焦点卡片的入选原因，按用户需要关注的紧急程度从高到低排列：
// 1. answering: Agent 正在运行或排队执行中
// 2. unread: 有尚未阅读的新回复
// 3. review: Todo 已由 Agent 推进到待人工验收
// 4. working: Todo 处于进行中
export type CardFocusReason = "answering" | "unread" | "review" | "working";

export interface FocusSummary {
  cards: Card[];
  answeringCount: number;
  unreadCount: number;
  reviewCount: number;
  workingCount: number;
  highestVisualStatus: CardVisualStatus;
}

export function getCardVisualStatus(
  card: Pick<Card, "current_attempt_status" | "unread">,
  isActive: boolean = false,
  activeAttemptStatus?: string,
): CardVisualStatus {
  if (
    (isActive &&
      (activeAttemptStatus === "running" ||
        activeAttemptStatus === "queued")) ||
    card.current_attempt_status === "running" ||
    card.current_attempt_status === "queued"
  ) {
    return "answering";
  }

  // unread 由后端基于 message sequence + Human Source read cursor 计算；
  // 标题、置顶、状态变化不会推进这个游标。
  if (!isActive && card.unread) {
    return "unread";
  }

  return "idle";
}

export function getCardFocusReason(
  card: Pick<
    Card,
    | "kind"
    | "todo_state"
    | "discussion_state"
    | "ongoing_state"
    | "current_attempt_status"
    | "unread"
  >,
  isActive: boolean = false,
  activeAttemptStatus?: string,
): CardFocusReason | null {
  // 已归档的讨论或定时任务不再进入日常焦点流，除非其仍有正在运行的 Attempt
  const visual = getCardVisualStatus(card, isActive, activeAttemptStatus);
  if (visual === "answering") {
    return "answering";
  }

  const archived =
    (card.kind === "discussion" && card.discussion_state === "archived") ||
    (card.kind === "ongoing" && card.ongoing_state === "archived");
  if (archived) {
    return null;
  }

  if (visual === "unread") {
    return "unread";
  }
  if (card.kind === "todo" && card.todo_state === "review") {
    return "review";
  }
  if (card.kind === "todo" && card.todo_state === "working") {
    return "working";
  }
  return null;
}

const focusReasonPriority: Record<CardFocusReason, number> = {
  answering: 0,
  unread: 1,
  review: 2,
  working: 3,
};

/**
 * 聚合跨类型（讨论 / 任务 / 定时）的焦点卡片与统计计数。
 * 统一服务首页焦点面板、⌘K/底部上滑快速切换抽屉以及角标计数，避免各视图各自维护平行筛选规则。
 */
export function buildFocusSummary(
  cards: Card[],
  activeCardID?: string,
  activeAttemptStatus?: string,
): FocusSummary {
  const focusCards: { card: Card; reason: CardFocusReason }[] = [];
  let answeringCount = 0;
  let unreadCount = 0;
  let reviewCount = 0;
  let workingCount = 0;

  for (const card of cards) {
    const isActive = Boolean(activeCardID && card.id === activeCardID);
    const reason = getCardFocusReason(card, isActive, activeAttemptStatus);
    if (!reason) continue;

    focusCards.push({ card, reason });
    if (reason === "answering") answeringCount++;
    else if (reason === "unread") unreadCount++;
    else if (reason === "review") reviewCount++;
    else if (reason === "working") workingCount++;
  }

  focusCards.sort((a, b) => {
    const priDiff =
      focusReasonPriority[a.reason] - focusReasonPriority[b.reason];
    if (priDiff !== 0) return priDiff;

    const aPinned = Boolean(a.card.pinned_at);
    const bPinned = Boolean(b.card.pinned_at);
    if (aPinned !== bPinned) return aPinned ? -1 : 1;
    if (aPinned && bPinned) {
      return (b.card.pinned_at || "").localeCompare(a.card.pinned_at || "");
    }
    return b.card.updated_at.localeCompare(a.card.updated_at);
  });

  const highestVisualStatus: CardVisualStatus =
    answeringCount > 0 ? "answering" : unreadCount > 0 ? "unread" : "idle";

  return {
    cards: focusCards.map((item) => item.card),
    answeringCount,
    unreadCount,
    reviewCount,
    workingCount,
    highestVisualStatus,
  };
}

/**
 * 计算某个一级导航分类（讨论 / 任务 / 定时）下的最高视觉状态，用于在导航图标右上角展示呼吸绿点或未读蓝点。
 */
export function getKindVisualStatus(
  cards: Card[],
  kind: CardKind,
  activeCardID?: string,
  activeAttemptStatus?: string,
): CardVisualStatus {
  let hasUnread = false;
  for (const card of cards) {
    if (card.kind !== kind) continue;
    const isActive = Boolean(activeCardID && card.id === activeCardID);
    const status = getCardVisualStatus(card, isActive, activeAttemptStatus);
    if (status === "answering") return "answering";
    const archived =
      (card.kind === "discussion" && card.discussion_state === "archived") ||
      (card.kind === "ongoing" && card.ongoing_state === "archived");
    if (!archived && status === "unread") hasUnread = true;
  }
  return hasUnread ? "unread" : "idle";
}
