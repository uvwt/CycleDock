import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, Bot } from "lucide-react";
import { useTranslation } from "react-i18next";
import type {
  Attempt,
  PendingTurn,
  TimelineEvent,
} from "../../shared/api/types";
import type { LiveAttempt } from "../../shared/realtime/useCardStream";
import {
  ExecutionStatus,
  RunDuration,
  StandaloneExecution,
} from "./ExecutionStatus";
import { RichText } from "./RichText";
import {
  groupExecutionAttempts,
  orderTimelineEvents,
  type ExecutionAttempt,
} from "./execution";

const TWO_HOURS_MS = 2 * 60 * 60 * 1000;
const systemTypes = new Set([
  "status_change",
  "progress",
  "checkin",
  "confirm",
  "reject",
]);
const labels: Record<string, string> = {
  status_change: "状态变化",
  progress: "阶段进度",
  checkin: "巡检",
  confirm: "提交审核",
  reject: "驳回",
};

type ExecutionActions = {
  cancelingTurnID?: string;
  retryingAttemptID?: string;
  onCancelTurn: (turnID: string) => void;
  onRetryAttempt: (attemptID: string) => void;
};

function TimeSeparator({ value }: { value: string }) {
  const { i18n } = useTranslation();
  const time = new Date(value);
  return (
    <div
      role="separator"
      data-time-separator
      className="flex items-center justify-center py-1 text-[11px] text-slate-400"
    >
      {time.toLocaleString(i18n.language === "en" ? "en-US" : "zh-CN", {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      })}
    </div>
  );
}

function EventRow({
  event,
  attempts,
  completedAttempt,
  live,
  ...actions
}: {
  event: TimelineEvent;
  attempts: ExecutionAttempt[];
  completedAttempt?: Attempt;
  live: LiveAttempt | null;
} & ExecutionActions) {
  const { t } = useTranslation();
  const system = systemTypes.has(event.type);
  const human = event.actor_kind === "human";
  if (system) {
    return (
      <>
        <div
          id={"event-" + event.id}
          className="flex justify-center py-1"
          data-sequence={event.sequence}
        >
          <span
            className={`max-w-[90%] px-2 py-0.5 text-center text-xs ${
              event.type === "reject"
                ? "text-rose-700"
                : event.type === "confirm"
                  ? "text-amber-700"
                  : "text-slate-500"
            }`}
          >
            {t(labels[event.type] || event.type)} · {event.content}
            {event.checkin_status ? ` · ${event.checkin_status}` : ""}
          </span>
        </div>
        <ExecutionStatus attempts={attempts} live={live} {...actions} />
      </>
    );
  }

  return (
    <>
      <article
        id={"event-" + event.id}
        data-sequence={event.sequence}
        className={`flex gap-3 ${human ? "justify-end" : "justify-start"}`}
      >
        {!human && (
          <span
            aria-hidden
            className="grid size-9 shrink-0 place-items-center rounded-full bg-slate-100 text-slate-600"
          >
            <Bot className="size-4" />
          </span>
        )}
        <div className="min-w-0 max-w-[min(88%,740px)]">
          {!human && (
            <p className="mb-1.5 text-xs font-semibold text-slate-500">
              {event.actor_name || "Cycle Agent"}
              {event.type === "finding" && (
                <span className="ml-2 text-sky-700">{t("发现")}</span>
              )}
              {event.type === "result" && (
                <span className="ml-2 text-emerald-700">{t("结果")}</span>
              )}
            </p>
          )}
          <div
            className={
              human
                ? "rounded-xl rounded-br-sm bg-sky-50 px-4 py-3"
                : undefined
            }
          >
            <RichText content={event.content} />
            {completedAttempt && (
              <p
                data-execution-id={completedAttempt.id}
                className="mt-2 text-[11px] text-slate-400"
              >
                {t("执行完成")}
                <span className="ml-2 inline-flex">
                  <RunDuration
                    startedAt={completedAttempt.started_at}
                    finishedAt={completedAttempt.finished_at}
                    active={false}
                  />
                </span>
                {completedAttempt.retry_of_attempt_id ||
                completedAttempt.reason === "retry"
                  ? " · " + t("重试")
                  : ""}
              </p>
            )}
          </div>
        </div>
      </article>
      <ExecutionStatus attempts={attempts} live={live} {...actions} />
    </>
  );
}

function LiveMessage({ live }: { live: LiveAttempt | null }) {
  const { t } = useTranslation();
  if (!live) return null;
  return (
    <div className="flex gap-3">
      <span className="grid size-9 shrink-0 place-items-center rounded-full bg-slate-100">
        <Bot className="size-4" />
      </span>
      <div className="min-w-0 max-w-[min(88%,740px)]">
        <div className="mb-1.5 flex items-center justify-between gap-3 text-xs font-semibold text-slate-500">
          <span>Cycle Agent · {live.stage || t("正在执行")}</span>
          <RunDuration startedAt={live.startedAt} active />
        </div>
        <div>
          {live.text ? (
            <RichText content={live.text} />
          ) : (
            <div
              aria-label={t("Agent 正在工作")}
              className="flex h-7 items-center gap-1"
            >
              <span className="live-dot size-1.5 rounded-full bg-slate-500" />
              <span className="live-dot size-1.5 rounded-full bg-slate-500" />
              <span className="live-dot size-1.5 rounded-full bg-slate-500" />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

export function ConversationView({
  events,
  live,
  cardKind,
  pendingTurns,
  currentAttempt,
  attemptHistory,
  ...actions
}: {
  events: TimelineEvent[];
  live: LiveAttempt | null;
  cardKind: string;
  pendingTurns: PendingTurn[];
  currentAttempt: Attempt | null;
  attemptHistory: Attempt[];
} & ExecutionActions) {
  const { t } = useTranslation();
  const bottom = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [showLatest, setShowLatest] = useState(false);

  const sortedEvents = useMemo(() => orderTimelineEvents(events), [events]);
  const { byEvent, standalone, completedByResult } = useMemo(
    () =>
      groupExecutionAttempts(
        sortedEvents,
        pendingTurns,
        currentAttempt,
        attemptHistory,
      ),
    [attemptHistory, currentAttempt, sortedEvents, pendingTurns],
  );

  useEffect(() => {
    if (stick.current) bottom.current?.scrollIntoView({ block: "end" });
  }, [events, live, pendingTurns, currentAttempt, attemptHistory]);

  function onScroll() {
    const element = list.current;
    if (!element) return;
    const nearBottom =
      element.scrollHeight - element.scrollTop - element.clientHeight < 120;
    stick.current = nearBottom;
    setShowLatest(!nearBottom);
  }

  return (
    <div
      ref={list}
      onScroll={onScroll}
      className="scrollbar-thin relative min-h-0 flex-1 overflow-y-auto px-4 py-7 sm:px-6"
      role="log"
      aria-label={t("统一消息时间线")}
    >
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        {!sortedEvents.length && !live && !standalone.length && (
          <div className="py-20 text-center">
            <div className="mx-auto mb-5 grid size-16 place-items-center rounded-2xl bg-slate-100 text-slate-400">
              <Bot className="size-8" />
            </div>
            <h2 className="text-lg font-semibold">
              {cardKind === "discussion" ? t("从第一句话开始") : t("开始协作")}
            </h2>
            <p className="mt-2 text-sm text-slate-500">
              {t("你的消息、Agent 结果和任务状态都会出现在这里。")}
            </p>
          </div>
        )}

        {sortedEvents.map((event, index) => {
          const previous = sortedEvents[index - 1];
          const showTime =
            !!previous &&
            new Date(event.created_at).getTime() -
              new Date(previous.created_at).getTime() >
              TWO_HOURS_MS;
          return (
            <div key={event.id} className="contents">
              {showTime && <TimeSeparator value={event.created_at} />}
              <EventRow
                event={event}
                attempts={byEvent.get(event.id) || []}
                completedAttempt={completedByResult.get(event.id)}
                live={live}
                {...actions}
              />
            </div>
          );
        })}

        <StandaloneExecution attempts={standalone} live={live} {...actions} />
        <LiveMessage live={live?.text ? live : null} />
        <div ref={bottom} className="h-1" />
      </div>

      {showLatest && (
        <div className="sticky bottom-3 flex justify-center">
          <button
            type="button"
            title={t("跳到最新")}
            aria-label={t("跳到最新")}
            onClick={() => {
              bottom.current?.scrollIntoView({
                behavior: "smooth",
                block: "end",
              });
              stick.current = true;
              setShowLatest(false);
            }}
            className="grid size-9 place-items-center rounded-full border border-slate-200 bg-white text-slate-600 shadow-sm transition hover:bg-slate-50"
          >
            <ArrowDown className="size-4" />
          </button>
        </div>
      )}
    </div>
  );
}
