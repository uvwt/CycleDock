import {
  type Ref,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router-dom";
import {
  ArrowDown,
  Bot,
  Check,
  Clock,
  Copy,
  MoreHorizontal,
  Pencil,
  Play,
  Quote,
  Reply,
  ClipboardCheck,
  AlertCircle,
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
  CardKind,
  CommentQuote,
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
import { CheckinTimelineItem } from "./CheckinTimelineItem";
import {
  AttemptMetadata,
  ExecutionStatus,
  OperatingDevice,
  RunDuration,
  StandaloneExecution,
} from "./ExecutionStatus";
import { BubbleRetryMenu } from "./BubbleRetryMenu";
import { RichText } from "./RichText";
import { resolveUnreadDividerPlacement } from "./unreadDivider";
import { ToolApprovalCard } from "./ToolApprovalCard";
import {
  MessageAttachmentGrid,
  MessageImageGallery,
} from "./AttachmentGallery";
import { usePacedTimeline } from "./usePacedTimeline";
import {
  computeSelectionPopoverPosition,
  formatQuotePreview,
  normalizeQuoteText,
  resolveSelectionQuote,
} from "./selectionQuote";
import { cn } from "../../shared/lib/utils";
import {
  TWO_HOURS_MS,
  buildTimelineItems,
  groupExecutionAttempts,
  orderTimelineEvents,
  type ExecutionAttempt,
} from "./execution";

const FIVE_MINUTES_MS = 5 * 60 * 1000;
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
 * 待验收与驳回系统卡片事件的视觉样式配置。
 * 巡检事件已独立由 CheckinTimelineItem 以更轻量的可折叠形态呈现，与正式气泡区分轻重。
 */
function getSystemCardStyle(type: string) {
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

const MAX_QUOTE_JUMP_HISTORY_PAGES = 10;

type ExecutionActions = {
  retryingAttemptID?: string;
  regeneratingEventID?: string;
  onRetryAttempt: (
    attemptID: string,
    preferredModelRouteID?: string | null,
  ) => void;
  onRegenerateMessage?: (
    eventID: string,
    preferredModelRouteID: string | null,
  ) => Promise<void>;
  onOpenTrace?: (attemptID: string) => void;
  onReply?: (quote: QuotedMessage) => void;
  onJumpToEvent?: (eventID: string) => void;
  onEditMessage?: (eventID: string, content: string) => Promise<void>;
  canApproveReview?: boolean;
  approvingReview?: boolean;
  onApproveReview?: () => void;
  canAcknowledgeCheckin?: boolean;
  acknowledgingCheckin?: boolean;
  onAcknowledgeCheckin?: () => void;
  canStartCard?: boolean;
  startingCard?: boolean;
  onStartCard?: () => void;
};

/** 展示已发送消息顶部的结构化引用预览块，点击平滑回跳并高亮原消息。 */
function MessageQuoteBlock({
  quote,
  onJump,
}: {
  quote: CommentQuote;
  onJump?: (eventID: string) => void;
}) {
  const { t } = useTranslation();
  const preview = formatQuotePreview(quote.text, 240);
  if (!preview) return null;
  const sender = quote.sender.trim() || "Cycle";
  const interactive = Boolean(quote.event_id && onJump);
  const content = (
    <>
      <span className="block truncate text-[11px] font-semibold text-primary">
        {sender}
      </span>
      <span className="mt-0.5 line-clamp-2 break-words text-xs leading-snug text-muted-foreground">
        {preview}
      </span>
    </>
  );

  if (!interactive) {
    return (
      <div
        data-quote-ignore="true"
        data-testid="message-quote-block"
        className="mb-2 max-w-full rounded-lg border border-line/60 border-l-3 border-l-primary/70 bg-surface/75 px-2.5 py-1.5 text-left dark:bg-canvas/55"
      >
        {content}
      </div>
    );
  }

  return (
    <button
      type="button"
      data-quote-ignore="true"
      data-testid="message-quote-block"
      data-quote-target-event-id={quote.event_id}
      onClick={(event) => {
        event.stopPropagation();
        onJump?.(quote.event_id);
      }}
      title={t("点击查看原消息")}
      aria-label={t("引用自 {{sender}}，点击查看原消息", { sender })}
      className="mb-2 block w-full max-w-full cursor-pointer rounded-lg border border-line/60 border-l-3 border-l-primary/80 bg-surface/80 px-2.5 py-1.5 text-left transition hover:border-primary/40 hover:bg-surface active:scale-[0.99] dark:bg-canvas/55 dark:hover:bg-canvas/80"
    >
      {content}
    </button>
  );
}

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

function UnreadMessageDivider({
  count,
  compact = false,
  hasTimeSeparatorAbove = false,
  dividerRef,
}: {
  count: number;
  compact?: boolean;
  hasTimeSeparatorAbove?: boolean;
  dividerRef?: Ref<HTMLDivElement>;
}) {
  const { t } = useTranslation();
  const label =
    count === 1
      ? t("1 条未读消息")
      : count > 1
        ? t("{{count}} 条未读消息", { count })
        : t("以下为未读消息");
  return (
    <div
      ref={dividerRef}
      role="separator"
      data-unread-divider
      data-unread-count={count}
      data-testid="unread-message-divider"
      aria-label={label}
      className={`flex items-center gap-3 select-none ${
        hasTimeSeparatorAbove
          ? "pt-1 pb-2.5"
          : compact
            ? "py-2"
            : "py-2.5"
      }`}
    >
      <span
        aria-hidden="true"
        className="h-px flex-1 bg-gradient-to-r from-transparent via-primary/35 to-primary/55 dark:via-sky-400/35 dark:to-sky-400/55"
      />
      <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-primary/25 bg-primary/8 px-3 py-0.5 text-[11px] font-medium tracking-wide text-primary shadow-2xs dark:border-sky-400/30 dark:bg-sky-400/12 dark:text-sky-300">
        <span
          aria-hidden="true"
          className="size-1.5 rounded-full bg-primary dark:bg-sky-400"
        />
        <span>{label}</span>
      </span>
      <span
        aria-hidden="true"
        className="h-px flex-1 bg-gradient-to-l from-transparent via-primary/35 to-primary/55 dark:via-sky-400/35 dark:to-sky-400/55"
      />
    </div>
  );
}

function EventRow({
  event,
  cardID,
  cardKind,
  cardPreferredRouteID,
  attempts,
  headerAttempts,
  toolApprovalAttempt,
  showSender,
  preview,
  previewStatus,
  replyModel,
  live,
  isLatest = false,
  willTruncateFollowing = false,
  cardRunning = false,
  compact = false,
  ...actions
}: {
  event: TimelineEvent;
  cardID?: string;
  cardKind?: CardKind;
  cardPreferredRouteID?: string | null;
  attempts: ExecutionAttempt[];
  headerAttempts: Attempt[];
  toolApprovalAttempt?: Attempt;
  showSender: boolean;
  preview?: LiveReply;
  previewStatus?: string;
  replyModel?: string;
  live: LiveAttempt | null;
  isLatest?: boolean;
  willTruncateFollowing?: boolean;
  cardRunning?: boolean;
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
  const commentAttachments =
    event.type === "comment" ? event.message?.attachments : undefined;
  if (event.type === "tool_approval") {
    return (
      <div id={"event-" + event.id} data-sequence={event.sequence}>
        <ToolApprovalCard
          cardID={cardID}
          approval={event.tool_approval}
          fallbackContent={event.content}
          senderName={senderLabel(event.actor_kind, event.actor_name) || "Cycle"}
          completedAttempt={toolApprovalAttempt}
          onOpenTrace={actions.onOpenTrace}
        />
        {attempts.length > 0 && (
          <div className="mt-3">
            <ExecutionStatus attempts={attempts} live={live} {...actions} />
          </div>
        )}
      </div>
    );
  }
  if (system) {
    if (event.type === "checkin") {
      return (
        <>
          <CheckinTimelineItem
            event={event}
            canAcknowledge={actions.canAcknowledgeCheckin}
            acknowledging={actions.acknowledgingCheckin}
            onAcknowledge={actions.onAcknowledgeCheckin}
          />
          {attempts.length > 0 && (
            <div className="mt-3">
              <ExecutionStatus attempts={attempts} live={live} {...actions} />
            </div>
          )}
        </>
      );
    }
    const cardStyle = getSystemCardStyle(event.type);
    if (cardStyle) {
      const isConfirm = event.type === "confirm";
      const showApproveAction = Boolean(
        isConfirm && actions.canApproveReview && actions.onApproveReview,
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
  const messageQuote = event.message?.quote;
  const displaySender =
    senderLabel(event.actor_kind, event.actor_name) ||
    (human ? t("你") : "Cycle");

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
    const normalized = normalizeQuoteText(rawText);
    if (!normalized || !actions.onReply) return;
    actions.onReply({
      id: event.id,
      sender: displaySender,
      text: normalized,
    });
  }

  const canEdit = Boolean(
    human && event.type === "comment" && actions.onEditMessage,
  );

  // 支持对已落库的 AI 回复气泡（含 finding/result）以及人类消息气泡触发重试/换模型重跑；
  // 排除尚未落库或正在流式输出的预览气泡（无 persisted eventID），以及非对话消息（如卡片间委托 comment）。
  const isStreamingPreview = Boolean(preview && !preview.eventID);
  const canRegenerate = Boolean(
    actions.onRegenerateMessage &&
      !isStreamingPreview &&
      !hasActiveAttempt &&
      (human
        ? event.type === "comment"
        : event.type === "message" ||
          event.type === "finding" ||
          event.type === "result"),
  );
  const isRegeneratingThis = actions.regeneratingEventID === event.id;
  const regenerateDisabled = Boolean(
    (actions.regeneratingEventID && !isRegeneratingThis) ||
      (cardRunning && !isRegeneratingThis),
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
        data-event-id={event.id}
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
                        cardKind={cardKind}
                        cardPreferredRouteID={cardPreferredRouteID}
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
            {messageQuote && (
              <MessageQuoteBlock
                quote={messageQuote}
                onJump={actions.onJumpToEvent}
              />
            )}
            {(event.type === "finding" || event.type === "result") && (
              <p
                data-quote-ignore="true"
                className={`mb-1 text-xs font-semibold ${event.type === "finding" ? "text-sky-700 dark:text-sky-400" : "text-emerald-700 dark:text-emerald-400"}`}
              >
                {t(event.type === "finding" ? "发现" : "结果")}
              </p>
            )}
            {rawText && (
              <div
                data-quote-message-body="true"
                data-quote-event-id={!isStreamingPreview ? event.id : undefined}
                data-quote-sender={displaySender}
              >
                <RichText content={rawText} />
              </div>
            )}
            {commentAttachments && commentAttachments.length > 0 && (
              <div data-quote-ignore="true">
                <MessageAttachmentGrid attachments={commentAttachments} />
              </div>
            )}
            {message?.type === "attachment" && message.url && (
              <a
                data-quote-ignore="true"
                href={message.url}
                target="_blank"
                rel="noopener noreferrer"
                className="mt-2 block break-all text-sm text-sky-700 underline underline-offset-2 dark:text-sky-400"
              >
                {message.alt || message.url}
              </a>
            )}
            {message?.images && message.images.length > 0 && (
              <MessageImageGallery
                images={message.images}
                alt={message.alt}
                hasContentAbove={Boolean(
                  messageQuote ||
                    event.type === "finding" ||
                    event.type === "result" ||
                    rawText ||
                    (commentAttachments && commentAttachments.length > 0) ||
                    (message?.type === "attachment" && message.url),
                )}
              />
            )}
            {showTime && formattedTime && (
              <div data-quote-ignore="true">
                <ChatBubbleTimestamp
                  timestamp={formattedTime}
                  status={human ? "read" : undefined}
                />
              </div>
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
            {canRegenerate && actions.onRegenerateMessage && (
              <BubbleRetryMenu
                replyModel={replyModel}
                cardKind={cardKind}
                cardPreferredRouteID={cardPreferredRouteID}
                align={human ? "end" : "start"}
                willTruncateFollowing={willTruncateFollowing}
                loading={isRegeneratingThis}
                disabled={regenerateDisabled}
                onOpenChange={(open) => {
                  if (open) setTouchActive(true);
                }}
                onConfirmRetry={async (preferredModelRouteID) => {
                  try {
                    await actions.onRegenerateMessage?.(
                      event.id,
                      preferredModelRouteID,
                    );
                    setTouchActive(false);
                  } catch (error) {
                    toast.error(
                      error instanceof Error
                        ? error.message
                        : t("重新回答失败"),
                    );
                  }
                }}
                renderTrigger={({ onClick, disabled, title, icon }) => (
                  <ChatBubbleAction
                    data-bubble-retry-trigger
                    icon={icon}
                    disabled={disabled}
                    title={
                      cardRunning && !isRegeneratingThis
                        ? t("当前正在生成回答，请稍候重试")
                        : title
                    }
                    aria-label={title}
                    onClick={onClick}
                    className="disabled:cursor-not-allowed disabled:opacity-45"
                  />
                )}
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
          <ExecutionStatus
            attempts={attempts}
            live={live}
            cardKind={cardKind}
            cardPreferredRouteID={cardPreferredRouteID}
            {...actions}
          />
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
  onSelectionQuoteChange,
  unreadDividerAfterSequence = null,
  onNearBottomChange,
  ...actions
}: {
  card?: Card;
  events: TimelineEvent[];
  live: LiveAttempt | null;
  cardKind: CardKind;
  pendingTurns: PendingTurn[];
  currentAttempt: Attempt | null;
  attemptHistory: Attempt[];
  bottomSpacerClass?: string;
  bottomInsetPx?: number;
  compact?: boolean;
  hasMoreBefore?: boolean;
  loadingMoreBefore?: boolean;
  onLoadMoreBefore?: () => Promise<boolean> | void;
  /** 移动端触屏选区变化回调；桌面端使用选区浮钮，不通过此回调展示底部动作条。 */
  onSelectionQuoteChange?: (quote: QuotedMessage | null) => void;
  /** 本次会话锁定的未读消息水位线；大于该序号的首条可见消息上方渲染未读分隔符。 */
  unreadDividerAfterSequence?: number | null;
  /** 向外同步消息列表是否接近底部，供翻看历史时判断新到消息是否触发未读分隔符。 */
  onNearBottomChange?: (nearBottom: boolean) => void;
} & ExecutionActions) {
  const { t } = useTranslation();
  const bottom = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const unreadDividerElRef = useRef<HTMLDivElement>(null);
  const initialUnreadAlignedRef = useRef(false);
  const stick = useRef(true);
  const [showLatest, setShowLatest] = useState(false);
  const [desktopSelectionPopover, setDesktopSelectionPopover] = useState<{
    quote: QuotedMessage;
    top: number;
    left: number;
    placement: "top" | "bottom";
  } | null>(null);
  const highlightTimerRef = useRef<number | null>(null);
  const highlightedElRef = useRef<HTMLElement | null>(null);
  const jumpRequestRef = useRef(0);
  const onReplyRef = useRef(actions.onReply);
  const onSelectionQuoteChangeRef = useRef(onSelectionQuoteChange);
  const onLoadMoreBeforeRef = useRef(onLoadMoreBefore);
  const onNearBottomChangeRef = useRef(onNearBottomChange);
  const hasMoreBeforeRef = useRef(hasMoreBefore);
  useEffect(() => {
    onReplyRef.current = actions.onReply;
    onSelectionQuoteChangeRef.current = onSelectionQuoteChange;
    onLoadMoreBeforeRef.current = onLoadMoreBefore;
    onNearBottomChangeRef.current = onNearBottomChange;
    hasMoreBeforeRef.current = hasMoreBefore;
  });

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
  // 实时层已确认本轮结束回复落库时，先按已回复处理，避免数据库 running 再渲染执行中占位。
  const effectiveAttempt =
    live?.replied && currentAttempt?.status === "running"
      ? null
      : currentAttempt;
  const { byEvent, standalone, statusByReply, statusByToolApproval, running } =
    groupExecutionAttempts(orderedEvents, effectiveAttempt, attemptHistory);
  const timelineItems = buildTimelineItems(rows, standalone, executions);
  const unreadDividerPlacement = resolveUnreadDividerPlacement(
    timelineItems,
    unreadDividerAfterSequence,
    hasMoreBefore,
  );

  const groupStarts = new Set<string>();
  const statusByHeader = new Map<string, Attempt[]>();
  let headerID = "";
  let previousEvent: TimelineEvent | undefined;
  for (const item of timelineItems) {
    if (item.kind === "standalone") {
      // 独立系统执行块会打断前后消息的同组连续展示，避免隔着系统提示仍隐藏发送者名称。
      previousEvent = undefined;
      headerID = "";
      continue;
    }
    const event = item.row.event;
    const startsUnreadSection =
      unreadDividerPlacement?.targetItemKey === item.key;
    const interval = previousEvent
      ? Date.parse(event.created_at) - Date.parse(previousEvent.created_at)
      : NaN;
    // 执行信息归到消息组首，避免在连续回复中重复展示发送者；
    // 未读消息分隔符之后的首条未读消息视为新组起点，避免隔着分隔符隐藏发送者头像与名称。
    const continuesGroup =
      !startsUnreadSection &&
      !!previousEvent &&
      !systemTypes.has(previousEvent.type) &&
      !systemTypes.has(event.type) &&
      (!(
        previewKeys.has(event.id) ||
        (live && event.attempt_id === live.attemptID)
      ) ||
        previousEvent.attempt_id === event.attempt_id) &&
      previousEvent.actor_kind === event.actor_kind &&
      senderLabel(previousEvent.actor_kind, previousEvent.actor_name) ===
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
    previousEvent = event;
  }
  const lastTimelineItem = timelineItems.at(-1);
  const lastEvent =
    lastTimelineItem?.kind === "event"
      ? lastTimelineItem.row.event
      : undefined;
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
    standalone.map((item) => `${item.value.id}:${item.value.status}`).join(","),
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

  // 首次进入且未读消息超出单屏高度时，将视口对齐到未读分隔符上方一点，方便从第一条未读顺着阅读；
  // 若未读消息较少（吸底时分隔符仍在视口内），则维持默认吸底，不破坏既有体验。
  const unreadDividerTargetKey = unreadDividerPlacement?.targetItemKey ?? null;
  useEffect(() => {
    if (!unreadDividerTargetKey || initialUnreadAlignedRef.current) return;
    const raf = window.requestAnimationFrame(() => {
      const element = list.current;
      const dividerEl = unreadDividerElRef.current;
      if (!element || !dividerEl) return;
      initialUnreadAlignedRef.current = true;
      const containerRect = element.getBoundingClientRect();
      const dividerRect = dividerEl.getBoundingClientRect();
      const dividerTop = dividerRect.top - containerRect.top + element.scrollTop;
      const distanceFromDividerToBottom = element.scrollHeight - dividerTop;
      if (distanceFromDividerToBottom > element.clientHeight - 40) {
        element.scrollTop = Math.max(0, dividerTop - 56);
        stick.current = false;
        setShowLatest(true);
        onNearBottomChangeRef.current?.(false);
      }
    });
    return () => window.cancelAnimationFrame(raf);
  }, [unreadDividerTargetKey]);

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
  }, [sortedEvents.length, standalone.length]);

  function handleLoadMoreBefore() {
    if (!hasMoreBefore || loadingMoreBefore || !onLoadMoreBefore) return;
    if (list.current) {
      prevScrollHeightRef.current = list.current.scrollHeight;
    }
    void onLoadMoreBefore();
  }

  /**
   * 判定当前交互是否属于触屏/移动端模式：
   * 优先参考 pointer: coarse / hover: none 媒体查询，避免在触屏设备上弹出浮钮与原生复制菜单打架。
   */
  const isTouchSelectionMode = useCallback(() => {
    if (typeof window === "undefined") return false;
    if (window.matchMedia?.("(pointer: coarse), (hover: none)").matches) {
      return true;
    }
    return window.innerWidth < 640 && navigator.maxTouchPoints > 0;
  }, []);

  const clearSelectionQuoteUI = useCallback(() => {
    setDesktopSelectionPopover(null);
    onSelectionQuoteChangeRef.current?.(null);
  }, []);

  const syncSelectionQuote = useCallback(() => {
    if (!onReplyRef.current || !list.current || typeof window === "undefined") {
      clearSelectionQuoteUI();
      return;
    }
    const selection = window.getSelection();
    if (!selection || selection.isCollapsed || selection.rangeCount === 0) {
      clearSelectionQuoteUI();
      return;
    }
    const range = selection.getRangeAt(0);
    const candidate = resolveSelectionQuote(
      {
        startContainer: range.startContainer,
        endContainer: range.endContainer,
        text: selection.toString(),
      },
      list.current,
    );
    if (!candidate) {
      clearSelectionQuoteUI();
      return;
    }

    const quote: QuotedMessage = {
      id: candidate.eventId,
      sender: candidate.sender,
      text: candidate.text,
    };

    if (isTouchSelectionMode()) {
      setDesktopSelectionPopover(null);
      onSelectionQuoteChangeRef.current?.(quote);
      return;
    }

    const rect = range.getBoundingClientRect();
    const fallbackRect =
      rect.width === 0 && rect.height === 0
        ? range.getClientRects()[0]
        : rect;
    if (!fallbackRect || (fallbackRect.width === 0 && fallbackRect.height === 0)) {
      clearSelectionQuoteUI();
      return;
    }

    const pos = computeSelectionPopoverPosition(
      {
        top: fallbackRect.top,
        left: fallbackRect.left,
        width: fallbackRect.width,
        height: fallbackRect.height,
      },
      { width: window.innerWidth, height: window.innerHeight },
    );
    onSelectionQuoteChangeRef.current?.(null);
    setDesktopSelectionPopover({
      quote,
      top: pos.top,
      left: pos.left,
      placement: pos.placement,
    });
  }, [clearSelectionQuoteUI, isTouchSelectionMode]);

  useEffect(() => {
    if (typeof document === "undefined") return;
    let rafId: number | null = null;
    const scheduleSync = () => {
      if (rafId !== null) window.cancelAnimationFrame(rafId);
      rafId = window.requestAnimationFrame(() => {
        rafId = null;
        syncSelectionQuote();
      });
    };
    const handlePointerUp = () => {
      scheduleSync();
    };
    const handleKeyUp = () => {
      scheduleSync();
    };
    const handleResize = () => {
      scheduleSync();
    };

    document.addEventListener("selectionchange", scheduleSync);
    document.addEventListener("mouseup", handlePointerUp);
    document.addEventListener("keyup", handleKeyUp);
    window.addEventListener("resize", handleResize);
    return () => {
      if (rafId !== null) window.cancelAnimationFrame(rafId);
      document.removeEventListener("selectionchange", scheduleSync);
      document.removeEventListener("mouseup", handlePointerUp);
      document.removeEventListener("keyup", handleKeyUp);
      window.removeEventListener("resize", handleResize);
      onSelectionQuoteChangeRef.current?.(null);
    };
  }, [syncSelectionQuote]);

  useEffect(() => {
    return () => {
      if (highlightTimerRef.current !== null) {
        window.clearTimeout(highlightTimerRef.current);
      }
    };
  }, []);

  const flashTargetMessage = useCallback((target: HTMLElement) => {
    if (highlightedElRef.current) {
      highlightedElRef.current.classList.remove("quote-jump-highlight");
    }
    if (highlightTimerRef.current !== null) {
      window.clearTimeout(highlightTimerRef.current);
    }
    target.classList.remove("quote-jump-highlight");
    // 强制重排以支持连续多次点击同一引用时重新触发闪烁动画。
    void target.offsetWidth;
    target.classList.add("quote-jump-highlight");
    highlightedElRef.current = target;
    highlightTimerRef.current = window.setTimeout(() => {
      target.classList.remove("quote-jump-highlight");
      if (highlightedElRef.current === target) {
        highlightedElRef.current = null;
      }
      highlightTimerRef.current = null;
    }, 1700);
  }, []);

  const findEventElement = useCallback((eventID: string): HTMLElement | null => {
    const container = list.current;
    if (!container || !eventID) return null;
    const byId = document.getElementById(`event-${eventID}`);
    if (byId && container.contains(byId)) return byId;
    const candidates = container.querySelectorAll<HTMLElement>("[data-event-id]");
    for (const el of candidates) {
      if (el.getAttribute("data-event-id") === eventID) return el;
    }
    return null;
  }, []);

  const waitNextFrame = () =>
    new Promise<void>((resolve) => {
      window.requestAnimationFrame(() => {
        window.requestAnimationFrame(() => resolve());
      });
    });

  const handleJumpToEvent = useCallback(
    async (eventID: string) => {
      if (!eventID) return;
      const requestID = ++jumpRequestRef.current;

      const focusTarget = (el: HTMLElement) => {
        stick.current = false;
        el.scrollIntoView({ behavior: "smooth", block: "center" });
        flashTargetMessage(el);
      };

      const existing = findEventElement(eventID);
      if (existing) {
        focusTarget(existing);
        return;
      }

      if (!onLoadMoreBeforeRef.current || !hasMoreBeforeRef.current) {
        toast.info(t("未找到原消息，可能已被删除或超出当前会话记录"));
        return;
      }

      for (let page = 0; page < MAX_QUOTE_JUMP_HISTORY_PAGES; page++) {
        if (jumpRequestRef.current !== requestID) return;
        if (!hasMoreBeforeRef.current || !onLoadMoreBeforeRef.current) break;
        if (list.current) {
          prevScrollHeightRef.current = list.current.scrollHeight;
        }
        const loaded = await onLoadMoreBeforeRef.current();
        if (jumpRequestRef.current !== requestID) return;
        await waitNextFrame();
        const found = findEventElement(eventID);
        if (found) {
          focusTarget(found);
          return;
        }
        if (loaded === false) {
          break;
        }
      }

      if (jumpRequestRef.current === requestID) {
        toast.info(t("未找到原消息，可能位于更早的历史记录或已被移除"));
      }
    },
    [findEventElement, flashTargetMessage, t],
  );

  function onScroll() {
    const element = list.current;
    if (!element) return;
    if (desktopSelectionPopover) {
      setDesktopSelectionPopover(null);
    }
    if (element.scrollTop < 60 && hasMoreBefore && !loadingMoreBefore && onLoadMoreBefore) {
      handleLoadMoreBefore();
    }
    const distanceToBottom =
      element.scrollHeight - element.scrollTop - element.clientHeight;
    const nearBottom = distanceToBottom < 100;
    stick.current = nearBottom;
    onNearBottomChangeRef.current?.(nearBottom);
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

                    {card.attachments && card.attachments.length > 0 && (
                      <div
                        className="border-t border-line/50 px-5 py-4 sm:px-6"
                        data-testid="card-attachments-empty-section"
                      >
                        <div className="text-[11px] font-medium text-muted-foreground">
                          {t("附带附件（{{count}}）", {
                            count: card.attachments.length,
                          })}
                        </div>
                        <MessageAttachmentGrid attachments={card.attachments} />
                      </div>
                    )}

                    <div className="flex flex-col gap-3 border-t border-line/50 bg-muted/15 px-5 py-3.5 text-xs text-muted-foreground sm:flex-row sm:items-center sm:justify-between sm:px-6">
                      <div className="flex items-center gap-2 min-w-0">
                        <Bot className="size-4 shrink-0 text-faint" />
                        <span>
                          {actions.canStartCard
                            ? cardKind === "ongoing"
                              ? t("当前定时任务尚未开始执行，点击即可立即启动首轮执行，或直接发送补充说明。")
                              : t("当前待办尚未开始执行，点击即可由 Cycle 开始处理，或直接发送补充说明。")
                            : t("你的消息、Agent 结果和任务状态都会出现在这里。")}
                        </span>
                      </div>
                      {actions.canStartCard && actions.onStartCard && (
                        <button
                          type="button"
                          disabled={actions.startingCard}
                          onClick={actions.onStartCard}
                          className="inline-flex h-8 shrink-0 items-center justify-center gap-1.5 rounded-lg bg-primary px-3.5 text-xs font-medium text-primary-foreground shadow-xs transition hover:bg-primary/90 disabled:pointer-events-none disabled:opacity-60"
                        >
                          {actions.startingCard ? (
                            <Loader2 className="size-3.5 animate-spin" />
                          ) : (
                            <Play className="size-3.5 fill-current" />
                          )}
                          <span>
                            {actions.startingCard
                              ? t("正在启动...")
                              : t("开始执行")}
                          </span>
                        </button>
                      )}
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
                      {actions.canStartCard
                        ? cardKind === "ongoing"
                          ? t("当前定时任务尚未开始执行，点击即可立即启动首轮执行，或直接发送补充说明。")
                          : t("当前待办尚未开始执行，点击即可由 Cycle 开始处理，或直接发送补充说明。")
                        : t("你的消息、Agent 结果和任务状态都会出现在这里。")}
                    </p>
                    {actions.canStartCard && actions.onStartCard && (
                      <div className="mt-4 flex justify-center">
                        <button
                          type="button"
                          disabled={actions.startingCard}
                          onClick={actions.onStartCard}
                          className="inline-flex h-9 items-center justify-center gap-1.5 rounded-lg bg-primary px-4 text-xs font-medium text-primary-foreground shadow-xs transition hover:bg-primary/90 disabled:pointer-events-none disabled:opacity-60"
                        >
                          {actions.startingCard ? (
                            <Loader2 className="size-3.5 animate-spin" />
                          ) : (
                            <Play className="size-3.5 fill-current" />
                          )}
                          <span>
                            {actions.startingCard
                              ? t("正在启动...")
                              : t("开始执行")}
                          </span>
                        </button>
                      </div>
                    )}
                  </div>
                )}
              </div>
            </div>
          )}

        {timelineItems.map((item, index) => {
          const previousItem = timelineItems[index - 1];
          const prevEndTime =
            previousItem?.kind === "event"
              ? previousItem.displayTime
              : previousItem?.kind === "standalone"
                ? previousItem.lastTime
                : null;
          const currentStartTime =
            item.kind === "event" ? item.displayTime : item.firstTime;
          const showTime =
            prevEndTime !== null &&
            currentStartTime !== null &&
            currentStartTime - prevEndTime > TWO_HOURS_MS;

          if (item.kind === "standalone") {
            return (
              <div key={item.key} className={index === 0 ? "" : "mt-3"}>
                {showTime && <TimeSeparator value={item.displayTimestamp} />}
                <StandaloneExecution
                  attempts={item.attempts}
                  live={displayLive}
                  cardKind={cardKind}
                  cardPreferredRouteID={card?.preferred_model_route_id}
                  {...actions}
                />
              </div>
            );
          }

          const { event, preview, key } = item.row;
          const showUnreadDivider =
            unreadDividerPlacement?.targetItemKey === item.key;
          const previousEventRow =
            previousItem?.kind === "event" ? previousItem.row.event : undefined;
          const continuesGroup = !groupStarts.has(event.id);
          const isConsecutiveStatusChange =
            previousEventRow?.type === "status_change" &&
            event.type === "status_change";
          const matchedAttempt = executions.find(
            (attempt) => attempt.id === event.attempt_id,
          );
          const rowHeaderAttempts = statusByHeader.get(event.id) || [];
          const replyModel =
            event.message?.model ||
            matchedAttempt?.model ||
            rowHeaderAttempts.find((attempt) => attempt.model)?.model;
          // 若当前气泡之后还存在后续对话轮次（另一轮人类消息、不同 attempt 的回复、或排队中的新 Turn），
          // 重新生成时会截断后续历史，因此需要弹出轻量二次确认。
          const hasFollowingDialogue =
            pendingTurns.length > 0 ||
            timelineItems.slice(index + 1).some((later) => {
              if (later.kind !== "event") return true;
              const laterEvent = later.row.event;
              if (systemTypes.has(laterEvent.type)) return false;
              // 同一 AI 回复轮次拆分成多个气泡（同一 attempt_id）时，点击其中任一气泡视作重跑最新这一轮，不弹确认
              if (
                event.actor_kind !== "human" &&
                event.attempt_id &&
                laterEvent.attempt_id === event.attempt_id
              ) {
                return false;
              }
              // 点击最新一条人类消息且后面仅有它自己的那轮回复（无更后的人类消息）也不需要打断确认
              if (
                event.actor_kind === "human" &&
                laterEvent.actor_kind !== "human"
              ) {
                const laterHuman = timelineItems
                  .slice(index + 1)
                  .some(
                    (afterItem) =>
                      afterItem.kind === "event" &&
                      afterItem.row.event.actor_kind === "human",
                  );
                return laterHuman;
              }
              return true;
            });
          return (
            <div
              key={key}
              className={`${!preview && !previewKeys.has(event.id) && (historyIDs ? !historyIDs.has(event.id) : event.sequence > historyThrough) ? "message-enter" : ""} ${index === 0 ? "" : isConsecutiveStatusChange ? "mt-0.5" : continuesGroup ? "mt-2.5" : "mt-4"}`}
            >
              {showTime && <TimeSeparator value={item.displayTimestamp} />}
              {showUnreadDivider && unreadDividerPlacement && (
                <UnreadMessageDivider
                  count={unreadDividerPlacement.unreadCount}
                  compact={compact}
                  hasTimeSeparatorAbove={showTime}
                  dividerRef={unreadDividerElRef}
                />
              )}
              <EventRow
                event={event}
                cardID={card?.id}
                cardKind={cardKind}
                cardPreferredRouteID={card?.preferred_model_route_id}
                preview={preview}
                previewStatus={matchedAttempt?.status}
                replyModel={replyModel}
                showSender={!continuesGroup}
                attempts={byEvent.get(event.id) || []}
                headerAttempts={rowHeaderAttempts}
                toolApprovalAttempt={statusByToolApproval.get(event.id)}
                live={displayLive}
                isLatest={!running && index === timelineItems.length - 1}
                willTruncateFollowing={hasFollowingDialogue}
                cardRunning={Boolean(running)}
                compact={compact}
                {...actions}
                onJumpToEvent={(targetEventID) => {
                  void handleJumpToEvent(targetEventID);
                }}
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
        {running && (
          <div className={runningContinues ? "mt-1.5" : "mt-3"}>
            <ExecutionStatus
              attempts={[running]}
              live={displayLive}
              showSender={!runningContinues}
              cardKind={cardKind}
              cardPreferredRouteID={card?.preferred_model_route_id}
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
            onNearBottomChangeRef.current?.(true);
            setShowLatest(false);
          }}
          className="pointer-events-auto grid size-9 place-items-center rounded-full border border-line bg-surface text-secondary-foreground shadow-md transition hover:bg-canvas active:scale-95"
        >
          <ArrowDown className="size-4" />
        </button>
      </div>

      {desktopSelectionPopover &&
        typeof document !== "undefined" &&
        createPortal(
          <div
            data-testid="desktop-selection-quote-popover"
            data-placement={desktopSelectionPopover.placement}
            style={{
              position: "fixed",
              top: `${desktopSelectionPopover.top}px`,
              left: `${desktopSelectionPopover.left}px`,
              transform: "translateX(-50%)",
            }}
            className="z-50"
          >
            <button
              type="button"
              onMouseDown={(event) => {
                // 拦截 mousedown，避免点击瞬间浏览器先清空选区导致浮钮卸载、无法触发引用。
                event.preventDefault();
              }}
              onClick={() => {
                const selectedQuote = desktopSelectionPopover.quote;
                onReplyRef.current?.(selectedQuote);
                window.getSelection()?.removeAllRanges();
                setDesktopSelectionPopover(null);
              }}
              className="inline-flex cursor-pointer items-center gap-1.5 rounded-full border border-line-strong/80 bg-surface px-3 py-1.5 text-xs font-medium text-foreground shadow-md backdrop-blur-sm transition hover:border-primary/60 hover:text-primary active:scale-95"
            >
              <Quote className="size-3.5 text-primary" />
              <span>{t("引用")}</span>
            </button>
          </div>,
          document.body,
        )}
    </div>
  );
}
