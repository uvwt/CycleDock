import type { ReactNode } from "react";
import type { Project } from "../api/types";

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

export function ContextProjectSwitcher({
  projects,
  projectID,
  ariaLabel,
  onChange,
  action,
}: {
  projects: Project[];
  projectID: string;
  ariaLabel: string;
  onChange: (projectID: string) => void;
  action?: ReactNode;
}) {
  return (
    <div className="flex items-center gap-2">
      <select
        aria-label={ariaLabel}
        value={projectID}
        onChange={(event) => onChange(event.target.value)}
        className="min-w-0 flex-1 rounded-xl border border-slate-200 bg-white px-3 py-2 text-sm outline-none focus:border-sky-500"
      >
        {projects.map((project) => (
          <option key={project.id} value={project.id}>
            {project.name}
          </option>
        ))}
      </select>
      {action}
    </div>
  );
}

export function ContextSidebarItem({
  active,
  icon,
  title,
  description,
  meta,
  onClick,
}: {
  active: boolean;
  icon?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  meta?: ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={active ? "page" : undefined}
      className={`w-full rounded-xl px-3 py-3 text-left transition ${
        active
          ? "bg-white shadow-sm ring-1 ring-slate-200"
          : "hover:bg-white/80"
      }`}
    >
      <div className="flex items-start gap-2.5">
        {icon ? <div className="mt-0.5 shrink-0">{icon}</div> : null}
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-slate-800">{title}</p>
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
  );
}
