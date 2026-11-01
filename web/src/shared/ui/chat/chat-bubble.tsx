import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Check, CheckCheck } from "lucide-react";
import { cn } from "../../lib/utils";

const chatBubbleVariants = cva(
  "flex gap-2 max-w-[85%] w-fit relative group",
  {
    variants: {
      variant: {
        received: "self-start items-start",
        sent: "self-end flex-row-reverse items-start ml-auto",
      },
      layout: {
        default: "",
        ai: "max-w-full w-full",
      },
    },
    defaultVariants: {
      variant: "received",
      layout: "default",
    },
  },
);

export interface ChatBubbleProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof chatBubbleVariants> {}

export const ChatBubble = React.forwardRef<HTMLDivElement, ChatBubbleProps>(
  ({ className, variant, layout, children, ...props }, ref) => {
    return (
      <div
        ref={ref}
        className={cn(chatBubbleVariants({ variant, layout }), className)}
        {...props}
      >
        {children}
      </div>
    );
  },
);
ChatBubble.displayName = "ChatBubble";

export interface ChatBubbleAvatarProps
  extends React.HTMLAttributes<HTMLDivElement> {
  src?: string;
  fallback?: string;
  icon?: React.ReactNode;
}

export const ChatBubbleAvatar = React.forwardRef<
  HTMLDivElement,
  ChatBubbleAvatarProps
>(({ className, src, fallback, icon, ...props }, ref) => {
  return (
    <div
      ref={ref}
      className={cn(
        "relative mt-0.5 flex size-8 shrink-0 select-none items-center justify-center overflow-hidden rounded-full bg-muted text-xs font-medium text-secondary-foreground shadow-xs",
        className,
      )}
      {...props}
    >
      {src ? (
        <img src={src} alt="Avatar" className="size-full object-cover" />
      ) : icon ? (
        icon
      ) : (
        <span>{fallback || "?"}</span>
      )}
    </div>
  );
});
ChatBubbleAvatar.displayName = "ChatBubbleAvatar";

const chatBubbleMessageVariants = cva(
  "relative max-w-full break-words text-[15px] shadow-xs",
  {
    variants: {
      variant: {
        received:
          "rounded-2xl bg-surface px-4 py-2.5 text-foreground border border-line",
        sent: "rounded-2xl bg-muted/80 dark:bg-muted/50 text-foreground border border-line px-4 py-2.5",
      },
      layout: {
        default: "",
        ai: "border-0 bg-transparent p-0 shadow-none",
      },
    },
    defaultVariants: {
      variant: "received",
      layout: "default",
    },
  },
);

export interface ChatBubbleMessageProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof chatBubbleMessageVariants> {
  isLoading?: boolean;
}

export const ChatBubbleMessage = React.forwardRef<
  HTMLDivElement,
  ChatBubbleMessageProps
>(({ className, variant, layout, isLoading, children, ...props }, ref) => {
  return (
    <div
      ref={ref}
      className={cn(chatBubbleMessageVariants({ variant, layout }), className)}
      {...props}
    >
      {isLoading ? (
        <div className="flex items-center gap-1.5 py-1">
          <span className="size-1.5 animate-bounce rounded-full bg-current [animation-delay:-0.3s]" />
          <span className="size-1.5 animate-bounce rounded-full bg-current [animation-delay:-0.15s]" />
          <span className="size-1.5 animate-bounce rounded-full bg-current" />
        </div>
      ) : (
        children
      )}
    </div>
  );
});
ChatBubbleMessage.displayName = "ChatBubbleMessage";

export interface ChatBubbleTimestampProps
  extends React.HTMLAttributes<HTMLDivElement> {
  timestamp: string;
  status?: "sending" | "sent" | "delivered" | "read";
}

export const ChatBubbleTimestamp = React.forwardRef<
  HTMLDivElement,
  ChatBubbleTimestampProps
>(({ className, timestamp, status, ...props }, ref) => {
  return (
    <div
      ref={ref}
      className={cn(
        "mt-1 flex items-center justify-end gap-1 select-none text-[11px] text-muted-foreground",
        className,
      )}
      {...props}
    >
      <span>{timestamp}</span>
      {status === "sent" && <Check className="size-3 text-muted-foreground" />}
      {status === "delivered" && (
        <CheckCheck className="size-3 text-muted-foreground" />
      )}
      {status === "read" && <CheckCheck className="size-3 text-primary" />}
    </div>
  );
});
ChatBubbleTimestamp.displayName = "ChatBubbleTimestamp";

export interface ChatBubbleActionProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  icon: React.ReactNode;
}

export const ChatBubbleAction = React.forwardRef<
  HTMLButtonElement,
  ChatBubbleActionProps
>(({ className, icon, ...props }, ref) => {
  return (
    <button
      ref={ref}
      type="button"
      className={cn(
        "inline-flex size-7 cursor-pointer items-center justify-center rounded-lg border border-line/60 bg-surface text-muted-foreground shadow-xs transition hover:bg-muted hover:text-foreground active:scale-95",
        className,
      )}
      {...props}
    >
      {icon}
    </button>
  );
});
ChatBubbleAction.displayName = "ChatBubbleAction";

export const ChatBubbleActionWrapper = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement>
>(({ className, children, ...props }, ref) => {
  return (
    <div
      ref={ref}
      className={cn(
        "flex items-center gap-1 self-end pb-1 opacity-0 transition-opacity group-hover:opacity-100",
        className,
      )}
      {...props}
    >
      {children}
    </div>
  );
});
ChatBubbleActionWrapper.displayName = "ChatBubbleActionWrapper";

