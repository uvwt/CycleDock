import { Skeleton } from "../../shared/ui/skeleton";

/**
 * 极简灰白活动记录骨架屏
 */
export function ActivitySkeleton() {
  return (
    <div aria-busy="true" aria-label="正在加载活动记录" className="space-y-4">
      {/* 筛选标签栏骨架 */}
      <div className="flex gap-2">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-7 w-18 rounded-lg" />
        ))}
      </div>

      {/* 活动项列表骨架 */}
      <div className="space-y-2.5 pt-2">
        {Array.from({ length: 5 }).map((_, i) => (
          <div
            key={i}
            className="flex items-center gap-3.5 rounded-xl bg-muted/40 p-4"
          >
            <Skeleton className="size-8 rounded-lg shrink-0" />
            <div className="flex-1 min-w-0 space-y-2">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="h-3 w-3/5" />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
