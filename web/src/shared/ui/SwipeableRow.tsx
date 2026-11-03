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
  variant?: "default" | "destructive" | "warning" | "primary" | "danger" | "secondary";
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
  const isOpenRef = useRef(false);

  useEffect(() => {
    offsetRef.current = offset;
  }, [offset]);
  useEffect(() => {
    isOpenRef.current = isOpen;
  }, [isOpen]);

  const getActionsWidth = useCallback(() => {
    // 纯图标模式下每个操作按钮固定 44px 宽
    return Math.max(actions.length * 44, 44);
  }, [actions.length]);

  const actionsWidth = getActionsWidth();
  // 当过度左滑时（-offset > actionsWidth），操作区随卡片左移动态等比例拉伸，始终紧贴卡片右边缘，杜绝断开断层
  const currentActionsWidth = Math.max(actionsWidth, -offset);

  const close = useCallback(() => {
    setOffset(0);
    offsetRef.current = 0;
    setIsOpen(false);
    isOpenRef.current = false;
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
    offsetRef.current = -width;
    setIsOpen(true);
    isOpenRef.current = true;
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
          // 向右拖拽施加强阻尼与软上限（无右滑操作，给予微弱弹性反馈）
          targetOffset = Math.min(targetOffset * 0.08, 16);
        } else if (targetOffset < -actionsWidth) {
          // 向左超出动作按钮总宽时（过度左滑），施加橡皮筋阻尼并限制最大拉伸距离
          const overscroll = -actionsWidth - targetOffset;
          const maxOverscroll = 40;
          const dampedOverscroll = Math.min(overscroll * 0.25, maxOverscroll);
          targetOffset = -actionsWidth - dampedOverscroll;
        }

        setOffset(targetOffset);
        offsetRef.current = targetOffset;
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
      // 仅响应水平手势；纵向滚动由 CSS touch-pan-y 原生处理，互不冲突
      axis: "x",
      // 区分轻点与拖拽：松手时如果只是滑动操作则拦截 click，避免滑动后误触进入详情
      filterTaps: true,
      // 移动端手指触控容差：8px 内微小位移仍视为点击，避免误判为滑动
      tapsThreshold: 8,
      // 注意：切勿配置 preventScrollAxis: "x"！
      // 该选项会在触控设备上引入 250ms 延迟，导致：
      // 1) 正常轻点（<250ms）被判断为未就绪而被全局拦截 click，导致无法点击跳转详情；
      // 2) 正常左滑因未满 250ms 触发轴阻断，导致“必须长按才能左滑”。
    },
  );

  // 如果无 actions 或被禁用，则退化为普通渲染
  if (!actions.length || disabled) {
    return <div className={cn("relative", className)}>{children}</div>;
  }

  // 关键：底层操作区在未拖拽且未展开时必须严格隐藏，避免任何透明背景穿透
  const isActionsVisible = isOpen || offset < 0 || isSwiping;

  return (
    <div
      ref={containerRef}
      data-swipe-open={isOpen ? "true" : undefined}
      className={cn(
        "group/swipe relative select-none",
        className,
      )}
    >
      {/* 隐藏在底部的快捷操作区：固定在右侧，随上层卡片整体左移而显露；过度左滑时动态拉伸跟随 */}
      <div
        ref={actionsRef}
        aria-hidden={!isOpen}
        onPointerDown={(e) => e.stopPropagation()}
        onTouchStart={(e) => e.stopPropagation()}
        style={{
          visibility: isActionsVisible ? "visible" : "hidden",
          width: currentActionsWidth,
          transition: isSwiping
            ? "none"
            : "width 0.28s cubic-bezier(0.2, 0.9, 0.3, 1), visibility 0s linear " +
              (isOpen ? "0s" : "0.28s"),
        }}
        className={cn(
          "absolute inset-y-0 right-0 flex items-stretch z-0 overflow-hidden rounded-r-xl",
          isOpen ? "pointer-events-auto" : "pointer-events-none",
        )}
      >
        {actions.map((act, index) => {
          const variantClasses =
            act.variant === "destructive" || act.variant === "danger"
              ? "bg-rose-500 hover:bg-rose-600 active:bg-rose-700 text-white"
              : act.variant === "warning"
                ? "bg-amber-500 hover:bg-amber-600 active:bg-amber-700 text-white"
                : act.variant === "primary"
                  ? "bg-primary hover:bg-primary/90 active:bg-primary/80 text-primary-foreground"
                  : act.variant === "secondary"
                    ? "bg-slate-600 hover:bg-slate-700 active:bg-slate-800 text-white"
                    : "bg-muted text-foreground hover:bg-muted/80 active:bg-muted";

          return (
            <button
              key={act.key}
              type="button"
              title={act.label}
              aria-label={act.label}
              onClick={(e) => {
                e.stopPropagation();
                close();
                act.onClick(e);
              }}
              className={cn(
                "flex items-center justify-center flex-1 min-w-[44px] transition-colors select-none touch-manipulation active:opacity-85",
                index > 0 && "border-l border-white/20 dark:border-black/20",
                variantClasses,
                act.className,
              )}
            >
              {act.icon ? (
                <span className="shrink-0 size-4 flex items-center justify-center pointer-events-none">
                  {act.icon}
                </span>
              ) : null}
            </button>
          );
        })}
      </div>

      {/* 滑动卡片主体（整体随手势向左平移，露出底层操作按钮） */}
      <div
        {...bind()}
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
        className={cn(
          "relative z-10 w-full overflow-hidden bg-surface rounded-xl transition-[border-radius] touch-pan-y",
          offset < -2
            ? "rounded-r-none [&_*]:rounded-r-none"
            : "",
          contentClassName,
        )}
      >
        {children}
      </div>
    </div>
  );
}

