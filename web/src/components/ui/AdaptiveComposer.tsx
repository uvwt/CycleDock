import {
  type FormEvent,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { ArrowUp, LoaderCircle } from "lucide-react";
import { cn } from "./utils";

export function AdaptiveComposer({
  value,
  onChange,
  onSubmit,
  placeholder,
  ariaLabel,
  expanded,
  onExpandedChange,
  busy = false,
  disabled = false,
  keepExpanded = false,
  actions,
  compactAction,
  meta,
  sendLabel,
  className,
}: {
  value: string;
  onChange: (value: string) => void;
  onSubmit: () => void;
  placeholder: string;
  ariaLabel: string;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  busy?: boolean;
  disabled?: boolean;
  keepExpanded?: boolean;
  actions?: ReactNode;
  compactAction?: ReactNode;
  meta?: ReactNode;
  sendLabel: string;
  className?: string;
}) {
  const canSend = !!value.trim() && !busy && !disabled;

  function submit(event: FormEvent) {
    event.preventDefault();
    if (canSend) onSubmit();
  }

  function keyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (
      event.key === "Enter" &&
      !event.shiftKey &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault();
      if (canSend) onSubmit();
    }
  }

  return (
    <form
      className={cn("product-composer", className)}
      data-expanded={expanded ? "true" : "false"}
      onSubmit={submit}
      onBlur={(event) => {
        const next = event.relatedTarget as Node | null;
        if (
          !value.trim() &&
          !keepExpanded &&
          !event.currentTarget.contains(next)
        ) {
          onExpandedChange(false);
        }
      }}
    >
      <textarea
        rows={expanded ? 3 : 1}
        value={value}
        readOnly={disabled}
        aria-label={ariaLabel}
        placeholder={placeholder}
        onFocus={() => onExpandedChange(true)}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={keyDown}
        className="product-composer-input"
      />

      {!expanded && compactAction && (
        <div className="product-composer-compact-action">{compactAction}</div>
      )}

      {expanded && (
        <div className="product-composer-toolbar">
          <div className="product-composer-context">
            {actions}
            {meta && <div className="product-composer-meta">{meta}</div>}
          </div>
          <button
            type="submit"
            className="product-composer-send"
            disabled={!canSend}
            aria-label={sendLabel}
            title={sendLabel}
          >
            {busy ? (
              <LoaderCircle size={16} className="animate-spin" aria-hidden="true" />
            ) : (
              <ArrowUp size={17} aria-hidden="true" />
            )}
          </button>
        </div>
      )}

      {!expanded && (
        <button
          type="submit"
          className="product-composer-send product-composer-send-compact"
          disabled={!canSend}
          aria-label={sendLabel}
          title={sendLabel}
        >
          {busy ? (
            <LoaderCircle size={16} className="animate-spin" aria-hidden="true" />
          ) : (
            <ArrowUp size={17} aria-hidden="true" />
          )}
        </button>
      )}
    </form>
  );
}
