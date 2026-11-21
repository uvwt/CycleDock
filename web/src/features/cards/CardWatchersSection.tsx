import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { AlarmClock, History, Radar, XCircle } from "lucide-react";
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
      toast.success(t("已取消等待"));
      onChanged?.();
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("取消等待失败"));
    },
  });

  if (allWatchers.length === 0) return null;

  return (
    <div className="space-y-2 border-t border-line/60 pt-2.5">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="flex items-center gap-1.5 font-medium text-foreground">
          <Radar className="size-3.5 text-amber-500" />
          <span>{t("等待与定时提醒")}</span>
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
          {t("当前没有等待中的事件或定时提醒。")}
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
                title={watcher.match_key || undefined}
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-1.5 min-w-0">
                    <span className="inline-flex items-center gap-1 font-medium text-foreground">
                      {isEvent ? (
                        <Radar className="size-3 text-amber-600 dark:text-amber-400 shrink-0" />
                      ) : (
                        <AlarmClock className="size-3 text-sky-600 dark:text-sky-400 shrink-0" />
                      )}
                      {isEvent ? t("等待外部事件") : t("定时唤醒")}
                    </span>
                  </div>
                  <button
                    type="button"
                    disabled={cancelling}
                    onClick={() => cancelMutation.mutate(watcher)}
                    className="inline-flex h-6 shrink-0 items-center gap-1 rounded-md border border-line bg-surface px-2 text-[11px] font-medium text-muted-foreground transition hover:border-destructive/40 hover:bg-destructive/10 hover:text-destructive disabled:opacity-50 cursor-pointer"
                  >
                    <XCircle className="size-3" />
                    <span>{cancelling ? t("取消中…") : t("取消")}</span>
                  </button>
                </div>

                {watcher.note && (
                  <p className="text-[11px] text-foreground/85 break-words">
                    {watcher.note}
                  </p>
                )}

                <div className="flex flex-wrap items-center justify-between gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground">
                  <span>
                    {isEvent ? t("超时兜底") : t("预计唤醒")}：
                    {formatLocalDateTime(watcher.expires_at)}
                  </span>
                  {countdown && (
                    <span className="font-medium text-amber-700 dark:text-amber-400 tabular-nums">
                      {countdown}
                    </span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}

      {showHistory && historyWatchers.length > 0 && (
        <div className="space-y-1.5 pt-1">
          {historyWatchers.map((watcher) => {
            const isEvent = watcher.kind === "event";
            return (
              <div
                key={watcher.id}
                className="flex items-center justify-between gap-2 rounded-lg border border-line/60 bg-muted/20 px-2.5 py-1.5 text-[11px] text-muted-foreground"
                title={watcher.note || watcher.match_key || undefined}
              >
                <span className="min-w-0 truncate">
                  {isEvent ? t("等待外部事件") : t("定时唤醒")}
                  {watcher.note ? ` · ${watcher.note}` : ""}
                </span>
                <span className="shrink-0 tabular-nums">
                  {t(watcherStatusLabel[watcher.status] || watcher.status)} ·{" "}
                  {formatLocalDateTime(
                    watcher.resolved_at || watcher.expires_at,
                  )}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
