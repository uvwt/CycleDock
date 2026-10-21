import type { ReactNode } from "react";
import { Bot, Info } from "lucide-react";
import { Avatar, Typography } from "./controls";

export function HumanMessage({
  children,
  author = "你",
}: {
  children: ReactNode;
  author?: string;
}) {
  return (
    <div className="flex min-w-0 justify-end" data-message-role="human">
      <div className="max-w-[min(80%,620px)] min-w-0">
        <Typography scale="meta" className="mb-1 text-right">
          {author}
        </Typography>
        <div className="v2-message-human min-w-0 break-words rounded-[var(--v2-radius-lg)] px-4 py-3 text-[var(--v2-type-body)]">
          {children}
        </div>
      </div>
    </div>
  );
}

export function AgentMessage({
  children,
  agent = "Cycle Agent",
  duration,
}: {
  children: ReactNode;
  agent?: string;
  duration?: string;
}) {
  return (
    <div className="flex min-w-0 items-start gap-3" data-message-role="agent">
      <Avatar fallback="C" />
      <div className="min-w-0 max-w-[min(82%,660px)] flex-1">
        <div className="mb-1 flex flex-wrap items-center justify-between gap-3">
          <Typography scale="meta" className="font-semibold">
            {agent}
          </Typography>
          {duration && (
            <Typography scale="meta" data-run-duration>
              运行 {duration}
            </Typography>
          )}
        </div>
        <div className="v2-message-agent min-w-0 break-words rounded-[var(--v2-radius-lg)] px-4 py-3">
          {children}
        </div>
      </div>
    </div>
  );
}

export function SystemEvent({ children }: { children: ReactNode }) {
  return (
    <div
      className="flex min-w-0 items-center justify-center gap-1.5 py-2 text-center text-[var(--v2-type-meta)] text-[var(--v2-muted)]"
      role="status"
      data-message-role="system"
    >
      <Info size={13} aria-hidden="true" />
      {children}
    </div>
  );
}

export function AgentRunMessage({
  status,
  duration,
  children,
}: {
  status: "queued" | "running" | "failed" | "completed";
  duration?: string;
  children: ReactNode;
}) {
  const title = {
    queued: "等待执行",
    running: "正在执行",
    failed: "执行失败",
    completed: "执行完成",
  }[status];
  return (
    <AgentMessage agent={title} duration={duration}>
      <div className="flex gap-2">
        <Bot
          size={16}
          className="mt-0.5 shrink-0 text-[var(--v2-muted)]"
          aria-hidden="true"
        />
        <div className="min-w-0">{children}</div>
      </div>
    </AgentMessage>
  );
}
