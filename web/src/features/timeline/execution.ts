import type { Attempt, TimelineEvent } from "../../shared/api/types";

export type ExecutionAttempt = {
  kind: "current" | "history";
  value: Attempt;
  question?: string;
};

function order(attempt: ExecutionAttempt) {
  const value = new Date(attempt.value.created_at).getTime();
  return Number.isFinite(value) ? value : Number.MAX_SAFE_INTEGER;
}

export function groupExecutionAttempts(
  events: TimelineEvent[],
  currentAttempt: Attempt | null,
  attemptHistory: Attempt[],
) {
  const eventIDs = new Set(events.map((event) => event.id));
  const lastEventByAttempt = new Map<string, string>();
  const resultEventByAttempt = new Map<string, string>();
  const replyEventByAttempt = new Map<string, string>();
  for (const event of events) {
    if (event.attempt_id) lastEventByAttempt.set(event.attempt_id, event.id);
    if (
      event.attempt_id &&
      event.actor_kind !== "human" &&
      ["comment", "finding", "result", "message"].includes(event.type)
    )
      replyEventByAttempt.set(event.attempt_id, event.id);
    if (
      (event.type === "result" ||
        (event.type === "message" &&
          event.message?.end_turn &&
          event.message.type !== "choice")) &&
      event.actor_kind !== "human" &&
      event.attempt_id
    ) {
      resultEventByAttempt.set(event.attempt_id, event.id);
    }
  }
  const statusByReply = new Map<string, Attempt>();
  const byEvent = new Map<string, ExecutionAttempt[]>();
  const standalone: ExecutionAttempt[] = [];
  // 正在执行的状态始终放在可见消息末尾，避免被迟到的上一轮回复压到上方。
  const runningReply = events.findLast(
    (event) =>
      event.attempt_id === currentAttempt?.id && event.message?.preview,
  );
  const running: ExecutionAttempt | null =
    currentAttempt?.status === "running" && !runningReply
      ? { kind: "current", value: currentAttempt }
      : null;
  const attempts: ExecutionAttempt[] = [
    ...attemptHistory.map((value) => ({
      kind: "history" as const,
      value,
    })),
    ...(currentAttempt && !running
      ? [{ kind: "current" as const, value: currentAttempt }]
      : []),
  ];
  attempts.sort((a, b) => order(a) - order(b));

  for (const attempt of attempts) {
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
          ["comment", "finding", "result", "message"].includes(event.type) &&
          (event.message?.widget?.question || event.content)?.trim() ===
            question,
      )
    ) {
      attempt.question = question;
    }
    // 等待回复、取消和替代都沿用正文上方的元信息，不再另起状态块。
    const resultID =
      execution.status === "completed"
        ? resultEventByAttempt.get(execution.id)
        : ["cancelled", "superseded", "waiting_human"].includes(
              execution.status,
            )
          ? replyEventByAttempt.get(execution.id)
          : execution.status === "running"
            ? runningReply?.id
            : undefined;
    if (resultID && !attempt.question) {
      statusByReply.set(resultID, attempt.value);
      continue;
    }
    const source =
      lastEventByAttempt.get(attempt.value.id) || attempt.value.source_event_id;
    if (!source || !eventIDs.has(source)) {
      standalone.push(attempt);
      continue;
    }
    const existing = byEvent.get(source) || [];
    existing.push(attempt);
    byEvent.set(source, existing);
  }
  return { byEvent, standalone, statusByReply, running };
}

/** 按持久事件顺序展示，保留人工输入与 Agent 多条回复的真实交错。 */
export function orderTimelineEvents(events: TimelineEvent[]) {
  return [...events].sort((a, b) => a.sequence - b.sequence);
}
