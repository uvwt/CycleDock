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
  notification_kind?: string;
  pending_approval_count?: number;
  attempt_status: string;
  notifications_muted?: boolean;
};

// 只有新的模型回复或待人工审批工具调用才触发浏览器/气泡通知。
// 置顶、静音切换、标题和执行状态会改变 revision，用于刷新页面缓存，但不会弹窗通知。
// 卡片静音屏蔽普通消息，但待人工确认工具审批会穿透静音提醒。
export function cardNotificationBody(
  card: CardUpdate,
  previous: CardUpdate | undefined,
): string {
  if (!card.message_preview) {
    return "";
  }
  const isToolApproval = card.notification_kind === "tool_approval";
  if (card.notifications_muted && !isToolApproval) {
    return "";
  }
  const currentSeq = Number(card.event_sequence);
  const previousSeq = previous ? Number(previous.event_sequence) : Number.NaN;
  if (Number.isFinite(currentSeq) && Number.isFinite(previousSeq)) {
    if (currentSeq <= previousSeq) {
      return "";
    }
  } else if (card.event_sequence === previous?.event_sequence) {
    return "";
  }
  return card.message_preview;
}
