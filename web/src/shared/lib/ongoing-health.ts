export type OngoingHealthKind = "healthy" | "warning" | "critical" | "unknown";

export function normalizeOngoingHealth(health?: string): OngoingHealthKind {
  if (health === "healthy" || health === "warning" || health === "critical") {
    return health;
  }
  return "unknown";
}

const healthMetaMap: Record<
  OngoingHealthKind,
  {
    labelKey: string;
    dotClassName: string;
    badgeClassName: string;
    textClassName: string;
  }
> = {
  healthy: {
    labelKey: "正常",
    dotClassName: "bg-emerald-500",
    badgeClassName:
      "border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/15 dark:text-emerald-300",
    textClassName: "text-emerald-700 dark:text-emerald-400",
  },
  warning: {
    labelKey: "需留意",
    dotClassName: "bg-amber-500",
    badgeClassName:
      "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:border-amber-500/30 dark:bg-amber-500/15 dark:text-amber-300",
    textClassName: "text-amber-700 dark:text-amber-400",
  },
  critical: {
    labelKey: "异常",
    dotClassName: "bg-rose-500",
    badgeClassName:
      "border-rose-500/30 bg-rose-500/10 text-rose-700 dark:border-rose-500/30 dark:bg-rose-500/15 dark:text-rose-300",
    textClassName: "text-rose-700 dark:text-rose-400",
  },
  unknown: {
    labelKey: "待首次巡检",
    dotClassName: "bg-slate-400 dark:bg-slate-500",
    badgeClassName:
      "border-line bg-muted/60 text-muted-foreground dark:bg-muted/40",
    textClassName: "text-muted-foreground",
  },
};

export function getOngoingHealthInfo(health?: string) {
  const kind = normalizeOngoingHealth(health);
  return {
    kind,
    ...healthMetaMap[kind],
  };
}

const attemptStatusLabels: Record<string, string> = {
  queued: "排队中",
  running: "运行中",
  completed: "已完成",
  failed: "执行失败",
  superseded: "已替代",
  expired: "已超时",
  cancelled: "已取消",
};

export function getAttemptStatusLabelKey(status?: string): string {
  if (!status) return "";
  return attemptStatusLabels[status] || status;
}
