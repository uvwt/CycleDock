import {
  Bell,
  BellOff,
  Mail,
  MailOpen,
  Pin,
  PinOff,
  Trash2,
} from "lucide-react";
import type { Card } from "../../shared/api/types";
import { isCardUnread } from "../../shared/lib/card-read-state";
import type { SwipeActionItem } from "../../shared/ui/SwipeableRow";

/**
 * 构建列表项左滑高频快捷按钮（主流 IM 手感，拒绝拥挤）：
 * - 普通卡片（非主讨论）：「标为未读 / 标为已读」+「置顶 / 取消置顶」+「删除」（最多 3 个）
 * - 主讨论（固定置顶、不可删）：「标为未读 / 标为已读」+「静音 / 取消静音」（2 个）
 */
export function buildCardSwipeActions({
  card,
  isMain = false,
  isActive = false,
  t,
  onToggleRead,
  onTogglePin,
  onToggleMute,
  onDelete,
}: {
  card: Card;
  isMain?: boolean;
  isActive?: boolean;
  t: (key: string) => string;
  onToggleRead: (nextUnread: boolean) => void;
  onTogglePin?: () => void;
  onToggleMute?: () => void;
  onDelete?: () => void;
}): SwipeActionItem[] {
  const unread = isCardUnread(card, isActive);

  const readAction: SwipeActionItem = {
    key: "read-state",
    label: unread ? t("标为已读") : t("标为未读"),
    icon: unread ? (
      <MailOpen className="size-4" />
    ) : (
      <Mail className="size-4" />
    ),
    variant: "primary",
    onClick: () => onToggleRead(!unread),
  };

  if (isMain) {
    const actions: SwipeActionItem[] = [readAction];
    if (onToggleMute) {
      actions.push({
        key: "mute",
        label: card.notifications_muted ? t("取消静音") : t("静音通知"),
        icon: card.notifications_muted ? (
          <Bell className="size-4" />
        ) : (
          <BellOff className="size-4" />
        ),
        variant: "secondary",
        onClick: () => onToggleMute(),
      });
    }
    return actions;
  }

  const actions: SwipeActionItem[] = [readAction];
  if (onTogglePin) {
    const pinLabel = card.pinned_at
      ? t("取消置顶")
      : card.kind === "discussion"
        ? t("置顶讨论")
        : card.kind === "ongoing"
          ? t("置顶定时任务")
          : t("置顶任务");
    actions.push({
      key: "pin",
      label: pinLabel,
      icon: card.pinned_at ? (
        <PinOff className="size-4" />
      ) : (
        <Pin className="size-4" />
      ),
      variant: "warning",
      onClick: () => onTogglePin(),
    });
  }
  if (onDelete) {
    const deleteLabel =
      card.kind === "discussion"
        ? t("删除讨论")
        : card.kind === "ongoing"
          ? t("删除定时任务")
          : t("删除任务");
    actions.push({
      key: "delete",
      label: deleteLabel,
      icon: <Trash2 className="size-4" />,
      variant: "destructive",
      onClick: () => onDelete(),
    });
  }
  return actions;
}
