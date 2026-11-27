import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { keys, queryClient } from "../../app/query";
import { api } from "../../shared/api/client";
import type { Card, TimelineEvent } from "../../shared/api/types";
import { patchCachedCard } from "../../shared/lib/card-cache";
import { activeCardAutoReadSuppression } from "../../shared/lib/card-read-state";
import {
  computeLatestVisibleMessageSequence,
  mergeCardReadSnapshots,
  resolveIncomingUnreadDividerAnchor,
  resolveInitialUnreadDividerAnchor,
} from "./unreadDivider";

interface UseUnreadDividerSessionOptions {
  cardID: string;
  projectID: string;
  detailCard?: Card | null;
  cachedCard?: Card | null;
  timeline: TimelineEvent[];
  /** 当前对话视图是否处于打开可见状态（CardPage 恒为 true；MainChatDock 随浮窗开合变化）。 */
  active: boolean;
  /** 详情查询是否仍在请求中，避免用过期的本地缓存抢先触发 markCardRead 覆盖服务端游标。 */
  detailFetching: boolean;
  /** 详情查询是否已有可用数据。 */
  detailReady: boolean;
}

export interface UnreadDividerSession {
  /** 当前停留在会话期间锁定的未读分隔符水位线（大于该序号的首条可见消息上方展示分隔符）。 */
  unreadDividerAfterSequence: number | null;
  /** 用户主动发送新消息后调用，自然清除当前会话的未读分隔符并同步已读状态。 */
  clearUnreadDivider: () => void;
  /** 由 ConversationView 上报当前是否处于消息列表底部，用于判断翻看历史期间是否新到消息。 */
  handleNearBottomChange: (nearBottom: boolean) => void;
  /** 手动立即同步一次当前卡片已读游标（例如发送消息成功后）。 */
  markCurrentCardRead: (targetCardID?: string, targetProjectID?: string) => void;
}

function isDocumentHidden(): boolean {
  return typeof document !== "undefined" && document.hidden;
}

/**
 * 统一管理聊天会话的「未读消息分隔符水位线」与「自动标已读」时序，
 * 供 CardPage（全屏讨论/待办/定时卡）与 MainChatDock（主聊天浮窗）共同复用。
 *
 * 核心保障：
 * 1. 进入卡片/打开浮窗时，先捕获并锁存进入前的已读水位线快照，再触发 markCardRead；
 * 2. markCardRead 返回后不会抹掉本次停留期间展示的未读分隔符；
 * 3. 用户发送新消息、切走卡片或关闭浮窗后再重新进入（且已读完）时自然清除；
 * 4. 停留期间若页面切后台或用户向上翻看历史时来了对方新消息，自动记录新消息起点且不抖动。
 */
export function useUnreadDividerSession({
  cardID,
  projectID,
  detailCard,
  cachedCard,
  timeline,
  active,
  detailFetching,
  detailReady,
}: UseUnreadDividerSessionOptions): UnreadDividerSession {
  const sessionKey = active && cardID ? `${cardID}:open` : "";
  const [anchorState, setAnchorState] = useState<{
    key: string;
    value: number | null;
  }>({ key: "", value: null });
  const [visibilityTick, setVisibilityTick] = useState(0);

  const anchorRef = useRef<number | null>(null);
  const initializedRef = useRef(false);
  const isNearBottomRef = useRef(true);
  const lastSeenMessageSeqRef = useRef(0);
  const hiddenBaselineSeqRef = useRef<number | null>(null);
  const lastMarkedSeqRef = useRef<number>(-1);

  const latestVisibleMessageSequence = useMemo(
    () => computeLatestVisibleMessageSequence(timeline),
    [timeline],
  );

  // 通过 microtask 更新 React 状态，避免 effect 同步 setState 触发级联渲染告警，同时用 ref 立即防抖。
  const setAnchor = useCallback(
    (next: number | null, targetKey = sessionKey) => {
      anchorRef.current = next;
      queueMicrotask(() => {
        setAnchorState((prev) =>
          prev.key === targetKey && prev.value === next
            ? prev
            : { key: targetKey, value: next },
        );
      });
    },
    [sessionKey],
  );

  const markCurrentCardRead = useCallback(
    (targetCardID?: string, targetProjectID?: string) => {
      const resolvedCardID = targetCardID || cardID;
      const resolvedProjectID = targetProjectID || projectID;
      if (!resolvedCardID) return;

      void api
        .markCardRead(resolvedCardID)
        .then((result) => {
          patchCachedCard(queryClient, resolvedCardID, resolvedProjectID || undefined, {
            last_read_message_sequence: result.last_read_message_sequence,
            unread: false,
            marked_unread: false,
          });
          if (!resolvedProjectID) return;
          return Promise.all([
            queryClient.invalidateQueries({
              queryKey: keys.cards(resolvedProjectID),
            }),
            queryClient.invalidateQueries({
              queryKey: keys.mainDiscussion(resolvedProjectID),
            }),
          ]);
        })
        .catch(() => {
          // 读状态同步失败不影响对话阅读本身
        });
    },
    [cardID, projectID],
  );

  // 切换卡片或关闭/重新打开浮窗时，彻底重置本次会话的未读分隔符状态（渲染侧也会按 sessionKey 隔离）。
  useEffect(() => {
    initializedRef.current = false;
    isNearBottomRef.current = true;
    lastSeenMessageSeqRef.current = 0;
    hiddenBaselineSeqRef.current = null;
    lastMarkedSeqRef.current = -1;
    anchorRef.current = null;
  }, [sessionKey]);

  // 监听标签页前后台切换：切到后台时冻结当时的已读基线，切回前台时再统一结算未读与自动已读。
  useEffect(() => {
    if (!active || !cardID || typeof document === "undefined") return;
    const handleVisibilityChange = () => {
      if (document.hidden) {
        if (hiddenBaselineSeqRef.current === null) {
          hiddenBaselineSeqRef.current = lastSeenMessageSeqRef.current;
        }
        return;
      }
      setVisibilityTick((tick) => tick + 1);
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [active, cardID]);

  // 核心协调 Effect：
  // 阶段 A（首次落定）：先捕获进入前的未读水位线，再执行首次自动已读；
  // 阶段 B（停留期间）：检测上翻历史或后台切回时的新到对方消息，以及推进服务端已读游标。
  useEffect(() => {
    if (!active || !cardID || !detailReady) return;

    const effectiveProjectID = detailCard?.project_id || cachedCard?.project_id || projectID;
    const snapshot = mergeCardReadSnapshots(detailCard, cachedCard);
    const effectiveLatestSeq = Math.max(
      latestVisibleMessageSequence,
      snapshot?.latest_message_sequence ?? 0,
    );

    if (!initializedRef.current) {
      // 首次拉取尚未落定时等待服务端最新快照，防止旧缓存抢先触发 markCardRead 吞掉未读水位线。
      if (detailFetching) return;

      const initialAnchor = resolveInitialUnreadDividerAnchor(snapshot, timeline);
      initializedRef.current = true;
      lastSeenMessageSeqRef.current = effectiveLatestSeq;
      if (initialAnchor !== null) {
        setAnchor(initialAnchor);
      }

      if (isDocumentHidden()) {
        hiddenBaselineSeqRef.current = effectiveLatestSeq;
        return;
      }
      if (
        !activeCardAutoReadSuppression.shouldSuppressAutoRead(
          cardID,
          effectiveLatestSeq,
        )
      ) {
        lastMarkedSeqRef.current = effectiveLatestSeq;
        markCurrentCardRead(cardID, effectiveProjectID);
      }
      return;
    }

    // 页面当前处于后台隐藏态：先暂存后台前的基线，等用户切回前台再结算分隔符与已读游标。
    if (isDocumentHidden()) {
      if (hiddenBaselineSeqRef.current === null) {
        hiddenBaselineSeqRef.current = lastSeenMessageSeqRef.current;
      }
      return;
    }

    // 刚从后台切回前台：若隐藏期间来了新的对方消息，在原基线处落下未读分隔符。
    if (hiddenBaselineSeqRef.current !== null) {
      const hiddenBaseline = hiddenBaselineSeqRef.current;
      hiddenBaselineSeqRef.current = null;
      const nextAnchor = resolveIncomingUnreadDividerAnchor({
        currentAnchor: anchorRef.current,
        previousReadSequence: hiddenBaseline,
        events: timeline,
      });
      if (nextAnchor !== anchorRef.current) {
        setAnchor(nextAnchor);
      }
    } else if (
      !isNearBottomRef.current &&
      latestVisibleMessageSequence > lastSeenMessageSeqRef.current
    ) {
      // 用户在前台但正向上翻看历史消息时收到新回复：锁定到达前的水位线，方便向下回看。
      const nextAnchor = resolveIncomingUnreadDividerAnchor({
        currentAnchor: anchorRef.current,
        previousReadSequence: lastSeenMessageSeqRef.current,
        events: timeline,
      });
      if (nextAnchor !== anchorRef.current) {
        setAnchor(nextAnchor);
      }
    }

    if (effectiveLatestSeq > lastSeenMessageSeqRef.current) {
      lastSeenMessageSeqRef.current = effectiveLatestSeq;
    }

    if (
      activeCardAutoReadSuppression.shouldSuppressAutoRead(
        cardID,
        effectiveLatestSeq,
      )
    ) {
      return;
    }

    const needsMarkRead =
      effectiveLatestSeq > lastMarkedSeqRef.current ||
      Boolean(snapshot?.unread && !snapshot?.marked_unread);

    if (needsMarkRead) {
      lastMarkedSeqRef.current = effectiveLatestSeq;
      markCurrentCardRead(cardID, effectiveProjectID);
    }
  }, [
    active,
    cardID,
    projectID,
    detailCard,
    cachedCard,
    detailFetching,
    detailReady,
    timeline,
    latestVisibleMessageSequence,
    visibilityTick,
    markCurrentCardRead,
    setAnchor,
  ]);

  const clearUnreadDivider = useCallback(() => {
    anchorRef.current = null;
    hiddenBaselineSeqRef.current = null;
    setAnchorState({ key: sessionKey, value: null });
  }, [sessionKey]);

  const handleNearBottomChange = useCallback((nearBottom: boolean) => {
    isNearBottomRef.current = nearBottom;
  }, []);

  const unreadDividerAfterSequence =
    anchorState.key === sessionKey ? anchorState.value : null;

  return {
    unreadDividerAfterSequence,
    clearUnreadDivider,
    handleNearBottomChange,
    markCurrentCardRead,
  };
}
