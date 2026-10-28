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

export function cardNotificationBody(
  card: CardUpdate,
  previous: CardUpdate | undefined,
  translate: (key: string) => string,
): string {
  if (
    card.event_sequence !== previous?.event_sequence &&
    card.message_preview
  ) {
    return card.message_preview;
  }
  if (card.attempt_status !== previous?.attempt_status) {
    const statuses: Record<string, string> = {
      queued: "排队中",
      running: "正在执行",
      waiting_human: "等待人工回复",
      completed: "已完成",
      failed: "执行失败",
    };
    if (statuses[card.attempt_status]) {
      return translate(statuses[card.attempt_status]);
    }
  }
  // 标题生成等元数据更新会替换同一卡片的系统通知，必须保留最新消息正文。
  return card.message_preview || translate("卡片有更新，点击查看");
}
