import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, Bot } from "lucide-react";
import { useTranslation } from "react-i18next";
import type {
  Attempt,
  PendingTurn,
  TimelineEvent,
} from "../../shared/api/types";
import type { LiveReply } from "../../shared/realtime/cardStreamState";
import type { LiveAttempt } from "../../shared/realtime/useCardStream";
import {
  ExecutionStatus,
  RunDuration,
  StandaloneExecution,
} from "./ExecutionStatus";
import { RichText } from "./RichText";
import { usePacedTimeline } from "./usePacedTimeline";
import {
  groupExecutionAttempts,
  orderTimelineEvents,
  type ExecutionAttempt,
} from "./execution";

const FIVE_MINUTES_MS = 5 * 60 * 1000;
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
  onChoose: (choice: {
    value: string;
    attemptID: string;
    messageID: string;
  }) => void;
  choicePending?: boolean;
  waitingAttemptID?: string;
};

function TimeSeparator({ value }: { value: string }) {
  const { i18n } = useTranslation();
  const time = new Date(value);
  return (
    <div
      role="separator"
      data-time-separator
      className="flex items-center justify-center py-1 text-[11px] text-faint"
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
  completedAttempts,
  showSender,
  preview,
  live,
  ...actions
}: {
  event: TimelineEvent;
  attempts: ExecutionAttempt[];
  completedAttempts: Attempt[];
  showSender: boolean;
  preview?: LiveReply;
  live: LiveAttempt | null;
} & ExecutionActions) {
  const { t } = useTranslation();
  const system = systemTypes.has(event.type);
  const human = event.actor_kind === "human";
  const message = event.type === "message" ? event.message : undefined;
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
                ? "text-danger"
                : event.type === "confirm"
                  ? "text-warning"
                  : "text-muted-foreground"
            }`}
          >
            {t(labels[event.type] || event.type)} · {event.content}
            {event.checkin_status ? ` · ${event.checkin_status}` : ""}
          </span>
        </div>
        {attempts.length > 0 && (
          <div className="mt-3">
            <ExecutionStatus attempts={attempts} live={live} {...actions} />
          </div>
        )}
      </>
    );
  }

  return (
    <>
      <article
        id={"event-" + event.id}
        data-sequence={event.sequence}
        data-message-group-start={showSender}
        className={`flex gap-3.5 ${human ? "justify-end" : "justify-start"} ${!human && !showSender ? "pl-[46px]" : ""}`}
      >
        {showSender && !human && (
          <span
            aria-hidden
            className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-full bg-muted text-secondary-foreground"
          >
            <Bot className="size-4" />
          </span>
        )}
        <div className="min-w-0 max-w-[78%]">
          {showSender && !human && (
            <p className="mb-1.5 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs font-semibold text-muted-foreground">
              <span>
                {event.actor_name === "Cycle Agent"
                  ? "Cycle"
                  : event.actor_name || "Cycle"}
              </span>
              {preview && !live?.finished && (
                <RunDuration startedAt={live?.startedAt} active />
              )}
              {completedAttempts.map((completedAttempt) => (
                <span
                  key={completedAttempt.id}
                  data-execution-id={completedAttempt.id}
                  className="inline-flex items-center gap-2 text-[11px] font-normal text-faint"
                >
                  {t("执行完成")}
                  <span className="inline-flex">
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
                </span>
              ))}
            </p>
          )}
          <div
            className={`w-fit max-w-full rounded-2xl px-4 py-2.5 ${human ? "ml-auto bg-sky-50" : "bg-muted"}`}
          >
            {(event.type === "finding" || event.type === "result") && (
              <p
                className={`mb-1 text-xs ${event.type === "finding" ? "text-sky-700" : "text-emerald-700"}`}
              >
                {t(event.type === "finding" ? "发现" : "结果")}
              </p>
            )}
            {message?.type === "choice" && message.widget ? (
              <div className="space-y-3">
                {message.content && <RichText content={message.content} />}
                <p className="text-base font-medium text-heading">
                  {message.widget.question}
                </p>
                <div className="flex flex-col gap-2">
                  {message.widget.options.map((option) => (
                    <button
                      key={option.value}
                      type="button"
                      disabled={
                        !event.attempt_id ||
                        actions.choicePending ||
                        actions.waitingAttemptID !== event.attempt_id
                      }
                      onClick={() => {
                        if (event.attempt_id) {
                          actions.onChoose({
                            value: option.value,
                            attemptID: event.attempt_id,
                            messageID: event.id,
                          });
                        }
                      }}
                      className="rounded-lg border border-line bg-surface px-3 py-2 text-left text-sm text-foreground transition hover:border-sky-400 hover:bg-sky-50 disabled:cursor-default disabled:opacity-50"
                    >
                      {option.label}
                    </button>
                  ))}
                </div>
              </div>
            ) : (
              (message?.content || event.content) && (
                <RichText content={message?.content || event.content} />
              )
            )}
            {preview?.failed && (
              <p className="mt-1 text-xs text-danger">{t("消息未发送")}</p>
            )}
            {message?.type === "attachment" && message.url && (
              <a
                href={message.url}
                target="_blank"
                rel="noopener noreferrer"
                className="mt-2 block break-all text-sm text-sky-700 underline underline-offset-2"
              >
                {message.alt || message.url}
              </a>
            )}
            {message?.images && message.images.length > 0 && (
              <div className="mt-3 grid grid-cols-2 gap-2">
                {message.images.map((src) => (
                  <a
                    key={src}
                    href={src}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    <img
                      src={src}
                      alt={message.alt || t("消息图片")}
                      loading="lazy"
                      referrerPolicy="no-referrer"
                      className="max-h-72 w-full rounded-lg object-cover"
                    />
                  </a>
                ))}
              </div>
            )}
          </div>
        </div>
      </article>
      {attempts.length > 0 && (
        <div className="mt-3">
          <ExecutionStatus attempts={attempts} live={live} {...actions} />
        </div>
      )}
    </>
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

  const orderedEvents = useMemo(() => orderTimelineEvents(events), [events]);
  // 预览转正式消息后仍保留原 key；SSE 重连或下一轮开始也不能重建已显示的气泡。
  const [previewKeys, setPreviewKeys] = useState(
    () => new Map<string, string>(),
  );
  const newBindings = (live?.replies || []).filter(
    (reply) => reply.eventID && !previewKeys.has(reply.eventID),
  );
  if (newBindings.length > 0) {
    const nextKeys = new Map(previewKeys);
    for (const reply of newBindings) nextKeys.set(reply.eventID!, reply.id);
    setPreviewKeys(nextKeys);
  }
  const revealThrough = orderedEvents.reduce(
    (sequence, event) =>
      previewKeys.has(event.id) ? Math.max(sequence, event.sequence) : sequence,
    0,
  );
  const { visibleEvents: sortedEvents, historyThrough } = usePacedTimeline(
    orderedEvents,
    revealThrough,
  );
  const pendingReplies = (live?.replies || []).filter(
    (reply) =>
      !reply.eventID ||
      !orderedEvents.some((event) => event.id === reply.eventID),
  );
  const sender = orderedEvents.findLast(
    (event) =>
      event.attempt_id === live?.attemptID && event.actor_kind !== "human",
  );
  const rows = [
    ...sortedEvents.map((event) => ({
      event,
      preview: undefined as LiveReply | undefined,
      key: previewKeys.get(event.id) || event.id,
    })),
    ...pendingReplies.map((preview, index) => ({
      key: preview.id,
      preview,
      event: {
        id: preview.eventID || preview.id,
        sequence: (orderedEvents.at(-1)?.sequence || 0) + index + 1,
        attempt_id: live!.attemptID,
        type: "message" as const,
        actor_kind: sender?.actor_kind || "cycle_agent",
        actor_name: sender?.actor_name || "Cycle",
        content: preview.text,
        created_at: sender?.created_at || live?.startedAt || "",
      },
    })),
  ];
  const displayEvents = rows.map((row) => row.event);
  const displayLive = live ? { ...live, replies: pendingReplies } : null;
  const { byEvent, standalone, completedByResult } = useMemo(
    () =>
      groupExecutionAttempts(
        orderedEvents,
        pendingTurns,
        currentAttempt,
        attemptHistory,
      ),
    [attemptHistory, currentAttempt, orderedEvents, pendingTurns],
  );

  const groupStarts = new Set<string>();
  const completedByHeader = new Map<string, Attempt[]>();
  let headerID = "";
  for (const [index, event] of displayEvents.entries()) {
    const previous = displayEvents[index - 1];
    const interval = previous
      ? Date.parse(event.created_at) - Date.parse(previous.created_at)
      : NaN;
    // 完成信息归到消息组首，避免在连续回复中重复展示发送者。
    const continuesGroup =
      !!previous &&
      !systemTypes.has(previous.type) &&
      !systemTypes.has(event.type) &&
      !byEvent.get(previous.id)?.some((attempt) => attempt.question) &&
      (!(
        previewKeys.has(event.id) ||
        (live && event.attempt_id === live.attemptID)
      ) ||
        previous.attempt_id === event.attempt_id) &&
      previous.actor_kind === event.actor_kind &&
      previous.actor_name === event.actor_name &&
      interval >= 0 &&
      interval <= FIVE_MINUTES_MS;
    if (!continuesGroup) {
      headerID = event.id;
      groupStarts.add(headerID);
    }
    const completed = completedByResult.get(event.id);
    if (completed) {
      const attempts = completedByHeader.get(headerID) || [];
      attempts.push(completed);
      completedByHeader.set(headerID, attempts);
    }
  }

  useEffect(() => {
    if (stick.current) bottom.current?.scrollIntoView({ block: "end" });
  }, [displayEvents, live, pendingTurns, currentAttempt, attemptHistory]);

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
      className="scrollbar-thin relative min-h-0 flex-1 overflow-y-auto px-4 pt-3.5 pb-1 sm:px-6"
      role="log"
      aria-label={t("统一消息时间线")}
    >
      <div className="mx-auto flex max-w-3xl flex-col">
        {!sortedEvents.length && !live && !standalone.length && (
          <div className="py-20 text-center">
            <div className="mx-auto mb-5 grid size-16 place-items-center rounded-2xl bg-muted text-faint">
              <Bot className="size-8" />
            </div>
            <h2 className="text-lg font-semibold">
              {cardKind === "discussion" ? t("从第一句话开始") : t("开始协作")}
            </h2>
            <p className="mt-2 text-sm text-muted-foreground">
              {t("你的消息、Agent 结果和任务状态都会出现在这里。")}
            </p>
          </div>
        )}

        {rows.map(({ event, preview, key }, index) => {
          const previous = displayEvents[index - 1];
          const showTime =
            !!previous &&
            new Date(event.created_at).getTime() -
              new Date(previous.created_at).getTime() >
              TWO_HOURS_MS;
          const continuesGroup = !groupStarts.has(event.id);
          return (
            <div
              key={key}
              className={`${!preview && !previewKeys.has(event.id) && event.sequence > historyThrough ? "message-enter" : ""} ${index === 0 ? "" : continuesGroup ? "mt-1.5" : "mt-3.5"}`}
            >
              {showTime && <TimeSeparator value={event.created_at} />}
              <EventRow
                event={event}
                preview={preview}
                showSender={!continuesGroup}
                attempts={byEvent.get(event.id) || []}
                completedAttempts={completedByHeader.get(event.id) || []}
                live={displayLive}
                {...actions}
              />
            </div>
          );
        })}

        {standalone.length > 0 && (
          <div className="mt-3">
            <StandaloneExecution
              attempts={standalone}
              live={displayLive}
              {...actions}
            />
          </div>
        )}
        <div ref={bottom} className="h-3 shrink-0" />
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
            className="grid size-9 place-items-center rounded-full border border-line bg-surface text-secondary-foreground shadow-sm transition hover:bg-canvas"
          >
            <ArrowDown className="size-4" />
          </button>
        </div>
      )}
    </div>
  );
}
