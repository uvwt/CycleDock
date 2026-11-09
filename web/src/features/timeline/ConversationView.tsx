import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  ArrowDown,
  Bot,
  Check,
  Clock,
  Copy,
  MoreHorizontal,
  Pencil,
  Reply,
  ClipboardCheck,
  AlertCircle,
  Activity,
  CornerDownRight,
  Loader2,
  ChevronDown,
  ChevronUp,
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../../shared/ui/dropdown-menu";
import type {
  Attempt,
  Card,
  PendingTurn,
  TimelineEvent,
} from "../../shared/api/types";
import type { LiveReply } from "../../shared/realtime/cardStreamState";
import type { LiveAttempt } from "../../shared/realtime/useCardStream";
import {
  ChatBubble,
  ChatBubbleAction,
  ChatBubbleActionWrapper,
  ChatBubbleMessage,
  ChatBubbleTimestamp,
  ChatMessageList,
} from "../../shared/ui/chat";
import { OngoingHealthBadge } from "../../shared/ui/OngoingHealthBadge";
import {
  AttemptMetadata,
  ExecutionStatus,
  OperatingDevice,
  RunDuration,
  StandaloneExecution,
} from "./ExecutionStatus";
import { RichText } from "./RichText";
import { usePacedTimeline } from "./usePacedTimeline";
import { cn } from "../../shared/lib/utils";
import {
  groupExecutionAttempts,
  orderTimelineEvents,
  type ExecutionAttempt,
} from "./execution";

const FIVE_MINUTES_MS = 5 * 60 * 1000;
const TWO_HOURS_MS = 2 * 60 * 60 * 1000;
const systemTypes = new Set([
  "status_change",
  "progress",
  "checkin",
  "checkin_ack",
  "confirm",
  "reject",
]);
const labels: Record<string, string> = {
  status_change: "状态变化",
  progress: "阶段进度",
  checkin: "巡检",
  checkin_ack: "巡检已确认",
  confirm: "提交验收",
  reject: "驳回",
};

/** 状态机状态键名到界面中文名与语义圆点样式的映射 */
const statusInfoMap: Record<string, { label: string; dot: string }> = {
  // TodoState
  todo: { label: "待办", dot: "bg-slate-400 dark:bg-slate-500" },
  working: { label: "进行中", dot: "bg-sky-500" },
  review: { label: "待审核", dot: "bg-amber-500" },
  done: { label: "已完成", dot: "bg-emerald-500" },
  // OngoingState
  active: { label: "运行中", dot: "bg-emerald-500" },
  paused: { label: "已暂停", dot: "bg-amber-500" },
  archived: { label: "已归档", dot: "bg-slate-400 dark:bg-slate-500" },
  // DiscussionState & 通用别名
  open: { label: "进行中", dot: "bg-sky-500" },
  closed: { label: "已关闭", dot: "bg-slate-400 dark:bg-slate-500" },
  resolved: { label: "已完成", dot: "bg-emerald-500" },
  healthy: { label: "正常", dot: "bg-emerald-500" },
  warning: { label: "需留意", dot: "bg-amber-500" },
  critical: { label: "异常", dot: "bg-rose-500" },
  acknowledged: { label: "已确认", dot: "bg-slate-400 dark:bg-slate-500" },
  // 中文别名容错
  待办: { label: "待办", dot: "bg-slate-400 dark:bg-slate-500" },
  进行中: { label: "进行中", dot: "bg-sky-500" },
  待审核: { label: "待审核", dot: "bg-amber-500" },
  已完成: { label: "已完成", dot: "bg-emerald-500" },
  运行中: { label: "运行中", dot: "bg-emerald-500" },
  已暂停: { label: "已暂停", dot: "bg-amber-500" },
  已归档: { label: "已归档", dot: "bg-slate-400 dark:bg-slate-500" },
  已关闭: { label: "已关闭", dot: "bg-slate-400 dark:bg-slate-500" },
  正常: { label: "正常", dot: "bg-emerald-500" },
  需留意: { label: "需留意", dot: "bg-amber-500" },
  异常: { label: "异常", dot: "bg-rose-500" },
  已确认: { label: "已确认", dot: "bg-slate-400 dark:bg-slate-500" },
};

/** 解析形如 "review -> working" 或 "待审核 → 进行中" 的状态流转字串 */
function parseStatusTransition(content?: string) {
  if (!content) return null;
  const parts = content.split(/\s*->\s*|\s*→\s*/);
  if (parts.length === 2 && parts[0] && parts[1]) {
    return {
      from: parts[0].trim(),
      to: parts[1].trim(),
    };
  }
  return null;
}

/** 获取状态对应的展示名（遵循 i18n）与语义圆点色 */
function getStatusDisplay(
  raw: string,
  t: (key: string) => string,
): { label: string; dot: string } {
  const trimmed = raw.trim();
  const lower = trimmed.toLowerCase();
  const meta = statusInfoMap[lower] || statusInfoMap[trimmed];
  if (meta) {
    return {
      label: t(meta.label),
      dot: meta.dot,
    };
  }
  return {
    label: t(trimmed),
    dot: "bg-muted-foreground/40",
  };
}

/**
 * 待验收、驳回及巡检等系统卡片事件的视觉样式配置。
 * 巡检卡片采用与待验收（confirm）一致的结构化卡片气泡呈现，
 * 并根据健康状态（healthy / warning / critical）动态匹配主题色与图标，
 * 避免长文本在居中系统胶囊中折行拥挤、排版失衡。
 */
function getSystemCardStyle(type: string, checkinStatus?: string) {
  if (type === "confirm") {
    return {
      container:
        "border-amber-500/30 bg-amber-500/[0.04] dark:border-amber-500/25 dark:bg-amber-500/[0.08]",
      iconWrap:
        "bg-amber-500/15 text-amber-600 dark:bg-amber-500/25 dark:text-amber-400",
      title: "text-amber-700 dark:text-amber-300",
      icon: <ClipboardCheck className="size-3" />,
    };
  }
  if (type === "reject") {
    return {
      container:
        "border-red-500/30 bg-red-500/[0.04] dark:border-red-500/25 dark:bg-red-500/[0.08]",
      iconWrap:
        "bg-red-500/15 text-red-600 dark:bg-red-500/25 dark:text-red-400",
      title: "text-red-700 dark:text-red-300",
      icon: <AlertCircle className="size-3" />,
    };
  }
  if (type === "checkin") {
    if (checkinStatus === "healthy") {
      return {
        container:
          "border-emerald-500/30 bg-emerald-500/[0.04] dark:border-emerald-500/25 dark:bg-emerald-500/[0.08]",
        iconWrap:
          "bg-emerald-500/15 text-emerald-600 dark:bg-emerald-500/25 dark:text-emerald-400",
        title: "text-emerald-700 dark:text-emerald-300",
        icon: <Activity className="size-3" />,
      };
    }
    if (checkinStatus === "warning") {
      return {
        container:
          "border-amber-500/30 bg-amber-500/[0.04] dark:border-amber-500/25 dark:bg-amber-500/[0.08]",
        iconWrap:
          "bg-amber-500/15 text-amber-600 dark:bg-amber-500/25 dark:text-amber-400",
        title: "text-amber-700 dark:text-amber-300",
        icon: <Activity className="size-3" />,
      };
    }
    if (checkinStatus === "critical") {
      return {
        container:
          "border-rose-500/30 bg-rose-500/[0.04] dark:border-rose-500/25 dark:bg-rose-500/[0.08]",
        iconWrap:
          "bg-rose-500/15 text-rose-600 dark:bg-rose-500/25 dark:text-rose-400",
        title: "text-rose-700 dark:text-rose-300",
        icon: <Activity className="size-3" />,
      };
    }
    return {
      container:
        "border-sky-500/30 bg-sky-500/[0.04] dark:border-sky-500/25 dark:bg-sky-500/[0.08]",
      iconWrap:
        "bg-sky-500/15 text-sky-600 dark:bg-sky-500/25 dark:text-sky-400",
      title: "text-sky-700 dark:text-sky-300",
      icon: <Activity className="size-3" />,
    };
  }
  return null;
}

function formatEventContent(event: TimelineEvent): string {
  const message = event.type === "message" ? event.message : undefined;
  if (message?.type === "choice" && message.widget) {
    const parts: string[] = [];
    if (message.content?.trim()) parts.push(message.content.trim());
    if (message.widget.question?.trim()) parts.push(message.widget.question.trim());
    if (message.widget.options?.length) {
      parts.push(
        message.widget.options
          .map((opt, i) => `${i + 1}. ${opt.label || opt.value}`)
          .join("\n"),
      );
    }
    return parts.join("\n\n");
  }
  return message?.content || event.content || "";
}

/** 气泡上的发送者。内置 Agent 的来源名是 Cycle Agent，界面统一写成 Cycle。 */
function senderLabel(actorKind: string, actorName?: string) {
  if (actorKind === "human") return actorName || "";
  if (!actorName || actorName === "Cycle Agent") return "Cycle";
  return actorName;
}

export type QuotedMessage = {
  id: string;
  sender: string;
  text: string;
};

type ExecutionActions = {
  retryingAttemptID?: string;
  onRetryAttempt: (attemptID: string) => void;
  onReply?: (quote: QuotedMessage) => void;
  onEditMessage?: (eventID: string, content: string) => Promise<void>;
  canApproveReview?: boolean;
  approvingReview?: boolean;
  onApproveReview?: () => void;
  canAcknowledgeCheckin?: boolean;
  acknowledgingCheckin?: boolean;
  onAcknowledgeCheckin?: () => void;
};

function TimeSeparator({ value }: { value: string }) {
  const { i18n } = useTranslation();
  const time = new Date(value);
  return (
    <div
      role="separator"
      data-time-separator
      className="flex items-center justify-center py-2 text-[11px]"
    >
      <span className="rounded-md border border-line/60 bg-surface px-3 py-1 font-medium text-muted-foreground shadow-xs">
        {time.toLocaleString(i18n.language === "en" ? "en-US" : "zh-CN", {
          month: "short",
          day: "numeric",
          hour: "2-digit",
          minute: "2-digit",
        })}
      </span>
    </div>
  );
}

function EventRow({
  event,
  attempts,
  headerAttempts,
  showSender,
  preview,
  previewStatus,
  replyModel,
  live,
  isLatest = false,
  compact = false,
  ...actions
}: {
  event: TimelineEvent;
  attempts: ExecutionAttempt[];
  headerAttempts: Attempt[];
  showSender: boolean;
  preview?: LiveReply;
  previewStatus?: string;
  replyModel?: string;
  live: LiveAttempt | null;
  isLatest?: boolean;
  compact?: boolean;
} & ExecutionActions) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const [showTime, setShowTime] = useState(false);
  const [touchActive, setTouchActive] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editDraft, setEditDraft] = useState("");
  const [submittingEdit, setSubmittingEdit] = useState(false);
  const bubbleRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!touchActive) return;
    function handleOutsidePointerDown(event: PointerEvent) {
      const target = event.target as HTMLElement | null;
      if (!target) return;
      if (bubbleRef.current?.contains(target)) return;
      if (
        target.closest(
          "[data-radix-popper-content-wrapper], [role='menu'], [role='dialog']",
        )
      ) {
        return;
      }
      setTouchActive(false);
    }
    document.addEventListener("pointerdown", handleOutsidePointerDown);
    return () => {
      document.removeEventListener("pointerdown", handleOutsidePointerDown);
    };
  }, [touchActive]);

  const system = systemTypes.has(event.type);
  const human = event.actor_kind === "human";
  const isDelegate = !human && event.type === "comment";
  const message = event.type === "message" ? event.message : undefined;
  if (system) {
    const cardStyle = getSystemCardStyle(event.type, event.checkin_status);
    if (cardStyle) {
      const isConfirm = event.type === "confirm";
      const showApproveAction = Boolean(
        isConfirm && actions.canApproveReview && actions.onApproveReview,
      );
      const isCheckin = event.type === "checkin";
      const showAcknowledgeAction = Boolean(
        isCheckin &&
          (event.checkin_status === "warning" ||
            event.checkin_status === "critical") &&
          actions.canAcknowledgeCheckin &&
          actions.onAcknowledgeCheckin,
      );
      return (
        <>
          <ChatBubble
            id={"event-" + event.id}
            data-sequence={event.sequence}
            data-message-group-start={showSender}
            variant="received"
            className="w-fit max-w-[94%] sm:max-w-[85%]"
          >
            <div className="flex min-w-0 flex-col items-start">
              {showSender && (
                <div className="mb-1.5 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
                  <span className="font-semibold text-foreground/80">
                    {senderLabel(event.actor_kind, event.actor_name) || "Cycle"}
                  </span>
                </div>
              )}

              <ChatBubbleMessage
                variant="received"
                className={`w-fit max-w-full rounded-2xl border px-4 py-3 shadow-xs transition-colors ${cardStyle.container}`}
              >
                <div className="mb-2 flex items-center gap-2 border-b border-line/60 pb-2">
                  <span
                    className={`grid size-5 shrink-0 place-items-center rounded-full ${cardStyle.iconWrap}`}
                  >
                    {cardStyle.icon}
                  </span>
                  <span
                    className={`text-xs font-semibold tracking-wide ${cardStyle.title}`}
                  >
                    {t(labels[event.type] || event.type)}
                  </span>
                  {event.type === "checkin" && event.checkin_status && (
                    <OngoingHealthBadge
                      health={event.checkin_status}
                      variant="pill"
                      size="xs"
                    />
                  )}
                  {event.created_at && (
                    <span className="ml-auto pl-3 text-[11px] text-faint">
                      {new Date(event.created_at).toLocaleTimeString([], {
                        hour: "2-digit",
                        minute: "2-digit",
                      })}
                    </span>
                  )}
                </div>

                {event.content && <RichText content={event.content} />}

                {showApproveAction && (
                  <div className="mt-3 flex items-center justify-end border-t border-line/60 pt-2.5">
                    <button
                      type="button"
                      disabled={actions.approvingReview}
                      onClick={actions.onApproveReview}
                      className="inline-flex cursor-pointer items-center gap-1.5 rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white shadow-2xs transition hover:bg-emerald-500 active:scale-95 disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      <Check className="size-3.5" />
                      <span>{t("通过验收")}</span>
                    </button>
                  </div>
                )}

                {showAcknowledgeAction && (
                  <div className="mt-3 flex items-center justify-end border-t border-line/60 pt-2.5">
                    <button
                      type="button"
                      disabled={actions.acknowledgingCheckin}
                      onClick={actions.onAcknowledgeCheckin}
                      className="inline-flex cursor-pointer items-center gap-1.5 rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white shadow-2xs transition hover:bg-emerald-500 active:scale-95 disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      <Check className="size-3.5" />
                      <span>{t("已确认")}</span>
                    </button>
                  </div>
                )}
              </ChatBubbleMessage>
            </div>
          </ChatBubble>
          {attempts.length > 0 && (
            <div className="mt-3">
              <ExecutionStatus attempts={attempts} live={live} {...actions} />
            </div>
          )}
        </>
      );
    }
    if (event.type === "status_change" || event.type === "checkin_ack") {
      const transition = parseStatusTransition(event.content);
      const fromMeta = transition ? getStatusDisplay(transition.from, t) : null;
      const toMeta = transition ? getStatusDisplay(transition.to, t) : null;
      return (
        <>
          <div
            id={"event-" + event.id}
            className="flex justify-center py-0.5 text-xs select-none"
            data-sequence={event.sequence}
            data-status-change
            title={
              event.created_at
                ? `${event.actor_name ? senderLabel(event.actor_kind, event.actor_name) + " · " : ""}${new Date(event.created_at).toLocaleString()}`
                : undefined
            }
          >
            <span className="inline-flex max-w-[90%] items-center gap-1.5 rounded-full border border-line/50 bg-muted/20 dark:bg-muted/10 px-2.5 py-0.5 text-[11px] text-muted-foreground transition-colors hover:bg-muted/30">
              <span className="text-muted-foreground/60 font-normal">
                {t(labels[event.type] || "状态变化")}
              </span>
              <span className="text-muted-foreground/30 font-normal">·</span>
              {fromMeta && toMeta ? (
                <>
                  <span className="inline-flex items-center gap-1 font-medium text-foreground/80">
                    <span
                      className={cn(
                        "size-1.5 rounded-full shrink-0",
                        fromMeta.dot,
                      )}
                    />
                    <span>{fromMeta.label}</span>
                  </span>
                  <span className="text-muted-foreground/40 text-[10px] select-none">
                    →
                  </span>
                  <span className="inline-flex items-center gap-1 font-medium text-foreground/80">
                    <span
                      className={cn(
                        "size-1.5 rounded-full shrink-0",
                        toMeta.dot,
                      )}
                    />
                    <span>{toMeta.label}</span>
                  </span>
                </>
              ) : (
                <span className="font-medium text-foreground/80">
                  {event.content}
                </span>
              )}
            </span>
          </div>
          {attempts.length > 0 && (
            <div className="mt-3">
              <ExecutionStatus attempts={attempts} live={live} {...actions} />
            </div>
          )}
        </>
      );
    }
    return (
      <>
        <div
          id={"event-" + event.id}
          className="flex justify-center py-1.5"
          data-sequence={event.sequence}
        >
          <span className="inline-flex max-w-[90%] items-center justify-center gap-1.5 rounded-md border border-line/60 bg-surface px-3 py-1 text-center text-xs text-muted-foreground">
            <span>
              {t(labels[event.type] || event.type)} · {event.content}
            </span>
          </span>
        </div>
        {attempts.length > 0 && (
          <div className="mt-3">
            <ExecutionStatus attempts={attempts} live={live} {...actions} />
          </div>
        )}
      </>
    );
  }

  const formattedTime = event.created_at
    ? new Date(event.created_at).toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
      })
    : "";

  const fullTime = event.created_at
    ? new Date(event.created_at).toLocaleString([], {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      })
    : "";

  const hasMetadata = Boolean(
    headerAttempts.length > 0 ||
      (preview &&
        !live?.finished &&
        event.attempt_id === live?.attemptID) ||
      (preview?.failed &&
        !["cancelled", "superseded"].includes(previewStatus || "")),
  );

  const hasActiveAttempt =
    headerAttempts.some((attempt) => attempt.status === "running") ||
    Boolean(
      preview &&
        !live?.finished &&
        event.attempt_id === live?.attemptID,
    );

  const activeDeviceName =
    isLatest &&
    !live?.finished &&
    live?.deviceName &&
    (event.attempt_id === live?.attemptID ||
      headerAttempts.some((attempt) => attempt.id === live?.attemptID))
      ? live.deviceName
      : undefined;

  const rawText = formatEventContent(event);

  async function handleCopy() {
    if (!rawText) return;
    try {
      await navigator.clipboard.writeText(rawText);
      setCopied(true);
      toast.success(t("已复制到剪贴板"));
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error(t("复制失败"));
    }
  }

  function handleReply() {
    if (!rawText || !actions.onReply) return;
    actions.onReply({
      id: event.id,
      sender:
        senderLabel(event.actor_kind, event.actor_name) ||
        (human ? t("你") : "Cycle"),
      text: rawText,
    });
  }

  const canEdit = Boolean(
    human && event.type === "comment" && actions.onEditMessage,
  );

  function handleStartEdit() {
    if (!canEdit) return;
    setEditDraft(rawText);
    setEditing(true);
    setTouchActive(false);
  }

  async function handleSubmitEdit() {
    const trimmed = editDraft.trim();
    if (!trimmed || !actions.onEditMessage || submittingEdit) return;
    setSubmittingEdit(true);
    try {
      await actions.onEditMessage(event.id, trimmed);
      setEditing(false);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("编辑失败"));
    } finally {
      setSubmittingEdit(false);
    }
  }

  if (editing) {
    return (
      <div
        id={"event-" + event.id}
        data-sequence={event.sequence}
        className={`ml-auto w-full ${compact ? "max-w-[92%]" : "max-w-[94%] sm:max-w-xl"}`}
      >
        <div className="rounded-2xl border border-primary/40 bg-surface p-3 shadow-xs">
          <textarea
            value={editDraft}
            onChange={(e) => setEditDraft(e.target.value)}
            disabled={submittingEdit}
            autoFocus
            rows={Math.min(8, Math.max(2, editDraft.split("\n").length))}
            onKeyDown={(e) => {
              if (e.key === "Escape" && !submittingEdit) {
                e.preventDefault();
                setEditing(false);
              } else if (
                e.key === "Enter" &&
                (e.metaKey || e.ctrlKey) &&
                !submittingEdit
              ) {
                e.preventDefault();
                void handleSubmitEdit();
              }
            }}
            className="w-full resize-none bg-transparent text-sm leading-relaxed text-foreground outline-none placeholder:text-faint"
            placeholder={t("输入修改后的消息内容...")}
          />
          <div className="mt-2.5 flex items-center justify-between gap-2 border-t border-line/60 pt-2">
            <span className="text-[11px] text-faint">
              {t("修改后将清除后续记录并重新回答")}
            </span>
            <div className="flex items-center gap-1.5">
              <button
                type="button"
                disabled={submittingEdit}
                onClick={() => setEditing(false)}
                className="rounded-lg px-2.5 py-1 text-xs font-medium text-muted-foreground transition hover:bg-muted hover:text-foreground disabled:opacity-50"
              >
                {t("取消")}
              </button>
              <button
                type="button"
                disabled={submittingEdit || !editDraft.trim()}
                onClick={() => void handleSubmitEdit()}
                className="inline-flex items-center gap-1.5 rounded-lg bg-primary px-3 py-1 text-xs font-medium text-primary-foreground shadow-2xs transition hover:bg-primary/90 disabled:opacity-50"
              >
                {submittingEdit && (
                  <Loader2 className="size-3 animate-spin" />
                )}
                <span>{t("重新发送")}</span>
              </button>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <>
      <ChatBubble
        ref={bubbleRef}
        id={"event-" + event.id}
        data-sequence={event.sequence}
        data-message-group-start={showSender}
        variant={human ? "sent" : "received"}
        className={`w-fit ${compact ? "max-w-[88%]" : "max-w-[92%] sm:max-w-[85%]"} ${human ? "ml-auto" : ""}`}
      >
        <div
          className={`flex min-w-0 flex-col ${human ? "items-end" : "items-start"}`}
        >
          {showSender && !human && (
            <div className="mb-1.5 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
              <span className="font-semibold text-foreground/80">
                {senderLabel(event.actor_kind, event.actor_name)}
              </span>
              {isDelegate && (
                event.source_card_id ? (
                  <Link
                    to={`/cards/${event.source_card_id}`}
                    className="inline-flex items-center gap-1 rounded-full border border-indigo-200/80 bg-indigo-50/80 px-2 py-0.5 text-[11px] font-medium text-indigo-700 transition hover:bg-indigo-100 hover:text-indigo-800 dark:border-indigo-800/80 dark:bg-indigo-950/50 dark:text-indigo-300 dark:hover:bg-indigo-900/60 dark:hover:text-indigo-200"
                    title={t("查看来源会话: {{title}}", {
                      title: event.source_card_title || t("未命名卡片"),
                    })}
                  >
                    <CornerDownRight className="h-3 w-3 shrink-0" />
                    <span>
                      {t("跨卡片委派")}: 「{event.source_card_title || t("来源卡片")}」
                    </span>
                  </Link>
                ) : (
                  <span className="inline-flex items-center gap-1 rounded-full border border-indigo-200/80 bg-indigo-50/80 px-2 py-0.5 text-[11px] font-medium text-indigo-700 dark:border-indigo-800/80 dark:bg-indigo-950/50 dark:text-indigo-300">
                    <CornerDownRight className="h-3 w-3" />
                    {t("跨卡片委派")}
                  </span>
                )
              )}
              {hasMetadata && (
                <div
                  className={`inline-flex items-center gap-x-2 transition-opacity duration-150 ${
                    hasActiveAttempt || touchActive
                      ? "opacity-100"
                      : "opacity-0 group-hover:opacity-100 group-focus-within:opacity-100"
                  }`}
                >
                  <span className="select-none text-[10px] text-muted-foreground/40">
                    •
                  </span>
                  <div className="inline-flex items-center gap-1.5 text-[11px] font-normal text-faint">
                    {preview &&
                      !live?.finished &&
                      event.attempt_id === live?.attemptID &&
                      !headerAttempts.length && (
                        <RunDuration startedAt={live?.startedAt} active />
                      )}
                    {preview?.failed &&
                      !["cancelled", "superseded"].includes(
                        previewStatus || "",
                      ) && (
                        <span className="text-[11px] font-normal text-faint">
                          {t("消息未发送")}
                        </span>
                      )}
                    {headerAttempts.map((attempt) => (
                      <AttemptMetadata
                        key={attempt.id}
                        attempt={attempt}
                        {...actions}
                      />
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}

          <div className="flex flex-wrap items-end gap-2.5 max-w-full">
            <ChatBubbleMessage
              variant={human ? "sent" : "received"}
              onClick={(e) => {
                const target = e.target as HTMLElement;
                if (
                  target.closest(
                    "a, button, input, textarea, [role='menuitem']",
                  )
                ) {
                  return;
                }
                if (
                  typeof window !== "undefined" &&
                  window.getSelection()?.toString().trim()
                ) {
                  return;
                }
                setTouchActive((prev) => !prev);
              }}
              className={`w-fit max-w-full ${
                isDelegate
                  ? "border border-indigo-200/70 bg-indigo-50/40 text-foreground dark:border-indigo-800/60 dark:bg-indigo-950/20"
                  : ""
              }`}
            >
            {(event.type === "finding" || event.type === "result") && (
              <p
                className={`mb-1 text-xs font-semibold ${event.type === "finding" ? "text-sky-700 dark:text-sky-400" : "text-emerald-700 dark:text-emerald-400"}`}
              >
                {t(event.type === "finding" ? "发现" : "结果")}
              </p>
            )}
            {rawText && <RichText content={rawText} />}
            {message?.type === "attachment" && message.url && (
              <a
                href={message.url}
                target="_blank"
                rel="noopener noreferrer"
                className="mt-2 block break-all text-sm text-sky-700 underline underline-offset-2 dark:text-sky-400"
              >
                {message.alt || message.url}
              </a>
            )}
            {message?.images && message.images.length > 0 && (
              <div className="mt-3 grid grid-cols-2 gap-2">
                {message.images.map((src) => (
                  <a
                    key={src}
                    href={src}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    <img
                      src={src}
                      alt={message.alt || t("消息图片")}
                      loading="lazy"
                      referrerPolicy="no-referrer"
                      className="max-h-72 w-full rounded-lg object-cover"
                    />
                  </a>
                ))}
              </div>
            )}
            {showTime && formattedTime && (
              <ChatBubbleTimestamp
                timestamp={formattedTime}
                status={human ? "read" : undefined}
              />
            )}
          </ChatBubbleMessage>
          {activeDeviceName && (
            <div className="self-end pb-1 shrink-0">
              <OperatingDevice name={activeDeviceName} />
            </div>
          )}
        </div>
      </div>

        {rawText && (
          <ChatBubbleActionWrapper
            className={
              compact
                ? `${touchActive ? "pointer-events-auto opacity-100" : "pointer-events-none"} absolute -bottom-3 z-10 pb-0 group-hover:pointer-events-auto group-focus-within:pointer-events-auto group-focus-within:opacity-100 has-[[data-state=open]]:pointer-events-auto has-[[data-state=open]]:opacity-100 ${human ? "right-1" : "left-1"}`
                : `${touchActive ? "opacity-100 max-sm:pointer-events-auto" : "max-sm:pointer-events-none"} max-sm:absolute max-sm:-bottom-3 max-sm:z-10 max-sm:pb-0 max-sm:group-hover:pointer-events-auto max-sm:group-focus-within:pointer-events-auto group-focus-within:opacity-100 max-sm:has-[[data-state=open]]:pointer-events-auto has-[[data-state=open]]:opacity-100 ${human ? "max-sm:right-1" : "max-sm:left-1"}`
            }
          >
            <ChatBubbleAction
              icon={
                copied ? (
                  <Check className="size-3.5 text-primary" />
                ) : (
                  <Copy className="size-3.5" />
                )
              }
              title={copied ? t("已复制") : t("复制全文")}
              aria-label={t("复制全文")}
              onClick={handleCopy}
            />
            {canEdit && (
              <ChatBubbleAction
                icon={<Pencil className="size-3.5" />}
                title={t("编辑并重新回答")}
                aria-label={t("编辑并重新回答")}
                onClick={handleStartEdit}
              />
            )}
            {actions.onReply && (
              <ChatBubbleAction
                icon={<Reply className="size-3.5" />}
                title={t("引用回复")}
                aria-label={t("引用回复")}
                onClick={handleReply}
              />
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <ChatBubbleAction
                  icon={<MoreHorizontal className="size-3.5" />}
                  title={t("具体时间与更多")}
                  aria-label={t("具体时间与更多")}
                />
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align={human ? "end" : "start"}
                className="w-auto min-w-[170px] p-1.5 shadow-md"
              >
                {!human && replyModel && (
                  <div className="flex items-center gap-2 px-2.5 py-1.5 text-xs text-muted-foreground select-none">
                    <Bot className="size-3.5 text-primary shrink-0" />
                    <div className="flex flex-col">
                      <span className="text-[10px] text-faint">
                        {t("回复模型")}
                      </span>
                      <span className="font-semibold text-foreground text-xs break-all">
                        {replyModel}
                      </span>
                    </div>
                  </div>
                )}
                <div className="flex items-center gap-2 px-2.5 py-1.5 text-xs text-muted-foreground select-none">
                  <Clock className="size-3.5 text-primary shrink-0" />
                  <div className="flex flex-col">
                    <span className="text-[10px] text-faint">
                      {t("具体时间")}
                    </span>
                    <span className="font-semibold text-foreground text-xs">
                      {fullTime || formattedTime}
                    </span>
                  </div>
                </div>
                <DropdownMenuSeparator className="my-1" />
                <DropdownMenuItem
                  onClick={() => setShowTime((v) => !v)}
                  className="text-xs cursor-pointer flex items-center gap-2"
                >
                  <Clock className="size-3.5 text-muted-foreground" />
                  <span>
                    {showTime ? t("隐藏气泡时间") : t("在气泡中显示时间")}
                  </span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </ChatBubbleActionWrapper>
        )}
      </ChatBubble>
      {attempts.length > 0 && (
        <div className="mt-3">
          <ExecutionStatus attempts={attempts} live={live} {...actions} />
        </div>
      )}
    </>
  );
}

function ConversationEmptyCardDescription({ description }: { description?: string }) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const trimmed = description?.trim();

  if (!trimmed) {
    return (
      <div className="px-5 py-4 sm:px-6 sm:py-5">
        <p className="text-xs text-faint">
          {t("暂无卡片详细描述")}
        </p>
      </div>
    );
  }

  const isLong = trimmed.length > 240 || trimmed.split("\n").length > 4;

  return (
    <div className="px-5 py-4 sm:px-6 sm:py-5 space-y-2">
      <div
        className={`relative overflow-hidden transition-all ${
          !expanded && isLong ? "max-h-28" : "max-h-[50vh] overflow-y-auto pr-1 scrollbar-thin"
        }`}
      >
        <RichText
          content={trimmed}
          className="text-sm leading-relaxed text-secondary-foreground"
        />
        {!expanded && isLong && (
          <div className="pointer-events-none absolute inset-x-0 bottom-0 h-10 bg-gradient-to-t from-surface to-transparent" />
        )}
      </div>
      {isLong && (
        <div className="flex justify-end pt-1">
          <button
            type="button"
            onClick={() => setExpanded((prev) => !prev)}
            className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-muted-foreground hover:text-foreground hover:bg-muted/50 transition cursor-pointer"
          >
            <span>{expanded ? t("收起描述") : t("展开全部描述")}</span>
            {expanded ? <ChevronUp className="size-3.5" /> : <ChevronDown className="size-3.5" />}
          </button>
        </div>
      )}
    </div>
  );
}

export function ConversationView({
  card,
  events,
  live,
  cardKind,
  pendingTurns,
  currentAttempt,
  attemptHistory,
  bottomSpacerClass = "h-28 shrink-0 sm:h-32",
  bottomInsetPx,
  compact = false,
  hasMoreBefore = false,
  loadingMoreBefore = false,
  onLoadMoreBefore,
  ...actions
}: {
  card?: Card;
  events: TimelineEvent[];
  live: LiveAttempt | null;
  cardKind: string;
  pendingTurns: PendingTurn[];
  currentAttempt: Attempt | null;
  attemptHistory: Attempt[];
  bottomSpacerClass?: string;
  bottomInsetPx?: number;
  compact?: boolean;
  hasMoreBefore?: boolean;
  loadingMoreBefore?: boolean;
  onLoadMoreBefore?: () => void;
} & ExecutionActions) {
  const { t } = useTranslation();
  const bottom = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [showLatest, setShowLatest] = useState(false);

  const executions = useMemo(
    () => [...attemptHistory, ...(currentAttempt ? [currentAttempt] : [])],
    [attemptHistory, currentAttempt],
  );

  // 只有已经被领取的输入进入对话，排队输入由输入框上方的列表承载。
  // 传入 executions 以便按业务轮次交错呈现一问一答，而非按入队时的物理 sequence 扎堆。
  const orderedEvents = useMemo(() => {
    const pendingEventIDs = new Set(
      pendingTurns.map((turn) => turn.source_event_id),
    );
    return orderTimelineEvents(events, executions).filter(
      (event) => !pendingEventIDs.has(event.id),
    );
  }, [events, pendingTurns, executions]);
  const outputs = [
    ...(live?.previous || []).filter((item) =>
      executions.some((attempt) => attempt.id === item.attemptID),
    ),
    ...(live ? [live] : []),
  ];
  // 预览转正式消息后仍保留原 key；SSE 重连或下一轮开始也不能重建已显示的气泡。
  const [previewKeys, setPreviewKeys] = useState(
    () => new Map<string, string>(),
  );
  const newBindings = (live?.replies || []).filter(
    (reply) => reply.eventID && !previewKeys.has(reply.eventID),
  );
  if (newBindings.length > 0) {
    const nextKeys = new Map(previewKeys);
    for (const reply of newBindings) nextKeys.set(reply.eventID!, reply.id);
    setPreviewKeys(nextKeys);
  }
  const revealThrough = orderedEvents.reduce(
    (sequence, event) =>
      previewKeys.has(event.id) ||
      outputs.some(
        (output) =>
          output.attemptID === event.attempt_id &&
          output.replies.some(
            (reply) => reply.id === event.message?.preview_id,
          ),
      )
        ? Math.max(sequence, event.sequence)
        : sequence,
    0,
  );
  const { visibleEvents: sortedEvents, historyThrough, historyIDs } =
    usePacedTimeline(orderedEvents, revealThrough);
  const pendingReplies = (live?.replies || []).filter(
    (reply) =>
      !orderedEvents.some(
        (event) =>
          event.id === reply.eventID || event.message?.preview_id === reply.id,
      ),
  );
  const rows = sortedEvents.map((event) => {
    const preview = event.message?.preview
      ? outputs
          .find((output) => output.attemptID === event.attempt_id)
          ?.replies.find((reply) => reply.id === event.message?.preview_id)
      : undefined;
    // REST 快照承载身份和刷新恢复，SSE 在同一气泡上补齐最新正文。
    return {
      event:
        preview &&
        event.message &&
        preview.text.startsWith(event.message.content || event.content)
          ? {
              ...event,
              content: preview.text,
              message: { ...event.message, content: preview.text },
            }
          : event,
      preview,
      key: event.message?.preview_id || previewKeys.get(event.id) || event.id,
    };
  });
  // 旧预览留在原执行的位置；新一轮的正文和运行气泡仍显示在时间线末尾。
  for (const output of outputs) {
    const sender = orderedEvents.findLast(
      (event) =>
        event.attempt_id === output.attemptID && event.actor_kind !== "human",
    );
    const execution = executions.find(
      (attempt) => attempt.id === output.attemptID,
    );
    const replies = output.replies.filter(
      (reply) =>
        !orderedEvents.some(
          (event) =>
            event.id === reply.eventID ||
            event.message?.preview_id === reply.id,
        ),
    );
    const historical =
      output.attemptID !== live?.attemptID ||
      ["cancelled", "superseded"].includes(execution?.status || "");
    const anchor = rows.findLastIndex(
      ({ event }) =>
        event.attempt_id === output.attemptID ||
        event.id === execution?.source_event_id,
    );
    const insertAt = historical && anchor >= 0 ? anchor + 1 : rows.length;
    rows.splice(
      insertAt,
      0,
      ...replies.map((preview, index) => ({
        key: preview.id,
        preview,
        event: {
          id: preview.eventID || preview.id,
          sequence: (rows[insertAt - 1]?.event.sequence || 0) + index + 1,
          attempt_id: output.attemptID,
          type: "message" as const,
          actor_kind: sender?.actor_kind || "cycle_agent",
          actor_name: sender?.actor_name || "Cycle",
          content: preview.text,
          created_at: output.startedAt || execution?.started_at || "",
        },
      })),
    );
  }
  const displayEvents = rows.map((row) => row.event);
  const displayLive = live ? { ...live, replies: pendingReplies } : null;
  const { byEvent, standalone, statusByReply, running } =
    groupExecutionAttempts(orderedEvents, currentAttempt, attemptHistory);

  const groupStarts = new Set<string>();
  const statusByHeader = new Map<string, Attempt[]>();
  let headerID = "";
  for (const [index, event] of displayEvents.entries()) {
    const previous = displayEvents[index - 1];
    const interval = previous
      ? Date.parse(event.created_at) - Date.parse(previous.created_at)
      : NaN;
    // 执行信息归到消息组首，避免在连续回复中重复展示发送者。
    const continuesGroup =
      !!previous &&
      !systemTypes.has(previous.type) &&
      !systemTypes.has(event.type) &&
      (!(
        previewKeys.has(event.id) ||
        (live && event.attempt_id === live.attemptID)
      ) ||
        previous.attempt_id === event.attempt_id) &&
      previous.actor_kind === event.actor_kind &&
      senderLabel(previous.actor_kind, previous.actor_name) ===
        senderLabel(event.actor_kind, event.actor_name) &&
      interval >= 0 &&
      interval <= FIVE_MINUTES_MS;
    if (!continuesGroup) {
      headerID = event.id;
      groupStarts.add(headerID);
    }
    const execution = statusByReply.get(event.id);
    if (execution) {
      const attempts = statusByHeader.get(headerID) || [];
      attempts.push(execution);
      statusByHeader.set(headerID, attempts);
    }
  }
  const lastEvent = displayEvents.at(-1);
  const latestConfirmID = displayEvents.findLast(
    (event) => event.type === "confirm",
  )?.id;
  const latestCheckinID = displayEvents.findLast(
    (event) => event.type === "checkin",
  )?.id;
  // 运行圆点是当前 Attempt 的下一条气泡。紧挨着本轮 Cycle 回复时不再重复头像和名称。
  // 必须是同一 Attempt：新的一轮即使发送者仍是 Cycle，也要重新带头像。
  const runningContinues = Boolean(
    running &&
      lastEvent &&
      !systemTypes.has(lastEvent.type) &&
      lastEvent.actor_kind !== "human" &&
      lastEvent.attempt_id === running.value.id &&
      senderLabel(lastEvent.actor_kind, lastEvent.actor_name) === "Cycle",
  );

  // 当运行状态紧接着本轮气泡时，将正在执行的时长挂载到当前组首气泡，气泡下方圆点不重复展示孤立时间
  if (runningContinues && running && headerID) {
    const attempts = statusByHeader.get(headerID) || [];
    if (!attempts.some((a) => a.id === running.value.id)) {
      const runningAttempt: Attempt = {
        ...running.value,
        started_at: running.value.started_at || live?.startedAt,
      };
      statusByHeader.set(headerID, [...attempts, runningAttempt]);
    }
  }

  const scrollToBottom = (smooth = false) => {
    const element = list.current;
    if (!element) return;
    if (smooth) {
      element.scrollTo({ top: element.scrollHeight, behavior: "smooth" });
    } else {
      element.scrollTop = element.scrollHeight;
    }
  };

  // 仅在真实新内容到达或底部避让高度变化（而非用户滚动触发的组件重渲染）时，若处于吸底状态则跟随最新内容滚动
  const contentSignature = [
    sortedEvents.length,
    live?.attemptID,
    live?.stage,
    live?.replies?.map((r) => r.text.length).join(","),
    pendingTurns.length,
    currentAttempt?.id,
    currentAttempt?.status,
    attemptHistory.length,
    bottomInsetPx ?? "",
  ].join(":");

  useEffect(() => {
    if (stick.current) {
      scrollToBottom(false);
      const raf = requestAnimationFrame(() => scrollToBottom(false));
      return () => cancelAnimationFrame(raf);
    }
  }, [contentSignature]);

  // 移动端软键盘弹起/收起或容器高度改变时，若原本吸底则自动贴齐底部
  useEffect(() => {
    const element = list.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => {
      if (stick.current) {
        scrollToBottom(false);
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  // 首次挂载或会话切换时，等待富文本和字体渲染稳定后再次对齐底部，防止末尾消息被遮挡
  useEffect(() => {
    scrollToBottom(false);
    const timer = window.setTimeout(() => {
      if (stick.current) {
        scrollToBottom(false);
      }
    }, 60);
    return () => window.clearTimeout(timer);
  }, []);

  // 记录向上加载历史前后的滚动位置偏移，精准维持用户视口不跳动
  const prevScrollHeightRef = useRef<number | null>(null);

  useLayoutEffect(() => {
    if (prevScrollHeightRef.current !== null && list.current) {
      const diff = list.current.scrollHeight - prevScrollHeightRef.current;
      if (diff > 0) {
        list.current.scrollTop += diff;
      }
      prevScrollHeightRef.current = null;
    }
  }, [sortedEvents.length]);

  function handleLoadMoreBefore() {
    if (!hasMoreBefore || loadingMoreBefore || !onLoadMoreBefore) return;
    if (list.current) {
      prevScrollHeightRef.current = list.current.scrollHeight;
    }
    onLoadMoreBefore();
  }

  function onScroll() {
    const element = list.current;
    if (!element) return;
    if (element.scrollTop < 60 && hasMoreBefore && !loadingMoreBefore && onLoadMoreBefore) {
      handleLoadMoreBefore();
    }
    const distanceToBottom =
      element.scrollHeight - element.scrollTop - element.clientHeight;
    const nearBottom = distanceToBottom < 100;
    stick.current = nearBottom;
    setShowLatest((prev) => {
      const next = !nearBottom;
      return prev !== next ? next : prev;
    });
  }

  return (
    <div className="relative z-0 flex min-h-0 w-full flex-1 flex-col">
      <ChatMessageList
        ref={list}
        onScroll={onScroll}
        aria-label={t("统一消息时间线")}
        className={
          compact ? "px-3 pt-11 pb-2 sm:px-3.5 sm:pt-11" : undefined
        }
      >
        {hasMoreBefore && (
          <div className="flex justify-center py-2">
            <button
              type="button"
              disabled={loadingMoreBefore}
              onClick={handleLoadMoreBefore}
              className="inline-flex items-center gap-1.5 rounded-full border border-line bg-surface/80 px-3 py-1 text-xs text-muted-foreground transition hover:bg-muted hover:text-foreground disabled:opacity-50"
            >
              {loadingMoreBefore ? (
                <>
                  <Loader2 className="size-3 animate-spin" />
                  <span>{t("加载历史消息中...")}</span>
                </>
              ) : (
                <span>{t("查看更早的历史消息")}</span>
              )}
            </button>
          </div>
        )}
        {!sortedEvents.length &&
          !live &&
          !standalone.length &&
          !running &&
          !pendingTurns.length && (
            <div className="py-10 px-4 sm:py-14 sm:px-6">
              <div className="mx-auto max-w-2xl">
                {card?.title ? (
                  <div className="overflow-hidden rounded-2xl border border-line/80 bg-surface/90 shadow-xs backdrop-blur-xs">
                    <div className="border-b border-line/50 bg-muted/25 px-5 py-4 sm:px-6">
                      <div className="mb-2 flex flex-wrap items-center gap-2">
                        <span className="inline-flex items-center rounded-md border border-line/70 bg-surface px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                          {cardKind === "todo"
                            ? t("待办")
                            : cardKind === "ongoing"
                              ? t("定时任务")
                              : t("讨论")}
                        </span>
                        {card.kind === "todo" && card.todo_state && (
                          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                            <span
                              className={`inline-block size-1.5 rounded-full ${
                                card.todo_state === "todo"
                                  ? "bg-slate-400"
                                  : card.todo_state === "working"
                                    ? "bg-sky-500"
                                    : card.todo_state === "review"
                                      ? "bg-amber-500"
                                      : "bg-emerald-500"
                              }`}
                            />
                            <span>
                              {card.todo_state === "todo"
                                ? t("待办")
                                : card.todo_state === "working"
                                  ? t("进行中")
                                  : card.todo_state === "review"
                                    ? t("待验收")
                                    : t("已完成")}
                            </span>
                          </span>
                        )}
                        {card.kind === "ongoing" && card.ongoing_state && (
                          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                            <span
                              className={`inline-block size-1.5 rounded-full ${
                                card.ongoing_state === "active"
                                  ? "bg-emerald-500"
                                  : card.ongoing_state === "paused"
                                    ? "bg-amber-500"
                                    : "bg-slate-400"
                              }`}
                            />
                            <span>
                              {card.ongoing_state === "active"
                                ? t("运行中")
                                : card.ongoing_state === "paused"
                                  ? t("已暂停")
                                  : t("已归档")}
                            </span>
                          </span>
                        )}
                      </div>
                      <h2 className="text-lg font-semibold leading-snug tracking-tight text-heading break-words sm:text-xl">
                        {card.title}
                      </h2>
                    </div>

                    <ConversationEmptyCardDescription description={card.description} />

                    <div className="flex items-center gap-2 border-t border-line/50 bg-muted/15 px-5 py-3 text-xs text-muted-foreground sm:px-6">
                      <Bot className="size-4 shrink-0 text-faint" />
                      <span>
                        {t("你的消息、Agent 结果和任务状态都会出现在这里。")}
                      </span>
                    </div>
                  </div>
                ) : (
                  <div className="text-center">
                    <div className="mx-auto mb-4 grid size-14 place-items-center rounded-2xl bg-muted text-faint">
                      <Bot className="size-7" />
                    </div>
                    <h2 className="text-base font-semibold text-heading">
                      {cardKind === "discussion"
                        ? t("从第一句话开始")
                        : t("开始协作")}
                    </h2>
                    <p className="mt-1.5 text-xs text-muted-foreground">
                      {t("你的消息、Agent 结果和任务状态都会出现在这里。")}
                    </p>
                  </div>
                )}
              </div>
            </div>
          )}

        {rows.map(({ event, preview, key }, index) => {
          const previous = displayEvents[index - 1];
          const showTime =
            !!previous &&
            new Date(event.created_at).getTime() -
              new Date(previous.created_at).getTime() >
              TWO_HOURS_MS;
          const continuesGroup = !groupStarts.has(event.id);
          const isConsecutiveStatusChange =
            previous?.type === "status_change" &&
            event.type === "status_change";
          const matchedAttempt = executions.find(
            (attempt) => attempt.id === event.attempt_id,
          );
          const rowHeaderAttempts = statusByHeader.get(event.id) || [];
          const replyModel =
            event.message?.model ||
            matchedAttempt?.model ||
            rowHeaderAttempts.find((attempt) => attempt.model)?.model;
          return (
            <div
              key={key}
              className={`${!preview && !previewKeys.has(event.id) && (historyIDs ? !historyIDs.has(event.id) : event.sequence > historyThrough) ? "message-enter" : ""} ${index === 0 ? "" : isConsecutiveStatusChange ? "mt-0.5" : continuesGroup ? "mt-2.5" : "mt-4"}`}
            >
              {showTime && <TimeSeparator value={event.created_at} />}
              <EventRow
                event={event}
                preview={preview}
                previewStatus={matchedAttempt?.status}
                replyModel={replyModel}
                showSender={!continuesGroup}
                attempts={byEvent.get(event.id) || []}
                headerAttempts={rowHeaderAttempts}
                live={displayLive}
                isLatest={!running && index === rows.length - 1}
                compact={compact}
                {...actions}
                canApproveReview={
                  Boolean(actions.canApproveReview) &&
                  event.id === latestConfirmID
                }
                canAcknowledgeCheckin={
                  Boolean(actions.canAcknowledgeCheckin) &&
                  event.id === latestCheckinID
                }
              />
            </div>
          );
        })}

        {standalone.length > 0 && (
          <div className="mt-3">
            <StandaloneExecution
              attempts={standalone}
              live={displayLive}
              {...actions}
            />
          </div>
        )}
        {running && (
          <div className={runningContinues ? "mt-1.5" : "mt-3"}>
            <ExecutionStatus
              attempts={[running]}
              live={displayLive}
              showSender={!runningContinues}
              {...actions}
            />
          </div>
        )}
        <div
          ref={bottom}
          className={
            bottomInsetPx !== undefined ? "shrink-0" : bottomSpacerClass
          }
          style={
            bottomInsetPx !== undefined
              ? { height: `${Math.max(16, bottomInsetPx)}px` }
              : undefined
          }
        />
      </ChatMessageList>

      <div
        style={
          bottomInsetPx !== undefined
            ? { bottom: `${bottomInsetPx <= 24 ? 16 : Math.max(88, bottomInsetPx + 8)}px` }
            : undefined
        }
        className={`pointer-events-none absolute inset-x-0 z-10 flex justify-center transition-all duration-200 ${
          bottomInsetPx !== undefined
            ? ""
            : compact
              ? "bottom-4"
              : "bottom-24"
        } ${
          showLatest
            ? "opacity-100 translate-y-0"
            : "opacity-0 translate-y-2 pointer-events-none"
        }`}
      >
        <button
          type="button"
          tabIndex={showLatest ? 0 : -1}
          title={t("跳到最新")}
          aria-label={t("跳到最新")}
          onClick={() => {
            scrollToBottom(true);
            stick.current = true;
            setShowLatest(false);
          }}
          className="pointer-events-auto grid size-9 place-items-center rounded-full border border-line bg-surface text-secondary-foreground shadow-md transition hover:bg-canvas active:scale-95"
        >
          <ArrowDown className="size-4" />
        </button>
      </div>
    </div>
  );
}
