import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { z } from "zod";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app-v2/query";
import type { MCPConnection } from "../../shared/api/types";
import {
  Button,
  Input,
  LoadingButton,
  Typography,
  Switch,
} from "../../components/ui/controls";
import {
  DelayedSkeleton,
  ErrorState,
  SettingsSkeleton,
} from "../../components/ui/feedback";

const mcpSchema = z.object({
  name: z.string().trim().min(1, "请输入名称"),
  url: z.url("请输入有效 URL"),
  bearer_token_env: z.string(),
  enabled: z.boolean(),
  timeout_ms: z.number().int().min(1000).max(600000),
});
type MCPValues = z.infer<typeof mcpSchema>;
const defaultMCP: MCPValues = {
  name: "",
  url: "",
  bearer_token_env: "",
  enabled: true,
  timeout_ms: 30000,
};

export function MCPSettings() {
  const { t } = useTranslation();
  const connections = useQuery({ queryKey: keys.mcp, queryFn: api.mcp });
  const [selected, setSelected] = useState<MCPConnection | null>(null);
  const form = useForm<MCPValues>({
    resolver: zodResolver(mcpSchema),
    defaultValues: defaultMCP,
  });
  const enabled = useWatch({ control: form.control, name: "enabled" });
  const select = (item: MCPConnection | null) => {
    setSelected(item);
    form.reset(
      item
        ? {
            name: item.name,
            url: item.url,
            bearer_token_env: item.bearer_token_env,
            enabled: item.enabled,
            timeout_ms: item.timeout_ms,
          }
        : defaultMCP,
    );
  };
  const save = useMutation({
    mutationFn: (data: MCPValues) =>
      selected ? api.updateMCP(selected.id, data) : api.addMCP(data),
    onSuccess: async () => {
      toast.success(t("MCP 连接已保存"));
      select(null);
      await queryClient.invalidateQueries({ queryKey: keys.mcp });
    },
    onError: (error) => toast.error(String(error)),
  });
  const remove = useMutation({
    mutationFn: api.removeMCP,
    onSuccess: async () => {
      select(null);
      await queryClient.invalidateQueries({ queryKey: keys.mcp });
    },
    onError: (error) => toast.error(String(error)),
  });
  const test = useMutation({
    mutationFn: () => api.testMCP(form.getValues()),
    onSuccess: (data) =>
      toast[data.ok ? "success" : "error"](data.message || data.code),
    onError: (error) => toast.error(String(error)),
  });
  if (connections.isPending)
    return (
      <DelayedSkeleton>
        <SettingsSkeleton />
      </DelayedSkeleton>
    );
  if (connections.isError)
    return (
      <ErrorState
        message={String(connections.error)}
        onRetry={() => void connections.refetch()}
      />
    );
  return (
    <div className="space-y-5">
      <div className="space-y-2">
        <Typography as="h2" scale="section">
          {t("已保存的 MCP")}
        </Typography>
        {connections.data?.length ? (
          connections.data.map((item) => (
            <div
              key={item.id}
              className="flex min-w-0 flex-wrap items-center justify-between gap-2 border-b border-[var(--v2-border)] py-2"
            >
              <div className="min-w-0">
                <Typography scale="control" className="font-semibold">
                  {item.name}
                </Typography>
                <Typography scale="meta" className="break-all">
                  {item.url}
                </Typography>
              </div>
              <Button size="compact" onClick={() => select(item)}>
                {t("编辑")}
              </Button>
            </div>
          ))
        ) : (
          <Typography scale="control">
            {t("尚未添加外部 MCP 连接。")}
          </Typography>
        )}
      </div>
      <form
        className="space-y-3 border-t border-[var(--v2-border)] pt-4"
        onSubmit={form.handleSubmit((data) => save.mutate(data))}
      >
        <Typography as="h3" scale="section">
          {selected ? t("编辑 MCP") : t("新建 MCP")}
        </Typography>
        <label className="block space-y-1">
          <Typography scale="control">{t("名称")}</Typography>
          <Input {...form.register("name")} />
        </label>
        <label className="block space-y-1">
          <Typography scale="control">MCP URL</Typography>
          <Input
            {...form.register("url")}
            placeholder="https://example.com/mcp"
          />
        </label>
        <label className="block space-y-1">
          <Typography scale="control">
            {t("Bearer Token 环境变量名（可选）")}
          </Typography>
          <Input
            {...form.register("bearer_token_env")}
            placeholder="MCP_ACCESS_TOKEN"
          />
        </label>
        <label className="block space-y-1">
          <Typography scale="control">{t("超时（毫秒）")}</Typography>
          <Input
            type="number"
            {...form.register("timeout_ms", { valueAsNumber: true })}
          />
        </label>
        <label className="flex items-center gap-2">
          <Switch
            checked={enabled}
            onCheckedChange={(v) =>
              form.setValue("enabled", v, { shouldDirty: true })
            }
          />
          {t("启用此连接")}
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
          <LoadingButton
            type="submit"
            variant="primary"
            loading={save.isPending}
          >
            {selected ? t("保存修改") : t("添加连接")}
          </LoadingButton>
          <LoadingButton
            type="button"
            loading={test.isPending}
            onClick={() => {
              void form.trigger().then((valid) => {
                if (valid) test.mutate();
              });
            }}
          >
            {t("测试连接")}
          </LoadingButton>
          {selected && (
            <>
              <Button type="button" onClick={() => select(null)}>
                {t("取消编辑")}
              </Button>
              <LoadingButton
                type="button"
                variant="danger"
                loading={remove.isPending}
                onClick={() => {
                  if (window.confirm(t("确定删除这个 MCP 连接吗？")))
                    remove.mutate(selected.id);
                }}
              >
                {t("删除")}
              </LoadingButton>
            </>
          )}
        </div>
      </form>
    </div>
  );
}
