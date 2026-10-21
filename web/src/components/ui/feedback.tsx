import {
  useEffect,
  useState,
  type ReactNode,
  type HTMLAttributes,
} from "react";
import { AlertCircle, Inbox, RefreshCw } from "lucide-react";
import { Button, Typography } from "./controls";
import { cn } from "./utils";

/** 延迟首屏骨架，快速请求不产生闪烁；由页面仅在首次加载时挂载。 */
export function DelayedSkeleton({
  children,
  delay = 180,
}: {
  children: ReactNode;
  delay?: number;
}) {
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    const id = window.setTimeout(() => setVisible(true), delay);
    return () => window.clearTimeout(id);
  }, [delay]);
  return visible ? <>{children}</> : null;
}

export function Skeleton({
  className,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      aria-hidden="true"
      className={cn("v2-skeleton", className)}
      {...props}
    />
  );
}
export function EmptyState({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center gap-3 px-4 py-10 text-center">
      <Inbox size={24} color="var(--v2-muted)" aria-hidden="true" />
      <Typography as="h2" scale="section">
        {title}
      </Typography>
      {description && (
        <Typography scale="control" className="text-[var(--v2-muted)]">
          {description}
        </Typography>
      )}
      {action}
    </div>
  );
}
export function ErrorState({
  title = "加载失败",
  message,
  onRetry,
}: {
  title?: string;
  message?: string;
  onRetry?: () => void;
}) {
  return (
    <div
      role="alert"
      className="flex flex-col items-center gap-3 px-4 py-10 text-center"
    >
      <AlertCircle size={24} color="var(--v2-danger)" aria-hidden="true" />
      <Typography as="h2" scale="section">
        {title}
      </Typography>
      {message && (
        <Typography scale="control" className="text-[var(--v2-muted)]">
          {message}
        </Typography>
      )}
      {onRetry && (
        <Button onClick={onRetry}>
          <RefreshCw size={14} aria-hidden="true" />
          重试
        </Button>
      )}
    </div>
  );
}

const lines = (widths: string[]) =>
  widths.map((width, index) => (
    <Skeleton key={index} className="h-3" style={{ width }} />
  ));
export function ProjectSwitcherSkeleton() {
  return (
    <div className="flex items-center gap-2 p-2">
      <Skeleton className="size-7" />
      <div className="min-w-0 flex-1 space-y-2">{lines(["70%", "45%"])}</div>
    </div>
  );
}
export function DiscussionListSkeleton() {
  return (
    <div className="space-y-2" aria-label="正在加载讨论">
      {Array.from({ length: 5 }, (_, i) => (
        <div key={i} className="space-y-2 p-3">
          {lines([i % 2 ? "78%" : "92%", "42%"])}
        </div>
      ))}
    </div>
  );
}
export function SidebarSkeleton() {
  return (
    <aside
      className="v2-sidebar flex h-full min-h-[380px] flex-col gap-3 p-3"
      aria-label="正在加载侧栏"
    >
      <Skeleton className="h-8 w-28" />
      <Skeleton className="h-9 w-full" />
      <Skeleton className="h-9 w-full" />
      <DiscussionListSkeleton />
      <div className="mt-auto">
        <ProjectSwitcherSkeleton />
      </div>
    </aside>
  );
}
export function ThreadSkeleton() {
  return (
    <div
      className="mx-auto w-full max-w-2xl space-y-7 px-3 py-6"
      aria-label="正在加载消息"
    >
      {Array.from({ length: 5 }, (_, i) => (
        <div
          key={i}
          className={cn(
            "flex gap-3",
            i % 2 === 0 ? "justify-start" : "justify-end",
          )}
        >
          {i % 2 === 0 && <Skeleton className="size-8 shrink-0" />}
          <div
            className={cn(
              "w-[70%] max-w-88 space-y-2 rounded-[var(--v2-radius)] p-3",
              i % 2 ? "bg-[var(--v2-subtle)]" : "v2-panel",
            )}
          >
            {lines(i % 2 ? ["85%", "55%"] : ["95%", "90%", "42%"])}
          </div>
        </div>
      ))}
    </div>
  );
}
export function BoardSkeleton() {
  return (
    <div
      className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4"
      aria-label="正在加载看板"
    >
      {["待办", "进行中", "待审核", "已完成"].map((name, i) => (
        <div className="v2-panel space-y-3 p-3" key={name}>
          <div className="flex justify-between">
            <span className="v2-text-control">{name}</span>
            <Skeleton className="h-4 w-6" />
          </div>
          {Array.from({ length: i === 3 ? 1 : 3 }, (_, j) => (
            <div className="v2-panel space-y-3 p-3" key={j}>
              {lines(["85%", "60%"])}
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}
export function CardDetailSkeleton() {
  return (
    <div
      className="mx-auto w-full max-w-3xl space-y-5 p-4"
      aria-label="正在加载卡片"
    >
      <Skeleton className="h-7 w-2/3" />
      <Skeleton className="h-5 w-24" />
      <ThreadSkeleton />
      <Skeleton className="h-10 w-full" />
    </div>
  );
}
export function SettingsSkeleton() {
  return (
    <div
      className="grid min-w-0 gap-5 md:grid-cols-[170px_minmax(0,1fr)]"
      aria-label="正在加载设置"
    >
      <div className="space-y-3">
        {Array.from({ length: 6 }, (_, i) => (
          <Skeleton key={i} className="h-8 w-full" />
        ))}
      </div>
      <div className="v2-panel space-y-5 p-5">
        {lines(["40%", "75%"])}
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-full" />
        {lines(["65%"])}
      </div>
    </div>
  );
}
