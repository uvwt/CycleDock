import { useQuery } from "@tanstack/react-query";
import { BellOff, CalendarClock, CirclePause, Pin } from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useProject } from "../../app/project-context";
import { keys } from "../../app/query";
import { api } from "../../shared/api/client";
import type { ProjectSchedule, OngoingState } from "../../shared/api/types";
import { ErrorView } from "../../shared/ui/components";
import {
  CardStatusDot,
  getCardVisualStatus,
} from "../../shared/ui/CardStatusDot";
import { OngoingHealthBadge } from "../../shared/ui/OngoingHealthBadge";
import { PlanSkeleton } from "./PlanSkeleton";
import { summarizePlanSchedules } from "./schedule-summary";

const planStateLabels: Record<OngoingState, string> = {
  active: "运行中",
  paused: "已暂停",
  archived: "已归档",
};

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
        </header>
        <PlanSkeleton />
      </main>
    );
  }

  if (projectsError && !projectID) {
    return (
      <main className="mx-auto w-full max-w-xl p-8">
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
  const schedulesByCard = new Map<string, ProjectSchedule[]>();
  for (const item of schedules.data || []) {
    const existing = schedulesByCard.get(item.card_id);
    if (existing) {
      existing.push(item);
    } else {
      schedulesByCard.set(item.card_id, [item]);
    }
  }
  const filters = [
    { id: "all", label: t("全部定时任务") },
    { id: "active", label: t("运行中") },
    { id: "paused", label: t("已暂停") },
  ];

  return (
    <main className="mx-auto flex-1 min-h-0 w-full max-w-7xl space-y-4 overflow-y-auto px-4 py-4 pb-24 md:space-y-7 md:px-8 md:py-8 md:pb-24">
      <header className="flex flex-wrap items-center justify-between gap-4">
        <div className="hidden md:block">
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
        <PlanSkeleton />
      ) : (cards.error && !cards.data) ||
        (schedules.error && !schedules.data) ? (
        <div>
          <ErrorView
            error={cards.error || schedules.error}
            onRetry={() => {
              void cards.refetch();
              void schedules.refetch();
            }}
          />
        </div>
      ) : items.length ? (
        <div className="overflow-hidden rounded-2xl border border-line bg-surface">
          {items.map((card) => {
            const active = card.ongoing_state === "active";
            const visualStatus = getCardVisualStatus(card);
            const cardSchedules = schedulesByCard.get(card.id) || [];
            const summary = summarizePlanSchedules(cardSchedules, {
              ongoingState: card.ongoing_state || "active",
              t,
            });
            return (
              <button
                key={card.id}
                type="button"
                onClick={() => navigate(`/plans/${projectID}/${card.id}`)}
                className="flex w-full items-start gap-3 border-b border-divider px-4 py-3.5 text-left transition last:border-b-0 hover:bg-canvas md:gap-4 md:px-5 md:py-4"
              >
                <span className="relative mt-0.5 grid size-9 shrink-0 place-items-center rounded-xl bg-muted text-foreground">
                  {active ? (
                    <CalendarClock className="size-[18px]" />
                  ) : (
                    <CirclePause className="size-[18px]" />
                  )}
                  {visualStatus !== "idle" && (
                    <span className="pointer-events-none absolute -top-0.5 -right-0.5 rounded-full bg-surface p-0.5">
                      <CardStatusDot status={visualStatus} />
                    </span>
                  )}
                </span>
                <span className="min-w-0 flex-1">
                  {/* 第一行固定为单行：左侧截断标题与小图标，右侧状态徽章保持稳定对齐不掉行 */}
                  <span className="flex items-center justify-between gap-2.5">
                    <span className="flex min-w-0 flex-1 items-center gap-1.5">
                      <span className="truncate text-sm font-semibold text-heading">
                        {card.title || t("未命名定时任务")}
                      </span>
                      {card.pinned_at && (
                        <Pin className="size-3.5 shrink-0 text-amber-500 fill-amber-500 rotate-45" />
                      )}
                      {card.notifications_muted && (
                        <span
                          title={t("通知已静音")}
                          aria-label={t("通知已静音")}
                          className="inline-flex shrink-0"
                        >
                          <BellOff className="size-3.5 shrink-0 text-muted-foreground/75" />
                        </span>
                      )}
                    </span>
                    <span className="flex shrink-0 items-center gap-1.5 whitespace-nowrap">
                      {visualStatus === "answering" && (
                        <span className="shrink-0 whitespace-nowrap rounded-full bg-emerald-500/10 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:text-emerald-300">
                          {t("正在执行")}
                        </span>
                      )}
                      {visualStatus === "unread" && (
                        <span className="shrink-0 whitespace-nowrap rounded-full bg-sky-500/10 px-2 py-0.5 text-[11px] font-medium text-sky-700 dark:text-sky-300">
                          {t("新消息")}
                        </span>
                      )}
                      {(card.health === "healthy" ||
                        card.health === "warning" ||
                        card.health === "critical" ||
                        card.health === "acknowledged") && (
                        <OngoingHealthBadge
                          health={card.health}
                          size="xs"
                          className="whitespace-nowrap"
                        />
                      )}
                      {card.ongoing_state && card.ongoing_state !== "active" && (
                        <span className="shrink-0 whitespace-nowrap rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                          {t(planStateLabels[card.ongoing_state])}
                        </span>
                      )}
                    </span>
                  </span>
                  {summary.isEmpty ? (
                    <span className="mt-1 block text-xs leading-5 text-faint">
                      {t("未设置自动执行时间")}
                    </span>
                  ) : (
                    <span className="mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs leading-5 text-faint tabular-nums">
                      <span className="min-w-0 break-words text-muted-foreground">
                        {summary.rulesText}
                      </span>
                      {summary.nextTriggerText && (
                        <span className="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap">
                          <span aria-hidden className="text-faint">
                            ·
                          </span>
                          <span>{summary.nextTriggerText}</span>
                        </span>
                      )}
                      {summary.statusHint && (
                        <span className="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap">
                          <span aria-hidden className="text-faint">
                            ·
                          </span>
                          <span>{summary.statusHint}</span>
                        </span>
                      )}
                      {summary.hasFailedAttempt && (
                        <span className="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap text-danger">
                          <span aria-hidden className="text-danger/75">
                            ·
                          </span>
                          <span>{t("上次执行失败")}</span>
                        </span>
                      )}
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
