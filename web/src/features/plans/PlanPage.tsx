import { useQuery } from "@tanstack/react-query";
import { CalendarClock, CirclePause, Clock3 } from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useProject } from "../../app/project-context";
import { keys } from "../../app/query";
import { api } from "../../shared/api/client";
import type { ProjectSchedule } from "../../shared/api/types";
import { ErrorView, Spinner } from "../../shared/ui/components";

const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

function timeText(item: ProjectSchedule) {
  return `${String(item.hour).padStart(2, "0")}:${String(item.minute).padStart(2, "0")}`;
}

function scheduleText(item: ProjectSchedule) {
  if (item.frequency === "daily") return `每天 ${timeText(item)}`;
  if (item.frequency === "weekly") {
    return `每${weekdays[item.day_of_week ?? 0]} ${timeText(item)}`;
  }
  return `每月 ${item.day_of_month ?? 1} 日 ${timeText(item)}`;
}

function localTime(value?: string) {
  if (!value) return "";
  return new Date(value).toLocaleString([], {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function inactiveLabel(item: ProjectSchedule) {
  if (item.ongoing_state === "archived") return "已归档";
  if (item.ongoing_state === "paused") return "事项已暂停";
  return "计划已暂停";
}

export default function PlanPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const {
    project,
    projectID,
    isPending: projectsPending,
    error: projectsError,
    refetch: refetchProjects,
  } = useProject();
  const view = searchParams.get("view") || "all";

  const schedules = useQuery({
    queryKey: keys.schedules(projectID),
    queryFn: () => api.projectSchedules(projectID),
    enabled: Boolean(projectID),
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

  const items = (schedules.data || []).filter((item) => {
    const active = item.enabled && item.ongoing_state === "active";
    if (view === "enabled") return active;
    if (view === "paused") return !active;
    return true;
  });

  const filters = [
    { id: "all", label: t("全部计划") },
    { id: "enabled", label: t("已启用") },
    { id: "paused", label: t("已暂停") },
  ];

  return (
    <main className="mx-auto min-h-[calc(100dvh-3.5rem)] max-w-5xl px-5 py-8 md:min-h-dvh md:px-8">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p className="text-xs font-semibold tracking-widest text-sky-700">
            {project?.name || t("当前项目")}
          </p>
          <h1 className="mt-2 text-3xl font-semibold tracking-tight text-slate-900">
            {t("计划")}
          </h1>
          <p className="mt-1 text-sm text-slate-500">
            {t("查看这个项目接下来会自动触发的持续工作。")}
          </p>
        </div>
        {projectID && (
          <div className="flex rounded-xl border border-slate-200 bg-white p-1">
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
                className={`rounded-lg px-3 py-1.5 text-xs font-medium transition ${
                  view === filter.id
                    ? "bg-slate-100 text-slate-900"
                    : "text-slate-500 hover:text-slate-800"
                }`}
              >
                {filter.label}
              </button>
            ))}
          </div>
        )}
      </header>

      {!projectID ? (
        <div className="mt-20 text-center text-sm text-slate-500">
          {t("还没有项目")}
        </div>
      ) : schedules.isPending ? (
        <div className="mt-20 grid place-items-center">
          <Spinner />
        </div>
      ) : schedules.error ? (
        <div className="mt-8">
          <ErrorView error={schedules.error} />
        </div>
      ) : items.length ? (
        <div className="mt-8 overflow-hidden rounded-2xl border border-slate-200 bg-white">
          {items.map((item) => {
            const active = item.enabled && item.ongoing_state === "active";
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => navigate(`/cards/${projectID}/${item.card_id}`)}
                className="flex w-full items-start gap-4 border-b border-slate-100 px-5 py-4 text-left transition last:border-b-0 hover:bg-slate-50"
              >
                <span
                  className={`mt-0.5 grid size-9 shrink-0 place-items-center rounded-xl ${
                    active
                      ? "bg-slate-100 text-slate-700"
                      : "bg-slate-50 text-slate-400"
                  }`}
                >
                  {active ? (
                    <CalendarClock className="size-[18px]" />
                  ) : (
                    <CirclePause className="size-[18px]" />
                  )}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-semibold text-slate-900">
                    {item.card_title || t("未命名持续事项")}
                  </span>
                  <span className="mt-1 block text-[13px] text-slate-500">
                    {active ? scheduleText(item) : t(inactiveLabel(item))} ·{" "}
                    {item.timezone}
                  </span>
                  <span className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-slate-400">
                    {active && (
                      <span>
                        <Clock3 className="mr-1 inline size-3.5" />
                        {t("下次")} {localTime(item.next_trigger_at)}
                      </span>
                    )}
                    {item.last_triggered_at && (
                      <span>
                        {t("上次")} {localTime(item.last_triggered_at)}
                      </span>
                    )}
                    {item.last_attempt_status && (
                      <span>
                        {t("最近运行")} · {item.last_attempt_status}
                      </span>
                    )}
                  </span>
                </span>
              </button>
            );
          })}
        </div>
      ) : (
        <div className="mt-20 text-center">
          <CalendarClock className="mx-auto size-9 text-slate-300" />
          <p className="mt-4 text-sm font-medium text-slate-700">
            {view === "all" ? t("这个项目还没有计划") : t("这个筛选下没有计划")}
          </p>
          <p className="mt-1 text-[13px] text-slate-400">
            {t("计划来自持续事项中的日程设置。")}
          </p>
        </div>
      )}
    </main>
  );
}
