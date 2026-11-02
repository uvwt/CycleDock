import { useQuery } from "@tanstack/react-query";
import { CalendarClock, CirclePause, Clock3 } from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useProject } from "../../app/project-context";
import { keys } from "../../app/query";
import { api } from "../../shared/api/client";
import type { ProjectSchedule, OngoingState } from "../../shared/api/types";
import { ErrorView, Spinner } from "../../shared/ui/components";
import { stripMarkdownSummary } from "../../shared/lib/markdown";
import { getAttemptStatusLabelKey } from "../../shared/lib/ongoing-health";
import { OngoingHealthBadge } from "../../shared/ui/OngoingHealthBadge";

const planStateLabels: Record<OngoingState, string> = {
  active: "运行中",
  paused: "已暂停",
  archived: "已归档",
};

const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

function timeText(item: ProjectSchedule) {
  return `${String(item.hour).padStart(2, "0")}:${String(item.minute).padStart(2, "0")}`;
}

function scheduleText(item: ProjectSchedule) {
  if (item.frequency === "interval") {
    return `每 ${item.interval_minutes ?? 5} 分钟`;
  }
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

export default function PlanPage() {
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

  const cards = useQuery({
    queryKey: keys.cards(projectID),
    queryFn: () => api.cards(projectID),
    enabled: Boolean(projectID),
  });
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

  // 计划按持续事项聚合，尚未配置日程的事项也必须能进入会话。
  const items = (cards.data || [])
    .filter(
      (card) =>
        card.kind === "ongoing" &&
        (view === "all" || card.ongoing_state === view),
    )
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  const filters = [
    { id: "all", label: t("全部定时任务") },
    { id: "active", label: t("运行中") },
    { id: "paused", label: t("已暂停") },
    { id: "archived", label: t("已归档") },
  ];

  return (
    <main className="mx-auto flex-1 min-h-0 w-full max-w-7xl space-y-7 overflow-y-auto px-5 py-6 pb-24 md:px-8 md:py-8 md:pb-24">
      <header className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-3xl font-semibold tracking-tight text-heading">
            {t("定时任务")}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("管理定时任务与长期工作，查看执行结果与自动执行日程。")}
          </p>
        </div>
        <div className="flex max-w-full flex-wrap items-center gap-2">
          {projectID && (
            <div className="flex rounded-xl border border-line bg-surface p-1">
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
                      ? "bg-muted text-heading"
                      : "text-muted-foreground hover:text-strong"
                  }`}
                >
                  {filter.label}
                </button>
              ))}
            </div>
          )}
        </div>
      </header>

      {!projectID ? (
        <div className="py-20 text-center text-sm text-muted-foreground">
          {t("还没有项目")}
        </div>
      ) : cards.isPending || schedules.isPending ? (
        <div className="py-20 grid place-items-center">
          <Spinner />
        </div>
      ) : cards.error || schedules.error ? (
        <div>
          <ErrorView error={cards.error || schedules.error} />
        </div>
      ) : items.length ? (
        <div className="overflow-hidden rounded-2xl border border-line bg-surface">
          {items.map((card) => {
            const active = card.ongoing_state === "active";
            const cardSchedules = (schedules.data || []).filter(
              (item) => item.card_id === card.id,
            );
            return (
              <button
                key={card.id}
                type="button"
                onClick={() => navigate(`/plans/${projectID}/${card.id}`)}
                className="flex w-full items-start gap-4 border-b border-divider px-5 py-4 text-left transition last:border-b-0 hover:bg-canvas"
              >
                <span className="mt-0.5 grid size-9 shrink-0 place-items-center rounded-xl bg-muted text-foreground">
                  {active ? (
                    <CalendarClock className="size-[18px]" />
                  ) : (
                    <CirclePause className="size-[18px]" />
                  )}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex flex-wrap items-center justify-between gap-2">
                    <span className="truncate text-sm font-semibold text-heading">
                      {card.title || t("未命名定时任务")}
                    </span>
                    <span className="flex shrink-0 items-center gap-2">
                      <OngoingHealthBadge health={card.health} size="xs" />
                      {card.ongoing_state && (
                        <span className="text-xs text-faint">
                          {t(planStateLabels[card.ongoing_state])}
                        </span>
                      )}
                    </span>
                  </span>
                  {stripMarkdownSummary(card.description) && (
                    <span className="mt-1 line-clamp-2 block text-[13px] leading-relaxed text-muted-foreground">
                      {stripMarkdownSummary(card.description)}
                    </span>
                  )}
                  {cardSchedules.length ? (
                    cardSchedules.map((item) => (
                      <span
                        key={item.id}
                        className="mt-2 block text-xs text-faint"
                      >
                        {scheduleText(item)} · {item.timezone}
                        {!item.enabled && <> · {t("日程已停用")}</>}
                        {active && item.enabled && (
                          <span className="ml-3">
                            <Clock3 className="mr-1 inline size-3.5" />
                            {t("下次")} {localTime(item.next_trigger_at)}
                          </span>
                        )}
                        {item.last_triggered_at && (
                          <span className="ml-3">
                            {t("上次")} {localTime(item.last_triggered_at)}
                          </span>
                        )}
                        {item.last_attempt_status && (
                          <span className="ml-3">
                            {t("最近运行")} ·{" "}
                            {t(getAttemptStatusLabelKey(item.last_attempt_status))}
                          </span>
                        )}
                      </span>
                    ))
                  ) : (
                    <span className="mt-2 block text-xs text-faint">
                      {t("未设置自动执行时间")}
                    </span>
                  )}
                </span>
              </button>
            );
          })}
        </div>
      ) : (
        <div className="py-20 text-center">
          <CalendarClock className="mx-auto size-9 text-placeholder" />
          <p className="mt-4 text-sm font-medium text-foreground">
            {view === "all" ? t("这个项目还没有定时任务") : t("这个筛选下没有定时任务")}
          </p>
          <p className="mt-1 text-[13px] text-faint">
            {t("从讨论中生成长期工作，在详情中配置周期规则。")}
          </p>
        </div>
      )}
    </main>
  );
}
