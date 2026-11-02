import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { useDrag } from "@use-gesture/react";
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

  // 手势跟踪 Ref 保证最新值
  const offsetRef = useRef(0);
  offsetRef.current = offset;
  const isOpenRef = useRef(false);
  isOpenRef.current = isOpen;

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

  // 使用 @use-gesture/react 统一管理手势
  const bind = useDrag(
    ({
      down,
      movement: [mx],
      velocity: [vx],
      direction: [dx],
      memo,
    }) => {
      if (disabled || !actions.length) return;

      const actionsWidth = getActionsWidth();

      if (memo === undefined) {
        memo = offsetRef.current;
        setIsSwiping(true);
      }

      if (down) {
        let targetOffset = memo + mx;

        // 边界与自然弹性阻尼
        if (targetOffset > 0) {
          targetOffset = targetOffset * 0.15;
        } else if (targetOffset < -actionsWidth) {
          const overscroll = -actionsWidth - targetOffset;
          targetOffset = -actionsWidth - overscroll * 0.2;
        }

        setOffset(targetOffset);
        return memo;
      }

      // 手势释放
      setIsSwiping(false);
      const currentOffset = offsetRef.current;
      const wasOpen = isOpenRef.current;

      let shouldOpen = false;
      const isFlick = vx > 0.35;

      if (!wasOpen) {
        // 从关闭到开启（向左滑）
        if (isFlick && dx < 0) {
          shouldOpen = true;
        } else if (currentOffset <= -actionsWidth * threshold) {
          shouldOpen = true;
        }
      } else {
        // 从开启到关闭（向右滑）
        if (isFlick && dx > 0) {
          shouldOpen = false;
        } else if (currentOffset > -actionsWidth * (1 - threshold)) {
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
    },
    {
      axis: "x",
      filterTaps: true,
      preventScrollAxis: "x",
      pointer: { touch: true },
    },
  );

  // 如果无 actions 或被禁用，则退化为普通渲染
  if (!actions.length || disabled) {
    return <div className={cn("relative", className)}>{children}</div>;
  }

  // 关键：底层操作区在未拖拽且未展开时必须严格隐藏，避免任何透明背景穿透
  const isActionsVisible = isOpen || offset !== 0 || isSwiping;

  return (
    <div
      ref={containerRef}
      className={cn(
        "group/swipe relative overflow-hidden select-none touch-pan-y",
        className,
      )}
      {...bind()}
    >
      {/* 隐藏在下层的快捷操作层 */}
      <div
        ref={actionsRef}
        aria-hidden={!isOpen}
        style={{
          visibility: isActionsVisible ? "visible" : "hidden",
          opacity: isActionsVisible ? 1 : 0,
        }}
        className={cn(
          "absolute inset-y-0 right-0 flex items-stretch z-0 transition-opacity duration-150",
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
          transition: isSwiping
            ? "none"
            : "transform 0.28s cubic-bezier(0.2, 0.9, 0.3, 1)",
        }}
        onClickCapture={(e) => {
          // 如果当前处于展开状态，点击主体应拦截默认行为并收起展开项
          if (isOpen && offset !== 0) {
            e.stopPropagation();
            e.preventDefault();
            close();
          }
        }}
        className={cn("relative z-10 w-full bg-surface", contentClassName)}
      >
        {children}
      </div>
    </div>
  );
}

