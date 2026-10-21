import { LoaderCircle, RotateCcw, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { ExecutionAttempt } from "./execution";

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

function triggerID(attempt: ExecutionAttempt) {
  return attempt.value.trigger_id;
}

function AttemptRow({
  attempt,
  cancelingTriggerID,
  retryingTriggerID,
  onCancelTrigger,
  onRetryTrigger,
}: {
  attempt: ExecutionAttempt;
  cancelingTriggerID?: string;
  retryingTriggerID?: string;
  onCancelTrigger: (triggerID: string) => void;
  onRetryTrigger: (triggerID: string) => void;
}) {
  const { t } = useTranslation();
  const id = triggerID(attempt);
  const queue = attempt.kind === "queue" ? attempt.value : null;
  const run = attempt.kind === "run" ? attempt.value : null;
  const history = attempt.kind === "history" ? attempt.value : null;
  const status = queue ? "queued" : run?.status || history?.status || "queued";
  const summary =
    run?.progress_summary ||
    run?.current_stage ||
    history?.error_summary ||
    history?.result_summary ||
    history?.progress_summary ||
    history?.current_stage ||
    "";
  const failed =
    status === "failed" || status === "expired" || status === "cancelled";
  const retry =
    attempt.kind !== "queue" && Boolean(attempt.value.retry_of_trigger_id);

  return (
    <div
      className="flex items-start justify-between gap-3 border-l-2 border-slate-200 py-1.5 pl-3 text-xs"
      data-trigger-id={id}
    >
      <div className="min-w-0">
        <p
          className={
            failed
              ? "font-medium text-rose-700"
              : status === "running"
                ? "font-medium text-sky-700"
                : "font-medium text-slate-500"
          }
        >
          {t(runLabels[status] || status)}
          {queue?.dispatch_mode === "interrupt" ? " · " + t("立即转向") : ""}
          {retry ? " · " + t("重试") : ""}
        </p>
        {summary && (
          <p className="mt-0.5 line-clamp-2 leading-5 text-slate-400">
            {summary}
          </p>
        )}
      </div>

      {queue && (
        <button
          type="button"
          disabled={cancelingTriggerID === id}
          onClick={() => onCancelTrigger(id)}
          className="inline-flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 font-medium text-slate-500 hover:bg-slate-100 disabled:opacity-40"
        >
          {cancelingTriggerID === id ? (
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
          disabled={retryingTriggerID === id}
          onClick={() => onRetryTrigger(id)}
          className="inline-flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 font-medium text-sky-700 hover:bg-sky-50 disabled:opacity-40"
        >
          {retryingTriggerID === id ? (
            <LoaderCircle className="size-3 animate-spin" />
          ) : (
            <RotateCcw className="size-3" />
          )}
          {t("重试")}
        </button>
      )}
    </div>
  );
}

export function ExecutionStatus({
  attempts,
  ...actions
}: {
  attempts: ExecutionAttempt[];
  cancelingTriggerID?: string;
  retryingTriggerID?: string;
  onCancelTrigger: (triggerID: string) => void;
  onRetryTrigger: (triggerID: string) => void;
}) {
  if (!attempts.length) return null;
  return (
    <div className="mt-2 space-y-1" aria-label="execution status">
      {attempts.map((attempt) => (
        <AttemptRow
          key={attempt.kind + ":" + triggerID(attempt)}
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
  cancelingTriggerID?: string;
  retryingTriggerID?: string;
  onCancelTrigger: (triggerID: string) => void;
  onRetryTrigger: (triggerID: string) => void;
}) {
  const { t } = useTranslation();
  if (!attempts.length) return null;
  return (
    <section className="rounded-xl border border-dashed border-slate-200 bg-slate-50/70 px-4 py-3">
      <p className="mb-2 text-[11px] font-semibold tracking-wide text-slate-400">
        {t("系统触发 / 未关联历史")}
      </p>
      <ExecutionStatus attempts={attempts} {...actions} />
    </section>
  );
}
