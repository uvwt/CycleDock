import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Cable, ExternalLink, Plus, Trash2, TestTube2 } from "lucide-react";
import { api } from "../../shared/api/client";
import type { MCPConnection, MCPInput } from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import {
  Button,
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
        <p className="text-sm text-slate-500">
          当前部署尚未启用 NexusDock 第一方连接。
        </p>
      ) : (
        <div className="space-y-4">
          <div className="flex items-center gap-3 rounded-xl border border-slate-200 bg-slate-50 p-4">
            <Cable className="size-6 text-sky-700" />
            <div>
              <p className="font-semibold">NexusDock</p>
              <p className="text-xs text-slate-500">
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
                    className="inline-flex items-center gap-2 rounded-xl border border-slate-200 px-3 py-2 text-sm"
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
  bearer_token_env: "",
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
    mutationFn: (value: MCPInput) => api.testMCP(value),
    onSuccess: (r) =>
      toast[r.ok ? "success" : "warning"](
        `${r.message} · ${r.tool_count ?? 0} tools · ${r.duration_ms} ms`,
      ),
    onError: (e) => toast.error(String(e)),
  });
  return (
    <Section
      title="MCP 服务"
      description="Agent 在执行开始时发现 MCP 工具；Bearer Token 从服务端环境变量获取。"
    >
      <div className="mb-5 flex items-center justify-between gap-3">
        <p className="text-sm text-slate-500">自定义 MCP 连接</p>
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
            <p className="rounded-xl border border-dashed border-slate-200 p-6 text-center text-sm text-slate-400">
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
              className="flex w-full items-center justify-between gap-4 rounded-xl border border-slate-200 p-4 text-left hover:border-sky-300"
            >
              <div className="min-w-0">
                <p className="font-semibold">{c.name}</p>
                <p className="mt-1 truncate text-xs text-slate-500">{c.url}</p>
              </div>
              <span
                className={`text-xs ${c.enabled ? "text-emerald-700" : "text-slate-400"}`}
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
  initial: MCPInput;
  onSave: (v: MCPInput) => void;
  onTest: (v: MCPInput) => void;
  onDelete?: () => void;
  busy: boolean;
}) {
  const [value, setValue] = useState<MCPInput>({ ...initial });
  return (
    <div className="space-y-4">
      <Field label="连接名称">
        <TextInput
          value={value.name}
          onChange={(e) => setValue({ ...value, name: e.target.value })}
        />
      </Field>
      <Field label="Remote MCP URL">
        <TextInput
          value={value.url}
          onChange={(e) => setValue({ ...value, url: e.target.value })}
          placeholder="https://mcp.example.com/mcp"
        />
      </Field>
      <Field label="Bearer Token 环境变量名称">
        <TextInput
          value={value.bearer_token_env}
          onChange={(e) =>
            setValue({ ...value, bearer_token_env: e.target.value })
          }
          placeholder="可选，例如 CYCLEDOCK_MCP_TOKEN"
        />
      </Field>
      <Field label="请求超时（毫秒）">
        <TextInput
          type="number"
          min={1000}
          max={300000}
          value={value.timeout_ms}
          onChange={(e) =>
            setValue({ ...value, timeout_ms: Number(e.target.value) })
          }
        />
      </Field>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          className="size-4 accent-sky-700"
          checked={value.enabled}
          onChange={(e) => setValue({ ...value, enabled: e.target.checked })}
        />
        启用服务
      </label>
      <div className="flex flex-wrap gap-2">
        <Button
          variant="primary"
          disabled={busy || !value.name.trim() || !value.url.trim()}
          onClick={() => onSave(value)}
        >
          保存连接
        </Button>
        <Button
          disabled={busy || !value.name.trim() || !value.url.trim()}
          onClick={() => onTest(value)}
        >
          <TestTube2 className="size-4" />
          测试
        </Button>
        {onDelete && (
          <Button
            variant="danger"
            disabled={busy}
            onClick={() => {
              if (window.confirm("确定删除此 MCP 服务？")) onDelete();
            }}
          >
            <Trash2 className="size-4" />
            删除
          </Button>
        )}
      </div>
    </div>
  );
}
