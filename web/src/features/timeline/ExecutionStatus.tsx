import { useEffect, useState } from "react";
import { Bot, LoaderCircle, RotateCcw, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { LiveAttempt } from "../../shared/realtime/useCardStream";
import type { ExecutionAttempt } from "./execution";
import { RichText } from "./RichText";
import { RunningDots } from "./RunningDots";

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
    <span
      className="shrink-0 text-[11px] tabular-nums text-slate-400"
      title="本次 Agent 执行时长"
    >
      {Math.floor(seconds / 60)}:{String(seconds % 60).padStart(2, "0")}
    </span>
  );
}

function AgentAttempt({
  attempt,
  live,
  cancelingTurnID,
  retryingAttemptID,
  onCancelTurn,
  onRetryAttempt,
}: {
  attempt: ExecutionAttempt;
  live?: LiveAttempt | null;
  cancelingTurnID?: string;
  retryingAttemptID?: string;
  onCancelTurn: (turnID: string) => void;
  onRetryAttempt: (attemptID: string) => void;
}) {
  const { t } = useTranslation();
  const pending = attempt.kind === "pending" ? attempt.value : null;
  const current = attempt.kind === "current" ? attempt.value : null;
  const history = attempt.kind === "history" ? attempt.value : null;
  const execution = current || history;
  const id = pending?.id || execution?.id || "";
  const status = pending ? "queued" : execution?.status || "queued";
  const sameLiveAttempt = Boolean(current && live?.attemptID === current.id);
  const humanReplied =
    status === "superseded" &&
    execution?.error_summary === "superseded by human reply";
  // 问题和回复由时间线承载，状态行只展示执行中的进度及失败原因。
  const summary =
    status === "running"
      ? execution?.progress_summary || execution?.current_stage || ""
      : status === "failed" || status === "expired" || status === "cancelled"
        ? execution?.error_summary || ""
        : "";
  const failed =
    status === "failed" || status === "expired" || status === "cancelled";
  const retried = Boolean(
    execution?.retry_of_attempt_id || execution?.reason === "retry",
  );

  return (
    <>
      {attempt.question && (
        <article className="flex gap-3" data-attempt-question={id}>
          <span
            aria-hidden
            className="grid size-9 shrink-0 place-items-center rounded-full bg-slate-100 text-slate-600"
          >
            <Bot className="size-4" />
          </span>
          <div className="min-w-0 max-w-[78%]">
            <p className="mb-1.5 text-xs font-semibold text-slate-500">Cycle</p>
            <div className="w-fit max-w-full rounded-2xl bg-slate-100 px-3.5 py-2.5">
              <RichText content={attempt.question} />
            </div>
          </div>
        </article>
      )}
      <article
        className={
          status === "running"
            ? "flex min-w-0 gap-3"
            : "ml-12 min-w-0 border-l-2 border-slate-200 py-1 pl-3"
        }
        data-execution-id={id}
      >
        {status === "running" && (
          <span
            aria-hidden
            className="grid size-9 shrink-0 place-items-center rounded-full bg-slate-100 text-slate-600"
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
                    ? "text-rose-700"
                    : status === "running"
                      ? "text-sky-700"
                      : "text-slate-600"
                }`}
              >
                {status === "running" ? (
                  <span className="font-semibold text-slate-500">Cycle</span>
                ) : (
                  t(
                    humanReplied
                      ? "已收到回复"
                      : attemptLabels[status] || status,
                  )
                )}
                {pending?.dispatch_mode === "interrupt"
                  ? " · " + t("立即转向")
                  : ""}
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
                <div className="mt-1.5 w-fit max-w-full rounded-2xl bg-slate-100 px-3.5 py-2.5 text-slate-500">
                  <RunningDots />
                  {summary && (
                    <div className="mt-1 text-sm leading-6 text-slate-600">
                      <RichText content={summary} />
                    </div>
                  )}
                </div>
              ) : (
                summary && (
                  <div className="mt-1 text-sm leading-6 text-slate-600">
                    <RichText content={summary} />
                  </div>
                )
              )}
            </div>
            {pending && (
              <button
                type="button"
                disabled={cancelingTurnID === pending.id}
                onClick={() => onCancelTurn(pending.id)}
                className="inline-flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 text-xs font-medium text-slate-500 hover:bg-white disabled:opacity-40"
              >
                {cancelingTurnID === pending.id ? (
                  <LoaderCircle className="size-3 animate-spin" />
                ) : (
                  <X className="size-3" />
                )}
                {t("取消排队")}
              </button>
            )}
            {history?.can_retry && (
              <button
                type="button"
                disabled={retryingAttemptID === history.id}
                onClick={() => onRetryAttempt(history.id)}
                className="inline-flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 text-xs font-medium text-sky-700 hover:bg-sky-50 disabled:opacity-40"
              >
                {retryingAttemptID === history.id ? (
                  <LoaderCircle className="size-3 animate-spin" />
                ) : (
                  <RotateCcw className="size-3" />
                )}
                {t("重试")}
              </button>
            )}
          </div>
        </div>
      </article>
    </>
  );
}

type ExecutionActions = {
  live?: LiveAttempt | null;
  cancelingTurnID?: string;
  retryingAttemptID?: string;
  onCancelTurn: (turnID: string) => void;
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
      <p className="text-center text-[11px] text-slate-400">
        {t("系统触发 / 未关联历史")}
      </p>
      <ExecutionStatus attempts={attempts} {...actions} />
    </section>
  );
}
