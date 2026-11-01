import * as React from "react";
import { cn } from "../../lib/utils";

export interface ChatMessageListProps
  extends React.HTMLAttributes<HTMLDivElement> {
  smooth?: boolean;
}

export const ChatMessageList = React.forwardRef<
  HTMLDivElement,
  ChatMessageListProps
>(({ className, children, smooth = false, ...props }, ref) => {
  return (
    <div
      ref={ref}
      className={cn(
        "scrollbar-thin relative flex min-h-0 w-full flex-1 flex-col overflow-y-auto px-4 pt-16 pb-6 sm:px-6 sm:pt-20",
        smooth && "scroll-smooth",
        className,
      )}
      role="log"
      {...props}
    >
      <div className="mx-auto flex w-full max-w-3xl flex-col">
        {children}
      </div>
    </div>
  );
});
ChatMessageList.displayName = "ChatMessageList";

