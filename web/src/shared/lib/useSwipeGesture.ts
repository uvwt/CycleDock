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
  useEffect(() => {
    callbacksRef.current = { onSwipeRight, onSwipeLeft };
  });

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

      // 1. 输入控件及禁止滑动区域（如会话输入窗口）绝对避让，杜绝手势误触
      if (isInsideHorizontalScrollableOrInput(e.target)) {
        isIgnored = true;
        isTracking = false;
        return;
      }

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

export interface SwipeUpGestureOptions<T extends HTMLElement = HTMLElement> {
  /** 目标容器 ref（若不传则 hook 自动创建并返回新 ref） */
  targetRef?: React.RefObject<T | null>;
  /** 向上滑触发回调 */
  onSwipeUp: () => void;
  /** 触发上滑的垂直距离阈值（像素），默认 40px */
  threshold?: number;
  /** 是否启用，默认 true */
  enabled?: boolean;
}

/**
 * 移动端在聊天框区域按住上滑触发回调（例如呼出快速切换卡片抽屉）
 * 特性：
 * 1. 严格判定单指垂直向上滑动（deltaY < 0 且垂直位移主导），不影响水平左右滑动手势；
 * 2. 智能文本避让：若 touch 起点在 textarea 且 textarea 存在尚未触底的纵向溢出文本，则放行内部文本滚动；
 * 3. 触发时自动收起软键盘（blur），并尝试调用轻微震动反馈（若设备支持）；
 * 4. 支持既有 targetRef 注入或返回自动创建的 ref。
 */
export function useSwipeUpGesture<T extends HTMLElement = HTMLElement>({
  targetRef,
  onSwipeUp,
  threshold = 40,
  enabled = true,
}: SwipeUpGestureOptions<T>) {
  const internalRef = useRef<T | null>(null);
  const ref = targetRef || internalRef;

  const callbacksRef = useRef({ onSwipeUp });
  useEffect(() => {
    callbacksRef.current = { onSwipeUp };
  });

  useEffect(() => {
    if (!enabled) return;
    const el = ref.current;
    if (!el) return;

    let startX = 0;
    let startY = 0;
    let isTracking = false;
    let hasTriggered = false;
    let targetElement: HTMLElement | null = null;

    const handleTouchStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) {
        isTracking = false;
        return;
      }
      const touch = e.touches[0];
      startX = touch.clientX;
      startY = touch.clientY;
      isTracking = true;
      hasTriggered = false;
      targetElement = e.target instanceof HTMLElement ? e.target : null;
    };

    const handleTouchMove = (e: TouchEvent) => {
      if (!isTracking || hasTriggered || e.touches.length !== 1) return;
      const touch = e.touches[0];
      const deltaX = touch.clientX - startX;
      const deltaY = touch.clientY - startY;
      const absX = Math.abs(deltaX);
      const absY = Math.abs(deltaY);

      // 上滑判定：向上滑动且纵向位移明显大于横向位移
      if (deltaY < 0 && absY > absX * 1.1) {
        // 如果是从多行可滚动的输入框内部拖动，且未滚动到底部，则放行输入框自身的纵向滚动
        if (targetElement instanceof HTMLTextAreaElement) {
          const canScrollDown =
            targetElement.scrollHeight > targetElement.clientHeight &&
            targetElement.scrollTop + targetElement.clientHeight <
              targetElement.scrollHeight - 6;
          if (canScrollDown) {
            return;
          }
        }

        if (absY >= 10 && e.cancelable) {
          e.preventDefault();
        }

        if (absY >= threshold) {
          hasTriggered = true;
          isTracking = false;

          callbacksRef.current.onSwipeUp?.();

          // 触发上滑后若输入框处于聚焦状态，收起软键盘确保弹窗完整呈现
          if (
            document.activeElement instanceof HTMLElement &&
            (document.activeElement.tagName === "TEXTAREA" ||
              document.activeElement.tagName === "INPUT")
          ) {
            document.activeElement.blur();
          }

          try {
            if (typeof navigator !== "undefined" && navigator.vibrate) {
              navigator.vibrate(10);
            }
          } catch {
            // 忽略不支持或权限错误
          }
        }
      }
    };

    const handleTouchEnd = (e: TouchEvent) => {
      if (isTracking && !hasTriggered && e.changedTouches.length === 1) {
        const touch = e.changedTouches[0];
        const deltaX = touch.clientX - startX;
        const deltaY = touch.clientY - startY;
        const absX = Math.abs(deltaX);
        const absY = Math.abs(deltaY);

        if (deltaY < 0 && absY > absX * 1.1 && absY >= threshold) {
          let shouldTrigger = true;
          if (targetElement instanceof HTMLTextAreaElement) {
            const canScrollDown =
              targetElement.scrollHeight > targetElement.clientHeight &&
              targetElement.scrollTop + targetElement.clientHeight <
                targetElement.scrollHeight - 6;
            if (canScrollDown) {
              shouldTrigger = false;
            }
          }

          if (shouldTrigger) {
            callbacksRef.current.onSwipeUp?.();
            if (
              document.activeElement instanceof HTMLElement &&
              (document.activeElement.tagName === "TEXTAREA" ||
                document.activeElement.tagName === "INPUT")
            ) {
              document.activeElement.blur();
            }
          }
        }
      }

      isTracking = false;
      hasTriggered = false;
      targetElement = null;
    };

    const handleTouchCancel = () => {
      isTracking = false;
      hasTriggered = false;
      targetElement = null;
    };

    el.addEventListener("touchstart", handleTouchStart, { passive: true });
    el.addEventListener("touchmove", handleTouchMove, { passive: false });
    el.addEventListener("touchend", handleTouchEnd, { passive: true });
    el.addEventListener("touchcancel", handleTouchCancel, { passive: true });

    return () => {
      el.removeEventListener("touchstart", handleTouchStart);
      el.removeEventListener("touchmove", handleTouchMove);
      el.removeEventListener("touchend", handleTouchEnd);
      el.removeEventListener("touchcancel", handleTouchCancel);
    };
  }, [enabled, threshold, ref]);

  return ref;
}
