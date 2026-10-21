import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, Bot, Check, Copy } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useTranslation } from "react-i18next";
import type {
  QueueItem,
  Run,
  TimelineEvent,
  WorkItem,
} from "../../shared/api/types";
import type { LiveRun } from "../../shared/realtime/useCardStream";
import { ExecutionStatus, StandaloneExecution } from "./ExecutionStatus";
import { groupExecutionAttempts, type ExecutionAttempt } from "./execution";

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

function RichText({ content }: { content: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  return (
    <div className="prose-chat text-sm text-slate-700">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          pre: ({ children }) => (
            <div className="relative">
              <button
                title={t("复制代码")}
                aria-label={t("复制代码")}
                onClick={async (event) => {
                  const code =
                    event.currentTarget.parentElement?.querySelector("code")
                      ?.textContent || "";
                  await navigator.clipboard.writeText(code);
                  setCopied(true);
                  window.setTimeout(() => setCopied(false), 1400);
                }}
                className="absolute top-2 right-2 z-10 rounded-lg bg-slate-700/80 p-1.5 text-white hover:bg-slate-600"
              >
                {copied ? (
                  <Check className="size-3.5" />
                ) : (
                  <Copy className="size-3.5" />
                )}
              </button>
              <pre>{children}</pre>
            </div>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}

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
  cancelingTriggerID,
  retryingTriggerID,
  onCancelTrigger,
  onRetryTrigger,
}: {
  event: TimelineEvent;
  attempts: ExecutionAttempt[];
  cancelingTriggerID?: string;
  retryingTriggerID?: string;
  onCancelTrigger: (triggerID: string) => void;
  onRetryTrigger: (triggerID: string) => void;
}) {
  const { t } = useTranslation();
  const system = systemTypes.has(event.type);
  const human = event.actor_kind === "human";
  if (system) {
    return (
      <div
        id={"event-" + event.id}
        className="flex justify-center py-2"
        data-sequence={event.sequence}
      >
        <span
          className={`max-w-[90%] rounded-full px-3 py-1 text-center text-xs ${event.type === "reject" ? "bg-rose-50 text-rose-700" : event.type === "confirm" ? "bg-amber-50 text-amber-700" : "text-slate-400"}`}
        >
          {t(labels[event.type] || event.type)} · {event.content}
          {event.checkin_status ? ` · ${event.checkin_status}` : ""}
        </span>
      </div>
    );
  }

  return (
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
      <div
        className={`max-w-[min(88%,740px)] min-w-0 ${human ? "rounded-2xl rounded-br-md bg-sky-50 px-4 py-3" : "py-1"}`}
      >
        {!human && (
          <p className="mb-1 text-xs font-semibold text-slate-500">
            {event.actor_name || "Cycle Agent"}
            {event.type === "finding" && (
              <span className="ml-2 text-sky-700">{t("发现")}</span>
            )}
            {event.type === "result" && (
              <span className="ml-2 text-emerald-700">{t("结果")}</span>
            )}
          </p>
        )}
        <RichText content={event.content} />
        {human && (
          <ExecutionStatus
            attempts={attempts}
            cancelingTriggerID={cancelingTriggerID}
            retryingTriggerID={retryingTriggerID}
            onCancelTrigger={onCancelTrigger}
            onRetryTrigger={onRetryTrigger}
          />
        )}
      </div>
    </article>
  );
}

function LiveMessage({ live }: { live: LiveRun | null }) {
  const { t } = useTranslation();
  if (!live) return null;
  return (
    <div className="flex gap-3">
      <span className="grid size-9 shrink-0 place-items-center rounded-full bg-slate-100">
        <Bot className="size-4" />
      </span>
      <div className="min-w-0 max-w-[min(88%,740px)]">
        <p className="mb-2 text-xs font-semibold text-slate-500">
          Cycle Agent · {live.stage || t("正在执行")}
        </p>
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
  );
}

export function ConversationView({
  events,
  live,
  cardKind,
  queue,
  currentRun,
  workHistory,
  cancelingTriggerID,
  retryingTriggerID,
  onCancelTrigger,
  onRetryTrigger,
}: {
  events: TimelineEvent[];
  live: LiveRun | null;
  cardKind: string;
  queue: QueueItem[];
  currentRun: Run | null;
  workHistory: WorkItem[];
  cancelingTriggerID?: string;
  retryingTriggerID?: string;
  onCancelTrigger: (triggerID: string) => void;
  onRetryTrigger: (triggerID: string) => void;
}) {
  const { t } = useTranslation();
  const bottom = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [showLatest, setShowLatest] = useState(false);
  const sortedEvents = useMemo(
    () => [...events].sort((a, b) => a.sequence - b.sequence),
    [events],
  );
  const eventIDs = useMemo(
    () => new Set(sortedEvents.map((event) => event.id)),
    [sortedEvents],
  );
  const { byEvent, standalone } = useMemo(
    () => groupExecutionAttempts(eventIDs, queue, currentRun, workHistory),
    [currentRun, eventIDs, queue, workHistory],
  );

  useEffect(() => {
    if (stick.current) bottom.current?.scrollIntoView({ block: "end" });
  }, [events, live, queue, currentRun, workHistory]);

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
      <div className="mx-auto flex max-w-3xl flex-col gap-5">
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
                cancelingTriggerID={cancelingTriggerID}
                retryingTriggerID={retryingTriggerID}
                onCancelTrigger={onCancelTrigger}
                onRetryTrigger={onRetryTrigger}
              />
            </div>
          );
        })}
        <StandaloneExecution
          attempts={standalone}
          cancelingTriggerID={cancelingTriggerID}
          retryingTriggerID={retryingTriggerID}
          onCancelTrigger={onCancelTrigger}
          onRetryTrigger={onRetryTrigger}
        />
        <LiveMessage live={live} />
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
