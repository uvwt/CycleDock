import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { keys } from "../../app-v2/query";
import type { CardDetail } from "../api/types";
export type LiveRun = {
  runID: string;
  text: string;
  stage: string;
  startedAt?: string;
};
export type StreamState = {
  connected: boolean;
  live: LiveRun | null;
  lastError?: string;
};
/** SSE 只负责临时状态和低延迟通知；REST timeline 是最终一致的事实源。 */
export function useCardStream(cardID: string, projectID: string): StreamState {
  const query = useQueryClient();
  const [state, setState] = useState<StreamState>({
    connected: false,
    live: null,
  });
  useEffect(() => {
    if (!cardID) return;
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
    const parse = (event: Event) => {
      try {
        return JSON.parse((event as MessageEvent).data) as {
          run_id?: string;
          text?: string;
          stage?: string;
          started_at?: string;
          message?: string;
        };
      } catch {
        return null;
      }
    };
    const onStarted = (e: Event) => {
      const v = parse(e);
      if (!v?.run_id || !active) return;
      setState((s) => ({
        ...s,
        live: {
          runID: v.run_id!,
          text: "",
          stage: "正在执行",
          startedAt: v.started_at,
        },
      }));
      sync();
    };
    const onStage = (e: Event) => {
      const v = parse(e);
      if (!active || !v) return;
      setState((s) => ({
        ...s,
        live:
          s.live && (!v.run_id || s.live.runID === v.run_id)
            ? { ...s.live, stage: v.stage || s.live.stage }
            : s.live,
      }));
      sync();
    };
    const onDelta = (e: Event) => {
      const v = parse(e);
      if (!active || !v?.run_id) return;
      const runID = v.run_id;
      const delta = v.text || "";
      setState((s) => ({
        ...s,
        live:
          s.live && s.live.runID !== runID
            ? s.live
            : s.live
              ? { ...s.live, text: s.live.text + delta }
              : {
                  runID,
                  text: delta,
                  stage: "正在生成回复",
                  startedAt: v.started_at,
                },
      }));
    };
    const onSnapshot = (e: Event) => {
      const v = parse(e);
      if (!active || !v?.run_id) return;
      setState((s) => ({
        ...s,
        live: {
          runID: v.run_id!,
          text: v.text || "",
          stage: v.stage || "正在执行",
          startedAt: v.started_at,
        },
      }));
    };
    const onFinal = (event: Event) => {
      const runID = parse(event)?.run_id;
      if (!active) return;
      // 等持久 Timeline 更新完再撤掉临时输出，避免最终回复闪烁消失。
      void refresh().finally(() => {
        if (active)
          setState((s) => ({
            ...s,
            live: !runID || s.live?.runID === runID ? null : s.live,
          }));
      });
    };
    stream.addEventListener("run.started", onStarted);
    stream.addEventListener("run.stage", onStage);
    stream.addEventListener("run.text.delta", onDelta);
    stream.addEventListener("run.text.snapshot", onSnapshot);
    stream.addEventListener("run.finished", onFinal);
    stream.addEventListener("run.failed", onFinal);
    stream.addEventListener("event.created", sync);
    stream.onopen = () => {
      if (!active) return;
      // EventSource 断线后会重新触发 open。不能清空尚未落库的临时文本；
      // 同实例会随后送快照，跨实例则以重新获取的持久 Run 状态收口。
      setState((s) => ({
        ...s,
        connected: true,
        lastError: undefined,
      }));
      void refresh().then(() => {
        if (!active) return;
        const detail = query.getQueryData<CardDetail>(keys.card(cardID));
        if (!detail) return;
        setState((s) => ({
          ...s,
          live:
            s.live && detail.current_run?.id !== s.live.runID
              ? null
              : s.live,
        }));
      });
    };
    stream.onerror = () => {
      if (active)
        setState((s) => ({
          ...s,
          connected: false,
          lastError: "实时连接中断，正在重连",
        }));
    };
    return () => {
      active = false;
      stream.close();
    };
  }, [cardID, projectID, query]);
  return state;
}
