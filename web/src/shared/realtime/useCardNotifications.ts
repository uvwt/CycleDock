import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { keys } from "../../app/query";
import { i18n } from "../preferences";
import {
  beatPushPresence,
  markPushIdle,
  notificationWorker,
  notificationsSupported,
  syncPushSubscription,
  useNotificationsEnabled,
} from "./notifications";
import { api } from "../api/client";

import {
  cardMessageEvent,
  cardNotificationBody,
  type CardMessageDetail,
  type CardUpdate,
} from "./cardUpdates";

function viewedCardID() {
  const [, section, , cardID] = window.location.pathname.split("/");
  if (!cardID || (section !== "chat" && section !== "cards" && section !== "plans"))
    return "";
  try {
    return decodeURIComponent(cardID);
  } catch {
    return "";
  }
}

function watchLocation(report: () => void) {
  const push = history.pushState.bind(history);
  const replace = history.replaceState.bind(history);
  history.pushState = (...args: Parameters<History["pushState"]>) => {
    push(...args);
    report();
  };
  history.replaceState = (...args: Parameters<History["replaceState"]>) => {
    replace(...args);
    report();
  };
  window.addEventListener("popstate", report);
  return () => {
    history.pushState = push;
    history.replaceState = replace;
    window.removeEventListener("popstate", report);
  };
}

export function useCardNotifications(workspaceID: string) {
  const enabled = useNotificationsEnabled();
  const query = useQueryClient();
  useEffect(() => {
    if (
      !workspaceID ||
      !enabled ||
      !notificationsSupported() ||
      Notification.permission !== "granted"
    )
      return;
    let active = true;
    const controller = new AbortController();
    // 页面还开着时由这里收快照并通知。心跳告诉服务端先别推送，避免和这条路径重复，
    // 也避免 Safari 收到一条不展示的推送后收回权限。
    void syncPushSubscription();
    void beatPushPresence();
    const presence = window.setInterval(() => void beatPushPresence(), 20_000);
    window.addEventListener("pagehide", markPushIdle);
    let release: (() => void) | undefined;
    let channel: BroadcastChannel | undefined;
    let stream: EventSource | undefined;
    let previousCards: Map<string, CardUpdate> | undefined;
    let stopWatchingLocation = () => {};
    let stopReporting = () => {};
    void notificationWorker()
      .then(async (registration) => {
        if (!active) return;
        const reportViewing = () => {
          const cardID = viewedCardID();
          registration.active?.postMessage({
            type: "card.viewing",
            cardID,
            visible: document.visibilityState === "visible" && cardID !== "",
          });
        };
        reportViewing();
        document.addEventListener("visibilitychange", reportViewing);
        stopReporting = () =>
          document.removeEventListener("visibilitychange", reportViewing);
        stopWatchingLocation = watchLocation(reportViewing);
        const onSnapshot = (event: MessageEvent<string>) => {
          const cards = JSON.parse(event.data) as CardUpdate[];
          // 页面确认看过这些版本。关掉标签前没确认的更新，留给关闭后的推送。
          void api.ackPushCards(
            cards.map((card) => ({ id: card.id, revision: card.revision })),
          );
          // 首次快照只建立基线；原生 SSE 重连保留基线，补齐断线期间变化。
          if (previousCards) {
            for (const card of cards) {
              const previous = previousCards.get(card.id);
              if (previous?.revision === card.revision) continue;
              const viewingCardID = viewedCardID();
              void query.invalidateQueries({ queryKey: keys.card(card.id) });
              void query.invalidateQueries({
                queryKey: keys.cards(card.project_id),
              });
              void query.invalidateQueries({
                queryKey: keys.activity(card.project_id),
              });
              void query.invalidateQueries({
                queryKey: keys.schedules(card.project_id),
              });
              const body = cardNotificationBody(card, previous);
              if (!body) continue;
              const detail: CardMessageDetail = {
                ...card,
                body,
                viewing:
                  document.visibilityState === "visible" &&
                  viewingCardID === card.id,
              };
              window.dispatchEvent(new CustomEvent(cardMessageEvent, { detail }));
              const section = card.kind === "discussion" ? "chat" : "cards";
              registration.active?.postMessage({
                type: "card.updated",
                workspaceID,
                cardID: card.id,
                revision: card.revision,
                title: card.title || i18n.t("新消息"),
                body,
                url: `/${section}/${encodeURIComponent(card.project_id)}/${encodeURIComponent(card.id)}`,
                viewing:
                  document.visibilityState === "visible" &&
                  viewingCardID === card.id,
              });
            }
          }
          previousCards = new Map(cards.map((card) => [card.id, card]));
        };
        const connect = () => {
          stream = new EventSource("/api/v1/cards/updates/stream", {
            withCredentials: true,
          });
          stream.addEventListener("cards.snapshot", (event) => {
            channel?.postMessage((event as MessageEvent<string>).data);
            onSnapshot(event as MessageEvent<string>);
          });
        };
        if (navigator.locks && typeof BroadcastChannel !== "undefined") {
          // 同域只保留一条通知 SSE；其他标签页通过广播获得同一份更新。
          channel = new BroadcastChannel(
            `cycledock:card-updates:${workspaceID}`,
          );
          channel.onmessage = onSnapshot;
          await navigator.locks.request(
            "cycledock:card-notifications",
            { signal: controller.signal },
            () =>
              new Promise<void>((resolve) => {
                release = resolve;
                connect();
              }),
          );
        } else {
          connect();
        }
      })
      .catch((error: unknown) => {
        if (active)
          toast.error(`${i18n.t("浏览器通知启动失败")}: ${String(error)}`);
      });
    return () => {
      active = false;
      stopReporting();
      stopWatchingLocation();
      window.clearInterval(presence);
      window.removeEventListener("pagehide", markPushIdle);
      controller.abort();
      stream?.close();
      channel?.close();
      release?.();
    };
  }, [enabled, workspaceID, query]);
}
