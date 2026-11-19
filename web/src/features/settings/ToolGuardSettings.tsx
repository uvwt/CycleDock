import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ShieldAlert, Plug, MonitorSmartphone, AlertCircle } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import type { ToolGuardMode, ToolGuardSettings as ToolGuardSettingsData } from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  ErrorView,
  Field,
  Section,
  Select,
  Spinner,
  TextArea,
  TextInput,
} from "../../shared/ui/components";

const DEFAULT_SETTINGS: ToolGuardSettingsData = {
  mode: "block_high_risk",
  model_route_id: "",
  timeout_seconds: 10,
  custom_rules: "",
};

export function ToolGuardSettings() {
  const { t } = useTranslation();
  const guardQuery = useQuery({
    queryKey: keys.toolGuard,
    queryFn: api.toolGuard,
  });
  const agentQuery = useQuery({
    queryKey: keys.agent,
    queryFn: api.agentSettings,
  });

  if (guardQuery.isPending || agentQuery.isPending) {
    return (
      <Section title={t("工具守卫")}>
        <Spinner />
      </Section>
    );
  }
  if (guardQuery.error) {
    return (
      <Section title={t("工具守卫")}>
        <ErrorView error={guardQuery.error} />
      </Section>
    );
  }
  if (agentQuery.error) {
    return (
      <Section title={t("工具守卫")}>
        <ErrorView error={agentQuery.error} />
      </Section>
    );
  }

  const initial = guardQuery.data ?? DEFAULT_SETTINGS;
  const formKey = `${initial.mode}|${initial.model_route_id ?? ""}|${initial.timeout_seconds}|${initial.custom_rules}`;

  return (
    <ToolGuardSettingsForm
      key={formKey}
      initial={initial}
      routes={agentQuery.data?.routes ?? []}
    />
  );
}

function ToolGuardSettingsForm({
  initial,
  routes,
}: {
  initial: ToolGuardSettingsData;
  routes: Array<{
    id: string;
    enabled: boolean;
    model: string;
    provider_type?: string;
  }>;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<ToolGuardMode>(initial.mode);
  const [modelRouteID, setModelRouteID] = useState(
    initial.model_route_id || "",
  );
  const [timeoutSeconds, setTimeoutSeconds] = useState<number>(
    initial.timeout_seconds || 10,
  );
  const [customRules, setCustomRules] = useState(initial.custom_rules || "");

  const enabledRoutes = routes.filter((route) => route.enabled);
  const selectedRouteMissingOrDisabled =
    Boolean(modelRouteID) &&
    !enabledRoutes.some((route) => route.id === modelRouteID);

  const saveMutation = useMutation({
    mutationFn: () => {
      const normalizedTimeout = Number.isFinite(timeoutSeconds)
        ? Math.min(60, Math.max(2, Math.trunc(timeoutSeconds)))
        : 10;
      const normalizedRouteID = modelRouteID.trim();
      return api.saveToolGuard({
        mode,
        model_route_id: normalizedRouteID || "",
        timeout_seconds: normalizedTimeout,
        custom_rules: customRules,
      });
    },
    onSuccess: (saved) => {
      queryClient.setQueryData(keys.toolGuard, saved);
      toast.success(t("全局工具守卫配置已保存"));
      void queryClient.invalidateQueries({ queryKey: keys.toolGuard });
    },
    onError: (err) => toast.error(String(err)),
  });

  return (
    <div className="space-y-5">
      <Section
        title={t("工作区全局工具守卫")}
        description={t(
          "统一配置所有受保护远程设备与 MCP 服务的风险评估档位、评估小模型、超时时间与全局规则。",
        )}
      >
        <div className="space-y-5">
          <div className="flex flex-col gap-3 rounded-xl border border-line bg-muted/30 p-3.5 text-xs text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
            <div className="flex min-w-0 items-start gap-2.5">
              <ShieldAlert className="mt-0.5 size-4 shrink-0 text-primary" />
              <p className="min-w-0 leading-relaxed">
                {t(
                  "各远程设备与 MCP 服务是否开启守卫可在对应页面单独开关；开启后将统一按此处配置执行评估。",
                )}
              </p>
            </div>
            <div className="flex shrink-0 flex-wrap items-center gap-2">
              <Link
                to="/nexusdock"
                className="inline-flex items-center gap-1.5 rounded-lg border border-line bg-canvas px-2.5 py-1.5 text-xs font-medium text-foreground transition-colors hover:bg-muted"
              >
                <MonitorSmartphone className="size-3.5 text-primary" />
                {t("管理设备开关")}
              </Link>
              <Link
                to="/settings/mcp"
                className="inline-flex items-center gap-1.5 rounded-lg border border-line bg-canvas px-2.5 py-1.5 text-xs font-medium text-foreground transition-colors hover:bg-muted"
              >
                <Plug className="size-3.5 text-primary" />
                {t("管理 MCP 开关")}
              </Link>
            </div>
          </div>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
            <Field label={t("全局拦截档位")}>
              <Select
                aria-label={t("全局拦截档位")}
                value={mode}
                onChange={(e) => setMode(e.target.value as ToolGuardMode)}
              >
                <option value="block_high_risk">
                  {t("仅拦截高危不可逆操作（推荐）")}
                </option>
                <option value="strict">
                  {t("严格模式（拦截所有写操作）")}
                </option>
              </Select>
            </Field>

            <Field label={t("风险评估小模型")}>
              <Select
                aria-label={t("风险评估小模型")}
                value={modelRouteID}
                onChange={(e) => setModelRouteID(e.target.value)}
              >
                <option value="">{t("默认使用首个可用路由")}</option>
                {enabledRoutes.map((route) => (
                  <option key={route.id} value={route.id}>
                    {route.model || route.provider_type || route.id}
                  </option>
                ))}
                {selectedRouteMissingOrDisabled && (
                  <option value={modelRouteID}>
                    {t("已失效或停用的路由")} ({modelRouteID.slice(0, 8)}…)
                  </option>
                )}
              </Select>
            </Field>

            <Field
              label={t("评估超时（秒）")}
              hint={t("允许范围 2~60 秒，超时后安全兜底挂起确认")}
            >
              <TextInput
                aria-label={t("评估超时（秒）")}
                type="number"
                min={2}
                max={60}
                value={Number.isFinite(timeoutSeconds) ? timeoutSeconds : 10}
                onChange={(e) =>
                  setTimeoutSeconds(Number.parseInt(e.target.value, 10) || 10)
                }
              />
            </Field>
          </div>

          {selectedRouteMissingOrDisabled && (
            <div className="flex items-start gap-2 rounded-xl border border-amber-500/25 bg-amber-500/10 px-3.5 py-2.5 text-xs text-amber-700 dark:text-amber-300">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              <span>
                {t(
                  "当前绑定的评估模型路由已被停用或删除，运行时会自动回退到首个可用模型路由；建议重新选择并保存。",
                )}
              </span>
            </div>
          )}

          <Field
            label={t("全局自定义守卫规则")}
            hint={t(
              "这些规则会作为最高优先级补充约束传入风险评估小模型，同时适用于所有开启守卫的远程设备与 MCP 服务。",
            )}
          >
            <TextArea
              aria-label={t("全局自定义守卫规则")}
              rows={5}
              value={customRules}
              onChange={(e) => setCustomRules(e.target.value)}
              placeholder={t(
                "例如：允许在独立 Git worktree 中自由修改测试代码；涉及重启生产容器、删除日志或修改线上配置必须触发人工确认。",
              )}
            />
          </Field>

          <div className="flex flex-wrap items-center justify-end gap-2 pt-1">
            <Button
              variant="primary"
              disabled={saveMutation.isPending}
              onClick={() => saveMutation.mutate()}
            >
              {saveMutation.isPending ? t("保存中…") : t("保存全局守卫配置")}
            </Button>
          </div>
        </div>
      </Section>
    </div>
  );
}
