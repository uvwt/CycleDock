import { Skeleton } from "../../shared/ui/skeleton";

/**
 * 极简灰白看板骨架屏
 */
export function BoardSkeleton() {
  return (
    <div
      aria-busy="true"
      aria-label="正在加载看板"
      className="flex min-h-0 flex-1 flex-col"
    >
      {/* 移动端 (lg:hidden)：分段标签 + 灰白卡片 */}
      <div className="flex min-h-0 flex-1 flex-col lg:hidden space-y-3">
        <div className="flex gap-2">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-8 flex-1 rounded-lg" />
          ))}
        </div>
        <div className="space-y-2.5">
          {Array.from({ length: 3 }).map((_, i) => (
            <div key={i} className="rounded-xl bg-muted/40 p-4 space-y-2.5">
              <Skeleton className="h-4 w-3/4" />
              <Skeleton className="h-3 w-1/2" />
            </div>
          ))}
        </div>
      </div>

      {/* 桌面端 (lg:grid)：4 列简洁灰白泳道 */}
      <div className="hidden min-h-0 flex-1 lg:grid lg:grid-cols-4 lg:gap-4 overflow-hidden">
        {Array.from({ length: 4 }).map((_, colIndex) => (
          <div
            key={colIndex}
            className="flex min-h-0 flex-col rounded-2xl bg-muted/20 p-3 space-y-3"
          >
            <div className="flex items-center justify-between px-1">
              <Skeleton className="h-4 w-16" />
              <Skeleton className="h-4 w-6 rounded-full" />
            </div>

            <div className="space-y-2.5 overflow-hidden">
              {Array.from({
                length: colIndex === 0 ? 3 : colIndex === 1 ? 2 : 1,
              }).map((_, cardIndex) => (
                <div
                  key={cardIndex}
                  className="rounded-xl bg-muted/50 p-3.5 space-y-2.5"
                >
                  <Skeleton className="h-4 w-4/5" />
                  <Skeleton className="h-3 w-3/5" />
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
