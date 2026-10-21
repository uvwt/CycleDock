import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, CircleAlert, Link2 } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { useCardStream } from "../../shared/realtime/useCardStream";
import { ErrorView, Sheet, Spinner } from "../../shared/ui/components";
import { ConversationView } from "../timeline/ConversationView";
import { CardDetails } from "./CardDetails";
import { CardStatusSwitch } from "./CardStatusSwitch";
import { ConversationComposer } from "./ConversationComposer";
import { WorkbenchLayout } from "../workbench/WorkbenchLayout";
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
  const projectCards = useQuery({
    queryKey: keys.cards(actualProjectID),
    queryFn: () => api.cards(actualProjectID),
    enabled: Boolean(card && actualProjectID),
  });

  const [relatedOpen, setRelatedOpen] = useState(false);
  const [taskInfoOpen, setTaskInfoOpen] = useState(false);
  const taskInfoRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!taskInfoOpen) return;
    function handlePointerDown(event: PointerEvent) {
      if (event.target instanceof Node && !taskInfoRef.current?.contains(event.target)) {
        setTaskInfoOpen(false);
      }
    }
    function handleEscape(event: KeyboardEvent) {
      if (event.key === "Escape") setTaskInfoOpen(false);
    }
    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleEscape);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleEscape);
    };
  }, [taskInfoOpen]);
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

  const isDiscussion = card?.kind === "discussion";
  const waiting = detail.data?.current_run?.status === "waiting_human";
  const current = detail.data?.current_run;
  const hasCurrent =
    Boolean(current) &&
    ["queued", "running", "waiting_human"].includes(current?.status || "");
  const blocked = Boolean(
    isDiscussion && card?.discussion_state === "archived",
  );
  const canGenerate = Boolean(isDiscussion && !blocked);

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
    onSuccess: () => {
      toast.success(t("生成任务指令已提交"));
      void sync();
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

  function submit() {
    if (!comment.trim() || blocked || send.isPending) return;
    send.mutate(waiting ? "reply" : submitMode);
  }

  function back() {
    navigate(isDiscussion ? "/chat?project=" + encodeURIComponent(actualProjectID) : "/board/" + actualProjectID);
  }

  function openDiscussion(item: Card) {
    navigate("/chat/" + item.project_id + "/" + item.id);
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

  if (projectCards.error) {
    return (
      <main className="h-dvh bg-white">
        <div className="mx-auto max-w-xl p-5 pt-12">
          <ErrorView
            error={projectCards.error}
            onRetry={() => void projectCards.refetch()}
          />
        </div>
      </main>
    );
  }

  const allCards = projectCards.data || [];
  const activeDiscussionID =
    card.kind === "discussion" ? card.id : card.source_discussion_id;
  const relatedCards = isDiscussion
    ? allCards.filter((item) => item.source_discussion_id === card.id)
    : [];

  return (
    <WorkbenchLayout
      mode={isDiscussion ? "discussion" : "work"}
      projectID={actualProjectID}
      cards={allCards}
      activeDiscussionID={activeDiscussionID}
      onSelectDiscussion={openDiscussion}
      onCreateDiscussion={() =>
        navigate("/chat?project=" + encodeURIComponent(actualProjectID) + "&newDiscussion=1")
      }
      fill
    >
      <main className="h-full min-h-0 bg-white">
        <section className="flex h-full min-h-0 min-w-0 flex-col bg-white">
          <header className="relative z-30 flex h-14 shrink-0 items-center justify-between gap-2 border-b border-slate-100 pr-3 pl-12 sm:pr-4">
            <div className="flex min-w-0 items-center gap-2">
              <button
                type="button"
                aria-label={t("返回看板")}
                title={t("返回看板")}
                onClick={back}
                className="grid size-9 shrink-0 place-items-center rounded-xl text-slate-500 hover:bg-slate-100"
              >
                <ArrowLeft className="size-5" />
              </button>
              <div className="flex min-w-0 items-center gap-2">
                <div className="min-w-0">
                  <h1 className="truncate text-[15px] font-semibold text-slate-900 sm:text-base">
                    {card.title || t("新讨论")}
                  </h1>
                  {isDiscussion && (
                    <p className="mt-0.5 truncate text-xs text-slate-400">
                      Discussion · {card.discussion_state}
                    </p>
                  )}
                </div>
                {!isDiscussion && <CardStatusSwitch card={card} />}
              </div>
            </div>

            <div className="flex shrink-0 items-center gap-1.5">
              {isDiscussion && relatedCards.length > 0 && (
                <button
                  type="button"
                  onClick={() => setRelatedOpen(true)}
                  className="inline-flex max-w-36 items-center gap-1.5 rounded-xl border border-slate-200 px-2.5 py-1.5 text-xs text-slate-600 hover:bg-slate-50 sm:max-w-none"
                >
                  <Link2 className="size-3.5 shrink-0" />
                  <span>{t("关联工作")} {relatedCards.length}</span>
                </button>
              )}
              <div ref={taskInfoRef} className="relative shrink-0">
                  <button
                    type="button"
                    title={isDiscussion ? "讨论信息" : t("任务信息")}
                    aria-label={isDiscussion ? "讨论信息" : t("任务信息")}
                    aria-expanded={taskInfoOpen}
                    aria-haspopup="dialog"
                    onClick={() => setTaskInfoOpen((open) => !open)}
                    className={`grid size-9 place-items-center rounded-xl text-slate-500 hover:bg-slate-100 ${taskInfoOpen ? "bg-slate-100 text-slate-800" : ""}`}
                  >
                    <CircleAlert className="size-[19px]" />
                  </button>
                  {taskInfoOpen && (
                    <div
                      role="dialog"
                      aria-label={isDiscussion ? "讨论信息" : t("任务信息")}
                      className={`absolute top-11 right-0 z-50 w-[min(20rem,calc(100vw-1.5rem))] rounded-xl border border-slate-200 bg-white p-4 shadow-md ${
                        isDiscussion
                          ? "overflow-visible"
                          : "max-h-[min(75dvh,680px)] overflow-y-auto"
                      }`}
                    >
                      <CardDetails
                        key={card.id}
                        detail={detail.data}
                        projectCards={allCards}
                        onDeleted={back}
                        compact
                      />
                    </div>
                  )}
                </div>
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
                    ? " · " + t("另有待处理") + " " + detail.data.queue.length
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
                canGenerate={canGenerate}
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
      </main>

      <Sheet open={relatedOpen} onOpenChange={setRelatedOpen} title={t("关联工作")}>
        <div className="space-y-2">
          {relatedCards.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => navigate("/cards/" + item.project_id + "/" + item.id)}
              className="flex w-full items-center justify-between gap-3 rounded-xl border border-slate-200 bg-white p-3 text-left text-sm hover:border-sky-300"
            >
              <span className="min-w-0 flex-1 truncate font-medium">{item.title}</span>
              <span className="shrink-0 text-xs text-slate-500">{item.todo_state || item.ongoing_state}</span>
            </button>
          ))}
        </div>
      </Sheet>

    </WorkbenchLayout>
  );
}
