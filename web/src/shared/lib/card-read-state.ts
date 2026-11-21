import type { Card } from "../api/types.ts";

/**
 * 判断一张卡片是否处于未读状态（用于左滑按钮 / 操作菜单文案切换）。
 * 当卡片为当前激活卡片时，只有用户显式标为未读（marked_unread === true）才视为未读；
 * 非当前激活卡片则由 unread 或 marked_unread 任一成立判定为未读。
 */
export function isCardUnread(
  card: Pick<Card, "unread" | "marked_unread">,
  isActive: boolean = false,
): boolean {
  if (card.marked_unread) return true;
  if (isActive) return false;
  return Boolean(card.unread);
}

/**
 * 追踪当前已打开卡片被用户手动「标为未读」时的自动已读抑制状态。
 *
 * 规则：
 * 1. 用户在列表中把当前正在查看的卡片手动标为未读时，记录当时的可见消息序号（baselineSequence）；
 * 2. 只要用户仍停留在同一张卡片且可见消息序号没有推进（<= baselineSequence），禁止自动触发 markCardRead；
 * 3. 一旦出现下列任一动作，立即解除抑制并恢复自动已读：
 *    - 用户手动点击「标为已读」；
 *    - 用户重新点击该列表项切入该卡片，或切换到其他卡片再切回；
 *    - 该卡片收到或发送了新消息（latestVisibleMessageSequence > baselineSequence）。
 */
export interface AutoReadSuppressionStore {
  markSuppressed(cardID: string, baselineSequence: number): void;
  clearSuppressed(cardID: string): void;
  clearAllExcept(activeCardID: string): void;
  shouldSuppressAutoRead(cardID: string, latestVisibleSequence: number): boolean;
  reset(): void;
}

export function createAutoReadSuppressionStore(): AutoReadSuppressionStore {
  const suppressedByCardID = new Map<string, number>();

  return {
    markSuppressed(cardID: string, baselineSequence: number) {
      if (!cardID) return;
      suppressedByCardID.set(cardID, Math.max(0, baselineSequence || 0));
    },
    clearSuppressed(cardID: string) {
      if (!cardID) return;
      suppressedByCardID.delete(cardID);
    },
    clearAllExcept(activeCardID: string) {
      for (const id of Array.from(suppressedByCardID.keys())) {
        if (id !== activeCardID) {
          suppressedByCardID.delete(id);
        }
      }
    },
    shouldSuppressAutoRead(
      cardID: string,
      latestVisibleSequence: number,
    ): boolean {
      if (!cardID) return false;
      const baseline = suppressedByCardID.get(cardID);
      if (baseline === undefined) return false;
      const currentSeq = Math.max(0, latestVisibleSequence || 0);
      if (currentSeq > baseline) {
        suppressedByCardID.delete(cardID);
        return false;
      }
      return true;
    },
    reset() {
      suppressedByCardID.clear();
    },
  };
}

export const activeCardAutoReadSuppression = createAutoReadSuppressionStore();
