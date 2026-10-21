import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, PanelLeft, PanelRight } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { useCardStream } from "../../shared/realtime/useCardStream";
import { ErrorView, Sheet, Spinner } from "../../shared/ui/components";
import { ConversationView } from "../timeline/ConversationView";
import { CardDetails } from "./CardDetails";
import { ConversationComposer } from "./ConversationComposer";
import { DiscussionSidebar } from "../chat/DiscussionSidebar";
import type { Card, CardDetail } from "../../shared/api/types";

function useElapsed(startedAt?: string, active = false) {
  const [now, setNow] = useState(0);
  useEffect(() => {
    if (!active) return;
    const handle = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(handle);
  }, [active]);
  if (!startedAt) return null;
  if (!now) return 0;
  return Math.max(
    0,
    Math.floor((now - new Date(startedAt).getTime()) / 1000),
  );
}

function prettyTime(seconds: number | null) {
  return seconds === null
    ? "—"
    : `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

export default function CardPage() {
  const { t } = useTranslation();
  const { projectID = "", cardID = "" } = useParams();
  const navigate = useNavigate();
  const detail = useQuery({
    queryKey: keys.card(cardID),
    queryFn: () => api.card(cardID),
    enabled: !!cardID,
  });
  const projects = useQuery({
    queryKey: keys.projects,
    queryFn: api.projects,
  });
  const stream = useCardStream(cardID, projectID);
  const card = detail.data?.card;
  const isDiscussion = card?.kind === "discussion";
  const projectCards = useQuery({
    queryKey: keys.cards(projectID),
    queryFn: () => api.cards(projectID),
    enabled: !!projectID && !!isDiscussion,
  });
  const discussions = useMemo(
    () =>
      ((projectCards.data || []).filter(
        (item) => item.kind === "discussion",
      ) as Card[]),
    [projectCards.data],
  );
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [discussionsOpen, setDiscussionsOpen] = useState(false);
  const [comment, setComment] = useState("");
  const [composerExpanded, setComposerExpanded] = useState(false);
  const [submitMode, setSubmitMode] = useState<"enqueue" | "interrupt">(
    "enqueue",
  );
  const waiting = detail.data?.current_run?.status === "waiting_human";
  const current = detail.data?.current_run;
  const hasCurrent =
    !!current &&
    ["queued", "running", "waiting_human"].includes(current.status);
  const elapsed = useElapsed(
    current?.started_at,
    current?.status === "running",
  );
  const blocked = !!(
    isDiscussion && card?.discussion_state === "archived"
  );
  const canGenerate = !!isDiscussion && !blocked;

  const sync = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.card(cardID) }),
      queryClient.invalidateQueries({ queryKey: keys.cards(projectID) }),
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
    onError: (error) => toast.error(String(error)),
  });
  const cancel = useMutation({
    mutationFn: () => api.cancelRun(cardID, current!.id),
    onSuccess: sync,
    onError: (error) => toast.error(String(error)),
  });
  const createDiscussion = useMutation({
    mutationFn: () => api.createDiscussion(projectID),
    onSuccess: async (created) => {
      await queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
      setDiscussionsOpen(false);
      navigate(`/chat/${projectID}/${created.id}`);
    },
    onError: (error) => toast.error(String(error)),
  });

  function submit() {
    if (!comment.trim() || blocked || send.isPending) return;
    send.mutate(waiting ? "reply" : submitMode);
  }

  function back() {
    navigate(isDiscussion ? "/chat" : "/board/" + projectID);
  }

  function openDiscussion(item: Card) {
    setDiscussionsOpen(false);
    navigate(`/chat/${item.project_id}/${item.id}`);
  }

  const discussionSidebar =
    isDiscussion && projectID ? (
      <DiscussionSidebar
        projects={projects.data || []}
        projectID={projectID}
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

  return (
    <main className="h-dvh min-h-0 bg-white">
      {detail.isPending ? (
        <div className="grid h-full place-items-center">
          <Spinner text={t("正在加载会话…")} />
        </div>
      ) : detail.error || !detail.data ? (
        <div className="mx-auto max-w-xl p-5 pt-12">
          <ErrorView
            error={detail.error}
            onRetry={() => void detail.refetch()}
          />
        </div>
      ) : (
        <div
          className={`grid h-full min-h-0 ${isDiscussion ? "lg:grid-cols-[260px_minmax(0,1fr)_320px] xl:grid-cols-[280px_minmax(0,1fr)_360px]" : "lg:grid-cols-[minmax(0,1fr)_320px] xl:grid-cols-[minmax(0,1fr)_360px]"}`}
        >
          {isDiscussion && (
            <div className="hidden min-h-0 border-r border-slate-200 lg:block">
              {discussionSidebar}
            </div>
          )}

          <section className="flex min-h-0 min-w-0 flex-col bg-white">
            <header className="flex h-14 shrink-0 items-center justify-between gap-3 border-b border-slate-100 px-3 sm:px-4">
              <div className="flex min-w-0 items-center gap-2">
                {isDiscussion ? (
                  <button
                    type="button"
                    title={t("打开讨论列表")}
                    aria-label={t("打开讨论列表")}
                    onClick={() => setDiscussionsOpen(true)}
                    className="grid size-9 shrink-0 place-items-center rounded-xl text-slate-500 hover:bg-slate-100 lg:hidden"
                  >
                    <PanelLeft className="size-5" />
                  </button>
                ) : (
                  <button
                    type="button"
                    aria-label={t("返回")}
                    title={t("返回")}
                    onClick={back}
                    className="grid size-9 shrink-0 place-items-center rounded-xl text-slate-500 hover:bg-slate-100"
                  >
                    <ArrowLeft className="size-5" />
                  </button>
                )}
                <div className="min-w-0">
                  <h1 className="truncate text-sm font-semibold text-slate-900 sm:text-base">
                    {card?.title || t("新讨论")}
                  </h1>
                  <p className="mt-0.5 truncate text-[11px] text-slate-400">
                    {isDiscussion
                      ? "Discussion"
                      : card?.kind === "todo"
                        ? "Todo"
                        : "Ongoing"}
                    {" · "}
                    {isDiscussion
                      ? card?.discussion_state
                      : card?.todo_state || card?.ongoing_state}
                  </p>
                </div>
              </div>

              <div className="flex shrink-0 items-center gap-1.5">
                {current && (
                  <span
                    role="status"
                    className="inline-flex max-w-28 items-center gap-1.5 truncate rounded-full bg-slate-50 px-2 py-1.5 text-[11px] text-slate-500 sm:max-w-none sm:px-3 sm:text-xs"
                  >
                    {current.status === "running" && (
                      <span className="live-dot size-1.5 rounded-full bg-sky-500" />
                    )}
                    {current.status === "queued"
                      ? t("排队中")
                      : current.status === "waiting_human"
                        ? t("等待回复")
                        : current.status === "running"
                          ? t("运行中")
                          : current.status}
                    {current.status === "running" && (
                      <span className="hidden sm:inline">
                        {" · "}
                        {prettyTime(elapsed)}
                      </span>
                    )}
                  </span>
                )}
                <button
                  type="button"
                  className="grid size-9 place-items-center rounded-xl text-slate-500 hover:bg-slate-100 lg:hidden"
                  title={t("任务详情")}
                  aria-label={t("任务详情")}
                  onClick={() => setDetailsOpen(true)}
                >
                  <PanelRight className="size-5" />
                </button>
              </div>
            </header>

            <ConversationView
              events={detail.data.timeline}
              live={stream.live}
              cardKind={detail.data.card.kind}
            />

            <div className="shrink-0 bg-white px-3 pt-2 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:px-5 sm:pt-3">
              <div className="mx-auto max-w-3xl">
                {blocked && (
                  <p className="mb-2 text-center text-xs text-slate-400">
                    {t("此讨论已归档，不能继续发送消息")}
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
                  cancelPending={cancel.isPending}
                  onSubmit={submit}
                  onGenerate={() => generate.mutate()}
                  onCancel={() => cancel.mutate()}
                />
              </div>
            </div>
          </section>

          <aside
            className="scrollbar-thin hidden min-h-0 overflow-y-auto border-l border-slate-200 bg-slate-50/70 p-3 lg:block"
            aria-label={t("任务详情")}
          >
            <CardDetails
              key={detail.data.card.id}
              detail={detail.data as CardDetail}
              onDeleted={back}
            />
          </aside>
        </div>
      )}

      {isDiscussion && (
        <Sheet
          open={discussionsOpen}
          onOpenChange={setDiscussionsOpen}
          side="left"
          title={t("讨论列表")}
        >
          <div className="-m-5 h-[calc(100dvh-4.25rem)]">
            {discussionSidebar}
          </div>
        </Sheet>
      )}

      <Sheet
        open={detailsOpen}
        onOpenChange={setDetailsOpen}
        title={t("任务详情")}
      >
        {detail.data ? (
          <CardDetails
            key={detail.data.card.id}
            detail={detail.data as CardDetail}
            onDeleted={back}
          />
        ) : (
          <Spinner />
        )}
      </Sheet>
    </main>
  );
}
