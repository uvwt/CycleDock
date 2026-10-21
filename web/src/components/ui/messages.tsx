import type { ReactNode } from "react";
import { Bot, Info } from "lucide-react";
import { Typography } from "./controls";

export function HumanMessage({
  children,
  author = "你",
}: {
  children: ReactNode;
  author?: string;
}) {
  return (
    <div className="product-human-message" data-message-role="human">
      <div className="min-w-0 max-w-[min(82%,620px)]">
        <Typography scale="meta" className="mb-1.5 text-right">
          {author}
        </Typography>
        <div className="product-human-bubble">{children}</div>
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
    <div className="product-agent-message" data-message-role="agent">
      <div className="product-agent-avatar" aria-hidden="true">
        C
      </div>
      <div className="product-agent-content">
        <div className="product-agent-meta">
          <Typography scale="meta" className="font-semibold">
            {agent}
          </Typography>
          {duration && (
            <Typography scale="meta" data-run-duration>
              运行 {duration}
            </Typography>
          )}
        </div>
        <div className="product-agent-body">{children}</div>
      </div>
    </div>
  );
}

export function SystemEvent({ children }: { children: ReactNode }) {
  return (
    <div
      className="product-system-event"
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
