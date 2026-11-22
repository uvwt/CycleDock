import type { Card, CardKind } from "../api/types.ts";

// 卡片状态点：answering 绿色呼吸点、approval 橙色点、critical 红色点、warning 琥珀黄点、unread 蓝点。
// 同一张卡片同时满足多种状态时，按 实时运行 > 等待人工确认 > 巡检异常 > 巡检需留意 > 未读 优先展示。
export type CardVisualStatus =
  | "answering"
  | "approval"
  | "critical"
  | "warning"
  | "unread"
  | "idle";

// 焦点卡片的入选原因，按用户需要关注的紧急程度从高到低排列：
// 1. approval: 工具调用被安全守卫挂起，Agent 停在原地，只有人能放行或拒绝（穿透静音）
// 2. critical: 定时任务最新巡检为异常且未确认/未归档（穿透静音）
// 3. warning: 定时任务最新巡检为需留意且未确认/未归档（穿透静音）
// 4. answering: Agent 正在运行或排队执行中
// 5. unread: 有尚未阅读的新回复
// 6. review: Todo 已由 Agent 推进到待人工验收
// 7. working: Todo 处于进行中
// 8. pinned: 用户主动置顶的常驻卡片（未归档，穿透静音，便于随时在焦点列表中直达）
export type CardFocusReason =
  | "approval"
  | "critical"
  | "warning"
  | "answering"
  | "unread"
  | "review"
  | "working"
  | "pinned";

export interface FocusSummary {
  cards: Card[];
  approvalCount: number;
  criticalCount: number;
  warningCount: number;
  answeringCount: number;
  unreadCount: number;
  reviewCount: number;
  workingCount: number;
  pinnedCount: number;
  highestVisualStatus: CardVisualStatus;
}

type FocusCardFields = Pick<
  Card,
  | "kind"
  | "discussion_state"
  | "ongoing_state"
  | "health"
  | "current_attempt_status"
  | "marked_unread"
  | "unread"
  | "pending_tool_approval_count"
  | "notifications_muted"
  | "pinned_at"
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

// 定时任务巡检报出异常（critical）或需留意（warning）且未归档时，需持续在焦点中提示；
// 人工点击「已确认」（health 变为 acknowledged）或下次巡检恢复 healthy 后自动退出。
function getOngoingAlertReason(
  card: FocusCardFields,
): "critical" | "warning" | null {
  if (card.kind !== "ongoing" || isArchived(card)) {
    return null;
  }
  if (card.health === "critical") return "critical";
  if (card.health === "warning") return "warning";
  return null;
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

  // 定时任务的未确认巡检异常/需留意同理：只有显式确认或恢复正常后才消退。
  const ongoingAlert = getOngoingAlertReason(card);
  if (ongoingAlert) {
    return ongoingAlert;
  }

  // unread 由后端基于 message sequence + Human Source read cursor + marked_unread 计算；
  // 普通新消息在正在查看当前卡片时抑制蓝点，但若用户显式手动「标为未读」（marked_unread === true），
  // 即使当前仍停留在该卡片会话中，也必须保留未读蓝点与未读提示。
  if (card.marked_unread || (!isActive && card.unread)) {
    return "unread";
  }

  return "idle";
}

export function getCardFocusReason(
  card: FocusCardFields & Pick<Card, "todo_state">,
  isActive: boolean = false,
  activeAttemptStatus?: string,
): CardFocusReason | null {
  // 即使后台又有新 Turn 在运行或卡片已静音，挂起的安全守卫审批仍需人处理，所以优先豁免。
  if (hasPendingApproval(card)) {
    return "approval";
  }

  // 定时任务巡检异常/需留意同样穿透静音，避免免打扰巡检卡真出问题时被静默漏看。
  const ongoingAlert = getOngoingAlertReason(card);
  if (ongoingAlert) {
    return ongoingAlert;
  }

  // 已开启静音（关闭消息推送）的卡片视为免打扰，除待人工审批与定时巡检异常/需留意豁免外不再进入全局焦点。
  // 若卡片已被用户主动置顶且未归档，仍保留 pinned 焦点入口，避免置顶项从焦点列表消失。
  if (card.notifications_muted) {
    return card.pinned_at && !isArchived(card) ? "pinned" : null;
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
  // 用户主动置顶的未归档卡片固定进入焦点列表，方便跨类型快速访问。
  if (card.pinned_at) {
    return "pinned";
  }
  return null;
}

const focusReasonPriority: Record<CardFocusReason, number> = {
  approval: 0,
  critical: 1,
  warning: 2,
  answering: 3,
  unread: 4,
  review: 5,
  working: 6,
  pinned: 7,
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
    critical: 0,
    warning: 0,
    answering: 0,
    unread: 0,
    review: 0,
    working: 0,
    pinned: 0,
  };

  for (const card of cards) {
    const isActive = Boolean(activeCardID && card.id === activeCardID);
    const reason = getCardFocusReason(card, isActive, activeAttemptStatus);
    if (!reason) continue;

    focusCards.push({ card, reason });
    counts[reason]++;
  }

  // 置顶卡片固定排在焦点列表最上方，避免被新消息或状态变化打乱常驻入口位置：
  // 1. 已置顶始终优先于未置顶；同为已置顶时按 pinned_at 倒序保持稳定顺序（相同时按 updated_at 倒序兜底）
  // 2. 未置顶卡片保持原有规则：按紧急程度（approval > critical > warning > answering > unread > review > working）与 updated_at 倒序排列
  focusCards.sort((a, b) => {
    const aPinned = Boolean(a.card.pinned_at);
    const bPinned = Boolean(b.card.pinned_at);
    if (aPinned !== bPinned) return aPinned ? -1 : 1;
    if (aPinned && bPinned) {
      const pinDiff = (b.card.pinned_at || "").localeCompare(
        a.card.pinned_at || "",
      );
      if (pinDiff !== 0) return pinDiff;
      return b.card.updated_at.localeCompare(a.card.updated_at);
    }

    const priDiff =
      focusReasonPriority[a.reason] - focusReasonPriority[b.reason];
    if (priDiff !== 0) return priDiff;

    return b.card.updated_at.localeCompare(a.card.updated_at);
  });

  const highestVisualStatus: CardVisualStatus =
    counts.answering > 0
      ? "answering"
      : counts.approval > 0
        ? "approval"
        : counts.critical > 0
          ? "critical"
          : counts.warning > 0
            ? "warning"
            : counts.unread > 0
              ? "unread"
              : "idle";

  return {
    cards: focusCards.map((item) => item.card),
    approvalCount: counts.approval,
    criticalCount: counts.critical,
    warningCount: counts.warning,
    answeringCount: counts.answering,
    unreadCount: counts.unread,
    reviewCount: counts.review,
    workingCount: counts.working,
    pinnedCount: counts.pinned,
    highestVisualStatus,
  };
}

/**
 * 计算某个一级导航分类（讨论 / 任务 / 定时）下的最高视觉状态，用于在导航图标右上角展示呼吸绿点、待确认橙点、异常红点、需留意黄点或未读蓝点。
 * 与焦点规则保持一致：已静音卡片忽略运行中（绿点）与未读（蓝点），仅在存在待人工确认审批或定时巡检异常/需留意时触发对应角标。
 */
export function getKindVisualStatus(
  cards: Card[],
  kind: CardKind,
  activeCardID?: string,
  activeAttemptStatus?: string,
): CardVisualStatus {
  let hasApproval = false;
  let hasCritical = false;
  let hasWarning = false;
  let hasUnread = false;
  for (const card of cards) {
    if (card.kind !== kind) continue;
    if (card.notifications_muted) {
      // 静音卡片同时在运行新 Turn 时，getCardVisualStatus 会返回 answering；
      // 这里直接检查硬豁免项（待审批、巡检异常/需留意），避免被运行态吞掉。
      if (hasPendingApproval(card)) hasApproval = true;
      const alert = getOngoingAlertReason(card);
      if (alert === "critical") hasCritical = true;
      else if (alert === "warning") hasWarning = true;
      continue;
    }
    const isActive = Boolean(activeCardID && card.id === activeCardID);
    const status = getCardVisualStatus(card, isActive, activeAttemptStatus);
    if (status === "answering") return "answering";
    if (status === "approval") hasApproval = true;
    else if (status === "critical") hasCritical = true;
    else if (status === "warning") hasWarning = true;
    else if (!isArchived(card) && status === "unread") hasUnread = true;
  }
  if (hasApproval) return "approval";
  if (hasCritical) return "critical";
  if (hasWarning) return "warning";
  return hasUnread ? "unread" : "idle";
}
