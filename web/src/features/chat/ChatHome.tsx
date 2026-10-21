import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
import { MessageSquareText } from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import { ErrorView, Spinner } from "../../shared/ui/components";
import { WorkbenchLayout } from "../workbench/WorkbenchLayout";
import { NewDiscussionComposer } from "../workbench/NewDiscussionComposer";

export default function ChatHome() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [openToken, setOpenToken] = useState(() => new URLSearchParams(window.location.search).get("newDiscussion") === "1" ? 1 : 0);
  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  const requested = searchParams.get("project");
  const project = projects.data?.find((item) => item.id === requested) || projects.data?.[0];
  const projectID = project?.id || "";
  const cards = useQuery({
    queryKey: keys.cards(projectID),
    queryFn: () => api.cards(projectID),
    enabled: Boolean(projectID),
  });

  if (projects.isPending) {
    return <main className="grid h-dvh place-items-center"><Spinner text={t("读取项目…")} /></main>;
  }
  if (projects.error) {
    return <main className="mx-auto max-w-xl p-8"><ErrorView error={projects.error} /></main>;
  }

  return (
    <WorkbenchLayout
      mode="discussion"
      projectID={projectID}
      cards={cards.data || []}
      onSelectDiscussion={(card) => navigate(`/chat/${card.project_id}/${card.id}`)}
      onCreateDiscussion={() => setOpenToken((value) => value + 1)}
      fill
    >
      <main className="flex h-full min-h-0 flex-col bg-white">
        {project ? (
          <div className="flex min-h-0 flex-1 flex-col items-center justify-center overflow-y-auto px-4 py-12">
            <div className="w-full max-w-2xl">
              <div className="mb-6 text-center">
                <span className="mx-auto grid size-11 place-items-center rounded-2xl bg-slate-100 text-slate-600">
                  <MessageSquareText className="size-5" />
                </span>
                <h1 className="mt-4 text-2xl font-semibold tracking-tight text-slate-900">
                  {t("今天想推进什么？")}
                </h1>
                <p className="mt-2 text-sm text-slate-500">
                  {t("直接输入第一句话，CycleDock 会创建 Discussion 并开始协作。")}
                </p>
              </div>
              <NewDiscussionComposer
                key={projectID}
                projectID={projectID}
                projectName={project.name}
                openToken={openToken}
                embedded
              />
            </div>
          </div>
        ) : (
          <div className="grid h-full place-items-center px-5 text-center">
            <div>
              <MessageSquareText className="mx-auto size-9 text-slate-300" />
              <h1 className="mt-4 text-xl font-semibold">{t("还没有项目")}</h1>
              <button type="button" onClick={() => navigate("/board?newProject=1")} className="mt-4 text-sm font-medium text-sky-700">
                {t("新建项目")}
              </button>
            </div>
          </div>
        )}
      </main>
    </WorkbenchLayout>
  );
}
