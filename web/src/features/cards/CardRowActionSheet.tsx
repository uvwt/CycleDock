import {
  Bell,
  BellOff,
  Mail,
  MailOpen,
  Pencil,
  Pin,
  PinOff,
  Trash2,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Card } from "../../shared/api/types";
import { isCardUnread } from "../../shared/lib/card-read-state";
import {
  Sheet as UISheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "../../shared/ui/sheet";

export function CardRowActionSheet({
  card,
  open,
  onOpenChange,
  preserve = false,
  isActive = false,
  readPending = false,
  pinPending = false,
  mutePending = false,
  deletePending = false,
  onToggleRead,
  onTogglePin,
  onToggleMute,
  onEdit,
  onDelete,
}: {
  card: Card | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  preserve?: boolean;
  isActive?: boolean;
  readPending?: boolean;
  pinPending?: boolean;
  mutePending?: boolean;
  deletePending?: boolean;
  onToggleRead: (card: Card, nextUnread: boolean) => void;
  onTogglePin?: (card: Card) => void;
  onToggleMute: (card: Card) => void;
  onEdit?: (card: Card) => void;
  onDelete?: (card: Card) => void;
}) {
  const { t } = useTranslation();

  if (!card) return null;

  const unread = isCardUnread(card, isActive);
  const cardKindLabel =
    card.kind === "discussion"
      ? t("讨论")
      : card.kind === "ongoing"
        ? t("定时任务")
        : t("任务");

  return (
    <UISheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        onOpenAutoFocus={(e) => e.preventDefault()}
        className="max-h-[85vh] rounded-t-2xl border-line/70 bg-surface/98 p-0 pb-safe backdrop-blur-md"
      >
        <div className="mx-auto mt-2.5 h-1.5 w-12 rounded-full bg-muted-foreground/20" />
        <SheetHeader className="border-b border-line/60 px-4 py-3">
          <SheetTitle className="truncate text-left text-sm font-medium text-muted-foreground">
            {card.title || cardKindLabel}
          </SheetTitle>
        </SheetHeader>
        <div className="flex flex-col gap-1 p-2">
          <button
            type="button"
            disabled={readPending}
            onClick={() => {
              onOpenChange(false);
              onToggleRead(card, !unread);
            }}
            className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm text-foreground transition active:bg-muted/80 disabled:opacity-50"
          >
            {unread ? (
              <>
                <MailOpen className="size-4 text-muted-foreground" />
                <span>{t("标为已读")}</span>
              </>
            ) : (
              <>
                <Mail className="size-4 text-muted-foreground" />
                <span>{t("标为未读")}</span>
              </>
            )}
          </button>

          {!preserve && onTogglePin && (
            <button
              type="button"
              disabled={pinPending}
              onClick={() => {
                onOpenChange(false);
                onTogglePin(card);
              }}
              className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm text-foreground transition active:bg-muted/80 disabled:opacity-50"
            >
              {card.pinned_at ? (
                <>
                  <PinOff className="size-4 text-muted-foreground" />
                  <span>{t("取消置顶")}</span>
                </>
              ) : (
                <>
                  <Pin className="size-4 text-muted-foreground" />
                  <span>
                    {card.kind === "discussion"
                      ? t("置顶讨论")
                      : card.kind === "ongoing"
                        ? t("置顶定时任务")
                        : t("置顶任务")}
                  </span>
                </>
              )}
            </button>
          )}

          <button
            type="button"
            disabled={mutePending}
            onClick={() => {
              onOpenChange(false);
              onToggleMute(card);
            }}
            className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm text-foreground transition active:bg-muted/80 disabled:opacity-50"
          >
            {card.notifications_muted ? (
              <>
                <Bell className="size-4 text-muted-foreground" />
                <span>{t("取消静音")}</span>
              </>
            ) : (
              <>
                <BellOff className="size-4 text-muted-foreground" />
                <span>{t("静音通知")}</span>
              </>
            )}
          </button>

          {!preserve && !card.managed_by_remote_channel_id && onEdit && (
            <button
              type="button"
              onClick={() => {
                onOpenChange(false);
                onEdit(card);
              }}
              className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm text-foreground transition active:bg-muted/80"
            >
              <Pencil className="size-4 text-muted-foreground" />
              <span>{t("编辑信息")}</span>
            </button>
          )}

          {!preserve && onDelete && (
            <>
              <div className="my-1 h-px bg-line/60" />
              <button
                type="button"
                disabled={deletePending}
                onClick={() => {
                  onOpenChange(false);
                  onDelete(card);
                }}
                className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm font-medium text-danger transition active:bg-danger/10 disabled:opacity-50"
              >
                <Trash2 className="size-4" />
                <span>
                  {card.kind === "discussion"
                    ? t("删除讨论")
                    : card.kind === "ongoing"
                      ? t("删除定时任务")
                      : t("删除任务")}
                </span>
              </button>
            </>
          )}
        </div>
      </SheetContent>
    </UISheet>
  );
}
