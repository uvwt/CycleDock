import { useEffect, useState, useSyncExternalStore } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { keys } from "../../app/query";
import {
  applyCardStreamEvent,
  type CardStreamEvent,
  type LiveAttempt,
} from "./cardStreamState";
export type { LiveAttempt } from "./cardStreamState";
export type StreamState = {
  connected: boolean;
  live: LiveAttempt | null;
  lastError?: string;
};
function subscribeVisibility(onChange: () => void) {
  document.addEventListener("visibilitychange", onChange);
  return () => document.removeEventListener("visibilitychange", onChange);
}

/** SSE 只负责临时状态和低延迟通知；REST timeline 是最终一致的事实源。 */
export function useCardStream(cardID: string, projectID: string): StreamState {
  const query = useQueryClient();
  const visible = useSyncExternalStore(
    subscribeVisibility,
    () => document.visibilityState === "visible",
    () => true,
  );
  const [state, setState] = useState<StreamState & { cardID: string }>({
    cardID,
    connected: false,
    live: null,
  });
  useEffect(() => {
    // HTTP/1.1 的连接额度由同域标签页共享，后台卡片不占用长连接。
    if (!cardID || !visible) return;
    let active = true;
    const stream = new EventSource(
      `/api/v1/cards/${encodeURIComponent(cardID)}/stream`,
      { withCredentials: true },
    );
    const refresh = () =>
      Promise.all([
        query.invalidateQueries({ queryKey: keys.card(cardID) }),
        query.invalidateQueries({ queryKey: keys.cards(projectID) }),
      ]);
    const sync = () => {
      void refresh();
    };
    const onEvent = (event: Event) => {
      if (!active) return;
      let payload: CardStreamEvent;
      try {
        payload = JSON.parse((event as MessageEvent).data);
      } catch {
        return;
      }
      setState((current) => ({
        ...current,
        cardID,
        live: applyCardStreamEvent(
          current.cardID === cardID ? current.live : null,
          event.type,
          payload,
        ),
      }));
      if (
        [
          "attempt.started",
          "attempt.finished",
          "attempt.failed",
          "reply.committed",
        ].includes(event.type)
      )
        sync();
    };
    for (const type of [
      "attempt.started",
      "attempt.snapshot",
      "attempt.stage",
      "attempt.device",
      "attempt.task",
      "reply.preview",
      "reply.committed",
      "reply.discarded",
      "reply.reset",
      "attempt.finished",
      "attempt.failed",
    ]) {
      stream.addEventListener(type, onEvent);
    }
    stream.addEventListener("event.created", sync);
    stream.addEventListener("card.updated", sync);
    stream.onopen = () => {
      if (!active) return;
      setState((s) => ({
        ...s,
        cardID,
        connected: true,
        lastError: undefined,
        live: s.cardID === cardID ? s.live : null,
      }));
      sync();
    };
    stream.onerror = () => {
      if (active)
        setState((s) => ({
          ...s,
          cardID,
          live: s.cardID === cardID ? s.live : null,
          connected: false,
          lastError: "实时连接中断，正在重连",
        }));
    };
    return () => {
      active = false;
      stream.close();
    };
  }, [cardID, projectID, query, visible]);
  return visible && state.cardID === cardID
    ? state
    : { connected: false, live: null };
}
