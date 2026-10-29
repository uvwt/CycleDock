import { NativeSelect } from "../../shared/ui/native-select";
import { ComposerInput } from "../../shared/ui/ComposerInput";
import { ArrowUp, ListPlus, Square, Zap } from "lucide-react";
import { useTranslation } from "react-i18next";

export function ConversationComposer({
  comment,
  setComment,
  blocked,
  waiting,
  hasCurrent,
  submitMode,
  setSubmitMode,
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
  submitMode: "enqueue" | "interrupt";
  setSubmitMode: (value: "enqueue" | "interrupt") => void;
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

  const disabled = sendPending || !comment.trim() || blocked;

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (!disabled) onSubmit();
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
        {hasCurrent && !waiting && (
          <NativeSelect
            size="sm"
            aria-label={t("任务发送方式")}
            value={submitMode}
            onChange={(event) =>
              setSubmitMode(event.target.value as "enqueue" | "interrupt")
            }
            className="text-xs text-secondary-foreground"
          >
            <option value="enqueue">{t("加入队列")}</option>
            <option value="interrupt">{t("立即转向")}</option>
          </NativeSelect>
        )}
        {waiting && (
          <span className="text-xs text-warning mr-1">
            {t("等待人工回复")}
          </span>
        )}
        {!streamConnected && (
          <span title={streamError} className="text-[11px] text-faint mr-1">
            {t("正在连接…")}
          </span>
        )}
        {hasCurrent && !waiting && (
          <button
            type="button"
            title={t("取消执行")}
            aria-label={t("取消执行")}
            onClick={onCancel}
            disabled={cancelPending}
            className="grid size-8 place-items-center rounded-lg text-muted-foreground hover:bg-muted disabled:opacity-40"
          >
            <Square className="size-3" />
          </button>
        )}
        <button
          type="submit"
          title={
            waiting
              ? t("回复并继续")
              : submitMode === "interrupt" && hasCurrent
                ? t("立即转向")
                : t("发送")
          }
          aria-label={
            waiting
              ? t("回复并继续")
              : submitMode === "interrupt" && hasCurrent
                ? t("立即转向")
                : t("发送")
          }
          disabled={disabled}
          className={`inline-flex h-9 shrink-0 items-center justify-center gap-1.5 rounded-full transition disabled:opacity-30 ${waiting ? "px-3" : "w-9"} ${submitMode === "interrupt" && hasCurrent && !waiting ? "bg-rose-600 text-white hover:bg-rose-700" : "bg-action text-action-foreground hover:bg-action-hover"}`}
        >
          {submitMode === "interrupt" && hasCurrent && !waiting ? (
            <Zap className="size-4" />
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
