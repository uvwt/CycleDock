import { useState, useMemo, useCallback, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Layers,
  MessageSquare,
  PlayCircle,
  ShieldCheck,
  CheckCircle2,
  CalendarClock,
  Pencil,
  Pin,
  PinOff,
  Sparkles,
  Trash2,
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
import { CardStatusDot, getCardVisualStatus } from "./CardStatusDot";
import { ConfirmDialog } from "./components";
import { EditCardDialog } from "./EditCardDialog";
import { SwipeActionGroup, SwipeableRow } from "./SwipeableRow";
import { cn } from "../lib/utils";

type SwitcherTab = "working" | "review" | "discussion" | "ongoing" | "done";

export interface QuickCardSwitcherTriggerProps {
  projectID: string;
  className?: string;
  onClick: () => void;
}

/**
 * 快速切换入口按钮
 * 仅负责渲染触发按钮与活跃任务数字角标，支持在移动端顶栏、桌面端工具栏等不同布局区域多次复用，
 * 避免重复挂载底层 SheetPortal 与全局键盘监听。
 */
export function QuickCardSwitcherTrigger({
  projectID,
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

  const activeBadgeCount = useMemo(() => {
    let count = 0;
    for (const card of cards) {
      if (
        card.kind === "todo" &&
        (card.todo_state === "working" || card.todo_state === "review")
      ) {
        count++;
      }
    }
    return count;
  }, [cards]);

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
        <span className="absolute -top-1 -right-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-bold text-primary-foreground shadow">
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
  const { workingCards, reviewCards, discussionCards, ongoingCards, doneCards } = useMemo(() => {
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
        if (card.todo_state === "working") {
          working.push(card);
        } else if (card.todo_state === "review") {
          review.push(card);
        } else if (card.todo_state === "done") {
          done.push(card);
        } else if (card.todo_state === "todo") {
          // 待办归入进行中/待办大类中供快速切换
          working.push(card);
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
      workingCards: working.sort(sortFn),
      reviewCards: review.sort(sortFn),
      discussionCards: discussions.sort(sortFn),
      ongoingCards: ongoing.sort(sortFn),
      doneCards: done.sort(sortFn),
    };
  }, [cards]);

  const activeBadgeCount = workingCards.length + reviewCards.length;

  const [selectedTab, setSelectedTab] = useState<SwitcherTab | null>(null);

  const defaultTab = useMemo((): SwitcherTab => {
    if (activeCardID) {
      const currentCard = cards.find((c) => c.id === activeCardID);
      if (currentCard) {
        if (currentCard.kind === "discussion") return "discussion";
        if (currentCard.kind === "ongoing") return "ongoing";
        if (currentCard.kind === "todo") {
          if (currentCard.todo_state === "review") return "review";
          if (currentCard.todo_state === "done") return "done";
          return "working";
        }
      }
    }
    return "discussion";
  }, [activeCardID, cards]);

  const activeTab = selectedTab || defaultTab;
  const setActiveTab = setSelectedTab;

  const handleOpenChange = useCallback((nextOpen: boolean) => {
    if (!nextOpen) {
      setSelectedTab(null);
    }
    onOpenChange(nextOpen);
  }, [onOpenChange]);

  const handleSelectCard = (targetCard: Card) => {
    setSelectedTab(null);
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

  const mainDiscussion = useQuery({
    queryKey: keys.mainDiscussion(projectID),
    queryFn: () => api.mainDiscussion(projectID),
    enabled: Boolean(projectID),
  });
  const mainDiscussionID = mainDiscussion.data?.id || "";

  const pinMutation = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) =>
      api.pinCard(id, pinned),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (card: Card) => api.deleteCard(card.id),
    onSuccess: (_, deletedCard) => {
      queryClient.setQueryData(keys.card(deletedCard.id), {
        card: null,
        notFound: true,
      });
      queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
      setDeleteTarget(null);
    },
  });

  const currentList = useMemo(() => {
    switch (activeTab) {
      case "working":
        return workingCards;
      case "review":
        return reviewCards;
      case "discussion":
        return discussionCards;
      case "ongoing":
        return ongoingCards;
      case "done":
        return doneCards;
      default:
        return [];
    }
  }, [activeTab, workingCards, reviewCards, discussionCards, ongoingCards, doneCards]);

  const tabs: {
    id: SwitcherTab;
    label: string;
    icon: typeof PlayCircle;
    count: number;
    colorClasses: string;
  }[] = [
    {
      id: "discussion",
      label: t("讨论"),
      icon: MessageSquare,
      count: discussionCards.length,
      colorClasses: "data-[state=active]:text-indigo-600 dark:data-[state=active]:text-indigo-400",
    },
    {
      id: "working",
      label: t("进行中"),
      icon: PlayCircle,
      count: workingCards.length,
      colorClasses: "data-[state=active]:text-sky-600 dark:data-[state=active]:text-sky-400",
    },
    {
      id: "review",
      label: t("待验收"),
      icon: ShieldCheck,
      count: reviewCards.length,
      colorClasses: "data-[state=active]:text-amber-600 dark:data-[state=active]:text-amber-400",
    },
    {
      id: "done",
      label: t("已完成"),
      icon: CheckCircle2,
      count: doneCards.length,
      colorClasses: "data-[state=active]:text-emerald-600 dark:data-[state=active]:text-emerald-400",
    },
    {
      id: "ongoing",
      label: t("定时任务"),
      icon: CalendarClock,
      count: ongoingCards.length,
      colorClasses: "data-[state=active]:text-violet-600 dark:data-[state=active]:text-violet-400",
    },
  ];

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // 桌面端快捷键：Cmd+K (macOS) 或 Ctrl+K (Windows/Linux)
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        handleOpenChange(!open);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
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
              <span className="rounded-full bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary">
                {t("{{count}} 个活跃任务", { count: activeBadgeCount })}
              </span>
            )}
          </div>

          {/* 分类 Tabs */}
          <div className="mt-2.5 flex items-center gap-1.5 overflow-x-auto pb-1 scrollbar-none">
            {tabs.map((tab) => {
              const Icon = tab.icon;
              const isActive = activeTab === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  data-state={isActive ? "active" : "inactive"}
                  onClick={() => setActiveTab(tab.id)}
                  className={cn(
                    "flex shrink-0 items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium transition",
                    isActive
                      ? "bg-primary/10 font-semibold text-primary shadow-xs"
                      : "text-muted-foreground hover:bg-muted hover:text-foreground",
                  )}
                >
                  <Icon className={cn("size-3.5", isActive ? "" : "opacity-70")} />
                  <span>{tab.label}</span>
                  {tab.count > 0 && (
                    <span
                      className={cn(
                        "rounded-full px-1.5 py-0.2 text-[10px] tabular-nums",
                        isActive
                          ? "bg-primary text-primary-foreground font-bold"
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
              <p className="text-xs">{t("该分类下暂无卡片")}</p>
            </div>
          ) : (
            <SwipeActionGroup className="flex flex-col gap-2 p-1">
              {currentList.map((card) => {
                const isActive = card.id === activeCardID;
                const isMain = card.id === mainDiscussionID;
                const isPinned = isMain || Boolean(card.pinned_at);
                const visualStatus = getCardVisualStatus(
                  card,
                  isActive,
                  isActive ? activeAttemptStatus : undefined,
                );

                const swipeActions = [
                  ...(!isMain
                    ? [
                        {
                          key: "pin",
                          label: isPinned ? t("取消置顶") : t("置顶"),
                          icon: isPinned ? (
                            <PinOff className="size-4" />
                          ) : (
                            <Pin className="size-4 rotate-45" />
                          ),
                          variant: "primary" as const,
                          onClick: () => {
                            pinMutation.mutate({
                              id: card.id,
                              pinned: !isPinned,
                            });
                          },
                        },
                        {
                          key: "edit",
                          label: t("编辑"),
                          icon: <Pencil className="size-4" />,
                          variant: "secondary" as const,
                          onClick: () => {
                            setEditTarget(card);
                          },
                        },
                        {
                          key: "delete",
                          label: t("删除"),
                          icon: <Trash2 className="size-4" />,
                          variant: "danger" as const,
                          onClick: () => {
                            setDeleteTarget(card);
                          },
                        },
                      ]
                    : []),
                ];

                return (
                  <SwipeableRow
                    key={card.id}
                    id={card.id}
                    actions={swipeActions}
                    className="rounded-xl"
                  >
                    <div
                      role="button"
                      tabIndex={0}
                      onClick={() => handleSelectCard(card)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" || e.key === " ") {
                          e.preventDefault();
                          handleSelectCard(card);
                        }
                      }}
                      className={cn(
                        "group relative flex w-full select-none items-center justify-between rounded-xl border px-3.5 py-2.5 text-left transition active:scale-[0.99] cursor-pointer",
                        isActive
                          ? "border-primary/40 bg-primary/5 text-foreground ring-1 ring-primary/25 shadow-xs"
                          : "border-line/60 bg-canvas/70 hover:border-line hover:bg-muted/70",
                      )}
                    >
                      <div className="flex min-w-0 flex-1 items-start gap-2.5">
                        <div className="mt-0.5 shrink-0">
                          {card.kind === "discussion" ? (
                            <MessageSquare className="size-3.5 text-indigo-500" />
                          ) : card.kind === "ongoing" ? (
                            <CalendarClock className="size-3.5 text-violet-500" />
                          ) : (
                            <CardStatusDot status={visualStatus} className="size-2.5" />
                          )}
                        </div>
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-1.5">
                            <h4
                              className={cn(
                                "truncate text-xs font-medium leading-snug",
                                isActive ? "text-primary font-semibold" : "text-foreground",
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
                          </div>
                          {card.description && (
                            <p className="mt-0.5 truncate text-[11px] text-muted-foreground">
                              {card.description}
                            </p>
                          )}
                        </div>
                      </div>

                      {/* 状态徽标与当前标记 */}
                      <div className="ml-2 flex shrink-0 items-center gap-1.5">
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
