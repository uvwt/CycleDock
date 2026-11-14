import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { mcpFormSchema, type MCPFormValues } from "./mcp-form";
import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { Plus, Trash2, TestTube2, ShieldAlert, ExternalLink } from "lucide-react";
import { api } from "../../shared/api/client";
import type { MCPConnection, MCPInput } from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  Checkbox,
  ConfirmDialog,
  ErrorView,
  Field,
  Modal,
  Section,
  Spinner,
  Switch,
  TextInput,
} from "../../shared/ui/components";

const blank: MCPInput = {
  name: "",
  url: "",
  enabled: true,
  bearer_token: "",
  timeout_ms: 30000,
  tool_guard_enabled: false,
};
export function MCPSettings() {
  const list = useQuery({ queryKey: keys.mcp, queryFn: api.mcp });
  const [open, setOpen] = useState(false),
    [picked, setPicked] = useState<MCPConnection | null>(null);
  const save = useMutation({
    mutationFn: (value: MCPInput) =>
      picked ? api.updateMCP(picked.id, value) : api.addMCP(value),
    onSuccess: (saved) => {
      queryClient.setQueryData<MCPConnection[]>(keys.mcp, (old) => {
        if (!old) return [saved];
        const idx = old.findIndex((item) => item.id === saved.id);
        if (idx === -1) return [...old, saved];
        const next = [...old];
        next[idx] = saved;
        return next;
      });
      toast.success("MCP 连接已保存");
      setOpen(false);
      void queryClient.invalidateQueries({ queryKey: keys.mcp });
    },
    onError: (e) => toast.error(String(e)),
  });
  const remove = useMutation({
    mutationFn: () => api.removeMCP(picked!.id),
    onSuccess: () => {
      const removedID = picked?.id;
      if (removedID) {
        queryClient.setQueryData<MCPConnection[]>(keys.mcp, (old) =>
          old ? old.filter((item) => item.id !== removedID) : old,
        );
      }
      toast.success("已删除");
      setOpen(false);
      void queryClient.invalidateQueries({ queryKey: keys.mcp });
    },
    onError: (e) => toast.error(String(e)),
  });
  const test = useMutation({
    mutationFn: (value: MCPInput) =>
      api.testMCP({ ...value, connection_id: picked?.id }),
    onSuccess: (r) =>
      toast[r.ok ? "success" : "warning"](
        `${r.message} · ${r.tool_count ?? 0} tools · ${r.duration_ms} ms`,
      ),
    onError: (e) => toast.error(String(e)),
  });
  const toggle = useMutation({
    mutationFn: (connection: MCPConnection) =>
      api.updateMCP(connection.id, {
        name: connection.name,
        url: connection.url,
        enabled: !connection.enabled,
        timeout_ms: connection.timeout_ms,
        bearer_token: "",
        clear_bearer_token: false,
        tool_guard_enabled: Boolean(connection.tool_guard_enabled),
      }),
    onSuccess: (updated, variables) => {
      queryClient.setQueryData<MCPConnection[]>(keys.mcp, (old) =>
        old
          ? old.map((item) => (item.id === updated.id ? updated : item))
          : old,
      );
      toast.success(variables.enabled ? "已停用此 MCP 连接" : "已启用此 MCP 连接");
      void queryClient.invalidateQueries({ queryKey: keys.mcp });
    },
    onError: (e) => toast.error(String(e)),
  });
  return (
    <Section
      title="MCP 服务"
      description="Agent 在执行开始时发现 MCP 工具；Bearer Token 由服务端加密保存。"
    >
      <div className="mb-5 flex items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">自定义 MCP 连接</p>
        <Button
          onClick={() => {
            setPicked(null);
            setOpen(true);
          }}
        >
          <Plus className="size-4" />
          添加连接
        </Button>
      </div>
      {list.isPending ? (
        <Spinner />
      ) : list.error ? (
        <ErrorView error={list.error} />
      ) : (
        <div className="space-y-2">
          {!list.data?.length && (
            <p className="rounded-xl border border-dashed border-line p-6 text-center text-sm text-faint">
              没有自定义 MCP 服务
            </p>
          )}
          {list.data?.map((c) => (
            <div
              key={c.id}
              className="flex w-full items-center justify-between gap-4 rounded-xl border border-line p-4 transition-colors hover:border-line-strong hover:bg-muted/30"
            >
              <button
                type="button"
                onClick={() => {
                  setPicked(c);
                  setOpen(true);
                }}
                className="flex-1 min-w-0 text-left cursor-pointer outline-none"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <p className="font-semibold text-foreground">{c.name}</p>
                  {c.tool_guard_enabled && (
                    <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/15 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:text-emerald-300">
                      <ShieldAlert className="size-3" />
                      守卫已开启
                    </span>
                  )}
                </div>
                <p className="mt-1 truncate text-xs text-muted-foreground">
                  {c.url}
                </p>
              </button>
              <div className="flex items-center gap-2 shrink-0">
                <Switch
                  checked={c.enabled}
                  disabled={toggle.isPending}
                  onCheckedChange={() => toggle.mutate(c)}
                  aria-label={c.enabled ? "停用此 MCP 连接" : "启用此 MCP 连接"}
                />
              </div>
            </div>
          ))}
        </div>
      )}
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={picked ? "编辑 MCP 服务" : "添加 MCP 服务"}
      >
        <MCPForm
          key={picked?.id || "new"}
          initial={picked || blank}
          onSave={(v) => save.mutate(v)}
          onTest={(v) => test.mutate(v)}
          onDelete={picked ? () => remove.mutate() : undefined}
          busy={save.isPending || test.isPending || remove.isPending}
        />
      </Modal>
    </Section>
  );
}
function MCPForm({
  initial,
  onSave,
  onTest,
  onDelete,
  busy,
}: {
  initial: MCPInput | MCPConnection;
  onSave: (v: MCPInput) => void;
  onTest: (v: MCPInput) => void;
  onDelete?: () => void;
  busy: boolean;
}) {
  const [confirmDelete, setConfirmDelete] = useState(false);
  // 编辑连接只展示是否已配置，绝不向浏览器回传已保存的 Token。
  const {
    register,
    handleSubmit,
    setValue,
    control,
    formState: { errors },
  } = useForm<MCPFormValues>({
    resolver: zodResolver(mcpFormSchema),
    defaultValues: {
      name: initial.name,
      url: initial.url,
      enabled: initial.enabled,
      timeout_ms: initial.timeout_ms,
      bearer_token: "",
      clear_bearer_token: false,
      tool_guard_enabled: Boolean(initial.tool_guard_enabled),
    },
  });
  const clearBearerToken = useWatch({ control, name: "clear_bearer_token" });
  const toolGuardEnabled = useWatch({ control, name: "tool_guard_enabled" });
  const configured = "token_configured" in initial && initial.token_configured;
  return (
    <>
      <form
        noValidate
        onSubmit={handleSubmit((value) => {
          if (!busy) onSave(value);
        })}
      >
        <fieldset disabled={busy} className="space-y-4">
          <Field label="连接名称" error={errors.name?.message}>
            <TextInput {...register("name")} aria-invalid={!!errors.name} />
          </Field>
          <Field label="Remote MCP URL" error={errors.url?.message}>
            <TextInput
              {...register("url")}
              aria-invalid={!!errors.url}
              placeholder="https://mcp.example.com/mcp"
            />
          </Field>
          <Field
            label="Bearer Token（可选）"
            error={errors.bearer_token?.message}
          >
            <TextInput
              type="password"
              autoComplete="new-password"
              {...register("bearer_token", {
                onChange: () => setValue("clear_bearer_token", false),
              })}
              aria-invalid={!!errors.bearer_token}
              placeholder={
                configured ? "留空则保留现有 Token" : "粘贴真实 Bearer Token"
              }
            />
          </Field>
          {configured && (
            <div className="space-y-2 text-xs text-muted-foreground">
              <p>已保存 Token。留空不会覆盖；填写新值将替换。</p>
              <label className="flex cursor-pointer items-center gap-2 text-secondary-foreground">
                <Checkbox
                  checked={clearBearerToken}
                  onCheckedChange={(checked) => {
                    const next = Boolean(checked);
                    setValue("clear_bearer_token", next);
                    if (next) setValue("bearer_token", "");
                  }}
                />
                清除已保存的 Token（改为匿名连接）
              </label>
            </div>
          )}
          <Field label="请求超时（毫秒）" error={errors.timeout_ms?.message}>
            <TextInput
              type="number"
              min={1000}
              max={300000}
              {...register("timeout_ms", { valueAsNumber: true })}
              aria-invalid={!!errors.timeout_ms}
            />
          </Field>

          <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-line/80 bg-muted/20 p-3.5">
            <div className="min-w-0 flex-1 space-y-1">
              <div className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                <ShieldAlert className="size-3.5 shrink-0 text-primary" />
                <span>启用工具安全守卫</span>
              </div>
              <p className="text-xs leading-relaxed text-muted-foreground">
                开启后，调用此 MCP 工具时将按工作区全局工具守卫策略执行风险评估。
                <Link
                  to="/settings/tool-guard"
                  className="ml-1.5 inline-flex items-center gap-0.5 font-medium text-primary hover:underline"
                >
                  全局规则设置
                  <ExternalLink className="size-3" />
                </Link>
              </p>
            </div>
            <Switch
              checked={Boolean(toolGuardEnabled)}
              onCheckedChange={(checked) =>
                setValue("tool_guard_enabled", Boolean(checked))
              }
              aria-label="启用工具安全守卫"
            />
          </div>
          <div className="flex flex-wrap items-center justify-between gap-2 pt-2">
            <div className="flex flex-wrap gap-2">
              <Button type="submit" variant="primary" disabled={busy}>
                保存连接
              </Button>
              <Button
                type="button"
                disabled={busy}
                onClick={handleSubmit((value) => {
                  if (!busy) onTest(value);
                })}
              >
                <TestTube2 className="size-4" />
                测试
              </Button>
            </div>
            {onDelete && (
              <Button
                type="button"
                variant="danger"
                disabled={busy}
                onClick={() => setConfirmDelete(true)}
              >
                <Trash2 className="size-4" />
                删除
              </Button>
            )}
          </div>
        </fieldset>
      </form>
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title="删除 MCP 服务"
        description="确定删除此 MCP 服务？"
        confirmLabel="删除"
        pending={busy}
        onConfirm={() => {
          if (!busy) onDelete?.();
        }}
      />
    </>
  );
}
