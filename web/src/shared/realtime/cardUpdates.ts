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
  notifications_muted?: boolean;
};

// 只有新的讨论消息或模型回复且未静音时才触发浏览器/气泡通知。
// 置顶、静音切换、标题和执行状态会改变 revision，用于刷新页面缓存，但不会弹窗通知。
export function cardNotificationBody(
  card: CardUpdate,
  previous: CardUpdate | undefined,
): string {
  if (
    card.notifications_muted ||
    card.event_sequence === previous?.event_sequence ||
    !card.message_preview
  ) {
    return "";
  }
  return card.message_preview;
}
