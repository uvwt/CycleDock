import type { ReactNode } from "react";
import { SwipeableRow, type SwipeActionItem } from "./SwipeableRow";
import { Skeleton } from "./skeleton";

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
      className={`flex h-full min-h-0 w-full flex-col bg-canvas ${className}`}
      aria-label={ariaLabel}
    >
      {header ? (
        <div className="space-y-2 px-3 pt-3.5 pb-2">{header}</div>
      ) : null}
      <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-2 pb-3">
        {children}
      </div>
    </aside>
  );
}

/**
 * 侧边栏骨架屏
 * 用于在项目切换或初次拉取卡片列表时提供平滑的占位体验
 */
export function ContextSidebarSkeleton({
  ariaLabel = "加载中…",
  itemCount = 6,
  className = "",
}: {
  ariaLabel?: string;
  itemCount?: number;
  className?: string;
}) {
  return (
    <ContextSidebar
      ariaLabel={ariaLabel}
      className={className}
      header={
        <div className="space-y-3 px-1">
          <div className="flex items-center justify-between">
            <Skeleton className="h-4 w-16" />
            <Skeleton className="size-6 rounded-lg" />
          </div>
          <Skeleton className="h-8 w-full rounded-xl" />
        </div>
      }
    >
      <div className="space-y-1.5 pt-1">
        {Array.from({ length: itemCount }).map((_, i) => (
          <div
            key={i}
            className="flex items-center gap-2.5 rounded-xl px-2.5 py-2.5"
          >
            <Skeleton className="size-4 shrink-0 rounded" />
            <div className="flex-1 space-y-1.5 min-w-0">
              <Skeleton
                className={`h-3.5 ${
                  i % 3 === 0 ? "w-4/5" : i % 3 === 1 ? "w-2/3" : "w-3/4"
                }`}
              />
              <Skeleton
                className={`h-2.5 ${i % 2 === 0 ? "w-1/2" : "w-1/3"}`}
              />
            </div>
          </div>
        ))}
      </div>
    </ContextSidebar>
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
  swipeActions,
  onClick,
}: {
  active: boolean;
  icon?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  meta?: ReactNode;
  metaHoverOnly?: boolean;
  actions?: ReactNode;
  swipeActions?: SwipeActionItem[];
  onClick: () => void;
}) {
  const content = (
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
        className={`min-w-0 flex-1 rounded-xl px-3 py-2 text-left transition-colors ${actions ? "group-hover:pr-16 [@media(hover:hover)]:pr-16" : ""}`}
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
      {/* 桌面端 hover 时展示 actions，移动端触控屏避免与手势冲突；滑开抽屉时隐藏 */}
      {actions ? (
        <div className="hidden [@media(hover:hover)]:flex absolute top-1/2 right-1.5 -translate-y-1/2 items-center group-data-[swipe-open=true]/swipe:hidden">
          {actions}
        </div>
      ) : null}
    </div>
  );

  if (swipeActions && swipeActions.length > 0) {
    return (
      <SwipeableRow
        actions={swipeActions}
        className="rounded-xl my-0.5"
        contentClassName={active ? "bg-surface" : "bg-canvas"}
      >
        {content}
      </SwipeableRow>
    );
  }

  return <div className="my-0.5">{content}</div>;
}


