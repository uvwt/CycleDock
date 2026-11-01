import type { Card } from "../api/types";

export type CardVisualStatus = "answering" | "unread" | "idle";

export function getCardVisualStatus(
  card: Pick<Card, "current_attempt_status" | "unread">,
  isActive: boolean = false,
  activeAttemptStatus?: string,
): CardVisualStatus {
  if (
    (isActive &&
      (activeAttemptStatus === "running" ||
        activeAttemptStatus === "queued")) ||
    card.current_attempt_status === "running" ||
    card.current_attempt_status === "queued"
  ) {
    return "answering";
  }

  // unread 由后端基于 message sequence + Human Source read cursor 计算；
  // 标题、置顶、状态变化不会推进这个游标。
  if (!isActive && card.unread) {
    return "unread";
  }

  return "idle";
}

export function CardStatusDot({
  status,
  className = "",
  animateAnswering = true,
}: {
  status: CardVisualStatus;
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

  return <span className={`size-2.5 shrink-0 ${className}`} />;
}
