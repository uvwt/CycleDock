export const cardMessageEvent = "cycledock:card-message";

export type CardMessageDetail = CardUpdate & {
  body: string;
  viewing: boolean;
};

export type CardUpdate = {
  id: string;
  project_id: string;
  kind: string;
  title: string;
  revision: string;
  event_sequence: string;
  message_preview: string;
  attempt_status: string;
};

// 只有新的讨论消息或模型回复才通知。置顶、标题和执行状态会改变 revision，用来刷新页面。
export function cardNotificationBody(
  card: CardUpdate,
  previous: CardUpdate | undefined,
): string {
  if (
    card.event_sequence === previous?.event_sequence ||
    !card.message_preview
  ) {
    return "";
  }
  return card.message_preview;
}
