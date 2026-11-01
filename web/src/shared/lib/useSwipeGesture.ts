import { useEffect, useRef } from "react";

export interface SwipeGestureOptions {
  /** 向右滑触发（如展开左侧菜单/抽屉） */
  onSwipeRight?: () => void;
  /** 向左滑触发（如展开右侧任务详情） */
  onSwipeLeft?: () => void;
  /** 触发滑动手势所需的最小横向位移距离（像素），默认 45px */
  threshold?: number;
  /** 横向与纵向位移判定的最小比例（小角度横滑判定），默认 1.5 倍 */
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
    if (tagName === "pre" || tagName === "code" || tagName === "table") {
      return true;
    }

    // 3. 计算样式判断是否横向可滚动
    const style = window.getComputedStyle(current);
    const overflowX = style.overflowX;
    if (overflowX === "auto" || overflowX === "scroll") {
      // 检查当前元素实际是否有横向滚动内容
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
 * 带有子元素避让、小角度横向位移锁定、防滚动冲突机制
 */
export function useSwipeGesture<T extends HTMLElement = HTMLElement>({
  onSwipeRight,
  onSwipeLeft,
  threshold = 45,
  horizontalRatio = 1.5,
  enabled = true,
  edgeWidth,
}: SwipeGestureOptions) {
  const containerRef = useRef<T | null>(null);

  useEffect(() => {
    if (!enabled) return;
    const el = containerRef.current;
    if (!el) return;

    let startX = 0;
    let startY = 0;
    let isTracking = false;
    let isLockedHorizontal = false;
    let isIgnored = false;

    const handleTouchStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) {
        isTracking = false;
        return;
      }

      const touch = e.touches[0];
      startX = touch.clientX;
      startY = touch.clientY;

      // 避让检测：如果起点在横向滚动元素或交互表单内，忽略该手势
      if (isInsideHorizontalScrollableOrInput(e.target)) {
        isIgnored = true;
        isTracking = false;
        return;
      }

      // 边缘宽度限制（若配置）
      if (edgeWidth && edgeWidth > 0) {
        const winWidth = window.innerWidth;
        const isNearLeftEdge = startX <= edgeWidth;
        const isNearRightEdge = startX >= winWidth - edgeWidth;
        if (!isNearLeftEdge && !isNearRightEdge) {
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
          // 如果纵向位移主导或横向位移不足以超过比例阈值，则判定为普通滚动，放弃手势监听
          if (absX < absY * horizontalRatio) {
            isTracking = false;
            return;
          }
          // 确认为明确的小角度横滑手势，锁定为横向
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

      if (absX >= threshold && absX >= absY * horizontalRatio) {
        if (deltaX > 0) {
          onSwipeRight?.();
        } else {
          onSwipeLeft?.();
        }
      }

      isTracking = false;
      isLockedHorizontal = false;
    };

    const handleTouchCancel = () => {
      isTracking = false;
      isLockedHorizontal = false;
      isIgnored = false;
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
  }, [enabled, threshold, horizontalRatio, edgeWidth, onSwipeRight, onSwipeLeft]);

  return containerRef;
}
