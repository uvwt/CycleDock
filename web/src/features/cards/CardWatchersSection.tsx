import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  AlarmClock,
  Check,
  Copy,
  History,
  Radar,
  XCircle,
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import type { CardWatcher } from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import { Badge } from "../../shared/ui/badge";
import { formatRemainingCountdown } from "../settings/trigger-form";

function formatLocalDateTime(value?: string) {
  if (!value) return "";
  return new Date(value).toLocaleString([], {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

const watcherStatusLabel: Record<string, string> = {
  armed: "等待中",
  triggered: "已唤醒",
  expired: "已超时兜底",
  cancelled: "已取消",
};

export function CardWatchersSection({
  cardID,
  onChanged,
}: {
  cardID: string;
  onChanged?: () => void;
}) {
  const { t, i18n } = useTranslation();
  const isEnglish = i18n.resolvedLanguage === "en";
  const [showHistory, setShowHistory] = useState(false);
  const [copiedID, setCopiedID] = useState<string | null>(null);
  const [nowMs, setNowMs] = useState(() => Date.now());

  const query = useQuery({
    queryKey: keys.cardWatchers(cardID),
    queryFn: () => api.cardWatchers(cardID, false),
    enabled: Boolean(cardID),
    staleTime: 10_000,
  });

  const allWatchers = query.data || [];
  const armedWatchers = allWatchers.filter((w) => w.status === "armed");
  const historyWatchers = allWatchers.filter((w) => w.status !== "armed");

  useEffect(() => {
    if (armedWatchers.length === 0) return;
    const timer = window.setInterval(() => setNowMs(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, [armedWatchers.length]);

  const cancelMutation = useMutation({
    mutationFn: (watcher: CardWatcher) =>
      api.cancelCardWatcher(cardID, watcher.id, "manual_cancelled"),
    onSuccess: (updated) => {
      queryClient.setQueryData<CardWatcher[]>(
        keys.cardWatchers(cardID),
        (old) =>
          old
            ? old.map((item) => (item.id === updated.id ? updated : item))
            : old,
      );
      void queryClient.invalidateQueries({ queryKey: ["workspace-watchers"] });
      toast.success(t("已取消该等待项"));
      onChanged?.();
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("取消等待失败"));
    },
  });

  async function copyMatchKey(watcher: CardWatcher) {
    if (!watcher.match_key) return;
    try {
      await navigator.clipboard.writeText(watcher.match_key);
      setCopiedID(watcher.id);
      toast.success(t("已复制 Match Key"));
      window.setTimeout(
        () => setCopiedID((cur) => (cur === watcher.id ? null : cur)),
        1500,
      );
    } catch {
      toast.error(t("复制失败，请手动复制"));
    }
  }

  if (allWatchers.length === 0) return null;

  return (
    <div className="space-y-2 border-t border-line/60 pt-2.5">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="flex items-center gap-1.5 font-medium text-foreground">
          <Radar className="size-3.5 text-amber-500" />
          <span>{t("等待与定时唤醒")}</span>
          {armedWatchers.length > 0 && (
            <Badge
              variant="secondary"
              className="h-4 px-1.5 text-[10px] font-mono"
            >
              {armedWatchers.length}
            </Badge>
          )}
        </span>
        {historyWatchers.length > 0 && (
          <button
            type="button"
            onClick={() => setShowHistory((prev) => !prev)}
            className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground transition-colors cursor-pointer"
          >
            <History className="size-3" />
            <span>
              {showHistory
                ? t("收起历史")
                : t("历史 ({{count}})", { count: historyWatchers.length })}
            </span>
          </button>
        )}
      </div>

      {armedWatchers.length === 0 ? (
        <p className="text-[11px] text-muted-foreground">
          {t("当前没有挂起中的事件等待或定时自唤醒。")}
        </p>
      ) : (
        <div className="space-y-2">
          {armedWatchers.map((watcher) => {
            const isEvent = watcher.kind === "event";
            const countdown = formatRemainingCountdown(
              watcher.expires_at,
              nowMs,
              isEnglish,
            );
            const cancelling =
              cancelMutation.isPending &&
              cancelMutation.variables?.id === watcher.id;
            return (
              <div
                key={watcher.id}
                className="space-y-1.5 rounded-lg border border-amber-500/30 bg-amber-500/[0.06] p-2.5 text-xs"
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-1.5 min-w-0">
                    <span className="inline-flex items-center gap-1 font-medium text-foreground">
                      {isEvent ? (
                        <Radar className="size-3 text-amber-600 dark:text-amber-400 shrink-0" />
                      ) : (
                        <AlarmClock className="size-3 text-sky-600 dark:text-sky-400 shrink-0" />
                      )}
                      {isEvent ? t("事件等待") : t("定时自唤醒")}
                    </span>
                    <Badge
                      variant="outline"
                      className="h-4 px-1.5 text-[10px] font-normal"
                    >
                      {watcher.dispatch_mode === "interrupt"
                        ? t("打断执行")
                        : t("排队执行")}
                    </Badge>
                  </div>
                  <button
                    type="button"
                    disabled={cancelling}
                    onClick={() => cancelMutation.mutate(watcher)}
                    className="inline-flex h-6 shrink-0 items-center gap-1 rounded-md border border-line bg-surface px-2 text-[11px] font-medium text-muted-foreground transition hover:border-destructive/40 hover:bg-destructive/10 hover:text-destructive disabled:opacity-50 cursor-pointer"
                  >
                    <XCircle className="size-3" />
                    <span>{cancelling ? t("取消中…") : t("取消等待")}</span>
                  </button>
                </div>

                {isEvent && watcher.match_key && (
                  <div className="flex items-center justify-between gap-1.5 rounded bg-surface/80 px-2 py-1 border border-line/50">
                    <code className="min-w-0 flex-1 break-all font-mono text-[11px] text-foreground">
                      {watcher.match_key}
                    </code>
                    <button
                      type="button"
                      onClick={() => void copyMatchKey(watcher)}
                      className="inline-flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground cursor-pointer"
                      aria-label={t("复制 Match Key")}
                      title={t("复制 Match Key")}
                    >
                      {copiedID === watcher.id ? (
                        <Check className="size-3 text-emerald-500" />
                      ) : (
                        <Copy className="size-3" />
                      )}
                    </button>
                  </div>
                )}

                <div className="flex flex-wrap items-center justify-between gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground">
                  <span>
                    {isEvent ? t("超时兜底") : t("唤醒时间")}：
                    {formatLocalDateTime(watcher.expires_at)}
                  </span>
                  {countdown && (
                    <span className="font-medium text-amber-700 dark:text-amber-400 tabular-nums">
                      {countdown}
                    </span>
                  )}
                </div>

                {watcher.note && (
                  <p className="text-[11px] leading-relaxed text-secondary-foreground break-words">
                    {watcher.note}
                  </p>
                )}
              </div>
            );
          })}
        </div>
      )}

      {showHistory && historyWatchers.length > 0 && (
        <div className="space-y-1.5 pt-1">
          {historyWatchers.slice(0, 6).map((watcher) => (
            <div
              key={watcher.id}
              className="space-y-1 rounded-lg border border-line/50 bg-surface/50 px-2.5 py-2 text-[11px] text-muted-foreground"
            >
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium text-secondary-foreground">
                  {watcher.kind === "event" ? t("事件等待") : t("定时自唤醒")}
                </span>
                <Badge
                  variant="outline"
                  className="h-4 px-1.5 text-[10px] font-normal"
                >
                  {t(watcherStatusLabel[watcher.status] || watcher.status)}
                </Badge>
              </div>
              {watcher.match_key && (
                <code className="block break-all font-mono text-[10px] text-muted-foreground">
                  {watcher.match_key}
                </code>
              )}
              {watcher.note && (
                <p className="break-words text-muted-foreground">
                  {watcher.note}
                </p>
              )}
              <div className="text-[10px] text-faint">
                {watcher.resolved_at
                  ? `${t("结束于")} ${formatLocalDateTime(watcher.resolved_at)}`
                  : formatLocalDateTime(watcher.expires_at)}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
