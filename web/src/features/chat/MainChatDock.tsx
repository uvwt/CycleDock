import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useLocation, useNavigate } from "react-router-dom";
import { Expand, X } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { useProject } from "../../app/project-context";
import { useDeviceSelection } from "../devices/device-selection";
import { ConversationComposer } from "../cards/ConversationComposer";
import { PendingTurnQueue } from "../cards/PendingTurnQueue";
import {
  ConversationView,
  type QuotedMessage,
} from "../timeline/ConversationView";
import { useCardStream } from "../../shared/realtime/useCardStream";
import {
  cardMessageEvent,
  type CardMessageDetail,
} from "../../shared/realtime/cardUpdates";
import { Spinner } from "../../shared/ui/components";

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

  // 仅在当前主区域正处于主聊天自身详情页时全局隐藏，避免在主聊天全屏视图中重叠小浮窗；
  // 处于具体会话详情页（讨论/待办/计划）时，上调悬浮球及 Peek 气泡的垂直高度，避免遮挡底部输入框及操作按钮
  const isViewingMainDiscussion =
    Boolean(cardID) &&
    (location.pathname === `/chat/${encodeURIComponent(projectID)}/${encodeURIComponent(cardID)}` ||
      location.pathname === `/chat/${projectID}/${cardID}` ||
      location.pathname.endsWith(`/${cardID}`) ||
      location.pathname.endsWith(`/${cardID}/`));
  const hidden = isViewingMainDiscussion;

  const [open, setOpen] = useState(false);
  const [peek, setPeek] = useState("");
  const [comment, setComment] = useState("");
  const [replyTo, setReplyTo] = useState<QuotedMessage | null>(null);
  const [cancelingTurnID, setCancelingTurnID] = useState("");
  const [steeringTurnID, setSteeringTurnID] = useState("");
  const [retryingAttemptID, setRetryingAttemptID] = useState("");

  const [position, setPosition] = useState<{ x: number; y: number } | null>(null);
  const [size, setSize] = useState<{ width: number; height: number } | null>(null);

  const windowRef = useRef<HTMLElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
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
    onSuccess: async (card) => {
      queryClient.setQueryData(keys.mainDiscussion(projectID), card);
      await queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("发送失败")),
  });

  const card = detail.data?.card;
  const current = detail.data?.current_attempt;
  const waiting = current?.status === "waiting_human";
  const hasCurrent =
    Boolean(current) &&
    ["queued", "running", "waiting_human"].includes(current?.status || "");
  const blocked = card?.discussion_state === "archived";

  async function sync() {
    if (!cardID) return;
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.card(cardID) }),
      queryClient.invalidateQueries({ queryKey: keys.cards(projectID) }),
    ]);
  }

  const send = useMutation({
    mutationFn: (mode: "enqueue" | "reply") => {
      const text = comment.trim();
      const content = replyTo
        ? `> ${replyTo.sender}：\n> ${replyTo.text.trim().split("\n").join("\n> ")}\n\n${text}`
        : text;
      if (mode === "reply") {
        if (!current || current.status !== "waiting_human") {
          return Promise.reject(new Error(t("当前回复已失效，请刷新后重试")));
        }
        return api.reply(cardID, content, current.id, undefined, target);
      }
      return api.send(cardID, content, "enqueue", undefined, target);
    },
    onSuccess: async () => {
      setComment("");
      setReplyTo(null);
      await sync();
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("发送失败")),
  });

  const choose = useMutation({
    mutationFn: (choice: {
      value: string;
      attemptID: string;
      messageID: string;
    }) =>
      api.reply(
        cardID,
        choice.value,
        choice.attemptID,
        choice.messageID,
        target,
      ),
    onSuccess: sync,
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
    onSettled: () => setCancelingTurnID(""),
  });

  const steerTurn = useMutation({
    mutationFn: (turnID: string) => api.steerTurn(cardID, turnID),
    onMutate: (turnID) => setSteeringTurnID(turnID),
    onSuccess: sync,
    onSettled: () => setSteeringTurnID(""),
  });

  const retryAttempt = useMutation({
    mutationFn: (attemptID: string) => api.retryAttempt(cardID, attemptID),
    onMutate: (attemptID) => setRetryingAttemptID(attemptID),
    onSuccess: sync,
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
      {peek && !open && (
        <div
          className={`fixed right-4 z-40 transition-all ${
            isFocusedConversation
              ? "bottom-[calc(max(1.25rem,env(safe-area-inset-bottom))+9rem)]"
              : "bottom-[calc(max(1.25rem,env(safe-area-inset-bottom))+4.5rem)]"
          }`}
        >
          <button
            type="button"
            onClick={show}
            className="max-w-[min(20rem,calc(100vw-2rem))] rounded-2xl border border-line bg-surface px-3 py-2 text-left text-xs text-foreground shadow-lg"
          >
            <span className="line-clamp-2">{peek}</span>
          </button>
        </div>
      )}

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
              : "fixed right-4 bottom-[max(1.25rem,env(safe-area-inset-bottom))] h-[min(560px,70dvh)] w-[min(400px,calc(100vw-2rem))]"
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
            className="flex h-12 shrink-0 cursor-move items-center gap-2 border-b border-line px-3 select-none"
          >
            <img
              src="/cycledock-logo.png"
              alt=""
              className="size-6 pointer-events-none object-contain"
              draggable={false}
            />
            <h2 className="min-w-0 flex-1 truncate text-sm font-semibold pointer-events-none">
              {card?.title || t("主聊天")}
            </h2>
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
              className="grid size-8 place-items-center rounded-lg text-muted-foreground hover:bg-muted disabled:opacity-40"
            >
              <Expand className="size-4" />
            </button>
            <button
              type="button"
              title={t("关闭主聊天")}
              aria-label={t("关闭主聊天")}
              onClick={() => setOpen(false)}
              className="grid size-8 place-items-center rounded-lg text-muted-foreground hover:bg-muted"
            >
              <X className="size-4" />
            </button>
          </header>

          <div className="flex min-h-0 flex-1 flex-col select-auto">
            {!cardID || detail.isPending || openChat.isPending ? (
              <div className="grid flex-1 place-items-center">
                <Spinner />
              </div>
            ) : (
              <ConversationView
                events={detail.data?.timeline || []}
                live={stream.live}
                cardKind="discussion"
                pendingTurns={detail.data?.pending_turns || []}
                currentAttempt={current || null}
                attemptHistory={detail.data?.attempt_history || []}
                bottomSpacerClass="h-4 shrink-0"
                compact
                retryingAttemptID={retryingAttemptID}
                onRetryAttempt={(attemptID) => retryAttempt.mutate(attemptID)}
                onChoose={(choice) => choose.mutate(choice)}
                choicePending={choose.isPending}
                waitingAttemptID={waiting ? current?.id : undefined}
                onReply={setReplyTo}
              />
            )}
          </div>

          <div className="shrink-0 space-y-2 border-t border-line bg-canvas p-3 select-auto">
            <PendingTurnQueue
              turns={detail.data?.pending_turns || []}
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
                send.mutate(waiting ? "reply" : "enqueue");
              }}
              onCancel={() => cancelAttempt.mutate()}
            />
          </div>
        </section>
      )}

      {!open && (
        <div
          className={`fixed right-4 z-40 transition-all ${
            isFocusedConversation
              ? "bottom-[calc(max(1.25rem,env(safe-area-inset-bottom))+4.5rem)]"
              : "bottom-[max(1.25rem,env(safe-area-inset-bottom))]"
          }`}
        >
          <button
            ref={triggerRef}
            type="button"
            title={t("打开主聊天")}
            aria-label={t("打开主聊天")}
            onClick={show}
            className="relative grid size-14 place-items-center rounded-full border border-line bg-surface shadow-lg transition hover:bg-muted active:scale-95"
          >
            <img
              src="/cycledock-logo.png"
              alt=""
              className="size-8 object-contain"
              draggable={false}
            />
          </button>
        </div>
      )}
    </>
  );
}
