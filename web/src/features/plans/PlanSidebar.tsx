import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  CalendarClock,
  ChevronDown,
  ChevronRight,
  CirclePause,
  LayoutGrid,
  Pin,
  PinOff,
  Search,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { Card } from "../../shared/api/types";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import {
  patchCachedCard,
  removeCachedCard,
} from "../../shared/lib/card-cache";
import { useCardNotificationMute } from "../../shared/lib/useCardNotificationMute";
import { activeCardAutoReadSuppression } from "../../shared/lib/card-read-state";
import { useCardReadState } from "../../shared/lib/useCardReadState";
import {
  ContextSidebar,
  ContextSidebarItem,
  ContextSidebarSkeleton,
} from "../../shared/ui/ContextSidebar";
import {
  Badge,
  Button,
  ConfirmDialog,
  HintTooltip,
  TextInput,
} from "../../shared/ui/components";
import { EditCardDialog } from "../../shared/ui/EditCardDialog";
import {
  CardStatusDot,
  getCardVisualStatus,
} from "../../shared/ui/CardStatusDot";
import { OngoingHealthBadge } from "../../shared/ui/OngoingHealthBadge";
import { CardActions } from "../cards/CardActions";
import { CardRowActionSheet } from "../cards/CardRowActionSheet";
import { buildCardSwipeActions } from "../cards/card-swipe-actions";
import {
  buildPlanSidebarGroups,
  buildPlanSidebarSubtitle,
  getDefaultExpandedPlanGroups,
  groupSchedulesByCard,
  type PlanSidebarGroupKey,
} from "./plan-sidebar-model";

export function PlanSidebar({
  cards,
  projectID,
  activeCardID,
  activeAttemptStatus,
  onSelect,
  onOpenOverview,
  loading = false,
  className = "",
}: {
  cards: Card[];
  projectID?: string;
  activeCardID: string;
  activeAttemptStatus?: string;
  onSelect: (card: Card) => void;
  onOpenOverview?: () => void;
  loading?: boolean;
  className?: string;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [term, setTerm] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<Card | null>(null);
  const [editTarget, setEditTarget] = useState<Card | null>(null);
  const [sheetTargetID, setSheetTargetID] = useState<string | null>(null);

  const ongoingCards = useMemo(
    () => cards.filter((card) => card.kind === "ongoing"),
    [cards],
  );
  const resolvedProjectID = projectID || ongoingCards[0]?.project_id || "";

  // 复用项目排程缓存，不在侧边栏引入新接口。
  const schedulesQuery = useQuery({
    queryKey: keys.schedules(resolvedProjectID),
    queryFn: () => api.projectSchedules(resolvedProjectID),
    enabled: Boolean(resolvedProjectID),
  });
  const schedulesByCard = useMemo(
    () => groupSchedulesByCard(schedulesQuery.data),
    [schedulesQuery.data],
  );

  const groups = useMemo(
    () =>
      buildPlanSidebarGroups(ongoingCards, {
        searchQuery: term,
        activeCardID,
        activeAttemptStatus,
      }),
    [ongoingCards, term, activeCardID, activeAttemptStatus],
  );

  const hasSearch = term.trim().length > 0;
  const activeGroupKey = useMemo(
    () =>
      groups.find((group) =>
        group.cards.some((card) => card.id === activeCardID),
      )?.key || "",
    [groups, activeCardID],
  );
  const expansionContextKey = `${activeCardID || ""}:${activeGroupKey}:${hasSearch ? "search" : "browse"}`;
  const defaultExpandedGroups = useMemo(
    () =>
      getDefaultExpandedPlanGroups(groups, {
        activeCardID,
        searchQuery: term,
      }),
    [groups, activeCardID, term],
  );
  const [manualExpansion, setManualExpansion] = useState<{
    contextKey: string;
    expandedGroups: PlanSidebarGroupKey[];
  } | null>(null);
  const expandedGroups =
    manualExpansion?.contextKey === expansionContextKey
      ? manualExpansion.expandedGroups
      : defaultExpandedGroups;

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
  const sheetTarget = useMemo(
    () => cards.find((item) => item.id === sheetTargetID) || null,
    [cards, sheetTargetID],
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

  const handleOpenOverview = () => {
    if (onOpenOverview) {
      onOpenOverview();
      return;
    }
    navigate(
      resolvedProjectID
        ? `/plans?project=${encodeURIComponent(resolvedProjectID)}`
        : "/plans",
    );
  };

  const deleteMutation = useMutation({
    mutationFn: (card: Card) => api.deleteCard(card.id),
    onSuccess: (_, deletedCard) => {
      removeCachedCard(queryClient, deletedCard);
      void queryClient.invalidateQueries({ queryKey: ["cards"] });
      setDeleteTarget(null);
    },
  });

  const labelForGroup = (key: PlanSidebarGroupKey) => {
    switch (key) {
      case "attention":
        return t("需关注");
      case "active":
        return t("运行中");
      case "inactive":
        return t("已暂停 / 已归档");
    }
  };

  if (loading && cards.length === 0) {
    return (
      <ContextSidebarSkeleton
        ariaLabel={t("定时列表")}
        className={className}
      />
    );
  }

  const visibleCount = groups.reduce((sum, group) => sum + group.cards.length, 0);
  const schedulesReady =
    !resolvedProjectID ||
    schedulesQuery.data !== undefined ||
    schedulesQuery.isError;

  return (
    <ContextSidebar
      ariaLabel={t("定时任务列表")}
      className={className}
      header={
        <>
          <div className="hidden items-center justify-between gap-2 px-1 md:flex">
            <div className="flex items-center gap-1.5">
              <span className="text-xs font-semibold text-foreground/80 tracking-wide">
                {t("定时任务")}
              </span>
              {visibleCount > 0 && (
                <span className="rounded-full bg-muted/80 px-1.5 py-0.2 text-[10.5px] font-medium tabular-nums text-muted-foreground">
                  {visibleCount}
                </span>
              )}
            </div>
            <HintTooltip content={t("查看定时任务总览")} side="bottom">
              <Button
                variant="ghost"
                size="xs"
                onClick={handleOpenOverview}
                className="h-7 gap-1 rounded-lg px-2 text-xs font-medium text-muted-foreground hover:bg-surface hover:text-foreground hover:shadow-2xs transition-all"
              >
                <LayoutGrid className="size-3.5" />
                <span>{t("总览")}</span>
              </Button>
            </HintTooltip>
          </div>
          <div className="flex items-center gap-1.5">
            <label className="relative block min-w-0 flex-1">
              <span className="sr-only">{t("搜索定时任务…")}</span>
              <Search className="pointer-events-none absolute top-2 left-2.5 size-3.5 text-muted-foreground/50" />
              <TextInput
                value={term}
                onChange={(event) => setTerm(event.target.value)}
                placeholder={t("搜索定时任务…")}
                className="h-7.5 rounded-lg border-transparent bg-muted/50 pl-8 pr-7 text-xs shadow-none placeholder:text-muted-foreground/50 transition-all focus:bg-surface focus:border-line/70 focus:shadow-2xs"
              />
              {term ? (
                <button
                  type="button"
                  aria-label={t("清空搜索")}
                  onClick={() => setTerm("")}
                  className="absolute top-1.5 right-1.5 grid size-4.5 place-items-center rounded text-muted-foreground/70 hover:bg-muted hover:text-foreground"
                >
                  <X className="size-3" />
                </button>
              ) : null}
            </label>
            <Button
              variant="ghost"
              size="xs"
              aria-label={t("总览")}
              onClick={handleOpenOverview}
              className="h-7.5 shrink-0 gap-1 rounded-lg border border-line/70 bg-surface px-2.5 text-xs font-medium text-muted-foreground shadow-2xs transition-all hover:bg-muted hover:text-foreground md:hidden"
            >
              <LayoutGrid className="size-3.5" />
              <span>{t("总览")}</span>
            </Button>
          </div>
        </>
      }
    >
      {groups.length > 0 ? (
        <div className="space-y-1.5">
          {groups.map((group) => {
            const isExpanded = expandedGroups.includes(group.key);
            const containsActive = activeGroupKey === group.key;
            const isAttention = group.key === "attention";
            const hasCritical =
              isAttention &&
              group.cards.some((card) => card.health === "critical");

            return (
              <section key={group.key} data-plan-group={group.key}>
                <button
                  type="button"
                  aria-expanded={isExpanded}
                  onClick={() =>
                    setManualExpansion({
                      contextKey: expansionContextKey,
                      expandedGroups: isExpanded
                        ? expandedGroups.filter((key) => key !== group.key)
                        : [...expandedGroups, group.key],
                    })
                  }
                  className={`flex w-full items-center gap-2 rounded-xl px-2.5 py-2 text-left transition ${
                    containsActive
                      ? "text-heading"
                      : "text-secondary-foreground hover:bg-surface/70"
                  }`}
                >
                  {isExpanded ? (
                    <ChevronDown className="size-3.5 shrink-0 text-faint" />
                  ) : (
                    <ChevronRight className="size-3.5 shrink-0 text-faint" />
                  )}
                  <span
                    className={`min-w-0 flex-1 truncate text-xs font-medium ${
                      isAttention
                        ? hasCritical
                          ? "text-rose-600 dark:text-rose-400"
                          : "text-amber-700 dark:text-amber-400"
                        : ""
                    }`}
                  >
                    {labelForGroup(group.key)}
                  </span>
                  <Badge
                    variant="secondary"
                    className={`px-1.5 py-0 text-[11px] font-normal tabular-nums ${
                      isAttention
                        ? hasCritical
                          ? "bg-rose-500/12 text-rose-600 dark:text-rose-400"
                          : "bg-amber-500/12 text-amber-700 dark:text-amber-400"
                        : "text-faint"
                    }`}
                  >
                    {group.cards.length}
                  </Badge>
                </button>

                {isExpanded ? (
                  <div className="mt-0.5 space-y-0.5 pl-1.5">
                    {group.cards.map((card) => {
                      const isMuted = Boolean(card.notifications_muted);
                      const isActive = card.id === activeCardID;
                      const isInactive =
                        card.ongoing_state === "paused" ||
                        card.ongoing_state === "archived";
                      const swipeActions = buildCardSwipeActions({
                        card,
                        isActive,
                        t,
                        onToggleRead: (nextUnread) => {
                          readStateMutation.mutate({
                            id: card.id,
                            unread: nextUnread,
                            projectID: card.project_id,
                            isActive,
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

                      const visualStatus = getCardVisualStatus(
                        card,
                        isActive,
                        activeAttemptStatus,
                      );

                      const cardSchedules = schedulesByCard.get(card.id) || [];
                      const subtitleInfo =
                        isInactive || schedulesReady
                          ? buildPlanSidebarSubtitle(card, cardSchedules, { t })
                          : null;
                      const subtitleFullText = subtitleInfo
                        ? subtitleInfo.hasFailedAttempt &&
                          subtitleInfo.failedLabel
                          ? subtitleInfo.text
                            ? `${subtitleInfo.failedLabel} · ${subtitleInfo.text}`
                            : subtitleInfo.failedLabel
                          : subtitleInfo.text
                        : undefined;

                      // 胶囊降噪：仅「需关注」展示醒目健康徽章，「已暂停/已归档」展示轻量状态标签；
                      // 「运行中」正常卡片不放右侧胶囊，把横向空间还给标题。
                      const itemMeta =
                        group.key === "attention" ? (
                          <OngoingHealthBadge health={card.health} size="xs" />
                        ) : isInactive ? (
                          <span className="inline-flex items-center rounded-full bg-muted/80 px-1.5 py-0.5 text-[10px] font-medium leading-none text-muted-foreground">
                            {card.ongoing_state === "archived"
                              ? t("已归档")
                              : t("已暂停")}
                          </span>
                        ) : undefined;

                      const itemIcon = isInactive ? (
                        visualStatus === "answering" ? (
                          <CardStatusDot status="answering" />
                        ) : (
                          <CirclePause className="size-4 text-faint" />
                        )
                      ) : visualStatus !== "idle" ? (
                        <CardStatusDot status={visualStatus} />
                      ) : (
                        <CalendarClock className="size-4 text-faint" />
                      );

                      return (
                        <ContextSidebarItem
                          key={card.id}
                          active={isActive}
                          pinned={Boolean(card.pinned_at)}
                          muted={isMuted}
                          onClick={() => handleSelect(card)}
                          onLongPress={() => setSheetTargetID(card.id)}
                          swipeActions={swipeActions}
                          icon={itemIcon}
                          title={card.title || t("未命名定时任务")}
                          subtitleTitle={subtitleFullText}
                          subtitle={
                            subtitleInfo ? (
                              subtitleInfo.hasFailedAttempt &&
                              subtitleInfo.failedLabel ? (
                                <>
                                  <span className="text-danger">
                                    {subtitleInfo.failedLabel}
                                  </span>
                                  {subtitleInfo.text ? (
                                    <>
                                      <span aria-hidden className="mx-1 text-faint">
                                        ·
                                      </span>
                                      <span>{subtitleInfo.text}</span>
                                    </>
                                  ) : null}
                                </>
                              ) : (
                                subtitleInfo.text
                              )
                            ) : undefined
                          }
                          meta={itemMeta}
                          actions={
                            <div className="flex items-center gap-0.5">
                              <button
                                type="button"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  pinMutation.mutate({
                                    id: card.id,
                                    pinned: !card.pinned_at,
                                  });
                                }}
                                title={card.pinned_at ? t("取消置顶") : t("置顶")}
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
                                isActive={isActive}
                                onDeleted={() => {
                                  if (card.id === activeCardID) {
                                    navigate(
                                      `/plans/${encodeURIComponent(card.project_id)}`,
                                    );
                                  }
                                }}
                              />
                            </div>
                          }
                        />
                      );
                    })}
                  </div>
                ) : null}
              </section>
            );
          })}
        </div>
      ) : (
        <div className="mx-1 rounded-xl border border-dashed border-line/60 bg-surface/40 px-4 py-8 text-center">
          <CalendarClock className="mx-auto mb-2 size-5 text-faint/70" />
          <p className="text-xs text-faint">
            {hasSearch
              ? t("没有匹配的定时任务")
              : t("这个项目还没有定时任务")}
          </p>
        </div>
      )}

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t("删除这个定时任务？")}
        description={t(
          "删除定时任务将同步清理其所有执行记录和周期规则，该操作无法撤销。",
        )}
        confirmLabel={t("删除定时任务")}
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
        readPending={readStateMutation.isPending}
        pinPending={pinMutation.isPending}
        mutePending={muteMutation.isPending}
        deletePending={deleteMutation.isPending}
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
