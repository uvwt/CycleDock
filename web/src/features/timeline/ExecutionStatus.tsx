import { useEffect, useState } from "react";
import { AlertCircle, Clock, Monitor } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Attempt, CardKind } from "../../shared/api/types";
import type { LiveAttempt } from "../../shared/realtime/useCardStream";
import type { ExecutionAttempt } from "./execution";
import { BubbleRetryMenu } from "./BubbleRetryMenu";
import { RichText } from "./RichText";
import { ChatBubbleMessage, MessageLoading } from "../../shared/ui/chat";
import { Button, HintTooltip } from "../../shared/ui/components";

const attemptLabels: Record<string, string> = {
  queued: "等待执行",
  running: "正在执行",
  completed: "执行完成",
  failed: "执行失败",
  superseded: "已由新消息替代",
  expired: "执行已过期",
  cancelled: "已停止生成",
};

function resolveAttemptStatusLabel(attempt: Attempt): string {
  if (
    attempt.status === "superseded" &&
    attempt.error_summary === "superseded by human reply"
  ) {
    return "已收到回复";
  }
  if (
    attempt.status === "superseded" &&
    attempt.error_summary === "interrupted by server shutdown"
  ) {
    return "服务重启续跑";
  }
  return attemptLabels[attempt.status] || attempt.status;
}

export function RunDuration({
  startedAt,
  finishedAt,
  active,
  onClick,
}: {
  startedAt?: string;
  finishedAt?: string;
  active: boolean;
  onClick?: () => void;
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
  const content = (
    <span className={`shrink-0 text-[11px] tabular-nums text-faint ${onClick ? "cursor-pointer hover:text-sky-600 hover:underline" : "cursor-default"}`}>
      {Math.floor(seconds / 60)}:{String(seconds % 60).padStart(2, "0")}
      {onClick && " ›"}
    </span>
  );
  if (onClick) {
    return (
      <HintTooltip content={t("查看执行链路")}>
        <button type="button" onClick={onClick} className="inline-flex items-center">
          {content}
        </button>
      </HintTooltip>
    );
  }
  return (
    <HintTooltip content={t("本次 Agent 执行时长")}>
      {content}
    </HintTooltip>
  );
}

/** 位于气泡右侧的操作设备状态徽章。 */
export function OperatingDevice({ name }: { name: string }) {
  const { t } = useTranslation();
  return (
    <span
      data-operating-device={name}
      className="inline-flex select-none items-center gap-1.5 rounded-full border border-sky-200/80 bg-sky-50/80 px-2.5 py-0.5 text-[11px] font-normal text-sky-700 shadow-2xs dark:border-sky-800/60 dark:bg-sky-950/40 dark:text-sky-300 shrink-0"
    >
      <Monitor className="size-3 shrink-0 text-sky-600 dark:text-sky-400" />
      <span>
        {t("正在操作")} <strong className="font-semibold">{name}</strong>
      </span>
    </span>
  );
}

/** 正文上方共用的执行元信息，不用错误块替代已输出的内容。 */
export function AttemptMetadata({
  attempt,
  cardKind,
  cardPreferredRouteID,
  retryingAttemptID,
  onRetryAttempt,
  onOpenTrace,
}: {
  attempt: Attempt;
  cardKind?: CardKind;
  cardPreferredRouteID?: string | null;
  retryingAttemptID?: string;
  onRetryAttempt: (
    attemptID: string,
    preferredModelRouteID?: string | null,
  ) => void;
  onOpenTrace?: (attemptID: string) => void;
}) {
  const { t } = useTranslation();
  const isRunning = attempt.status === "running";
  return (
    <span
      data-execution-id={attempt.id}
      className="inline-flex items-center gap-2 text-[11px] font-normal text-faint"
    >
      {!isRunning && t(resolveAttemptStatusLabel(attempt))}
      <RunDuration
        startedAt={attempt.started_at}
        finishedAt={attempt.finished_at}
        active={isRunning}
        onClick={onOpenTrace ? () => onOpenTrace(attempt.id) : undefined}
      />
      {attempt.retry_of_attempt_id || attempt.reason === "retry"
        ? " · " + t("重试")
        : ""}
      {attempt.can_retry && (
        <BubbleRetryMenu
          replyModel={attempt.model}
          cardKind={cardKind}
          cardPreferredRouteID={cardPreferredRouteID}
          align="start"
          loading={retryingAttemptID === attempt.id}
          disabled={Boolean(retryingAttemptID && retryingAttemptID !== attempt.id)}
          onConfirmRetry={(preferredModelRouteID) =>
            onRetryAttempt(attempt.id, preferredModelRouteID)
          }
          renderTrigger={({ onClick, disabled, title, icon }) => (
            <button
              type="button"
              disabled={disabled}
              title={title}
              onClick={onClick}
              className="inline-flex cursor-pointer items-center gap-1 text-sky-700 disabled:cursor-not-allowed disabled:opacity-50"
            >
              <span className="[&>svg]:size-3">{icon}</span>
              {t("重试")}
            </button>
          )}
        />
      )}
    </span>
  );
}

function AgentAttempt({
  attempt,
  live,
  showSender = true,
  cardKind,
  cardPreferredRouteID,
  retryingAttemptID,
  onRetryAttempt,
  onOpenTrace,
}: {
  attempt: ExecutionAttempt;
  live?: LiveAttempt | null;
  showSender?: boolean;
  cardKind?: CardKind;
  cardPreferredRouteID?: string | null;
  retryingAttemptID?: string;
  onRetryAttempt: (
    attemptID: string,
    preferredModelRouteID?: string | null,
  ) => void;
  onOpenTrace?: (attemptID: string) => void;
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
  const runningStartedAt =
    execution?.started_at || (sameLiveAttempt ? live?.startedAt : undefined);
  const showRunningDevice = Boolean(
    status === "running" &&
    sameLiveAttempt &&
    live?.deviceName &&
    !live.finished,
  );
  // 续在上一条气泡后面时，执行耗时挂在组首（第一个气泡），本气泡不单独展示孤立时间行。
  const showRunningHeader = status === "running" && showSender;

  if (status === "running") {
    return (
      <article className="flex min-w-0" data-execution-id={id}>
        <div className="min-w-0 flex-1">
          {showRunningHeader && (
            <div className="flex flex-wrap items-center gap-x-2 text-xs font-medium text-sky-700">
              <span className="font-semibold text-muted-foreground">Cycle</span>
              <span className="select-none text-[10px] text-muted-foreground/40">
                •
              </span>
              {retried ? <span> · {t("重试")}</span> : null}
              <span className="inline-flex items-center gap-1.5 text-[11px] font-normal text-faint">
                <RunDuration
                  startedAt={runningStartedAt}
                  finishedAt={execution?.finished_at}
                  active
                  onClick={onOpenTrace && id ? () => onOpenTrace(id) : undefined}
                />
              </span>
            </div>
          )}
          <div
            className={`${showRunningHeader ? "mt-1.5 " : ""}flex flex-wrap items-end gap-2.5`}
          >
            <ChatBubbleMessage variant="received" className="w-fit max-w-full">
              <MessageLoading />
              {summary && (
                <div className="mt-1.5 border-t border-line/60 pt-1.5 text-sm leading-relaxed text-secondary-foreground">
                  <RichText content={summary} />
                </div>
              )}
            </ChatBubbleMessage>
            {showRunningDevice && live?.deviceName && (
              <div className="self-end pb-1 shrink-0">
                <OperatingDevice name={live.deviceName} />
              </div>
            )}
          </div>
        </div>
      </article>
    );
  }

  if (failed) {
    return (
      <article
        className="my-1.5 w-full max-w-2xl overflow-hidden rounded-2xl border border-rose-500/35 bg-rose-500/[0.04] px-3.5 py-2.5 shadow-2xs dark:border-rose-500/30 dark:bg-rose-950/20"
        data-execution-id={id}
        data-execution-status={status}
      >
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex min-w-0 flex-wrap items-center gap-2 text-xs">
            <span className="grid size-5 shrink-0 place-items-center rounded-full bg-rose-500/15 text-rose-600 dark:bg-rose-500/25 dark:text-rose-400">
              {status === "expired" ? (
                <Clock className="size-3.5" />
              ) : (
                <AlertCircle className="size-3.5" />
              )}
            </span>
            <span className="font-semibold text-rose-700 dark:text-rose-300">
              {t(attemptLabels[status] || status)}
            </span>
            {retried && (
              <span className="text-[11px] font-normal text-rose-600/80 dark:text-rose-300/80">
                · {t("重试")}
              </span>
            )}
            <RunDuration
              startedAt={runningStartedAt}
              finishedAt={execution?.finished_at}
              active={false}
              onClick={onOpenTrace && id ? () => onOpenTrace(id) : undefined}
            />
          </div>
          {execution?.can_retry && execution.id && (
            <BubbleRetryMenu
              replyModel={execution.model}
              cardKind={cardKind}
              cardPreferredRouteID={cardPreferredRouteID}
              align="end"
              loading={retryingAttemptID === execution.id}
              disabled={Boolean(
                retryingAttemptID && retryingAttemptID !== execution.id,
              )}
              onConfirmRetry={(preferredModelRouteID) =>
                onRetryAttempt(execution.id, preferredModelRouteID)
              }
              renderTrigger={({ onClick, disabled, title, icon }) => (
                <Button
                  variant="ghost"
                  size="xs"
                  disabled={disabled}
                  title={title}
                  onClick={onClick}
                  className="h-6 text-rose-700 hover:bg-rose-500/10 hover:text-rose-800 dark:text-rose-300 dark:hover:bg-rose-500/20"
                >
                  <span className="[&>svg]:size-3">{icon}</span>
                  {t("重试")}
                </Button>
              )}
            />
          )}
        </div>
        {summary && (
          <div className="mt-2 border-t border-rose-500/15 pt-2 text-xs leading-relaxed text-secondary-foreground dark:border-rose-500/20">
            <RichText content={summary} />
          </div>
        )}
      </article>
    );
  }

  // 无正文的 cancelled / superseded，以及静默 completed / queued，统一渲染为单行浅灰圆角小胶囊。
  const capsuleLabel = execution
    ? t(resolveAttemptStatusLabel(execution))
    : t(attemptLabels[status] || status);
  const dotClass =
    status === "completed"
      ? "bg-emerald-500"
      : status === "queued"
        ? "bg-sky-500"
        : "bg-slate-400 dark:bg-slate-500";

  return (
    <div
      className="flex justify-center py-0.5 text-xs"
      data-execution-id={id}
      data-execution-status={status}
    >
      <span className="inline-flex max-w-[92%] flex-wrap items-center justify-center gap-x-2 gap-y-1 rounded-full border border-line/50 bg-muted/20 px-2.5 py-0.5 text-[11px] text-muted-foreground dark:bg-muted/10">
        <span className={`size-1.5 shrink-0 rounded-full ${dotClass}`} />
        <span>{capsuleLabel}</span>
        {retried && <span>· {t("重试")}</span>}
        <RunDuration
          startedAt={runningStartedAt}
          finishedAt={execution?.finished_at}
          active={false}
          onClick={onOpenTrace && id ? () => onOpenTrace(id) : undefined}
        />
        {execution?.can_retry && execution.id && (
          <BubbleRetryMenu
            replyModel={execution.model}
            cardKind={cardKind}
            cardPreferredRouteID={cardPreferredRouteID}
            align="end"
            loading={retryingAttemptID === execution.id}
            disabled={Boolean(
              retryingAttemptID && retryingAttemptID !== execution.id,
            )}
            onConfirmRetry={(preferredModelRouteID) =>
              onRetryAttempt(execution.id, preferredModelRouteID)
            }
            renderTrigger={({ onClick, disabled, title, icon }) => (
              <button
                type="button"
                disabled={disabled}
                title={title}
                onClick={onClick}
                className="inline-flex cursor-pointer items-center gap-1 font-medium text-sky-700 hover:underline disabled:cursor-not-allowed disabled:opacity-50 dark:text-sky-400"
              >
                <span className="[&>svg]:size-3">{icon}</span>
                {t("重试")}
              </button>
            )}
          />
        )}
      </span>
    </div>
  );
}

type ExecutionActions = {
  live?: LiveAttempt | null;
  cardKind?: CardKind;
  cardPreferredRouteID?: string | null;
  retryingAttemptID?: string;
  onRetryAttempt: (
    attemptID: string,
    preferredModelRouteID?: string | null,
  ) => void;
  onOpenTrace?: (attemptID: string) => void;
};

export function ExecutionStatus({
  attempts,
  showSender = true,
  ...actions
}: { attempts: ExecutionAttempt[]; showSender?: boolean } & ExecutionActions) {
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
            showSender={showSender}
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
