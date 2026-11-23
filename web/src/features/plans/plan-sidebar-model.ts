import type { Card, OngoingState, ProjectSchedule } from "../../shared/api/types";
import { isCardUnread } from "../../shared/lib/card-read-state";
import {
  summarizePlanSchedules,
  type ScheduleSummaryInput,
  type TranslateFn,
} from "./schedule-summary";

export type PlanSidebarGroupKey = "attention" | "active" | "muted" | "inactive";

export type PlanSidebarGroup = {
  key: PlanSidebarGroupKey;
  cards: Card[];
};

export type BuildPlanSidebarGroupsOptions = {
  searchQuery?: string;
  activeCardID?: string;
  activeAttemptStatus?: string;
};

export type PlanSidebarSubtitle = {
  /** 主副标题文案（用于单行截断展示） */
  text: string;
  /** 是否存在最近一次执行失败（用于柔和红字提示） */
  hasFailedAttempt: boolean;
  /** 失败提示文案（例如「上次执行失败」） */
  failedLabel: string | null;
};

export type BuildPlanSidebarSubtitleOptions = {
  now?: Date;
  t?: TranslateFn;
};

const identityTranslate: TranslateFn = (key) => key;

function resolvedOngoingState(card: Pick<Card, "ongoing_state">): OngoingState {
  return card.ongoing_state || "active";
}

/**
 * 仅运行中且最新巡检为 critical / warning 的定时卡归入「需关注」。
 * 已暂停、已归档或已人工确认（acknowledged）的卡片不占异常置顶位。
 */
export function isPlanAttentionCard(
  card: Pick<Card, "kind" | "ongoing_state" | "health">,
): boolean {
  if (card.kind !== "ongoing") return false;
  if (resolvedOngoingState(card) !== "active") return false;
  return card.health === "critical" || card.health === "warning";
}

function isPlanRunning(
  card: Pick<Card, "id" | "current_attempt_status">,
  activeCardID?: string,
  activeAttemptStatus?: string,
): boolean {
  if (
    card.current_attempt_status === "running" ||
    card.current_attempt_status === "queued"
  ) {
    return true;
  }
  return (
    Boolean(activeCardID) &&
    card.id === activeCardID &&
    (activeAttemptStatus === "running" || activeAttemptStatus === "queued")
  );
}

export function isPlanCardUnread(
  card: Pick<Card, "id" | "unread" | "marked_unread">,
  activeCardID?: string,
): boolean {
  return isCardUnread(card, Boolean(activeCardID && card.id === activeCardID));
}

export function getUnreadPlanCardIDs(
  cards: readonly Pick<Card, "id" | "unread" | "marked_unread">[],
  activeCardID?: string,
): string[] {
  const ids: string[] = [];
  for (const card of cards) {
    if (isPlanCardUnread(card, activeCardID)) {
      ids.push(card.id);
    }
  }
  return ids;
}

// 正在执行优先于未读，普通未读优先于免打扰未读，最后为空闲。
// 注意：需关注组内 health=critical/warning 会覆盖 getCardVisualStatus 的 unread，
// 因此排序必须直接读取运行态与未读态，避免异常卡丢失未读优先级。
function activityRank(
  card: Card,
  activeCardID?: string,
  activeAttemptStatus?: string,
): number {
  if (isPlanRunning(card, activeCardID, activeAttemptStatus)) return 0;
  if (isPlanCardUnread(card, activeCardID)) {
    return card.notifications_muted ? 2 : 1;
  }
  return 3;
}

function compareTimeDesc(a?: string, b?: string): number {
  const aMs = a ? Date.parse(a) : Number.NaN;
  const bMs = b ? Date.parse(b) : Number.NaN;
  if (!Number.isNaN(aMs) && !Number.isNaN(bMs) && aMs !== bMs) {
    return bMs - aMs;
  }
  return (b || "").localeCompare(a || "");
}

function comparePinnedFirst(a: Card, b: Card): number {
  const aPinned = Boolean(a.pinned_at);
  const bPinned = Boolean(b.pinned_at);
  if (aPinned !== bPinned) return aPinned ? -1 : 1;
  return 0;
}

function compareAttentionCards(
  a: Card,
  b: Card,
  activeCardID?: string,
  activeAttemptStatus?: string,
): number {
  const aSeverity = a.health === "critical" ? 0 : 1;
  const bSeverity = b.health === "critical" ? 0 : 1;
  if (aSeverity !== bSeverity) return aSeverity - bSeverity;

  const pinDiff = comparePinnedFirst(a, b);
  if (pinDiff !== 0) return pinDiff;

  const activityDiff =
    activityRank(a, activeCardID, activeAttemptStatus) -
    activityRank(b, activeCardID, activeAttemptStatus);
  if (activityDiff !== 0) return activityDiff;

  const timeDiff = compareTimeDesc(a.updated_at, b.updated_at);
  if (timeDiff !== 0) return timeDiff;

  return a.id.localeCompare(b.id);
}

function compareActiveCards(
  a: Card,
  b: Card,
  activeCardID?: string,
  activeAttemptStatus?: string,
): number {
  const pinDiff = comparePinnedFirst(a, b);
  if (pinDiff !== 0) return pinDiff;

  const activityDiff =
    activityRank(a, activeCardID, activeAttemptStatus) -
    activityRank(b, activeCardID, activeAttemptStatus);
  if (activityDiff !== 0) return activityDiff;

  const timeDiff = compareTimeDesc(a.updated_at, b.updated_at);
  if (timeDiff !== 0) return timeDiff;

  return a.id.localeCompare(b.id);
}

function compareInactiveCards(a: Card, b: Card): number {
  const aStateRank = resolvedOngoingState(a) === "paused" ? 0 : 1;
  const bStateRank = resolvedOngoingState(b) === "paused" ? 0 : 1;
  if (aStateRank !== bStateRank) return aStateRank - bStateRank;

  const pinDiff = comparePinnedFirst(a, b);
  if (pinDiff !== 0) return pinDiff;

  const timeDiff = compareTimeDesc(a.updated_at, b.updated_at);
  if (timeDiff !== 0) return timeDiff;

  return a.id.localeCompare(b.id);
}

/**
 * 将项目卡片过滤并组织为定时任务分组：
 * 组间固定顺序：需关注 -> 运行中 -> 免打扰 -> 已暂停 / 已归档，空分组自动隐藏。
 *
 * 关键规则：
 * - 巡检异常（critical / warning）穿透免打扰，始终进入「需关注」；
 * - 用户手动置顶的免打扰定时卡留在「运行中」顶部，不收进折叠的「免打扰」组；
 * - 未置顶且开启免打扰的运行中卡片统一归入「免打扰」分组，避免刷屏干扰普通未读。
 */
export function buildPlanSidebarGroups(
  cards: readonly Card[],
  options: BuildPlanSidebarGroupsOptions = {},
): PlanSidebarGroup[] {
  const { searchQuery = "", activeCardID, activeAttemptStatus } = options;
  const query = searchQuery.trim().toLowerCase();

  const attention: Card[] = [];
  const active: Card[] = [];
  const muted: Card[] = [];
  const inactive: Card[] = [];

  for (const card of cards) {
    if (card.kind !== "ongoing") continue;
    if (
      query &&
      !`${card.title || ""} ${card.description || ""}`
        .toLowerCase()
        .includes(query)
    ) {
      continue;
    }

    const state = resolvedOngoingState(card);
    if (state === "paused" || state === "archived") {
      inactive.push(card);
    } else if (isPlanAttentionCard(card)) {
      attention.push(card);
    } else if (card.notifications_muted && !card.pinned_at) {
      muted.push(card);
    } else {
      active.push(card);
    }
  }

  const groups: PlanSidebarGroup[] = [];
  if (attention.length > 0) {
    groups.push({
      key: "attention",
      cards: attention.sort((a, b) =>
        compareAttentionCards(a, b, activeCardID, activeAttemptStatus),
      ),
    });
  }
  if (active.length > 0) {
    groups.push({
      key: "active",
      cards: active.sort((a, b) =>
        compareActiveCards(a, b, activeCardID, activeAttemptStatus),
      ),
    });
  }
  if (muted.length > 0) {
    groups.push({
      key: "muted",
      cards: muted.sort((a, b) =>
        compareActiveCards(a, b, activeCardID, activeAttemptStatus),
      ),
    });
  }
  if (inactive.length > 0) {
    groups.push({
      key: "inactive",
      cards: inactive.sort(compareInactiveCards),
    });
  }

  return groups;
}

/**
 * 计算定时侧边栏默认展开分组：
 * - 「需关注」与「运行中」默认展开；
 * - 「免打扰」与「已暂停 / 已归档」默认折叠，仅当包含当前激活卡片或搜索框非空时自动展开。
 */
export function getDefaultExpandedPlanGroups(
  groups: readonly PlanSidebarGroup[],
  options: { activeCardID?: string; searchQuery?: string } = {},
): PlanSidebarGroupKey[] {
  const expanded: PlanSidebarGroupKey[] = [];
  const hasSearch = Boolean(options.searchQuery?.trim());

  for (const group of groups) {
    if (group.key === "attention" || group.key === "active") {
      expanded.push(group.key);
      continue;
    }
    const containsActive = Boolean(
      options.activeCardID &&
        group.cards.some((card) => card.id === options.activeCardID),
    );
    if (hasSearch || containsActive) {
      expanded.push(group.key);
    }
  }

  return expanded;
}

/**
 * 按 card_id 归组项目排程，方便侧边栏 O(1) 读取单卡规则列表。
 */
export function groupSchedulesByCard(
  schedules: readonly ProjectSchedule[] | undefined,
): Map<string, ProjectSchedule[]> {
  const map = new Map<string, ProjectSchedule[]>();
  if (!schedules) return map;
  for (const item of schedules) {
    const existing = map.get(item.card_id);
    if (existing) {
      existing.push(item);
    } else {
      map.set(item.card_id, [item]);
    }
  }
  return map;
}

/**
 * 生成定时侧边栏单行紧凑副标题，复用 schedule-summary.ts 汇总逻辑：
 * - 运行中且有启用排程：优先前置「下次」时间，再拼接合并后的周期规则（如「下次 今天 13:17 · 每天 08:17、13:17、20:17」）；
 * - 无排程：「未设置自动执行时间」；
 * - 全部规则停用：「已停用」（可附带规则）；
 * - 卡片已暂停/已归档：「已暂停」/「已归档」（若配置了规则则附带规则）；
 * - 运行中卡片若存在最近失败，标记 hasFailedAttempt 以便柔和红字提示。
 */
export function buildPlanSidebarSubtitle(
  card: Pick<Card, "ongoing_state">,
  schedules: readonly ScheduleSummaryInput[],
  options: BuildPlanSidebarSubtitleOptions = {},
): PlanSidebarSubtitle {
  const { now = new Date(), t = identityTranslate } = options;
  const ongoingState = resolvedOngoingState(card);
  const summary = summarizePlanSchedules(schedules, { ongoingState, now, t });

  if (ongoingState === "paused" || ongoingState === "archived") {
    const stateLabel =
      ongoingState === "archived" ? t("已归档") : t("已暂停");
    return {
      text: summary.rulesText
        ? `${stateLabel} · ${summary.rulesText}`
        : stateLabel,
      hasFailedAttempt: false,
      failedLabel: null,
    };
  }

  if (summary.isEmpty) {
    return {
      text: t("未设置自动执行时间"),
      hasFailedAttempt: false,
      failedLabel: null,
    };
  }

  if (summary.statusHint) {
    return {
      text: summary.rulesText
        ? `${summary.statusHint} · ${summary.rulesText}`
        : summary.statusHint,
      hasFailedAttempt: false,
      failedLabel: null,
    };
  }

  const parts = [summary.nextTriggerText, summary.rulesText].filter(
    (part): part is string => Boolean(part),
  );
  return {
    text: parts.join(" · "),
    hasFailedAttempt: summary.hasFailedAttempt,
    failedLabel: summary.hasFailedAttempt ? t("上次执行失败") : null,
  };
}
