import { useMutation } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ShieldAlert, ExternalLink } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { Switch } from "../../shared/ui/components";
import type {
  DeviceToolGuard,
  NexusFleetDevice,
  ToolGuardMode,
} from "../../shared/api/types";

export function DeviceToolGuardSection({
  workspaceID,
  device,
  guard,
  globalMode,
  isOwner,
}: {
  workspaceID: string;
  device: NexusFleetDevice;
  guard?: DeviceToolGuard;
  globalMode?: ToolGuardMode;
  isOwner: boolean;
}) {
  const { t } = useTranslation();
  const enabled = Boolean(guard?.enabled);

  const toggleMutation = useMutation({
    mutationFn: (nextEnabled: boolean) =>
      api.saveDeviceToolGuard(device.id, {
        device_node_id: device.id,
        device_name: device.name ?? "",
        enabled: nextEnabled,
      }),
    onSuccess: (saved) => {
      queryClient.setQueryData<DeviceToolGuard[]>(
        keys.deviceToolGuards(workspaceID),
        (old) => {
          if (!old) return [saved];
          const idx = old.findIndex(
            (item) => item.device_node_id === saved.device_node_id,
          );
          if (idx === -1) return [...old, saved];
          const next = [...old];
          next[idx] = saved;
          return next;
        },
      );
      toast.success(
        saved.enabled
          ? t("已为该设备开启工具安全守卫")
          : t("已关闭该设备工具安全守卫"),
      );
      void queryClient.invalidateQueries({
        queryKey: keys.deviceToolGuards(workspaceID),
      });
    },
    onError: (err) => toast.error(String(err)),
  });

  const activeGlobalLabel =
    globalMode === "strict" ? t("严格模式") : t("仅拦截高危");

  return (
    <div
      data-device-guard-row={device.id}
      className="flex min-w-0 flex-wrap items-center justify-between gap-3 rounded-xl border border-line bg-canvas/30 px-4 py-3 shadow-2xs"
    >
      <div className="flex min-w-0 flex-1 items-center gap-2.5">
        <ShieldAlert
          className={`size-4 shrink-0 ${
            enabled ? "text-primary" : "text-muted-foreground"
          }`}
        />
        <div className="min-w-0 flex-1 space-y-0.5">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-semibold text-foreground">
              {t("设备安全守卫")}
            </h3>
            <span
              className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
                enabled
                  ? "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300"
                  : "bg-muted text-muted-foreground"
              }`}
            >
              {enabled ? t("已开启") : t("守卫已关闭")}
            </span>
            {enabled && (
              <span
                className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
                  globalMode === "strict"
                    ? "bg-amber-500/15 text-amber-700 dark:text-amber-300"
                    : "bg-primary/10 text-primary"
                }`}
              >
                {activeGlobalLabel}
              </span>
            )}
          </div>
          <p className="text-xs text-muted-foreground">
            {enabled
              ? t("已启用工作区全局安全守卫策略，调用高危或敏感工具前自动拦截评估。")
              : t("未开启安全守卫，该设备工具调用将按默认放行策略执行。")}
          </p>
        </div>
      </div>

      <div className="flex shrink-0 items-center gap-3">
        {isOwner ? (
          <>
            <Link
              to="/settings/tool-guard"
              className="inline-flex items-center gap-1 rounded-lg border border-line bg-canvas px-2.5 py-1 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            >
              {t("全局规则设置")}
              <ExternalLink className="size-3" />
            </Link>
            <Switch
              checked={enabled}
              disabled={toggleMutation.isPending}
              onCheckedChange={(checked) => toggleMutation.mutate(Boolean(checked))}
              aria-label={t("切换设备安全守卫")}
            />
          </>
        ) : (
          <span className="rounded-lg border border-line bg-muted/40 px-2.5 py-1 text-[11px] text-muted-foreground">
            {t("仅所有者可修改")}
          </span>
        )}
      </div>
    </div>
  );
}
