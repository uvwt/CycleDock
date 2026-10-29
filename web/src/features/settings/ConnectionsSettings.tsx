import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { mcpFormSchema, type MCPFormValues } from "./mcp-form";
import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Cable, ExternalLink, Plus, Trash2, TestTube2 } from "lucide-react";
import { api } from "../../shared/api/client";
import type { MCPConnection, MCPInput } from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  Checkbox,
  ConfirmDialog,
  ErrorView,
  Field,
  Section,
  Sheet,
  Spinner,
  TextInput,
} from "../../shared/ui/components";

export function NexusSettings() {
  const q = useQuery({ queryKey: keys.nexus, queryFn: api.nexus });
  const connect = useMutation({
    mutationFn: api.nexusConnect,
    onSuccess: (r) => window.location.assign(r.authorization_url),
    onError: (e) => toast.error(String(e)),
  });
  const disconnect = useMutation({
    mutationFn: api.nexusDisconnect,
    onSuccess: () => {
      toast.success("已断开");
      void queryClient.invalidateQueries({ queryKey: keys.nexus });
    },
    onError: (e) => toast.error(String(e)),
  });
  return (
    <Section
      title="NexusDock 远程设备"
      description="第一方设备授权，不需要手动添加 MCP URL、Token 或设备 ID。"
    >
      {q.isPending ? (
        <Spinner />
      ) : q.error ? (
        <ErrorView error={q.error} />
      ) : !q.data?.available ? (
        <p className="text-sm text-muted-foreground">
          当前部署尚未启用 NexusDock 第一方连接。
        </p>
      ) : (
        <div className="space-y-4">
          <div className="flex items-center gap-3 rounded-xl border border-line bg-canvas p-4">
            <Cable className="size-6 text-sky-700" />
            <div>
              <p className="font-semibold">NexusDock</p>
              <p className="text-xs text-muted-foreground">
                {q.data.connected
                  ? "已授权 Cycle Agent 使用已配对设备"
                  : "尚未连接"}
              </p>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            {q.data.connected ? (
              <>
                <Button
                  variant="danger"
                  disabled={disconnect.isPending}
                  onClick={() => disconnect.mutate()}
                >
                  断开设备连接
                </Button>
                {q.data.manage_url && (
                  <a
                    href={q.data.manage_url}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-2 rounded-xl border border-line px-3 py-2 text-sm"
                  >
                    <ExternalLink className="size-4" />
                    前往 NexusDock 管理设备
                  </a>
                )}
              </>
            ) : (
              <Button
                variant="primary"
                disabled={connect.isPending}
                onClick={() => connect.mutate()}
              >
                授权使用设备
              </Button>
            )}
          </div>
        </div>
      )}
    </Section>
  );
}
const blank: MCPInput = {
  name: "",
  url: "",
  enabled: true,
  bearer_token: "",
  timeout_ms: 30000,
};
export function MCPSettings() {
  const list = useQuery({ queryKey: keys.mcp, queryFn: api.mcp });
  const [open, setOpen] = useState(false),
    [picked, setPicked] = useState<MCPConnection | null>(null);
  const save = useMutation({
    mutationFn: (value: MCPInput) =>
      picked ? api.updateMCP(picked.id, value) : api.addMCP(value),
    onSuccess: async () => {
      toast.success("MCP 连接已保存");
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: keys.mcp });
    },
    onError: (e) => toast.error(String(e)),
  });
  const remove = useMutation({
    mutationFn: () => api.removeMCP(picked!.id),
    onSuccess: async () => {
      toast.success("已删除");
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: keys.mcp });
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
            <button
              key={c.id}
              onClick={() => {
                setPicked(c);
                setOpen(true);
              }}
              className="flex w-full items-center justify-between gap-4 rounded-xl border border-line p-4 text-left hover:border-sky-300"
            >
              <div className="min-w-0">
                <p className="font-semibold">{c.name}</p>
                <p className="mt-1 truncate text-xs text-muted-foreground">
                  {c.url}
                </p>
              </div>
              <span
                className={`text-xs ${c.enabled ? "text-emerald-700" : "text-faint"}`}
              >
                {c.enabled ? "启用" : "停用"}
              </span>
            </button>
          ))}
        </div>
      )}
      <Sheet
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
      </Sheet>
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
    },
  });
  const clearBearerToken = useWatch({ control, name: "clear_bearer_token" });
  const enabled = useWatch({ control, name: "enabled" });
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
          <label className="flex cursor-pointer items-center gap-2 text-sm">
            <Checkbox
              checked={enabled}
              onCheckedChange={(checked) =>
                setValue("enabled", Boolean(checked))
              }
            />
            启用服务
          </label>
          <div className="flex flex-wrap gap-2">
            <Button type="submit" variant="primary" disabled={busy}>
              保存连接
            </Button>
            <Button
              disabled={busy}
              onClick={handleSubmit((value) => {
                if (!busy) onTest(value);
              })}
            >
              <TestTube2 className="size-4" />
              测试
            </Button>
            {onDelete && (
              <Button
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
