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
  AttemptMetadata,
  ExecutionStatus,
  OperatingDevice,
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

/** 气泡上的发送者。内置 Agent 的来源名是 Cycle Agent，界面统一写成 Cycle。 */
function senderLabel(actorKind: string, actorName?: string) {
  if (actorKind === "human") return actorName || "";
  if (!actorName || actorName === "Cycle Agent") return "Cycle";
  return actorName;
}

type ExecutionActions = {
  retryingAttemptID?: string;
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
  headerAttempts,
  showSender,
  preview,
  previewStatus,
  live,
  ...actions
}: {
  event: TimelineEvent;
  attempts: ExecutionAttempt[];
  headerAttempts: Attempt[];
  showSender: boolean;
  preview?: LiveReply;
  previewStatus?: string;
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
              <span>{senderLabel(event.actor_kind, event.actor_name)}</span>
              {preview &&
                !live?.finished &&
                event.attempt_id === live?.attemptID &&
                !headerAttempts.length && (
                  <>
                    <RunDuration startedAt={live?.startedAt} active />
                    {live.deviceName && (
                      <OperatingDevice name={live.deviceName} />
                    )}
                  </>
                )}
              {preview?.failed &&
                !["cancelled", "superseded"].includes(previewStatus || "") && (
                  <span className="text-[11px] font-normal text-faint">
                    {t("消息未发送")}
                  </span>
                )}
              {headerAttempts.map((attempt) => (
                <AttemptMetadata
                  key={attempt.id}
                  attempt={attempt}
                  deviceName={
                    !live?.finished &&
                    live?.deviceName &&
                    live.attemptID === attempt.id
                      ? live.deviceName
                      : undefined
                  }
                  {...actions}
                />
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

  // 只有已经被领取的输入进入对话，排队输入由输入框上方的列表承载。
  const orderedEvents = useMemo(() => {
    const pendingEventIDs = new Set(
      pendingTurns.map((turn) => turn.source_event_id),
    );
    return orderTimelineEvents(events).filter(
      (event) => !pendingEventIDs.has(event.id),
    );
  }, [events, pendingTurns]);
  const outputs = [...(live?.previous || []), ...(live ? [live] : [])];
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
      previewKeys.has(event.id) ||
      outputs.some(
        (output) =>
          output.attemptID === event.attempt_id &&
          output.replies.some(
            (reply) => reply.id === event.message?.preview_id,
          ),
      )
        ? Math.max(sequence, event.sequence)
        : sequence,
    0,
  );
  const { visibleEvents: sortedEvents, historyThrough } = usePacedTimeline(
    orderedEvents,
    revealThrough,
  );
  const pendingReplies = (live?.replies || []).filter(
    (reply) =>
      !orderedEvents.some(
        (event) =>
          event.id === reply.eventID || event.message?.preview_id === reply.id,
      ),
  );
  const rows = sortedEvents.map((event) => {
    const preview = event.message?.preview
      ? outputs
          .find((output) => output.attemptID === event.attempt_id)
          ?.replies.find((reply) => reply.id === event.message?.preview_id)
      : undefined;
    // REST 快照承载身份和刷新恢复，SSE 在同一气泡上补齐最新正文。
    return {
      event:
        preview &&
        event.message &&
        preview.text.startsWith(event.message.content || event.content)
          ? {
              ...event,
              content: preview.text,
              message: { ...event.message, content: preview.text },
            }
          : event,
      preview,
      key: event.message?.preview_id || previewKeys.get(event.id) || event.id,
    };
  });
  const executions = [
    ...attemptHistory,
    ...(currentAttempt ? [currentAttempt] : []),
  ];
  // 旧预览留在原执行的位置；新一轮的正文和运行气泡仍显示在时间线末尾。
  for (const output of outputs) {
    const sender = orderedEvents.findLast(
      (event) =>
        event.attempt_id === output.attemptID && event.actor_kind !== "human",
    );
    const execution = executions.find(
      (attempt) => attempt.id === output.attemptID,
    );
    const replies = output.replies.filter(
      (reply) =>
        !orderedEvents.some(
          (event) =>
            event.id === reply.eventID ||
            event.message?.preview_id === reply.id,
        ),
    );
    const historical =
      output.attemptID !== live?.attemptID ||
      ["cancelled", "superseded"].includes(execution?.status || "");
    const anchor = rows.findLastIndex(
      ({ event }) =>
        event.attempt_id === output.attemptID ||
        event.id === execution?.source_event_id,
    );
    const insertAt = historical && anchor >= 0 ? anchor + 1 : rows.length;
    rows.splice(
      insertAt,
      0,
      ...replies.map((preview, index) => ({
        key: preview.id,
        preview,
        event: {
          id: preview.eventID || preview.id,
          sequence: (rows[insertAt - 1]?.event.sequence || 0) + index + 1,
          attempt_id: output.attemptID,
          type: "message" as const,
          actor_kind: sender?.actor_kind || "cycle_agent",
          actor_name: sender?.actor_name || "Cycle",
          content: preview.text,
          created_at: output.startedAt || execution?.started_at || "",
        },
      })),
    );
  }
  const displayEvents = rows.map((row) => row.event);
  const displayLive = live ? { ...live, replies: pendingReplies } : null;
  const { byEvent, standalone, statusByReply, running } =
    groupExecutionAttempts(displayEvents, currentAttempt, attemptHistory);

  const groupStarts = new Set<string>();
  const statusByHeader = new Map<string, Attempt[]>();
  let headerID = "";
  for (const [index, event] of displayEvents.entries()) {
    const previous = displayEvents[index - 1];
    const interval = previous
      ? Date.parse(event.created_at) - Date.parse(previous.created_at)
      : NaN;
    // 执行信息归到消息组首，避免在连续回复中重复展示发送者。
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
      senderLabel(previous.actor_kind, previous.actor_name) ===
        senderLabel(event.actor_kind, event.actor_name) &&
      interval >= 0 &&
      interval <= FIVE_MINUTES_MS;
    if (!continuesGroup) {
      headerID = event.id;
      groupStarts.add(headerID);
    }
    const execution = statusByReply.get(event.id);
    if (execution) {
      const attempts = statusByHeader.get(headerID) || [];
      attempts.push(execution);
      statusByHeader.set(headerID, attempts);
    }
  }
  const lastEvent = displayEvents.at(-1);
  // 运行圆点是当前 Attempt 的下一条气泡。紧挨着本轮 Cycle 回复时不再重复头像和名称。
  // 必须是同一 Attempt：新的一轮即使发送者仍是 Cycle，也要重新带头像。
  const runningContinues = Boolean(
    running &&
      lastEvent &&
      !systemTypes.has(lastEvent.type) &&
      lastEvent.actor_kind !== "human" &&
      lastEvent.attempt_id === running.value.id &&
      senderLabel(lastEvent.actor_kind, lastEvent.actor_name) === "Cycle",
  );

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
        {!sortedEvents.length &&
          !live &&
          !standalone.length &&
          !running &&
          !pendingTurns.length && (
            <div className="py-20 text-center">
              <div className="mx-auto mb-5 grid size-16 place-items-center rounded-2xl bg-muted text-faint">
                <Bot className="size-8" />
              </div>
              <h2 className="text-lg font-semibold">
                {cardKind === "discussion"
                  ? t("从第一句话开始")
                  : t("开始协作")}
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
                previewStatus={
                  executions.find((attempt) => attempt.id === event.attempt_id)
                    ?.status
                }
                showSender={!continuesGroup}
                attempts={byEvent.get(event.id) || []}
                headerAttempts={statusByHeader.get(event.id) || []}
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
        {running && (
          <div className={runningContinues ? "mt-1.5" : "mt-3"}>
            <ExecutionStatus
              attempts={[running]}
              live={displayLive}
              showSender={!runningContinues}
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
