import { Reply, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { formatQuotePreview } from "../../../features/timeline/selectionQuote";

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
        "relative mx-1 mt-0.5 mb-1.5 flex items-center justify-between gap-2 rounded-xl border border-line/60 border-l-3 border-l-primary bg-muted/75 px-2.5 py-1.5 text-xs shadow-2xs",
        className,
      )}
    >
      <div className="flex min-w-0 flex-1 items-center gap-2">
        <Reply className="size-3.5 shrink-0 text-primary" />
        <div className="flex min-w-0 flex-1 items-baseline gap-1.5">
          <span className="shrink-0 font-semibold text-primary">
            {sender}
          </span>
          <span className="truncate text-muted-foreground" title={text}>
            {previewText}
          </span>
        </div>
      </div>
      <button
        type="button"
        onClick={onClose}
        className="grid size-5 shrink-0 cursor-pointer place-items-center rounded-full text-muted-foreground transition hover:bg-muted-foreground/10 hover:text-foreground active:scale-95"
        title={t("取消引用")}
        aria-label={t("取消引用")}
      >
        <X className="size-3.5" />
      </button>
    </div>
  );
}
