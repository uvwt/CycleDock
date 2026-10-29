import { useEffect, useState } from "react";
import { Bot, LoaderCircle, RotateCcw } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Attempt } from "../../shared/api/types";
import type { LiveAttempt } from "../../shared/realtime/useCardStream";
import type { ExecutionAttempt } from "./execution";
import { RichText } from "./RichText";
import { RunningDots } from "./RunningDots";
import { Button, HintTooltip } from "../../shared/ui/components";

const attemptLabels: Record<string, string> = {
  queued: "等待执行",
  running: "正在执行",
  waiting_human: "等待你的回复",
  completed: "执行完成",
  failed: "执行失败",
  superseded: "已被新要求替代",
  expired: "执行已过期",
  cancelled: "已取消执行",
};

export function RunDuration({
  startedAt,
  finishedAt,
  active,
}: {
  startedAt?: string;
  finishedAt?: string;
  active: boolean;
}) {
  const { t } = useTranslation();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active || !startedAt) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [active, startedAt]);
  if (!startedAt || (!active && !finishedAt)) return null;
  const start = new Date(startedAt).getTime();
  if (!Number.isFinite(start)) return null;
  const end = active ? now : new Date(finishedAt || "").getTime();
  if (!Number.isFinite(end)) return null;
  const seconds = Math.max(0, Math.floor((end - start) / 1000));
  return (
    <HintTooltip content={t("本次 Agent 执行时长")}>
      <span className="shrink-0 cursor-default text-[11px] tabular-nums text-faint">
        {Math.floor(seconds / 60)}:{String(seconds % 60).padStart(2, "0")}
      </span>
    </HintTooltip>
  );
}

/** 正文上方共用的执行元信息，不用错误块替代已输出的内容。 */
export function AttemptMetadata({
  attempt,
  retryingAttemptID,
  onRetryAttempt,
}: {
  attempt: Attempt;
  retryingAttemptID?: string;
  onRetryAttempt: (attemptID: string) => void;
}) {
  const { t } = useTranslation();
  const humanReplied =
    attempt.status === "superseded" &&
    attempt.error_summary === "superseded by human reply";
  return (
    <span
      data-execution-id={attempt.id}
      className="inline-flex items-center gap-2 text-[11px] font-normal text-faint"
    >
      {t(
        humanReplied
          ? "已收到回复"
          : attemptLabels[attempt.status] || attempt.status,
      )}
      <RunDuration
        startedAt={attempt.started_at}
        finishedAt={attempt.finished_at}
        active={attempt.status === "running"}
      />
      {attempt.retry_of_attempt_id || attempt.reason === "retry"
        ? " · " + t("重试")
        : ""}
      {attempt.can_retry && (
        <button
          type="button"
          disabled={retryingAttemptID === attempt.id}
          onClick={() => onRetryAttempt(attempt.id)}
          className="inline-flex items-center gap-1 text-sky-700"
        >
          {retryingAttemptID === attempt.id ? (
            <LoaderCircle className="size-3 animate-spin" />
          ) : (
            <RotateCcw className="size-3" />
          )}
          {t("重试")}
        </button>
      )}
    </span>
  );
}

function AgentAttempt({
  attempt,
  live,
  retryingAttemptID,
  onRetryAttempt,
}: {
  attempt: ExecutionAttempt;
  live?: LiveAttempt | null;
  retryingAttemptID?: string;
  onRetryAttempt: (attemptID: string) => void;
}) {
  const { t } = useTranslation();
  const current = attempt.kind === "current" ? attempt.value : null;
  const history = attempt.kind === "history" ? attempt.value : null;
  const execution = current || history;
  const id = execution?.id || "";
  const status = execution?.status || "queued";
  const sameLiveAttempt = Boolean(current && live?.attemptID === current.id);
  // 问题和回复由时间线承载，状态行只展示执行中的进度及失败原因。
  const summary =
    status === "running"
      ? execution?.progress_summary || execution?.current_stage || ""
      : status === "failed" || status === "expired"
        ? execution?.error_summary || ""
        : "";
  const failed = status === "failed" || status === "expired";
  const retried = Boolean(
    execution?.retry_of_attempt_id || execution?.reason === "retry",
  );

  if (
    ["cancelled", "superseded", "waiting_human"].includes(status) &&
    execution
  ) {
    return (
      <article
        className="flex min-w-0 gap-3.5"
        data-attempt-question={attempt.question ? id : undefined}
      >
        <span
          aria-hidden
          className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-full bg-muted text-secondary-foreground"
        >
          <Bot className="size-4" />
        </span>
        <div className="min-w-0 max-w-[78%]">
          <p className="mb-1.5 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs font-semibold text-muted-foreground">
            <span>Cycle</span>
            <AttemptMetadata
              attempt={execution}
              retryingAttemptID={retryingAttemptID}
              onRetryAttempt={onRetryAttempt}
            />
          </p>
          <div className="w-fit max-w-full rounded-2xl bg-muted px-4 py-2.5">
            {attempt.question ? (
              <RichText content={attempt.question} />
            ) : (
              <p className="text-sm text-muted-foreground">
                {t("暂无已保存的正文")}
              </p>
            )}
          </div>
        </div>
      </article>
    );
  }

  return (
    <>
      {attempt.question && (
        <article className="flex gap-3" data-attempt-question={id}>
          <span
            aria-hidden
            className="grid size-9 shrink-0 place-items-center rounded-full bg-muted text-secondary-foreground"
          >
            <Bot className="size-4" />
          </span>
          <div className="min-w-0 max-w-[78%]">
            <p className="mb-1.5 text-xs font-semibold text-muted-foreground">
              Cycle
            </p>
            <div className="w-fit max-w-full rounded-2xl bg-muted px-3.5 py-2.5">
              <RichText content={attempt.question} />
            </div>
          </div>
        </article>
      )}
      <article
        className={
          status === "running"
            ? "flex min-w-0 gap-3"
            : "ml-12 min-w-0 border-l-2 border-line py-1 pl-3"
        }
        data-execution-id={id}
      >
        {status === "running" && (
          <span
            aria-hidden
            className="grid size-9 shrink-0 place-items-center rounded-full bg-muted text-secondary-foreground"
          >
            <Bot className="size-4" />
          </span>
        )}
        <div className="min-w-0 flex-1">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0 flex-1">
              <p
                className={`text-xs font-medium ${
                  failed
                    ? "text-danger"
                    : status === "running"
                      ? "text-sky-700"
                      : "text-secondary-foreground"
                }`}
              >
                {status === "running" ? (
                  <span className="font-semibold text-muted-foreground">
                    Cycle
                  </span>
                ) : (
                  t(attemptLabels[status] || status)
                )}
                {retried ? " · " + t("重试") : ""}
                <span className="ml-2 inline-flex">
                  <RunDuration
                    startedAt={
                      execution?.started_at ||
                      (sameLiveAttempt ? live?.startedAt : undefined)
                    }
                    finishedAt={execution?.finished_at}
                    active={status === "running"}
                  />
                </span>
              </p>
              {status === "running" ? (
                <div className="mt-1.5 w-fit max-w-full rounded-2xl bg-muted px-3.5 py-2.5 text-muted-foreground">
                  <RunningDots />
                  {summary && (
                    <div className="mt-1 text-sm leading-6 text-secondary-foreground">
                      <RichText content={summary} />
                    </div>
                  )}
                </div>
              ) : (
                summary && (
                  <div className="mt-1 text-sm leading-6 text-secondary-foreground">
                    <RichText content={summary} />
                  </div>
                )
              )}
            </div>
            {history?.can_retry && (
              <Button
                variant="ghost"
                size="xs"
                disabled={retryingAttemptID === history.id}
                onClick={() => onRetryAttempt(history.id)}
                className="text-sky-700 hover:bg-sky-50 dark:hover:bg-sky-950/40"
              >
                {retryingAttemptID === history.id ? (
                  <LoaderCircle className="size-3 animate-spin" />
                ) : (
                  <RotateCcw className="size-3" />
                )}
                {t("重试")}
              </Button>
            )}
          </div>
        </div>
      </article>
    </>
  );
}

type ExecutionActions = {
  live?: LiveAttempt | null;
  retryingAttemptID?: string;
  onRetryAttempt: (attemptID: string) => void;
};

export function ExecutionStatus({
  attempts,
  ...actions
}: { attempts: ExecutionAttempt[] } & ExecutionActions) {
  const visibleAttempts = attempts.filter(
    (attempt) =>
      !(
        attempt.kind === "current" &&
        attempt.value.status === "running" &&
        actions.live?.attemptID === attempt.value.id &&
        (actions.live.finished ||
          actions.live.replies.some((reply) => !reply.failed))
      ),
  );
  if (!visibleAttempts.length) return null;
  return (
    <div className="space-y-3" aria-label="Agent 执行消息">
      {visibleAttempts.map((attempt) => {
        const id = attempt.value.id;
        return (
          <AgentAttempt
            key={attempt.kind + ":" + id}
            attempt={attempt}
            {...actions}
          />
        );
      })}
    </div>
  );
}

export function StandaloneExecution({
  attempts,
  ...actions
}: { attempts: ExecutionAttempt[] } & ExecutionActions) {
  const { t } = useTranslation();
  if (!attempts.length) return null;
  return (
    <section className="space-y-3">
      <p className="text-center text-[11px] text-faint">
        {t("系统触发 / 未关联历史")}
      </p>
      <ExecutionStatus attempts={attempts} {...actions} />
    </section>
  );
}
