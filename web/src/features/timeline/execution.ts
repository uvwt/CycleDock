import type { QueueItem, Run, WorkItem } from "../../shared/api/types";

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
