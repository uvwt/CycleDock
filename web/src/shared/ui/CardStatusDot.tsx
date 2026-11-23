import { getCardVisualStatus, type CardVisualStatus } from "../lib/card-focus";

export { getCardVisualStatus, type CardVisualStatus };

export function CardStatusDot({
  status,
  muted = false,
  className = "",
  animateAnswering = true,
}: {
  status: CardVisualStatus;
  muted?: boolean;
  className?: string;
  animateAnswering?: boolean;
}) {
  if (status === "answering") {
    return (
      <span
        className={`relative flex size-2.5 shrink-0 items-center justify-center ${className}`}
        title="Agent 正在回答…"
        aria-label="Agent 正在回答…"
      >
        {animateAnswering ? (
          <span className="absolute inline-flex size-2 animate-ping rounded-full bg-emerald-400 opacity-75" />
        ) : null}
        <span className="relative inline-flex size-2 rounded-full bg-emerald-500 shadow-[0_0_6px_rgba(16,185,129,0.5)]" />
      </span>
    );
  }

  if (status === "unread") {
    // 免打扰卡片的未读降噪为浅灰小圆点，与普通未读的亮蓝点一眼区分。
    if (muted) {
      return (
        <span
          className={`flex size-2.5 shrink-0 items-center justify-center ${className}`}
          title="免打扰未读"
          aria-label="免打扰未读"
          data-muted-unread="true"
        >
          <span className="inline-flex size-2 rounded-full bg-muted-foreground/45" />
        </span>
      );
    }
    return (
      <span
        className={`flex size-2.5 shrink-0 items-center justify-center ${className}`}
        title="有新回复"
        aria-label="有新回复"
      >
        <span className="inline-flex size-2 rounded-full bg-sky-500 shadow-[0_0_4px_rgba(14,165,233,0.4)]" />
      </span>
    );
  }

  if (status === "approval") {
    return (
      <span
        className={`flex size-2.5 shrink-0 items-center justify-center ${className}`}
        title="等待人工确认"
        aria-label="等待人工确认"
      >
        <span className="inline-flex size-2 rounded-full bg-orange-500 shadow-[0_0_5px_rgba(249,115,22,0.5)]" />
      </span>
    );
  }

  if (status === "critical") {
    return (
      <span
        className={`flex size-2.5 shrink-0 items-center justify-center ${className}`}
        title="巡检异常"
        aria-label="巡检异常"
      >
        <span className="inline-flex size-2 rounded-full bg-rose-500 shadow-[0_0_5px_rgba(244,63,94,0.5)]" />
      </span>
    );
  }

  if (status === "warning") {
    return (
      <span
        className={`flex size-2.5 shrink-0 items-center justify-center ${className}`}
        title="巡检需留意"
        aria-label="巡检需留意"
      >
        <span className="inline-flex size-2 rounded-full bg-amber-500 shadow-[0_0_5px_rgba(245,158,11,0.5)]" />
      </span>
    );
  }

  return <span className={`size-2.5 shrink-0 ${className}`} />;
}
