import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { cn } from "../lib/utils";

interface SwipeActionGroupContextValue {
  activeId: string | null;
  setActiveId: (id: string | null) => void;
}

const SwipeActionGroupContext = createContext<SwipeActionGroupContextValue | null>(
  null,
);

/**
 * 可选的排他互斥容器。
 * 如果需要在某个列表容器内确保同一时间只滑开一个 item，可用 SwipeActionGroup 包裹。
 * 即使不包裹，SwipeableRow 也有全局兜底排他逻辑。
 */
export function SwipeActionGroup({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  const [activeId, setActiveId] = useState<string | null>(null);

  return (
    <SwipeActionGroupContext.Provider value={{ activeId, setActiveId }}>
      <div className={className}>{children}</div>
    </SwipeActionGroupContext.Provider>
  );
}

// 全局排他互斥总线（针对未被 SwipeActionGroup 包裹的场景）
const globalSwipeListeners = new Set<(openedId: string | null) => void>();
let globalOpenedSwipeId: string | null = null;

function notifyGlobalSwipeOpen(id: string | null) {
  globalOpenedSwipeId = id;
  globalSwipeListeners.forEach((fn) => fn(id));
}

export interface SwipeActionItem {
  key: string;
  label: string;
  icon?: React.ReactNode;
  onClick: (e: React.MouseEvent) => void;
  className?: string;
  variant?: "default" | "destructive" | "warning" | "primary" | "danger";
}

export interface SwipeableRowProps {
  children: React.ReactNode;
  /**
   * 右侧展开的快捷操作按钮列表（向左滑时露出）
   */
  actions?: SwipeActionItem[];
  /**
   * 自定义唯一标识（用于互斥排他）
   */
  id?: string;
  /**
   * 是否禁用滑动操作（例如在 PC 端或特定状态下）
   */
  disabled?: boolean;
  className?: string;
  contentClassName?: string;
  /**
   * 触发阈值（达到操作区宽度的比例判定吸附展开，默认 0.35）
   */
  threshold?: number;
  /**
   * 展开或收起状态变更回调
   */
  onOpenChange?: (open: boolean) => void;
}

export function SwipeableRow({
  children,
  actions = [],
  id: propId,
  disabled = false,
  className,
  contentClassName,
  threshold = 0.35,
  onOpenChange,
}: SwipeableRowProps) {
  const autoId = useId();
  const itemId = propId || autoId;

  const group = useContext(SwipeActionGroupContext);

  const containerRef = useRef<HTMLDivElement>(null);
  const actionsRef = useRef<HTMLDivElement>(null);

  // 偏移量（负数表示向左移动）
  const [offset, setOffset] = useState(0);
  const [isOpen, setIsOpen] = useState(false);
  const [isSwiping, setIsSwiping] = useState(false);

  // 手势跟踪 Ref，避免 re-render 延迟
  const touchState = useRef({
    startX: 0,
    startY: 0,
    currentX: 0,
    currentY: 0,
    axisLocked: null as "horizontal" | "vertical" | null,
    initialOffset: 0,
    maxActionsWidth: 0,
    hasMoved: false,
  });

  const getActionsWidth = useCallback(() => {
    if (actionsRef.current) {
      return actionsRef.current.offsetWidth || 140;
    }
    // 默认每个操作按钮约 64px 宽
    return Math.max(actions.length * 64, 80);
  }, [actions.length]);

  const close = useCallback(() => {
    setOffset(0);
    setIsOpen(false);
    onOpenChange?.(false);
    if (group && group.activeId === itemId) {
      group.setActiveId(null);
    }
    if (globalOpenedSwipeId === itemId) {
      notifyGlobalSwipeOpen(null);
    }
  }, [group, itemId, onOpenChange]);

  const open = useCallback(() => {
    const width = getActionsWidth();
    setOffset(-width);
    setIsOpen(true);
    onOpenChange?.(true);
    if (group) {
      group.setActiveId(itemId);
    }
    notifyGlobalSwipeOpen(itemId);
  }, [getActionsWidth, group, itemId, onOpenChange]);

  // 监听容器外点击或互斥关闭
  useEffect(() => {
    if (group) {
      if (group.activeId !== itemId && isOpen) {
        setOffset(0);
        setIsOpen(false);
        onOpenChange?.(false);
      }
    }
  }, [group, group?.activeId, itemId, isOpen, onOpenChange]);

  useEffect(() => {
    const handleGlobalNotify = (openedId: string | null) => {
      if (openedId !== itemId && isOpen) {
        setOffset(0);
        setIsOpen(false);
        onOpenChange?.(false);
      }
    };
    globalSwipeListeners.add(handleGlobalNotify);
    return () => {
      globalSwipeListeners.delete(handleGlobalNotify);
    };
  }, [itemId, isOpen, onOpenChange]);

  // 展开状态下点击外部自动收起
  useEffect(() => {
    if (!isOpen) return;

    const handlePointerDownOutside = (e: MouseEvent | TouchEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(e.target as Node)
      ) {
        close();
      }
    };

    window.addEventListener("touchstart", handlePointerDownOutside, {
      passive: true,
    });
    window.addEventListener("mousedown", handlePointerDownOutside, {
      passive: true,
    });
    return () => {
      window.removeEventListener("touchstart", handlePointerDownOutside);
      window.removeEventListener("mousedown", handlePointerDownOutside);
    };
  }, [close, isOpen]);

  // 如果无 actions 或被禁用，则退化为普通渲染
  if (!actions.length || disabled) {
    return <div className={cn("relative", className)}>{children}</div>;
  }

  const handleTouchStart = (e: React.TouchEvent) => {
    if (e.touches.length !== 1) return;

    const touch = e.touches[0];
    const actionsWidth = getActionsWidth();

    touchState.current = {
      startX: touch.clientX,
      startY: touch.clientY,
      currentX: touch.clientX,
      currentY: touch.clientY,
      axisLocked: null,
      initialOffset: isOpen ? -actionsWidth : 0,
      maxActionsWidth: actionsWidth,
      hasMoved: false,
    };

    setIsSwiping(false);
  };

  const handleTouchMove = (e: React.TouchEvent) => {
    if (e.touches.length !== 1) return;
    const touch = e.touches[0];
    const dx = touch.clientX - touchState.current.startX;
    const dy = touch.clientY - touchState.current.startY;

    // 前 6px 严格判定轴向
    if (touchState.current.axisLocked === null) {
      const absX = Math.abs(dx);
      const absY = Math.abs(dy);
      if (absX < 6 && absY < 6) {
        return;
      }
      if (absY >= absX) {
        // 垂直移动占优，判定为列表滚动，放弃滑动拦截
        touchState.current.axisLocked = "vertical";
        return;
      }
      // 水平方向占优，锁定水平滑动
      touchState.current.axisLocked = "horizontal";
      setIsSwiping(true);
    }

    if (touchState.current.axisLocked !== "horizontal") {
      return;
    }

    // 组织浏览器默认滚动行为
    touchState.current.hasMoved = true;
    touchState.current.currentX = touch.clientX;
    touchState.current.currentY = touch.clientY;

    const maxW = touchState.current.maxActionsWidth;
    let targetOffset = touchState.current.initialOffset + dx;

    // 边界与阻尼计算
    if (targetOffset > 0) {
      // 向右滑（超过左边缘），施加强阻尼
      targetOffset = targetOffset * 0.2;
    } else if (targetOffset < -maxW) {
      // 向左超出操作按钮宽度，施加橡皮筋阻尼
      const overscroll = -maxW - targetOffset;
      targetOffset = -maxW - overscroll * 0.25;
    }

    setOffset(targetOffset);
  };

  const handleTouchEnd = () => {
    if (touchState.current.axisLocked !== "horizontal") {
      touchState.current.axisLocked = null;
      setIsSwiping(false);
      return;
    }

    setIsSwiping(false);
    const maxW = touchState.current.maxActionsWidth;
    const currentOffset = offset;
    const wasOpen = isOpen;

    let shouldOpen = false;

    if (!wasOpen) {
      // 从关闭状态向左滑：超过 threshold 展开
      if (currentOffset <= -maxW * threshold) {
        shouldOpen = true;
      }
    } else {
      // 从展开状态向右滑：只要收拢超过 (1 - threshold)，或者向右移动超过 25px
      const deltaX =
        touchState.current.currentX - touchState.current.startX;
      if (deltaX > 25 || currentOffset > -maxW * (1 - threshold)) {
        shouldOpen = false;
      } else {
        shouldOpen = true;
      }
    }

    if (shouldOpen) {
      open();
    } else {
      close();
    }

    touchState.current.axisLocked = null;
  };

  const handleTouchCancel = () => {
    setIsSwiping(false);
    touchState.current.axisLocked = null;
    close();
  };

  return (
    <div
      ref={containerRef}
      className={cn("group/swipe relative overflow-hidden select-none", className)}
      onTouchStart={handleTouchStart}
      onTouchMove={handleTouchMove}
      onTouchEnd={handleTouchEnd}
      onTouchCancel={handleTouchCancel}
    >
      {/* 隐藏在下层的快捷操作层 */}
      <div
        ref={actionsRef}
        aria-hidden={!isOpen}
        className={cn(
          "absolute inset-y-0 right-0 flex items-stretch z-0",
          isOpen ? "pointer-events-auto" : "pointer-events-none",
        )}
      >
        {actions.map((act) => {
          const variantClasses =
            act.variant === "destructive" || act.variant === "danger"
              ? "bg-rose-500 hover:bg-rose-600 active:bg-rose-700 text-white"
              : act.variant === "warning"
                ? "bg-amber-500 hover:bg-amber-600 active:bg-amber-700 text-white"
                : act.variant === "primary"
                  ? "bg-primary hover:bg-primary/90 active:bg-primary/80 text-primary-foreground"
                  : "bg-muted text-foreground hover:bg-muted/80 active:bg-muted";

          return (
            <button
              key={act.key}
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                close();
                act.onClick(e);
              }}
              className={cn(
                "flex flex-col items-center justify-center gap-1 px-3.5 py-2 text-xs font-medium transition-colors select-none",
                variantClasses,
                act.className,
              )}
            >
              {act.icon ? (
                <span className="shrink-0 size-4 flex items-center justify-center">
                  {act.icon}
                </span>
              ) : null}
              <span className="truncate max-w-[4rem] text-[11px] leading-tight">
                {act.label}
              </span>
            </button>
          );
        })}
      </div>

      {/* 滑动上层主体内容 */}
      <div
        style={{
          transform: `translateX(${offset}px)`,
          transition: isSwiping ? "none" : "transform 0.25s cubic-bezier(0.2, 0.9, 0.3, 1)",
        }}
        onClickCapture={(e) => {
          // 如果当前处于展开状态，点击主体应拦截默认行为并收起展开项
          if (isOpen && offset !== 0) {
            e.stopPropagation();
            e.preventDefault();
            close();
          }
        }}
        className={cn("relative z-10 w-full bg-inherit", contentClassName)}
      >
        {children}
      </div>
    </div>
  );
}
