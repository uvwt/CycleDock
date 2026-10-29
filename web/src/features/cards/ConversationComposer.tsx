import { ComposerInput } from "../../shared/ui/ComposerInput";
import { ArrowUp, ListPlus, Sparkles, Square, Zap } from "lucide-react";
import { useTranslation } from "react-i18next";

export function ConversationComposer({
  comment,
  setComment,
  expanded,
  setExpanded,
  blocked,
  waiting,
  hasCurrent,
  submitMode,
  setSubmitMode,
  sendPending,
  canGenerate,
  generatePending,
  streamConnected,
  streamError,
  cancelPending,
  onSubmit,
  onGenerate,
  onCancel,
}: {
  comment: string;
  setComment: (value: string) => void;
  expanded: boolean;
  setExpanded: (value: boolean) => void;
  blocked: boolean;
  waiting: boolean;
  hasCurrent: boolean;
  submitMode: "enqueue" | "interrupt";
  setSubmitMode: (value: "enqueue" | "interrupt") => void;
  sendPending: boolean;
  canGenerate: boolean;
  generatePending: boolean;
  streamConnected: boolean;
  streamError?: string;
  cancelPending: boolean;
  onSubmit: () => void;
  onGenerate: () => void;
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
      className={`relative border bg-surface transition-all focus-within:border-line-strong ${expanded ? "rounded-2xl border-line p-2 shadow-sm" : "flex items-center rounded-full border-line py-1 pr-1 pl-4 shadow-sm"}`}
    >
      <ComposerInput
        value={comment}
        onChange={setComment}
        expanded={expanded}
        onExpandedChange={setExpanded}
        placeholder={placeholder}
        readOnly={blocked}
        keepExpanded={hasCurrent}
        className="pr-12"
      />
      {expanded ? (
        <>
          {canGenerate && (
            <button
              type="button"
              title={t("生成任务")}
              aria-label={t("生成任务")}
              disabled={generatePending}
              onClick={onGenerate}
              className="absolute top-2.5 right-2.5 grid size-8 place-items-center rounded-lg text-sky-700 transition hover:bg-sky-50 disabled:opacity-40"
            >
              <Sparkles className="size-4" />
            </button>
          )}
          <div className="flex min-h-9 flex-wrap items-center justify-between gap-2 px-1 pb-1">
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              {hasCurrent && !waiting && (
                <select
                  aria-label={t("任务发送方式")}
                  value={submitMode}
                  onChange={(event) =>
                    setSubmitMode(event.target.value as "enqueue" | "interrupt")
                  }
                  className="rounded-lg border border-line bg-surface px-2 py-1.5 text-xs text-secondary"
                >
                  <option value="enqueue">{t("加入队列")}</option>
                  <option value="interrupt">{t("立即转向")}</option>
                </select>
              )}
              {waiting && (
                <span className="text-xs text-warning">
                  {t("等待人工回复")}
                </span>
              )}
              {!streamConnected && (
                <span title={streamError} className="text-[11px] text-faint">
                  {t("正在连接实时通道…")}
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
            </div>
            <button
              type="submit"
              title={
                waiting
                  ? t("回复并继续")
                  : submitMode === "interrupt"
                    ? t("立即转向")
                    : t("发送")
              }
              aria-label={
                waiting
                  ? t("回复并继续")
                  : submitMode === "interrupt"
                    ? t("立即转向")
                    : t("发送")
              }
              disabled={disabled}
              className={`inline-flex h-9 shrink-0 items-center justify-center gap-1.5 rounded-full transition disabled:opacity-30 ${waiting ? "px-3" : "w-9"} ${submitMode === "interrupt" && !waiting ? "bg-rose-600 text-white hover:bg-rose-700" : "bg-action text-action-foreground hover:bg-action-hover"}`}
            >
              {submitMode === "interrupt" && !waiting ? (
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
        </>
      ) : (
        <>
          {canGenerate && (
            <button
              type="button"
              title={t("生成任务")}
              aria-label={t("生成任务")}
              disabled={generatePending}
              onClick={onGenerate}
              className="grid size-9 shrink-0 place-items-center rounded-full text-sky-700 hover:bg-sky-50 disabled:opacity-40"
            >
              <Sparkles className="size-4" />
            </button>
          )}
          <button
            type="submit"
            title={t(waiting ? "回复并继续" : "发送")}
            aria-label={t(waiting ? "回复并继续" : "发送")}
            disabled={disabled}
            className={`inline-flex h-9 shrink-0 items-center justify-center gap-1.5 rounded-full bg-action text-action-foreground transition hover:bg-action-hover disabled:opacity-30 ${waiting ? "px-3" : "w-9"}`}
          >
            {waiting ? (
              <ListPlus className="size-4" />
            ) : (
              <ArrowUp className="size-4" />
            )}
            {waiting && (
              <span className="text-xs font-medium">{t("回复并继续")}</span>
            )}
          </button>
        </>
      )}
    </form>
  );
}
