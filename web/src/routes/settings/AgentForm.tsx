import { useEffect } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { z } from "zod";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app-v2/query";
import type { Provider } from "../../shared/api/types";
import {
  Input,
  LoadingButton,
  Textarea,
  Typography,
  Switch,
} from "../../components/ui/controls";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../../components/ui/overlays";
import {
  DelayedSkeleton,
  ErrorState,
  SettingsSkeleton,
} from "../../components/ui/feedback";

const agentSchema = z.object({
  enabled: z.boolean(),
  provider_type: z.enum(["chatgpt", "grok", "openai_compatible"]),
  provider_base_url: z.string(),
  model: z.string().trim().min(1, "请选择模型"),
  api_key_env: z.string(),
  request_timeout_ms: z.number().int().min(1000).max(600000),
  max_tool_rounds: z.number().int().min(1).max(100),
  system_instructions: z.string(),
});
type AgentFormValues = z.infer<typeof agentSchema>;
const defaultAgent: AgentFormValues = {
  enabled: false,
  provider_type: "openai_compatible",
  provider_base_url: "",
  model: "",
  api_key_env: "",
  request_timeout_ms: 120000,
  max_tool_rounds: 12,
  system_instructions: "",
};

export function AgentForm() {
  const { t } = useTranslation();
  const agent = useQuery({ queryKey: keys.agent, queryFn: api.agentSettings });
  const form = useForm<AgentFormValues>({
    resolver: zodResolver(agentSchema),
    defaultValues: defaultAgent,
  });
  useEffect(() => {
    if (agent.data) {
      const { configured: _, source_id: __, ...values } = agent.data;
      void _;
      void __;
      form.reset(values);
    }
  }, [agent.data, form]);
  const provider = useWatch({ control: form.control, name: "provider_type" });
  const enabled = useWatch({ control: form.control, name: "enabled" });
  const selectedModel = useWatch({ control: form.control, name: "model" });
  const statuses = useQuery({
    queryKey: keys.auth,
    queryFn: api.authStatuses,
    retry: false,
  });
  const connected = statuses.data?.find(
    (item) => item.provider === provider,
  )?.connected;
  const availableModels = useQuery({
    queryKey: keys.models(provider),
    queryFn: () => api.models(provider as "chatgpt" | "grok"),
    enabled: provider !== "openai_compatible" && !!connected,
    staleTime: 60000,
  });
  const testProvider = useMutation({
    mutationFn: () =>
      api.testProvider({
        provider_type: form.getValues("provider_type"),
        provider_base_url: form.getValues("provider_base_url"),
        model: form.getValues("model"),
        api_key_env: form.getValues("api_key_env"),
        request_timeout_ms: form.getValues("request_timeout_ms"),
      }),
    onSuccess: (result) =>
      toast[result.ok ? "success" : "error"](result.message || result.code),
    onError: (error) => toast.error(String(error)),
  });
  const save = useMutation({
    mutationFn: (value: AgentFormValues) => api.saveAgent(value),
    onSuccess: async () => {
      toast.success(t("Agent 设置已保存"));
      await queryClient.invalidateQueries({ queryKey: keys.agent });
    },
    onError: (error) => toast.error(String(error)),
  });
  if (agent.isPending)
    return (
      <DelayedSkeleton>
        <SettingsSkeleton />
      </DelayedSkeleton>
    );
  if (agent.isError)
    return (
      <ErrorState
        message={String(agent.error)}
        onRetry={() => void agent.refetch()}
      />
    );
  return (
    <form
      className="max-w-2xl space-y-5"
      onSubmit={form.handleSubmit((data) => save.mutate(data))}
    >
      <label className="flex items-center gap-3">
        <Switch
          checked={enabled}
          onCheckedChange={(v) =>
            form.setValue("enabled", v, { shouldDirty: true })
          }
        />
        {t("启用内置 Agent")}
      </label>
      <label className="block space-y-1">
        <Typography scale="control">{t("模型来源")}</Typography>
        <Select
          value={provider}
          onValueChange={(v) =>
            form.setValue("provider_type", v as Provider, { shouldDirty: true })
          }
        >
          <SelectTrigger aria-label={t("模型来源")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="openai_compatible">OpenAI-compatible</SelectItem>
            {/* 当前已选的存量配置仍可见；新增订阅选项严格服从后端 available。 */}
            {(provider === "chatgpt" ||
              statuses.data?.some(
                (item) => item.provider === "chatgpt" && item.available,
              )) && <SelectItem value="chatgpt">ChatGPT Plan</SelectItem>}
            {(provider === "grok" ||
              statuses.data?.some(
                (item) => item.provider === "grok" && item.available,
              )) && <SelectItem value="grok">Grok Subscription</SelectItem>}
          </SelectContent>
        </Select>
      </label>
      {provider === "openai_compatible" && (
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="block space-y-1">
            <Typography scale="control">Provider Base URL</Typography>
            <Input
              {...form.register("provider_base_url")}
              placeholder="https://..."
            />
          </label>
          <label className="block space-y-1">
            <Typography scale="control">{t("API Key 环境变量名")}</Typography>
            <Input
              {...form.register("api_key_env")}
              placeholder="CYCLEDOCK_MODEL_API_KEY"
            />
          </label>
        </div>
      )}
      <label className="block space-y-1">
        <Typography scale="control">{t("模型 ID")}</Typography>
        <Input
          {...form.register("model")}
          placeholder={t("输入已授权可用的模型 ID")}
          required
        />
      </label>
      {provider !== "openai_compatible" && (
        <div className="space-y-2">
          {!connected && (
            <Typography scale="control" className="text-[var(--v2-muted)]">
              {t(
                "请先在「模型服务」完成此平台授权，再读取当前账号的模型目录。",
              )}
            </Typography>
          )}
          {connected && availableModels.isPending && (
            <Typography scale="meta">{t("正在读取模型目录…")}</Typography>
          )}
          {connected && availableModels.isError && (
            <Typography scale="meta">
              {t("模型目录暂不可用，可保留手动输入的模型 ID。")}
            </Typography>
          )}
          {availableModels.data && availableModels.data.length > 0 && (
            <Select
              value={
                availableModels.data.some((model) => model.id === selectedModel)
                  ? selectedModel
                  : undefined
              }
              onValueChange={(id) =>
                form.setValue("model", id, { shouldDirty: true })
              }
            >
              <SelectTrigger aria-label={t("已授权模型目录")}>
                <SelectValue placeholder={t("从当前授权账号选择模型")} />
              </SelectTrigger>
              <SelectContent>
                {availableModels.data.map((model) => (
                  <SelectItem key={model.id} value={model.id}>
                    {model.name || model.id}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block space-y-1">
          <Typography scale="control">{t("请求超时（毫秒）")}</Typography>
          <Input
            type="number"
            {...form.register("request_timeout_ms", { valueAsNumber: true })}
          />
        </label>
        <label className="block space-y-1">
          <Typography scale="control">{t("最大工具调用轮数")}</Typography>
          <Input
            type="number"
            {...form.register("max_tool_rounds", { valueAsNumber: true })}
          />
        </label>
      </div>
      <label className="block space-y-1">
        <Typography scale="control">Workspace Agent Instructions</Typography>
        <Textarea rows={6} {...form.register("system_instructions")} />
      </label>
      {Object.values(form.formState.errors).length > 0 && (
        <Typography scale="control" className="text-[var(--v2-danger)]">
          {Object.values(form.formState.errors)
            .map((e) => e.message && t(e.message))
            .filter(Boolean)
            .join(" · ")}
        </Typography>
      )}
      <div className="flex flex-wrap gap-2">
        <LoadingButton type="submit" variant="primary" loading={save.isPending}>
          {t("保存 Agent 配置")}
        </LoadingButton>
        <LoadingButton
          type="button"
          loading={testProvider.isPending}
          disabled={!selectedModel?.trim()}
          onClick={() => {
            void form.trigger().then((valid) => {
              if (valid) testProvider.mutate();
            });
          }}
        >
          {t("测试当前模型")}
        </LoadingButton>
      </div>
    </form>
  );
}
