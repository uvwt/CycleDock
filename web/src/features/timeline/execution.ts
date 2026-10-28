import type {
  Attempt,
  PendingTurn,
  TimelineEvent,
} from "../../shared/api/types";

export type ExecutionAttempt = (
  | { kind: "pending"; value: PendingTurn }
  | { kind: "current"; value: Attempt }
  | { kind: "history"; value: Attempt }
) & { question?: string };

function sourceEventID(attempt: ExecutionAttempt) {
  return attempt.value.source_event_id;
}

function order(attempt: ExecutionAttempt) {
  if (attempt.kind === "pending") return attempt.value.sequence;
  const value = new Date(attempt.value.created_at).getTime();
  return Number.isFinite(value) ? value : Number.MAX_SAFE_INTEGER;
}

export function groupExecutionAttempts(
  events: TimelineEvent[],
  pendingTurns: PendingTurn[],
  currentAttempt: Attempt | null,
  attemptHistory: Attempt[],
) {
  const eventIDs = new Set(events.map((event) => event.id));
  const lastEventByAttempt = new Map<string, string>();
  const resultEventByAttempt = new Map<string, string>();
  for (const event of events) {
    if (event.attempt_id) lastEventByAttempt.set(event.attempt_id, event.id);
    if (
      event.type === "result" &&
      event.actor_kind !== "human" &&
      event.attempt_id
    ) {
      resultEventByAttempt.set(event.attempt_id, event.id);
    }
  }
  const completedByResult = new Map<string, Attempt>();
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
    if (attempt.kind !== "pending") {
      const execution = attempt.value;
      const awaitingReply = execution.status === "waiting_human";
      const humanReplied =
        execution.status === "superseded" &&
        execution.error_summary === "superseded by human reply";
      const question = execution.progress_summary?.trim();
      // 旧提问可能只保存在执行摘要中；同次执行已有正文消息时不重复展示。
      if (
        question &&
        (awaitingReply || humanReplied) &&
        !events.some(
          (event) =>
            event.attempt_id === execution.id &&
            event.actor_kind !== "human" &&
            ["comment", "finding", "result"].includes(event.type) &&
            event.content?.trim() === question,
        )
      ) {
        attempt.question = question;
      }
    }
    // 仅在同次执行的结果已可见时合并完成状态；无结果、失败和重试历史仍保留。
    const resultID = resultEventByAttempt.get(attempt.value.id);
    if (
      attempt.kind !== "pending" &&
      attempt.value.status === "completed" &&
      resultID
    ) {
      completedByResult.set(resultID, attempt.value);
      continue;
    }
    const source =
      (attempt.kind !== "pending" &&
        lastEventByAttempt.get(attempt.value.id)) ||
      sourceEventID(attempt);
    if (!source || !eventIDs.has(source)) {
      standalone.push(attempt);
      continue;
    }
    const existing = byEvent.get(source) || [];
    existing.push(attempt);
    byEvent.set(source, existing);
  }
  return { byEvent, standalone, completedByResult };
}

/** 按持久事件顺序展示，保留人工输入与 Agent 多条回复的真实交错。 */
export function orderTimelineEvents(events: TimelineEvent[]) {
  return [...events].sort((a, b) => a.sequence - b.sequence);
}
