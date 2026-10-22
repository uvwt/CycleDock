import { useEffect, useState } from "react";
import { Bot, LoaderCircle, RotateCcw, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { LiveRun } from "../../shared/realtime/useCardStream";
import type { ExecutionAttempt } from "./execution";
import { RichText } from "./RichText";

const runLabels: Record<string, string> = {
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
  active,
}: {
  startedAt?: string;
  active: boolean;
}) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active || !startedAt) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [active, startedAt]);
  if (!startedAt || !active) return null;
  const start = new Date(startedAt).getTime();
  if (!Number.isFinite(start)) return null;
  const seconds = Math.max(0, Math.floor((now - start) / 1000));
  return (
    <span className="shrink-0 text-[11px] tabular-nums text-slate-400" title="本次 Agent 执行时长">
      {Math.floor(seconds / 60)}:{String(seconds % 60).padStart(2, "0")}
    </span>
  );
}

function AgentAttempt({
  attempt,
  live,
  cancelingTriggerID,
  retryingTriggerID,
  onCancelTrigger,
  onRetryTrigger,
}: {
  attempt: ExecutionAttempt;
  live?: LiveRun | null;
  cancelingTriggerID?: string;
  retryingTriggerID?: string;
  onCancelTrigger: (triggerID: string) => void;
  onRetryTrigger: (triggerID: string) => void;
}) {
  const { t } = useTranslation();
  const id = attempt.value.trigger_id;
  const queue = attempt.kind === "queue" ? attempt.value : null;
  const run = attempt.kind === "run" ? attempt.value : null;
  const history = attempt.kind === "history" ? attempt.value : null;
  const status = queue ? "queued" : run?.status || history?.status || "queued";
  const sameLiveRun = Boolean(run && live?.runID === run.id);
  const summary =
    run?.error_summary ||
    history?.error_summary ||
    run?.progress_summary ||
    run?.current_stage ||
    history?.progress_summary ||
    history?.current_stage ||
    "";
  const failed = status === "failed" || status === "expired" || status === "cancelled";
  const retry = attempt.kind !== "queue" && Boolean(attempt.value.retry_of_trigger_id);

  return (
    <article className="flex min-w-0 items-start gap-3" data-trigger-id={id}>
      <span
        aria-hidden
        className="grid size-9 shrink-0 place-items-center rounded-full bg-slate-100 text-slate-600"
      >
        <Bot className="size-4" />
      </span>
      <div className="min-w-0 max-w-[min(88%,740px)] flex-1 rounded-xl rounded-tl-sm border border-slate-200/80 bg-slate-50 px-4 py-3">
        <div className="mb-2 flex items-center justify-between gap-3">
          <span className="text-xs font-semibold text-slate-600">Cycle Agent</span>
          {run?.status === "running" && (
            <span className="inline-flex shrink-0 items-center gap-1.5 text-[11px] text-sky-700">
              <span className="live-dot size-1.5 rounded-full bg-sky-500" />
              {t("运行中")}
              <RunDuration startedAt={run.started_at || (sameLiveRun ? live?.startedAt : undefined)} active />
            </span>
          )}
        </div>
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0 flex-1">
            <p
              className={`text-xs font-medium ${failed ? "text-rose-700" : status === "running" ? "text-sky-700" : "text-slate-600"}`}
            >
              {t(runLabels[status] || status)}
              {queue?.dispatch_mode === "interrupt" ? " · " + t("立即转向") : ""}
              {retry ? " · " + t("重试") : ""}
            </p>
            {summary && (
              <div className="mt-1 text-sm leading-6 text-slate-600">
                <RichText content={summary} />
              </div>
            )}
            {sameLiveRun && live?.text && (
              <div className="mt-2 border-t border-slate-200 pt-2">
                <RichText content={live.text} />
              </div>
            )}
            {sameLiveRun && !live?.text && status === "running" && (
              <div aria-label={t("Agent 正在工作")} className="mt-2 flex items-center gap-1">
                <span className="live-dot size-1.5 rounded-full bg-slate-400" />
                <span className="live-dot size-1.5 rounded-full bg-slate-400" />
                <span className="live-dot size-1.5 rounded-full bg-slate-400" />
              </div>
            )}
          </div>
          {queue && (
            <button
              type="button"
              disabled={cancelingTriggerID === id}
              onClick={() => onCancelTrigger(id)}
              className="inline-flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 text-xs font-medium text-slate-500 hover:bg-white disabled:opacity-40"
            >
              {cancelingTriggerID === id ? <LoaderCircle className="size-3 animate-spin" /> : <X className="size-3" />}
              {t("取消排队")}
            </button>
          )}
          {history?.can_retry && (
            <button
              type="button"
              disabled={retryingTriggerID === id}
              onClick={() => onRetryTrigger(id)}
              className="inline-flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 text-xs font-medium text-sky-700 hover:bg-sky-50 disabled:opacity-40"
            >
              {retryingTriggerID === id ? <LoaderCircle className="size-3 animate-spin" /> : <RotateCcw className="size-3" />}
              {t("重试")}
            </button>
          )}
        </div>
      </div>
    </article>
  );
}

export function ExecutionStatus({
  attempts,
  ...actions
}: {
  attempts: ExecutionAttempt[];
  live?: LiveRun | null;
  cancelingTriggerID?: string;
  retryingTriggerID?: string;
  onCancelTrigger: (triggerID: string) => void;
  onRetryTrigger: (triggerID: string) => void;
}) {
  if (!attempts.length) return null;
  return (
    <div className="space-y-3" aria-label="Agent 执行消息">
      {attempts.map((attempt) => (
        <AgentAttempt
          key={attempt.kind + ":" + attempt.value.trigger_id}
          attempt={attempt}
          {...actions}
        />
      ))}
    </div>
  );
}

export function StandaloneExecution({
  attempts,
  ...actions
}: {
  attempts: ExecutionAttempt[];
  live?: LiveRun | null;
  cancelingTriggerID?: string;
  retryingTriggerID?: string;
  onCancelTrigger: (triggerID: string) => void;
  onRetryTrigger: (triggerID: string) => void;
}) {
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
