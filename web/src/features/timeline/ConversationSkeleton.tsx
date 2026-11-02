import { PanelLeft } from "lucide-react";
import { Skeleton } from "../../shared/ui/skeleton";

/**
 * 极简灰白会话骨架屏
 * 保持克制、纯粹的灰白占位条与气泡块，不使用彩色或复杂多层卡片
 */
export function ConversationSkeleton({
  onOpenSidebar,
}: {
  isDiscussion?: boolean;
  onOpenSidebar?: () => void;
}) {
  return (
    <div
      aria-busy="true"
      aria-label="正在加载会话"
      className="relative flex flex-1 min-h-0 min-w-0 flex-col overflow-hidden"
    >
      {/* 手机端悬浮顶部栏 */}
      <div
        data-mobile-top-surface
        className="absolute top-0 inset-x-0 z-20 flex items-center justify-between px-3 pt-[calc(env(safe-area-inset-top,0px)+0.75rem)] pb-3 sm:px-4 md:hidden pointer-events-none bg-transparent"
      >
        <div className="flex items-center gap-1.5 pointer-events-auto">
          <Skeleton className="h-7 w-24 rounded-lg" />
          {onOpenSidebar && (
            <button
              type="button"
              onClick={onOpenSidebar}
              className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg border border-line bg-surface text-muted-foreground shadow-xs"
              aria-label="打开会话列表"
            >
              <PanelLeft className="size-4" />
            </button>
          )}
        </div>
        <div className="flex items-center gap-1.5 pointer-events-auto">
          <Skeleton className="size-8 rounded-lg" />
          <Skeleton className="size-8 rounded-lg" />
        </div>
      </div>

      {/* 桌面端右上角悬浮操作按钮占位 */}
      <div className="hidden md:flex absolute top-3 right-3 z-20 items-center gap-1.5 sm:right-4">
        <Skeleton className="h-8 w-20 rounded-lg" />
        <Skeleton className="size-8 rounded-lg" />
      </div>

      {/* 消息时间线灰白气泡流 */}
      <div className="scrollbar-thin relative flex min-h-0 w-full flex-1 flex-col overflow-y-auto px-4 pt-[calc(env(safe-area-inset-top,0px)+4.5rem)] pb-6 sm:px-8 sm:pt-20 sm:pb-32 space-y-7">
        <div className="mx-auto w-full max-w-3xl space-y-7">
          {/* 用户发言气泡（右对齐，纯灰白块） */}
          <div className="ml-auto w-52 max-w-[70%]">
            <Skeleton className="h-12 w-full rounded-2xl rounded-tr-xs" />
          </div>

          {/* Agent 回复（左对齐，头像 + 极简灰白文本行） */}
          <div className="flex items-start gap-3 max-w-2xl">
            <Skeleton className="size-8 rounded-full shrink-0" />
            <div className="flex-1 space-y-2.5 pt-1">
              <Skeleton className="h-3.5 w-36" />
              <Skeleton className="h-3.5 w-full" />
              <Skeleton className="h-3.5 w-4/5" />
            </div>
          </div>

          {/* 第二轮用户发言 */}
          <div className="ml-auto w-40 max-w-[60%]">
            <Skeleton className="h-10 w-full rounded-2xl rounded-tr-xs" />
          </div>

          {/* 第二轮 Agent 简短回复 */}
          <div className="flex items-start gap-3 max-w-xl">
            <Skeleton className="size-8 rounded-full shrink-0" />
            <div className="flex-1 space-y-2.5 pt-1">
              <Skeleton className="h-3.5 w-full" />
              <Skeleton className="h-3.5 w-3/5" />
            </div>
          </div>
        </div>
      </div>

      {/* 底部输入框停靠栏占位（单个灰白容器） */}
      <div
        data-composer-dock
        className="pointer-events-none z-30 shrink-0 w-full flex justify-center border-t border-line/60 bg-surface px-3 pt-2 pb-[max(0.5rem,env(safe-area-inset-bottom,0px))] sm:absolute sm:inset-x-0 sm:bottom-0 sm:border-t-0 sm:bg-transparent sm:px-6 sm:pb-3"
      >
        <Skeleton className="h-14 w-full max-w-3xl rounded-2xl" />
      </div>
    </div>
  );
}
