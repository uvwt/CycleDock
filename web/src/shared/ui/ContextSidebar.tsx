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
      className={`flex h-full min-h-0 w-full flex-col bg-slate-50/80 ${className}`}
      aria-label={ariaLabel}
    >
      {header ? (
        <div className="space-y-3 border-b border-slate-200 p-3">{header}</div>
      ) : null}
      <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto p-2">
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
  actions,
  onClick,
}: {
  active: boolean;
  icon?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
  onClick: () => void;
}) {
  return (
    <div
      className={`group relative flex w-full items-center rounded-xl transition ${
        active
          ? "bg-white shadow-sm ring-1 ring-slate-200"
          : "hover:bg-white/80"
      }`}
    >
      <button
        type="button"
        onClick={onClick}
        aria-current={active ? "page" : undefined}
        className={`min-w-0 flex-1 rounded-xl px-3 py-3 text-left ${actions ? "pr-12" : ""}`}
      >
        <div className="flex items-start gap-2.5">
          {icon ? <div className="mt-0.5 shrink-0">{icon}</div> : null}
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium text-slate-800">
              {title}
            </p>
            {description ? (
              <p className="mt-1 line-clamp-2 text-xs leading-5 text-slate-500">
                {description}
              </p>
            ) : null}
            {meta ? (
              <p className="mt-1.5 text-[11px] text-slate-400">{meta}</p>
            ) : null}
          </div>
        </div>
      </button>
      {actions ? (
        <div className="absolute top-1/2 right-2 -translate-y-1/2 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 has-[[data-state=open]]:opacity-100 [@media(hover:hover)]:opacity-0">
          {actions}
        </div>
      ) : null}
    </div>
  );
}
