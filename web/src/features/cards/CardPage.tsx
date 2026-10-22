import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { PanelLeft } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { useCardStream } from "../../shared/realtime/useCardStream";
import { ErrorView, Sheet, Spinner } from "../../shared/ui/components";
import { ConversationView } from "../timeline/ConversationView";
import { CardHeaderActions } from "./CardHeaderActions";
import { CardStatusSwitch } from "./CardStatusSwitch";
import { ConversationComposer } from "./ConversationComposer";
import { DiscussionSidebar } from "../chat/DiscussionSidebar";
import { WorkSidebar } from "../board/WorkSidebar";
import type { Card } from "../../shared/api/types";

export default function CardPage() {
  const { cardID = "" } = useParams();
  return <CardPageContent key={cardID} />;
}

function CardPageContent() {
  const { t } = useTranslation();
  const { projectID = "", cardID = "" } = useParams();
  const navigate = useNavigate();

  const detail = useQuery({
    queryKey: keys.card(cardID),
    queryFn: () => api.card(cardID),
    enabled: Boolean(cardID),
  });
  const card = detail.data?.card;
  const actualProjectID = card?.project_id || projectID;
  const stream = useCardStream(cardID, actualProjectID);

  const projects = useQuery({
    queryKey: keys.projects,
    queryFn: api.projects,
  });
  const projectCards = useQuery({
    queryKey: keys.cards(actualProjectID),
    queryFn: () => api.cards(actualProjectID),
    enabled: Boolean(actualProjectID),
  });
  const discussions = useMemo(
    () =>
      ((projectCards.data || []).filter(
        (item) => item.kind === "discussion",
      ) as Card[]),
    [projectCards.data],
  );

  const isDiscussion = card?.kind === "discussion";
  const blocked = Boolean(
    isDiscussion && card?.discussion_state === "archived",
  );
  const current = detail.data?.current_run;
  const waiting = current?.status === "waiting_human";
  const hasCurrent =
    Boolean(current) &&
    ["queued", "running", "waiting_human"].includes(current?.status || "");

  const [contextSidebarOpen, setContextSidebarOpen] = useState(false);
  const [comment, setComment] = useState("");
  const [composerExpanded, setComposerExpanded] = useState(false);
  const [submitMode, setSubmitMode] = useState<"enqueue" | "interrupt">(
    "enqueue",
  );
  const [cancelingTriggerID, setCancelingTriggerID] = useState("");
  const [retryingTriggerID, setRetryingTriggerID] = useState("");

  useEffect(() => {
    if (!card || !projectID || projectID === card.project_id) return;
    const path =
      (card.kind === "discussion" ? "/chat/" : "/cards/") +
      card.project_id +
      "/" +
      card.id;
    navigate(path, { replace: true });
  }, [card, navigate, projectID]);

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
    mutationFn: (mode: "enqueue" | "interrupt" | "reply") =>
      mode === "reply"
        ? api.reply(cardID, comment.trim())
        : api.send(cardID, comment.trim(), mode),
    onSuccess: async () => {
      setComment("");
      setSubmitMode("enqueue");
      setComposerExpanded(false);
      await sync();
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("发送失败")),
  });

  const generate = useMutation({
    mutationFn: () =>
      api.send(
        cardID,
        "请根据当前 Discussion 的上下文生成具体工作。明确结束条件的创建 Todo；长期维护或周期执行的创建 Ongoing。使用 CycleDock 工作创建工具，记录来源 Discussion；不要转换当前 Discussion。",
        "enqueue",
      ),
    onSuccess: async () => {
      toast.success(t("生成任务指令已提交"));
      await sync();
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("提交失败")),
  });

  const cancelRun = useMutation({
    mutationFn: () => api.cancelRun(cardID, current!.id),
    onSuccess: sync,
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("取消失败")),
  });

  const cancelTrigger = useMutation({
    mutationFn: (triggerID: string) => api.cancelTrigger(cardID, triggerID),
    onMutate: (triggerID) => setCancelingTriggerID(triggerID),
    onSuccess: sync,
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("取消排队失败")),
    onSettled: () => setCancelingTriggerID(""),
  });

  const retryTrigger = useMutation({
    mutationFn: (triggerID: string) => api.retry(cardID, triggerID),
    onMutate: (triggerID) => setRetryingTriggerID(triggerID),
    onSuccess: sync,
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("重试失败")),
    onSettled: () => setRetryingTriggerID(""),
  });

  const createDiscussion = useMutation({
    mutationFn: () => api.createDiscussion(actualProjectID),
    onSuccess: async (created) => {
      await queryClient.invalidateQueries({
        queryKey: keys.cards(actualProjectID),
      });
      setContextSidebarOpen(false);
      navigate(`/chat/${actualProjectID}/${created.id}`);
    },
    onError: (error) =>
      toast.error(
        error instanceof Error ? error.message : t("创建讨论失败"),
      ),
  });

  function submit() {
    if (!comment.trim() || blocked || send.isPending) return;
    send.mutate(waiting ? "reply" : submitMode);
  }

  function back() {
    navigate(
      isDiscussion
        ? "/chat?project=" + encodeURIComponent(actualProjectID)
        : "/board/" + actualProjectID,
    );
  }

  function openDiscussion(item: Card) {
    setContextSidebarOpen(false);
    navigate(`/chat/${item.project_id}/${item.id}`);
  }

  function openWork(item: Card) {
    setContextSidebarOpen(false);
    navigate(`/cards/${item.project_id}/${item.id}`);
  }

  if (detail.isPending) {
    return (
      <main className="grid h-dvh place-items-center bg-white">
        <Spinner text={t("正在加载会话…")} />
      </main>
    );
  }

  if (detail.error || !detail.data || !card) {
    return (
      <main className="h-dvh bg-white">
        <div className="mx-auto max-w-xl p-5 pt-12">
          <ErrorView
            error={detail.error}
            onRetry={() => void detail.refetch()}
          />
        </div>
      </main>
    );
  }

  const discussionSidebar =
    isDiscussion && actualProjectID ? (
      <DiscussionSidebar
        projects={projects.data || []}
        projectID={actualProjectID}
        discussions={discussions}
        activeCardID={cardID}
        creating={createDiscussion.isPending}
        onProjectChange={(nextProjectID) =>
          navigate(`/chat?project=${encodeURIComponent(nextProjectID)}`)
        }
        onSelect={openDiscussion}
        onCreate={() => createDiscussion.mutate()}
      />
    ) : null;

  const workSidebar =
    !isDiscussion && actualProjectID ? (
      <WorkSidebar
        projects={projects.data || []}
        projectID={actualProjectID}
        cards={projectCards.data || []}
        activeCardID={cardID}
        onProjectChange={(nextProjectID) => navigate(`/board/${nextProjectID}`)}
        onSelect={openWork}
      />
    ) : null;

  const contextSidebar = isDiscussion ? discussionSidebar : workSidebar;

  return (
    <main className="h-dvh min-h-0 bg-white">
      <div
        className="grid h-full min-h-0 grid-cols-1 lg:grid-cols-[260px_minmax(0,1fr)] xl:grid-cols-[280px_minmax(0,1fr)]"
      >
        <div className="hidden min-h-0 border-r border-slate-200 lg:block">
          {contextSidebar}
        </div>

        <section className="flex min-h-0 min-w-0 flex-col bg-white">
          <header className="flex h-14 shrink-0 items-center gap-3 border-b border-slate-100 px-3 sm:px-4">
            <div className="flex min-w-0 flex-1 items-center gap-2">
              <button
                type="button"
                title={isDiscussion ? t("打开讨论列表") : t("打开工作列表")}
                aria-label={isDiscussion ? t("打开讨论列表") : t("打开工作列表")}
                onClick={() => setContextSidebarOpen(true)}
                className="grid size-9 shrink-0 place-items-center rounded-xl text-slate-500 hover:bg-slate-100 lg:hidden"
              >
                <PanelLeft className="size-5" />
              </button>
              <div className="flex min-w-0 items-center gap-2">
                <div className="min-w-0">
                  <h1 className="truncate text-sm font-semibold text-slate-900 sm:text-base">
                    {card.title || t("新讨论")}
                  </h1>
                  {isDiscussion && (
                    <p className="mt-0.5 truncate text-[11px] text-slate-400">
                      Discussion · {card.discussion_state}
                    </p>
                  )}
                </div>
                {!isDiscussion && <CardStatusSwitch card={card} />}
              </div>
            </div>
            <div className="shrink-0">
              <CardHeaderActions card={card} onDeleted={back} />
            </div>
          </header>

          <ConversationView
            events={detail.data.timeline}
            live={stream.live}
            cardKind={card.kind}
            queue={detail.data.queue}
            currentRun={detail.data.current_run}
            workHistory={detail.data.work_history}
            cancelingTriggerID={cancelingTriggerID}
            retryingTriggerID={retryingTriggerID}
            onCancelTrigger={(triggerID) => cancelTrigger.mutate(triggerID)}
            onRetryTrigger={(triggerID) => retryTrigger.mutate(triggerID)}
          />

          <div className="shrink-0 bg-white px-3 pt-2 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:px-5 sm:pt-3">
            <div className="mx-auto max-w-3xl">
              {blocked && (
                <p className="mb-2 text-center text-xs text-slate-400">
                  {t("此讨论已归档，不能继续发送消息")}
                </p>
              )}
              {(hasCurrent || detail.data.queue.length > 0) && (
                <p className="mb-1.5 text-center text-[11px] text-slate-400">
                  {hasCurrent
                    ? t("Agent 正在处理当前工作")
                    : t("Agent 当前空闲")}
                  {detail.data.queue.length > 0
                    ? " · " +
                      t("另有待处理") +
                      " " +
                      detail.data.queue.length
                    : ""}
                </p>
              )}
              <ConversationComposer
                comment={comment}
                setComment={setComment}
                expanded={composerExpanded}
                setExpanded={setComposerExpanded}
                blocked={blocked}
                waiting={waiting}
                hasCurrent={hasCurrent}
                submitMode={submitMode}
                setSubmitMode={setSubmitMode}
                sendPending={send.isPending}
                canGenerate={Boolean(isDiscussion && !blocked)}
                generatePending={generate.isPending}
                streamConnected={stream.connected}
                streamError={stream.lastError}
                cancelPending={cancelRun.isPending}
                onSubmit={submit}
                onGenerate={() => generate.mutate()}
                onCancel={() => cancelRun.mutate()}
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
          title={isDiscussion ? t("讨论列表") : t("工作列表")}
        >
          <div className="-m-5 h-[calc(100dvh-4.25rem)]">
            {contextSidebar}
          </div>
        </Sheet>
      )}

    </main>
  );
}
