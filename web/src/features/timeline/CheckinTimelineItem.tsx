import { useState } from "react";
import { Activity, Check, ChevronDown, ChevronUp } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TimelineEvent } from "../../shared/api/types";
import { cn } from "../../shared/lib/utils";
import { OngoingHealthBadge } from "../../shared/ui/OngoingHealthBadge";
import { RichText } from "./RichText";

type CheckinPalette = {
  expandedCard: string;
  collapsedAbnormal: string;
  iconWrap: string;
  title: string;
};

function getCheckinPalette(status?: string): CheckinPalette {
  if (status === "healthy") {
    return {
      expandedCard:
        "border-emerald-500/25 bg-emerald-500/[0.03] dark:border-emerald-500/20 dark:bg-emerald-500/[0.06]",
      collapsedAbnormal: "",
      iconWrap:
        "bg-emerald-500/15 text-emerald-600 dark:bg-emerald-500/25 dark:text-emerald-400",
      title: "text-emerald-700 dark:text-emerald-300",
    };
  }
  if (status === "warning") {
    return {
      expandedCard:
        "border-amber-500/35 bg-amber-500/[0.04] dark:border-amber-500/30 dark:bg-amber-500/[0.08]",
      collapsedAbnormal:
        "border-amber-500/35 bg-amber-500/[0.05] text-foreground/85 hover:border-amber-500/50 hover:bg-amber-500/[0.09] dark:border-amber-500/30 dark:bg-amber-500/[0.08] dark:hover:bg-amber-500/[0.13]",
      iconWrap:
        "bg-amber-500/15 text-amber-600 dark:bg-amber-500/25 dark:text-amber-400",
      title: "text-amber-700 dark:text-amber-300",
    };
  }
  if (status === "critical") {
    return {
      expandedCard:
        "border-rose-500/35 bg-rose-500/[0.04] dark:border-rose-500/30 dark:bg-rose-500/[0.08]",
      collapsedAbnormal:
        "border-rose-500/35 bg-rose-500/[0.05] text-foreground/85 hover:border-rose-500/50 hover:bg-rose-500/[0.09] dark:border-rose-500/30 dark:bg-rose-500/[0.08] dark:hover:bg-rose-500/[0.13]",
      iconWrap:
        "bg-rose-500/15 text-rose-600 dark:bg-rose-500/25 dark:text-rose-400",
      title: "text-rose-700 dark:text-rose-300",
    };
  }
  return {
    expandedCard:
      "border-sky-500/25 bg-sky-500/[0.03] dark:border-sky-500/20 dark:bg-sky-500/[0.06]",
    collapsedAbnormal: "",
    iconWrap: "bg-sky-500/15 text-sky-600 dark:bg-sky-500/25 dark:text-sky-400",
    title: "text-sky-700 dark:text-sky-300",
  };
}

/** 将巡检正文整理为适合折叠态单行预览的纯文本摘要。 */
function formatCheckinSummary(content?: string): string {
  if (!content) return "";
  return content
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/^\s{0,3}(?:#{1,6}\s+|[-*+]\s+|\d+\.\s+)/gm, "")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/[`*_~]+/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

/**
 * 定时巡检时间线条目：与正式模型消息气泡拉开视觉轻重。
 * - 默认折叠为紧凑单行状态锚点（正常、历史巡检、已确认异常）；
 * - 仅当前最新且未确认的需留意/异常巡检默认展开，确认后自动收起；
 * - 任意巡检均可随时点击展开或收起查看完整内容。
 */
export function CheckinTimelineItem({
  event,
  canAcknowledge = false,
  acknowledging = false,
  onAcknowledge,
}: {
  event: TimelineEvent;
  canAcknowledge?: boolean;
  acknowledging?: boolean;
  onAcknowledge?: () => void;
}) {
  const { t } = useTranslation();
  const isAbnormal =
    event.checkin_status === "warning" || event.checkin_status === "critical";
  const isUnacknowledgedAbnormal = Boolean(isAbnormal && canAcknowledge);
  const showAcknowledgeAction = Boolean(
    isUnacknowledgedAbnormal && onAcknowledge,
  );

  // 仅在当前未确认状态不变时保留用户的手动展开/收起覆写；
  // 一旦状态变化（例如点击“已确认”后），当帧自动回到默认策略，避免额外 effect 级联渲染。
  const [expandedOverride, setExpandedOverride] = useState<{
    forUnacknowledged: boolean;
    expanded: boolean;
  } | null>(null);

  const expanded =
    expandedOverride &&
    expandedOverride.forUnacknowledged === isUnacknowledgedAbnormal
      ? expandedOverride.expanded
      : isUnacknowledgedAbnormal;
  const toggleExpanded = () =>
    setExpandedOverride({
      forUnacknowledged: isUnacknowledgedAbnormal,
      expanded: !expanded,
    });

  const palette = getCheckinPalette(event.checkin_status);
  const summary = formatCheckinSummary(event.content);
  const formattedTime = event.created_at
    ? new Date(event.created_at).toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
      })
    : "";

  if (!expanded) {
    return (
      <div
        id={"event-" + event.id}
        data-sequence={event.sequence}
        data-checkin-event="true"
        data-checkin-status={event.checkin_status || "unknown"}
        data-checkin-expanded="false"
        className="flex justify-start"
      >
        <button
          type="button"
          data-testid="checkin-toggle"
          aria-expanded={false}
          aria-label={t("展开巡检详情")}
          onClick={toggleExpanded}
          className={cn(
            "group inline-flex min-w-0 max-w-[94%] cursor-pointer items-center gap-2 rounded-xl border px-3 py-1.5 text-left transition-colors sm:max-w-[85%]",
            isUnacknowledgedAbnormal && palette.collapsedAbnormal
              ? palette.collapsedAbnormal
              : "border-line/60 bg-muted/25 text-muted-foreground hover:border-line hover:bg-muted/45 dark:bg-muted/15 dark:hover:bg-muted/30",
          )}
        >
          <span
            className={cn(
              "grid size-4 shrink-0 place-items-center rounded-full",
              palette.iconWrap,
            )}
          >
            <Activity className="size-2.5" />
          </span>
          <span className="shrink-0 text-[11px] font-medium text-muted-foreground">
            {t("巡检")}
          </span>
          {event.checkin_status && (
            <OngoingHealthBadge
              health={event.checkin_status}
              variant="pill"
              size="xs"
              className="shrink-0"
            />
          )}
          <span
            aria-hidden
            className="shrink-0 text-[11px] text-muted-foreground/35"
          >
            ·
          </span>
          <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground group-hover:text-foreground/85">
            {summary || t("已记录")}
          </span>
          {formattedTime && (
            <span className="ml-auto shrink-0 pl-1 text-[11px] tabular-nums text-faint">
              {formattedTime}
            </span>
          )}
          <ChevronDown className="size-3.5 shrink-0 text-muted-foreground/60 transition-colors group-hover:text-foreground/75" />
        </button>
      </div>
    );
  }

  return (
    <div
      id={"event-" + event.id}
      data-sequence={event.sequence}
      data-checkin-event="true"
      data-checkin-status={event.checkin_status || "unknown"}
      data-checkin-expanded="true"
      className="flex justify-start"
    >
      <div
        className={cn(
          "w-fit min-w-[240px] max-w-[94%] overflow-hidden rounded-xl border transition-colors sm:max-w-[85%]",
          palette.expandedCard,
        )}
      >
        <button
          type="button"
          data-testid="checkin-toggle"
          aria-expanded={true}
          aria-label={t("收起巡检详情")}
          onClick={toggleExpanded}
          className="flex w-full cursor-pointer items-center gap-2 px-3.5 py-2 text-left transition-colors hover:bg-black/[0.02] dark:hover:bg-white/[0.02]"
        >
          <span
            className={cn(
              "grid size-4.5 shrink-0 place-items-center rounded-full",
              palette.iconWrap,
            )}
          >
            <Activity className="size-2.5" />
          </span>
          <span
            className={cn(
              "shrink-0 text-xs font-semibold tracking-wide",
              palette.title,
            )}
          >
            {t("巡检")}
          </span>
          {event.checkin_status && (
            <OngoingHealthBadge
              health={event.checkin_status}
              variant="pill"
              size="xs"
              className="shrink-0"
            />
          )}
          {formattedTime && (
            <span className="ml-auto shrink-0 pl-3 text-[11px] tabular-nums text-faint">
              {formattedTime}
            </span>
          )}
          <ChevronUp className="size-3.5 shrink-0 text-muted-foreground/70" />
        </button>

        {event.content && (
          <div className="border-t border-line/50 px-3.5 py-2.5 text-[13px]">
            <RichText content={event.content} />
          </div>
        )}

        {showAcknowledgeAction && (
          <div className="flex items-center justify-end border-t border-line/50 px-3.5 py-2">
            <button
              type="button"
              disabled={acknowledging}
              onClick={onAcknowledge}
              className="inline-flex cursor-pointer items-center gap-1.5 rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white shadow-2xs transition hover:bg-emerald-500 active:scale-95 disabled:cursor-not-allowed disabled:opacity-50"
            >
              <Check className="size-3.5" />
              <span>{t("已确认")}</span>
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
