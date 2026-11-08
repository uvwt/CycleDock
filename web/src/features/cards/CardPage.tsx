import { useDeviceSelection } from "../devices/device-selection";
import { DeviceMenu } from "../devices/DeviceMenu";
import { useEffect, useMemo, useRef, useState, useCallback } from "react";
import { useSwipeGesture, useSwipeUpGesture } from "../../shared/lib/useSwipeGesture";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { Check, Info, PanelLeft } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { useCardStream } from "../../shared/realtime/useCardStream";
import { ErrorView, Sheet } from "../../shared/ui/components";
import type { Attempt, TimelineEvent } from "../../shared/api/types";
import {
  ConversationView,
  type QuotedMessage,
} from "../timeline/ConversationView";
import { ConversationSkeleton } from "../timeline/ConversationSkeleton";
import { CardHeaderInfoCard } from "./CardHeaderInfoCard";
import { CardStatusSwitch } from "./CardStatusSwitch";
import { OngoingHealthBadge } from "../../shared/ui/OngoingHealthBadge";
import { PendingTurnQueue } from "./PendingTurnQueue";
import { ConversationComposer } from "./ConversationComposer";
import { DiscussionSidebar } from "../chat/DiscussionSidebar";
import { PlanSidebar } from "../plans/PlanSidebar";
import { WorkSidebar } from "../board/WorkSidebar";
import type { Card, DeviceTarget, PendingTurn } from "../../shared/api/types";
import { useProject } from "../../app/project-context";
import { useContextSidebarVisible } from "../../app/context-sidebar";
import {
  QuickCardSwitcherTrigger,
  QuickCardSwitcherModal,
} from "../../shared/ui/MobileQuickCardSwitcher";

type NavigationState = {
  initialMessage?: string;
  initialDeviceTarget?: DeviceTarget | null;
  initialIdempotencyKey?: string;
};

export default function CardPage() {
  const { t } = useTranslation();
  const { projectID = "", cardID = "" } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const listVisible = useContextSidebarVisible();
  const { projectID: activeProjectID } = useProject();
  const [contextSidebarOpen, setContextSidebarOpen] = useState(false);
  const [activeAttemptStatus, setActiveAttemptStatus] = useState<string | undefined>();

  // 监听屏幕断点（<1024px 为移动/平板端，对应 Tailwind lg 断点）
  // 用于严格控制 Radix Dialog/Sheet 等 Portal 浮层组件的挂载，防止桌面端同时渲染出 body 浮层
  const [isMobile, setIsMobile] = useState(() => {
    if (typeof window !== "undefined" && window.matchMedia) {
      return window.matchMedia("(max-width: 1023px)").matches;
    }
    return false;
  });

  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const media = window.matchMedia("(max-width: 1023px)");
    const handler = (e: MediaQueryListEvent) => setIsMobile(e.matches);
    media.addEventListener("change", handler);
    return () => media.removeEventListener("change", handler);
  }, []);

  const isDiscussion = location.pathname.startsWith("/chat");
  const isOngoing = location.pathname.startsWith("/plans");
  const actualProjectID = projectID || activeProjectID;

  // 会话列表按项目缓存查询；会话间切换时直接复用，不重新挂载整个侧边栏
  const projectCards = useQuery({
    queryKey: keys.cards(actualProjectID),
    queryFn: () => api.cards(actualProjectID),
    enabled: Boolean(actualProjectID),
  });

  const discussions = useMemo(
    () =>
      (projectCards.data || []).filter(
        (item) => item.kind === "discussion",
      ) as Card[],
    [projectCards.data],
  );

  function openDiscussion(item: Card) {
    setContextSidebarOpen(false);
    navigate(`/chat/${item.project_id}/${item.id}`);
  }

  function startDiscussion() {
    setContextSidebarOpen(false);
    navigate(`/chat?project=${encodeURIComponent(actualProjectID)}`);
  }

  function openWork(item: Card) {
    setContextSidebarOpen(false);
    navigate(
      `/${item.kind === "ongoing" ? "plans" : "cards"}/${item.project_id}/${item.id}`,
    );
  }

  const discussionSidebar =
    isDiscussion && actualProjectID ? (
      <DiscussionSidebar
        discussions={discussions}
        activeCardID={cardID}
        activeAttemptStatus={activeAttemptStatus}
        onSelect={openDiscussion}
        onCreate={startDiscussion}
        loading={projectCards.isPending}
      />
    ) : null;

  const workSidebar =
    !isDiscussion && !isOngoing && actualProjectID ? (
      <WorkSidebar
        cards={projectCards.data || []}
        activeCardID={cardID}
        activeAttemptStatus={activeAttemptStatus}
        onSelect={openWork}
        loading={projectCards.isPending}
      />
    ) : null;

  const contextSidebar = isDiscussion ? (
    discussionSidebar
  ) : isOngoing ? (
    <PlanSidebar
      cards={projectCards.data || []}
      activeCardID={cardID}
      activeAttemptStatus={activeAttemptStatus}
      onSelect={openWork}
      loading={projectCards.isPending}
    />
  ) : (
    workSidebar
  );

  return (
    <main className="flex flex-1 h-full min-h-0 w-full flex-col bg-canvas">
      <div className="flex flex-1 h-full min-h-0 w-full overflow-hidden">
        {/* 侧边栏：在会话切换或加载过程中始终保持挂载，绝不被遮挡或卸载 */}
        <div
          className={`${listVisible ? "hidden lg:block" : "hidden"} h-full min-h-0 w-[260px] xl:w-[280px] shrink-0 border-r border-line`}
        >
          {contextSidebar}
        </div>

        {/* 右侧会话主内容区：按 cardID 挂载，未加载完成时仅右侧展示骨架屏 */}
        <CardPageContent
          key={cardID}
          cardID={cardID}
          projectID={actualProjectID}
          isDiscussion={isDiscussion}
          isOngoing={isOngoing}
          isMobile={isMobile}
          projectCards={projectCards.data || []}
          onOpenSidebar={() => setContextSidebarOpen(true)}
          onAttemptStatusChange={setActiveAttemptStatus}
        />
      </div>

      {/* 移动端侧边栏抽屉：同样在切换会话时不被硬拔掉 */}
      {isMobile && contextSidebar && (
        <Sheet
          open={contextSidebarOpen}
          onOpenChange={setContextSidebarOpen}
          title={
            isDiscussion
              ? t("讨论列表")
              : isOngoing
                ? t("定时列表")
                : t("任务列表")
          }
          side="left"
          padded={false}
          showCloseButton={false}
          className="bg-canvas"
        >
          <div
            className="flex h-full min-h-0 flex-col"
            onClick={(e) => {
              if ((e.target as HTMLElement).closest("a")) {
                setContextSidebarOpen(false);
              }
            }}
          >
            {contextSidebar}
          </div>
        </Sheet>
      )}
    </main>
  );
}

function CardPageContent({
  cardID,
  projectID,
  isDiscussion,
  isOngoing,
  isMobile,
  projectCards,
  onOpenSidebar,
  onAttemptStatusChange,
}: {
  cardID: string;
  projectID: string;
  isDiscussion: boolean;
  isOngoing: boolean;
  isMobile: boolean;
  projectCards: Card[];
  onOpenSidebar: () => void;
  onAttemptStatusChange: (status?: string) => void;
}) {
  const { target } = useDeviceSelection();
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const navState = (location.state as NavigationState | null) || null;
  const [pendingInitialTurn, setPendingInitialTurn] = useState<string | null>(
    navState?.initialMessage?.trim() || null,
  );
  const { projectID: activeProjectID, setProjectID } = useProject();

  const detail = useQuery({
    queryKey: keys.card(cardID),
    queryFn: () => api.card(cardID),
    enabled: Boolean(cardID),
  });
  const card = detail.data?.card;
  const actualProjectID = card?.project_id || projectID;
  const stream = useCardStream(cardID, actualProjectID);

  useEffect(() => {
    if (card?.project_id && card.project_id !== activeProjectID) {
      setProjectID(card.project_id);
    }
  }, [card?.project_id, activeProjectID, setProjectID]);

  const activeAttemptStatus = stream.live
    ? stream.live.finished
      ? "completed"
      : "running"
    : detail.data?.current_attempt?.status;

  useEffect(() => {
    onAttemptStatusChange(activeAttemptStatus);
  }, [activeAttemptStatus, onAttemptStatusChange]);

  const blocked = Boolean(
    isDiscussion && card?.discussion_state === "archived",
  );
  const current = detail.data?.current_attempt;
  const hasCurrent =
    Boolean(current) &&
    ["queued", "running"].includes(current?.status || "");

  const [infoOpen, setInfoOpen] = useState(false);
  const [comment, setComment] = useState("");
  const [replyTo, setReplyTo] = useState<QuotedMessage | null>(null);
  const [cancelingTurnID, setCancelingTurnID] = useState("");
  const [steeringTurnID, setSteeringTurnID] = useState("");
  const [retryingAttemptID, setRetryingAttemptID] = useState("");
  const composerDockRef = useRef<HTMLDivElement>(null);
  const [composerDockHeight, setComposerDockHeight] = useState(96);
  const [quickSwitcherOpen, setQuickSwitcherOpen] = useState(false);

  // 手机端聊天框区域上滑手势：快速呼出卡片切换面板
  useSwipeUpGesture({
    targetRef: composerDockRef,
    onSwipeUp: () => setQuickSwitcherOpen(true),
    enabled: isMobile,
  });

  // 手机端滑动手势：右滑呼出左侧导航/卡片抽屉，左滑呼出右侧任务详情
  const handleSwipeRight = useCallback(() => {
    if (!isMobile) return;
    if (infoOpen) {
      setInfoOpen(false);
    } else {
      onOpenSidebar();
    }
  }, [infoOpen, isMobile, onOpenSidebar]);

  const handleSwipeLeft = useCallback(() => {
    if (!isMobile) return;
    setInfoOpen(true);
  }, [isMobile]);

  const containerRef = useSwipeGesture<HTMLElement>({
    onSwipeRight: handleSwipeRight,
    onSwipeLeft: handleSwipeLeft,
    threshold: 45,
    horizontalRatio: 1.2,
  });

  useEffect(() => {
    const el = composerDockRef.current;
    if (!el) return;
    const measure = () => {
      const next = Math.ceil(el.getBoundingClientRect().height);
      if (next > 0) {
        setComposerDockHeight((prev) => (prev !== next ? next : prev));
      }
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [
    card?.id,
    card?.todo_state,
    blocked,
    replyTo?.id,
    detail.data?.pending_turns?.length,
  ]);

  const [olderEvents, setOlderEvents] = useState<TimelineEvent[]>([]);
  const [olderAttempts, setOlderAttempts] = useState<Attempt[]>([]);
  const [hasMoreBefore, setHasMoreBefore] = useState(false);
  const [loadingMoreBefore, setLoadingMoreBefore] = useState(false);

  // 当切换卡片时重置向前分页状态
  useEffect(() => {
    setOlderEvents([]);
    setOlderAttempts([]);
    setHasMoreBefore(false);
    setLoadingMoreBefore(false);
  }, [cardID]);

  // 当卡片详情更新时，若尚未加载过更早历史批次，则同步后端基于真实数据量判断的 has_more_before 分页标识
  useEffect(() => {
    if (detail.data && olderEvents.length === 0) {
      setHasMoreBefore(Boolean(detail.data.has_more_before));
    }
  }, [detail.data, olderEvents.length]);

  const loadMoreBefore = async () => {
    if (!cardID || loadingMoreBefore || !hasMoreBefore) return;
    const allCurrentEvents = [...olderEvents, ...(detail.data?.timeline || [])];
    const minSeq = allCurrentEvents.reduce((min, ev) => (ev.sequence && ev.sequence < min ? ev.sequence : min), allCurrentEvents[0]?.sequence || 0);
    if (!minSeq || minSeq <= 1) {
      setHasMoreBefore(false);
      return;
    }
    setLoadingMoreBefore(true);
    try {
      const res = await api.cardTimelineBefore(cardID, minSeq, 50);
      if (res.timeline && res.timeline.length > 0) {
        setOlderEvents((prev) => [...res.timeline, ...prev]);
      }
      setOlderAttempts((prev) => [...(res.attempt_history || []), ...prev]);
      setHasMoreBefore(res.has_more_before);
    } catch (err) {
      console.error("Failed to load older timeline events", err);
    } finally {
      setLoadingMoreBefore(false);
    }
  };

  const combinedAttempts = useMemo(() => {
    const attempts = new Map(olderAttempts.map((attempt) => [attempt.id, attempt]));
    for (const attempt of detail.data?.attempt_history || []) attempts.set(attempt.id, attempt);
    if (detail.data?.current_attempt) attempts.delete(detail.data.current_attempt.id);
    return [...attempts.values()];
  }, [olderAttempts, detail.data?.attempt_history, detail.data?.current_attempt]);

  const combinedTimeline = useMemo(() => {
    const rawTimeline = detail.data?.timeline || [];
    if (!olderEvents.length) return rawTimeline;
    const seen = new Set<string>();
    const res: TimelineEvent[] = [];
    for (const ev of [...olderEvents, ...rawTimeline]) {
      const key = `${ev.type}-${ev.id || ev.sequence}`;
      if (!seen.has(key)) {
        seen.add(key);
        res.push(ev);
      }
    }
    return res;
  }, [olderEvents, detail.data?.timeline]);

  useEffect(() => {
    if (actualProjectID && actualProjectID !== activeProjectID) {
      setProjectID(actualProjectID);
    }
  }, [activeProjectID, actualProjectID, setProjectID]);

  useEffect(() => {
    if (!card) return;
    const path =
      (card.kind === "discussion"
        ? "/chat/"
        : card.kind === "ongoing"
          ? "/plans/"
          : "/cards/") +
      card.project_id +
      "/" +
      card.id;
    if (location.pathname !== path)
      navigate(path, { replace: true, state: location.state });
  }, [card, navigate, location.pathname, location.state]);

  const latestVisibleMessageSequence = useMemo(
    () =>
      (detail.data?.timeline || []).reduce((max, event) => {
        if (event.type !== "message" && event.type !== "comment") return max;
        return Math.max(max, event.sequence || 0);
      }, 0),
    [detail.data?.timeline],
  );

  // 打开卡片详情或可见消息推进时更新服务端已读游标
  useEffect(() => {
    if (!card?.id) return;
    void api
      .markCardRead(card.id)
      .then((result) => {
        queryClient.setQueryData<Card[]>(
          keys.cards(card.project_id),
          (currentCards) =>
            currentCards?.map((item) => {
              if (item.id !== card.id) return item;
              const nextReadSeq = Math.max(
                item.last_read_message_sequence ?? 0,
                result.last_read_message_sequence,
              );
              const latestSeq = item.latest_message_sequence ?? 0;
              return {
                ...item,
                last_read_message_sequence: nextReadSeq,
                unread: latestSeq > nextReadSeq,
              };
            }),
        );
        return queryClient.invalidateQueries({
          queryKey: keys.cards(card.project_id),
        });
      })
      .catch(() => {
        // 读状态失败不影响卡片展示本身
      });
  }, [card?.id, card?.project_id, latestVisibleMessageSequence]);

  const sync = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.card(cardID) }),
      actualProjectID
        ? queryClient.invalidateQueries({
            queryKey: keys.cards(actualProjectID),
          })
        : Promise.resolve(),
    ]);
  };

  const send = useMutation({
    mutationFn: () => {
      const text = comment.trim();
      const content = replyTo
        ? `> ${replyTo.sender}：\n> ${replyTo.text.trim().split("\n").join("\n> ")}\n\n${text}`
        : text;
      return api.send(
        cardID,
        content,
        "enqueue",
        undefined,
        target,
        card?.preferred_model_route_id || undefined,
      );
    },
    onSuccess: async () => {
      setComment("");
      setReplyTo(null);
      await sync();
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("发送失败")),
  });

  const cancelAttempt = useMutation({
    mutationFn: () => api.cancelAttempt(cardID, current!.id),
    onSuccess: sync,
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("取消失败")),
  });

  const cancelTurn = useMutation({
    mutationFn: (turnID: string) => api.cancelTurn(cardID, turnID),
    onMutate: (turnID) => setCancelingTurnID(turnID),
    onSuccess: sync,
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("取消排队失败")),
    onSettled: () => setCancelingTurnID(""),
  });

  const steerTurn = useMutation({
    mutationFn: (turnID: string) => api.steerTurn(cardID, turnID),
    onMutate: (turnID) => setSteeringTurnID(turnID),
    onSuccess: sync,
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("引导失败")),
    onSettled: () => setSteeringTurnID(""),
  });

  const retryAttempt = useMutation({
    mutationFn: (attemptID: string) => api.retryAttempt(cardID, attemptID),
    onMutate: (attemptID) => setRetryingAttemptID(attemptID),
    onSuccess: sync,
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("重试失败")),
    onSettled: () => setRetryingAttemptID(""),
  });

  const approveReview = useMutation({
    mutationFn: () => api.moveTodo(cardID, "done"),
    onSuccess: async () => {
      await sync();
      toast.success(t("已通过验收"));
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("状态更新失败")),
  });

  const acknowledgeCheckin = useMutation({
    mutationFn: () => api.acknowledgeCheckin(cardID),
    onSuccess: async () => {
      await sync();
      toast.success(t("已标记为已确认"));
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("状态更新失败")),
  });

  function submit() {
    if (!comment.trim() || blocked || send.isPending) return;
    send.mutate();
  }

  // 当由首页新建讨论快速跳转进入时，承接并在后台自动发送首条消息
  useEffect(() => {
    if (!cardID || !pendingInitialTurn) return;
    const messageToSend = pendingInitialTurn;
    const initialKey = navState?.initialIdempotencyKey;
    const initialDevice = navState?.initialDeviceTarget ?? target;

    // 避免重复触发，清理路由 state
    navigate(location.pathname, { replace: true, state: {} });

    api
      .send(cardID, messageToSend, "enqueue", initialKey, initialDevice)
      .then(async () => {
        setPendingInitialTurn(null);
        await sync();
      })
      .catch((error) => {
        setPendingInitialTurn(null);
        // 发送失败时将消息填入输入框，方便用户重试
        setComment(messageToSend);
        toast.error(
          error instanceof Error
            ? error.message
            : t("首条消息发送失败，已保留在输入框"),
        );
      });
    // 仅挂载时根据 initialMessage 触发一次
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cardID]);

  // 首次加载或会话 401 正在切回登录页时展示骨架屏；
  // 若已有卡片缓存数据（如 SSE 重连或切回前台触发的后台静默刷新），偶发报错不卸载当前会话界面。
  if (
    detail.isPending ||
    (!detail.data &&
      detail.error &&
      typeof detail.error === "object" &&
      "status" in detail.error &&
      detail.error.status === 401)
  ) {
    return (
      <section
        ref={containerRef}
        className="relative flex flex-1 min-h-0 min-w-0 flex-col overflow-hidden bg-canvas"
      >
        <ConversationSkeleton
          isDiscussion={isDiscussion}
          onOpenSidebar={onOpenSidebar}
        />
      </section>
    );
  }

  if (!detail.data || !card) {
    return (
      <section
        ref={containerRef}
        className="relative flex flex-1 min-h-0 min-w-0 flex-col overflow-hidden bg-canvas"
      >
        <div className="mx-auto w-full max-w-xl p-5 pt-12">
          <ErrorView
            error={detail.error}
            onRetry={() => void detail.refetch()}
          />
        </div>
      </section>
    );
  }

  const liveTask = stream.live?.task;
  const liveTaskTotal = liveTask?.step_count || liveTask?.steps?.length || 0;
  const liveTaskCompleted = Math.min(
    liveTask?.completed_step_count || 0,
    liveTaskTotal || liveTask?.completed_step_count || 0,
  );
  const titleStatusDotClass =
    card.kind === "todo"
      ? card.todo_state === "working"
        ? "bg-sky-500"
        : card.todo_state === "review"
          ? "bg-amber-500"
          : card.todo_state === "done"
            ? "bg-emerald-500"
            : "bg-slate-400"
      : card.kind === "ongoing"
        ? card.ongoing_state === "active"
          ? "bg-emerald-500"
          : card.ongoing_state === "paused"
            ? "bg-amber-500"
            : "bg-slate-400"
        : null;

  return (
    <section
      ref={containerRef}
      className="relative flex flex-1 min-h-0 min-w-0 flex-col overflow-hidden bg-canvas"
    >
          {/* 手机端悬浮顶部栏：统一极简对称 左 [侧栏] | 中 [● 标题 ⓘ] | 右 [快速切换] */}
          <div
            data-mobile-top-surface
            className="absolute top-0 inset-x-0 z-20 flex items-center justify-between gap-2 px-3 pt-[calc(env(safe-area-inset-top,0px)+0.75rem)] pb-4 sm:px-4 md:hidden pointer-events-none bg-gradient-to-b from-canvas/95 via-canvas/75 to-transparent"
          >
            <div className="flex shrink-0 items-center gap-1.5 pointer-events-auto">
              <button
                type="button"
                title={
                  isDiscussion
                    ? t("打开讨论列表")
                    : isOngoing
                      ? t("打开定时列表")
                      : t("打开任务列表")
                }
                aria-label={
                  isDiscussion
                    ? t("打开讨论列表")
                    : isOngoing
                      ? t("打开定时列表")
                      : t("打开任务列表")
                }
                onClick={onOpenSidebar}
                className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg border border-line bg-surface text-muted-foreground shadow-xs transition hover:bg-muted hover:text-foreground"
              >
                <PanelLeft className="size-4" />
              </button>
            </div>

            <button
              type="button"
              aria-expanded={infoOpen}
              aria-label={t("任务详情")}
              onClick={() => setInfoOpen((prev) => !prev)}
              title={card.title || t("任务详情")}
              className={`pointer-events-auto inline-flex h-8 min-w-0 max-w-[64vw] items-center gap-1.5 rounded-lg border px-2.5 text-xs shadow-xs transition ${
                infoOpen
                  ? "border-primary/40 bg-primary/10 text-primary font-semibold"
                  : "border-line bg-surface text-foreground hover:bg-muted"
              }`}
            >
              {titleStatusDotClass && (
                <span
                  aria-hidden="true"
                  className={`size-1.5 shrink-0 rounded-full ${titleStatusDotClass}`}
                />
              )}
              <span className="truncate font-medium">
                {card.title ||
                  (isDiscussion
                    ? t("未命名讨论")
                    : isOngoing
                      ? t("未命名定时任务")
                      : t("未命名卡片"))}
              </span>
              {liveTaskTotal > 0 && (
                <span className="shrink-0 tabular-nums text-[11px] opacity-80">
                  {liveTaskCompleted}/{liveTaskTotal}
                </span>
              )}
            </button>

            <div className="relative flex shrink-0 items-center gap-1.5 pointer-events-auto">
              <QuickCardSwitcherTrigger
                projectID={card.project_id}
                activeCardID={cardID}
                activeAttemptStatus={activeAttemptStatus}
                onClick={() => setQuickSwitcherOpen(true)}
              />
            </div>
          </div>

          {/* 平板/桌面端：操作按钮悬浮在右上角 */}
          <div className="hidden md:flex absolute top-3 right-3 z-20 max-w-[calc(100vw-1.5rem)] flex-col items-end gap-2 sm:right-4">
            <div className="flex items-center gap-1.5">
              <button
                type="button"
                aria-expanded={infoOpen}
                aria-label={t("任务详情")}
                title={t("任务详情")}
                onClick={() => setInfoOpen((prev) => !prev)}
                className={`inline-flex h-8 shrink-0 items-center justify-center gap-1.5 rounded-lg border text-xs leading-none shadow-xs transition ${
                  liveTaskTotal > 0 ? "px-2.5" : "w-8 sm:w-auto sm:px-2.5"
                } ${
                  infoOpen
                    ? "border-primary/40 bg-primary/10 text-primary font-medium"
                    : "border-line bg-surface text-muted-foreground hover:bg-muted hover:text-foreground"
                }`}
              >
                <Info className="size-4 sm:size-3.5" />
                <span className="hidden sm:inline">{t("任务详情")}</span>
                {liveTaskTotal > 0 && (
                  <span className="tabular-nums text-[11px] opacity-80">
                    {liveTaskCompleted}/{liveTaskTotal}
                  </span>
                )}
              </button>
              <DeviceMenu className="hidden md:inline-flex" />
              {!isDiscussion && <CardStatusSwitch card={card} />}
            </div>
          </div>

          {/* 全局卡片快速切换弹窗（单例挂载，避免多端 CSS 响应式断点导致 Portal 重复渲染与动画打架） */}
          <QuickCardSwitcherModal
            projectID={card.project_id}
            activeCardID={card.id}
            activeAttemptStatus={current?.status}
            open={quickSwitcherOpen}
            onOpenChange={setQuickSwitcherOpen}
          />

          {/* 移动/平板端窄屏：展开任务详情抽屉（使用 Sheet，支持右滑收起与遮罩关闭）。
              注意：Radix UI 的 SheetContent 基于 Dialog.Portal 直接挂载到 document.body 根节点，
              纯 CSS 类名 lg:hidden 无法阻止 Portal 渲染，必须在 React 逻辑层由 isMobile 控制挂载，
              避免桌面端（>=1024px）展开详情时同时出现桌面嵌入卡片与全屏抽屉两个任务详情浮层。 */}
          {isMobile && (
            <div className="lg:hidden">
              <Sheet
                open={infoOpen}
                onOpenChange={setInfoOpen}
                side="right"
                title={
                  isDiscussion
                    ? t("讨论详情")
                    : isOngoing
                      ? t("定时详情")
                      : t("任务详情")
                }
              >
                <CardHeaderInfoCard
                  card={card}
                  detail={detail.data}
                  projectCards={projectCards}
                  task={stream.live?.task}
                  variant="sheet"
                  onSync={sync}
                />
              </Sheet>
            </div>
          )}

          {/* 平板/桌面端：左上角悬浮当前卡片标题胶囊与侧栏切换 */}
          <div className="hidden md:flex absolute top-3 left-3 z-20 max-w-[42%] items-center gap-1.5 sm:left-4">
            <button
              type="button"
              title={
                isDiscussion
                  ? t("打开讨论列表")
                  : isOngoing
                    ? t("打开定时列表")
                    : t("打开任务列表")
              }
              aria-label={
                isDiscussion
                  ? t("打开讨论列表")
                  : isOngoing
                    ? t("打开定时列表")
                    : t("打开任务列表")
              }
              onClick={onOpenSidebar}
              className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg border border-line bg-surface text-muted-foreground shadow-xs transition hover:bg-muted hover:text-foreground lg:hidden"
            >
              <PanelLeft className="size-4" />
            </button>
            <button
              type="button"
              onClick={() => setInfoOpen((prev) => !prev)}
              title={card.title || t("任务详情")}
              className="inline-flex h-8 min-w-0 items-center gap-1.5 rounded-lg border border-line/80 bg-surface/90 px-3 text-xs font-medium text-foreground shadow-xs backdrop-blur-xs transition hover:bg-muted"
            >
              {titleStatusDotClass && (
                <span
                  aria-hidden="true"
                  className={`size-1.5 shrink-0 rounded-full ${titleStatusDotClass}`}
                />
              )}
              <span className="truncate">
                {card.title ||
                  (isDiscussion
                    ? t("未命名讨论")
                    : isOngoing
                      ? t("未命名定时任务")
                      : t("未命名卡片"))}
              </span>
            </button>
            {isOngoing && (
              <OngoingHealthBadge
                health={detail.data.health ?? card.health}
                hideWhenUnknown
                size="xs"
                className="shrink-0 shadow-xs"
              />
            )}
          </div>

          <div className="flex flex-1 min-h-0 min-w-0">
            <div className="relative flex flex-1 min-h-0 min-w-0 flex-col">
              <ConversationView
                key={cardID}
                card={card}
                events={combinedTimeline}
                live={stream.live}
                cardKind={card.kind}
                pendingTurns={
                  pendingInitialTurn &&
                  !detail.data.pending_turns.some(
                    (turn) => turn.content === pendingInitialTurn,
                  )
                    ? [
                        {
                          id: "initial-temp",
                          sequence: 0,
                          source: "human",
                          dispatch_mode: "enqueue",
                          content: pendingInitialTurn,
                          created_at: new Date().toISOString(),
                        } as PendingTurn,
                        ...detail.data.pending_turns,
                      ]
                    : detail.data.pending_turns
                }
                currentAttempt={detail.data.current_attempt}
                attemptHistory={combinedAttempts}
                bottomInsetPx={composerDockHeight}
                hasMoreBefore={hasMoreBefore}
                loadingMoreBefore={loadingMoreBefore}
                onLoadMoreBefore={loadMoreBefore}
                retryingAttemptID={retryingAttemptID}
                onRetryAttempt={(attemptID) => retryAttempt.mutate(attemptID)}
                canApproveReview={
                  card.kind === "todo" && card.todo_state === "review"
                }
                approvingReview={approveReview.isPending}
                onApproveReview={() => approveReview.mutate()}
                canAcknowledgeCheckin={
                  card.kind === "ongoing" &&
                  ((detail.data.health ?? card.health) === "warning" ||
                    (detail.data.health ?? card.health) === "critical")
                }
                acknowledgingCheckin={acknowledgeCheckin.isPending}
                onAcknowledgeCheckin={() => acknowledgeCheckin.mutate()}
                onReply={(quote) => setReplyTo(quote)}
                onEditMessage={
                  !blocked && card.todo_state !== "done"
                    ? async (eventID, content) => {
                        await api.editComment(cardID, eventID, content);
                        setOlderEvents([]);
                        setOlderAttempts([]);
                        await sync();
                      }
                    : undefined
                }
              />

              <div
                data-composer-dock
                className="pointer-events-none absolute inset-x-0 bottom-0 z-30 flex flex-col justify-end bg-gradient-to-t from-canvas via-canvas/95 to-transparent pt-8"
              >
                <div
                  ref={composerDockRef}
                  data-no-swipe="true"
                  className="pointer-events-auto mx-auto w-full max-w-3xl px-2.5 pt-2 pb-[max(0.5rem,calc(env(safe-area-inset-bottom,0px)*0.6))] sm:px-6 sm:pb-3"
                >
                  {card.kind === "todo" && card.todo_state === "review" && (
                    <div className="mb-2 flex items-center justify-between gap-2 rounded-xl border border-amber-500/25 bg-surface/95 px-3 py-1.5 text-xs shadow-2xs backdrop-blur-xs">
                      <div className="flex min-w-0 items-center gap-2">
                        <span className="size-2 shrink-0 rounded-full bg-amber-500" />
                        <span className="truncate font-medium text-foreground/90">
                          {t("任务待验收")}
                        </span>
                      </div>
                      <div className="flex shrink-0 items-center gap-1.5">
                        <CardStatusSwitch
                          card={card}
                          className="h-7 px-2 text-[11px]"
                        />
                        <button
                          type="button"
                          disabled={approveReview.isPending}
                          onClick={() => approveReview.mutate()}
                          className="inline-flex h-7 shrink-0 cursor-pointer items-center gap-1 rounded-lg bg-emerald-600 px-2.5 text-xs font-medium text-white shadow-2xs transition hover:bg-emerald-500 active:scale-95 disabled:opacity-50"
                        >
                          <Check className="size-3.5" />
                          <span>{t("通过验收")}</span>
                        </button>
                      </div>
                    </div>
                  )}
                  {card.kind === "ongoing" &&
                    ((detail.data.health ?? card.health) === "warning" ||
                      (detail.data.health ?? card.health) === "critical") && (
                      <div
                        className={`mb-2 flex items-center justify-between gap-2 rounded-xl border bg-surface/95 px-3 py-1.5 text-xs shadow-2xs backdrop-blur-xs ${
                          (detail.data.health ?? card.health) === "critical"
                            ? "border-rose-500/25"
                            : "border-amber-500/25"
                        }`}
                      >
                        <div className="flex min-w-0 items-center gap-2">
                          <span
                            className={`size-2 shrink-0 rounded-full ${
                              (detail.data.health ?? card.health) === "critical"
                                ? "bg-rose-500"
                                : "bg-amber-500"
                            }`}
                          />
                          <span className="truncate font-medium text-foreground/90">
                            {(detail.data.health ?? card.health) === "critical"
                              ? t("巡检状态：异常")
                              : t("巡检状态：需留意")}
                          </span>
                        </div>
                        <div className="flex shrink-0 items-center gap-1.5">
                          <button
                            type="button"
                            disabled={acknowledgeCheckin.isPending}
                            onClick={() => acknowledgeCheckin.mutate()}
                            className="inline-flex h-7 shrink-0 cursor-pointer items-center gap-1 rounded-lg bg-emerald-600 px-2.5 text-xs font-medium text-white shadow-2xs transition hover:bg-emerald-500 active:scale-95 disabled:opacity-50"
                          >
                            <Check className="size-3.5" />
                            <span>{t("已确认")}</span>
                          </button>
                        </div>
                      </div>
                    )}
                  {blocked && (
                    <p className="mb-2 text-center text-xs text-faint">
                      {t("此讨论已归档，不能继续发送消息")}
                    </p>
                  )}
                  <PendingTurnQueue
                    turns={detail.data.pending_turns}
                    cancelingTurnID={cancelingTurnID}
                    steeringTurnID={steeringTurnID}
                    onCancel={(turnID) => cancelTurn.mutate(turnID)}
                    onSteer={(turnID) => steerTurn.mutate(turnID)}
                    onEdit={async (turnID, content) => {
                      await api.editTurn(cardID, turnID, content);
                      await sync();
                    }}
                  />
                  <ConversationComposer
                    cardID={cardID}
                    projectID={actualProjectID}
                    selectedRouteId={card?.preferred_model_route_id}
                    comment={comment}
                    setComment={setComment}
                    replyTo={replyTo}
                    onCancelReply={() => setReplyTo(null)}
                    blocked={blocked}
                    hasCurrent={hasCurrent}
                    sendPending={send.isPending}
                    streamConnected={stream.connected}
                    streamError={stream.lastError}
                    cancelPending={cancelAttempt.isPending}
                    onSubmit={submit}
                    onCancel={() => cancelAttempt.mutate()}
                  />
                </div>
              </div>
            </div>

            {/* 桌面端：右上角嵌入圆角详情卡片（非全高侧边栏），展开时左侧聊天与输入框自动左移让位 */}
            {!isMobile && infoOpen && (
              <div className="hidden lg:block shrink-0 pt-14 pr-4 pb-4">
                <CardHeaderInfoCard
                  card={card}
                  detail={detail.data}
                  projectCards={projectCards}
                  task={stream.live?.task}
                  onSync={sync}
                  onClose={() => setInfoOpen(false)}
                />
              </div>
            )}
          </div>
        </section>
  );
}
