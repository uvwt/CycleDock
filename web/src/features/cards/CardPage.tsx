import { useDeviceSelection } from "../devices/device-selection";
import { DeviceMenu } from "../devices/DeviceMenu";
import { TaskProgressCard } from "./TaskProgressCard";
import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { PanelLeft } from "lucide-react";
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
import { CardStatusSwitch } from "./CardStatusSwitch";
import { PendingTurnQueue } from "./PendingTurnQueue";
import { ConversationComposer } from "./ConversationComposer";
import { DiscussionSidebar } from "../chat/DiscussionSidebar";
import { PlanSidebar } from "../plans/PlanSidebar";
import { WorkSidebar } from "../board/WorkSidebar";
import type { Card } from "../../shared/api/types";
import { useProject } from "../../app/project-context";
import { useContextSidebarVisible } from "../../app/context-sidebar";
import { ProjectSwitcherWithSettings } from "../projects/ProjectSwitcherWithSettings";

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
  const { projects, projectID: activeProjectID, setProjectID } = useProject();

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
  const [comment, setComment] = useState("");
  const [replyTo, setReplyTo] = useState<QuotedMessage | null>(null);
  const [cancelingTurnID, setCancelingTurnID] = useState("");
  const [steeringTurnID, setSteeringTurnID] = useState("");
  const [retryingAttemptID, setRetryingAttemptID] = useState("");

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

  function switchProject(nextProjectID: string) {
    setProjectID(nextProjectID);
    setContextSidebarOpen(false);
    navigate(
      isDiscussion
        ? `/chat?project=${encodeURIComponent(nextProjectID)}`
        : `/${isOngoing ? "plans" : "board"}/${encodeURIComponent(nextProjectID)}`,
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
    <main className="h-dvh min-h-0 bg-surface">
      <div
        className={`grid h-full min-h-0 grid-cols-1 ${listVisible ? "lg:grid-cols-[260px_minmax(0,1fr)] xl:grid-cols-[280px_minmax(0,1fr)]" : ""}`}
      >
        <div
          className={`${listVisible ? "hidden lg:block" : "hidden"} min-h-0 border-r border-line`}
        >
          {contextSidebar}
        </div>

        <section
          className={`relative flex min-h-0 min-w-0 flex-col ${isDiscussion ? "bg-canvas" : "bg-surface"}`}
        >
          {/* 移除整块占位导航栏，操作按钮悬浮在右上角 */}
          <div className="absolute top-3 right-3 z-10 flex items-center gap-1.5 sm:right-4">
            <TaskProgressCard task={stream.live?.task} />
            <DeviceMenu />
            {!isDiscussion && <CardStatusSwitch card={card} />}
            <CardActions
              card={card}
              preserve={card.id === mainDiscussion.data?.id}
              taskDetails={{
                detail: detail.data,
                projectCards: projectCards.data || [],
              }}
              onDeleted={back}
            />
          </div>

          {/* 移动端呼出侧边栏悬浮在左上角 */}
          <div className="absolute top-3 left-3 z-10 flex items-center gap-2 lg:hidden">
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
              className="grid size-8 shrink-0 place-items-center rounded-lg border border-line bg-surface/90 text-muted-foreground shadow-sm backdrop-blur hover:bg-muted"
            >
              <PanelLeft className="size-4" />
            </button>
            <ProjectSwitcherWithSettings
              compact
              projects={projects}
              projectID={actualProjectID}
              onChange={switchProject}
              onCreate={() =>
                navigate(
                  `/board/${encodeURIComponent(actualProjectID)}?new=project`,
                )
              }
            />
          </div>

          <ConversationView
            key={cardID}
            events={detail.data.timeline}
            live={stream.live}
            cardKind={card.kind}
            pendingTurns={detail.data.pending_turns}
            currentAttempt={detail.data.current_attempt}
            attemptHistory={detail.data.attempt_history}
            waitingAttemptID={waiting ? current?.id : undefined}
            choicePending={choose.isPending}
            onChoose={(choice) => choose.mutate(choice)}
            retryingAttemptID={retryingAttemptID}
            onRetryAttempt={(attemptID) => retryAttempt.mutate(attemptID)}
            onReply={(quote) => setReplyTo(quote)}
          />

          <div
            className={`pointer-events-none absolute inset-x-0 bottom-0 z-10 flex flex-col justify-end bg-gradient-to-t ${
              isDiscussion
                ? "from-canvas via-canvas/90"
                : "from-surface via-surface/90"
            } to-transparent pt-10 pb-[max(0.75rem,env(safe-area-inset-bottom))]`}
          >
            <div className="pointer-events-auto mx-auto w-full max-w-3xl px-4 sm:px-6">
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
