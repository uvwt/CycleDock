import { useMutation, useQuery } from "@tanstack/react-query";
import {
  Loader2,
  Monitor,
  Plug,
  ShieldCheck,
  Trash2,
  Wrench,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import type { CardToolWhitelist } from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import { Badge } from "../../shared/ui/badge";

function formatShortDateTime(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString([], {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatFullDateTime(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString();
}

function scopeMeta(scopeType: string) {
  switch (scopeType) {
    case "device":
      return {
        labelKey: "设备",
        Icon: Monitor,
        badgeClass:
          "border-sky-500/25 bg-sky-500/10 text-sky-700 dark:text-sky-300",
      };
    case "mcp":
      return {
        labelKey: "MCP",
        Icon: Plug,
        badgeClass:
          "border-violet-500/25 bg-violet-500/10 text-violet-700 dark:text-violet-300",
      };
    default:
      return {
        labelKey: "工具",
        Icon: Wrench,
        badgeClass:
          "border-amber-500/25 bg-amber-500/10 text-amber-700 dark:text-amber-300",
      };
  }
}

export function CardToolWhitelistsSection({
  cardID,
  variant = "section",
  onChanged,
}: {
  cardID: string;
  variant?: "section" | "compact";
  onChanged?: () => void;
}) {
  const { t } = useTranslation();

  const query = useQuery({
    queryKey: keys.cardToolWhitelists(cardID),
    queryFn: () => api.cardToolWhitelists(cardID),
    enabled: Boolean(cardID),
    staleTime: 10_000,
  });

  const whitelists = query.data || [];

  const revokeMutation = useMutation({
    mutationFn: (item: CardToolWhitelist) =>
      api.deleteCardToolWhitelist(cardID, item.id),
    onSuccess: (_res, item) => {
      queryClient.setQueryData<CardToolWhitelist[]>(
        keys.cardToolWhitelists(cardID),
        (old) => (old ? old.filter((entry) => entry.id !== item.id) : old),
      );
      void queryClient.invalidateQueries({
        queryKey: keys.cardToolWhitelists(cardID),
      });
      toast.success(t("已撤销临时放行，下次操作将重新审批"));
      onChanged?.();
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("撤销临时白名单失败"));
    },
  });

  const clearMutation = useMutation({
    mutationFn: () => api.clearCardToolWhitelists(cardID),
    onSuccess: () => {
      queryClient.setQueryData<CardToolWhitelist[]>(
        keys.cardToolWhitelists(cardID),
        [],
      );
      void queryClient.invalidateQueries({
        queryKey: keys.cardToolWhitelists(cardID),
      });
      toast.success(t("已清空本卡片临时白名单"));
      onChanged?.();
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("清空临时白名单失败"));
    },
  });

  if (whitelists.length === 0) return null;

  const isCompact = variant === "compact";

  return (
    <div
      data-testid="card-tool-whitelists-section"
      className={
        isCompact
          ? "space-y-2 border-b border-line/70 bg-surface/95 px-3 py-2.5 backdrop-blur-md"
          : "space-y-2 border-t border-line/60 pt-2.5"
      }
    >
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="flex min-w-0 items-center gap-1.5 font-medium text-foreground">
          <ShieldCheck className="size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
          <span className="truncate">{t("临时免审白名单")}</span>
          <Badge
            variant="secondary"
            className="h-4 px-1.5 font-mono text-[10px]"
          >
            {whitelists.length}
          </Badge>
        </span>
        {whitelists.length >= 2 && (
          <button
            type="button"
            data-testid="tool-whitelist-clear-btn"
            disabled={clearMutation.isPending || revokeMutation.isPending}
            onClick={() => clearMutation.mutate()}
            className="inline-flex shrink-0 cursor-pointer items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive disabled:opacity-50"
          >
            {clearMutation.isPending ? (
              <Loader2 className="size-3 animate-spin" />
            ) : (
              <Trash2 className="size-3" />
            )}
            <span>{t("全部撤销")}</span>
          </button>
        )}
      </div>

      <p className="text-[11px] leading-relaxed text-muted-foreground">
        {t("仅对本卡片生效，撤销后下次受保护操作将重新触发审批。")}
      </p>

      <div
        className={
          isCompact
            ? "max-h-40 space-y-1.5 overflow-y-auto pr-0.5"
            : "space-y-1.5"
        }
      >
        {whitelists.map((item) => {
          const meta = scopeMeta(item.scope_type);
          const ScopeIcon = meta.Icon;
          const displayName =
            item.scope_label?.trim() ||
            item.scope_target ||
            item.tool_name ||
            "-";
          const secondaryTarget =
            item.scope_label?.trim() &&
            item.scope_target &&
            item.scope_label.trim() !== item.scope_target
              ? item.scope_target
              : "";
          const addedAtShort = formatShortDateTime(item.created_at);
          const addedAtFull = formatFullDateTime(item.created_at);
          const isRevoking =
            clearMutation.isPending ||
            (revokeMutation.isPending &&
              revokeMutation.variables?.id === item.id);

          return (
            <div
              key={item.id}
              data-testid="tool-whitelist-item"
              data-scope-type={item.scope_type}
              className="flex items-center justify-between gap-2 rounded-lg border border-emerald-500/25 bg-emerald-500/[0.05] px-2.5 py-2 text-xs"
            >
              <div className="min-w-0 flex-1 space-y-1">
                <div className="flex items-center gap-1.5 min-w-0">
                  <span
                    className={`inline-flex shrink-0 items-center gap-1 rounded-md border px-1.5 py-0.5 text-[10px] font-medium leading-none ${meta.badgeClass}`}
                  >
                    <ScopeIcon className="size-2.5 shrink-0" />
                    <span>{t(meta.labelKey)}</span>
                  </span>
                  <span
                    className="truncate font-medium text-foreground"
                    title={displayName}
                  >
                    {displayName}
                  </span>
                </div>
                {(secondaryTarget || addedAtShort) && (
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10px] text-muted-foreground">
                    {secondaryTarget && (
                      <span
                        className="truncate font-mono opacity-80"
                        title={secondaryTarget}
                      >
                        {secondaryTarget}
                      </span>
                    )}
                    {addedAtShort && (
                      <span
                        className="shrink-0 tabular-nums"
                        title={
                          addedAtFull
                            ? `${t("放行时间")}：${addedAtFull}`
                            : undefined
                        }
                      >
                        {addedAtShort}
                      </span>
                    )}
                  </div>
                )}
              </div>

              <button
                type="button"
                data-testid="tool-whitelist-revoke-btn"
                disabled={isRevoking}
                aria-label={t("撤销 {{target}} 的临时白名单", {
                  target: displayName,
                })}
                onClick={() => revokeMutation.mutate(item)}
                className="inline-flex h-6 shrink-0 cursor-pointer items-center gap-1 rounded-md border border-line bg-surface px-2 text-[11px] font-medium text-muted-foreground transition hover:border-destructive/40 hover:bg-destructive/10 hover:text-destructive disabled:opacity-50"
              >
                {isRevoking ? (
                  <Loader2 className="size-3 animate-spin" />
                ) : (
                  <X className="size-3" />
                )}
                <span>{t("撤销")}</span>
              </button>
            </div>
          );
        })}
      </div>
    </div>
  );
}
