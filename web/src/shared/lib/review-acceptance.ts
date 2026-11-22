import type { Card } from "../api/types.ts";

export type CardSwipeActionSlot =
  | "complete"
  | "read-state"
  | "pin"
  | "mute"
  | "delete";

export interface BatchReviewAcceptanceFailure {
  id: string;
  error: unknown;
}

export interface BatchReviewAcceptanceResult {
  succeeded: Card[];
  succeededIDs: string[];
  failed: BatchReviewAcceptanceFailure[];
  failedIDs: string[];
}

export const DEFAULT_BATCH_ACCEPTANCE_CONCURRENCY = 5;

/**
 * 判断卡片是否为待人工验收的 Todo（Review -> Done 仅由人工触发）。
 */
export function isReviewTodoCard(
  card?: Pick<Card, "kind" | "todo_state"> | null,
): boolean {
  return Boolean(card && card.kind === "todo" && card.todo_state === "review");
}

/**
 * 决定卡片行左滑按钮槽位排布（严格控制最多 3 个高频操作，保持移动端触控宽裕）：
 * - 主讨论（固定置顶、不可删）：「已读/未读」+「静音」
 * - 待验收 Todo 且提供验收回调：首个按钮优先给绿色「验收完成」，其次「已读/未读」，
 *   第三优先「删除」（无删除时回退「置顶」）；被替换的次要动作仍完整保留在长按菜单中
 * - 其他普通卡片：「已读/未读」+「置顶」+「删除」
 */
export function resolveCardSwipeActionSlots({
  card,
  isMain = false,
  hasComplete = false,
  hasPin = false,
  hasMute = false,
  hasDelete = false,
}: {
  card: Pick<Card, "kind" | "todo_state">;
  isMain?: boolean;
  hasComplete?: boolean;
  hasPin?: boolean;
  hasMute?: boolean;
  hasDelete?: boolean;
}): CardSwipeActionSlot[] {
  if (isMain) {
    const slots: CardSwipeActionSlot[] = ["read-state"];
    if (hasMute) {
      slots.push("mute");
    }
    return slots;
  }

  if (hasComplete && isReviewTodoCard(card)) {
    const slots: CardSwipeActionSlot[] = ["complete", "read-state"];
    if (hasDelete) {
      slots.push("delete");
    } else if (hasPin) {
      slots.push("pin");
    }
    return slots;
  }

  const slots: CardSwipeActionSlot[] = ["read-state"];
  if (hasPin) {
    slots.push("pin");
  }
  if (hasDelete) {
    slots.push("delete");
  }
  return slots;
}

/**
 * 切换单个待验收卡片的勾选状态，返回新的不可变集合。
 */
export function toggleReviewSelection(
  selectedIDs: ReadonlySet<string>,
  cardID: string,
): Set<string> {
  const next = new Set(selectedIDs);
  if (next.has(cardID)) {
    next.delete(cardID);
  } else {
    next.add(cardID);
  }
  return next;
}

/**
 * 根据当前仍处于待验收状态的卡片列表裁剪已选集合，避免他端已验收或过滤变化后残留幽灵勾选。
 */
export function reconcileReviewSelection(
  selectedIDs: ReadonlySet<string>,
  availableIDs: readonly string[],
): Set<string> {
  if (selectedIDs.size === 0 || availableIDs.length === 0) {
    return new Set();
  }
  const availableSet = new Set(availableIDs);
  const next = new Set<string>();
  for (const id of selectedIDs) {
    if (availableSet.has(id)) {
      next.add(id);
    }
  }
  return next;
}

/**
 * 判断当前可见的待验收卡片是否已全部勾选。
 */
export function isAllReviewSelected(
  selectedIDs: ReadonlySet<string>,
  availableIDs: readonly string[],
): boolean {
  if (availableIDs.length === 0) return false;
  return availableIDs.every((id) => selectedIDs.has(id));
}

/**
 * 切换全选 / 取消全选。
 */
export function toggleSelectAllReview(
  selectedIDs: ReadonlySet<string>,
  availableIDs: readonly string[],
): Set<string> {
  if (isAllReviewSelected(selectedIDs, availableIDs)) {
    return new Set();
  }
  return new Set(availableIDs);
}

/**
 * 带并发上限批量验收待确认卡片：
 * - 自动对传入 ID 去重，避免重复请求；
 * - 单张失败不中断其余卡片，最终返回成功项与失败项，供调用方精准提示并仅保留失败项勾选。
 */
export async function runBatchReviewAcceptance(
  cardIDs: readonly string[],
  completeCard: (cardID: string) => Promise<Card>,
  concurrency: number = DEFAULT_BATCH_ACCEPTANCE_CONCURRENCY,
): Promise<BatchReviewAcceptanceResult> {
  const uniqueIDs = Array.from(new Set(cardIDs.filter(Boolean)));
  if (uniqueIDs.length === 0) {
    return {
      succeeded: [],
      succeededIDs: [],
      failed: [],
      failedIDs: [],
    };
  }

  const limit = Math.max(1, Math.min(Math.floor(concurrency) || 1, uniqueIDs.length));
  const outcomes: Array<
    | { ok: true; id: string; card: Card }
    | { ok: false; id: string; error: unknown }
  > = new Array(uniqueIDs.length);

  let cursor = 0;
  async function worker() {
    while (true) {
      const index = cursor;
      cursor += 1;
      if (index >= uniqueIDs.length) return;
      const id = uniqueIDs[index];
      try {
        const card = await completeCard(id);
        outcomes[index] = { ok: true, id, card };
      } catch (error) {
        outcomes[index] = { ok: false, id, error };
      }
    }
  }

  await Promise.all(Array.from({ length: limit }, () => worker()));

  const succeeded: Card[] = [];
  const succeededIDs: string[] = [];
  const failed: BatchReviewAcceptanceFailure[] = [];
  const failedIDs: string[] = [];

  for (const outcome of outcomes) {
    if (!outcome) continue;
    if (outcome.ok) {
      succeeded.push(outcome.card);
      succeededIDs.push(outcome.id);
    } else {
      failed.push({ id: outcome.id, error: outcome.error });
      failedIDs.push(outcome.id);
    }
  }

  return {
    succeeded,
    succeededIDs,
    failed,
    failedIDs,
  };
}
