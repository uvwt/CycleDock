import { useEffect, useState } from "react";
import type { TimelineEvent } from "../../shared/api/types";

const MESSAGE_INTERVAL_MS = 240;

/** 历史直接展示；实时批量消息逐条入场，人工输入到来时立即追平。 */
export function usePacedTimeline(events: TimelineEvent[], revealThrough = 0) {
  const [delivery, setDelivery] = useState(() => ({
    source: events,
    visibleThrough: events.at(-1)?.sequence ?? 0,
    historyThrough: events.at(-1)?.sequence ?? 0,
  }));

  if (delivery.source !== events || revealThrough > delivery.visibleThrough) {
    const receivedThrough = delivery.source.at(-1)?.sequence ?? 0;
    const newHuman = events.findLast(
      (event) =>
        event.sequence > receivedThrough && event.actor_kind === "human",
    );
    const firstNew = events.find((event) => event.sequence > receivedThrough);
    const wasCaughtUp = delivery.visibleThrough >= receivedThrough;
    setDelivery({
      source: events,
      historyThrough: delivery.historyThrough,
      visibleThrough: Math.max(
        delivery.visibleThrough,
        revealThrough,
        newHuman?.sequence ?? 0,
        wasCaughtUp ? (firstNew?.sequence ?? 0) : 0,
      ),
    });
  }

  const next = events.find((event) => event.sequence > delivery.visibleThrough);
  useEffect(() => {
    if (!next) return;
    const timer = window.setTimeout(() => {
      setDelivery((current) => ({
        ...current,
        visibleThrough: Math.max(current.visibleThrough, next.sequence),
      }));
    }, MESSAGE_INTERVAL_MS);
    return () => window.clearTimeout(timer);
  }, [next]);

  return {
    visibleEvents: events.filter(
      (event) => event.sequence <= delivery.visibleThrough,
    ),
    historyThrough: delivery.historyThrough,
  };
}
