import type {
  Attempt,
  PendingTurn,
  TimelineEvent,
} from "../../shared/api/types";

export type ExecutionAttempt =
  | { kind: "pending"; value: PendingTurn }
  | { kind: "current"; value: Attempt }
  | { kind: "history"; value: Attempt };

function sourceEventID(attempt: ExecutionAttempt) {
  return attempt.value.source_event_id;
}

function order(attempt: ExecutionAttempt) {
  if (attempt.kind === "pending") return attempt.value.sequence;
  const value = new Date(attempt.value.created_at).getTime();
  return Number.isFinite(value) ? value : Number.MAX_SAFE_INTEGER;
}

export function groupExecutionAttempts(
  eventIDs: Set<string>,
  pendingTurns: PendingTurn[],
  currentAttempt: Attempt | null,
  attemptHistory: Attempt[],
) {
  const byEvent = new Map<string, ExecutionAttempt[]>();
  const standalone: ExecutionAttempt[] = [];
  const attempts: ExecutionAttempt[] = [
    ...attemptHistory.map((value) => ({
      kind: "history" as const,
      value,
    })),
    ...pendingTurns.map((value) => ({
      kind: "pending" as const,
      value,
    })),
    ...(currentAttempt
      ? [{ kind: "current" as const, value: currentAttempt }]
      : []),
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

export function orderTimelineEvents(
  events: TimelineEvent[],
  currentAttempt: Attempt | null,
  attemptHistory: Attempt[],
) {
  const auditOrder = [...events].sort((a, b) => a.sequence - b.sequence);
  const eventIDs = new Set(auditOrder.map((event) => event.id));
  const sourceByAttempt = new Map<string, string>();
  for (const attempt of [
    ...attemptHistory,
    ...(currentAttempt ? [currentAttempt] : []),
  ]) {
    if (attempt.source_event_id) {
      sourceByAttempt.set(attempt.id, attempt.source_event_id);
    }
  }

  const bySource = new Map<string, TimelineEvent[]>();
  const groupedEventIDs = new Set<string>();
  for (const event of auditOrder) {
    if (!event.attempt_id) continue;
    const sourceEventID = sourceByAttempt.get(event.attempt_id);
    if (!sourceEventID || !eventIDs.has(sourceEventID)) continue;
    const grouped = bySource.get(sourceEventID) || [];
    grouped.push(event);
    bySource.set(sourceEventID, grouped);
    groupedEventIDs.add(event.id);
  }

  const ordered: TimelineEvent[] = [];
  for (const event of auditOrder) {
    if (groupedEventIDs.has(event.id)) continue;
    ordered.push(event);
    const grouped = bySource.get(event.id);
    if (grouped) {
      grouped.sort((a, b) => a.sequence - b.sequence);
      ordered.push(...grouped);
    }
  }
  return ordered;
}
