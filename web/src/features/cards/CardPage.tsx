import { useDeviceSelection } from "../devices/device-selection";
import { DeviceMenu } from "../devices/DeviceMenu";
import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { Info, PanelLeft } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { useCardStream } from "../../shared/realtime/useCardStream";
import { ErrorView, Sheet, Spinner } from "../../shared/ui/components";
import {
  ConversationView,
  type QuotedMessage,
} from "../timeline/ConversationView";
import { CardActions } from "./CardActions";
import { CardHeaderInfoCard } from "./CardHeaderInfoCard";
import { CardStatusSwitch } from "./CardStatusSwitch";
import { PendingTurnQueue } from "./PendingTurnQueue";
import { ConversationComposer } from "./ConversationComposer";
import { DiscussionSidebar } from "../chat/DiscussionSidebar";
import { PlanSidebar } from "../plans/PlanSidebar";
import { WorkSidebar } from "../board/WorkSidebar";
import type { Card } from "../../shared/api/types";
import { useProject } from "../../app/project-context";
import { useContextSidebarVisible } from "../../app/context-sidebar";
import { MobileGlobalNavMenu } from "../../shared/ui/MobileGlobalNavMenu";
import { MobileQuickCardSwitcher } from "../../shared/ui/MobileQuickCardSwitcher";

export default function CardPage() {
  const { cardID = "" } = useParams();
  return <CardPageContent key={cardID} />;
}

function CardPageContent() {
  const { target } = useDeviceSelection();
  const { t } = useTranslation();
  const { projectID = "", cardID = "" } = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const listVisible = useContextSidebarVisible();
  const { projectID: activeProjectID, setProjectID } = useProject();

  const detail = useQuery({
    queryKey: keys.card(cardID),
    queryFn: () => api.card(cardID),
    enabled: Boolean(cardID),
  });
  const card = detail.data?.card;
  const actualProjectID = card?.project_id || projectID;
  const stream = useCardStream(cardID, actualProjectID);

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

  const isDiscussion = card?.kind === "discussion";
  const mainDiscussion = useQuery({
    queryKey: keys.mainDiscussion(actualProjectID),
    queryFn: () => api.mainDiscussion(actualProjectID),
    enabled: Boolean(actualProjectID) && isDiscussion,
  });
  const isOngoing = card?.kind === "ongoing";
  const blocked = Boolean(
    isDiscussion && card?.discussion_state === "archived",
  );
  const current = detail.data?.current_attempt;
  const waiting = current?.status === "waiting_human";
  const hasCurrent =
    Boolean(current) &&
    ["queued", "running", "waiting_human"].includes(current?.status || "");

  const [contextSidebarOpen, setContextSidebarOpen] = useState(false);
  const [infoOpen, setInfoOpen] = useState(false);
  const [comment, setComment] = useState("");
  const [replyTo, setReplyTo] = useState<QuotedMessage | null>(null);
  const [cancelingTurnID, setCancelingTurnID] = useState("");
  const [steeringTurnID, setSteeringTurnID] = useState("");
  const [retryingAttemptID, setRetryingAttemptID] = useState("");
  const composerDockRef = useRef<HTMLDivElement>(null);
  const [composerDockHeight, setComposerDockHeight] = useState(96);

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
    blocked,
    replyTo?.id,
    detail.data?.pending_turns?.length,
  ]);

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
    if (location.pathname !== path) navigate(path, { replace: true });
  }, [card, navigate, location.pathname]);

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
      return api.send(cardID, content, mode, undefined, target);
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
      toast.success(t("已通过审核"));
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("状态更新失败")),
  });

  function submit() {
    if (!comment.trim() || blocked || send.isPending) return;
    send.mutate(waiting ? "reply" : "enqueue");
  }

  function back() {
    navigate(
      isDiscussion
        ? "/chat?project=" + encodeURIComponent(actualProjectID)
        : (isOngoing ? "/plans/" : "/board/") + actualProjectID,
    );
  }

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

  if (detail.isPending) {
    return (
      <main className="grid h-dvh place-items-center bg-surface">
        <Spinner text={t("正在加载会话…")} />
      </main>
    );
  }

  if (detail.error || !detail.data || !card) {
    return (
      <main className="h-dvh bg-surface">
        <div className="mx-auto max-w-xl p-5 pt-12">
          <ErrorView
            error={detail.error}
            onRetry={() => void detail.refetch()}
          />
        </div>
      </main>
    );
  }

  const activeAttemptStatus = stream.live
    ? stream.live.finished
      ? "completed"
      : "running"
    : detail.data?.current_attempt?.status;
  const liveTask = stream.live?.task;
  const liveTaskTotal = liveTask?.step_count || liveTask?.steps?.length || 0;
  const liveTaskCompleted = Math.min(
    liveTask?.completed_step_count || 0,
    liveTaskTotal || liveTask?.completed_step_count || 0,
  );

  const discussionSidebar =
    isDiscussion && actualProjectID ? (
      <DiscussionSidebar
        discussions={discussions}
        activeCardID={cardID}
        activeAttemptStatus={activeAttemptStatus}
        onSelect={openDiscussion}
        onCreate={startDiscussion}
      />
    ) : null;

  const workSidebar =
    !isDiscussion && !isOngoing && actualProjectID ? (
      <WorkSidebar
        cards={projectCards.data || []}
        activeCardID={cardID}
        activeAttemptStatus={activeAttemptStatus}
        onSelect={openWork}
      />
    ) : null;

  const contextSidebar = isDiscussion ? (
    discussionSidebar
  ) : isOngoing ? (
    <PlanSidebar
      cards={projectCards.data || []}
      activeCardID={cardID}
      onSelect={openWork}
    />
  ) : (
    workSidebar
  );

  return (
    <main
      className={`h-dvh min-h-0 ${isDiscussion ? "bg-canvas" : "bg-surface"}`}
    >
      <div
        className={`flex h-full min-h-0 w-full overflow-hidden`}
      >
        <div
          className={`${listVisible ? "hidden lg:block" : "hidden"} h-full min-h-0 w-[260px] xl:w-[280px] shrink-0 border-r border-line`}
        >
          {contextSidebar}
        </div>

        <section
          className={`relative flex flex-1 min-h-0 min-w-0 flex-col ${isDiscussion ? "bg-canvas" : "bg-surface"}`}
        >
          <header
            aria-hidden="true"
            data-mobile-top-surface
            className="pointer-events-none fixed inset-x-0 top-0 z-10 h-14 bg-transparent md:hidden"
          />
          {/* 移除整块占位导航栏，操作按钮悬浮在右上角 */}
          <div className="absolute top-3 right-3 z-20 flex max-w-[calc(100vw-1.5rem)] flex-col items-end gap-2 sm:right-4">
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
              <CardActions
                card={card}
                variant="toolbar"
                preserve={card.id === mainDiscussion.data?.id}
                taskDetails={{
                  detail: detail.data,
                  projectCards: projectCards.data || [],
                }}
                onDeleted={back}
              />
            </div>

            {/* 手机端窄屏：展开任务详情浮层，配备防止误触穿透遮罩与关闭控制 */}
            {infoOpen && (
              <div className="lg:hidden">
                <div
                  className="fixed inset-0 z-30 bg-background/40 backdrop-blur-[1px]"
                  aria-hidden="true"
                  onClick={() => setInfoOpen(false)}
                />
                <div className="absolute right-0 top-full mt-2 z-40">
                  <CardHeaderInfoCard
                    card={card}
                    detail={detail.data}
                    projectCards={projectCards.data || []}
                    task={stream.live?.task}
                    onSync={sync}
                    onClose={() => setInfoOpen(false)}
                  />
                </div>
              </div>
            )}
          </div>

          {/* 移动端呼出侧边栏悬浮在左上角 */}
          <div className="absolute top-3 left-3 z-20 flex items-center gap-1.5 sm:left-4 lg:hidden">
            <MobileGlobalNavMenu />
            <MobileQuickCardSwitcher
              projectID={card.project_id}
              activeCardID={card.id}
              activeAttemptStatus={current?.status}
            />
            <button
              type="button"
              title={
                isDiscussion
                  ? t("打开讨论列表")
                  : isOngoing
                    ? t("打开计划列表")
                    : t("打开工作列表")
              }
              aria-label={
                isDiscussion
                  ? t("打开讨论列表")
                  : isOngoing
                    ? t("打开计划列表")
                    : t("打开工作列表")
              }
              onClick={() => setContextSidebarOpen(true)}
              className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg border border-line bg-surface text-muted-foreground shadow-xs transition hover:bg-muted hover:text-foreground"
            >
              <PanelLeft className="size-4" />
            </button>
          </div>

          <div className="flex flex-1 min-h-0 min-w-0">
            <div className="relative flex flex-1 min-h-0 min-w-0 flex-col">
              <ConversationView
                key={cardID}
                card={card}
                events={detail.data.timeline}
                live={stream.live}
                cardKind={card.kind}
                pendingTurns={detail.data.pending_turns}
                currentAttempt={detail.data.current_attempt}
                attemptHistory={detail.data.attempt_history}
                bottomInsetPx={composerDockHeight}
                waitingAttemptID={waiting ? current?.id : undefined}
                choicePending={choose.isPending}
                onChoose={(choice) => choose.mutate(choice)}
                retryingAttemptID={retryingAttemptID}
                onRetryAttempt={(attemptID) => retryAttempt.mutate(attemptID)}
                canApproveReview={
                  card.kind === "todo" && card.todo_state === "review"
                }
                approvingReview={approveReview.isPending}
                onApproveReview={() => approveReview.mutate()}
                onReply={(quote) => setReplyTo(quote)}
              />

              <div
                data-composer-dock
                className={`pointer-events-none absolute inset-x-0 bottom-0 z-30 flex flex-col justify-end bg-gradient-to-t ${
                  isDiscussion
                    ? "from-canvas via-canvas/95"
                    : "from-surface via-surface/95"
                } to-transparent pt-8`}
              >
                <div
                  ref={composerDockRef}
                  className="pointer-events-auto mx-auto w-full max-w-3xl px-3 pt-2 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:px-6 sm:pb-3"
                >
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
                    comment={comment}
                    setComment={setComment}
                    replyTo={replyTo}
                    onCancelReply={() => setReplyTo(null)}
                    blocked={blocked}
                    waiting={waiting}
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
            {infoOpen && (
              <div className="hidden lg:block shrink-0 pt-14 pr-4 pb-4">
                <CardHeaderInfoCard
                  card={card}
                  detail={detail.data}
                  projectCards={projectCards.data || []}
                  task={stream.live?.task}
                  onSync={sync}
                />
              </div>
            )}
          </div>
        </section>
      </div>

      {contextSidebar && (
        <Sheet
          open={contextSidebarOpen}
          onOpenChange={setContextSidebarOpen}
          side="left"
          title={
            isDiscussion
              ? t("讨论列表")
              : isOngoing
                ? t("计划列表")
                : t("工作列表")
          }
        >
          <div className="-m-5 h-[calc(100dvh-4.25rem)]">{contextSidebar}</div>
        </Sheet>
      )}
    </main>
  );
}
