import { DeviceMenu } from "../devices/DeviceMenu";
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { MessageSquareText, PanelLeft } from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import { ErrorView, Sheet, Spinner } from "../../shared/ui/components";
import type { Card } from "../../shared/api/types";
import { useProject } from "../../app/project-context";
import { useContextSidebarVisible } from "../../app/context-sidebar";
import { DiscussionSidebar } from "./DiscussionSidebar";
import { DiscussionStartComposer } from "./DiscussionStartComposer";

export default function ChatHome() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const listVisible = useContextSidebarVisible();
  const [mobileListOpen, setMobileListOpen] = useState(false);
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
      <main className="grid h-[calc(100dvh-3.5rem)] place-items-center md:h-dvh">
        <Spinner text={t("读取项目…")} />
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
    <main className="relative h-[calc(100dvh-3.5rem)] min-h-0 bg-surface md:h-dvh">
      <div className="absolute top-3 right-3 z-20 sm:right-4">
        <DeviceMenu />
      </div>
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
            />
          </div>

          <section className="relative flex min-h-0 min-w-0 flex-col">
            <button
              type="button"
              title={t("打开讨论列表")}
              aria-label={t("打开讨论列表")}
              onClick={() => setMobileListOpen(true)}
              className="absolute top-3 left-3 z-10 grid size-9 place-items-center rounded-xl border border-line bg-surface/90 text-muted-foreground shadow-sm backdrop-blur hover:bg-muted md:hidden"
            >
              <PanelLeft className="size-5" />
            </button>

            <div className="flex min-h-0 flex-1 flex-col items-center justify-center px-5 pb-6">
              <div className="w-full max-w-3xl">
                <div className="mb-10 text-center">
                  <h1 className="text-2xl font-semibold tracking-tight text-heading sm:text-3xl">
                    {t("今天想推进什么？")}
                  </h1>
                </div>

                <DiscussionStartComposer
                  key={projectID}
                  projectID={projectID}
                />
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
              {t("请先在工作区中创建一个项目。")}
            </p>
            <Link
              className="mt-5 inline-block text-sm font-medium text-sky-700"
              to="/board"
            >
              {t("前往工作")}
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
              discussions={discussions}
              onSelect={selectDiscussion}
              onCreate={startDiscussion}
            />
          </div>
        </Sheet>
      )}
    </main>
  );
}
