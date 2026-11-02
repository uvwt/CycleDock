import { useEffect, useRef } from "react";

export interface SwipeGestureOptions {
  /** 向右滑触发（如展开左侧菜单/抽屉） */
  onSwipeRight?: () => void;
  /** 向左滑触发（如展开右侧任务详情） */
  onSwipeLeft?: () => void;
  /** 触发滑动手势所需的最小横向位移距离（像素），默认 45px */
  threshold?: number;
  /** 横向与纵向位移判定的最小比例（小角度横滑判定），默认 1.2 倍（给单手弧线滑动更多容差） */
  horizontalRatio?: number;
  /** 是否启用，默认 true */
  enabled?: boolean;
  /** 边缘手势限制（像素）：若设置，仅当滑动起点位于屏幕左边缘或右边缘内时才响应；未设置时不限制边缘 */
  edgeWidth?: number;
}

/**
 * 检查触摸事件的起始目标是否处于不可被打断横向滚动的子元素内（如代码块、表格、横向滚动栏、输入框等）
 */
function isInsideHorizontalScrollableOrInput(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;

  let current: HTMLElement | null = target;
  while (current && current !== document.body) {
    // 1. 输入控件及可编辑内容
    const tagName = current.tagName.toLowerCase();
    if (
      tagName === "input" ||
      tagName === "textarea" ||
      tagName === "select" ||
      current.isContentEditable ||
      current.dataset.noSwipe === "true"
    ) {
      return true;
    }

    // 2. 常见横向滚动容器：pre, code, table
    // 仅在实际发生横向溢出滚动时才避让，避免普通的行内 code 或未溢出的 pre 导致误判
    if (tagName === "pre" || tagName === "code" || tagName === "table") {
      if (current.scrollWidth > current.clientWidth) {
        return true;
      }
    }

    // 3. 计算样式判断是否横向可滚动（且实际存在可滚动内容）
    const style = window.getComputedStyle(current);
    const overflowX = style.overflowX;
    if (overflowX === "auto" || overflowX === "scroll") {
      if (current.scrollWidth > current.clientWidth) {
        return true;
      }
    }

    current = current.parentElement;
  }

  return false;
}

/**
 * 通用移动端左右滑动手势 Hook
 * 带有子元素精准避让、单手弧线横向位移容差、边缘滑动优先响应机制
 */
export function useSwipeGesture<T extends HTMLElement = HTMLElement>({
  onSwipeRight,
  onSwipeLeft,
  threshold = 45,
  horizontalRatio = 1.2,
  enabled = true,
  edgeWidth,
}: SwipeGestureOptions) {
  const containerRef = useRef<T | null>(null);

  // 使用 ref 避免闭包捕获旧回调与配置
  const callbacksRef = useRef({ onSwipeRight, onSwipeLeft });
  callbacksRef.current = { onSwipeRight, onSwipeLeft };

  useEffect(() => {
    if (!enabled) return;
    const el = containerRef.current;
    if (!el) return;

    let startX = 0;
    let startY = 0;
    let isTracking = false;
    let isLockedHorizontal = false;
    let isIgnored = false;
    let isEdgeSwipe = false;

    const handleTouchStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) {
        isTracking = false;
        return;
      }

      const touch = e.touches[0];
      startX = touch.clientX;
      startY = touch.clientY;

      const winWidth = window.innerWidth;
      const effectiveEdge = edgeWidth && edgeWidth > 0 ? edgeWidth : 30; // 默认前 30px 为边缘滑动手势区
      const isNearLeftEdge = startX <= effectiveEdge;
      const isNearRightEdge = startX >= winWidth - effectiveEdge;

      // 如果明确指定了 edgeWidth 选项，则必须在边缘内才跟踪
      if (edgeWidth && edgeWidth > 0 && !isNearLeftEdge && !isNearRightEdge) {
        isIgnored = true;
        isTracking = false;
        return;
      }

      // 如果是屏幕边缘滑动，赋予最高优先级，直接响应，不受内部滚动容器避让影响
      if (isNearLeftEdge || isNearRightEdge) {
        isEdgeSwipe = true;
      } else {
        isEdgeSwipe = false;
        // 普通区域滑动时进行避让检测
        if (isInsideHorizontalScrollableOrInput(e.target)) {
          isIgnored = true;
          isTracking = false;
          return;
        }
      }

      isIgnored = false;
      isTracking = true;
      isLockedHorizontal = false;
    };

    const handleTouchMove = (e: TouchEvent) => {
      if (!isTracking || isIgnored || e.touches.length !== 1) return;

      const touch = e.touches[0];
      const deltaX = touch.clientX - startX;
      const deltaY = touch.clientY - startY;
      const absX = Math.abs(deltaX);
      const absY = Math.abs(deltaY);

      // 初期判断（前 8px~10px）：判断是否是小角度横滑还是纵向滚动浏览
      if (!isLockedHorizontal) {
        if (absX > 8 || absY > 8) {
          // 边缘手势放宽判定比例至 1.0；非边缘手势使用 horizontalRatio (默认 1.2)
          const ratio = isEdgeSwipe ? 1.0 : horizontalRatio;
          if (absX < absY * ratio) {
            isTracking = false;
            return;
          }
          // 确认为小角度横滑手势，锁定为横向
          isLockedHorizontal = true;
        }
      }
    };

    const handleTouchEnd = (e: TouchEvent) => {
      if (!isTracking || isIgnored || !isLockedHorizontal) {
        isTracking = false;
        isLockedHorizontal = false;
        return;
      }

      const touch = e.changedTouches[0];
      const deltaX = touch.clientX - startX;
      const deltaY = touch.clientY - startY;
      const absX = Math.abs(deltaX);
      const absY = Math.abs(deltaY);

      const ratio = isEdgeSwipe ? 1.0 : horizontalRatio;
      const effectiveThreshold = isEdgeSwipe ? Math.min(threshold, 35) : threshold;

      if (absX >= effectiveThreshold && absX >= absY * ratio) {
        if (deltaX > 0) {
          callbacksRef.current.onSwipeRight?.();
        } else {
          callbacksRef.current.onSwipeLeft?.();
        }
      }

      isTracking = false;
      isLockedHorizontal = false;
      isIgnored = false;
      isEdgeSwipe = false;
    };

    const handleTouchCancel = () => {
      isTracking = false;
      isLockedHorizontal = false;
      isIgnored = false;
      isEdgeSwipe = false;
    };

    // 使用 passive: true 确保不阻塞手机浏览器默认滚动的性能
    el.addEventListener("touchstart", handleTouchStart, { passive: true });
    el.addEventListener("touchmove", handleTouchMove, { passive: true });
    el.addEventListener("touchend", handleTouchEnd, { passive: true });
    el.addEventListener("touchcancel", handleTouchCancel, { passive: true });

    return () => {
      el.removeEventListener("touchstart", handleTouchStart);
      el.removeEventListener("touchmove", handleTouchMove);
      el.removeEventListener("touchend", handleTouchEnd);
      el.removeEventListener("touchcancel", handleTouchCancel);
    };
  }, [enabled, threshold, horizontalRatio, edgeWidth]);

  return containerRef;
}
