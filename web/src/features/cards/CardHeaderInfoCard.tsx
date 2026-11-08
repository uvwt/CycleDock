import { useMemo } from "react";
import { useMutation } from "@tanstack/react-query";
import { Link2, ListChecks, Calendar, Activity, Check, CheckCircle2, Clock, AlertCircle, X } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { api } from "../../shared/api/client";
import type { Card, CardDetail } from "../../shared/api/types";
import type { TaskProgress } from "../../shared/realtime/cardStreamState";
import { Badge } from "../../shared/ui/badge";
import { OngoingHealthBadge } from "../../shared/ui/OngoingHealthBadge";
import { ScheduleEditor } from "./TaskDetailsPanel";
import { CardStatusSwitch } from "./CardStatusSwitch";
import { RichText } from "../timeline/RichText";

const statusLabels: Record<string, string> = {
  active: "进行中",
  blocked: "已阻塞",
  completed: "已完成",
  pending: "未开始",
  in_progress: "进行中",
  pass: "已通过",
  failed: "未通过",
};

export function CardHeaderInfoCard({
  card,
  detail,
  projectCards = [],
  task,
  variant = "popover",
  onSync,
  onClose,
}: {
  card: Card;
  detail?: CardDetail;
  projectCards?: Card[];
  task?: TaskProgress;
  variant?: "popover" | "sheet";
  onSync?: () => Promise<void>;
  onClose?: () => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const currentHealth = detail?.health ?? card.health;
  const canAcknowledge =
    card.kind === "ongoing" &&
    (currentHealth === "warning" || currentHealth === "critical");

  const acknowledgeMutation = useMutation({
    mutationFn: () => api.acknowledgeCheckin(card.id),
    onSuccess: async () => {
      if (onSync) await onSync();
      toast.success(t("已标记为已确认"));
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("状态更新失败")),
  });

  const sourceDiscussion = useMemo(() => {
    if (!card.source_discussion_id) return undefined;
    return projectCards.find((item) => item.id === card.source_discussion_id);
  }, [card.source_discussion_id, projectCards]);

  const derivedCards = useMemo(() => {
    if (card.kind !== "discussion") return [];
    return projectCards.filter((item) => item.source_discussion_id === card.id);
  }, [card.id, card.kind, projectCards]);

  const kindLabel = useMemo(() => {
    if (card.kind === "discussion") return t("讨论");
    if (card.kind === "ongoing") return t("定时");
    return t("任务");
  }, [card.kind, t]);

  const totalSteps = task?.step_count || task?.steps?.length || 0;
  const completedSteps = Math.min(
    task?.completed_step_count || 0,
    totalSteps || task?.completed_step_count || 0,
  );
  const progressRatio = totalSteps > 0 ? Math.round((completedSteps / totalSteps) * 100) : 0;
  const normalizedCurrentStep = task?.current_step?.trim() || "";
  const currentStepMatchedInList = Boolean(
    normalizedCurrentStep &&
      task?.steps?.some((step) => step.title.trim() === normalizedCurrentStep),
  );
  const showDistinctTaskTitle = Boolean(
    task?.title?.trim() && task.title.trim() !== card.title.trim(),
  );

  return (
    <section
      className={
        variant === "sheet"
          ? "w-full overflow-y-auto space-y-4"
          : "w-80 max-w-[calc(100vw-1.5rem)] max-h-[calc(100dvh-4.5rem)] overflow-y-auto scrollbar-thin rounded-2xl border border-line bg-surface/95 p-4 shadow-sm backdrop-blur transition-all space-y-3.5"
      }
      aria-label={t("任务详情")}
    >
      {/* 标题与类型状态概览 */}
      <div className="space-y-1.5">
        <div className="flex items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline" className="text-[10px] px-1.5 py-0 h-5 font-normal">
              {kindLabel}
            </Badge>
            {card.kind !== "discussion" && <CardStatusSwitch card={card} />}
            {card.kind === "ongoing" && (
              <OngoingHealthBadge
                health={detail?.health ?? card.health}
                size="xs"
              />
            )}
          </div>
          {variant !== "sheet" && onClose && (
            <button
              type="button"
              onClick={onClose}
              className="inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-muted/80 focus:outline-none focus:ring-1 focus:ring-ring transition-colors"
              aria-label={t("关闭")}
            >
              <X className="size-4" />
            </button>
          )}
        </div>
        <h2 className="text-sm font-semibold leading-snug text-foreground break-words">
          {card.title}
        </h2>
      </div>

      {/* 详细描述 */}
      {card.description?.trim() ? (
        <div className="space-y-1.5">
          <div className="text-[11px] font-medium text-muted-foreground">
            {t("描述")}
          </div>
          <div className="rounded-lg bg-canvas/60 p-2.5 text-xs leading-relaxed text-secondary-foreground break-words border border-line/40">
            <RichText
              content={card.description.trim()}
              className="text-xs leading-relaxed text-secondary-foreground"
            />
          </div>
        </div>
      ) : null}

      {card.kind === "ongoing" && detail?.last_checkin && (
        <div className="space-y-1.5 rounded-lg border border-line/50 bg-canvas/50 p-2.5 text-xs">
          <div className="flex items-center justify-between gap-2">
            <div className="text-[11px] font-medium text-muted-foreground">
              {t("最近巡检")}
            </div>
            {canAcknowledge && (
              <button
                type="button"
                disabled={acknowledgeMutation.isPending}
                onClick={() => acknowledgeMutation.mutate()}
                className="inline-flex h-6 shrink-0 cursor-pointer items-center gap-1 rounded-md bg-emerald-600 px-2 text-[11px] font-medium text-white shadow-2xs transition hover:bg-emerald-500 active:scale-95 disabled:opacity-50"
              >
                <Check className="size-3" />
                <span>{t("已确认")}</span>
              </button>
            )}
          </div>
          <div className="text-secondary-foreground leading-relaxed break-words">
            {detail.last_checkin.content || t("已记录")}
          </div>
        </div>
      )}

      {/* 来源讨论关联 */}
      {sourceDiscussion && (
        <button
          type="button"
          onClick={() => navigate(`/chat/${sourceDiscussion.project_id}/${sourceDiscussion.id}`)}
          className="flex w-full items-center justify-between rounded-lg border border-line bg-surface/60 p-2 text-left text-xs transition hover:bg-muted"
        >
          <div className="flex items-center gap-2 min-w-0 pr-2">
            <Link2 className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate font-medium text-foreground">{sourceDiscussion.title}</span>
          </div>
          <Badge variant="secondary" className="shrink-0 text-[10px]">
            {t("讨论")}
          </Badge>
        </button>
      )}

      {/* 讨论派生出的卡片列表 */}
      {derivedCards.length > 0 && (
        <div className="space-y-2 rounded-lg border border-line/60 bg-surface/40 p-2.5">
          <div className="flex items-center justify-between text-xs">
            <span className="flex items-center gap-1.5 font-medium text-foreground">
              <Link2 className="size-3.5 text-primary" />
              {t("已派生卡片")}
            </span>
            <span className="text-[11px] text-muted-foreground font-mono">
              {derivedCards.length}
            </span>
          </div>
          <div className="space-y-1.5">
            {derivedCards.map((dCard) => (
              <button
                key={dCard.id}
                type="button"
                onClick={() =>
                  navigate(
                    (dCard.kind === "discussion" ? "/chat/" : "/cards/") +
                      `${dCard.project_id}/${dCard.id}`,
                  )
                }
                className="flex w-full items-center justify-between rounded border border-line/40 bg-surface/80 px-2 py-1.5 text-left text-xs transition hover:border-line hover:bg-muted/60"
              >
                <div className="flex items-center gap-1.5 min-w-0 pr-2">
                  <span
                    className={`inline-block size-1.5 shrink-0 rounded-full ${
                      dCard.kind === "ongoing"
                        ? dCard.ongoing_state === "active"
                          ? "bg-emerald-500"
                          : "bg-slate-400"
                        : dCard.todo_state === "working"
                          ? "bg-sky-500"
                          : dCard.todo_state === "review"
                            ? "bg-amber-500"
                            : dCard.todo_state === "done"
                              ? "bg-emerald-500"
                              : "bg-slate-400"
                    }`}
                  />
                  <span className="truncate text-secondary-foreground">
                    {dCard.title}
                  </span>
                </div>
                <Badge variant="outline" className="shrink-0 text-[10px] px-1 py-0 h-4">
                  {dCard.kind === "ongoing" ? t("定时") : t("任务")}
                </Badge>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* 任务进度 & Subtasks / 步骤列表 */}
      {task && (
        <div className="space-y-2.5 rounded-xl border border-line/60 bg-canvas/40 p-3">
          <div className="flex items-center justify-between gap-2 text-xs">
            <span className="flex items-center gap-1.5 shrink-0 font-medium text-foreground">
              <ListChecks className="size-3.5 shrink-0 text-primary" />
              <span>{t("任务进度")}</span>
            </span>
            <div className="flex items-center gap-1.5 shrink-0 whitespace-nowrap text-[11px] text-muted-foreground">
              <span
                className={`inline-block size-1.5 shrink-0 rounded-full ${
                  task.status === "completed" || task.status === "pass"
                    ? "bg-emerald-500"
                    : task.status === "failed" || task.status === "blocked"
                      ? "bg-destructive"
                      : "bg-sky-500"
                }`}
              />
              <span>
                {task.status && task.status in statusLabels
                  ? t(statusLabels[task.status as keyof typeof statusLabels])
                  : task.status || t("进行中")}
              </span>
              {totalSteps > 0 && (
                <span className="tabular-nums font-mono">
                  {completedSteps}/{totalSteps} ({progressRatio}%)
                </span>
              )}
            </div>
          </div>

          {showDistinctTaskTitle && (
            <div className="text-xs font-medium leading-snug text-foreground break-words">
              {task.title}
            </div>
          )}

          {totalSteps > 0 && (
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className={`h-full rounded-full transition-all duration-300 ${
                  task.status === "failed" || task.status === "blocked"
                    ? "bg-destructive"
                    : task.status === "completed" || task.status === "pass"
                      ? "bg-emerald-500"
                      : "bg-primary"
                }`}
                style={{ width: `${progressRatio}%` }}
              />
            </div>
          )}

          {normalizedCurrentStep && !currentStepMatchedInList && (
            <div className="rounded-lg bg-surface/80 px-2.5 py-1.5 text-xs leading-relaxed text-secondary-foreground break-words border border-line/40">
              <span className="text-faint">{t("当前步骤")}：</span>
              {normalizedCurrentStep}
            </div>
          )}

          {task.blocker && (
            <div className="flex items-start gap-1.5 rounded-lg bg-destructive/10 p-2 text-xs leading-relaxed text-destructive break-words">
              <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
              <span>{task.blocker}</span>
            </div>
          )}

          {task.summary && (
            <div className="text-xs text-muted-foreground leading-relaxed break-words">
              {task.summary}
            </div>
          )}

          {!!task.steps?.length && (
            <ul className="space-y-1.5 pt-0.5">
              {task.steps.map((step, index) => {
                const isPassed = step.status === "completed" || step.status === "pass";
                const isFailed = step.status === "failed" || step.status === "blocked";
                const isInProgress =
                  step.status === "in_progress" ||
                  step.status === "active" ||
                  (!isPassed &&
                    !isFailed &&
                    Boolean(normalizedCurrentStep) &&
                    step.title.trim() === normalizedCurrentStep);

                return (
                  <li
                    key={`${index}:${step.title}`}
                    className={`flex items-start gap-2 rounded-lg px-2 py-1.5 text-xs leading-relaxed transition-colors ${
                      isInProgress
                        ? "bg-sky-500/10 text-foreground font-medium"
                        : isPassed
                          ? "text-muted-foreground"
                          : "text-secondary-foreground"
                    }`}
                  >
                    {isPassed ? (
                      <CheckCircle2 className="size-3.5 shrink-0 mt-0.5 text-emerald-500" />
                    ) : isFailed ? (
                      <AlertCircle className="size-3.5 shrink-0 mt-0.5 text-destructive" />
                    ) : isInProgress ? (
                      <Activity className="size-3.5 shrink-0 mt-0.5 text-sky-500 animate-pulse" />
                    ) : (
                      <Clock className="size-3.5 shrink-0 mt-0.5 text-faint" />
                    )}
                    <span className="min-w-0 flex-1 break-words">{step.title}</span>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      )}

      {/* Ongoing 计划调度配置 */}
      {card.kind === "ongoing" && onSync && detail && (
        <div className="space-y-1.5 border-t border-line/60 pt-2">
          <div className="flex items-center gap-1.5 text-xs font-medium text-foreground">
            <Calendar className="size-3.5 text-muted-foreground" />
            <span>{t("执行计划")}</span>
          </div>
          <ScheduleEditor
            cardID={card.id}
            schedules={detail.schedules || []}
            onChange={onSync}
          />
        </div>
      )}
    </section>
  );
}
