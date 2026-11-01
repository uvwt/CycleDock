import { useEffect, useRef } from "react";

export function ComposerInput({
  value,
  onChange,
  placeholder,
  readOnly = false,
  className = "",
}: {
  value: string;
  onChange: (value: string) => void;
  expanded?: boolean;
  onExpandedChange?: (expanded: boolean) => void;
  placeholder: string;
  readOnly?: boolean;
  keepExpanded?: boolean;
  className?: string;
}) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "auto";
    const nextHeight = Math.min(Math.max(el.scrollHeight, 36), 144);
    el.style.height = `${nextHeight}px`;
  }, [value]);

  return (
    <textarea
      ref={textareaRef}
      aria-label={placeholder}
      value={value}
      onChange={(event) => onChange(event.target.value)}
      readOnly={readOnly}
      placeholder={placeholder}
      rows={1}
      className={`min-w-0 flex-1 resize-none bg-transparent py-1.5 px-2 text-[15px] outline-none read-only:text-secondary-foreground overflow-y-auto ${className}`}
      onKeyDown={(event) => {
        // 中文输入法确认候选词和 Shift+Enter 均不触发发送。
        if (
          event.key === "Enter" &&
          !event.shiftKey &&
          !event.nativeEvent.isComposing
        ) {
          event.preventDefault();
          event.currentTarget.form?.requestSubmit();
        }
      }}
    />
  );
}
