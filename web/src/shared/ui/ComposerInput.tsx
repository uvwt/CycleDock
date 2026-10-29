export function ComposerInput({
  value,
  onChange,
  expanded,
  onExpandedChange,
  placeholder,
  readOnly = false,
  keepExpanded = false,
  className = "",
}: {
  value: string;
  onChange: (value: string) => void;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  placeholder: string;
  readOnly?: boolean;
  keepExpanded?: boolean;
  className?: string;
}) {
  if (!expanded) {
    return (
      <input
        aria-label={placeholder}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onFocus={() => onExpandedChange(true)}
        readOnly={readOnly}
        placeholder={placeholder}
        className="min-w-0 flex-1 bg-transparent py-2 text-sm outline-none"
      />
    );
  }

  return (
    <textarea
      autoFocus
      aria-label={placeholder}
      value={value}
      onChange={(event) => onChange(event.target.value)}
      readOnly={readOnly}
      placeholder={placeholder}
      rows={3}
      className={`max-h-40 min-h-20 w-full resize-none bg-transparent px-2 py-2 text-sm outline-none read-only:text-secondary ${className}`}
      onBlur={(event) => {
        // 在输入框与表单内按钮之间移动焦点时保持展开，避免按钮卸载吞掉点击。
        if (
          !value.trim() &&
          !keepExpanded &&
          !event.currentTarget.form?.contains(event.relatedTarget)
        ) {
          onExpandedChange(false);
        }
      }}
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
