import type { Card } from "../../shared/api/types";
import { isCardUnread } from "../../shared/lib/card-read-state";

export interface DiscussionListSections {
  /** 顶部常驻区：项目主聊天（固定第一）+ 手动置顶讨论（含置顶的免打扰讨论） */
  pinned: Card[];
  /** 未置顶的免打扰讨论，放入紧邻置顶下方的单行折叠分组 */
  muted: Card[];
  /** 其余常规讨论，按最近更新时间倒序 */
  regular: Card[];
  /** 过滤后的可见讨论总数 */
  totalCount: number;
}

export interface BuildDiscussionListSectionsOptions {
  mainDiscussionID?: string;
  searchQuery?: string;
  activeCardID?: string;
  activeAttemptStatus?: string;
}

function isDiscussionRunning(
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

function compareTimeDesc(a?: string, b?: string): number {
  const aMs = a ? Date.parse(a) : Number.NaN;
  const bMs = b ? Date.parse(b) : Number.NaN;
  if (!Number.isNaN(aMs) && !Number.isNaN(bMs) && aMs !== bMs) {
    return bMs - aMs;
  }
  return (b || "").localeCompare(a || "");
}

export function getUnreadDiscussionCardIDs(
  cards: readonly Pick<Card, "id" | "unread" | "marked_unread">[],
  activeCardID?: string,
): string[] {
  const ids: string[] = [];
  for (const card of cards) {
    if (isCardUnread(card, Boolean(activeCardID && card.id === activeCardID))) {
      ids.push(card.id);
    }
  }
  return ids;
}

/**
 * 将讨论列表切分为三段：
 * 1. pinned：主聊天永远第一，其余手动置顶按 pinned_at 倒序（置顶免打扰留在外面，不收进折叠组）；
 * 2. muted：未置顶的免打扰讨论（收进紧邻置顶下方的单行折叠入口，避免讨论过长时要翻到底）；
 * 3. regular：其余普通讨论，按 updated_at 倒序。
 */
export function buildDiscussionListSections(
  discussions: readonly Card[],
  options: BuildDiscussionListSectionsOptions = {},
): DiscussionListSections {
  const {
    mainDiscussionID = "",
    searchQuery = "",
    activeCardID,
    activeAttemptStatus,
  } = options;
  const query = searchQuery.trim().toLowerCase();

  const pinned: Card[] = [];
  const muted: Card[] = [];
  const regular: Card[] = [];

  for (const card of discussions) {
    if (card.kind !== "discussion") continue;
    if (
      query &&
      !`${card.title || ""} ${card.description || ""}`
        .toLowerCase()
        .includes(query)
    ) {
      continue;
    }

    const isMain = Boolean(mainDiscussionID && card.id === mainDiscussionID);
    if (isMain || Boolean(card.pinned_at)) {
      pinned.push(card);
    } else if (card.notifications_muted) {
      muted.push(card);
    } else {
      regular.push(card);
    }
  }

  pinned.sort((a, b) => {
    if (mainDiscussionID) {
      if (a.id === mainDiscussionID) return -1;
      if (b.id === mainDiscussionID) return 1;
    }
    const pinDiff = compareTimeDesc(a.pinned_at, b.pinned_at);
    if (pinDiff !== 0) return pinDiff;
    return compareTimeDesc(a.updated_at, b.updated_at);
  });

  // 免打扰分组内按「正在执行 > 未读 > 最近更新」排列，展开后一眼看到最新有变动的会话。
  muted.sort((a, b) => {
    const aRunning = isDiscussionRunning(a, activeCardID, activeAttemptStatus)
      ? 0
      : 1;
    const bRunning = isDiscussionRunning(b, activeCardID, activeAttemptStatus)
      ? 0
      : 1;
    if (aRunning !== bRunning) return aRunning - bRunning;

    const aUnread = isCardUnread(
      a,
      Boolean(activeCardID && a.id === activeCardID),
    )
      ? 0
      : 1;
    const bUnread = isCardUnread(
      b,
      Boolean(activeCardID && b.id === activeCardID),
    )
      ? 0
      : 1;
    if (aUnread !== bUnread) return aUnread - bUnread;

    const timeDiff = compareTimeDesc(a.updated_at, b.updated_at);
    if (timeDiff !== 0) return timeDiff;
    return a.id.localeCompare(b.id);
  });

  regular.sort((a, b) => {
    const timeDiff = compareTimeDesc(a.updated_at, b.updated_at);
    if (timeDiff !== 0) return timeDiff;
    return a.id.localeCompare(b.id);
  });

  return {
    pinned,
    muted,
    regular,
    totalCount: pinned.length + muted.length + regular.length,
  };
}

/**
 * 讨论列表的「免打扰」分组默认折叠；
 * 当用户正在搜索或当前激活卡片就在免打扰分组中时自动展开。
 */
export function shouldDefaultExpandMutedDiscussions(
  mutedCards: readonly Pick<Card, "id">[],
  options: { activeCardID?: string; searchQuery?: string } = {},
): boolean {
  if (mutedCards.length === 0) return false;
  if (Boolean(options.searchQuery?.trim())) return true;
  if (
    options.activeCardID &&
    mutedCards.some((card) => card.id === options.activeCardID)
  ) {
    return true;
  }
  return false;
}
