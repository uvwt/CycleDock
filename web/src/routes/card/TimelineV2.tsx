import { useMemo, useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useVirtualizer } from "@tanstack/react-virtual";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { LoadingButton, Typography } from "../../components/ui/controls";
import {
  HumanMessage,
  AgentMessage,
  AgentRunMessage,
  SystemEvent,
} from "../../components/ui/messages";
import type {
  CardDetail,
  TimelineEvent,
  WorkItem,
  QueueItem,
} from "../../shared/api/types";
import type { LiveRun } from "../../shared/realtime/useCardStream";

function Markdown({ children }: { children: string }) {
  return (
    <div className="v2-markdown min-w-0">
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{children}</ReactMarkdown>
    </div>
  );
}

function EventDisplay({ event }: { event: TimelineEvent }) {
  const { t } = useTranslation();
  const human = event.type === "comment" && event.actor_kind === "human";
  if (human)
    return (
      <HumanMessage>
        <Markdown>{event.content}</Markdown>
      </HumanMessage>
    );
  if (event.type === "status_change")
    return <SystemEvent>{event.content}</SystemEvent>;
  return (
    <AgentMessage
      agent={
        event.actor_name ||
        t(
          (
            {
              comment: "Agent",
              progress: "进度",
              finding: "发现",
              result: "结果",
              checkin: "检查",
              confirm: "确认",
              reject: "退回",
            } as Record<string, string>
          )[event.type] || "Cycle Agent",
        )
      }
    >
      {event.type !== "comment" && (
        <Typography scale="meta" className="mb-1">
          {t(
            (
              {
                progress: "进度",
                finding: "发现",
                result: "结果",
                checkin: "检查",
                confirm: "确认",
                reject: "退回",
              } as Record<string, string>
            )[event.type] || "",
          )}
        </Typography>
      )}
      <Markdown>{event.content}</Markdown>
    </AgentMessage>
  );
}

function WorkState({
  item,
  onCancel,
  onRetry,
  pending,
}: {
  item: WorkItem | QueueItem;
  onCancel: (id: string) => void;
  onRetry: (id: string) => void;
  pending: boolean;
}) {
  const { t } = useTranslation();
  const queued = "dispatch_mode" in item;
  const status = queued ? "queued" : (item as WorkItem).status;
  const summary = queued
    ? "排队中"
    : (
        {
          running: "正在执行",
          queued: "排队中",
          waiting_human: "等待回复",
          failed: "执行失败",
          cancelled: "执行取消",
          superseded: "执行已转向",
          completed: "执行完成",
          expired: "执行过期",
        } as Record<string, string>
      )[status] || status;
  return (
    <AgentMessage agent={t(summary)}>
      <div className="space-y-2">
        <Typography scale="control">
          {(!queued &&
            ((item as WorkItem).error_summary ||
              (item as WorkItem).current_stage ||
              (item as WorkItem).progress_summary)) ||
            item.content}
        </Typography>
        <div className="flex flex-wrap gap-2">
          {queued && (
            <LoadingButton
              size="compact"
              loading={pending}
              onClick={() => onCancel(item.trigger_id)}
            >
              {t("取消排队")}
            </LoadingButton>
          )}
          {!queued && (item as WorkItem).can_retry && (
            <LoadingButton
              size="compact"
              loading={pending}
              onClick={() => onRetry(item.trigger_id)}
            >
              {t("重试")}
            </LoadingButton>
          )}
        </div>
      </div>
    </AgentMessage>
  );
}

type TimelineRow = { key: string; content: ReactNode };
/** 服务端 sequence 是唯一顺序依据。无稳定来源 id 的执行记录单独展示，不用时间/文本猜测关联。 */
function timelineRows(
  detail: CardDetail,
  live: LiveRun | null,
  onCancel: (id: string) => void,
  onRetry: (id: string) => void,
  pending: boolean,
  duration?: string,
): TimelineRow[] {
  const events = [...detail.timeline].sort((a, b) => a.sequence - b.sequence);
  const linked = new Map<string, (WorkItem | QueueItem)[]>();
  const unlinked: (WorkItem | QueueItem)[] = [];
  const eventIDs = new Set(events.map((e) => e.id));
  const seenTriggers = new Set<string>();
  const add = (item: WorkItem | QueueItem) => {
    if (seenTriggers.has(item.trigger_id)) return;
    seenTriggers.add(item.trigger_id);
    const id = item.source_event_id || item.cause_event_id;
    if (id && eventIDs.has(id))
      linked.set(id, [...(linked.get(id) || []), item]);
    else unlinked.push(item);
  };
  [...detail.work_history].sort((a, b) => a.sequence - b.sequence).forEach(add);
  [...detail.queue].sort((a, b) => a.sequence - b.sequence).forEach(add);
  const rows: TimelineRow[] = [];
  events.forEach((event, index) => {
    const previous = events[index - 1];
    if (
      previous &&
      Date.parse(event.created_at) - Date.parse(previous.created_at) >
        2 * 60 * 60 * 1000
    ) {
      rows.push({
        key: "time-" + event.id,
        content: (
          <SystemEvent>
            {new Date(event.created_at).toLocaleString()}
          </SystemEvent>
        ),
      });
    }
    rows.push({ key: event.id, content: <EventDisplay event={event} /> });
    linked.get(event.id)?.forEach((item) =>
      rows.push({
        key: "work-" + item.trigger_id,
        content: (
          <WorkState
            item={item}
            pending={pending}
            onCancel={onCancel}
            onRetry={onRetry}
          />
        ),
      }),
    );
  });
  unlinked.forEach((item) =>
    rows.push({
      key: "work-" + item.trigger_id,
      content: (
        <WorkState
          item={item}
          pending={pending}
          onCancel={onCancel}
          onRetry={onRetry}
        />
      ),
    }),
  );
  // interrupt 后 SSE 可能先于 REST 更新抵达；新 Run 的即时文本优先，
  // 不能因为旧 current_run 尚未刷新而隐藏新执行结果。
  if (
    detail.current_run &&
    ["running", "queued", "waiting_human"].includes(
      detail.current_run.status,
    ) &&
    (!live || live.runID === detail.current_run.id)
  ) {
    const run = detail.current_run;
    rows.push({
      key: "current-" + run.id,
      content: (
        <AgentRunMessage
          status={run.status === "running" ? "running" : "queued"}
          duration={run.status === "running" ? duration : undefined}
        >
          <div className="space-y-2">
            <Typography scale="control">
              {run.current_stage || run.progress_summary || "Agent 正在处理"}
            </Typography>
            {live?.runID === run.id && live.text && (
              <Markdown>{live.text}</Markdown>
            )}
          </div>
        </AgentRunMessage>
      ),
    });
  } else if (live) {
    rows.push({
      key: "live-" + live.runID,
      content: (
        <AgentRunMessage status="running" duration={duration}>
          {live.text || live.stage}
        </AgentRunMessage>
      ),
    });
  }
  return rows;
}

export function TimelineV2({
  detail,
  live,
  onCancel,
  onRetry,
  pending,
  duration,
  scrollAnchor,
}: {
  detail: CardDetail;
  live: LiveRun | null;
  onCancel: (id: string) => void;
  onRetry: (id: string) => void;
  pending: boolean;
  duration?: string;
  scrollAnchor?: number;
}) {
  const { t } = useTranslation();
  const parent = useRef<HTMLDivElement>(null);
  const rows = useMemo(
    () => timelineRows(detail, live, onCancel, onRetry, pending, duration),
    [detail, live, onCancel, onRetry, pending, duration],
  );
  const long = rows.length > 100;
  // oxlint-disable-next-line react/incompatible-library -- 第三方虚拟布局由 TanStack 自己维护，不交给 React Compiler 自动 memoize。
  const virtual = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parent.current,
    estimateSize: () => 132,
    overscan: 8,
    enabled: long,
  });
  const render = (row: TimelineRow) => (
    <div key={row.key} className="py-2">
      {row.content}
    </div>
  );
  return (
    <div
      className="min-h-0 flex-1 overflow-auto overscroll-contain"
      ref={parent}
      data-timeline
    >
      <div className="mx-auto w-full max-w-4xl px-4 py-5 md:px-8">
        {rows.length === 0 && (
          <div className="v2-text-control py-8 text-center text-[var(--v2-muted)]">
            {t("尚无消息，发送第一条内容开始协作。")}
          </div>
        )}
        {long ? (
          <div style={{ height: virtual.getTotalSize(), position: "relative" }}>
            {virtual.getVirtualItems().map((item) => (
              <div
                key={rows[item.index].key}
                data-index={item.index}
                ref={virtual.measureElement}
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  width: "100%",
                  transform: `translateY(${item.start}px)`,
                }}
              >
                {render(rows[item.index])}
              </div>
            ))}
          </div>
        ) : (
          <div className="space-y-2">{rows.map(render)}</div>
        )}
        <div data-timeline-end={scrollAnchor} />
      </div>
    </div>
  );
}
