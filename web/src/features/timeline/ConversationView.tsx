import { useEffect, useRef, useState } from "react";
import { ArrowDown, Bot, Copy, Check } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { formatDistanceToNow } from "date-fns";
import type { TimelineEvent } from "../../shared/api/types";
import type { LiveRun } from "../../shared/realtime/useCardStream";
import { Button } from "../../shared/ui/components";

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
  const [copied, setCopied] = useState(false);
  return (
    <div className="prose-chat text-sm text-slate-700">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          pre: ({ children }) => (
            <div className="relative">
              <button
                title="复制代码"
                aria-label="复制代码"
                onClick={async (e) => {
                  const code =
                    e.currentTarget.parentElement?.querySelector("code")
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
function EventRow({ event }: { event: TimelineEvent }) {
  const system = systemTypes.has(event.type);
  const human = event.actor_kind === "human";
  if (system)
    return (
      <div className="flex justify-center py-2" data-sequence={event.sequence}>
        <span
          className={`max-w-[90%] rounded-full px-3 py-1 text-center text-xs ${event.type === "reject" ? "bg-rose-50 text-rose-700" : event.type === "confirm" ? "bg-amber-50 text-amber-700" : "text-slate-400"}`}
        >
          {labels[event.type] || event.type} · {event.content}
          {event.checkin_status ? ` · ${event.checkin_status}` : ""}
        </span>
      </div>
    );
  return (
    <article
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
        className={`max-w-[min(85%,740px)] min-w-0 ${human ? "rounded-2xl rounded-br-md bg-sky-50 px-4 py-3" : "py-1"}`}
      >
        {!human && (
          <p className="mb-1 text-xs font-semibold text-slate-500">
            {event.actor_name || "Cycle Agent"}
            {event.type === "finding" && (
              <span className="ml-2 text-sky-700">发现</span>
            )}
            {event.type === "result" && (
              <span className="ml-2 text-emerald-700">结果</span>
            )}
          </p>
        )}
        <RichText content={event.content} />
        <p
          className="mt-1 text-[11px] text-slate-400"
          title={new Date(event.created_at).toLocaleString()}
        >
          {formatDistanceToNow(new Date(event.created_at), { addSuffix: true })}
        </p>
      </div>
    </article>
  );
}
function LiveMessage({ live }: { live: LiveRun | null }) {
  if (!live) return null;
  return (
    <div className="flex gap-3">
      <span className="grid size-9 shrink-0 place-items-center rounded-full bg-slate-100">
        <Bot className="size-4" />
      </span>
      <div className="min-w-0 max-w-[min(85%,740px)]">
        <p className="mb-2 text-xs font-semibold text-slate-500">
          Cycle Agent · {live.stage || "正在执行"}
        </p>
        {live.text ? (
          <RichText content={live.text} />
        ) : (
          <div
            aria-label="Agent 正在工作"
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
}: {
  events: TimelineEvent[];
  live: LiveRun | null;
  cardKind: string;
}) {
  const bottom = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [showLatest, setShowLatest] = useState(false);
  useEffect(() => {
    if (stick.current) bottom.current?.scrollIntoView({ block: "end" });
  }, [events, live]);
  function onScroll() {
    const el = list.current;
    if (!el) return;
    const near = el.scrollHeight - el.scrollTop - el.clientHeight < 120;
    stick.current = near;
    setShowLatest(!near);
  }
  return (
    <div
      ref={list}
      onScroll={onScroll}
      className="scrollbar-thin relative min-h-0 flex-1 overflow-y-auto px-4 py-7 sm:px-6"
      role="log"
      aria-label="统一消息时间线"
    >
      <div className="mx-auto flex max-w-3xl flex-col gap-5">
        {!events.length && !live && (
          <div className="py-20 text-center">
            <div className="mx-auto mb-5 grid size-16 place-items-center rounded-2xl bg-slate-100 text-slate-400">
              <Bot className="size-8" />
            </div>
            <h2 className="text-lg font-semibold">
              {cardKind === "discussion" ? "从第一句话开始" : "开始协作"}
            </h2>
            <p className="mt-2 text-sm text-slate-500">
              你的消息、Agent 结果和任务状态都会出现在这里。
            </p>
          </div>
        )}
        {[...events]
          .sort((a, b) => a.sequence - b.sequence)
          .map((e) => (
            <EventRow key={e.id} event={e} />
          ))}
        <LiveMessage live={live} />
        <div ref={bottom} className="h-1" />
      </div>
      {showLatest && (
        <div className="sticky bottom-3 flex justify-center">
          <Button
            onClick={() => {
              bottom.current?.scrollIntoView({
                behavior: "smooth",
                block: "end",
              });
              stick.current = true;
              setShowLatest(false);
            }}
          >
            <ArrowDown className="size-4" />
            跳到最新
          </Button>
        </div>
      )}
    </div>
  );
}
