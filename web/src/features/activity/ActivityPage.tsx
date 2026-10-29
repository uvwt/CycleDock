import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  CircleCheck,
  CircleSlash,
  CircleX,
  Clock3,
  LoaderCircle,
} from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useProject } from "../../app/project-context";
import { keys } from "../../app/query";
import { api } from "../../shared/api/client";
import type { ProjectActivity } from "../../shared/api/types";
import { ErrorView, Spinner } from "../../shared/ui/components";

function localTime(value: string) {
  return new Date(value).toLocaleString([], {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function matchesView(item: ProjectActivity, view: string) {
  if (view === "running") return item.status === "running";
  if (view === "waiting") {
    return item.status === "queued" || item.status === "waiting_human";
  }
  if (view === "completed") {
    return ["completed", "result", "checkin", "confirm"].includes(item.status);
  }
  if (view === "failed") {
    return ["failed", "expired", "reject"].includes(item.status);
  }
  return true;
}

function presentation(item: ProjectActivity) {
  if (item.status === "running") {
    return { label: "正在运行", icon: LoaderCircle, spin: true };
  }
  if (item.status === "queued") return { label: "等待执行", icon: Clock3 };
  if (item.status === "waiting_human")
    return { label: "等待回复", icon: Clock3 };
  if (["completed", "result", "checkin", "confirm"].includes(item.status)) {
    return { label: "已完成", icon: CircleCheck };
  }
  if (["failed", "expired", "reject"].includes(item.status)) {
    return { label: "失败", icon: CircleX };
  }
  if (["cancelled", "superseded"].includes(item.status)) {
    return { label: "已结束", icon: CircleSlash };
  }
  if (item.status === "finding") return { label: "发现", icon: Activity };
  if (item.status === "progress") return { label: "进展", icon: Activity };
  return { label: "活动", icon: Activity };
}

export default function ActivityPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const {
    projectID,
    isPending: projectsPending,
    error: projectsError,
    refetch: refetchProjects,
  } = useProject();
  const view = searchParams.get("view") || "all";

  const activity = useQuery({
    queryKey: keys.activity(projectID),
    queryFn: () => api.projectActivity(projectID, 80),
    enabled: Boolean(projectID),
    refetchInterval: 10_000,
  });

  if (projectsPending) {
    return (
      <main className="grid min-h-[calc(100dvh-3.5rem)] place-items-center md:min-h-dvh">
        <Spinner text={t("读取项目…")} />
      </main>
    );
  }

  if (projectsError) {
    return (
      <main className="mx-auto max-w-xl p-8">
        <ErrorView error={projectsError} onRetry={refetchProjects} />
      </main>
    );
  }

  const items = (activity.data || []).filter((item) => matchesView(item, view));
  const filters = [
    { id: "all", label: t("全部活动") },
    { id: "running", label: t("正在运行") },
    { id: "waiting", label: t("等待处理") },
    { id: "completed", label: t("已完成") },
    { id: "failed", label: t("失败") },
  ];

  function open(item: ProjectActivity) {
    const root = item.card_kind === "discussion" ? "/chat/" : "/cards/";
    navigate(`${root}${projectID}/${item.card_id}`);
  }

  return (
    <main className="mx-auto min-h-[calc(100dvh-3.5rem)] max-w-[1600px] space-y-7 px-5 py-8 md:min-h-dvh md:px-8">
      <header className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-3xl font-semibold tracking-tight text-heading">
            {t("活动")}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("查看 Agent 正在做什么，以及最近发生了什么。")}
          </p>
        </div>
        {projectID && (
          <div className="flex max-w-full gap-1 overflow-x-auto rounded-xl border border-line bg-surface p-1">
            {filters.map((filter) => (
              <button
                key={filter.id}
                type="button"
                onClick={() =>
                  setSearchParams(
                    filter.id === "all" ? {} : { view: filter.id },
                    { replace: true },
                  )
                }
                className={`shrink-0 rounded-lg px-3 py-1.5 text-xs font-medium transition ${
                  view === filter.id
                    ? "bg-muted text-heading"
                    : "text-muted-foreground hover:text-strong"
                }`}
              >
                {filter.label}
              </button>
            ))}
          </div>
        )}
      </header>

      {!projectID ? (
        <div className="py-20 text-center text-sm text-muted-foreground">
          {t("还没有项目")}
        </div>
      ) : activity.isPending ? (
        <div className="py-20 grid place-items-center">
          <Spinner />
        </div>
      ) : activity.error ? (
        <div>
          <ErrorView error={activity.error} />
        </div>
      ) : items.length ? (
        <div className="overflow-hidden rounded-2xl border border-line bg-surface">
          {items.map((item) => {
            const state = presentation(item);
            const Icon = state.icon;
            return (
              <button
                key={`${item.activity_type}-${item.id}`}
                type="button"
                onClick={() => open(item)}
                className="flex w-full items-start gap-4 border-b border-divider px-5 py-4 text-left transition last:border-b-0 hover:bg-canvas"
              >
                <span className="mt-0.5 grid size-9 shrink-0 place-items-center rounded-xl bg-muted text-secondary-foreground">
                  <Icon
                    className={`size-[18px] ${state.spin ? "animate-spin" : ""}`}
                  />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="truncate text-sm font-semibold text-heading">
                      {item.card_title || t("未命名工作")}
                    </span>
                    <span className="text-xs text-faint">{t(state.label)}</span>
                  </span>
                  {item.content && (
                    <span className="mt-1 line-clamp-2 block text-[13px] leading-5 text-muted-foreground">
                      {item.content}
                    </span>
                  )}
                  <span className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-faint">
                    {item.actor_name && <span>{item.actor_name}</span>}
                    <span>{localTime(item.occurred_at)}</span>
                  </span>
                </span>
              </button>
            );
          })}
        </div>
      ) : (
        <div className="py-20 text-center">
          <Activity className="mx-auto size-9 text-placeholder" />
          <p className="mt-4 text-sm font-medium text-foreground">
            {view === "all" ? t("这个项目还没有活动") : t("这个筛选下没有活动")}
          </p>
          <p className="mt-1 text-[13px] text-faint">
            {t("Agent 执行状态和重要活动会出现在这里。")}
          </p>
        </div>
      )}
    </main>
  );
}
