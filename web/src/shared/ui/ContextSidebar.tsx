import type { ReactNode } from "react";

export function ContextSidebar({
  ariaLabel,
  header,
  children,
  className = "",
}: {
  ariaLabel: string;
  header?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <aside
      className={`flex h-full min-h-0 w-full flex-col bg-canvas/60 backdrop-blur-sm ${className}`}
      aria-label={ariaLabel}
    >
      {header ? (
        <div className="space-y-2 px-3 pt-3.5 pb-2">{header}</div>
      ) : null}
      <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto px-2 pb-3">
        {children}
      </div>
    </aside>
  );
}

export function ContextSidebarItem({
  active,
  icon,
  title,
  description,
  meta,
  metaHoverOnly = false,
  actions,
  onClick,
}: {
  active: boolean;
  icon?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  meta?: ReactNode;
  metaHoverOnly?: boolean;
  actions?: ReactNode;
  onClick: () => void;
}) {
  return (
    <div
      className={`group relative flex w-full items-center rounded-xl transition-all duration-150 ${
        active
          ? "bg-surface text-foreground font-medium shadow-2xs border border-line/70"
          : "text-muted-foreground hover:bg-surface/60 hover:text-foreground border border-transparent"
      }`}
    >
      <button
        type="button"
        onClick={onClick}
        aria-current={active ? "page" : undefined}
        className={`min-w-0 flex-1 rounded-xl px-3 py-2 text-left transition-colors ${actions ? "pr-24" : ""}`}
      >
        <div className={`flex gap-2.5 ${description ? "items-start" : "items-center"}`}>
          {icon ? <div className={`shrink-0 ${description ? "mt-0.5" : ""}`}>{icon}</div> : null}
          <div className="min-w-0 flex-1">
            <div className="flex items-center justify-between gap-1.5">
              <p className="truncate text-[13px] leading-5">
                {title}
              </p>
              {meta && !description ? (
                <span
                  className={`shrink-0 text-[11px] font-normal tabular-nums text-faint group-hover:text-muted-foreground transition-opacity duration-150 ${
                    metaHoverOnly
                      ? "group-focus-within:opacity-100 group-hover:opacity-100 [@media(hover:hover)]:opacity-0"
                      : ""
                  }`}
                >
                  {meta}
                </span>
              ) : null}
            </div>
            {description ? (
              <p className="mt-0.5 line-clamp-2 text-xs font-normal leading-normal text-muted-foreground">
                {description}
              </p>
            ) : null}
            {meta && description ? (
              <p
                className={`mt-0.5 text-[11px] font-normal text-faint transition-opacity duration-150 ${
                  metaHoverOnly
                    ? "group-focus-within:opacity-100 group-hover:opacity-100 [@media(hover:hover)]:opacity-0"
                    : ""
                }`}
              >
                {meta}
              </p>
            ) : null}
          </div>
        </div>
      </button>
      {actions ? (
        <div className="absolute top-1/2 right-1.5 -translate-y-1/2 flex items-center">
          {actions}
        </div>
      ) : null}
    </div>
  );
}
