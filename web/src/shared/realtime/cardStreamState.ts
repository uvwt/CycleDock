export type LiveReply = {
  id: string;
  order: number;
  text: string;
  eventID?: string;
  failed?: boolean;
};

export type TaskStep = { title: string; status?: string };

export type TaskProgress = {
  id: string;
  title: string;
  status?: string;
  completed_step_count: number;
  step_count: number;
  current_step?: string;
  summary?: string;
  blocker?: string;
  steps?: TaskStep[];
};

export type LiveAttempt = {
  attemptID: string;
  replies: LiveReply[];
  stage: string;
  deviceName?: string;
  task?: TaskProgress;
  startedAt?: string;
  finished?: boolean;
  previous?: { attemptID: string; replies: LiveReply[]; startedAt?: string }[];
};

export type CardStreamEvent = {
  attempt_id?: string;
  reply_id?: string;
  event_id?: string;
  text?: string;
  order?: number;
  stage?: string;
  device_name?: string;
  task?: TaskProgress;
  started_at?: string;
  replies?: {
    id: string;
    order: number;
    text: string;
    event_id?: string;
    failed?: boolean;
  }[];
};

/** 参数预览使用完整正文快照，重复通知不会重复拼字；正式消息按事件 ID 接替。 */
export function applyCardStreamEvent(
  live: LiveAttempt | null,
  type: string,
  event: CardStreamEvent,
): LiveAttempt | null {
  if (!event.attempt_id) return live;
  if (type === "attempt.started" || type === "attempt.snapshot") {
    // 下一次执行可以接替运行状态，但不能撤回用户已经看见的正文。
    const previous = [...(live?.previous || [])];
    if (live && live.attemptID !== event.attempt_id) {
      const replies = live.replies.filter((reply) => !reply.eventID);
      if (replies.length)
        previous.push({
          attemptID: live.attemptID,
          startedAt: live.startedAt,
          replies: replies.map((reply) => ({ ...reply, failed: true })),
        });
    }
    return {
      previous,
      attemptID: event.attempt_id,
      replies: (event.replies || []).map((reply) => ({
        id: reply.id,
        order: reply.order,
        text: reply.text,
        eventID: reply.event_id,
        failed: reply.failed,
      })),
      stage: event.stage || "正在执行",
      deviceName: event.device_name || "",
      task: event.task,
      startedAt: event.started_at,
    };
  }
  if (!live || live.attemptID !== event.attempt_id || live.finished)
    return live;
  switch (type) {
    case "attempt.stage":
      return { ...live, stage: event.stage || live.stage };
    case "attempt.device":
      // 缺省设备名表示这次调用已经结束。阶段轮询不带该字段，因此不能在 attempt.stage 里清设备。
      return { ...live, deviceName: event.device_name || "" };
    case "attempt.task":
      if (!event.task?.id) return live;
      return { ...live, task: event.task };
    case "reply.preview": {
      if (!event.reply_id || !event.text) return live;
      const existing = live.replies.find(
        (reply) => reply.id === event.reply_id,
      );
      if (existing?.eventID || existing?.failed) return live;
      const reply = {
        id: event.reply_id,
        text: event.text,
        order: event.order || 0,
      };
      return {
        ...live,
        replies: existing
          ? live.replies.map((item) => (item.id === reply.id ? reply : item))
          : [...live.replies, reply].sort((a, b) => a.order - b.order),
      };
    }
    case "reply.committed":
      return {
        ...live,
        replies: live.replies.map((reply) =>
          reply.id === event.reply_id
            ? { ...reply, eventID: event.event_id }
            : reply,
        ),
      };
    case "reply.discarded":
      return {
        ...live,
        replies: live.replies.map((reply) =>
          reply.id === event.reply_id ? { ...reply, failed: true } : reply,
        ),
      };
    case "reply.reset":
      return {
        ...live,
        replies: live.replies.map((reply) =>
          reply.eventID ? reply : { ...reply, failed: true },
        ),
      };
    case "attempt.finished":
    case "attempt.failed":
      return {
        ...live,
        finished: true,
        deviceName: "",
        replies: live.replies.map((reply) =>
          reply.eventID ? reply : { ...reply, failed: true },
        ),
      };
    default:
      return live;
  }
}
