import * as React from "react";
import { cn } from "../../lib/utils";

export interface MessageLoadingProps
  extends React.HTMLAttributes<HTMLDivElement> {
  size?: "sm" | "default" | "lg";
}

export const MessageLoading = React.forwardRef<
  HTMLDivElement,
  MessageLoadingProps
>(({ className, size = "default", ...props }, ref) => {
  const dotSize =
    size === "sm" ? "size-1.5" : size === "lg" ? "size-2.5" : "size-2";

  return (
    <div
      ref={ref}
      role="status"
      aria-label="正在思考并生成回复"
      className={cn("flex items-center gap-1.5 py-1 px-0.5", className)}
      {...props}
    >
      <span
        className={cn(
          "rounded-full bg-primary/70 animate-bounce [animation-delay:-0.32s]",
          dotSize,
        )}
      />
      <span
        className={cn(
          "rounded-full bg-primary/70 animate-bounce [animation-delay:-0.16s]",
          dotSize,
        )}
      />
      <span
        className={cn(
          "rounded-full bg-primary/70 animate-bounce",
          dotSize,
        )}
      />
    </div>
  );
});
MessageLoading.displayName = "MessageLoading";

