import { useState, useMemo } from "react";
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
  Pin,
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
import { SwipeActionGroup, SwipeableRow } from "./SwipeableRow";
import { cn } from "../lib/utils";

type SwitcherTab = "working" | "review" | "discussion" | "done";

export function MobileQuickCardSwitcher({
  projectID,
  activeCardID,
  activeAttemptStatus,
  className = "",
}: {
  projectID: string;
  activeCardID?: string;
  activeAttemptStatus?: string;
  className?: string;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [activeTab, setActiveTab] = useState<SwitcherTab>("working");

  const cardsQuery = useQuery({
    queryKey: keys.cards(projectID),
    queryFn: () => api.cards(projectID),
    enabled: Boolean(projectID),
  });

  const cards: Card[] = useMemo(() => cardsQuery.data || [], [cardsQuery.data]);

  // 分组统计与列表
  const { workingCards, reviewCards, discussionCards, doneCards } = useMemo(() => {
    const working: Card[] = [];
    const review: Card[] = [];
    const discussions: Card[] = [];
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
        // ongoing 持续计划归入活跃流
        working.push(card);
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
      doneCards: done.sort(sortFn),
    };
  }, [cards]);

  const activeBadgeCount = workingCards.length + reviewCards.length;

  // 打开抽屉时自动选择最有优先级的 Tab
  const handleOpenChange = (nextOpen: boolean) => {
    if (nextOpen) {
      // 优先聚焦当前卡片所在分组或有活跃任务的分组
      if (activeCardID) {
        const currentCard = cards.find((c) => c.id === activeCardID);
        if (currentCard) {
          if (currentCard.kind === "discussion") {
            setActiveTab("discussion");
          } else if (currentCard.kind === "todo" && currentCard.todo_state === "review") {
            setActiveTab("review");
          } else if (currentCard.kind === "todo" && currentCard.todo_state === "done") {
            setActiveTab("done");
          } else {
            setActiveTab("working");
          }
          setOpen(true);
          return;
        }
      }
      setActiveTab("discussion");
    }
    setOpen(nextOpen);
  };

  const handleSelectCard = (targetCard: Card) => {
    setOpen(false);
    if (targetCard.kind === "discussion") {
      navigate(`/chat/${targetCard.project_id}/${targetCard.id}`);
    } else {
      navigate(`/chat/${targetCard.project_id}/${targetCard.id}`);
    }
  };

  const queryClient = useQueryClient();
  const [deleteTarget, setDeleteTarget] = useState<Card | null>(null);

  const pinMutation = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) =>
      api.pinCard(id, pinned),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (card: Card) => api.deleteCard(card.id),
    onSuccess: () => {
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
      case "done":
        return doneCards;
      default:
        return [];
    }
  }, [activeTab, workingCards, reviewCards, discussionCards, doneCards]);

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
      label: t("待审核"),
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
  ];

  return (
    <>
      <button
        type="button"
        title={t("快速切换卡片")}
        aria-label={t("快速切换卡片")}
        onClick={() => handleOpenChange(true)}
        className={cn(
          "relative inline-flex size-8 shrink-0 items-center justify-center rounded-lg border border-line bg-surface text-muted-foreground shadow-xs transition hover:bg-muted hover:text-foreground md:hidden",
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

      <UISheet open={open} onOpenChange={setOpen}>
        <SheetContent
          side="bottom"
          aria-describedby={undefined}
          className="fixed inset-x-0 bottom-0 z-50 flex max-h-[82vh] h-[70vh] flex-col rounded-t-2xl border-t border-line bg-surface p-0 shadow-2xl focus:outline-none sm:max-w-none"
        >
          {/* 拖动提示条 */}
          <div className="flex w-full items-center justify-center pt-2.5 pb-1">
            <span className="h-1.5 w-10 rounded-full bg-muted-foreground/25" />
          </div>

          <SheetHeader className="border-b border-divider px-4 py-2 text-left">
            <div className="flex items-center justify-between">
              <SheetTitle className="text-base font-semibold text-foreground">
                {t("快速切换")}
              </SheetTitle>
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
                        ? "bg-muted text-foreground shadow-xs font-semibold"
                        : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                      tab.colorClasses,
                    )}
                  >
                    <Icon className="size-3.5" />
                    <span>{tab.label}</span>
                    {tab.count > 0 && (
                      <span
                        className={cn(
                          "rounded-full px-1.5 py-0.2 text-[10px] tabular-nums font-semibold",
                          isActive
                            ? "bg-foreground/10 text-foreground"
                            : "bg-muted-foreground/15 text-muted-foreground",
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

          {/* 卡片切换列表 */}
          <div className="flex-1 overflow-y-auto overscroll-contain p-3 scrollbar-thin">
            {currentList.length === 0 ? (
              <div className="flex h-44 flex-col items-center justify-center text-center text-muted-foreground">
                <Sparkles className="mb-2 size-6 stroke-1 text-placeholder" />
                <p className="text-xs">{t("当前分类暂无内容")}</p>
              </div>
            ) : (
              <SwipeActionGroup className="space-y-1.5">
                {currentList.map((item) => {
                  const isCurrent = item.id === activeCardID;
                  const visualStatus = getCardVisualStatus(
                    item,
                    isCurrent,
                    isCurrent ? activeAttemptStatus : undefined,
                  );

                  const swipeActions = [
                    {
                      key: "pin",
                      label: item.pinned_at ? t("取消置顶") : t("置顶"),
                      icon: <Pin className="size-4 rotate-45" />,
                      variant: "primary" as const,
                      onClick: () => {
                        pinMutation.mutate({
                          id: item.id,
                          pinned: !item.pinned_at,
                        });
                      },
                    },
                    ...(item.kind !== "discussion"
                      ? [
                          {
                            key: "delete",
                            label: t("删除"),
                            icon: <Trash2 className="size-4" />,
                            variant: "danger" as const,
                            onClick: () => {
                              setDeleteTarget(item);
                            },
                          },
                        ]
                      : []),
                  ];

                  return (
                    <SwipeableRow
                      key={item.id}
                      actions={swipeActions}
                      className="rounded-xl overflow-hidden"
                    >
                      <button
                        type="button"
                        onClick={() => handleSelectCard(item)}
                        className={cn(
                          "group flex w-full items-start gap-2.5 rounded-xl border p-2.5 text-left transition active:scale-[0.99]",
                          isCurrent
                            ? "border-primary/40 bg-primary/5 shadow-xs"
                            : "border-transparent bg-canvas/60 hover:border-line hover:bg-muted/60",
                        )}
                      >
                        <div className="mt-0.5 shrink-0">
                          {item.kind === "discussion" ? (
                            <MessageSquare className="size-3.5 text-indigo-500" />
                          ) : item.kind === "ongoing" ? (
                            <CalendarClock className="size-3.5 text-violet-500" />
                          ) : (
                            <CardStatusDot status={visualStatus} className="size-2.5" />
                          )}
                        </div>

                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-1.5">
                            <span
                              className={cn(
                                "truncate text-xs font-medium leading-snug",
                                isCurrent ? "font-semibold text-primary" : "text-foreground",
                              )}
                            >
                              {item.title || t("未命名")}
                            </span>
                            {item.pinned_at && (
                              <Pin className="size-2.5 shrink-0 text-amber-500 fill-amber-500" />
                            )}
                            {isCurrent && (
                              <span className="shrink-0 rounded bg-primary/15 px-1 py-0.2 text-[9px] font-semibold text-primary">
                                {t("当前")}
                              </span>
                            )}
                          </div>

                          {item.description && (
                            <p className="mt-0.5 line-clamp-1 text-[11px] text-muted-foreground">
                              {item.description}
                            </p>
                          )}
                        </div>
                      </button>
                    </SwipeableRow>
                  );
                })}
              </SwipeActionGroup>
            )}
          </div>

          <ConfirmDialog
            open={Boolean(deleteTarget)}
            onOpenChange={(open) => !open && setDeleteTarget(null)}
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
        </SheetContent>
      </UISheet>
    </>
  );
}
