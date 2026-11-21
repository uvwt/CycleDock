import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { keys, queryClient } from "../../app/query";
import { api } from "../api/client";
import type { Card, CardDetail } from "../api/types";
import { patchCachedCard } from "./card-cache";
import { activeCardAutoReadSuppression } from "./card-read-state";

function computeMaxMessageSequence(detail?: CardDetail): number {
  if (!detail?.timeline?.length) return 0;
  return detail.timeline.reduce((max, event) => {
    if (event.type !== "message" && event.type !== "comment") return max;
    return Math.max(max, event.sequence || 0);
  }, 0);
}

/**
 * 统一卡片「标为未读 / 标为已读」状态切换（覆盖主聊天、讨论、Todo 与 Ongoing）。
 * 当操作对象是当前正在打开查看的卡片且切换为「未读」时，自动登记自动已读抑制基线，
 * 防止 CardPage 的自动已读 Effect 在下一毫秒立刻把手动未读清回已读。
 */
export function useCardReadState(card?: Card | null) {
  const { t } = useTranslation();

  return useMutation({
    mutationFn: ({
      id,
      unread,
    }: {
      id: string;
      unread: boolean;
      projectID?: string;
      isActive?: boolean;
    }) => (unread ? api.markCardUnread(id) : api.markCardRead(id)),
    onMutate: async ({ id, unread, projectID, isActive }) => {
      const targetProjectID = projectID || card?.project_id;
      const detail = queryClient.getQueryData<CardDetail>(keys.card(id));
      const cachedCards = targetProjectID
        ? queryClient.getQueryData<Card[]>(keys.cards(targetProjectID))
        : undefined;
      const targetCard =
        cachedCards?.find((item) => item.id === id) || detail?.card || card;

      const baselineSeq = Math.max(
        targetCard?.latest_message_sequence ?? 0,
        computeMaxMessageSequence(detail),
      );

      if (unread) {
        if (isActive) {
          activeCardAutoReadSuppression.markSuppressed(id, baselineSeq);
        }
      } else {
        activeCardAutoReadSuppression.clearSuppressed(id);
      }

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

      const prevUnread = targetCard?.unread;
      const prevMarkedUnread = targetCard?.marked_unread;
      const prevLastReadSeq = targetCard?.last_read_message_sequence;

      patchCachedCard(queryClient, id, targetProjectID, {
        unread,
        marked_unread: unread,
        ...(unread
          ? {}
          : {
              last_read_message_sequence: Math.max(
                prevLastReadSeq ?? 0,
                baselineSeq,
              ),
            }),
      });

      return {
        targetProjectID,
        prevUnread,
        prevMarkedUnread,
        prevLastReadSeq,
      };
    },
    onSuccess: (result, variables, context) => {
      const targetProjectID = context?.targetProjectID || variables.projectID;
      patchCachedCard(queryClient, variables.id, targetProjectID, {
        last_read_message_sequence: result.last_read_message_sequence,
        unread: result.unread,
        marked_unread: result.marked_unread,
      });
      toast.success(
        result.marked_unread ? t("已标为未读") : t("已标为已读"),
      );
      if (targetProjectID) {
        void queryClient.invalidateQueries({
          queryKey: keys.cards(targetProjectID),
        });
        void queryClient.invalidateQueries({
          queryKey: keys.mainDiscussion(targetProjectID),
        });
      }
    },
    onError: (error, variables, context) => {
      if (variables.unread) {
        activeCardAutoReadSuppression.clearSuppressed(variables.id);
      }
      patchCachedCard(queryClient, variables.id, context?.targetProjectID, {
        unread: context?.prevUnread,
        marked_unread: context?.prevMarkedUnread,
        last_read_message_sequence: context?.prevLastReadSeq,
      });
      toast.error(
        error instanceof Error ? error.message : t("未读状态更新失败"),
      );
    },
  });
}
