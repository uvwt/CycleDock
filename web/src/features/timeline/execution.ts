import type {
  QueueItem,
  Run,
  TimelineEvent,
  WorkItem,
} from "../../shared/api/types";

export type ExecutionAttempt =
  | { kind: "queue"; value: QueueItem }
  | { kind: "run"; value: Run }
  | { kind: "history"; value: WorkItem };

function sourceEventID(attempt: ExecutionAttempt) {
  return attempt.value.source_event_id;
}

function order(attempt: ExecutionAttempt) {
  if (attempt.kind === "queue" || attempt.kind === "history") {
    return attempt.value.sequence;
  }
  return Number.MAX_SAFE_INTEGER;
}

export function groupExecutionAttempts(
  eventIDs: Set<string>,
  queue: QueueItem[],
  currentRun: Run | null,
  workHistory: WorkItem[],
) {
  const byEvent = new Map<string, ExecutionAttempt[]>();
  const standalone: ExecutionAttempt[] = [];
  const attempts: ExecutionAttempt[] = [
    ...workHistory.map((value) => ({
      kind: "history" as const,
      value,
    })),
    ...queue.map((value) => ({ kind: "queue" as const, value })),
    ...(currentRun ? [{ kind: "run" as const, value: currentRun }] : []),
  ];
  attempts.sort((a, b) => order(a) - order(b));

  for (const attempt of attempts) {
    const source = sourceEventID(attempt);
    if (!source || !eventIDs.has(source)) {
      standalone.push(attempt);
      continue;
    }
    const existing = byEvent.get(source) || [];
    existing.push(attempt);
    byEvent.set(source, existing);
  }
  return { byEvent, standalone };
}

export function groupRunEvents(
  events: TimelineEvent[],
  attemptsByEvent: Map<string, ExecutionAttempt[]>,
) {
  const sourceEventByRunID = new Map<string, string>();
  for (const [sourceEventID, attempts] of attemptsByEvent) {
    for (const attempt of attempts) {
      const runID =
        attempt.kind === "run"
          ? attempt.value.id
          : attempt.kind === "history"
            ? attempt.value.run_id
            : undefined;
      if (runID) sourceEventByRunID.set(runID, sourceEventID);
    }
  }

  const bySourceEvent = new Map<string, TimelineEvent[]>();
  const topLevel: TimelineEvent[] = [];
  for (const event of events) {
    // Timeline 的 sequence 仍是审计顺序；展示层只在服务端给出稳定 Run 关联时
    // 把 Agent 事件收回触发它的那一轮，避免并发排队让回复跑到后续用户消息之后。
    const sourceEventID = event.run_id
      ? sourceEventByRunID.get(event.run_id)
      : undefined;
    if (!sourceEventID || sourceEventID === event.id || event.actor_kind === "human") {
      topLevel.push(event);
      continue;
    }
    const attached = bySourceEvent.get(sourceEventID) || [];
    attached.push(event);
    bySourceEvent.set(sourceEventID, attached);
  }

  return { bySourceEvent, topLevel };
}
