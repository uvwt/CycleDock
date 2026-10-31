import { Reply, X } from "lucide-react";
import { cn } from "../../lib/utils";

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
  return (
    <div
      className={cn(
        "relative mx-1 mt-0.5 mb-1.5 flex items-center justify-between gap-2.5 rounded-xl border border-line/60 border-l-4 border-l-primary bg-muted/75 px-3 py-2 text-xs shadow-xs",
        className,
      )}
    >
      <div className="flex min-w-0 flex-1 items-center gap-2.5">
        <div className="grid size-6 shrink-0 place-items-center rounded-lg bg-primary/10 text-primary">
          <Reply className="size-3.5" />
        </div>
        <div className="min-w-0 flex-1 leading-snug">
          <p className="font-semibold text-primary">{sender}</p>
          <p className="truncate text-muted-foreground">{text}</p>
        </div>
      </div>
      <button
        type="button"
        onClick={onClose}
        className="grid size-6 shrink-0 cursor-pointer place-items-center rounded-full text-muted-foreground transition hover:bg-muted-foreground/10 hover:text-foreground active:scale-95"
        title="取消引用"
        aria-label="取消引用"
      >
        <X className="size-3.5" />
      </button>
    </div>
  );
}
