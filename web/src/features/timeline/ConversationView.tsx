import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowDown, Bot, Check, Clock, Copy, MoreHorizontal, Reply, RotateCcw } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../../shared/ui/dropdown-menu";
import type {
  Attempt,
  PendingTurn,
  TimelineEvent,
} from "../../shared/api/types";
import type { LiveReply } from "../../shared/realtime/cardStreamState";
import type { LiveAttempt } from "../../shared/realtime/useCardStream";
import {
  ChatBubble,
  ChatBubbleAction,
  ChatBubbleActionWrapper,
  ChatBubbleMessage,
  ChatBubbleTimestamp,
  ChatMessageList,
} from "../../shared/ui/chat";
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

function formatEventContent(event: TimelineEvent): string {
  const message = event.type === "message" ? event.message : undefined;
  if (message?.type === "choice" && message.widget) {
    const parts: string[] = [];
    if (message.content?.trim()) parts.push(message.content.trim());
    if (message.widget.question?.trim()) parts.push(message.widget.question.trim());
    if (message.widget.options?.length) {
      parts.push(
        message.widget.options
          .map((opt, i) => `${i + 1}. ${opt.label || opt.value}`)
          .join("\n"),
      );
    }
    return parts.join("\n\n");
  }
  return message?.content || event.content || "";
}

/** 气泡上的发送者。内置 Agent 的来源名是 Cycle Agent，界面统一写成 Cycle。 */
function senderLabel(actorKind: string, actorName?: string) {
  if (actorKind === "human") return actorName || "";
  if (!actorName || actorName === "Cycle Agent") return "Cycle";
  return actorName;
}

export type QuotedMessage = {
  id: string;
  sender: string;
  text: string;
};

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
  onReply?: (quote: QuotedMessage) => void;
};

function TimeSeparator({ value }: { value: string }) {
  const { i18n } = useTranslation();
  const time = new Date(value);
  return (
    <div
      role="separator"
      data-time-separator
      className="flex items-center justify-center py-2 text-[11px]"
    >
      <span className="rounded-md border border-line/60 bg-surface px-3 py-1 font-medium text-muted-foreground shadow-xs">
        {time.toLocaleString(i18n.language === "en" ? "en-US" : "zh-CN", {
          month: "short",
          day: "numeric",
          hour: "2-digit",
          minute: "2-digit",
        })}
      </span>
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
  isLatest = false,
  ...actions
}: {
  event: TimelineEvent;
  attempts: ExecutionAttempt[];
  headerAttempts: Attempt[];
  showSender: boolean;
  preview?: LiveReply;
  previewStatus?: string;
  live: LiveAttempt | null;
  isLatest?: boolean;
} & ExecutionActions) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const [showTime, setShowTime] = useState(false);
  const system = systemTypes.has(event.type);
  const human = event.actor_kind === "human";
  if (event.type === "notice") {
    const parts = event.content.split("\u001f");
    const kindLabel =
      parts[0] === "discussion"
        ? t("讨论")
        : parts[0] === "ongoing"
          ? t("计划")
          : t("工作");
    const title = parts[1] || t("新消息");
    const preview = parts[2] || "";
    const href = parts[3] || "";
    return (
      <div
        id={"event-" + event.id}
        className="flex justify-center py-1.5"
        data-sequence={event.sequence}
      >
        <div className="w-full max-w-[90%] rounded-xl border border-line/70 bg-surface px-3 py-2 text-left text-xs shadow-xs">
          <p className="font-medium text-foreground">
            {parts.length >= 4 ? `${kindLabel} · ${title}` : event.content}
          </p>
          {parts.length >= 4 && preview && (
            <p className="mt-1 whitespace-pre-wrap text-muted-foreground">
              {preview}
            </p>
          )}
          {href.startsWith("/") && (
            <Link
              to={href}
              className="mt-1.5 inline-block font-medium text-primary"
            >
              {t("打开这张卡")}
            </Link>
          )}
        </div>
      </div>
    );
  }
  const message = event.type === "message" ? event.message : undefined;
  if (system) {
    return (
      <>
        <div
          id={"event-" + event.id}
          className="flex justify-center py-1.5"
          data-sequence={event.sequence}
        >
          <span
            className={`max-w-[90%] rounded-md border border-line/60 bg-surface px-3 py-1 text-center text-xs shadow-xs ${
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

  const formattedTime = event.created_at
    ? new Date(event.created_at).toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
      })
    : "";

  const fullTime = event.created_at
    ? new Date(event.created_at).toLocaleString([], {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      })
    : "";

  const hasMetadata = Boolean(
    headerAttempts.length > 0 ||
      (preview &&
        !live?.finished &&
        event.attempt_id === live?.attemptID) ||
      (preview?.failed &&
        !["cancelled", "superseded"].includes(previewStatus || "")),
  );

  const hasActiveAttempt =
    headerAttempts.some((attempt) => attempt.status === "running") ||
    Boolean(
      preview &&
        !live?.finished &&
        event.attempt_id === live?.attemptID,
    );

  const activeDeviceName =
    isLatest &&
    !live?.finished &&
    live?.deviceName &&
    (event.attempt_id === live?.attemptID ||
      headerAttempts.some((attempt) => attempt.id === live?.attemptID))
      ? live.deviceName
      : undefined;

  const rawText = formatEventContent(event);

  async function handleCopy() {
    if (!rawText) return;
    try {
      await navigator.clipboard.writeText(rawText);
      setCopied(true);
      toast.success(t("已复制到剪贴板"));
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error(t("复制失败"));
    }
  }

  function handleReply() {
    if (!rawText || !actions.onReply) return;
    actions.onReply({
      id: event.id,
      sender:
        senderLabel(event.actor_kind, event.actor_name) ||
        (human ? t("你") : "Cycle"),
      text: rawText,
    });
  }

  return (
    <>
      <ChatBubble
        id={"event-" + event.id}
        data-sequence={event.sequence}
        data-message-group-start={showSender}
        variant={human ? "sent" : "received"}
        className={`w-fit max-w-[85%] ${human ? "ml-auto" : ""}`}
      >
        <div
          className={`flex min-w-0 flex-col ${human ? "items-end" : "items-start"}`}
        >
          {showSender && !human && (
            <div className="mb-1.5 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
              <span className="font-semibold text-foreground/80">
                {senderLabel(event.actor_kind, event.actor_name)}
              </span>
              {hasMetadata && (
                <div
                  className={`inline-flex items-center gap-x-2 ${
                    hasActiveAttempt
                      ? ""
                      : "opacity-0 transition-opacity duration-150 group-hover:opacity-100"
                  }`}
                >
                  <span className="select-none text-[10px] text-muted-foreground/40">
                    •
                  </span>
                  <div className="inline-flex items-center gap-1.5 text-[11px] font-normal text-faint">
                    {preview &&
                      !live?.finished &&
                      event.attempt_id === live?.attemptID &&
                      !headerAttempts.length && (
                        <RunDuration startedAt={live?.startedAt} active />
                      )}
                    {preview?.failed &&
                      !["cancelled", "superseded"].includes(
                        previewStatus || "",
                      ) && (
                        <span className="text-[11px] font-normal text-faint">
                          {t("消息未发送")}
                        </span>
                      )}
                    {headerAttempts.map((attempt) => (
                      <AttemptMetadata
                        key={attempt.id}
                        attempt={attempt}
                        {...actions}
                      />
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}

          <div className="flex flex-wrap items-end gap-2.5 max-w-full">
            <ChatBubbleMessage
              variant={human ? "sent" : "received"}
              className="w-fit max-w-full"
            >
            {(event.type === "finding" || event.type === "result") && (
              <p
                className={`mb-1 text-xs font-semibold ${event.type === "finding" ? "text-sky-700 dark:text-sky-400" : "text-emerald-700 dark:text-emerald-400"}`}
              >
                {t(event.type === "finding" ? "发现" : "结果")}
              </p>
            )}
            {rawText && <RichText content={rawText} />}
            {message?.type === "attachment" && message.url && (
              <a
                href={message.url}
                target="_blank"
                rel="noopener noreferrer"
                className="mt-2 block break-all text-sm text-sky-700 underline underline-offset-2 dark:text-sky-400"
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
            {showTime && formattedTime && (
              <ChatBubbleTimestamp
                timestamp={formattedTime}
                status={human ? "read" : undefined}
              />
            )}
          </ChatBubbleMessage>
          {activeDeviceName && (
            <div className="self-end pb-1 shrink-0">
              <OperatingDevice name={activeDeviceName} />
            </div>
          )}
        </div>
      </div>

        {rawText && (
          <ChatBubbleActionWrapper>
            <ChatBubbleAction
              icon={
                copied ? (
                  <Check className="size-3.5 text-primary" />
                ) : (
                  <Copy className="size-3.5" />
                )
              }
              title={copied ? t("已复制") : t("复制全文")}
              aria-label={t("复制全文")}
              onClick={handleCopy}
            />
            {actions.onReply && (
              <ChatBubbleAction
                icon={<Reply className="size-3.5" />}
                title={t("引用回复")}
                aria-label={t("引用回复")}
                onClick={handleReply}
              />
            )}
            {!human && event.attempt_id && actions.onRetryAttempt && (
              <ChatBubbleAction
                icon={<RotateCcw className="size-3.5" />}
                title={t("重新生成")}
                aria-label={t("重新生成")}
                onClick={() => actions.onRetryAttempt(event.attempt_id!)}
              />
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <ChatBubbleAction
                  icon={<MoreHorizontal className="size-3.5" />}
                  title={t("具体时间与更多")}
                  aria-label={t("具体时间与更多")}
                />
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align={human ? "end" : "start"}
                className="w-auto min-w-[170px] p-1.5 shadow-md"
              >
                <div className="flex items-center gap-2 px-2.5 py-1.5 text-xs text-muted-foreground select-none">
                  <Clock className="size-3.5 text-primary shrink-0" />
                  <div className="flex flex-col">
                    <span className="text-[10px] text-faint">
                      {t("具体时间")}
                    </span>
                    <span className="font-semibold text-foreground text-xs">
                      {fullTime || formattedTime}
                    </span>
                  </div>
                </div>
                <DropdownMenuSeparator className="my-1" />
                <DropdownMenuItem
                  onClick={() => setShowTime((v) => !v)}
                  className="text-xs cursor-pointer flex items-center gap-2"
                >
                  <Clock className="size-3.5 text-muted-foreground" />
                  <span>
                    {showTime ? t("隐藏气泡时间") : t("在气泡中显示时间")}
                  </span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </ChatBubbleActionWrapper>
        )}
      </ChatBubble>
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
  bottomSpacerClass = "h-52 shrink-0 sm:h-60",
  ...actions
}: {
  events: TimelineEvent[];
  live: LiveAttempt | null;
  cardKind: string;
  pendingTurns: PendingTurn[];
  currentAttempt: Attempt | null;
  attemptHistory: Attempt[];
  bottomSpacerClass?: string;
} & ExecutionActions) {
  const { t } = useTranslation();
  const bottom = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [showLatest, setShowLatest] = useState(false);

  const executions = useMemo(
    () => [...attemptHistory, ...(currentAttempt ? [currentAttempt] : [])],
    [attemptHistory, currentAttempt],
  );

  // 只有已经被领取的输入进入对话，排队输入由输入框上方的列表承载。
  // 传入 executions 以便按业务轮次交错呈现一问一答，而非按入队时的物理 sequence 扎堆。
  const orderedEvents = useMemo(() => {
    const pendingEventIDs = new Set(
      pendingTurns.map((turn) => turn.source_event_id),
    );
    return orderTimelineEvents(events, executions).filter(
      (event) => !pendingEventIDs.has(event.id),
    );
  }, [events, pendingTurns, executions]);
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
  const { visibleEvents: sortedEvents, historyThrough, historyIDs } =
    usePacedTimeline(orderedEvents, revealThrough);
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

  // 当运行状态紧接着本轮气泡时，将正在执行的时长挂载到当前组首气泡，气泡下方圆点不重复展示孤立时间
  if (runningContinues && running && headerID) {
    const attempts = statusByHeader.get(headerID) || [];
    if (!attempts.some((a) => a.id === running.value.id)) {
      const runningAttempt: Attempt = {
        ...running.value,
        started_at: running.value.started_at || live?.startedAt,
      };
      statusByHeader.set(headerID, [...attempts, runningAttempt]);
    }
  }

  const scrollToBottom = (smooth = false) => {
    const element = list.current;
    if (!element) return;
    if (smooth) {
      element.scrollTo({ top: element.scrollHeight, behavior: "smooth" });
    } else {
      element.scrollTop = element.scrollHeight;
    }
  };

  // 仅在真实新内容到达（而非用户滚动触发的组件重渲染）时，若处于吸底状态则跟随最新内容滚动
  const contentSignature = [
    sortedEvents.length,
    live?.attemptID,
    live?.stage,
    live?.replies?.map((r) => r.text.length).join(","),
    pendingTurns.length,
    currentAttempt?.id,
    currentAttempt?.status,
    attemptHistory.length,
  ].join(":");

  useEffect(() => {
    if (stick.current) {
      scrollToBottom(false);
      const raf = requestAnimationFrame(() => scrollToBottom(false));
      return () => cancelAnimationFrame(raf);
    }
  }, [contentSignature]);

  // 首次挂载或会话切换时，等待富文本和字体渲染稳定后再次对齐底部，防止末尾消息被遮挡
  useEffect(() => {
    scrollToBottom(false);
    const timer = window.setTimeout(() => {
      if (stick.current) {
        scrollToBottom(false);
      }
    }, 60);
    return () => window.clearTimeout(timer);
  }, []);

  function onScroll() {
    const element = list.current;
    if (!element) return;
    const distanceToBottom =
      element.scrollHeight - element.scrollTop - element.clientHeight;
    const nearBottom = distanceToBottom < 100;
    stick.current = nearBottom;
    setShowLatest((prev) => {
      const next = !nearBottom;
      return prev !== next ? next : prev;
    });
  }

  return (
    <div className="relative flex min-h-0 w-full flex-1 flex-col">
      <ChatMessageList
        ref={list}
        onScroll={onScroll}
        aria-label={t("统一消息时间线")}
      >
        {!sortedEvents.length &&
          !live &&
          !standalone.length &&
          !running &&
          !pendingTurns.length && (
            <div className="py-20 text-center">
              <div className="mx-auto mb-5 grid size-16 place-items-center rounded-2xl bg-muted text-faint">
                <Bot className="size-8" />
              </div>
              <h2 className="text-base font-semibold">
                {cardKind === "discussion"
                  ? t("从第一句话开始")
                  : t("开始协作")}
              </h2>
              <p className="mt-1.5 text-xs text-muted-foreground">
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
              className={`${!preview && !previewKeys.has(event.id) && (historyIDs ? !historyIDs.has(event.id) : event.sequence > historyThrough) ? "message-enter" : ""} ${index === 0 ? "" : continuesGroup ? "mt-1" : "mt-2.5"}`}
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
                isLatest={!running && index === rows.length - 1}
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
        <div ref={bottom} className={bottomSpacerClass} />
      </ChatMessageList>

      <div
        className={`pointer-events-none absolute bottom-24 inset-x-0 z-20 flex justify-center transition-all duration-200 ${
          showLatest
            ? "opacity-100 translate-y-0"
            : "opacity-0 translate-y-2 pointer-events-none"
        }`}
      >
        <button
          type="button"
          tabIndex={showLatest ? 0 : -1}
          title={t("跳到最新")}
          aria-label={t("跳到最新")}
          onClick={() => {
            scrollToBottom(true);
            stick.current = true;
            setShowLatest(false);
          }}
          className="pointer-events-auto grid size-9 place-items-center rounded-full border border-line bg-surface text-secondary-foreground shadow-md transition hover:bg-canvas active:scale-95"
        >
          <ArrowDown className="size-4" />
        </button>
      </div>
    </div>
  );
}
