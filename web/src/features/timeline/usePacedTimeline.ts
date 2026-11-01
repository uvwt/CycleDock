import { useEffect, useState } from "react";
import type { TimelineEvent } from "../../shared/api/types";

const MESSAGE_INTERVAL_MS = 240;

/** 历史直接展示；实时批量消息逐条入场，人工输入到来时立即追平。 */
export function usePacedTimeline(events: TimelineEvent[], revealThrough = 0) {
  const [delivery, setDelivery] = useState(() => {
    const historyIDs = new Set(events.map((e) => e.id));
    return {
      source: events,
      visibleCount: events.length,
      historyIDs,
      // 保持对原有 historyThrough 的兼容（取历史事件中最大的 sequence）
      historyThrough: events.reduce((max, e) => Math.max(max, e.sequence), 0),
    };
  });

  // 当 events 更新，或者 revealThrough 增大时检查
  const targetRevealIndex =
    revealThrough > 0
      ? events.findLastIndex((e) => e.sequence <= revealThrough)
      : -1;
  const minRequiredCount = targetRevealIndex >= 0 ? targetRevealIndex + 1 : 0;

  if (delivery.source !== events || minRequiredCount > delivery.visibleCount) {
    const previousCount = delivery.source.length;
    const wasCaughtUp = delivery.visibleCount >= previousCount;

    // 检查新增事件中是否有人工输入
    const hasNewHuman = events
      .slice(delivery.visibleCount)
      .some((e) => e.actor_kind === "human");

    let nextVisible = delivery.visibleCount;
    if (events.length <= delivery.visibleCount) {
      // 消息减少或重排时，对齐最新长度
      nextVisible = events.length;
    } else if (hasNewHuman) {
      // 人工输入到来时立即追平全部
      nextVisible = events.length;
    } else if (wasCaughtUp) {
      // 之前已追平，新来的一批 Agent 消息先立刻展示第一条，后续逐条入场
      nextVisible = Math.min(events.length, delivery.visibleCount + 1);
    }
    nextVisible = Math.max(nextVisible, minRequiredCount);

    setDelivery({
      source: events,
      visibleCount: nextVisible,
      historyIDs: delivery.historyIDs,
      historyThrough: delivery.historyThrough,
    });
  }

  const hasMore = delivery.visibleCount < events.length;
  useEffect(() => {
    if (!hasMore) return;
    const timer = window.setTimeout(() => {
      setDelivery((current) => ({
        ...current,
        visibleCount: Math.min(events.length, current.visibleCount + 1),
      }));
    }, MESSAGE_INTERVAL_MS);
    return () => window.clearTimeout(timer);
  }, [hasMore, events.length, delivery.visibleCount]);

  return {
    visibleEvents: events.slice(0, delivery.visibleCount),
    historyThrough: delivery.historyThrough,
    historyIDs: delivery.historyIDs,
  };
}
