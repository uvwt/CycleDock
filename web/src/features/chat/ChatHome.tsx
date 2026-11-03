import { useCallback, useMemo, useState } from "react";
import { useSwipeGesture, useSwipeUpGesture } from "../../shared/lib/useSwipeGesture";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { MessageSquareText, PanelLeft } from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import { ErrorView, Sheet, Skeleton } from "../../shared/ui/components";
import { ContextSidebarSkeleton } from "../../shared/ui/ContextSidebar";
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

  function selectDiscussion(card: Card) {
    setMobileListOpen(false);
    navigate(`/chat/${card.project_id}/${card.id}`);
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
            <div className="absolute top-3 left-3 z-10 flex items-center gap-1.5 md:hidden">
              <button
                type="button"
                title={t("打开讨论列表")}
                aria-label={t("打开讨论列表")}
                onClick={() => setMobileListOpen(true)}
                className="grid size-9 place-items-center rounded-xl border border-line bg-surface text-muted-foreground shadow-sm hover:bg-muted"
              >
                <PanelLeft className="size-5" />
              </button>
              <QuickCardSwitcherTrigger
                projectID={projectID}
                onClick={() => setQuickSwitcherOpen(true)}
              />
            </div>

            {/* 桌面端右上角快速切换 */}
            <div className="absolute top-3 right-3 z-10 hidden md:flex items-center gap-1.5 sm:right-4">
              <QuickCardSwitcherTrigger
                projectID={projectID}
                onClick={() => setQuickSwitcherOpen(true)}
              />
            </div>

            <div className="flex min-h-0 flex-1 flex-col items-center justify-center px-5 pb-6">
              <div className="w-full max-w-3xl">
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
          side="left"
          padded={false}
          className="bg-canvas"
          title={t("讨论列表")}
        >
          <DiscussionSidebar
            discussions={discussions}
            onSelect={selectDiscussion}
            onCreate={startDiscussion}
          />
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
