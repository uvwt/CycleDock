export type LiveReply = {
  id: string;
  order: number;
  text: string;
  eventID?: string;
  failed?: boolean;
};

export type LiveAttempt = {
  attemptID: string;
  replies: LiveReply[];
  stage: string;
  startedAt?: string;
  finished?: boolean;
};

export type CardStreamEvent = {
  attempt_id?: string;
  reply_id?: string;
  event_id?: string;
  text?: string;
  order?: number;
  stage?: string;
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
    return {
      attemptID: event.attempt_id,
      replies: (event.replies || []).map((reply) => ({
        id: reply.id,
        order: reply.order,
        text: reply.text,
        eventID: reply.event_id,
        failed: reply.failed,
      })),
      stage: event.stage || "正在执行",
      startedAt: event.started_at,
    };
  }
  if (!live || live.attemptID !== event.attempt_id || live.finished)
    return live;
  switch (type) {
    case "attempt.stage":
      return { ...live, stage: event.stage || live.stage };
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
        replies: live.replies.filter((reply) => reply.eventID),
      };
    case "attempt.finished":
    case "attempt.failed":
      return {
        ...live,
        finished: true,
        replies: live.replies.map((reply) =>
          reply.eventID ? reply : { ...reply, failed: true },
        ),
      };
    default:
      return live;
  }
}
