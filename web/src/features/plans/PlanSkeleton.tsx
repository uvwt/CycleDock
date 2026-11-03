import { Skeleton } from "../../shared/ui/skeleton";

/**
 * 极简灰白定时任务骨架屏
 */
export function PlanSkeleton() {
  return (
    <div aria-busy="true" aria-label="正在加载定时任务" className="space-y-4">
      {/* 筛选标签栏骨架 */}
      <div className="flex gap-2">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-7 w-20 rounded-lg" />
        ))}
      </div>

      {/* 定时卡片列表骨架 */}
      <div className="space-y-2.5 pt-2">
        {Array.from({ length: 4 }).map((_, i) => (
          <div
            key={i}
            className="flex items-center justify-between rounded-xl bg-muted/40 p-4"
          >
            <div className="space-y-2 flex-1">
              <Skeleton className="h-4 w-44" />
              <Skeleton className="h-3 w-64" />
            </div>
            <Skeleton className="h-6 w-16 rounded-full" />
          </div>
        ))}
      </div>
    </div>
  );
}
