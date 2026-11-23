import { useCallback, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { keys, queryClient } from "../../app/query";
import { api } from "../api/client";
import type { Card, CardDetail } from "../api/types";
import { patchCachedCard } from "./card-cache";
import { activeCardAutoReadSuppression } from "./card-read-state";

const DEFAULT_BATCH_MARK_READ_CONCURRENCY = 5;

export interface BatchMarkReadResult {
  succeededIDs: string[];
  failedIDs: string[];
}

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

/**
 * 批量将多张卡片一键标为已读（如「免打扰」分组的「全部已读」操作）。
 * 复用单卡 `api.markCardRead`，限制并发并做乐观更新；单卡失败时只回滚失败项并给出明确提示。
 */
export function useBatchMarkCardsRead(projectID?: string) {
  const { t } = useTranslation();
  const [isPending, setIsPending] = useState(false);

  const markBatchRead = useCallback(
    async (cardIDs: readonly string[]): Promise<BatchMarkReadResult> => {
      const uniqueIDs = Array.from(new Set(cardIDs.filter(Boolean)));
      if (uniqueIDs.length === 0 || isPending) {
        return { succeededIDs: [], failedIDs: [] };
      }

      setIsPending(true);
      const cachedCards = projectID
        ? queryClient.getQueryData<Card[]>(keys.cards(projectID))
        : undefined;
      const prevByID = new Map<
        string,
        {
          projectID?: string;
          unread?: boolean;
          marked_unread?: boolean;
          last_read_message_sequence?: number;
        }
      >();
      const touchedProjects = new Set<string>();
      if (projectID) touchedProjects.add(projectID);

      try {
        await Promise.all([
          ...uniqueIDs.map((id) =>
            queryClient.cancelQueries({ queryKey: keys.card(id) }),
          ),
          ...(projectID
            ? [
                queryClient.cancelQueries({ queryKey: keys.cards(projectID) }),
                queryClient.cancelQueries({
                  queryKey: keys.mainDiscussion(projectID),
                }),
              ]
            : []),
        ]);

        // 先乐观清零未读状态，让列表里的免打扰未读点与计数立即响应。
        for (const id of uniqueIDs) {
          const detail = queryClient.getQueryData<CardDetail>(keys.card(id));
          const targetCard =
            cachedCards?.find((item) => item.id === id) || detail?.card;
          const targetProjectID = targetCard?.project_id || projectID;
          if (targetProjectID) touchedProjects.add(targetProjectID);

          const baselineSeq = Math.max(
            targetCard?.latest_message_sequence ?? 0,
            computeMaxMessageSequence(detail),
          );
          activeCardAutoReadSuppression.clearSuppressed(id);
          prevByID.set(id, {
            projectID: targetProjectID,
            unread: targetCard?.unread,
            marked_unread: targetCard?.marked_unread,
            last_read_message_sequence: targetCard?.last_read_message_sequence,
          });

          patchCachedCard(queryClient, id, targetProjectID, {
            unread: false,
            marked_unread: false,
            last_read_message_sequence: Math.max(
              targetCard?.last_read_message_sequence ?? 0,
              baselineSeq,
            ),
          });
        }

        const succeededIDs: string[] = [];
        const failedIDs: string[] = [];
        let cursor = 0;
        const workerCount = Math.min(
          DEFAULT_BATCH_MARK_READ_CONCURRENCY,
          uniqueIDs.length,
        );

        await Promise.all(
          Array.from({ length: workerCount }, async () => {
            while (cursor < uniqueIDs.length) {
              const id = uniqueIDs[cursor++];
              const prev = prevByID.get(id);
              try {
                const result = await api.markCardRead(id);
                succeededIDs.push(id);
                patchCachedCard(queryClient, id, prev?.projectID, {
                  last_read_message_sequence: result.last_read_message_sequence,
                  unread: result.unread,
                  marked_unread: result.marked_unread,
                });
              } catch {
                failedIDs.push(id);
                patchCachedCard(queryClient, id, prev?.projectID, {
                  unread: prev?.unread,
                  marked_unread: prev?.marked_unread,
                  last_read_message_sequence: prev?.last_read_message_sequence,
                });
              }
            }
          }),
        );

        if (failedIDs.length === 0) {
          toast.success(
            t("已全部标为已读 ({{count}})", { count: succeededIDs.length }),
          );
        } else if (succeededIDs.length > 0) {
          toast.warning(
            t("已标为已读 {{succeeded}} 条，{{failed}} 条失败", {
              succeeded: succeededIDs.length,
              failed: failedIDs.length,
            }),
          );
        } else {
          toast.error(t("批量标为已读失败"));
        }

        for (const pid of touchedProjects) {
          void queryClient.invalidateQueries({ queryKey: keys.cards(pid) });
          void queryClient.invalidateQueries({
            queryKey: keys.mainDiscussion(pid),
          });
        }

        return { succeededIDs, failedIDs };
      } finally {
        setIsPending(false);
      }
    },
    [isPending, projectID, t],
  );

  return { markBatchRead, isPending };
}
