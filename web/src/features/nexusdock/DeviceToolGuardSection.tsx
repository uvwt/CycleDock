import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { ShieldAlert, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  Field,
  Select,
  TextArea,
  TextInput,
} from "../../shared/ui/components";
import type {
  DeviceToolGuard,
  ModelRoute,
  NexusFleetDevice,
} from "../../shared/api/types";

type GuardMode = "disabled" | "block_high_risk" | "strict";

export function DeviceToolGuardSection({
  workspaceID,
  device,
  guard,
  routes,
  isOwner,
}: {
  workspaceID: string;
  device: NexusFleetDevice;
  guard?: DeviceToolGuard;
  routes: ModelRoute[];
  isOwner: boolean;
}) {
  const { t } = useTranslation();
  const [isEditing, setIsEditing] = useState(false);
  const [draft, setDraft] = useState<{
    mode: GuardMode;
    modelRouteID: string;
    timeoutSeconds: number;
    customRules: string;
  }>({
    mode: guard?.mode || "disabled",
    modelRouteID: guard?.model_route_id || "",
    timeoutSeconds: guard?.timeout_seconds || 10,
    customRules: guard?.custom_rules || "",
  });

  const mode: GuardMode = guard?.mode || "disabled";

  const saveMutation = useMutation({
    mutationFn: () => {
      const normalizedTimeout = Number.isFinite(draft.timeoutSeconds)
        ? Math.min(60, Math.max(2, Math.trunc(draft.timeoutSeconds)))
        : 10;
      const normalizedRouteID = draft.modelRouteID.trim();
      return api.saveDeviceToolGuard(device.id, {
        device_node_id: device.id,
        device_name: device.name ?? "",
        mode: draft.mode,
        model_route_id: normalizedRouteID || undefined,
        timeout_seconds: normalizedTimeout,
        custom_rules: draft.customRules,
      });
    },
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
      toast.success(t("设备安全守卫配置已保存"));
      setIsEditing(false);
      void queryClient.invalidateQueries({
        queryKey: keys.deviceToolGuards(workspaceID),
      });
    },
    onError: (err) => toast.error(String(err)),
  });

  const deleteMutation = useMutation({
    mutationFn: () => api.deleteDeviceToolGuard(device.id),
    onSuccess: () => {
      queryClient.setQueryData<DeviceToolGuard[]>(
        keys.deviceToolGuards(workspaceID),
        (old) =>
          old ? old.filter((item) => item.device_node_id !== device.id) : old,
      );
      toast.success(t("已重置为默认策略"));
      setIsEditing(false);
      void queryClient.invalidateQueries({
        queryKey: keys.deviceToolGuards(workspaceID),
      });
    },
    onError: (err) => toast.error(String(err)),
  });

  const customRuleSummary = guard?.custom_rules?.trim();

  return (
    <div
      data-device-guard-row={device.id}
      className="rounded-xl border border-line bg-canvas/30 p-4 shadow-2xs space-y-3"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <ShieldAlert className="size-4 text-primary shrink-0" />
          <h3 className="text-sm font-semibold text-foreground">
            {t("设备安全守卫")}
          </h3>
          <span
            className={`inline-flex rounded-full border px-2.5 py-0.5 text-xs font-medium ${
              mode === "disabled"
                ? "border-line bg-muted text-muted-foreground"
                : mode === "strict"
                  ? "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-300"
                  : "border-orange-500/30 bg-orange-500/10 text-orange-700 dark:text-orange-300"
            }`}
          >
            {mode === "disabled"
              ? t("守卫已关闭")
              : mode === "strict"
                ? t("严格模式")
                : t("仅拦截高危")}
          </span>
        </div>

        {isOwner ? (
          <div className="flex items-center gap-2">
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                if (isEditing) {
                  setIsEditing(false);
                } else {
                  // 每次展开编辑面板时以服务端最新守卫快照初始化草稿，避免放弃编辑后残留旧值。
                  setDraft({
                    mode: guard?.mode || "disabled",
                    modelRouteID: guard?.model_route_id || "",
                    timeoutSeconds: guard?.timeout_seconds || 10,
                    customRules: guard?.custom_rules || "",
                  });
                  setIsEditing(true);
                }
              }}
            >
              {isEditing ? t("收起配置") : t("独立配置")}
            </Button>
            {guard && (
              <Button
                variant="ghost"
                size="sm"
                title={t("重置为默认")}
                aria-label={t("重置为默认")}
                disabled={deleteMutation.isPending}
                onClick={() => deleteMutation.mutate()}
              >
                <Trash2 className="size-3.5 text-faint hover:text-rose-600" />
              </Button>
            )}
          </div>
        ) : (
          <span className="text-xs text-faint">{t("仅所有者可修改")}</span>
        )}
      </div>

      {!isEditing && (
        <p className="text-xs leading-relaxed text-muted-foreground">
          {customRuleSummary
            ? customRuleSummary
            : t(
                "针对此设备节点独立配置工具调用安全拦截策略、评估模型与自定义规则。",
              )}
        </p>
      )}

      {isOwner && isEditing && (
        <div className="pt-3 border-t border-line/60 space-y-3 text-[13px]">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <Field label={t("拦截模式")}>
              <Select
                aria-label={t("拦截模式")}
                value={draft.mode}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    mode: e.target.value as GuardMode,
                  })
                }
              >
                <option value="disabled">{t("关闭拦截")}</option>
                <option value="block_high_risk">
                  {t("仅拦截高危不可逆操作（推荐）")}
                </option>
                <option value="strict">
                  {t("严格模式（拦截所有写操作）")}
                </option>
              </Select>
            </Field>

            <Field label={t("风险评估轻量小模型")}>
              <Select
                aria-label={t("风险评估轻量小模型")}
                value={draft.modelRouteID}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    modelRouteID: e.target.value,
                  })
                }
              >
                <option value="">{t("默认使用首个可用路由")}</option>
                {routes
                  .filter((r) => r.enabled && r.id)
                  .map((r) => (
                    <option key={r.id} value={r.id}>
                      {r.model}
                    </option>
                  ))}
              </Select>
            </Field>

            <Field label={t("评估超时（秒）")}>
              <TextInput
                aria-label={t("评估超时（秒）")}
                type="number"
                min={2}
                max={60}
                value={String(draft.timeoutSeconds)}
                onChange={(e) => {
                  const val = parseInt(e.target.value, 10);
                  setDraft({
                    ...draft,
                    timeoutSeconds: Number.isNaN(val) ? 10 : val,
                  });
                }}
              />
            </Field>
          </div>

          <Field
            label={t("此设备专属自定义守卫规则")}
            hint={t("声明此设备特定目录、容器或环境的放行或严禁要求")}
          >
            <TextArea
              aria-label={t("此设备专属自定义守卫规则")}
              rows={2}
              value={draft.customRules}
              onChange={(e) =>
                setDraft({ ...draft, customRules: e.target.value })
              }
              placeholder={t(
                "例如：允许在 /workspace 下修改代码，但严禁重启 docker 或执行 rm -rf...",
              )}
            />
          </Field>

          <div className="flex justify-end gap-2 pt-1">
            <Button variant="ghost" onClick={() => setIsEditing(false)}>
              {t("取消")}
            </Button>
            <Button
              variant="primary"
              disabled={saveMutation.isPending}
              onClick={() => saveMutation.mutate()}
            >
              {t("保存设备守卫规则")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
