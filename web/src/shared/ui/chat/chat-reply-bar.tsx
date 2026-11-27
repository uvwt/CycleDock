import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { formatQuotePreview } from "../../../features/timeline/selectionQuote";
import { QuoteMarkIcon } from "./quote-mark-icon";

export interface ChatReplyBarProps {
  sender: string;
  text: string;
  onClose: () => void;
  className?: string;
}

export function ChatReplyBar({
  sender,
  text,
  onClose,
  className,
}: ChatReplyBarProps) {
  const { t } = useTranslation();
  const previewText = formatQuotePreview(text, 160);
  return (
    <div
      data-testid="chat-reply-bar"
      className={cn(
        "relative mx-1 mt-0.5 mb-1.5 flex items-center justify-between gap-2 rounded-lg bg-slate-900/[0.045] px-2.5 py-1.5 text-xs dark:bg-white/[0.06]",
        className,
      )}
    >
      <div className="flex min-w-0 flex-1 items-center gap-1.5">
        <QuoteMarkIcon className="size-3 shrink-0 text-primary/80 dark:text-sky-300/85" />
        <div className="flex min-w-0 flex-1 items-baseline gap-1.5">
          {sender.trim() && (
            <span className="shrink-0 font-medium text-primary dark:text-sky-300">
              {sender}
            </span>
          )}
          <span
            className="truncate text-primary/85 dark:text-sky-200/85"
            title={text}
          >
            {previewText}
          </span>
        </div>
      </div>
      <button
        type="button"
        onClick={onClose}
        className="grid size-5 shrink-0 cursor-pointer place-items-center rounded-full text-muted-foreground transition hover:bg-slate-900/[0.08] hover:text-foreground active:scale-95 dark:hover:bg-white/[0.1]"
        title={t("取消引用")}
        aria-label={t("取消引用")}
      >
        <X className="size-3.5" />
      </button>
    </div>
  );
}
