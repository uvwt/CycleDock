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
 * 检查当前页面是否存在非空的文本选中状态（避免选中文字或拖拽选区手柄时误触滑动手势）
 */
function hasActiveTextSelection(): boolean {
  if (typeof window === "undefined" || !window.getSelection) return false;
  const selection = window.getSelection();
  return Boolean(
    selection && !selection.isCollapsed && selection.toString().length > 0,
  );
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
  const stateRef = useRef({
    onSwipeRight,
    onSwipeLeft,
    threshold,
    horizontalRatio,
    edgeWidth,
  });
  useEffect(() => {
    stateRef.current = {
      onSwipeRight,
      onSwipeLeft,
      threshold,
      horizontalRatio,
      edgeWidth,
    };
  });

  const boundRef = useRef<{ el: T; cleanup: () => void } | null>(null);

  useEffect(() => {
    const el = enabled ? containerRef.current : null;
    if (boundRef.current?.el === el) return;

    if (boundRef.current) {
      boundRef.current.cleanup();
      boundRef.current = null;
    }
    if (!el) return;

    let startX = 0;
    let startY = 0;
    let startTime = 0;
    let isTracking = false;
    let isLockedHorizontal = false;
    let isIgnored = false;
    let isEdgeSwipe = false;

    const handleSelectionChange = () => {
      if (isTracking) {
        isIgnored = true;
        isTracking = false;
        isLockedHorizontal = false;
      }
    };

    const handleTouchStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) {
        isTracking = false;
        return;
      }

      // 0. 若当前已有选中文本（用户正在拖拽选区手柄或调整选区），禁止触发左右滑抽屉
      if (hasActiveTextSelection()) {
        isIgnored = true;
        isTracking = false;
        return;
      }

      const touch = e.touches[0];
      startX = touch.clientX;
      startY = touch.clientY;
      startTime = Date.now();

      // 1. 输入控件及禁止滑动区域（如会话输入窗口）绝对避让，杜绝手势误触
      if (isInsideHorizontalScrollableOrInput(e.target)) {
        isIgnored = true;
        isTracking = false;
        return;
      }

      const winWidth = window.innerWidth;
      const curEdgeWidth = stateRef.current.edgeWidth;
      const effectiveEdge =
        curEdgeWidth && curEdgeWidth > 0 ? curEdgeWidth : 30; // 默认前 30px 为边缘滑动手势区
      const isNearLeftEdge = startX <= effectiveEdge;
      const isNearRightEdge = startX >= winWidth - effectiveEdge;

      // 如果明确指定了 edgeWidth 选项，则必须在边缘内才跟踪
      if (
        curEdgeWidth &&
        curEdgeWidth > 0 &&
        !isNearLeftEdge &&
        !isNearRightEdge
      ) {
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

      if (hasActiveTextSelection()) {
        isIgnored = true;
        isTracking = false;
        return;
      }

      const touch = e.touches[0];
      const deltaX = touch.clientX - startX;
      const deltaY = touch.clientY - startY;
      const absX = Math.abs(deltaX);
      const absY = Math.abs(deltaY);

      // 初期判断（前 8px~10px）：判断是否是小角度横滑还是纵向滚动浏览
      if (!isLockedHorizontal) {
        if (absX > 8 || absY > 8) {
          // 若非屏幕边缘滑动且手指按住停顿超过 220ms 才开始移动（典型长按选词特征），视为文本选择操作并放弃手势
          if (!isEdgeSwipe && Date.now() - startTime > 220) {
            isIgnored = true;
            isTracking = false;
            return;
          }

          // 边缘手势放宽判定比例至 1.0；非边缘手势使用 horizontalRatio (默认 1.2)
          const ratio = isEdgeSwipe ? 1.0 : stateRef.current.horizontalRatio;
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
      if (
        !isTracking ||
        isIgnored ||
        !isLockedHorizontal ||
        hasActiveTextSelection() ||
        Date.now() - startTime > 500
      ) {
        isTracking = false;
        isLockedHorizontal = false;
        isIgnored = false;
        isEdgeSwipe = false;
        return;
      }

      const touch = e.changedTouches[0];
      const deltaX = touch.clientX - startX;
      const deltaY = touch.clientY - startY;
      const absX = Math.abs(deltaX);
      const absY = Math.abs(deltaY);

      const { threshold: curThreshold, horizontalRatio: curRatio } =
        stateRef.current;
      const ratio = isEdgeSwipe ? 1.0 : curRatio;
      const effectiveThreshold = isEdgeSwipe
        ? Math.min(curThreshold, 35)
        : curThreshold;

      if (absX >= effectiveThreshold && absX >= absY * ratio) {
        if (deltaX > 0) {
          stateRef.current.onSwipeRight?.();
        } else {
          stateRef.current.onSwipeLeft?.();
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
    document.addEventListener("selectionchange", handleSelectionChange);

    boundRef.current = {
      el,
      cleanup: () => {
        el.removeEventListener("touchstart", handleTouchStart);
        el.removeEventListener("touchmove", handleTouchMove);
        el.removeEventListener("touchend", handleTouchEnd);
        el.removeEventListener("touchcancel", handleTouchCancel);
        document.removeEventListener("selectionchange", handleSelectionChange);
      },
    };
  });

  useEffect(() => {
    return () => {
      boundRef.current?.cleanup();
      boundRef.current = null;
    };
  }, []);

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
  threshold = 28,
  enabled = true,
}: SwipeUpGestureOptions<T>) {
  const internalRef = useRef<T | null>(null);
  const ref = targetRef || internalRef;

  const stateRef = useRef({ onSwipeUp, threshold });
  useEffect(() => {
    stateRef.current = { onSwipeUp, threshold };
  });

  const boundRef = useRef<{ el: T; cleanup: () => void } | null>(null);

  useEffect(() => {
    const el = enabled ? ref.current : null;
    if (boundRef.current?.el === el) return;

    if (boundRef.current) {
      boundRef.current.cleanup();
      boundRef.current = null;
    }
    if (!el) return;

    let startX = 0;
    let startY = 0;
    let startTime = 0;
    let isTracking = false;
    let targetElement: HTMLElement | null = null;

    const canTextareaScrollDown = (target: HTMLElement | null) => {
      if (!(target instanceof HTMLTextAreaElement)) return false;
      return (
        target.scrollHeight > target.clientHeight + 4 &&
        target.scrollTop + target.clientHeight < target.scrollHeight - 6
      );
    };

    const triggerSwipeUp = () => {
      stateRef.current.onSwipeUp?.();
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
    };

    const handleTouchStart = (e: TouchEvent) => {
      if (e.touches.length !== 1 || hasActiveTextSelection()) {
        isTracking = false;
        return;
      }
      const touch = e.touches[0];

      // 1. 避让全面屏底部系统手势区（iOS Home Indicator / Android 全面屏返回桌面与多任务手势区，底部 38px）
      const viewportHeight =
        window.visualViewport?.height || window.innerHeight || 0;
      if (viewportHeight > 0 && touch.clientY >= viewportHeight - 38) {
        isTracking = false;
        return;
      }

      // 2. 若容器内包含输入表单 <form>，仅响应表单卡片本体内的上滑，忽略表单外侧及底部安全区内边距的触摸
      const target = e.target instanceof HTMLElement ? e.target : null;
      const formEl = el.querySelector("form");
      if (formEl && target && !formEl.contains(target)) {
        isTracking = false;
        return;
      }

      startX = touch.clientX;
      startY = touch.clientY;
      startTime = Date.now();
      isTracking = true;
      targetElement = target;
    };

    const handleTouchMove = (e: TouchEvent) => {
      if (!isTracking || e.touches.length !== 1) return;
      if (hasActiveTextSelection()) {
        isTracking = false;
        return;
      }
      const touch = e.touches[0];
      const deltaX = touch.clientX - startX;
      const deltaY = touch.clientY - startY;
      const absX = Math.abs(deltaX);
      const absY = Math.abs(deltaY);

      // 上滑判定：向上滑动且纵向位移明显大于横向位移
      if (deltaY < 0 && absY >= absX * 1.2) {
        if (canTextareaScrollDown(targetElement)) {
          return;
        }

        // 仅阻止输入框原生滚动，不在 touchmove 中提前触发打开抽屉，等待 touchend 抬手确认（避免全面屏上滑悬停或系统手势触发 touchcancel 前抢先误触）
        if (absY >= 6 && e.cancelable) {
          e.preventDefault();
        }
      }
    };

    const handleTouchEnd = (e: TouchEvent) => {
      if (
        isTracking &&
        !hasActiveTextSelection() &&
        Date.now() - startTime <= 450 &&
        e.changedTouches.length === 1
      ) {
        const touch = e.changedTouches[0];
        const deltaX = touch.clientX - startX;
        const deltaY = touch.clientY - startY;
        const absX = Math.abs(deltaX);
        const absY = Math.abs(deltaY);

        if (
          deltaY < 0 &&
          absY >= absX * 1.2 &&
          absY >= stateRef.current.threshold &&
          !canTextareaScrollDown(targetElement)
        ) {
          triggerSwipeUp();
        }
      }

      isTracking = false;
      targetElement = null;
    };

    const handleTouchCancel = () => {
      isTracking = false;
      targetElement = null;
    };

    el.addEventListener("touchstart", handleTouchStart, { passive: true });
    el.addEventListener("touchmove", handleTouchMove, { passive: false });
    el.addEventListener("touchend", handleTouchEnd, { passive: true });
    el.addEventListener("touchcancel", handleTouchCancel, { passive: true });

    boundRef.current = {
      el,
      cleanup: () => {
        el.removeEventListener("touchstart", handleTouchStart);
        el.removeEventListener("touchmove", handleTouchMove);
        el.removeEventListener("touchend", handleTouchEnd);
        el.removeEventListener("touchcancel", handleTouchCancel);
      },
    };
  });

  useEffect(() => {
    return () => {
      boundRef.current?.cleanup();
      boundRef.current = null;
    };
  }, []);

  return ref;
}
