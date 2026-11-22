import { useCallback, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { keys } from "../../app/query";
import { api } from "../api/client";
import type { Card, CardDetail, TodoState } from "../api/types";
import { patchCachedCard } from "./card-cache";
import {
  DEFAULT_BATCH_ACCEPTANCE_CONCURRENCY,
  runBatchReviewAcceptance,
  type BatchReviewAcceptanceResult,
} from "./review-acceptance";

/**
 * 统一单张待验收 Todo 卡片的「验收完成（review -> done）」操作：
 * 乐观更新单卡与项目列表缓存，接口成功后回写服务端最新数据并提示，失败时自动回滚。
 */
export function useCompleteReviewCard() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ id }: { id: string; projectID?: string }) =>
      api.moveTodo(id, "done"),
    onMutate: async ({ id, projectID }) => {
      const detail = queryClient.getQueryData<CardDetail | { card: null; notFound: true }>(
        keys.card(id),
      );
      const cachedDetailCard = detail && "card" in detail ? detail.card : null;
      const targetProjectID = projectID || cachedDetailCard?.project_id;

      await Promise.all([
        queryClient.cancelQueries({ queryKey: keys.card(id) }),
        ...(targetProjectID
          ? [
              queryClient.cancelQueries({
                queryKey: keys.cards(targetProjectID),
              }),
            ]
          : []),
      ]);

      const cachedCards = targetProjectID
        ? queryClient.getQueryData<Card[]>(keys.cards(targetProjectID))
        : undefined;
      const targetCard =
        cachedCards?.find((item) => item.id === id) || cachedDetailCard;
      const prevTodoState: TodoState | undefined = targetCard?.todo_state;

      patchCachedCard(queryClient, id, targetProjectID, {
        todo_state: "done",
      });

      return { targetProjectID, prevTodoState };
    },
    onSuccess: (updated, variables, context) => {
      const targetProjectID =
        updated.project_id || context?.targetProjectID || variables.projectID;
      patchCachedCard(queryClient, updated.id, targetProjectID, updated);
      toast.success(t("已验收完成"));
      void queryClient.invalidateQueries({ queryKey: keys.card(updated.id) });
      if (targetProjectID) {
        void queryClient.invalidateQueries({
          queryKey: keys.cards(targetProjectID),
        });
      } else {
        void queryClient.invalidateQueries({ queryKey: ["cards"] });
      }
    },
    onError: (error, variables, context) => {
      patchCachedCard(queryClient, variables.id, context?.targetProjectID, {
        todo_state: context?.prevTodoState ?? "review",
      });
      toast.error(error instanceof Error ? error.message : t("验收失败"));
    },
  });
}

/**
 * 批量验收待确认 Todo 卡片：
 * 并发调用 `api.moveTodo(id, "done")`（默认并发 5），统一回写缓存并按结果给出精准提示。
 */
export function useBatchCompleteReviewCards(projectID?: string) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [isPending, setIsPending] = useState(false);

  const completeBatch = useCallback(
    async (cardIDs: readonly string[]): Promise<BatchReviewAcceptanceResult> => {
      const uniqueIDs = Array.from(new Set(cardIDs.filter(Boolean)));
      if (uniqueIDs.length === 0 || isPending) {
        return {
          succeeded: [],
          succeededIDs: [],
          failed: [],
          failedIDs: [],
        };
      }

      setIsPending(true);
      try {
        const result = await runBatchReviewAcceptance(
          uniqueIDs,
          (id) => api.moveTodo(id, "done"),
          DEFAULT_BATCH_ACCEPTANCE_CONCURRENCY,
        );

        const touchedProjects = new Set<string>();
        if (projectID) {
          touchedProjects.add(projectID);
        }

        for (const updated of result.succeeded) {
          const targetProjectID = updated.project_id || projectID;
          if (targetProjectID) {
            touchedProjects.add(targetProjectID);
          }
          patchCachedCard(queryClient, updated.id, targetProjectID, updated);
          void queryClient.invalidateQueries({ queryKey: keys.card(updated.id) });
        }

        if (touchedProjects.size > 0) {
          for (const pid of touchedProjects) {
            void queryClient.invalidateQueries({ queryKey: keys.cards(pid) });
          }
        } else {
          void queryClient.invalidateQueries({ queryKey: ["cards"] });
        }

        if (result.succeeded.length > 0 && result.failed.length === 0) {
          toast.success(
            t("已完成 {{count}} 张卡片", { count: result.succeeded.length }),
          );
        } else if (result.succeeded.length > 0 && result.failed.length > 0) {
          toast.warning(
            t("已完成 {{succeeded}} 张卡片，{{failed}} 张失败", {
              succeeded: result.succeeded.length,
              failed: result.failed.length,
            }),
          );
        } else if (result.failed.length > 0) {
          toast.error(
            t("批量验收失败（{{failed}} 张）", { failed: result.failed.length }),
          );
        }

        return result;
      } finally {
        setIsPending(false);
      }
    },
    [isPending, projectID, queryClient, t],
  );

  return {
    isPending,
    completeBatch,
  };
}
