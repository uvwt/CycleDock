import type { Attempt, TimelineEvent } from "../../shared/api/types";

export const TWO_HOURS_MS = 2 * 60 * 60 * 1000;

export type ExecutionAttempt = {
  kind: "current" | "history";
  value: Attempt;
};

export type TimelineRowLike = {
  event: TimelineEvent;
  preview?: unknown;
  key: string;
};

export type TimelineItem<TRow extends TimelineRowLike> =
  | {
      kind: "event";
      key: string;
      row: TRow;
      orderTime: number | null;
      displayTime: number | null;
      displayTimestamp: string;
    }
  | {
      kind: "standalone";
      key: string;
      attempts: ExecutionAttempt[];
      firstTime: number | null;
      lastTime: number | null;
      displayTimestamp: string;
    };

function parseTimestamp(value?: string): number | null {
  if (!value) return null;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function getAttemptTime(attempt: Attempt): number | null {
  return parseTimestamp(attempt.started_at) ?? parseTimestamp(attempt.created_at);
}

function compareAttempts(a: Attempt, b: Attempt): number {
  const timeA = getAttemptTime(a) ?? Number.MAX_SAFE_INTEGER;
  const timeB = getAttemptTime(b) ?? Number.MAX_SAFE_INTEGER;
  if (timeA !== timeB) return timeA - timeB;
  const createdA = parseTimestamp(a.created_at) ?? Number.MAX_SAFE_INTEGER;
  const createdB = parseTimestamp(b.created_at) ?? Number.MAX_SAFE_INTEGER;
  if (createdA !== createdB) return createdA - createdB;
  return a.attempt_number - b.attempt_number;
}

function buildAttemptLookup(attempts?: Attempt[]) {
  const sortedAttempts = [...(attempts || [])].sort(compareAttempts);
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

  return { attemptByID, promptAttemptBySourceEventID };
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
        (event.type === "message" && event.message?.end_turn)) &&
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
  attempts.sort((a, b) => compareAttempts(a.value, b.value));

  for (const attempt of attempts) {
    const execution = attempt.value;
    // 终态执行的状态沿用对应回复，不为普通提问创建等待状态。
    const resultID =
      execution.status === "completed"
        ? resultEventByAttempt.get(execution.id)
        : ["cancelled", "superseded"].includes(execution.status)
          ? replyEventByAttempt.get(execution.id)
          : execution.status === "running"
            ? runningReply?.id
            : undefined;
    if (resultID) {
      statusByReply.set(resultID, attempt.value);
      continue;
    }
    const source =
      lastEventByAttempt.get(attempt.value.id) || attempt.value.source_event_id;
    // source_event_id 存在但消息尚未加载，不能据此认定为系统触发。
    if (source && !eventIDs.has(source)) continue;
    if (!source) {
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

  const { attemptByID, promptAttemptBySourceEventID } =
    buildAttemptLookup(attempts);

  // 为每个事件计算排序关键元组：
  // - groupTime: 归属 Attempt 的执行基准时间（若是独立事件则为自身时间）
  // - role: 0 为本轮人工提问（置于该 Attempt 所有回复之前），1 为 Attempt 执行产生的回复或事件
  // - sequence: 同角色或同 Attempt 内保持原有递增 sequence
  type EventKey = {
    groupTime: number;
    role: number;
    sequence: number;
  };

  const getEventKey = (event: TimelineEvent): EventKey => {
    const promptAttempt = promptAttemptBySourceEventID.get(event.id);
    if (promptAttempt) {
      return {
        groupTime: getAttemptTime(promptAttempt) ?? 0,
        role: 0,
        sequence: event.sequence,
      };
    }

    if (event.attempt_id) {
      const executionAttempt = attemptByID.get(event.attempt_id);
      if (executionAttempt) {
        return {
          groupTime: getAttemptTime(executionAttempt) ?? 0,
          role: 1,
          sequence: event.sequence,
        };
      }
    }

    // 独立事件（如卡片状态变更、未关联 Attempt 的事件等）
    return {
      groupTime: parseTimestamp(event.created_at) ?? 0,
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

/**
 * 将未关联消息的系统触发执行（standalone）按实际发生时间（started_at || created_at）
 * 穿插到对应事件前后，避免旧系统失败记录被永久固定在时间线末尾。
 * 相邻且间隔不超过两小时的连续系统触发会合并为同一展示组；跨过两小时则拆分以便渲染时间分隔线。
 */
export function buildTimelineItems<TRow extends TimelineRowLike>(
  rows: TRow[],
  standalone: ExecutionAttempt[],
  attempts?: Attempt[],
): TimelineItem<TRow>[] {
  const sortedStandalone = [...standalone].sort((a, b) =>
    compareAttempts(a.value, b.value),
  );
  const { attemptByID, promptAttemptBySourceEventID } =
    buildAttemptLookup(attempts);

  const getRowOrderTime = (row: TRow): number | null => {
    const event = row.event;
    const promptAttempt = promptAttemptBySourceEventID.get(event.id);
    if (promptAttempt) {
      return (
        getAttemptTime(promptAttempt) ?? parseTimestamp(event.created_at)
      );
    }
    if (event.attempt_id) {
      const executionAttempt = attemptByID.get(event.attempt_id);
      if (executionAttempt) {
        return (
          getAttemptTime(executionAttempt) ?? parseTimestamp(event.created_at)
        );
      }
    }
    return parseTimestamp(event.created_at);
  };

  const items: TimelineItem<TRow>[] = [];
  const pushStandalone = (attempt: ExecutionAttempt) => {
    const time = getAttemptTime(attempt.value);
    const timestampValue =
      attempt.value.started_at || attempt.value.created_at || "";
    const lastItem = items.at(-1);
    if (
      lastItem?.kind === "standalone" &&
      (lastItem.lastTime === null ||
        time === null ||
        time - lastItem.lastTime <= TWO_HOURS_MS)
    ) {
      lastItem.attempts.push(attempt);
      if (lastItem.firstTime === null) lastItem.firstTime = time;
      if (time !== null) lastItem.lastTime = time;
      if (!lastItem.displayTimestamp && timestampValue) {
        lastItem.displayTimestamp = timestampValue;
      }
      return;
    }
    items.push({
      kind: "standalone",
      key: `standalone:${attempt.kind}:${attempt.value.id}`,
      attempts: [attempt],
      firstTime: time,
      lastTime: time,
      displayTimestamp: timestampValue,
    });
  };

  let standaloneIndex = 0;
  for (const row of rows) {
    const rawOrderTime = getRowOrderTime(row);
    // 尚未拿到时间戳的实时预览默认视为当前末尾，防止历史系统失败记录被压到实时回复之后。
    const effectiveOrderTime =
      rawOrderTime ?? (row.preview ? Number.MAX_SAFE_INTEGER : null);

    if (effectiveOrderTime !== null) {
      while (standaloneIndex < sortedStandalone.length) {
        const candidate = sortedStandalone[standaloneIndex];
        const candidateTime = getAttemptTime(candidate.value);
        if (candidateTime === null || candidateTime >= effectiveOrderTime) {
          break;
        }
        pushStandalone(candidate);
        standaloneIndex += 1;
      }
    }

    items.push({
      kind: "event",
      key: row.key,
      row,
      orderTime: effectiveOrderTime,
      displayTime: parseTimestamp(row.event.created_at),
      displayTimestamp: row.event.created_at,
    });
  }

  while (standaloneIndex < sortedStandalone.length) {
    pushStandalone(sortedStandalone[standaloneIndex]);
    standaloneIndex += 1;
  }

  return items;
}
