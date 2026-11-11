import { useState } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { Server, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  Field,
  Section,
  Select,
  Spinner,
  TextArea,
  TextInput,
} from "../../shared/ui/components";
import type { DeviceToolGuard } from "../../shared/api/types";

export function DeviceToolGuardsSettings() {
  const { t } = useTranslation();
  const devices = useQuery({
    queryKey: ["nexus", "devices"],
    queryFn: api.nexusDevices,
  });
  const guards = useQuery({
    queryKey: ["device-tool-guards"],
    queryFn: api.listDeviceToolGuards,
  });
  const agentSettings = useQuery({
    queryKey: keys.agent,
    queryFn: api.agentSettings,
  });

  const [editingNodeID, setEditingNodeID] = useState<string | null>(null);
  const [draft, setDraft] = useState<{
    deviceName: string;
    mode: "disabled" | "block_high_risk" | "strict";
    modelRouteID: string;
    timeoutSeconds: number;
    customRules: string;
  }>({
    deviceName: "",
    mode: "block_high_risk",
    modelRouteID: "",
    timeoutSeconds: 10,
    customRules: "",
  });

  const saveMutation = useMutation({
    mutationFn: (nodeID: string) =>
      api.saveDeviceToolGuard(nodeID, {
        device_node_id: nodeID,
        device_name: draft.deviceName,
        mode: draft.mode,
        model_route_id: draft.modelRouteID || undefined,
        timeout_seconds: draft.timeoutSeconds,
        custom_rules: draft.customRules,
      }),
    onSuccess: async () => {
      toast.success(t("设备安全守卫配置已保存"));
      await queryClient.invalidateQueries({ queryKey: ["device-tool-guards"] });
      setEditingNodeID(null);
    },
    onError: (err) => toast.error(String(err)),
  });

  const deleteMutation = useMutation({
    mutationFn: (nodeID: string) => api.deleteDeviceToolGuard(nodeID),
    onSuccess: async () => {
      toast.success(t("已重置为默认策略"));
      await queryClient.invalidateQueries({ queryKey: ["device-tool-guards"] });
    },
    onError: (err) => toast.error(String(err)),
  });

  const guardMap = new Map<string, DeviceToolGuard>();
  for (const g of guards.data || []) {
    guardMap.set(g.device_node_id, g);
  }

  const deviceList = ("devices" in (devices.data || {})) ? (devices.data as { devices: Array<{ id: string; name: string; os: string; arch: string; online: boolean }> }).devices : [];

  return (
    <Section
      title={t("设备安全守卫（Device Tool Guard）")}
      description={t(
        "针对不同设备节点单独配置安全拦截策略、评估超时与自然语言规则，不搞一刀切全局拦截。",
      )}
    >
      <div className="space-y-4 text-[13px]">
        {devices.isPending || guards.isPending ? (
          <Spinner />
        ) : deviceList.length === 0 ? (
          <p className="rounded-xl border border-dashed border-line p-6 text-center text-sm text-faint">
            {t("暂无在线或已连接的设备节点")}
          </p>
        ) : (
          <div className="space-y-3">
            {deviceList.map((dev) => {
              const guard = guardMap.get(dev.id);
              const isEditing = editingNodeID === dev.id;
              const mode = guard?.mode || "disabled";

              return (
                <div
                  key={dev.id}
                  data-device-guard-row={dev.id}
                  className="rounded-xl border border-line bg-surface p-4 transition-colors"
                >
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="flex items-center gap-2.5">
                      <Server className="size-4 text-primary shrink-0" />
                      <div>
                        <div className="flex items-center gap-2">
                          <span className="font-semibold text-foreground">
                            {dev.name}
                          </span>
                          <span className="font-mono text-[11px] text-faint">
                            {dev.id}
                          </span>
                        </div>
                        <p className="text-xs text-muted-foreground">
                          {dev.os} / {dev.arch} ·{" "}
                          <span
                            className={
                              dev.online ? "text-emerald-600" : "text-faint"
                            }
                          >
                            {dev.online ? t("在线") : t("离线")}
                          </span>
                        </p>
                      </div>
                    </div>

                    <div className="flex items-center gap-2">
                      <span
                        className={`inline-flex rounded-full border px-2.5 py-0.5 text-xs font-medium ${
                          mode === "disabled"
                            ? "border-muted bg-muted text-muted-foreground"
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
                      <Button
                        variant="secondary"
                        onClick={() => {
                          if (isEditing) {
                            setEditingNodeID(null);
                          } else {
                            setDraft({
                              deviceName: dev.name,
                              mode: guard?.mode || "disabled",
                              modelRouteID: guard?.model_route_id || "",
                              timeoutSeconds: guard?.timeout_seconds || 10,
                              customRules: guard?.custom_rules || "",
                            });
                            setEditingNodeID(dev.id);
                          }
                        }}
                      >
                        {isEditing ? t("收起配置") : t("独立配置")}
                      </Button>
                      {guard && (
                        <Button
                          variant="ghost"
                          title={t("重置为默认")}
                          disabled={deleteMutation.isPending}
                          onClick={() => deleteMutation.mutate(dev.id)}
                        >
                          <Trash2 className="size-3.5 text-faint hover:text-rose-600" />
                        </Button>
                      )}
                    </div>
                  </div>

                  {isEditing && (
                    <div className="mt-4 pt-3 border-t border-line/60 space-y-3">
                      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                        <Field label={t("拦截模式")}>
                          <Select
                            value={draft.mode}
                            onChange={(e) =>
                              setDraft({
                                ...draft,
                                mode: e.target.value as
                                  | "disabled"
                                  | "block_high_risk"
                                  | "strict",
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
                            value={draft.modelRouteID}
                            onChange={(e) =>
                              setDraft({
                                ...draft,
                                modelRouteID: e.target.value,
                              })
                            }
                          >
                            <option value="">{t("默认使用首个可用路由")}</option>
                            {(agentSettings.data?.routes || [])
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
                        <Button
                          variant="ghost"
                          onClick={() => setEditingNodeID(null)}
                        >
                          {t("取消")}
                        </Button>
                        <Button
                          variant="primary"
                          disabled={saveMutation.isPending}
                          onClick={() => saveMutation.mutate(dev.id)}
                        >
                          {t("保存设备守卫规则")}
                        </Button>
                      </div>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </Section>
  );
}
