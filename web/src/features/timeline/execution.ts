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

/** 按持久事件与执行轮次顺序展示，保留人工输入与 Agent 多条回复的真实交错（一问一答）。 */
export function orderTimelineEvents(
  events: TimelineEvent[],
  attempts?: Attempt[],
) {
  if (!attempts || attempts.length === 0) {
    return [...events].sort((a, b) => a.sequence - b.sequence);
  }

  // 1. 整理 Attempt：按执行时间升序排列
  const sortedAttempts = [...attempts].sort((a, b) => {
    const timeA = new Date(a.started_at || a.created_at).getTime();
    const timeB = new Date(b.started_at || b.created_at).getTime();
    if (timeA !== timeB) return timeA - timeB;
    return a.attempt_number - b.attempt_number;
  });

  // 2. 建立映射：
  //    - attemptByID: id -> Attempt
  //    - promptAttemptBySourceEventID: 记录该人工输入对应的首个 Attempt（重试沿用首次）
  const attemptByID = new Map<string, Attempt>();
  const promptAttemptBySourceEventID = new Map<string, Attempt>();

  for (const attempt of sortedAttempts) {
    attemptByID.set(attempt.id, attempt);
    if (
      attempt.source_event_id &&
      !promptAttemptBySourceEventID.has(attempt.source_event_id)
    ) {
      promptAttemptBySourceEventID.set(attempt.source_event_id, attempt);
    }
  }

  // 3. 为每个事件计算排序关键元组：
  //    - groupTime: 归属 Attempt 的执行基准时间（若是独立事件则为自身时间）
  //    - role: 0 为本轮人工提问（置于该 Attempt 所有回复之前），1 为 Attempt 执行产生的回复或事件
  //    - sequence: 同角色或同 Attempt 内保持原有递增 sequence
  type EventKey = {
    groupTime: number;
    role: number;
    sequence: number;
  };

  const getEventKey = (event: TimelineEvent): EventKey => {
    const promptAttempt = promptAttemptBySourceEventID.get(event.id);
    if (promptAttempt) {
      const groupTime = new Date(
        promptAttempt.started_at || promptAttempt.created_at,
      ).getTime();
      return {
        groupTime: Number.isFinite(groupTime) ? groupTime : 0,
        role: 0,
        sequence: event.sequence,
      };
    }

    if (event.attempt_id) {
      const executionAttempt = attemptByID.get(event.attempt_id);
      if (executionAttempt) {
        const groupTime = new Date(
          executionAttempt.started_at || executionAttempt.created_at,
        ).getTime();
        return {
          groupTime: Number.isFinite(groupTime) ? groupTime : 0,
          role: 1,
          sequence: event.sequence,
        };
      }
    }

    // 独立事件（如卡片状态变更、未关联 Attempt 的事件等）
    const selfTime = new Date(event.created_at).getTime();
    return {
      groupTime: Number.isFinite(selfTime) ? selfTime : 0,
      role: 0,
      sequence: event.sequence,
    };
  };

  return [...events].sort((a, b) => {
    const keyA = getEventKey(a);
    const keyB = getEventKey(b);

    if (keyA.groupTime !== keyB.groupTime) {
      return keyA.groupTime - keyB.groupTime;
    }
    if (keyA.role !== keyB.role) {
      return keyA.role - keyB.role;
    }
    return keyA.sequence - keyB.sequence;
  });
}
