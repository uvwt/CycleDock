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
    if (!workspaceID) return;
    const canNotify =
      enabled &&
      notificationsSupported() &&
      Notification.permission === "granted";

    let active = true;
    const controller = new AbortController();
    // 页面还开着时由这里收快照并刷新全局卡片状态（导航栏绿点/蓝点、焦点列表、看板与定时任务）。
    // 若同时开启了浏览器通知权限，则额外维护推送心跳与 Service Worker 弹窗通知。
    const syncPresenceWithVisibility = () => {
      if (!canNotify) return;
      if (document.visibilityState === "visible") {
        void beatPushPresence();
      } else {
        markPushIdle();
      }
    };
    let presence: number | undefined;
    if (canNotify) {
      void syncPushSubscription();
      syncPresenceWithVisibility();
      presence = window.setInterval(() => {
        if (document.visibilityState === "visible") {
          void beatPushPresence();
        }
      }, 20_000);
      document.addEventListener("visibilitychange", syncPresenceWithVisibility);
      window.addEventListener("pagehide", markPushIdle);
      window.addEventListener("pageshow", syncPresenceWithVisibility);
    }

    let release: (() => void) | undefined;
    let channel: BroadcastChannel | undefined;
    let stream: EventSource | undefined;
    let previousCards: Map<string, CardUpdate> | undefined;
    let stopWatchingLocation = () => {};
    let stopReporting = () => {};

    const startStream = async (registration?: ServiceWorkerRegistration) => {
      if (!active) return;
      if (registration) {
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
      }

      const onSnapshot = (event: MessageEvent<string>) => {
        const cards = JSON.parse(event.data) as CardUpdate[];
        // 页面处于前台可见状态时才确认看过这些版本。
        // 若页面处于后台或锁屏（hidden），严禁自动 ack，保留让 Web Push 触达的机会。
        // 待切回前台（visible）时再补发已阅确认。
        if (canNotify && document.visibilityState === "visible") {
          void api.ackPushCards(
            cards.map((card) => ({ id: card.id, revision: card.revision })),
          );
        }
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
            if (card.kind === "discussion") {
              void query.invalidateQueries({
                queryKey: keys.mainDiscussion(card.project_id),
              });
            }
            void query.invalidateQueries({
              queryKey: keys.activity(card.project_id),
            });
            void query.invalidateQueries({
              queryKey: keys.schedules(card.project_id),
            });
            const body = cardNotificationBody(card, previous);
            if (!body || card.notifications_muted) continue;
            const detail: CardMessageDetail = {
              ...card,
              body,
              viewing:
                document.visibilityState === "visible" &&
                viewingCardID === card.id,
            };
            window.dispatchEvent(new CustomEvent(cardMessageEvent, { detail }));
            if (registration) {
              const section =
                card.kind === "discussion"
                  ? "chat"
                  : card.kind === "ongoing"
                    ? "plans"
                    : "cards";
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
        }
        previousCards = new Map(cards.map((card) => [card.id, card]));
      };

      if (canNotify) {
        const syncAckOnVisible = () => {
          if (document.visibilityState === "visible" && previousCards) {
            const cards = Array.from(previousCards.values());
            if (cards.length > 0) {
              void api.ackPushCards(
                cards.map((card) => ({ id: card.id, revision: card.revision })),
              );
            }
          }
        };
        document.addEventListener("visibilitychange", syncAckOnVisible);
        const previousStopReporting = stopReporting;
        stopReporting = () => {
          previousStopReporting();
          document.removeEventListener("visibilitychange", syncAckOnVisible);
        };
      }

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
    };

    if (canNotify) {
      void notificationWorker()
        .then((registration) => startStream(registration))
        .catch((error: unknown) => {
          if (active) {
            toast.error(`${i18n.t("浏览器通知启动失败")}: ${String(error)}`);
            void startStream();
          }
        });
    } else {
      void startStream();
    }

    return () => {
      active = false;
      stopReporting();
      stopWatchingLocation();
      if (presence !== undefined) {
        window.clearInterval(presence);
      }
      if (canNotify) {
        document.removeEventListener(
          "visibilitychange",
          syncPresenceWithVisibility,
        );
        window.removeEventListener("pagehide", markPushIdle);
        window.removeEventListener("pageshow", syncPresenceWithVisibility);
      }
      controller.abort();
      stream?.close();
      channel?.close();
      release?.();
    };
  }, [enabled, workspaceID, query]);
}
