import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { ExternalLink, LogOut, TestTube2 } from "lucide-react";
import { api } from "../../shared/api/client";
import type { AgentSettings, Provider } from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  ErrorView,
  Field,
  Section,
  Spinner,
  TextInput,
} from "../../shared/ui/components";
const names: Record<Provider, string> = {
  chatgpt: "ChatGPT Plan",
  grok: "Grok Subscription",
  openai_compatible: "OpenAI-compatible API",
};
export function ModelsSettings() {
  const settings = useQuery({
    queryKey: keys.agent,
    queryFn: api.agentSettings,
  });
  const auth = useQuery({ queryKey: keys.auth, queryFn: api.authStatuses });
  const [override, setDraft] = useState<AgentSettings | null>(null);
  const draft = override ?? settings.data ?? null;
  const [manualURL, setManualURL] = useState("");
  const [authorizationURL, setAuthorizationURL] = useState("");
  const [flow, setFlow] = useState<{
    id: string;
    code: string;
    url: string;
  } | null>(null);
  const provider = draft?.provider_type || "openai_compatible";
  const selectedAuth = auth.data?.find((x) => x.provider === provider);
  const modelQuery = useQuery({
    queryKey: keys.models(provider),
    queryFn: () => api.models(provider as "chatgpt" | "grok"),
    enabled: provider !== "openai_compatible" && !!selectedAuth?.connected,
  });
  const save = useMutation({
    mutationFn: () => {
      if (!draft) throw Error("未加载配置");
      const { configured: unused, source_id: unusedSource, ...rest } = draft;
      void unused;
      void unusedSource;
      return api.saveAgent(rest);
    },
    onSuccess: (r) => {
      queryClient.setQueryData(keys.agent, r);
      toast.success("模型配置已保存");
    },
    onError: (e) => toast.error(String(e)),
  });
  const test = useMutation({
    mutationFn: () =>
      api.testProvider({
        provider_type: provider,
        provider_base_url: draft?.provider_base_url || "",
        model: draft?.model || "",
        api_key_env: draft?.api_key_env || "",
        request_timeout_ms: draft?.request_timeout_ms || 120000,
      }),
    onSuccess: (r) => toast[r.ok ? "success" : "warning"](r.message),
    onError: (e) => toast.error(String(e)),
  });
  const chatgpt = useMutation({
    mutationFn: api.chatgptStart,
    onSuccess: (r) => {
      if (r.manual_callback) {
        setAuthorizationURL(r.authorization_url);
        setManualURL("");
      } else window.location.assign(r.authorization_url);
    },
    onError: (e) => toast.error(String(e)),
  });
  const complete = useMutation({
    mutationFn: () => api.chatgptComplete(manualURL.trim()),
    onSuccess: async () => {
      setAuthorizationURL("");
      setManualURL("");
      await queryClient.invalidateQueries({ queryKey: keys.auth });
      toast.success("连接成功");
    },
    onError: (e) => toast.error(String(e)),
  });
  const grok = useMutation({
    mutationFn: api.grokStart,
    onSuccess: (r) => {
      setFlow({ id: r.flow_id, code: r.user_code, url: r.verification_url });
      window.open(r.verification_url, "_blank", "noopener,noreferrer");
    },
    onError: (e) => toast.error(String(e)),
  });
  const poll = useMutation({
    mutationFn: (id: string) => api.grokPoll(id),
    onSuccess: async (r) => {
      if (r.status === "connected") {
        setFlow(null);
        await queryClient.invalidateQueries({ queryKey: keys.auth });
        toast.success("Grok 已连接");
      } else toast.message("等待 xAI 账号授权");
    },
    onError: (e) => toast.error(String(e)),
  });
  const disconnect = useMutation({
    mutationFn: (p: string) => api.modelDisconnect(p),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.auth });
      toast.success("连接已断开");
    },
    onError: (e) => toast.error(String(e)),
  });
  if (settings.isPending || auth.isPending)
    return <Spinner text="加载模型配置…" />;
  if (settings.error || auth.error)
    return <ErrorView error={settings.error || auth.error} />;
  if (!draft) return null;
  const available: Provider[] = [
    "openai_compatible",
    "grok",
    ...((auth.data || []).find((x) => x.provider === "chatgpt")?.available ||
    provider === "chatgpt"
      ? ["chatgpt" as Provider]
      : []),
  ];
  return (
    <div className="space-y-5">
      <Section
        title="模型提供商"
        description="选择 Agent 使用的模型。模型账号授权与 CycleDock 登录分开管理。"
      >
        <div className="grid gap-3 sm:grid-cols-3">
          {available.map((p) => (
            <button
              type="button"
              key={p}
              onClick={() => {
                setDraft({ ...draft, provider_type: p, model: "" });
                setFlow(null);
                setAuthorizationURL("");
              }}
              className={`rounded-xl border p-3 text-left text-sm font-semibold transition ${provider === p ? "border-sky-500 bg-sky-50 text-sky-800" : "border-slate-200 hover:border-slate-300"}`}
            >
              {names[p]}
            </button>
          ))}
        </div>
        {provider === "openai_compatible" ? (
          <div className="mt-5 grid gap-4 sm:grid-cols-2">
            <Field label="OpenAI-compatible Base URL">
              <TextInput
                value={draft.provider_base_url}
                onChange={(e) =>
                  setDraft({ ...draft, provider_base_url: e.target.value })
                }
                placeholder="https://api.example.com/v1"
              />
            </Field>
            <Field label="API Key 环境变量名称">
              <TextInput
                value={draft.api_key_env}
                onChange={(e) =>
                  setDraft({ ...draft, api_key_env: e.target.value })
                }
                placeholder="CYCLEDOCK_MODEL_API_KEY"
              />
            </Field>
          </div>
        ) : (
          <div className="mt-5 rounded-xl border border-slate-200 bg-slate-50 p-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p className="font-medium">{names[provider]} 连接状态</p>
                <p className="mt-1 text-xs text-slate-500">
                  {selectedAuth?.connected
                    ? "已连接"
                    : selectedAuth?.available
                      ? "尚未授权"
                      : "当前部署未开放"}
                </p>
                {selectedAuth?.warning && (
                  <p className="mt-2 text-xs text-amber-700">
                    这是 Hosted DEV 的临时登录测试模式，正式使用需要批准的 OAuth
                    Client ID。
                  </p>
                )}
              </div>
              {selectedAuth?.connected ? (
                <Button
                  onClick={() => disconnect.mutate(provider)}
                  disabled={disconnect.isPending}
                >
                  <LogOut className="size-4" />
                  断开
                </Button>
              ) : selectedAuth?.available ? (
                <Button
                  variant="primary"
                  disabled={grok.isPending || chatgpt.isPending}
                  onClick={() =>
                    provider === "chatgpt" ? chatgpt.mutate() : grok.mutate()
                  }
                >
                  <ExternalLink className="size-4" />
                  连接 {names[provider]}
                </Button>
              ) : null}
            </div>
            {provider === "chatgpt" && authorizationURL && (
              <div className="mt-4 space-y-3 rounded-xl border border-amber-200 bg-amber-50 p-3 text-xs">
                <p>
                  仅限 DEV：先完成官方 OpenAI 登录，再复制浏览器地址栏最终
                  localhost callback URL 到下方。
                </p>
                <a
                  href={authorizationURL}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-2 text-sky-700 underline"
                >
                  <ExternalLink className="size-3" />
                  打开 OpenAI 登录
                </a>
                <TextInput
                  value={manualURL}
                  autoComplete="off"
                  onChange={(e) => setManualURL(e.target.value)}
                  placeholder="http://localhost:1455/auth/callback?code=..."
                />
                <Button
                  disabled={!manualURL.trim() || complete.isPending}
                  onClick={() => complete.mutate()}
                >
                  完成连接
                </Button>
              </div>
            )}
            {provider === "grok" && flow && (
              <div className="mt-4 space-y-2 rounded-xl bg-sky-50 p-3 text-sm">
                <p>
                  在 xAI 授权页输入代码：
                  <strong className="font-mono">{flow.code}</strong>
                </p>
                <a
                  href={flow.url}
                  target="_blank"
                  rel="noreferrer"
                  className="text-sky-700 underline"
                >
                  打开授权页
                </a>
                <div>
                  <Button
                    onClick={() => poll.mutate(flow.id)}
                    disabled={poll.isPending}
                  >
                    检查授权结果
                  </Button>
                </div>
              </div>
            )}
          </div>
        )}
        <div className="mt-5">
          <Field label="模型">
            <>
              {provider !== "openai_compatible" && !!modelQuery.data?.length ? (
                <select
                  value={draft.model}
                  onChange={(e) =>
                    setDraft({ ...draft, model: e.target.value })
                  }
                  className="w-full rounded-xl border border-slate-200 bg-white p-2.5 text-sm"
                >
                  <option value="">选择模型</option>
                  {modelQuery.data.map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.name}
                    </option>
                  ))}
                </select>
              ) : (
                <TextInput
                  disabled={
                    provider !== "openai_compatible" && !selectedAuth?.connected
                  }
                  value={draft.model}
                  onChange={(e) =>
                    setDraft({ ...draft, model: e.target.value })
                  }
                  placeholder={
                    selectedAuth?.connected ? "模型 ID" : "先连接账户"
                  }
                />
              )}
            </>
          </Field>
          {modelQuery.isError && (
            <p className="mt-1 text-xs text-amber-600">
              无法获取模型目录，可手动输入模型 ID。
            </p>
          )}
        </div>
        <div className="mt-5 flex flex-wrap gap-2">
          <Button
            variant="primary"
            onClick={() => save.mutate()}
            disabled={save.isPending}
          >
            保存模型
          </Button>
          <Button
            onClick={() => test.mutate()}
            disabled={test.isPending || !draft.model.trim()}
          >
            <TestTube2 className="size-4" />
            测试连接
          </Button>
        </div>
      </Section>
    </div>
  );
}
