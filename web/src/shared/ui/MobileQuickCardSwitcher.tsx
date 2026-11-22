import { useState, useMemo, useCallback, useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  BellOff,
  Check,
  Layers,
  ListChecks,
  ListTodo,
  Loader2,
  MessageSquare,
  PlayCircle,
  Search,
  ShieldCheck,
  CheckCircle2,
  CalendarClock,
  Pin,
  Sparkles,
  X,
} from "lucide-react";
import {
  Sheet as UISheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "./sheet";
import type { Card } from "../api/types";
import { api } from "../api/client";
import { keys } from "../../app/query";
import {
  CardStatusDot,
  getCardVisualStatus,
  type CardVisualStatus,
} from "./CardStatusDot";
import { buildFocusSummary, getCardFocusReason } from "../lib/card-focus";
import { patchCachedCard, removeCachedCard } from "../lib/card-cache";
import { useCardNotificationMute } from "../lib/useCardNotificationMute";
import { activeCardAutoReadSuppression } from "../lib/card-read-state";
import { useCardReadState } from "../lib/useCardReadState";
import {
  isAllReviewSelected,
  isReviewTodoCard,
  reconcileReviewSelection,
  toggleReviewSelection,
  toggleSelectAllReview,
} from "../lib/review-acceptance";
import {
  useBatchCompleteReviewCards,
  useCompleteReviewCard,
} from "../lib/useCompleteReviewCard";
import { CardRowActionSheet } from "../../features/cards/CardRowActionSheet";
import { buildCardSwipeActions } from "../../features/cards/card-swipe-actions";
import { ConfirmDialog } from "./components";
import { EditCardDialog } from "./EditCardDialog";
import { SwipeActionGroup, SwipeableRow } from "./SwipeableRow";
import { cn } from "../lib/utils";
import {
  QUICK_SWITCHER_TAB_ORDER,
  type SwitcherTab,
} from "../lib/quick-switcher-tabs";

export interface QuickCardSwitcherTriggerProps {
  projectID: string;
  activeCardID?: string;
  activeAttemptStatus?: string;
  className?: string;
  onClick: () => void;
}

/**
 * 快速切换入口按钮
 * 仅负责渲染触发按钮与全局焦点（待确认 / 巡检异常 / 正在运行 / 未读回复 / 待验收 / 进行中 / 已置顶）角标，
 * 支持在移动端顶栏、桌面端工具栏等不同布局区域多次复用，避免重复挂载底层 SheetPortal 与全局键盘监听。
 */
export function QuickCardSwitcherTrigger({
  projectID,
  activeCardID,
  activeAttemptStatus,
  className = "",
  onClick,
}: QuickCardSwitcherTriggerProps) {
  const { t } = useTranslation();
  const cardsQuery = useQuery({
    queryKey: keys.cards(projectID),
    queryFn: () => api.cards(projectID),
    enabled: Boolean(projectID),
  });

  const cards: Card[] = useMemo(() => cardsQuery.data || [], [cardsQuery.data]);

  const focusSummary = useMemo(
    () => buildFocusSummary(cards, activeCardID, activeAttemptStatus),
    [cards, activeCardID, activeAttemptStatus],
  );

  const activeBadgeCount = focusSummary.cards.length;
  const badgeColorClass =
    focusSummary.highestVisualStatus === "answering"
      ? "bg-emerald-500 text-white shadow-[0_0_8px_rgba(16,185,129,0.5)]"
      : focusSummary.highestVisualStatus === "approval"
        ? "bg-orange-500 text-white shadow-[0_0_6px_rgba(249,115,22,0.45)]"
        : focusSummary.highestVisualStatus === "critical"
          ? "bg-rose-500 text-white shadow-[0_0_6px_rgba(244,63,94,0.45)]"
          : focusSummary.highestVisualStatus === "warning"
            ? "bg-amber-500 text-white shadow-[0_0_6px_rgba(245,158,11,0.45)]"
            : focusSummary.highestVisualStatus === "unread"
              ? "bg-sky-500 text-white shadow-[0_0_6px_rgba(14,165,233,0.45)]"
              : "bg-primary text-primary-foreground shadow";

  return (
    <button
      type="button"
      title={t("快速切换卡片 (⌘K)")}
      aria-label={t("快速切换卡片 (⌘K)")}
      onClick={onClick}
      className={cn(
        "relative inline-flex size-8 shrink-0 items-center justify-center rounded-lg border border-line bg-surface text-muted-foreground shadow-xs transition hover:bg-muted hover:text-foreground",
        className,
      )}
    >
      <Layers className="size-4" />
      {activeBadgeCount > 0 && (
        <span
          className={cn(
            "absolute -top-1 -right-1 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-bold tabular-nums",
            badgeColorClass,
          )}
        >
          {activeBadgeCount > 99 ? "99+" : activeBadgeCount}
        </span>
      )}
    </button>
  );
}

export interface QuickCardSwitcherModalProps {
  projectID: string;
  activeCardID?: string;
  activeAttemptStatus?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/**
 * 快速切换弹窗本体
 * 负责全局 ⌘K/Ctrl+K 快捷键、Tab 分类筛选、滑动操作与卡片跳转导航。
 * 移动端以标准底部 Sheet 呈现，桌面端转换为居中命令面板，且动画采用平滑渐隐缩放以消除位移冲突与掉帧。
 */
export function QuickCardSwitcherModal({
  projectID,
  activeCardID,
  activeAttemptStatus,
  open,
  onOpenChange,
}: QuickCardSwitcherModalProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const cardsQuery = useQuery({
    queryKey: keys.cards(projectID),
    queryFn: () => api.cards(projectID),
    enabled: Boolean(projectID),
  });

  const cards: Card[] = useMemo(() => cardsQuery.data || [], [cardsQuery.data]);

  // 分组统计与列表
  const {
    todoCards,
    workingCards,
    reviewCards,
    discussionCards,
    ongoingCards,
    doneCards,
  } = useMemo(() => {
    const todo: Card[] = [];
    const working: Card[] = [];
    const review: Card[] = [];
    const discussions: Card[] = [];
    const ongoing: Card[] = [];
    const done: Card[] = [];

    for (const card of cards) {
      if (card.kind === "discussion") {
        discussions.push(card);
        continue;
      }
      if (card.kind === "todo") {
        if (card.todo_state === "todo") {
          todo.push(card);
        } else if (card.todo_state === "working") {
          working.push(card);
        } else if (card.todo_state === "review") {
          review.push(card);
        } else if (card.todo_state === "done") {
          done.push(card);
        }
      } else if (card.kind === "ongoing") {
        // 定时任务与周期卡片归入专门的定时任务分类
        ongoing.push(card);
      }
    }

    // 排序：有置顶排前面，其次最新更新
    const sortFn = (a: Card, b: Card) => {
      const aPinned = Boolean(a.pinned_at);
      const bPinned = Boolean(b.pinned_at);
      if (aPinned !== bPinned) return aPinned ? -1 : 1;
      if (aPinned && bPinned) {
        return (b.pinned_at || "").localeCompare(a.pinned_at || "");
      }
      return b.updated_at.localeCompare(a.updated_at);
    };

    return {
      todoCards: todo.sort(sortFn),
      workingCards: working.sort(sortFn),
      reviewCards: review.sort(sortFn),
      discussionCards: discussions.sort(sortFn),
      ongoingCards: ongoing.sort(sortFn),
      doneCards: done.sort(sortFn),
    };
  }, [cards]);

  const focusSummary = useMemo(
    () => buildFocusSummary(cards, activeCardID, activeAttemptStatus),
    [cards, activeCardID, activeAttemptStatus],
  );
  const focusCards = focusSummary.cards;
  const activeBadgeCount = focusCards.length;

  const [selectedTab, setSelectedTab] = useState<SwitcherTab | null>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [batchProjectID, setBatchProjectID] = useState<string | null>(null);
  const [selectedReviewIDs, setSelectedReviewIDs] = useState<Set<string>>(
    () => new Set(),
  );
  const searchInputRef = useRef<HTMLInputElement>(null);

  const defaultTab = useMemo((): SwitcherTab => {
    // 只要存在需要关注的焦点卡片（待确认 / 巡检异常 / 正在运行 / 未读回复 / 待验收 / 进行中 / 已置顶），
    // 打开面板时默认定位到「焦点」Tab，让手机上滑或 ⌘K 首屏即可一眼扫完所有动态。
    if (focusCards.length > 0) {
      return "focus";
    }
    if (activeCardID) {
      const currentCard = cards.find((c) => c.id === activeCardID);
      if (currentCard) {
        if (currentCard.kind === "discussion") return "discussion";
        if (currentCard.kind === "ongoing") return "ongoing";
        if (currentCard.kind === "todo") {
          if (currentCard.todo_state === "todo") return "todo";
          if (currentCard.todo_state === "review") return "review";
          if (currentCard.todo_state === "done") return "done";
          return "working";
        }
      }
    }
    return "discussion";
  }, [activeCardID, cards, focusCards.length]);

  const activeTab = selectedTab || defaultTab;

  const exitBatchMode = useCallback(() => {
    setBatchProjectID(null);
    setSelectedReviewIDs(new Set());
  }, []);

  const handleTabChange = useCallback(
    (tab: SwitcherTab) => {
      setSelectedTab(tab);
      if (tab !== "review") {
        exitBatchMode();
      }
    },
    [exitBatchMode],
  );

  const handleOpenChange = useCallback(
    (nextOpen: boolean) => {
      if (!nextOpen) {
        setSelectedTab(null);
        setSearchQuery("");
        exitBatchMode();
      }
      onOpenChange(nextOpen);
    },
    [exitBatchMode, onOpenChange],
  );

  useEffect(() => {
    if (!open) return;
    if (
      typeof window !== "undefined" &&
      window.matchMedia?.("(min-width: 768px)").matches
    ) {
      const raf = requestAnimationFrame(() => {
        searchInputRef.current?.focus();
      });
      return () => cancelAnimationFrame(raf);
    }
  }, [open]);

  const handleSelectCard = (targetCard: Card) => {
    activeCardAutoReadSuppression.clearSuppressed(targetCard.id);
    if (targetCard.id === activeCardID && targetCard.marked_unread) {
      readStateMutation.mutate({
        id: targetCard.id,
        unread: false,
        projectID: targetCard.project_id,
        isActive: true,
      });
    }
    setSelectedTab(null);
    setSearchQuery("");
    onOpenChange(false);
    if (targetCard.kind === "discussion") {
      navigate(`/chat/${targetCard.project_id}/${targetCard.id}`);
    } else if (targetCard.kind === "ongoing") {
      navigate(`/plans/${targetCard.project_id}/${targetCard.id}`);
    } else {
      navigate(`/cards/${targetCard.project_id}/${targetCard.id}`);
    }
  };

  const queryClient = useQueryClient();
  const [deleteTarget, setDeleteTarget] = useState<Card | null>(null);
  const [editTarget, setEditTarget] = useState<Card | null>(null);
  const [sheetTargetID, setSheetTargetID] = useState<string | null>(null);

  const mainDiscussion = useQuery({
    queryKey: keys.mainDiscussion(projectID),
    queryFn: () => api.mainDiscussion(projectID),
    enabled: open && Boolean(projectID),
  });
  const mainDiscussionID = mainDiscussion.data?.id || "";

  const pinMutation = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) =>
      api.pinCard(id, pinned),
    onMutate: ({ id, pinned }) => {
      const target = cards.find((item) => item.id === id);
      const prevPinnedAt = target?.pinned_at;
      patchCachedCard(queryClient, id, projectID, {
        pinned_at: pinned ? new Date().toISOString() : undefined,
      });
      return { prevPinnedAt };
    },
    onSuccess: (updated) => {
      patchCachedCard(queryClient, updated.id, projectID, updated);
      void queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
    },
    onError: (_err, variables, context) => {
      patchCachedCard(queryClient, variables.id, projectID, {
        pinned_at: context?.prevPinnedAt,
      });
    },
  });

  const muteMutation = useCardNotificationMute();
  const readStateMutation = useCardReadState();
  const completeReviewMutation = useCompleteReviewCard();
  const batchComplete = useBatchCompleteReviewCards(projectID);
  const sheetTarget = useMemo(
    () => cards.find((item) => item.id === sheetTargetID) || null,
    [cards, sheetTargetID],
  );

  const deleteMutation = useMutation({
    mutationFn: (card: Card) => api.deleteCard(card.id),
    onSuccess: (_, deletedCard) => {
      removeCachedCard(queryClient, deletedCard);
      void queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
      setDeleteTarget(null);
    },
  });

  const currentList = useMemo(() => {
    const keyword = searchQuery.trim().toLowerCase();
    let baseList: Card[];
    switch (activeTab) {
      case "focus":
        baseList = keyword ? cards : focusCards;
        break;
      case "todo":
        baseList = todoCards;
        break;
      case "working":
        baseList = workingCards;
        break;
      case "review":
        baseList = reviewCards;
        break;
      case "discussion":
        baseList = discussionCards;
        break;
      case "ongoing":
        baseList = ongoingCards;
        break;
      case "done":
        baseList = doneCards;
        break;
      default:
        baseList = [];
    }
    if (!keyword) return baseList;
    return baseList.filter(
      (card) =>
        (card.title || "").toLowerCase().includes(keyword) ||
        (card.description || "").toLowerCase().includes(keyword),
    );
  }, [
    activeTab,
    searchQuery,
    cards,
    focusCards,
    todoCards,
    workingCards,
    reviewCards,
    discussionCards,
    ongoingCards,
    doneCards,
  ]);

  const visibleReviewIDs = useMemo(
    () =>
      activeTab === "review"
        ? currentList.filter((card) => isReviewTodoCard(card)).map((card) => card.id)
        : [],
    [activeTab, currentList],
  );
  // 仅在「待验收」Tab 且存在待验收卡片时保持批量验收态，切换 Tab 或列表清空后自动退出
  const isBatchMode =
    Boolean(projectID) &&
    batchProjectID === projectID &&
    activeTab === "review" &&
    reviewCards.length > 0;
  const effectiveSelectedIDs = useMemo(
    () =>
      isBatchMode
        ? reconcileReviewSelection(selectedReviewIDs, visibleReviewIDs)
        : new Set<string>(),
    [isBatchMode, selectedReviewIDs, visibleReviewIDs],
  );
  const allVisibleReviewSelected = isAllReviewSelected(
    effectiveSelectedIDs,
    visibleReviewIDs,
  );

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

  const tabFocusMeta = useMemo(() => {
    const summarize = (list: Card[]) => {
      const summary = buildFocusSummary(
        list,
        activeCardID,
        activeAttemptStatus,
      );
      return {
        count: summary.cards.length,
        status: summary.highestVisualStatus,
      };
    };
    return {
      focus: {
        count: focusCards.length,
        status: focusSummary.highestVisualStatus,
      },
      discussion: summarize(discussionCards),
      todo: summarize(todoCards),
      working: summarize(workingCards),
      review: summarize(reviewCards),
      done: summarize(doneCards),
      ongoing: summarize(ongoingCards),
    };
  }, [
    activeCardID,
    activeAttemptStatus,
    focusCards.length,
    focusSummary.highestVisualStatus,
    discussionCards,
    todoCards,
    workingCards,
    reviewCards,
    doneCards,
    ongoingCards,
  ]);

  const tabs: {
    id: SwitcherTab;
    label: string;
    icon: typeof PlayCircle;
    count: number;
    badgeStatus: CardVisualStatus;
    colorClasses: string;
  }[] = [
    {
      id: "focus",
      label: t("焦点"),
      icon: Sparkles,
      count: tabFocusMeta.focus.count,
      badgeStatus: tabFocusMeta.focus.status,
      colorClasses:
        "data-[state=active]:text-primary dark:data-[state=active]:text-primary",
    },
    {
      id: "discussion",
      label: t("讨论"),
      icon: MessageSquare,
      count: tabFocusMeta.discussion.count,
      badgeStatus: tabFocusMeta.discussion.status,
      colorClasses:
        "data-[state=active]:text-indigo-600 dark:data-[state=active]:text-indigo-400",
    },
    {
      id: "ongoing",
      label: t("定时任务"),
      icon: CalendarClock,
      count: tabFocusMeta.ongoing.count,
      badgeStatus: tabFocusMeta.ongoing.status,
      colorClasses:
        "data-[state=active]:text-violet-600 dark:data-[state=active]:text-violet-400",
    },
    {
      id: "todo",
      label: t("待办"),
      icon: ListTodo,
      count: tabFocusMeta.todo.count,
      badgeStatus: tabFocusMeta.todo.status,
      colorClasses:
        "data-[state=active]:text-slate-700 dark:data-[state=active]:text-slate-300",
    },
    {
      id: "working",
      label: t("进行中"),
      icon: PlayCircle,
      count: tabFocusMeta.working.count,
      badgeStatus: tabFocusMeta.working.status,
      colorClasses:
        "data-[state=active]:text-sky-600 dark:data-[state=active]:text-sky-400",
    },
    {
      id: "review",
      label: t("待验收"),
      icon: ShieldCheck,
      count: tabFocusMeta.review.count,
      badgeStatus: tabFocusMeta.review.status,
      colorClasses:
        "data-[state=active]:text-amber-600 dark:data-[state=active]:text-amber-400",
    },
    {
      id: "done",
      label: t("已完成"),
      icon: CheckCircle2,
      count: tabFocusMeta.done.count,
      badgeStatus: tabFocusMeta.done.status,
      colorClasses:
        "data-[state=active]:text-emerald-600 dark:data-[state=active]:text-emerald-400",
    },
  ];
  tabs.sort(
    (a, b) =>
      QUICK_SWITCHER_TAB_ORDER.indexOf(a.id) -
      QUICK_SWITCHER_TAB_ORDER.indexOf(b.id),
  );

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // 桌面端快捷键：Cmd+K (macOS) 或 Ctrl+K (Windows/Linux)
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        handleOpenChange(!open);
      }
    };
    const handleOpenEvent = () => handleOpenChange(true);
    window.addEventListener("keydown", handleKeyDown);
    window.addEventListener("cycledock:open-quick-switcher", handleOpenEvent);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      window.removeEventListener(
        "cycledock:open-quick-switcher",
        handleOpenEvent,
      );
    };
  }, [open, handleOpenChange]);

  return (
    <UISheet open={open} onOpenChange={handleOpenChange}>
      <SheetContent
        side="bottom"
        showCloseButton={false}
        aria-describedby={undefined}
        onOpenAutoFocus={(e) => e.preventDefault()}
        className={cn(
          "fixed inset-x-0 bottom-0 z-50 flex max-h-[82vh] h-[70vh] flex-col rounded-t-2xl border-t border-line bg-surface p-0 shadow-2xl focus:outline-none sm:max-w-none",
          // 桌面端（>= 768px）形态转换为居中悬浮命令面板：
          // 1. 使用 inset-x-0 mx-auto 纯盒模型居中，避免 translate-x-[-50%] 与 CSS keyframe exit 的 X 位移冲突
          // 2. 覆盖默认 slide-out-to-bottom 为 slide-out-to-bottom-0，改用渐隐 + 缩放 (fade-out-0 zoom-out-95)
          // 3. 关闭桌面端 transition，使 200ms 的 exit keyframe 动画干净执行，消除双重 transform 过渡导致的掉帧与白屏闪烁
          "md:bottom-auto md:top-[12vh] md:inset-x-0 md:mx-auto md:translate-x-0 md:w-full md:max-w-xl md:h-[600px] md:max-h-[76vh] md:rounded-2xl md:border",
          "md:transition-none md:data-[state=open]:duration-200 md:data-[state=closed]:duration-200",
          "md:data-[state=open]:slide-in-from-bottom-0 md:data-[state=open]:fade-in-0 md:data-[state=open]:zoom-in-95",
          "md:data-[state=closed]:slide-out-to-bottom-0 md:data-[state=closed]:fade-out-0 md:data-[state=closed]:zoom-out-95",
        )}
      >
        {/* 拖动提示条（仅移动端触屏展示） */}
        <div className="flex w-full items-center justify-center pt-2.5 pb-1 md:hidden">
          <span className="h-1.5 w-10 rounded-full bg-muted-foreground/25" />
        </div>

        <SheetHeader className="border-b border-divider px-4 py-2.5 text-left">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <SheetTitle className="text-base font-semibold text-foreground">
                {t("快速切换")}
              </SheetTitle>
              <span className="hidden md:inline-flex items-center rounded border border-line bg-muted/60 px-1.5 py-0.5 text-[10px] font-mono text-muted-foreground">
                ⌘K / Ctrl+K
              </span>
            </div>
            {activeBadgeCount > 0 && (
              <div className="flex items-center gap-1.5">
                {focusSummary.approvalCount > 0 && (
                  <span className="inline-flex items-center gap-1 rounded-full bg-orange-500/15 px-2 py-0.5 text-[11px] font-medium text-orange-700 dark:text-orange-300">
                    <span className="size-1.5 rounded-full bg-orange-500" />
                    {t("{{count}} 待确认", {
                      count: focusSummary.approvalCount,
                    })}
                  </span>
                )}
                {focusSummary.criticalCount > 0 && (
                  <span className="inline-flex items-center gap-1 rounded-full bg-rose-500/15 px-2 py-0.5 text-[11px] font-medium text-rose-700 dark:text-rose-300">
                    <span className="size-1.5 rounded-full bg-rose-500" />
                    {t("{{count}} 异常", {
                      count: focusSummary.criticalCount,
                    })}
                  </span>
                )}
                {focusSummary.warningCount > 0 && (
                  <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:text-amber-300">
                    <span className="size-1.5 rounded-full bg-amber-500" />
                    {t("{{count}} 需留意", {
                      count: focusSummary.warningCount,
                    })}
                  </span>
                )}
                {focusSummary.answeringCount > 0 && (
                  <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/15 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:text-emerald-300">
                    <span className="size-1.5 rounded-full bg-emerald-500 animate-pulse" />
                    {t("{{count}} 运行中", {
                      count: focusSummary.answeringCount,
                    })}
                  </span>
                )}
                {focusSummary.unreadCount > 0 && (
                  <span className="inline-flex items-center gap-1 rounded-full bg-sky-500/15 px-2 py-0.5 text-[11px] font-medium text-sky-700 dark:text-sky-300">
                    <span className="size-1.5 rounded-full bg-sky-500" />
                    {t("{{count}} 未读", { count: focusSummary.unreadCount })}
                  </span>
                )}
                {focusSummary.pinnedCount > 0 && (
                  <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/12 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:text-amber-300">
                    <Pin className="size-2.5 fill-current" />
                    {t("{{count}} 置顶", { count: focusSummary.pinnedCount })}
                  </span>
                )}
                {focusSummary.answeringCount === 0 &&
                  focusSummary.approvalCount === 0 &&
                  focusSummary.criticalCount === 0 &&
                  focusSummary.warningCount === 0 &&
                  focusSummary.unreadCount === 0 &&
                  focusSummary.pinnedCount === 0 && (
                    <span className="rounded-full bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary">
                      {t("{{count}} 个焦点事项", { count: activeBadgeCount })}
                    </span>
                  )}
              </div>
            )}
          </div>

          {/* 搜索过滤框 */}
          <div className="mt-2.5 flex items-center gap-2">
            <div className="relative min-w-0 flex-1">
              <Search className="pointer-events-none absolute top-1/2 left-3 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <input
                ref={searchInputRef}
                type="text"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && currentList.length > 0 && !isBatchMode) {
                    e.preventDefault();
                    handleSelectCard(currentList[0]);
                  }
                }}
                placeholder={t("搜索卡片标题或描述...")}
                aria-label={t("搜索卡片标题或描述...")}
                className="h-8 w-full rounded-lg border border-line/80 bg-canvas/80 pr-8 pl-8 text-xs text-foreground placeholder:text-muted-foreground/70 focus:border-primary/50 focus:bg-surface focus:ring-2 focus:ring-primary/15 focus:outline-none"
              />
              {searchQuery && (
                <button
                  type="button"
                  aria-label={t("清空搜索")}
                  onClick={() => {
                    setSearchQuery("");
                    searchInputRef.current?.focus();
                  }}
                  className="absolute top-1/2 right-2 inline-flex size-5 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
                >
                  <X className="size-3.5" />
                </button>
              )}
            </div>
            {activeTab === "review" && reviewCards.length > 0 && (
              <button
                type="button"
                onClick={() => {
                  if (isBatchMode) {
                    exitBatchMode();
                  } else {
                    setSelectedReviewIDs(new Set());
                    setBatchProjectID(projectID);
                  }
                }}
                className={cn(
                  "inline-flex h-8 shrink-0 items-center gap-1 rounded-lg border px-2.5 text-xs font-medium whitespace-nowrap transition-colors",
                  isBatchMode
                    ? "border-emerald-500/40 bg-emerald-500/12 text-emerald-700 hover:bg-emerald-500/20 dark:text-emerald-300"
                    : "border-line/80 bg-surface text-muted-foreground hover:border-emerald-500/40 hover:bg-emerald-500/8 hover:text-emerald-700 dark:hover:text-emerald-300",
                )}
              >
                <ListChecks className="size-3.5" />
                <span>{isBatchMode ? t("取消") : t("批量验收")}</span>
              </button>
            )}
          </div>

          {/* 分类 Tabs */}
          <div className="mt-2 flex items-center gap-1.5 overflow-x-auto pb-1 scrollbar-none">
            {tabs.map((tab) => {
              const Icon = tab.icon;
              const isActive = activeTab === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  data-state={isActive ? "active" : "inactive"}
                  onClick={() => handleTabChange(tab.id)}
                  className={cn(
                    "flex shrink-0 items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium transition",
                    isActive
                      ? "bg-primary/10 font-semibold text-primary shadow-xs"
                      : "text-muted-foreground hover:bg-muted hover:text-foreground",
                  )}
                >
                  <Icon
                    className={cn("size-3.5", isActive ? "" : "opacity-70")}
                  />
                  <span>{tab.label}</span>
                  {tab.count > 0 && (
                    <span
                      className={cn(
                        "rounded-full px-1.5 py-0.2 text-[10px] font-bold tabular-nums",
                        tab.badgeStatus === "answering"
                          ? "bg-emerald-500 text-white"
                          : tab.badgeStatus === "approval"
                            ? "bg-orange-500 text-white"
                            : tab.badgeStatus === "critical"
                              ? "bg-rose-500 text-white"
                              : tab.badgeStatus === "warning"
                                ? "bg-amber-500 text-white"
                                : tab.badgeStatus === "unread"
                                  ? "bg-sky-500 text-white"
                                  : tab.id === "review"
                                    ? "bg-amber-500/20 text-amber-700 dark:text-amber-300"
                                    : isActive
                                      ? "bg-primary text-primary-foreground"
                                      : "bg-muted text-muted-foreground",
                      )}
                    >
                      {tab.count}
                    </span>
                  )}
                </button>
              );
            })}
          </div>
        </SheetHeader>

        {/* 任务/讨论列表 */}
        <div className="flex-1 overflow-y-auto px-3 py-2.5 min-h-0">
          {currentList.length === 0 ? (
            <div className="flex h-40 flex-col items-center justify-center text-center text-muted-foreground">
              <p className="text-xs">
                {searchQuery.trim()
                  ? t("未找到匹配的卡片")
                  : activeTab === "focus"
                    ? t("当前没有待确认、正在运行、未读、待处理或置顶的事项")
                    : t("该分类下暂无卡片")}
              </p>
            </div>
          ) : (
            <SwipeActionGroup className="flex flex-col gap-2 p-1">
              {currentList.map((card) => {
                const isActive = card.id === activeCardID;
                const isMain = card.id === mainDiscussionID;
                const isPinned = isMain || Boolean(card.pinned_at);
                const isMuted = Boolean(card.notifications_muted);
                const visualStatus = getCardVisualStatus(
                  card,
                  isActive,
                  isActive ? activeAttemptStatus : undefined,
                );
                const focusReason = getCardFocusReason(
                  card,
                  isActive,
                  isActive ? activeAttemptStatus : undefined,
                );
                const kindLabel =
                  card.kind === "discussion"
                    ? t("讨论")
                    : card.kind === "ongoing"
                      ? t("定时")
                      : t("任务");

                const swipeActions = buildCardSwipeActions({
                  card,
                  isMain,
                  isActive,
                  t,
                  onComplete:
                    !isMain && isReviewTodoCard(card)
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
                      isActive,
                    });
                  },
                  onTogglePin: !isMain
                    ? () => {
                        pinMutation.mutate({
                          id: card.id,
                          pinned: !card.pinned_at,
                        });
                      }
                    : undefined,
                  onToggleMute: () => {
                    muteMutation.mutate({
                      id: card.id,
                      muted: !isMuted,
                      projectID: card.project_id,
                    });
                  },
                  onDelete: !isMain
                    ? () => {
                        setDeleteTarget(card);
                      }
                    : undefined,
                });
                const isSelectedInBatch =
                  isBatchMode && effectiveSelectedIDs.has(card.id);

                return (
                  <SwipeableRow
                    key={card.id}
                    id={card.id}
                    actions={swipeActions}
                    disabled={isBatchMode}
                    onLongPress={
                      isBatchMode ? undefined : () => setSheetTargetID(card.id)
                    }
                    className="rounded-xl"
                  >
                    <div
                      role={isBatchMode ? "checkbox" : "button"}
                      aria-checked={isBatchMode ? isSelectedInBatch : undefined}
                      tabIndex={0}
                      onClick={() => {
                        if (isBatchMode) {
                          setSelectedReviewIDs((prev) =>
                            toggleReviewSelection(prev, card.id),
                          );
                          return;
                        }
                        handleSelectCard(card);
                      }}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" || e.key === " ") {
                          e.preventDefault();
                          if (isBatchMode) {
                            setSelectedReviewIDs((prev) =>
                              toggleReviewSelection(prev, card.id),
                            );
                          } else {
                            handleSelectCard(card);
                          }
                        }
                      }}
                      className={cn(
                        "group relative flex w-full select-none items-center justify-between rounded-xl border px-3.5 py-2.5 text-left transition active:scale-[0.99] cursor-pointer",
                        isSelectedInBatch
                          ? "border-emerald-500/50 bg-emerald-500/8 text-foreground ring-1 ring-emerald-500/25 shadow-xs"
                          : isActive
                            ? "border-primary/40 bg-primary/5 text-foreground ring-1 ring-primary/25 shadow-xs"
                            : "border-line/60 bg-canvas/70 hover:border-line hover:bg-muted/70",
                      )}
                    >
                      <div className="flex min-w-0 flex-1 items-center gap-2.5">
                        {isBatchMode && (
                          <span
                            aria-hidden="true"
                            className={cn(
                              "grid size-4.5 shrink-0 place-items-center rounded-md border transition-colors",
                              isSelectedInBatch
                                ? "border-emerald-600 bg-emerald-600 text-white"
                                : "border-line bg-surface text-transparent",
                            )}
                          >
                            <Check className="size-3 stroke-[2.5]" />
                          </span>
                        )}
                        <div className="flex shrink-0 items-center gap-1.5">
                          {visualStatus !== "idle" ? (
                            <CardStatusDot
                              status={visualStatus}
                              className="size-2.5"
                            />
                          ) : card.kind === "discussion" ? (
                            <MessageSquare className="size-3.5 text-indigo-500" />
                          ) : card.kind === "ongoing" ? (
                            <CalendarClock className="size-3.5 text-violet-500" />
                          ) : card.todo_state === "todo" ? (
                            <ListTodo className="size-3.5 text-slate-500" />
                          ) : card.todo_state === "review" ? (
                            <ShieldCheck className="size-3.5 text-amber-500" />
                          ) : card.todo_state === "done" ? (
                            <CheckCircle2 className="size-3.5 text-emerald-500" />
                          ) : (
                            <PlayCircle className="size-3.5 text-sky-500" />
                          )}
                        </div>
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-1.5">
                            {(activeTab === "focus" ||
                              Boolean(searchQuery.trim())) && (
                              <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                                {kindLabel}
                              </span>
                            )}
                            <h4
                              className={cn(
                                "truncate text-xs font-medium leading-snug",
                                isActive
                                  ? "text-primary font-semibold"
                                  : "text-foreground",
                              )}
                            >
                              {card.title ||
                                (card.kind === "discussion"
                                  ? t("未命名讨论")
                                  : card.kind === "ongoing"
                                    ? t("未命名定时任务")
                                    : t("未命名卡片"))}
                            </h4>
                            {isPinned && (
                              <Pin className="size-3 shrink-0 text-amber-500 fill-amber-500 rotate-45" />
                            )}
                            {isMuted && (
                              <span
                                title={t("通知已静音")}
                                aria-label={t("通知已静音")}
                              >
                                <BellOff className="size-3 shrink-0 text-muted-foreground/75" />
                              </span>
                            )}
                          </div>
                        </div>
                      </div>

                      {/* 状态徽标与当前标记 */}
                      <div className="ml-2 flex shrink-0 items-center gap-1.5">
                        {!isBatchMode && isReviewTodoCard(card) && (
                          <button
                            type="button"
                            disabled={completeReviewMutation.isPending}
                            title={t("验收完成")}
                            aria-label={t("验收完成")}
                            onClick={(e) => {
                              e.stopPropagation();
                              e.preventDefault();
                              completeReviewMutation.mutate({
                                id: card.id,
                                projectID: card.project_id,
                              });
                            }}
                            className="hidden [@media(hover:hover)]:group-hover:inline-flex items-center gap-1 rounded-full border border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 text-[10px] font-medium text-emerald-700 transition-colors hover:bg-emerald-500/20 disabled:opacity-50 dark:text-emerald-300"
                          >
                            <CheckCircle2 className="size-3" />
                            <span>{t("完成")}</span>
                          </button>
                        )}
                        {focusReason === "approval" && (
                          <span className="rounded-full bg-orange-500/10 px-2 py-0.5 text-[10px] font-medium text-orange-700 dark:text-orange-300">
                            {t("待确认")}
                          </span>
                        )}
                        {focusReason === "critical" && (
                          <span className="rounded-full bg-rose-500/10 px-2 py-0.5 text-[10px] font-medium text-rose-700 dark:text-rose-300">
                            {t("异常")}
                          </span>
                        )}
                        {focusReason === "warning" && (
                          <span className="rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300">
                            {t("需留意")}
                          </span>
                        )}
                        {focusReason === "answering" && (
                          <span className="rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] font-medium text-emerald-700 dark:text-emerald-300">
                            {t("运行中")}
                          </span>
                        )}
                        {focusReason === "unread" && (
                          <span className="rounded-full bg-sky-500/10 px-2 py-0.5 text-[10px] font-medium text-sky-700 dark:text-sky-300">
                            {card.marked_unread &&
                            (card.latest_message_sequence ?? 0) <=
                              (card.last_read_message_sequence ?? 0)
                              ? t("未读")
                              : t("新消息")}
                          </span>
                        )}
                        {focusReason === "review" && (
                          <span className="rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300">
                            {t("待验收")}
                          </span>
                        )}
                        {focusReason === "working" && activeTab === "focus" && (
                          <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                            {t("进行中")}
                          </span>
                        )}
                        {focusReason === "pinned" && activeTab === "focus" && (
                          <span className="rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300">
                            {t("已置顶")}
                          </span>
                        )}
                        {isActive && (
                          <span className="flex items-center gap-1 rounded-full bg-primary/15 px-2 py-0.5 text-[10px] font-semibold text-primary">
                            <Sparkles className="size-2.5" />
                            {t("当前")}
                          </span>
                        )}
                      </div>
                    </div>
                  </SwipeableRow>
                );
              })}
            </SwipeActionGroup>
          )}
        </div>

        {isBatchMode && (
          <div className="flex shrink-0 items-center justify-between gap-2 border-t border-divider bg-surface/95 px-4 py-2.5 pb-[calc(env(safe-area-inset-bottom,0px)+0.625rem)] backdrop-blur-xs">
            <button
              type="button"
              disabled={batchComplete.isPending || visibleReviewIDs.length === 0}
              onClick={() =>
                setSelectedReviewIDs((prev) =>
                  toggleSelectAllReview(
                    reconcileReviewSelection(prev, visibleReviewIDs),
                    visibleReviewIDs,
                  ),
                )
              }
              className="inline-flex h-8 items-center gap-1.5 rounded-lg px-2 text-xs font-medium text-foreground transition hover:bg-muted disabled:opacity-50"
            >
              <span
                aria-hidden="true"
                className={cn(
                  "grid size-4 place-items-center rounded border transition-colors",
                  allVisibleReviewSelected
                    ? "border-emerald-600 bg-emerald-600 text-white"
                    : "border-line bg-canvas text-transparent",
                )}
              >
                <Check className="size-3 stroke-[2.5]" />
              </span>
              <span>
                {allVisibleReviewSelected ? t("取消全选") : t("全选")}
              </span>
            </button>

            <div className="flex items-center gap-2">
              <button
                type="button"
                disabled={batchComplete.isPending}
                onClick={exitBatchMode}
                className="inline-flex h-8 items-center rounded-lg border border-line bg-surface px-3 text-xs font-medium text-muted-foreground transition hover:bg-muted hover:text-foreground disabled:opacity-50"
              >
                {t("取消")}
              </button>
              <button
                type="button"
                disabled={
                  batchComplete.isPending || effectiveSelectedIDs.size === 0
                }
                onClick={() => void handleBatchSubmit()}
                className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-emerald-600 px-3 text-xs font-medium text-white shadow-xs transition hover:bg-emerald-700 active:scale-[0.99] disabled:opacity-50"
              >
                {batchComplete.isPending ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <CheckCircle2 className="size-3.5" />
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

        <ConfirmDialog
          open={Boolean(deleteTarget)}
          onOpenChange={(isOpen) => !isOpen && setDeleteTarget(null)}
          title={t("删除这个卡片？")}
          description={t("删除卡片将同步清理相关记录，该操作无法撤销。")}
          confirmLabel={t("删除卡片")}
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
          onOpenChange={(isOpen) => !isOpen && setEditTarget(null)}
        />

        <CardRowActionSheet
          card={sheetTarget}
          open={Boolean(sheetTarget)}
          onOpenChange={(isOpen) => !isOpen && setSheetTargetID(null)}
          preserve={Boolean(sheetTarget && sheetTarget.id === mainDiscussionID)}
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
      </SheetContent>
    </UISheet>
  );
}

export interface QuickCardSwitcherProps {
  projectID: string;
  activeCardID?: string;
  activeAttemptStatus?: string;
  className?: string;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  triggerOnly?: boolean;
  modalOnly?: boolean;
}

/**
 * 完整组合版 QuickCardSwitcher
 * 提供向后兼容性；当仅需按钮时可传 triggerOnly，仅需弹窗时可传 modalOnly。
 */
export function MobileQuickCardSwitcher({
  projectID,
  activeCardID,
  activeAttemptStatus,
  className = "",
  open: controlledOpen,
  onOpenChange: controlledOnOpenChange,
  triggerOnly = false,
  modalOnly = false,
}: QuickCardSwitcherProps) {
  const [internalOpen, setInternalOpen] = useState(false);
  const isControlled = controlledOpen !== undefined;
  const open = isControlled ? controlledOpen : internalOpen;

  const setOpen = useCallback(
    (nextOpen: boolean) => {
      if (!isControlled) {
        setInternalOpen(nextOpen);
      }
      controlledOnOpenChange?.(nextOpen);
    },
    [isControlled, controlledOnOpenChange],
  );

  if (triggerOnly) {
    return (
      <QuickCardSwitcherTrigger
        projectID={projectID}
        activeCardID={activeCardID}
        activeAttemptStatus={activeAttemptStatus}
        className={className}
        onClick={() => setOpen(true)}
      />
    );
  }

  if (modalOnly) {
    return (
      <QuickCardSwitcherModal
        projectID={projectID}
        activeCardID={activeCardID}
        activeAttemptStatus={activeAttemptStatus}
        open={open}
        onOpenChange={setOpen}
      />
    );
  }

  return (
    <>
      <QuickCardSwitcherTrigger
        projectID={projectID}
        activeCardID={activeCardID}
        activeAttemptStatus={activeAttemptStatus}
        className={className}
        onClick={() => setOpen(true)}
      />
      <QuickCardSwitcherModal
        projectID={projectID}
        activeCardID={activeCardID}
        activeAttemptStatus={activeAttemptStatus}
        open={open}
        onOpenChange={setOpen}
      />
    </>
  );
}

export { MobileQuickCardSwitcher as QuickCardSwitcher };
