import * as React from "react";
import { cn } from "../../lib/utils";

export interface ChatInputProps
  extends Omit<React.TextareaHTMLAttributes<HTMLTextAreaElement>, "onChange"> {
  value: string;
  onChange: (value: string) => void;
  onEnterSubmit?: () => void;
}

export const ChatInput = React.forwardRef<HTMLTextAreaElement, ChatInputProps>(
  (
    {
      value,
      onChange,
      onEnterSubmit,
      placeholder = "",
      readOnly = false,
      rows = 1,
      className,
      onKeyDown,
      ...props
    },
    forwardedRef,
  ) => {
    const internalRef = React.useRef<HTMLTextAreaElement>(null);
    const textareaRef =
      (forwardedRef as React.RefObject<HTMLTextAreaElement | null>) ||
      internalRef;

    React.useEffect(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.style.height = "auto";
      const nextHeight = Math.min(Math.max(el.scrollHeight, 36), 160);
      el.style.height = `${nextHeight}px`;
    }, [value, textareaRef]);

    return (
      <textarea
        ref={textareaRef}
        autoComplete="off"
        rows={rows}
        aria-label={placeholder}
        placeholder={placeholder}
        value={value}
        readOnly={readOnly}
        onChange={(event) => onChange(event.target.value)}
        className={cn(
          "max-h-40 min-h-[36px] w-full resize-none bg-transparent py-1.5 px-2 text-[15px] outline-none placeholder:text-muted-foreground focus-visible:outline-none focus:outline-none disabled:cursor-not-allowed disabled:opacity-50 read-only:text-secondary-foreground overflow-y-auto",
          className,
        )}
        onKeyDown={(event) => {
          onKeyDown?.(event);
          if (
            event.key === "Enter" &&
            !event.shiftKey &&
            !event.nativeEvent.isComposing
          ) {
            event.preventDefault();
            if (onEnterSubmit) {
              onEnterSubmit();
            } else {
              event.currentTarget.form?.requestSubmit();
            }
          }
        }}
        {...props}
      />
    );
  },
);
ChatInput.displayName = "ChatInput";
