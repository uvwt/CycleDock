import { ComposerInput } from "../../shared/ui/ComposerInput";
import { ArrowUp, ListPlus, LoaderCircle, Square } from "lucide-react";
import { useTranslation } from "react-i18next";

export function ConversationComposer({
  comment,
  setComment,
  blocked,
  waiting,
  hasCurrent,
  sendPending,
  streamConnected,
  streamError,
  cancelPending,
  onSubmit,
  onCancel,
}: {
  comment: string;
  setComment: (value: string) => void;
  expanded?: boolean;
  setExpanded?: (value: boolean) => void;
  blocked: boolean;
  waiting: boolean;
  hasCurrent: boolean;
  sendPending: boolean;
  streamConnected: boolean;
  streamError?: string;
  cancelPending: boolean;
  onSubmit: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const placeholder = blocked
    ? t("讨论已归档")
    : waiting
      ? t("回复 Agent，继续执行…")
      : t("发送消息，继续推进任务…");

  const sendDisabled = sendPending || !comment.trim() || blocked;
  const showStop = hasCurrent && !waiting && !comment.trim();
  const actionLabel = showStop
    ? t("中止")
    : waiting
      ? t("回复并继续")
      : t("发送");

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (!sendDisabled && !cancelPending) onSubmit();
      }}
      className="relative flex items-end rounded-2xl border border-line bg-surface py-1.5 pr-1.5 pl-3 shadow-sm transition-[border-color,box-shadow] focus-within:border-line-strong"
    >
      <ComposerInput
        value={comment}
        onChange={setComment}
        placeholder={placeholder}
        readOnly={blocked}
      />
      <div className="flex shrink-0 items-center gap-1.5 pb-0.5">
        {waiting && (
          <span className="text-xs text-warning mr-1">{t("等待人工回复")}</span>
        )}
        {!streamConnected && (
          <span title={streamError} className="text-[11px] text-faint mr-1">
            {t("正在连接…")}
          </span>
        )}
        <button
          type={showStop ? "button" : "submit"}
          title={actionLabel}
          aria-label={actionLabel}
          onClick={showStop ? onCancel : undefined}
          disabled={cancelPending || (!showStop && sendDisabled)}
          className={`inline-flex h-9 shrink-0 items-center justify-center gap-1.5 rounded-full transition disabled:opacity-30 ${waiting ? "px-3" : "w-9"} bg-action text-action-foreground hover:bg-action-hover`}
        >
          {showStop ? (
            cancelPending ? (
              <LoaderCircle className="size-4 animate-spin" />
            ) : (
              <Square className="size-3.5 fill-current" />
            )
          ) : waiting ? (
            <ListPlus className="size-4" />
          ) : (
            <ArrowUp className="size-4" />
          )}
          {waiting && (
            <span className="text-xs font-medium">{t("回复并继续")}</span>
          )}
        </button>
      </div>
    </form>
  );
}
