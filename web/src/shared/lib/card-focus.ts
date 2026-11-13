import type { Card, CardKind } from "../api/types.ts";

// 卡片状态点：answering 绿色呼吸点、approval 橙色点、unread 蓝点。
// 同一张卡片同时满足多种状态时，实时运行优先展示，其次是等待人工确认，最后是未读。
export type CardVisualStatus = "answering" | "approval" | "unread" | "idle";

// 焦点卡片的入选原因，按用户需要关注的紧急程度从高到低排列：
// 1. approval: 工具调用被安全守卫挂起，Agent 停在原地，只有人能放行或拒绝
// 2. answering: Agent 正在运行或排队执行中
// 3. unread: 有尚未阅读的新回复
// 4. review: Todo 已由 Agent 推进到待人工验收
// 5. working: Todo 处于进行中
export type CardFocusReason =
  "approval" | "answering" | "unread" | "review" | "working";

export interface FocusSummary {
  cards: Card[];
  approvalCount: number;
  answeringCount: number;
  unreadCount: number;
  reviewCount: number;
  workingCount: number;
  highestVisualStatus: CardVisualStatus;
}

type FocusCardFields = Pick<
  Card,
  | "kind"
  | "discussion_state"
  | "ongoing_state"
  | "current_attempt_status"
  | "unread"
  | "pending_tool_approval_count"
>;

function isArchived(
  card: Pick<Card, "kind" | "discussion_state" | "ongoing_state">,
): boolean {
  return (
    (card.kind === "discussion" && card.discussion_state === "archived") ||
    (card.kind === "ongoing" && card.ongoing_state === "archived")
  );
}

// 挂起审批时 Attempt 已经结束，current_attempt_status 为空；
// 只能依赖后端列表摘要统计的未处理审批数量判断“等待人工确认”。
function hasPendingApproval(card: FocusCardFields): boolean {
  return (card.pending_tool_approval_count ?? 0) > 0 && !isArchived(card);
}

export function getCardVisualStatus(
  card: FocusCardFields,
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

  // 等待确认是需要人处理的阻塞状态，正在查看该卡片时也不能被抑制。
  if (hasPendingApproval(card)) {
    return "approval";
  }

  // unread 由后端基于 message sequence + Human Source read cursor 计算；
  // 标题、置顶、状态变化不会推进这个游标。
  if (!isActive && card.unread) {
    return "unread";
  }

  return "idle";
}

export function getCardFocusReason(
  card: FocusCardFields & Pick<Card, "todo_state">,
  isActive: boolean = false,
  activeAttemptStatus?: string,
): CardFocusReason | null {
  // 即使后台又有新 Turn 在运行，旧的待确认审批仍需人处理，所以先于运行状态判断。
  if (hasPendingApproval(card)) {
    return "approval";
  }

  // 已归档的讨论或定时任务不再进入日常焦点流，除非其仍有正在运行的 Attempt
  const visual = getCardVisualStatus(card, isActive, activeAttemptStatus);
  if (visual === "answering") {
    return "answering";
  }
  if (isArchived(card)) {
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
  approval: 0,
  answering: 1,
  unread: 2,
  review: 3,
  working: 4,
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
  const counts: Record<CardFocusReason, number> = {
    approval: 0,
    answering: 0,
    unread: 0,
    review: 0,
    working: 0,
  };

  for (const card of cards) {
    const isActive = Boolean(activeCardID && card.id === activeCardID);
    const reason = getCardFocusReason(card, isActive, activeAttemptStatus);
    if (!reason) continue;

    focusCards.push({ card, reason });
    counts[reason]++;
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
    counts.answering > 0
      ? "answering"
      : counts.approval > 0
        ? "approval"
        : counts.unread > 0
          ? "unread"
          : "idle";

  return {
    cards: focusCards.map((item) => item.card),
    approvalCount: counts.approval,
    answeringCount: counts.answering,
    unreadCount: counts.unread,
    reviewCount: counts.review,
    workingCount: counts.working,
    highestVisualStatus,
  };
}

/**
 * 计算某个一级导航分类（讨论 / 任务 / 定时）下的最高视觉状态，用于在导航图标右上角展示呼吸绿点、待确认橙点或未读蓝点。
 */
export function getKindVisualStatus(
  cards: Card[],
  kind: CardKind,
  activeCardID?: string,
  activeAttemptStatus?: string,
): CardVisualStatus {
  let hasApproval = false;
  let hasUnread = false;
  for (const card of cards) {
    if (card.kind !== kind) continue;
    const isActive = Boolean(activeCardID && card.id === activeCardID);
    const status = getCardVisualStatus(card, isActive, activeAttemptStatus);
    if (status === "answering") return "answering";
    if (status === "approval") hasApproval = true;
    if (!isArchived(card) && status === "unread") hasUnread = true;
  }
  if (hasApproval) return "approval";
  return hasUnread ? "unread" : "idle";
}
