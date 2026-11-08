import {
  getCardVisualStatus,
  type CardVisualStatus,
} from "../lib/card-focus";

export { getCardVisualStatus, type CardVisualStatus };

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
