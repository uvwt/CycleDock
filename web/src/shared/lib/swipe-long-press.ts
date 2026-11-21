export const DEFAULT_LONG_PRESS_DELAY_MS = 460;
export const DEFAULT_LONG_PRESS_MOVE_TOLERANCE_PX = 10;

export interface LongPressMachineOptions {
  delayMs?: number;
  moveTolerancePx?: number;
  onLongPress: () => void;
  setTimer?: (cb: () => void, ms: number) => ReturnType<typeof setTimeout>;
  clearTimer?: (id: ReturnType<typeof setTimeout>) => void;
}

export interface LongPressPoint {
  x: number;
  y: number;
}

/**
 * 列表行触摸长按防冲突状态机（纯函数内核，支持单测与真实 DOM 复用）：
 * - 按下开始计时（约 460ms）；
 * - 若手指横向滑动（左滑露出快捷按钮）或纵向滚动位移超出阈值（10px），立即取消长按判定；
 * - 若左滑抽屉当前处于展开状态（isSwipeOpen === true），不启动长按判定；
 * - 长按一旦触发，锁死后续 touchend / click，避免松手瞬间误触发卡片点击跳转；
 * - 长按按住及触发后的 contextmenu 事件一并拦截，阻止移动端弹出系统文本选择/链接菜单。
 */
export function createLongPressMachine({
  delayMs = DEFAULT_LONG_PRESS_DELAY_MS,
  moveTolerancePx = DEFAULT_LONG_PRESS_MOVE_TOLERANCE_PX,
  onLongPress,
  setTimer = setTimeout,
  clearTimer = clearTimeout,
}: LongPressMachineOptions) {
  let timerId: ReturnType<typeof setTimeout> | null = null;
  let startPoint: LongPressPoint | null = null;
  let pressing = false;
  let triggered = false;
  let suppressNextClick = false;

  function stopTimer() {
    if (timerId !== null) {
      clearTimer(timerId);
      timerId = null;
    }
  }

  function cancel() {
    stopTimer();
    startPoint = null;
    pressing = false;
  }

  return {
    start(point: LongPressPoint, opts?: { disabled?: boolean; isSwipeOpen?: boolean }) {
      cancel();
      triggered = false;
      suppressNextClick = false;
      if (opts?.disabled || opts?.isSwipeOpen) {
        return;
      }
      startPoint = { x: point.x, y: point.y };
      pressing = true;
      timerId = setTimer(() => {
        timerId = null;
        if (!pressing) return;
        pressing = false;
        triggered = true;
        suppressNextClick = true;
        onLongPress();
      }, delayMs);
    },

    move(point: LongPressPoint) {
      if (!pressing || !startPoint) return;
      const dx = point.x - startPoint.x;
      const dy = point.y - startPoint.y;
      if (Math.hypot(dx, dy) > moveTolerancePx) {
        cancel();
      }
    },

    end(): { wasTriggered: boolean } {
      const wasTriggered = triggered;
      cancel();
      triggered = false;
      return { wasTriggered };
    },

    cancel() {
      cancel();
      triggered = false;
    },

    consumeClickSuppression(): boolean {
      if (!suppressNextClick) return false;
      suppressNextClick = false;
      return true;
    },

    shouldPreventContextMenu(): boolean {
      return pressing || triggered || suppressNextClick;
    },

    isPressing(): boolean {
      return pressing;
    },
  };
}
