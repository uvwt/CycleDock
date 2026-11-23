import { useCallback, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  ListChecks,
  Loader2,
  Pin,
  PinOff,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { Card, TodoState } from "../../shared/api/types";
import { api } from "../../shared/api/client";
import {
  patchCachedCard,
  removeCachedCard,
} from "../../shared/lib/card-cache";
import { useCardNotificationMute } from "../../shared/lib/useCardNotificationMute";
import { activeCardAutoReadSuppression } from "../../shared/lib/card-read-state";
import { useCardReadState } from "../../shared/lib/useCardReadState";
import {
  isAllReviewSelected,
  isReviewTodoCard,
  reconcileReviewSelection,
  toggleReviewSelection,
  toggleSelectAllReview,
} from "../../shared/lib/review-acceptance";
import {
  useBatchCompleteReviewCards,
  useCompleteReviewCard,
} from "../../shared/lib/useCompleteReviewCard";
import {
  ContextSidebar,
  ContextSidebarItem,
  ContextSidebarSkeleton,
} from "../../shared/ui/ContextSidebar";
import { Badge, ConfirmDialog } from "../../shared/ui/components";
import { EditCardDialog } from "../../shared/ui/EditCardDialog";
import {
  CardStatusDot,
  getCardVisualStatus,
} from "../../shared/ui/CardStatusDot";
import { CardActions } from "../cards/CardActions";
import { CardRowActionSheet } from "../cards/CardRowActionSheet";
import { buildCardSwipeActions } from "../cards/card-swipe-actions";

type WorkGroup = TodoState;

const todoGroups: TodoState[] = ["todo", "working", "review", "done"];

function groupForCard(card?: Card): WorkGroup | "" {
  if (!card) return "";
  if (card.kind === "todo" && card.todo_state) return card.todo_state;
  return "";
}

export function WorkSidebar({
  cards,
  activeCardID,
  activeAttemptStatus,
  onSelect,
  loading = false,
  className = "",
}: {
  cards: Card[];
  activeCardID?: string;
  activeAttemptStatus?: string;
  onSelect: (card: Card) => void;
  loading?: boolean;
  className?: string;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const workCards = useMemo(
    () =>
      cards
        .filter((card) => card.kind === "todo")
        .sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
    [cards],
  );
  const activeCard = useMemo(
    () => workCards.find((card) => card.id === activeCardID),
    [activeCardID, workCards],
  );
  const activeGroup = groupForCard(activeCard);
  const activeKey = `${activeCardID || ""}:${activeGroup || ""}`;
  const defaultExpandedGroups = useMemo<WorkGroup[]>(() => {
    if (activeGroup === "working" || activeGroup === "review") {
      return ["working", "review"];
    }
    return [activeGroup || "todo"];
  }, [activeGroup]);

  const [manualExpansion, setManualExpansion] = useState<{
    activeKey: string;
    expandedGroups: WorkGroup[];
  } | null>(null);

  const expandedGroups =
    manualExpansion?.activeKey === activeKey
      ? manualExpansion.expandedGroups
      : defaultExpandedGroups;

  const groups = useMemo(
    () =>
      todoGroups.map((group) => ({
        group,
        cards: workCards.filter((card) => card.todo_state === group),
      })),
    [workCards],
  );

  const labelFor = (group: WorkGroup) => {
    switch (group) {
      case "todo":
        return t("待办");
      case "working":
        return t("进行中");
      case "review":
        return t("待验收");
      case "done":
        return t("已完成");
    }
  };

  const queryClient = useQueryClient();
  const [deleteTarget, setDeleteTarget] = useState<Card | null>(null);
  const [editTarget, setEditTarget] = useState<Card | null>(null);
  const [sheetTargetID, setSheetTargetID] = useState<string | null>(null);
  const [batchMode, setBatchMode] = useState(false);
  const [selectedReviewIDs, setSelectedReviewIDs] = useState<Set<string>>(
    () => new Set(),
  );

  const pinMutation = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) =>
      api.pinCard(id, pinned),
    onMutate: ({ id, pinned }) => {
      const target = cards.find((item) => item.id === id);
      const prevPinnedAt = target?.pinned_at;
      patchCachedCard(queryClient, id, target?.project_id, {
        pinned_at: pinned ? new Date().toISOString() : undefined,
      });
      return { prevPinnedAt, cardProjectID: target?.project_id };
    },
    onSuccess: (updated) => {
      patchCachedCard(queryClient, updated.id, updated.project_id, updated);
      void queryClient.invalidateQueries({ queryKey: ["cards"] });
    },
    onError: (_err, variables, context) => {
      patchCachedCard(queryClient, variables.id, context?.cardProjectID, {
        pinned_at: context?.prevPinnedAt,
      });
    },
  });

  const muteMutation = useCardNotificationMute();
  const readStateMutation = useCardReadState();
  const completeReviewMutation = useCompleteReviewCard();
  const reviewGroupCards = useMemo(
    () => groups.find((item) => item.group === "review")?.cards || [],
    [groups],
  );
  const reviewProjectID = reviewGroupCards[0]?.project_id || workCards[0]?.project_id;
  const batchComplete = useBatchCompleteReviewCards(reviewProjectID);
  const reviewIDs = useMemo(
    () => reviewGroupCards.map((card) => card.id),
    [reviewGroupCards],
  );
  const isBatchMode = batchMode && reviewIDs.length > 0;
  const effectiveSelectedIDs = useMemo(
    () =>
      isBatchMode
        ? reconcileReviewSelection(selectedReviewIDs, reviewIDs)
        : new Set<string>(),
    [isBatchMode, reviewIDs, selectedReviewIDs],
  );
  const allReviewSelected = isAllReviewSelected(effectiveSelectedIDs, reviewIDs);

  const exitBatchMode = useCallback(() => {
    setBatchMode(false);
    setSelectedReviewIDs(new Set());
  }, []);

  const enterBatchMode = useCallback(() => {
    setSelectedReviewIDs(new Set());
    setBatchMode(true);
    if (!expandedGroups.includes("review")) {
      setManualExpansion({
        activeKey,
        expandedGroups: [...expandedGroups, "review"],
      });
    }
  }, [activeKey, expandedGroups]);

  const handleBatchSubmit = useCallback(async () => {
    const ids = Array.from(effectiveSelectedIDs);
    if (ids.length === 0) return;
    const result = await batchComplete.completeBatch(ids);
    if (result.failedIDs.length === 0) {
      exitBatchMode();
    } else {
      setSelectedReviewIDs(new Set(result.failedIDs));
    }
  }, [batchComplete, effectiveSelectedIDs, exitBatchMode]);

  const sheetTarget = useMemo(
    () => workCards.find((item) => item.id === sheetTargetID) || null,
    [workCards, sheetTargetID],
  );

  const handleSelect = (card: Card) => {
    activeCardAutoReadSuppression.clearSuppressed(card.id);
    if (card.id === activeCardID && card.marked_unread) {
      readStateMutation.mutate({
        id: card.id,
        unread: false,
        projectID: card.project_id,
        isActive: true,
      });
    }
    onSelect(card);
  };

  const deleteMutation = useMutation({
    mutationFn: (card: Card) => api.deleteCard(card.id),
    onSuccess: (_, deletedCard) => {
      removeCachedCard(queryClient, deletedCard);
      void queryClient.invalidateQueries({ queryKey: ["cards"] });
      setDeleteTarget(null);
    },
  });

  if (loading && cards.length === 0) {
    return (
      <ContextSidebarSkeleton
        ariaLabel={t("任务列表")}
        className={className}
      />
    );
  }

  return (
    <ContextSidebar
      ariaLabel={t("任务列表")}
      className={className}
      hideHeaderOnMobile
      header={
        <div className="px-1 text-xs font-medium text-muted-foreground">
          {t("任务")}
        </div>
      }
    >
      <div className="space-y-1.5">
        {groups.map(({ group, cards: groupCards }) => {
          const isExpanded = expandedGroups.includes(group);
          const containsActive = activeGroup === group;
          const isReviewGroup = group === "review";
          const showReviewBatch = isReviewGroup && isBatchMode;
          return (
            <section key={group}>
              <div className="flex items-center gap-1">
                <button
                  type="button"
                  aria-expanded={isExpanded}
                  onClick={() =>
                    setManualExpansion({
                      activeKey,
                      expandedGroups: isExpanded
                        ? expandedGroups.filter((g) => g !== group)
                        : [...expandedGroups, group],
                    })
                  }
                  className={`flex min-w-0 flex-1 items-center gap-2 rounded-xl px-3 py-2.5 text-left transition ${
                    containsActive
                      ? "text-heading"
                      : "text-secondary-foreground hover:bg-surface/70"
                  }`}
                >
                  {isExpanded ? (
                    <ChevronDown className="size-4 shrink-0 text-faint" />
                  ) : (
                    <ChevronRight className="size-4 shrink-0 text-faint" />
                  )}
                  <span className="min-w-0 flex-1 truncate text-sm font-normal">
                    {labelFor(group)}
                  </span>
                  <Badge
                    variant="secondary"
                    className="px-1.5 py-0 text-[11px] font-normal text-faint"
                  >
                    {groupCards.length}
                  </Badge>
                </button>
                {isReviewGroup && groupCards.length > 0 && (
                  <button
                    type="button"
                    onClick={() => {
                      if (showReviewBatch) {
                        exitBatchMode();
                      } else {
                        enterBatchMode();
                      }
                    }}
                    className={`inline-flex h-7 shrink-0 items-center gap-1 rounded-lg px-2 text-[11px] font-medium whitespace-nowrap transition-colors ${
                      showReviewBatch
                        ? "bg-emerald-500/12 text-emerald-700 hover:bg-emerald-500/20 dark:text-emerald-300"
                        : "text-muted-foreground hover:bg-surface/80 hover:text-emerald-700 dark:hover:text-emerald-300"
                    }`}
                  >
                    <ListChecks className="size-3.5" />
                    <span>{showReviewBatch ? t("取消") : t("批量验收")}</span>
                  </button>
                )}
              </div>

              {isExpanded ? (
                <div className="mt-1 space-y-1 pl-2">
                  {showReviewBatch && (
                    <div className="mb-1.5 rounded-xl border border-emerald-500/25 bg-emerald-500/6 p-2">
                      <div className="flex items-center justify-between gap-1.5">
                        <button
                          type="button"
                          disabled={batchComplete.isPending}
                          onClick={() =>
                            setSelectedReviewIDs((prev) =>
                              toggleSelectAllReview(
                                reconcileReviewSelection(prev, reviewIDs),
                                reviewIDs,
                              ),
                            )
                          }
                          className="inline-flex h-7 items-center gap-1.5 rounded-md px-1.5 text-[11px] font-medium text-foreground transition hover:bg-surface/80 disabled:opacity-50"
                        >
                          <span
                            aria-hidden="true"
                            className={`grid size-3.5 place-items-center rounded border transition-colors ${
                              allReviewSelected
                                ? "border-emerald-600 bg-emerald-600 text-white"
                                : "border-line bg-surface text-transparent"
                            }`}
                          >
                            <Check className="size-2.5 stroke-[2.5]" />
                          </span>
                          <span>
                            {allReviewSelected ? t("取消全选") : t("全选")}
                          </span>
                        </button>
                        <button
                          type="button"
                          disabled={
                            batchComplete.isPending ||
                            effectiveSelectedIDs.size === 0
                          }
                          onClick={() => void handleBatchSubmit()}
                          className="inline-flex h-7 items-center gap-1 rounded-lg bg-emerald-600 px-2.5 text-[11px] font-medium text-white shadow-2xs transition hover:bg-emerald-700 disabled:opacity-50"
                        >
                          {batchComplete.isPending ? (
                            <Loader2 className="size-3 animate-spin" />
                          ) : (
                            <CheckCircle2 className="size-3" />
                          )}
                          <span>
                            {t("完成所选 ({{count}})", {
                              count: effectiveSelectedIDs.size,
                            })}
                          </span>
                        </button>
                      </div>
                    </div>
                  )}
                  {groupCards.length ? (
                    groupCards.map((card) => {
                      const active = card.id === activeCardID;
                      const isMuted = Boolean(card.notifications_muted);
                      const isSelectedInBatch =
                        showReviewBatch && effectiveSelectedIDs.has(card.id);
                      const swipeActions = buildCardSwipeActions({
                        card,
                        isActive: active,
                        t,
                        onComplete: isReviewTodoCard(card)
                          ? () => {
                              completeReviewMutation.mutate({
                                id: card.id,
                                projectID: card.project_id,
                              });
                            }
                          : undefined,
                        onToggleRead: (nextUnread) => {
                          readStateMutation.mutate({
                            id: card.id,
                            unread: nextUnread,
                            projectID: card.project_id,
                            isActive: active,
                          });
                        },
                        onTogglePin: () => {
                          pinMutation.mutate({
                            id: card.id,
                            pinned: !card.pinned_at,
                          });
                        },
                        onDelete: () => {
                          setDeleteTarget(card);
                        },
                      });

                      return (
                        <ContextSidebarItem
                          key={card.id}
                          active={showReviewBatch ? isSelectedInBatch : active}
                          pinned={Boolean(card.pinned_at)}
                          muted={isMuted}
                          onClick={() => {
                            if (showReviewBatch) {
                              setSelectedReviewIDs((prev) =>
                                toggleReviewSelection(prev, card.id),
                              );
                              return;
                            }
                            handleSelect(card);
                          }}
                          onLongPress={
                            showReviewBatch
                              ? undefined
                              : () => setSheetTargetID(card.id)
                          }
                          swipeActions={showReviewBatch ? undefined : swipeActions}
                          icon={
                            showReviewBatch ? (
                              <span
                                role="checkbox"
                                aria-checked={isSelectedInBatch}
                                className={`grid size-4 place-items-center rounded border transition-colors ${
                                  isSelectedInBatch
                                    ? "border-emerald-600 bg-emerald-600 text-white"
                                    : "border-line bg-surface text-transparent"
                                }`}
                              >
                                <Check className="size-3 stroke-[2.5]" />
                              </span>
                            ) : (
                              <CardStatusDot
                                status={getCardVisualStatus(
                                  card,
                                  active,
                                  activeAttemptStatus,
                                )}
                                muted={isMuted}
                              />
                            )
                          }
                          title={card.title || t("未命名任务")}
                          actions={
                            showReviewBatch ? undefined : (
                              <div className="flex items-center gap-0.5">
                                {isReviewTodoCard(card) && (
                                  <button
                                    type="button"
                                    disabled={completeReviewMutation.isPending}
                                    onClick={(e) => {
                                      e.stopPropagation();
                                      completeReviewMutation.mutate({
                                        id: card.id,
                                        projectID: card.project_id,
                                      });
                                    }}
                                    title={t("验收完成")}
                                    aria-label={t("验收完成")}
                                    className="grid size-6 place-items-center rounded-lg text-emerald-600 transition-all hover:bg-emerald-500/12 hover:text-emerald-700 disabled:opacity-50 dark:text-emerald-400 dark:hover:text-emerald-300"
                                  >
                                    <CheckCircle2 className="size-3.5" />
                                  </button>
                                )}
                                <button
                                  type="button"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    pinMutation.mutate({
                                      id: card.id,
                                      pinned: !card.pinned_at,
                                    });
                                  }}
                                  title={
                                    card.pinned_at ? t("取消置顶") : t("置顶")
                                  }
                                  aria-label={
                                    card.pinned_at ? t("取消置顶") : t("置顶")
                                  }
                                  className={`grid size-6 place-items-center rounded-lg transition-all ${
                                    card.pinned_at
                                      ? "text-amber-500 hover:bg-muted hover:text-amber-600"
                                      : "text-muted-foreground/60 hover:bg-muted hover:text-foreground"
                                  }`}
                                >
                                  {card.pinned_at ? (
                                    <PinOff className="size-3.5" />
                                  ) : (
                                    <Pin className="size-3.5" />
                                  )}
                                </button>
                                <CardActions
                                  card={card}
                                  menuSide="right"
                                  isActive={active}
                                  onDeleted={() => {
                                    if (card.id === activeCardID) {
                                      navigate(
                                        `/board/${encodeURIComponent(card.project_id)}`,
                                      );
                                    }
                                  }}
                                />
                              </div>
                            )
                          }
                        />
                      );
                    })
                  ) : (
                    <div className="mx-1 rounded-xl border border-dashed border-line/60 bg-surface/40 px-3 py-4 text-center text-xs text-faint">
                      {t("该状态暂无任务")}
                    </div>
                  )}
                </div>
              ) : null}
            </section>
          );
        })}
      </div>

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t("删除这个任务？")}
        description={t(
          "删除任务将同步清理其所有执行尝试和产物，该操作无法撤销。",
        )}
        confirmLabel={t("删除任务")}
        pending={deleteMutation.isPending}
        onConfirm={() => {
          if (deleteTarget) {
            deleteMutation.mutate(deleteTarget);
          }
        }}
      />

      <EditCardDialog
        card={editTarget}
        open={Boolean(editTarget)}
        onOpenChange={(open) => !open && setEditTarget(null)}
      />

      <CardRowActionSheet
        card={sheetTarget}
        open={Boolean(sheetTarget)}
        onOpenChange={(open) => !open && setSheetTargetID(null)}
        isActive={Boolean(sheetTarget && sheetTarget.id === activeCardID)}
        completePending={completeReviewMutation.isPending}
        readPending={readStateMutation.isPending}
        pinPending={pinMutation.isPending}
        mutePending={muteMutation.isPending}
        deletePending={deleteMutation.isPending}
        onComplete={(target) => {
          completeReviewMutation.mutate({
            id: target.id,
            projectID: target.project_id,
          });
        }}
        onToggleRead={(target, nextUnread) => {
          readStateMutation.mutate({
            id: target.id,
            unread: nextUnread,
            projectID: target.project_id,
            isActive: target.id === activeCardID,
          });
        }}
        onTogglePin={(target) => {
          pinMutation.mutate({
            id: target.id,
            pinned: !target.pinned_at,
          });
        }}
        onToggleMute={(target) => {
          muteMutation.mutate({
            id: target.id,
            muted: !target.notifications_muted,
            projectID: target.project_id,
          });
        }}
        onEdit={(target) => setEditTarget(target)}
        onDelete={(target) => setDeleteTarget(target)}
      />
    </ContextSidebar>
  );
}
