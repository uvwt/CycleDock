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

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
      className={`relative border bg-white transition-all focus-within:border-sky-300 ${expanded ? "rounded-2xl border-slate-200 p-2 shadow-sm" : "flex items-center rounded-full border-slate-200 py-1 pr-1 pl-4 shadow-sm"}`}
    >
      {expanded ? (
        <>
          <textarea
            autoFocus
            aria-label={placeholder}
            value={comment}
            onChange={(event) => setComment(event.target.value)}
            readOnly={blocked}
            placeholder={placeholder}
            rows={3}
            className="max-h-40 min-h-20 w-full resize-none bg-transparent px-2 pt-2 pr-12 pb-1 text-sm outline-none"
            onBlur={(event) => {
              const next = event.relatedTarget as Node | null;
              if (
                !comment.trim() &&
                !hasCurrent &&
                !event.currentTarget.form?.contains(next)
              ) {
                setExpanded(false);
              }
            }}
            onKeyDown={(event) => {
              if (
                event.key === "Enter" &&
                !event.shiftKey &&
                !event.nativeEvent.isComposing
              ) {
                event.preventDefault();
                onSubmit();
              }
            }}
          />
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
                  className="rounded-lg border border-slate-200 bg-white px-2 py-1.5 text-xs text-slate-600"
                >
                  <option value="enqueue">{t("加入队列")}</option>
                  <option value="interrupt">{t("立即转向")}</option>
                </select>
              )}
              {waiting && (
                <span className="text-xs text-amber-600">
                  {t("等待人工回复")}
                </span>
              )}
              {!streamConnected && (
                <span
                  title={streamError}
                  className="text-[11px] text-slate-400"
                >
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
                  className="grid size-8 place-items-center rounded-lg text-slate-500 hover:bg-slate-100 disabled:opacity-40"
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
              disabled={sendPending || !comment.trim() || blocked}
              className={`grid size-9 shrink-0 place-items-center rounded-full text-white transition disabled:opacity-30 ${submitMode === "interrupt" && !waiting ? "bg-rose-600 hover:bg-rose-700" : "bg-slate-900 hover:bg-slate-700"}`}
            >
              {submitMode === "interrupt" && !waiting ? (
                <Zap className="size-4" />
              ) : waiting ? (
                <ListPlus className="size-4" />
              ) : (
                <ArrowUp className="size-4" />
              )}
            </button>
          </div>
        </>
      ) : (
        <>
          <input
            aria-label={placeholder}
            value={comment}
            onChange={(event) => setComment(event.target.value)}
            onFocus={() => setExpanded(true)}
            readOnly={blocked}
            placeholder={placeholder}
            className="min-w-0 flex-1 bg-transparent py-2 text-sm outline-none"
          />
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
            title={t("发送")}
            aria-label={t("发送")}
            disabled={sendPending || !comment.trim() || blocked}
            className="grid size-9 shrink-0 place-items-center rounded-full bg-slate-900 text-white transition hover:bg-slate-700 disabled:opacity-30"
          >
            <ArrowUp className="size-4" />
          </button>
        </>
      )}
    </form>
  );
}
