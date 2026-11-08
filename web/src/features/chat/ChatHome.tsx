import { useCallback, useMemo, useState } from "react";
import { useSwipeGesture, useSwipeUpGesture } from "../../shared/lib/useSwipeGesture";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import {
  CalendarClock,
  ChevronRight,
  MessageSquare,
  MessageSquareText,
  PanelLeft,
  PlayCircle,
  Sparkles,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import { ErrorView, Sheet, Skeleton } from "../../shared/ui/components";
import { ContextSidebarSkeleton } from "../../shared/ui/ContextSidebar";
import {
  CardStatusDot,
  getCardVisualStatus,
} from "../../shared/ui/CardStatusDot";
import {
  buildFocusSummary,
  getCardFocusReason,
} from "../../shared/lib/card-focus";
import type { Card } from "../../shared/api/types";
import { useProject } from "../../app/project-context";
import { useContextSidebarVisible } from "../../app/context-sidebar";
import { DiscussionSidebar } from "./DiscussionSidebar";
import {
  QuickCardSwitcherTrigger,
  QuickCardSwitcherModal,
} from "../../shared/ui/MobileQuickCardSwitcher";
import { DiscussionStartComposer } from "./DiscussionStartComposer";

export default function ChatHome() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const listVisible = useContextSidebarVisible();
  const [mobileListOpen, setMobileListOpen] = useState(false);
  const [quickSwitcherOpen, setQuickSwitcherOpen] = useState(false);

  // 手机端聊天框区域上滑手势：快速呼出卡片切换面板
  const composerRef = useSwipeUpGesture<HTMLDivElement>({
    onSwipeUp: () => setQuickSwitcherOpen(true),
  });

  const handleSwipeRight = useCallback(() => {
    if (typeof window !== "undefined" && window.innerWidth < 768 && !mobileListOpen) {
      setMobileListOpen(true);
    }
  }, [mobileListOpen]);

  const handleSwipeLeft = useCallback(() => {
    if (typeof window !== "undefined" && window.innerWidth < 768 && mobileListOpen) {
      setMobileListOpen(false);
    }
  }, [mobileListOpen]);

  // 移动端仅在当前打开了移动端抽屉列表时才激活向左滑关闭手势；
  // 避免未展开移动抽屉时向左滑与列表项的 SwipeableRow（如向左滑置顶/删除）发生手势冲突；
  // 不设 edgeWidth，允许在除会话输入窗口外的页面任意空白区域右滑呼出讨论列表
  const containerRef = useSwipeGesture<HTMLElement>({
    onSwipeRight: handleSwipeRight,
    onSwipeLeft: mobileListOpen ? handleSwipeLeft : undefined,
    threshold: 45,
    horizontalRatio: 1.2,
  });
  const {
    projectID,
    isPending: projectsPending,
    error: projectsError,
    refetch: refetchProjects,
  } = useProject();

  const cards = useQuery({
    queryKey: keys.cards(projectID),
    queryFn: () => api.cards(projectID),
    enabled: Boolean(projectID),
  });
  const discussions = useMemo(
    () =>
      (cards.data || []).filter((card) => card.kind === "discussion") as Card[],
    [cards.data],
  );
  const focusSummary = useMemo(
    () => buildFocusSummary(cards.data || []),
    [cards.data],
  );

  function selectDiscussion(card: Card) {
    setMobileListOpen(false);
    navigate(`/chat/${card.project_id}/${card.id}`);
  }

  function openFocusCard(card: Card) {
    setMobileListOpen(false);
    if (card.kind === "discussion") {
      navigate(`/chat/${card.project_id}/${card.id}`);
    } else if (card.kind === "ongoing") {
      navigate(`/plans/${card.project_id}/${card.id}`);
    } else {
      navigate(`/cards/${card.project_id}/${card.id}`);
    }
  }

  function startDiscussion() {
    setMobileListOpen(false);
    navigate(`/chat?project=${encodeURIComponent(projectID)}`);
  }

  if (projectsPending) {
    return (
      <main className="relative flex h-full min-h-0 flex-1 flex-col bg-surface">
        <div
          className={`grid h-full min-h-0 ${listVisible ? "md:grid-cols-[280px_minmax(0,1fr)]" : "grid-cols-1"}`}
        >
          <div
            className={`${listVisible ? "hidden md:block" : "hidden"} min-h-0 border-r border-line`}
          >
            <ContextSidebarSkeleton ariaLabel={t("讨论列表")} />
          </div>
          <div className="flex h-full min-h-0 flex-col items-center justify-center p-6 text-center">
            <Skeleton className="size-14 rounded-2xl mb-4" />
            <Skeleton className="h-5 w-44 mb-2" />
            <Skeleton className="h-3.5 w-64" />
          </div>
        </div>
      </main>
    );
  }

  if (projectsError) {
    return (
      <main className="mx-auto max-w-xl p-6">
        <ErrorView error={projectsError} onRetry={refetchProjects} />
      </main>
    );
  }

  return (
    <main className="relative flex h-full min-h-0 flex-1 flex-col bg-surface">
      {projectID ? (
        <div
          className={`grid h-full min-h-0 ${listVisible ? "md:grid-cols-[280px_minmax(0,1fr)]" : "grid-cols-1"}`}
        >
          <div
            className={`${listVisible ? "hidden md:block" : "hidden"} min-h-0 border-r border-line`}
          >
            <DiscussionSidebar
              discussions={discussions}
              onSelect={selectDiscussion}
              onCreate={startDiscussion}
              loading={cards.isPending}
            />
          </div>

          <section ref={containerRef} className="relative flex min-h-0 min-w-0 flex-col">
            <div className="absolute top-[calc(env(safe-area-inset-top,0px)+0.75rem)] left-3 z-10 flex items-center gap-1.5 md:hidden">
              <button
                type="button"
                title={t("打开讨论列表")}
                aria-label={t("打开讨论列表")}
                onClick={() => setMobileListOpen(true)}
                className="inline-flex size-8 items-center justify-center rounded-lg border border-line bg-surface text-muted-foreground shadow-xs transition hover:bg-muted hover:text-foreground"
              >
                <PanelLeft className="size-4" />
              </button>
            </div>

            {/* 移动端右上角快速切换（桌面端统一在左侧主栏底部常驻） */}
            <div className="absolute top-[calc(env(safe-area-inset-top,0px)+0.75rem)] right-3 z-10 flex items-center gap-1.5 md:hidden">
              <QuickCardSwitcherTrigger
                projectID={projectID}
                onClick={() => setQuickSwitcherOpen(true)}
              />
            </div>

            <div className="flex min-h-0 flex-1 flex-col items-center justify-center overflow-y-auto px-5 pt-14 pb-8 md:pt-8">
              <div className="w-full max-w-3xl my-auto">
                <div className="mb-6 text-center">
                  <h1 className="text-xl font-semibold tracking-tight text-heading sm:text-2xl">
                    {t("今天想推进什么？")}
                  </h1>
                </div>

                <div
                  ref={composerRef}
                  data-no-swipe="true"
                  className="w-full"
                >
                  <DiscussionStartComposer
                    key={projectID}
                    projectID={projectID}
                  />
                </div>

                {focusSummary.cards.length > 0 && (
                  <div
                    aria-label={t("当前焦点")}
                    className="mt-6 w-full space-y-2.5"
                  >
                    <div className="flex items-center justify-between gap-2 px-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="inline-flex items-center gap-1.5 text-xs font-semibold text-muted-foreground">
                          <Sparkles className="size-3.5 text-primary" />
                          {t("当前焦点")}
                        </span>
                        {focusSummary.answeringCount > 0 && (
                          <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/12 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:text-emerald-300">
                            <span className="size-1.5 rounded-full bg-emerald-500 animate-pulse" />
                            {t("{{count}} 运行中", {
                              count: focusSummary.answeringCount,
                            })}
                          </span>
                        )}
                        {focusSummary.unreadCount > 0 && (
                          <span className="inline-flex items-center gap-1 rounded-full bg-sky-500/12 px-2 py-0.5 text-[11px] font-medium text-sky-700 dark:text-sky-300">
                            <span className="size-1.5 rounded-full bg-sky-500" />
                            {t("{{count}} 未读", {
                              count: focusSummary.unreadCount,
                            })}
                          </span>
                        )}
                        {focusSummary.reviewCount > 0 && (
                          <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/12 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:text-amber-300">
                            {t("{{count}} 待验收", {
                              count: focusSummary.reviewCount,
                            })}
                          </span>
                        )}
                      </div>

                      <button
                        type="button"
                        onClick={() => setQuickSwitcherOpen(true)}
                        className="inline-flex items-center gap-0.5 text-xs font-medium text-muted-foreground transition hover:text-foreground"
                      >
                        <span>
                          {t("全部 ({{count}})", {
                            count: focusSummary.cards.length,
                          })}
                        </span>
                        <ChevronRight className="size-3.5" />
                      </button>
                    </div>

                    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                      {focusSummary.cards.slice(0, 6).map((card, index) => {
                        const visualStatus = getCardVisualStatus(card);
                        const reason = getCardFocusReason(card);
                        const kindLabel =
                          card.kind === "discussion"
                            ? t("讨论")
                            : card.kind === "ongoing"
                              ? t("定时")
                              : t("任务");
                        return (
                          <button
                            key={card.id}
                            type="button"
                            onClick={() => openFocusCard(card)}
                            // 手机端最多展示前 3 条，避免挤压输入框与软键盘空间；平板/桌面端展示最多 6 条
                            className={`${
                              index >= 3 ? "hidden sm:flex" : "flex"
                            } group items-center justify-between gap-2.5 rounded-xl border border-line/80 bg-canvas/70 px-3 py-2.5 text-left shadow-2xs transition hover:border-line hover:bg-muted/60 active:scale-[0.99]`}
                          >
                            <div className="flex min-w-0 flex-1 items-center gap-2">
                              {visualStatus !== "idle" ? (
                                <CardStatusDot
                                  status={visualStatus}
                                  className="size-2.5"
                                />
                              ) : card.kind === "discussion" ? (
                                <MessageSquare className="size-3.5 shrink-0 text-indigo-500" />
                              ) : card.kind === "ongoing" ? (
                                <CalendarClock className="size-3.5 shrink-0 text-violet-500" />
                              ) : (
                                <PlayCircle className="size-3.5 shrink-0 text-sky-500" />
                              )}
                              <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                                {kindLabel}
                              </span>
                              <span className="truncate text-xs font-medium text-foreground group-hover:text-primary">
                                {card.title ||
                                  (card.kind === "discussion"
                                    ? t("未命名讨论")
                                    : card.kind === "ongoing"
                                      ? t("未命名定时任务")
                                      : t("未命名卡片"))}
                              </span>
                            </div>

                            <div className="flex shrink-0 items-center gap-1.5">
                              {reason === "answering" && (
                                <span className="rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] font-medium text-emerald-700 dark:text-emerald-300">
                                  {t("运行中")}
                                </span>
                              )}
                              {reason === "unread" && (
                                <span className="rounded-full bg-sky-500/10 px-2 py-0.5 text-[10px] font-medium text-sky-700 dark:text-sky-300">
                                  {t("新消息")}
                                </span>
                              )}
                              {reason === "review" && (
                                <span className="rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300">
                                  {t("待验收")}
                                </span>
                              )}
                              {reason === "working" && (
                                <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                                  {t("进行中")}
                                </span>
                              )}
                            </div>
                          </button>
                        );
                      })}
                    </div>

                    {focusSummary.cards.length > 3 && (
                      <button
                        type="button"
                        onClick={() => setQuickSwitcherOpen(true)}
                        className={`${
                          focusSummary.cards.length <= 6 ? "sm:hidden" : ""
                        } flex w-full items-center justify-center gap-1 rounded-xl border border-dashed border-line/80 py-2 text-xs font-medium text-muted-foreground transition hover:bg-muted/50 hover:text-foreground`}
                      >
                        <span>
                          {t("查看全部 {{count}} 个焦点事项", {
                            count: focusSummary.cards.length,
                          })}
                        </span>
                        <ChevronRight className="size-3.5" />
                      </button>
                    )}
                  </div>
                )}
              </div>
            </div>
          </section>
        </div>
      ) : (
        <div className="grid h-full place-items-center px-5 text-center">
          <div>
            <MessageSquareText className="mx-auto size-9 text-placeholder" />
            <h1 className="mt-4 text-xl font-semibold">{t("还没有项目")}</h1>
            <p className="mt-2 text-sm text-muted-foreground">
              {t("请先创建一个项目。")}
            </p>
            <Link
              className="mt-5 inline-block text-sm font-medium text-sky-700"
              to="/board"
            >
              {t("前往任务")}
            </Link>
          </div>
        </div>
      )}

      {projectID && (
        <Sheet
          open={mobileListOpen}
          onOpenChange={setMobileListOpen}
          title={t("讨论列表")}
          side="left"
          padded={false}
          showCloseButton={false}
          className="bg-canvas"
        >
          <div
            className="flex h-full min-h-0 flex-col"
            onClick={(e) => {
              if ((e.target as HTMLElement).closest("a")) {
                setMobileListOpen(false);
              }
            }}
          >
            <DiscussionSidebar
              discussions={discussions}
              onSelect={selectDiscussion}
              onCreate={startDiscussion}
            />
          </div>
        </Sheet>
      )}

      {/* 页面全局快速切换弹窗（单例挂载，避免多端 CSS 响应式断点导致 Portal 重复渲染与动画打架） */}
      <QuickCardSwitcherModal
        projectID={projectID}
        open={quickSwitcherOpen}
        onOpenChange={setQuickSwitcherOpen}
      />
    </main>
  );
}
