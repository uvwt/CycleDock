import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { ArrowUp, MessageSquareText, PanelLeft } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { ErrorView, Sheet, Spinner } from "../../shared/ui/components";
import type { Card } from "../../shared/api/types";
import { DiscussionSidebar } from "./DiscussionSidebar";

export default function ChatHome() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  const [selectedProject, setSelectedProject] = useState("");
  const [mobileListOpen, setMobileListOpen] = useState(false);
  const [message, setMessage] = useState("");
  const [composerExpanded, setComposerExpanded] = useState(false);
  const requestedProject = selectedProject || searchParams.get("project") || "";
  const projectID = projects.data?.some(
    (project) => project.id === requestedProject,
  )
    ? requestedProject
    : projects.data?.[0]?.id || "";
  const cards = useQuery({
    queryKey: keys.cards(projectID),
    queryFn: () => api.cards(projectID),
    enabled: !!projectID,
  });
  const discussions = useMemo(
    () =>
      ((cards.data || []).filter(
        (card) => card.kind === "discussion",
      ) as Card[]),
    [cards.data],
  );

  const create = useMutation({
    mutationFn: async (firstMessage?: string) => {
      const card = await api.createDiscussion(projectID);
      const content = firstMessage?.trim();
      let sendError: unknown;
      if (content) {
        try {
          await api.send(card.id, content, "enqueue");
        } catch (error) {
          sendError = error;
        }
      }
      return { card, sendError };
    },
    onSuccess: async ({ card, sendError }) => {
      setMessage("");
      setComposerExpanded(false);
      setMobileListOpen(false);
      await queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
      if (sendError) {
        toast.error(
          sendError instanceof Error ? sendError.message : t("发送失败"),
        );
      }
      navigate(`/chat/${projectID}/${card.id}`);
    },
    onError: (error) =>
      toast.error(
        error instanceof Error ? error.message : t("创建讨论失败"),
      ),
  });

  function selectProject(nextProjectID: string) {
    setSelectedProject(nextProjectID);
    setSearchParams({ project: nextProjectID }, { replace: true });
    setMobileListOpen(false);
  }

  function selectDiscussion(card: Card) {
    setMobileListOpen(false);
    navigate(`/chat/${card.project_id}/${card.id}`);
  }

  function startFromMessage() {
    if (!message.trim() || !projectID || create.isPending) return;
    create.mutate(message);
  }

  if (projects.isPending) {
    return (
      <main className="grid h-[calc(100dvh-4rem)] place-items-center md:h-dvh">
        <Spinner text={t("读取项目…")} />
      </main>
    );
  }
  if (projects.error) {
    return (
      <main className="mx-auto max-w-xl p-6">
        <ErrorView
          error={projects.error}
          onRetry={() => void projects.refetch()}
        />
      </main>
    );
  }

  return (
    <main className="h-[calc(100dvh-4rem)] min-h-0 bg-white md:h-dvh">
      {projectID ? (
        <div className="grid h-full min-h-0 md:grid-cols-[280px_minmax(0,1fr)]">
          <div className="hidden min-h-0 border-r border-slate-200 md:block">
            <DiscussionSidebar
              projects={projects.data || []}
              projectID={projectID}
              discussions={discussions}
              creating={create.isPending}
              onProjectChange={selectProject}
              onSelect={selectDiscussion}
              onCreate={() => create.mutate(undefined)}
            />
          </div>

          <section className="relative flex min-h-0 min-w-0 flex-col">
            <button
              type="button"
              title={t("打开讨论列表")}
              aria-label={t("打开讨论列表")}
              onClick={() => setMobileListOpen(true)}
              className="absolute top-3 left-3 z-10 grid size-9 place-items-center rounded-xl border border-slate-200 bg-white/90 text-slate-500 shadow-sm backdrop-blur hover:bg-slate-100 md:hidden"
            >
              <PanelLeft className="size-5" />
            </button>
            <div className="flex min-h-0 flex-1 flex-col items-center justify-center px-5 pb-6">
              <div className="w-full max-w-3xl">
                <div className="mb-10 text-center">
                  <span className="mx-auto grid size-12 place-items-center rounded-2xl bg-slate-100 text-slate-500">
                    <MessageSquareText className="size-6" />
                  </span>
                  <h1 className="mt-5 text-2xl font-semibold tracking-tight text-slate-900 sm:text-3xl">
                    {t("今天想推进什么？")}
                  </h1>
                  <p className="mt-2 text-sm text-slate-500">
                    {t("直接输入第一句话，CycleDock 会创建 Discussion 并开始协作。")}
                  </p>
                </div>

                <form
                  onSubmit={(event) => {
                    event.preventDefault();
                    startFromMessage();
                  }}
                  className={`mx-auto border bg-white transition-all focus-within:border-sky-300 ${composerExpanded ? "rounded-2xl border-slate-200 p-2 shadow-sm" : "flex items-center rounded-full border-slate-200 py-1 pr-1 pl-4 shadow-sm"}`}
                >
                  {composerExpanded ? (
                    <textarea
                      autoFocus
                      aria-label={t("发送消息，开始讨论…")}
                      value={message}
                      onChange={(event) => setMessage(event.target.value)}
                      onBlur={() => {
                        if (!message.trim()) setComposerExpanded(false);
                      }}
                      placeholder={t("发送消息，开始讨论…")}
                      rows={3}
                      className="max-h-44 min-h-20 w-full resize-none bg-transparent px-2 py-2 text-sm outline-none"
                      onKeyDown={(event) => {
                        if (
                          event.key === "Enter" &&
                          !event.shiftKey &&
                          !event.nativeEvent.isComposing
                        ) {
                          event.preventDefault();
                          startFromMessage();
                        }
                      }}
                    />
                  ) : (
                    <input
                      aria-label={t("发送消息，开始讨论…")}
                      value={message}
                      onChange={(event) => setMessage(event.target.value)}
                      onFocus={() => setComposerExpanded(true)}
                      placeholder={t("发送消息，开始讨论…")}
                      className="min-w-0 flex-1 bg-transparent py-2 text-sm outline-none"
                    />
                  )}
                  <div
                    className={
                      composerExpanded
                        ? "flex justify-end px-1 pb-1"
                        : "shrink-0"
                    }
                  >
                    <button
                      type="submit"
                      title={t("发送")}
                      aria-label={t("发送")}
                      disabled={!message.trim() || create.isPending}
                      className="grid size-9 place-items-center rounded-full bg-slate-900 text-white transition hover:bg-slate-700 disabled:opacity-30"
                    >
                      <ArrowUp className="size-4" />
                    </button>
                  </div>
                </form>
              </div>
            </div>
          </section>
        </div>
      ) : (
        <div className="grid h-full place-items-center px-5 text-center">
          <div>
            <MessageSquareText className="mx-auto size-9 text-slate-300" />
            <h1 className="mt-4 text-xl font-semibold">
              {t("还没有项目")}
            </h1>
            <p className="mt-2 text-sm text-slate-500">
              {t("请先在看板中创建一个项目。")}
            </p>
            <Link
              className="mt-5 inline-block text-sm font-medium text-sky-700"
              to="/board"
            >
              {t("前往看板")}
            </Link>
          </div>
        </div>
      )}

      {projectID && (
        <Sheet
          open={mobileListOpen}
          onOpenChange={setMobileListOpen}
          side="left"
          title={t("讨论列表")}
        >
          <div className="-m-5 h-[calc(100dvh-4.25rem)]">
            <DiscussionSidebar
              projects={projects.data || []}
              projectID={projectID}
              discussions={discussions}
              creating={create.isPending}
              onProjectChange={selectProject}
              onSelect={selectDiscussion}
              onCreate={() => create.mutate(undefined)}
            />
          </div>
        </Sheet>
      )}
    </main>
  );
}
