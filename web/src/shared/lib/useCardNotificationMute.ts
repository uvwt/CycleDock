import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { keys, queryClient } from "../../app/query";
import { api } from "../api/client";
import type { Card } from "../api/types";
import { patchCachedCard } from "./card-cache";

// 统一卡片静音切换（适用于主聊天、普通讨论、Todo 与 Ongoing）。
export function useCardNotificationMute(card?: Card | null) {
  const { t } = useTranslation();

  return useMutation({
    mutationFn: ({
      id,
      muted,
    }: {
      id: string;
      muted: boolean;
      projectID?: string;
    }) => api.toggleCardNotifications(id, muted),
    onMutate: async ({ id, muted, projectID }) => {
      const targetProjectID = projectID || card?.project_id;
      await Promise.all([
        queryClient.cancelQueries({ queryKey: keys.card(id) }),
        ...(targetProjectID
          ? [
              queryClient.cancelQueries({
                queryKey: keys.cards(targetProjectID),
              }),
              queryClient.cancelQueries({
                queryKey: keys.mainDiscussion(targetProjectID),
              }),
            ]
          : []),
      ]);
      const prevMuted = Boolean(card?.notifications_muted);
      patchCachedCard(queryClient, id, targetProjectID, {
        notifications_muted: muted,
      });
      return { prevMuted, targetProjectID };
    },
    onSuccess: (updated) => {
      patchCachedCard(queryClient, updated.id, updated.project_id, updated);
      toast.success(
        updated.notifications_muted ? t("已静音通知") : t("已恢复通知"),
      );
      void Promise.all([
        queryClient.invalidateQueries({ queryKey: keys.card(updated.id) }),
        queryClient.invalidateQueries({
          queryKey: keys.cards(updated.project_id),
        }),
        ...(updated.kind === "discussion"
          ? [
              queryClient.invalidateQueries({
                queryKey: keys.mainDiscussion(updated.project_id),
              }),
            ]
          : []),
      ]);
    },
    onError: (error, variables, context) => {
      patchCachedCard(queryClient, variables.id, context?.targetProjectID, {
        notifications_muted: context?.prevMuted,
      });
      toast.error(
        error instanceof Error ? error.message : t("静音设置失败"),
      );
    },
  });
}
