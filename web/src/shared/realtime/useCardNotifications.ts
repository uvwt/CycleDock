import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { keys } from "../../app/query";
import { i18n } from "../preferences";
import {
  notificationWorker,
  notificationsSupported,
  useNotificationsEnabled,
} from "./notifications";

import { cardNotificationBody, type CardUpdate } from "./cardUpdates";

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
    let stream: EventSource | undefined;
    let previousCards: Map<string, CardUpdate> | undefined;
    void notificationWorker()
      .then((registration) => {
        if (!active) return;
        stream = new EventSource("/api/v1/cards/updates/stream", {
          withCredentials: true,
        });
        stream.addEventListener("cards.snapshot", (event) => {
          const cards = JSON.parse(
            (event as MessageEvent<string>).data,
          ) as CardUpdate[];
          // 首次快照只建立基线；原生 SSE 重连保留基线，补齐断线期间变化。
          if (previousCards) {
            for (const card of cards) {
              const previous = previousCards.get(card.id);
              if (previous?.revision === card.revision) continue;
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
              const body = cardNotificationBody(card, previous, (key) =>
                i18n.t(key),
              );
              const section = card.kind === "discussion" ? "chat" : "cards";
              registration.active?.postMessage({
                type: "card.updated",
                workspaceID,
                cardID: card.id,
                revision: card.revision,
                title: card.title || i18n.t("卡片有更新"),
                body,
                url: `/${section}/${encodeURIComponent(card.project_id)}/${encodeURIComponent(card.id)}`,
              });
            }
          }
          previousCards = new Map(cards.map((card) => [card.id, card]));
        });
      })
      .catch((error: unknown) => {
        if (active)
          toast.error(`${i18n.t("浏览器通知启动失败")}: ${String(error)}`);
      });
    return () => {
      active = false;
      stream?.close();
    };
  }, [enabled, workspaceID, query]);
}
