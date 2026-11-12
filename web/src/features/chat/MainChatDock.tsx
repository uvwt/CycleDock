import { Suspense, lazy, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useLocation, useNavigate } from "react-router-dom";
import { Expand, X } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { useProject } from "../../app/project-context";
import { useDeviceSelection } from "../devices/device-selection";
import type {
  Attempt,
  CardDetail,
  Identity,
  TimelineEvent,
} from "../../shared/api/types";
import {
  applyOptimisticSentTurn,
  upsertCachedProjectCard,
} from "../../shared/lib/card-cache";
import type { QuotedMessage } from "../timeline/ConversationView";
import { useCardStream } from "../../shared/realtime/useCardStream";
import {
  cardMessageEvent,
  type CardMessageDetail,
} from "../../shared/realtime/cardUpdates";
import { Spinner } from "../../shared/ui/components";

const ConversationView = lazy(() =>
  import("../timeline/ConversationView").then((m) => ({
    default: m.ConversationView,
  })),
);
const PendingTurnQueue = lazy(() =>
  import("../cards/PendingTurnQueue").then((m) => ({
    default: m.PendingTurnQueue,
  })),
);
const ConversationComposer = lazy(() =>
  import("../cards/ConversationComposer").then((m) => ({
    default: m.ConversationComposer,
  })),
);
const AgentTracePanel = lazy(() =>
  import("../trace/AgentTracePanel").then((m) => ({
    default: m.AgentTracePanel,
  })),
);

const BUBBLE_BOTTOM_STORAGE_KEY = "cycledock.main-chat.bubble-bottom";

export function MainChatDock() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const { projectID } = useProject();
  const { target } = useDeviceSelection();

  const main = useQuery({
    queryKey: keys.mainDiscussion(projectID),
    queryFn: () => api.mainDiscussion(projectID),
    enabled: Boolean(projectID),
  });
  const cardID = main.data?.id || "";

  const isFocusedConversation =
    /^\/(?:chat|cards|plans)\/[^/]+\/[^/]+\/?$/.test(location.pathname);

  // 仅在当前主区域正处于主聊天自身详情页或设置页面时全局隐藏，避免在主聊天全屏视图中重叠小浮窗，以及在设置表单页遮挡内容与控件；
  // 处于具体会话详情页（讨论/待办/计划）时，上调悬浮球及 Peek 气泡的垂直高度，避免遮挡底部输入框及操作按钮
  const isViewingMainDiscussion =
    Boolean(cardID) &&
    (location.pathname === `/chat/${encodeURIComponent(projectID)}/${encodeURIComponent(cardID)}` ||
      location.pathname === `/chat/${projectID}/${cardID}` ||
      location.pathname.endsWith(`/${cardID}`) ||
      location.pathname.endsWith(`/${cardID}/`));
  const isSettings = location.pathname.startsWith("/settings");
  const hidden = isViewingMainDiscussion || isSettings;

  const [open, setOpen] = useState(false);
  const [peek, setPeek] = useState("");
  const [comment, setComment] = useState("");
  const [replyTo, setReplyTo] = useState<QuotedMessage | null>(null);
  const [cancelingTurnID, setCancelingTurnID] = useState("");
  const [steeringTurnID, setSteeringTurnID] = useState("");
  const [retryingAttemptID, setRetryingAttemptID] = useState("");
  const [traceAttemptID, setTraceAttemptID] = useState("");

  const [position, setPosition] = useState<{ x: number; y: number } | null>(null);
  const [size, setSize] = useState<{ width: number; height: number } | null>(null);

  const windowRef = useRef<HTMLElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);

  const [bubbleBottom, setBubbleBottom] = useState<number | null>(() => {
    try {
      const saved = localStorage.getItem(BUBBLE_BOTTOM_STORAGE_KEY);
      if (saved !== null) {
        const val = parseFloat(saved);
        if (Number.isFinite(val)) return val;
      }
    } catch {
      // 避免部分私密浏览环境访问 localStorage 报错
    }
    return null;
  });
  const [isBubbleDragging, setIsBubbleDragging] = useState(false);
  const bubbleDragRef = useRef<{
    startY: number;
    startBottom: number;
    hasMoved: boolean;
  } | null>(null);
  const suppressBubbleClickRef = useRef(false);

  // 监听窗口大小变化，确保用户自定义悬浮球垂直高度始终落在安全可视区域内
  useEffect(() => {
    function handleResize() {
      setBubbleBottom((prev) => {
        if (prev === null) return null;
        const minBottom = 16;
        const maxBottom = Math.max(minBottom, window.innerHeight - 56 - 16);
        return Math.min(Math.max(minBottom, prev), maxBottom);
      });
    }
    window.addEventListener("resize", handleResize);
    return () => window.removeEventListener("resize", handleResize);
  }, []);

  // 悬浮球及消息气泡上下拖拽逻辑（统一兼容鼠标与触屏事件）
  function handleBubblePointerDown(e: React.PointerEvent) {
    if (e.button !== 0) return;

    const rect = triggerRef.current?.getBoundingClientRect();
    const currentBottom = rect ? window.innerHeight - rect.bottom : 20;

    bubbleDragRef.current = {
      startY: e.clientY,
      startBottom: currentBottom,
      hasMoved: false,
    };

    function onPointerMove(ev: PointerEvent) {
      if (!bubbleDragRef.current) return;
      const deltaY = ev.clientY - bubbleDragRef.current.startY;

      // 超过 4px 位移判定为拖拽意图，避免正常轻击时误触移动状态
      if (!bubbleDragRef.current.hasMoved) {
        if (Math.abs(deltaY) > 4) {
          bubbleDragRef.current.hasMoved = true;
          suppressBubbleClickRef.current = true;
          setIsBubbleDragging(true);
        } else {
          return;
        }
      }

      // 屏幕向上拖拽 deltaY < 0，对应 bottom 增加
      const nextBottom = bubbleDragRef.current.startBottom - deltaY;
      const minBottom = 16;
      const maxBottom = Math.max(minBottom, window.innerHeight - 56 - 16);
      const clamped = Math.min(Math.max(minBottom, nextBottom), maxBottom);

      setBubbleBottom(clamped);
    }

    function onPointerUp() {
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", onPointerUp);
      window.removeEventListener("pointercancel", onPointerUp);

      setIsBubbleDragging(false);

      if (bubbleDragRef.current?.hasMoved) {
        // 用户发生拖拽，持久化用户自定义的垂直高度
        setBubbleBottom((latest) => {
          if (latest !== null) {
            try {
              localStorage.setItem(BUBBLE_BOTTOM_STORAGE_KEY, String(latest));
            } catch {
              // 忽略本地存储写入失败
            }
          }
          return latest;
        });
      } else {
        suppressBubbleClickRef.current = false;
      }

      bubbleDragRef.current = null;
    }

    window.addEventListener("pointermove", onPointerMove);
    window.addEventListener("pointerup", onPointerUp);
    window.addEventListener("pointercancel", onPointerUp);
  }

  function handleTriggerClick(e: React.MouseEvent) {
    if (suppressBubbleClickRef.current) {
      suppressBubbleClickRef.current = false;
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    show();
  }

  function handlePeekClick(e: React.MouseEvent) {
    if (suppressBubbleClickRef.current) {
      suppressBubbleClickRef.current = false;
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    show();
  }
  const detail = useQuery({
    queryKey: keys.card(cardID),
    queryFn: () => api.card(cardID),
    enabled: open && Boolean(cardID),
  });
  const stream = useCardStream(open ? cardID : "", projectID);

  useEffect(() => {
    setOpen(false);
    setPeek("");
  }, [projectID]);

  useEffect(() => {
    if (hidden) setOpen(false);
  }, [hidden]);

  useEffect(() => {
    if (!peek) return;
    const timer = window.setTimeout(() => setPeek(""), 5000);
    return () => window.clearTimeout(timer);
  }, [peek]);

  const openChat = useMutation({
    mutationFn: () => api.openMainDiscussion(projectID),
    onSuccess: (card) => {
      queryClient.setQueryData(keys.mainDiscussion(projectID), card);
      upsertCachedProjectCard(queryClient, projectID, card);
      void queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("发送失败")),
  });

  const [olderEvents, setOlderEvents] = useState<TimelineEvent[]>([]);
  const [olderAttempts, setOlderAttempts] = useState<Attempt[]>([]);
  const [hasMoreBefore, setHasMoreBefore] = useState(false);
  const [loadingMoreBefore, setLoadingMoreBefore] = useState(false);

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
    const minSeq = allCurrentEvents.reduce(
      (min, ev) => (ev.sequence && ev.sequence < min ? ev.sequence : min),
      allCurrentEvents[0]?.sequence || 0,
    );
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

  const card = detail.data?.card;
  const current = detail.data?.current_attempt;
  const hasCurrent =
    Boolean(current) &&
    ["queued", "running"].includes(current?.status || "");
  const blocked = card?.discussion_state === "archived";

  async function sync() {
    if (!cardID) return;
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.card(cardID) }),
      queryClient.invalidateQueries({ queryKey: keys.cards(projectID) }),
    ]);
  }

  const send = useMutation({
    mutationFn: async () => {
      const text = comment.trim();
      const content = replyTo
        ? `> ${replyTo.sender}：\n> ${replyTo.text.trim().split("\n").join("\n> ")}\n\n${text}`
        : text;
      const deviceTarget = target;
      const result = await api.send(
        cardID,
        content,
        "enqueue",
        undefined,
        deviceTarget,
        main.data?.preferred_model_route_id || undefined,
      );
      return { ...result, content, deviceTarget };
    },
    onSuccess: (result) => {
      setComment("");
      setReplyTo(null);
      const me = queryClient.getQueryData<Identity>(keys.me);
      applyOptimisticSentTurn(queryClient, cardID, {
        eventID: result.event_id,
        turnID: result.turn_id,
        content: result.content,
        actorName: me?.display_name,
        deviceTarget: result.deviceTarget,
      });
      void sync();
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("发送失败")),
  });

  const cancelAttempt = useMutation({
    mutationFn: () => api.cancelAttempt(cardID, current!.id),
    onSuccess: () => {
      queryClient.setQueryData<CardDetail>(keys.card(cardID), (old) =>
        old && old.card ? { ...old, current_attempt: null } : old,
      );
      void sync();
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("取消失败")),
  });

  const cancelTurn = useMutation({
    mutationFn: (turnID: string) => api.cancelTurn(cardID, turnID),
    onMutate: (turnID) => setCancelingTurnID(turnID),
    onSuccess: (_, turnID) => {
      queryClient.setQueryData<CardDetail>(keys.card(cardID), (old) => {
        if (!old || !old.card) return old;
        const removed = old.pending_turns.find((item) => item.id === turnID);
        return {
          ...old,
          pending_turns: old.pending_turns.filter((item) => item.id !== turnID),
          timeline: removed?.source_event_id
            ? old.timeline.filter((ev) => ev.id !== removed.source_event_id)
            : old.timeline,
        };
      });
      void sync();
    },
    onSettled: () => setCancelingTurnID(""),
  });

  const steerTurn = useMutation({
    mutationFn: (turnID: string) => api.steerTurn(cardID, turnID),
    onMutate: (turnID) => setSteeringTurnID(turnID),
    onSuccess: (_, turnID) => {
      queryClient.setQueryData<CardDetail>(keys.card(cardID), (old) => {
        if (!old || !old.card) return old;
        const steered = old.pending_turns.find((item) => item.id === turnID);
        return {
          ...old,
          pending_turns: old.pending_turns.filter((item) => item.id !== turnID),
          current_attempt:
            old.current_attempt && steered
              ? {
                  ...old.current_attempt,
                  turn_id: steered.id,
                  source_event_id:
                    steered.source_event_id ||
                    old.current_attempt.source_event_id,
                }
              : old.current_attempt,
        };
      });
      void sync();
    },
    onSettled: () => setSteeringTurnID(""),
  });

  const retryAttempt = useMutation({
    mutationFn: (attemptID: string) => api.retryAttempt(cardID, attemptID),
    onMutate: (attemptID) => setRetryingAttemptID(attemptID),
    onSuccess: () => {
      void sync();
    },
    onSettled: () => setRetryingAttemptID(""),
  });

  useEffect(() => {
    if (!projectID) return;
    const onMessage = (event: Event) => {
      const detail = (event as CustomEvent<CardMessageDetail>).detail;
      if (
        !detail ||
        detail.project_id !== projectID ||
        !detail.body ||
        detail.viewing
      ) {
        return;
      }
      if (cardID) {
        void queryClient.invalidateQueries({ queryKey: keys.card(cardID) });
      }
      void queryClient.invalidateQueries({
        queryKey: keys.mainDiscussion(projectID),
      });
      if (open) return;
      setPeek(detail.body);
    };
    window.addEventListener(cardMessageEvent, onMessage);
    return () => window.removeEventListener(cardMessageEvent, onMessage);
  }, [cardID, open, projectID]);

  function show() {
    setOpen(true);
    setPeek("");
    if (!cardID) openChat.mutate();
  }

  // 点击外部自动关闭浮窗（排除窗口本体和触发按钮）
  useEffect(() => {
    if (!open) return;
    function handleClickOutside(event: MouseEvent | TouchEvent) {
      const target = event.target as Node | null;
      if (!target) return;
      if (windowRef.current && windowRef.current.contains(target)) return;
      if (triggerRef.current && triggerRef.current.contains(target)) return;
      setOpen(false);
    }

    // 捕获阶段或冒泡均可，用 mousedown / touchstart 避免拖拽释放时的误触发
    document.addEventListener("mousedown", handleClickOutside);
    document.addEventListener("touchstart", handleClickOutside);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("touchstart", handleClickOutside);
    };
  }, [open]);

  // 移动端浮窗打开时，锁定底层页面滚动（兼容 iOS Safari / WebKit 与 Android）并拦截未捕获的滚动链
  useEffect(() => {
    if (!open) return;

    // 仅在移动端小屏（< 640px）或者具有触摸交互且视口较小时执行底层滚动锁定
    const isMobile = window.innerWidth < 640 || window.matchMedia("(max-width: 639px)").matches;
    if (!isMobile) return;

    const scrollY = window.scrollY || window.pageYOffset || 0;
    const body = document.body;
    const documentElement = document.documentElement;

    const prevBodyOverflow = body.style.overflow;
    const prevBodyPosition = body.style.position;
    const prevBodyTop = body.style.top;
    const prevBodyWidth = body.style.width;
    const prevDocOverflow = documentElement.style.overflow;

    // 锁定 body 滚动并保持当前滚动偏移量
    body.style.overflow = "hidden";
    body.style.position = "fixed";
    body.style.top = `-${scrollY}px`;
    body.style.width = "100%";
    documentElement.style.overflow = "hidden";

    return () => {
      body.style.overflow = prevBodyOverflow;
      body.style.position = prevBodyPosition;
      body.style.top = prevBodyTop;
      body.style.width = prevBodyWidth;
      documentElement.style.overflow = prevDocOverflow;
      window.scrollTo(0, scrollY);
    };
  }, [open]);

  // 拖拽窗口移动
  function handleDragStart(e: React.MouseEvent<HTMLElement>) {
    if ((e.target as HTMLElement).closest("button, a, input, textarea")) return;
    e.preventDefault();

    const el = windowRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const startX = e.clientX;
    const startY = e.clientY;
    const initialLeft = rect.left;
    const initialTop = rect.top;
    const currentWidth = rect.width;
    const currentHeight = rect.height;

    // 如果之前没有记录过 size，初始化锁定当前尺寸，防止从类名布局切换到 position: fixed 时尺寸跳变
    if (!size) {
      setSize({ width: currentWidth, height: currentHeight });
    }

    function onMouseMove(ev: MouseEvent) {
      const deltaX = ev.clientX - startX;
      const deltaY = ev.clientY - startY;

      // 视口边界约束
      const maxLeft = Math.max(0, window.innerWidth - currentWidth);
      const maxTop = Math.max(0, window.innerHeight - currentHeight);

      const nextLeft = Math.min(Math.max(0, initialLeft + deltaX), maxLeft);
      const nextTop = Math.min(Math.max(0, initialTop + deltaY), maxTop);

      setPosition({ x: nextLeft, y: nextTop });
    }

    function onMouseUp() {
      window.removeEventListener("mousemove", onMouseMove);
      window.removeEventListener("mouseup", onMouseUp);
    }

    window.addEventListener("mousemove", onMouseMove);
    window.addEventListener("mouseup", onMouseUp);
  }

  // 调整尺寸（resize）处理
  type ResizeDirection = "n" | "s" | "e" | "w" | "ne" | "nw" | "se" | "sw";

  function handleResizeStart(direction: ResizeDirection, e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();

    const el = windowRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();

    const startX = e.clientX;
    const startY = e.clientY;
    const startLeft = rect.left;
    const startTop = rect.top;
    const startWidth = rect.width;
    const startHeight = rect.height;

    const minWidth = 320;
    const minHeight = 240;
    const maxWidth = Math.max(minWidth, window.innerWidth - 16);
    const maxHeight = Math.max(minHeight, window.innerHeight - 16);

    function onMouseMove(ev: MouseEvent) {
      const deltaX = ev.clientX - startX;
      const deltaY = ev.clientY - startY;

      let newLeft = startLeft;
      let newTop = startTop;
      let newWidth = startWidth;
      let newHeight = startHeight;

      // 宽度与水平位置
      if (direction.includes("e")) {
        newWidth = Math.min(maxWidth, Math.max(minWidth, startWidth + deltaX));
      } else if (direction.includes("w")) {
        const potentialWidth = startWidth - deltaX;
        if (potentialWidth < minWidth) {
          newWidth = minWidth;
          newLeft = startLeft + (startWidth - minWidth);
        } else if (potentialWidth > maxWidth) {
          newWidth = maxWidth;
          newLeft = startLeft + (startWidth - maxWidth);
        } else {
          newWidth = potentialWidth;
          newLeft = startLeft + deltaX;
        }
      }

      // 高度与垂直位置
      if (direction.includes("s")) {
        newHeight = Math.min(maxHeight, Math.max(minHeight, startHeight + deltaY));
      } else if (direction.includes("n")) {
        const potentialHeight = startHeight - deltaY;
        if (potentialHeight < minHeight) {
          newHeight = minHeight;
          newTop = startTop + (startHeight - minHeight);
        } else if (potentialHeight > maxHeight) {
          newHeight = maxHeight;
          newTop = startTop + (startHeight - maxHeight);
        } else {
          newHeight = potentialHeight;
          newTop = startTop + deltaY;
        }
      }

      // 限制在屏幕内
      newLeft = Math.min(Math.max(0, newLeft), window.innerWidth - newWidth);
      newTop = Math.min(Math.max(0, newTop), window.innerHeight - newHeight);

      setPosition({ x: newLeft, y: newTop });
      setSize({ width: newWidth, height: newHeight });
    }

    function onMouseUp() {
      window.removeEventListener("mousemove", onMouseMove);
      window.removeEventListener("mouseup", onMouseUp);
    }

    window.addEventListener("mousemove", onMouseMove);
    window.addEventListener("mouseup", onMouseUp);
  }

  if (!projectID || hidden) return null;

  return (
    <>
      {open && (
        <section
          ref={windowRef}
          aria-label={t("主聊天")}
          style={
            position && size
              ? {
                  position: "fixed",
                  left: `${position.x}px`,
                  top: `${position.y}px`,
                  width: `${size.width}px`,
                  height: `${size.height}px`,
                  margin: 0,
                }
              : position
                ? {
                    position: "fixed",
                    left: `${position.x}px`,
                    top: `${position.y}px`,
                    margin: 0,
                  }
                : size
                  ? {
                      width: `${size.width}px`,
                      height: `${size.height}px`,
                    }
                  : undefined
          }
          className={`z-40 flex flex-col overflow-hidden rounded-2xl border border-line bg-canvas shadow-xl select-none overscroll-contain touch-manipulation ${
            position
              ? size
                ? ""
                : "h-[min(560px,70dvh)] w-[min(400px,calc(100vw-2rem))]"
              : "absolute right-4 bottom-[max(1.25rem,env(safe-area-inset-bottom))] h-[min(560px,70dvh)] w-[min(400px,calc(100vw-2rem))]"
          }`}
        >
          {/* Resize handles */}
          <div
            onMouseDown={(e) => handleResizeStart("n", e)}
            className="absolute top-0 left-2 right-2 h-2 cursor-ns-resize z-50"
          />
          <div
            onMouseDown={(e) => handleResizeStart("s", e)}
            className="absolute bottom-0 left-2 right-2 h-2 cursor-ns-resize z-50"
          />
          <div
            onMouseDown={(e) => handleResizeStart("w", e)}
            className="absolute top-2 bottom-2 left-0 w-2 cursor-ew-resize z-50"
          />
          <div
            onMouseDown={(e) => handleResizeStart("e", e)}
            className="absolute top-2 bottom-2 right-0 w-2 cursor-ew-resize z-50"
          />
          <div
            onMouseDown={(e) => handleResizeStart("nw", e)}
            className="absolute top-0 left-0 size-3 cursor-nwse-resize z-50"
          />
          <div
            onMouseDown={(e) => handleResizeStart("ne", e)}
            className="absolute top-0 right-0 size-3 cursor-nesw-resize z-50"
          />
          <div
            onMouseDown={(e) => handleResizeStart("sw", e)}
            className="absolute bottom-0 left-0 size-3 cursor-nesw-resize z-50"
          />
          <div
            onMouseDown={(e) => handleResizeStart("se", e)}
            className="absolute bottom-0 right-0 size-3 cursor-nwse-resize z-50"
          />

          <header
            onMouseDown={handleDragStart}
            onTouchMove={(e) => {
              // 移动端浮窗头部不应拖动页面或触发滚动穿透
              e.preventDefault();
            }}
            className="absolute top-0 inset-x-0 z-20 flex cursor-move items-center justify-between gap-2 bg-gradient-to-b from-canvas/95 via-canvas/70 to-transparent px-3 pt-2.5 pb-3 select-none"
          >
            <div className="pointer-events-none inline-flex h-7 min-w-0 max-w-[68%] items-center gap-1.5 rounded-lg border border-line/75 bg-surface/85 px-2.5 shadow-2xs backdrop-blur-md">
              <img
                src="/cycledock-logo.png"
                alt=""
                className="size-4 shrink-0 object-contain"
                draggable={false}
              />
              <h2 className="truncate text-xs font-semibold text-foreground">
                {card?.title || t("主聊天")}
              </h2>
            </div>

            <div className="flex shrink-0 items-center gap-1.5">
              <button
                type="button"
                title={t("在完整页面打开")}
                aria-label={t("在完整页面打开")}
                disabled={!cardID}
                onClick={() =>
                  cardID &&
                  navigate(
                    `/chat/${encodeURIComponent(projectID)}/${encodeURIComponent(cardID)}`,
                  )
                }
                className="grid size-7 place-items-center rounded-lg border border-line/75 bg-surface/85 text-muted-foreground shadow-2xs backdrop-blur-md transition hover:bg-muted hover:text-foreground disabled:opacity-40"
              >
                <Expand className="size-3.5" />
              </button>
              <button
                type="button"
                title={t("关闭主聊天")}
                aria-label={t("关闭主聊天")}
                onClick={() => setOpen(false)}
                className="grid size-7 place-items-center rounded-lg border border-line/75 bg-surface/85 text-muted-foreground shadow-2xs backdrop-blur-md transition hover:bg-muted hover:text-foreground"
              >
                <X className="size-3.5" />
              </button>
            </div>
          </header>

          <Suspense
            fallback={
              <div className="grid min-h-0 flex-1 place-items-center">
                <Spinner />
              </div>
            }
          >
            <div className="flex min-h-0 flex-1 flex-col select-auto">
              {!cardID || detail.isPending || openChat.isPending ? (
                <div className="grid flex-1 place-items-center">
                  <Spinner />
                </div>
              ) : (
                <ConversationView
                  events={combinedTimeline}
                  live={stream.live}
                  cardKind="discussion"
                  pendingTurns={detail.data?.pending_turns || []}
                  currentAttempt={current || null}
                  attemptHistory={combinedAttempts}
                  hasMoreBefore={hasMoreBefore}
                  loadingMoreBefore={loadingMoreBefore}
                  onLoadMoreBefore={loadMoreBefore}
                  bottomSpacerClass="h-2 shrink-0"
                  compact
                  retryingAttemptID={retryingAttemptID}
                  onRetryAttempt={(attemptID) => retryAttempt.mutate(attemptID)}
                  onOpenTrace={(attemptID) => setTraceAttemptID(attemptID)}
                  onReply={setReplyTo}
                  onEditMessage={async (eventID, content) => {
                    await api.editComment(cardID, eventID, content);
                    setOlderEvents([]);
                    setOlderAttempts([]);
                    queryClient.setQueryData<CardDetail>(
                      keys.card(cardID),
                      (old) =>
                        old && old.card
                          ? {
                              ...old,
                              timeline: old.timeline.map((ev) =>
                                ev.id === eventID ? { ...ev, content } : ev,
                              ),
                            }
                          : old,
                    );
                    void sync();
                  }}
                />
              )}
            </div>

            <div className="shrink-0 space-y-2 bg-canvas px-2.5 pt-1 pb-2.5 select-auto">
              <PendingTurnQueue
                turns={detail.data?.pending_turns || []}
                cancelingTurnID={cancelingTurnID}
                steeringTurnID={steeringTurnID}
                onCancel={(turnID) => cancelTurn.mutate(turnID)}
                onSteer={(turnID) => steerTurn.mutate(turnID)}
                onEdit={async (turnID, content) => {
                  await api.editTurn(cardID, turnID, content);
                  queryClient.setQueryData<CardDetail>(
                    keys.card(cardID),
                    (old) => {
                      if (!old || !old.card) return old;
                      return {
                        ...old,
                        pending_turns: old.pending_turns.map((item) =>
                          item.id === turnID ? { ...item, content } : item,
                        ),
                      };
                    },
                  );
                  void sync();
                }}
              />
              <ConversationComposer
                cardID={cardID}
                projectID={projectID}
                selectedRouteId={main.data?.preferred_model_route_id}
                comment={comment}
                setComment={setComment}
                replyTo={replyTo}
                onCancelReply={() => setReplyTo(null)}
                blocked={Boolean(blocked) || !cardID}
                hasCurrent={hasCurrent}
                sendPending={send.isPending}
                streamConnected={stream.connected || !cardID}
                streamError={stream.lastError}
                cancelPending={cancelAttempt.isPending}
                onSubmit={() => {
                  if (!comment.trim() || blocked || send.isPending) return;
                  send.mutate();
                }}
                onCancel={() => cancelAttempt.mutate()}
              />
            </div>
          </Suspense>
        </section>
      )}

      {!open && (
        <div
          style={bubbleBottom !== null ? { bottom: `${bubbleBottom}px` } : undefined}
          className={`absolute right-4 z-40 flex flex-col items-end gap-3 pointer-events-none ${
            isBubbleDragging ? "select-none" : "transition-[bottom] duration-200"
          } ${
            bubbleBottom !== null
              ? ""
              : isFocusedConversation
                ? "bottom-[calc(max(1.25rem,env(safe-area-inset-bottom))+4.5rem)]"
                : "bottom-[max(1.25rem,env(safe-area-inset-bottom))]"
          }`}
        >
          {peek && (
            <button
              type="button"
              onClick={handlePeekClick}
              onPointerDown={handleBubblePointerDown}
              className="pointer-events-auto max-w-[min(20rem,calc(100vw-2rem))] rounded-2xl border border-line bg-surface px-3 py-2 text-left text-xs text-foreground shadow-lg transition hover:bg-muted active:scale-95 cursor-grab active:cursor-grabbing touch-none select-none"
            >
              <span className="line-clamp-2">{peek}</span>
            </button>
          )}

          <button
            ref={triggerRef}
            type="button"
            title={t("打开主聊天")}
            aria-label={t("打开主聊天")}
            onClick={handleTriggerClick}
            onPointerDown={handleBubblePointerDown}
            className="pointer-events-auto relative grid size-14 place-items-center rounded-full border border-line bg-surface shadow-lg transition hover:bg-muted active:scale-95 cursor-grab active:cursor-grabbing touch-none select-none"
          >
            <img
              src="/cycledock-logo.png"
              alt=""
              className="size-8 object-contain pointer-events-none"
              draggable={false}
            />
          </button>
        </div>
      )}
      {traceAttemptID && (
        <Suspense fallback={null}>
          <AgentTracePanel
            cardID={cardID}
            attemptID={traceAttemptID}
            onClose={() => setTraceAttemptID("")}
          />
        </Suspense>
      )}
    </>
  );
}
