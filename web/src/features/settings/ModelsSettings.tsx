import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  ArrowDown,
  ArrowUp,
  ExternalLink,
  LogOut,
  Plus,
  TestTube2,
  Trash2,
} from "lucide-react";
import { api } from "../../shared/api/client";
import type {
  AgentSettings,
  ModelAuth,
  ModelRoute,
  Provider,
} from "../../shared/api/types";
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

const selectClass =
  "w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-[13px] leading-5 outline-none focus:border-sky-500";

function newRoute(): ModelRoute {
  return {
    id: crypto.randomUUID(),
    enabled: true,
    provider_type: "openai_compatible",
    credential_id: "",
    provider_base_url: "",
    model: "",
    api_key_env: "",
  };
}

function providerAccounts(auth: ModelAuth[], provider: Provider) {
  if (provider === "openai_compatible") return [];
  return auth.find((item) => item.provider === provider)?.accounts ?? [];
}

function RouteEditor({
  route,
  index,
  total,
  auth,
  timeout,
  onChange,
  onMove,
  onRemove,
}: {
  route: ModelRoute;
  index: number;
  total: number;
  auth: ModelAuth[];
  timeout: number;
  onChange: (route: ModelRoute) => void;
  onMove: (from: number, to: number) => void;
  onRemove: () => void;
}) {
  const subscription = route.provider_type !== "openai_compatible";
  const accounts = providerAccounts(auth, route.provider_type);
  const catalog = useQuery({
    queryKey: keys.models(route.provider_type, route.credential_id),
    queryFn: () =>
      api.models(
        route.provider_type as "chatgpt" | "grok",
        route.credential_id,
      ),
    enabled: subscription && !!route.credential_id,
  });
  const test = useMutation({
    mutationFn: () =>
      api.testProvider({
        provider_type: route.provider_type,
        credential_id: route.credential_id,
        provider_base_url: route.provider_base_url,
        model: route.model,
        api_key_env: route.api_key_env,
        request_timeout_ms: timeout || 120000,
      }),
    onSuccess: (result) =>
      toast[result.ok ? "success" : "warning"](result.message),
    onError: (error) => toast.error(String(error)),
  });

  const setProvider = (provider: Provider) => {
    const nextAccounts = providerAccounts(auth, provider);
    onChange({
      ...route,
      provider_type: provider,
      credential_id:
        provider === "openai_compatible" ? "" : nextAccounts[0]?.id ?? "",
      provider_base_url:
        provider === "openai_compatible" ? route.provider_base_url : "",
      api_key_env: provider === "openai_compatible" ? route.api_key_env : "",
      model: "",
    });
  };

  return (
    <div className="rounded-xl border border-slate-200 bg-white p-4">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="text-sm font-semibold">优先级 {index + 1}</p>
          <p className="mt-0.5 text-xs text-slate-500">
            当前 Route 失败后自动尝试下一条。
          </p>
        </div>
        <div className="flex items-center gap-1.5">
          <label className="mr-2 flex items-center gap-2 text-xs text-slate-600">
            <input
              type="checkbox"
              className="size-4 accent-sky-700"
              checked={route.enabled}
              onChange={(event) =>
                onChange({ ...route, enabled: event.target.checked })
              }
            />
            启用
          </label>
          <Button
            aria-label="上移模型 Route"
            disabled={index === 0}
            onClick={() => onMove(index, index - 1)}
            className="px-2"
          >
            <ArrowUp className="size-4" />
          </Button>
          <Button
            aria-label="下移模型 Route"
            disabled={index === total - 1}
            onClick={() => onMove(index, index + 1)}
            className="px-2"
          >
            <ArrowDown className="size-4" />
          </Button>
          <Button
            aria-label="删除模型 Route"
            onClick={onRemove}
            className="px-2"
          >
            <Trash2 className="size-4" />
          </Button>
        </div>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Provider">
          <select
            className={selectClass}
            value={route.provider_type}
            onChange={(event) => setProvider(event.target.value as Provider)}
          >
            <option value="openai_compatible">{names.openai_compatible}</option>
            <option value="chatgpt">{names.chatgpt}</option>
            <option value="grok">{names.grok}</option>
          </select>
        </Field>

        {subscription && (
          <Field label="账号">
            <select
              className={selectClass}
              value={route.credential_id}
              onChange={(event) =>
                onChange({
                  ...route,
                  credential_id: event.target.value,
                  model: "",
                })
              }
            >
              <option value="">选择已连接账号</option>
              {accounts.map((account) => (
                <option key={account.id} value={account.id}>
                  {account.display_label}
                </option>
              ))}
            </select>
          </Field>
        )}

        {!subscription && (
          <>
            <Field label="OpenAI-compatible Base URL">
              <TextInput
                value={route.provider_base_url}
                onChange={(event) =>
                  onChange({
                    ...route,
                    provider_base_url: event.target.value,
                  })
                }
                placeholder="https://api.example.com/v1"
              />
            </Field>
            <Field label="API Key 环境变量名称">
              <TextInput
                value={route.api_key_env}
                onChange={(event) =>
                  onChange({ ...route, api_key_env: event.target.value })
                }
                placeholder="CYCLEDOCK_MODEL_API_KEY"
              />
            </Field>
          </>
        )}

        <Field label="模型">
          {subscription && !!catalog.data?.length ? (
            <select
              className={selectClass}
              value={route.model}
              onChange={(event) =>
                onChange({ ...route, model: event.target.value })
              }
            >
              <option value="">选择模型</option>
              {catalog.data.map((model) => (
                <option key={model.id} value={model.id}>
                  {model.name}
                </option>
              ))}
            </select>
          ) : (
            <TextInput
              disabled={subscription && !route.credential_id}
              value={route.model}
              onChange={(event) =>
                onChange({ ...route, model: event.target.value })
              }
              placeholder={
                subscription && !route.credential_id ? "先选择账号" : "模型 ID"
              }
            />
          )}
        </Field>
      </div>

      {catalog.isError && (
        <p className="mt-2 text-xs text-amber-600">
          无法获取模型目录，可手动输入模型 ID。
        </p>
      )}

      <div className="mt-4">
        <Button
          disabled={
            test.isPending ||
            !route.model.trim() ||
            (subscription && !route.credential_id)
          }
          onClick={() => test.mutate()}
        >
          <TestTube2 className="size-4" />
          测试此 Route
        </Button>
      </div>
    </div>
  );
}

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

  const save = useMutation({
    mutationFn: () => {
      if (!draft) throw Error("未加载配置");
      const { configured: unused, source_id: unusedSource, ...input } = draft;
      void unused;
      void unusedSource;
      return api.saveAgent(input);
    },
    onSuccess: (result) => {
      queryClient.setQueryData(keys.agent, result);
      setDraft(result);
      toast.success("模型路由已保存");
    },
    onError: (error) => toast.error(String(error)),
  });

  const chatgpt = useMutation({
    mutationFn: api.chatgptStart,
    onSuccess: (result) => {
      if (result.manual_callback) {
        setAuthorizationURL(result.authorization_url);
        setManualURL("");
      } else {
        window.location.assign(result.authorization_url);
      }
    },
    onError: (error) => toast.error(String(error)),
  });
  const complete = useMutation({
    mutationFn: () => api.chatgptComplete(manualURL.trim()),
    onSuccess: async () => {
      setAuthorizationURL("");
      setManualURL("");
      await queryClient.invalidateQueries({ queryKey: keys.auth });
      toast.success("ChatGPT 账号已连接");
    },
    onError: (error) => toast.error(String(error)),
  });
  const grok = useMutation({
    mutationFn: api.grokStart,
    onSuccess: (result) => {
      setFlow({
        id: result.flow_id,
        code: result.user_code,
        url: result.verification_url,
      });
      window.open(result.verification_url, "_blank", "noopener,noreferrer");
    },
    onError: (error) => toast.error(String(error)),
  });
  const poll = useMutation({
    mutationFn: (id: string) => api.grokPoll(id),
    onSuccess: async (result) => {
      if (result.status === "connected") {
        setFlow(null);
        await queryClient.invalidateQueries({ queryKey: keys.auth });
        toast.success("Grok 账号已连接");
      } else {
        toast.message("等待 xAI 账号授权");
      }
    },
    onError: (error) => toast.error(String(error)),
  });
  const disconnect = useMutation({
    mutationFn: ({
      provider,
      credentialID,
    }: {
      provider: "chatgpt" | "grok";
      credentialID: string;
    }) => api.modelDisconnect(provider, credentialID),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.auth });
      toast.success("账号连接已断开");
    },
    onError: (error) => toast.error(String(error)),
  });

  if (settings.isPending || auth.isPending)
    return <Spinner text="加载模型配置…" />;
  if (settings.error || auth.error)
    return <ErrorView error={settings.error || auth.error} />;
  if (!draft) return null;

  const statuses = auth.data ?? [];
  const moveRoute = (from: number, to: number) => {
    if (to < 0 || to >= draft.routes.length) return;
    const routes = [...draft.routes];
    const [route] = routes.splice(from, 1);
    routes.splice(to, 0, route);
    setDraft({ ...draft, routes });
  };
  const updateRoute = (index: number, route: ModelRoute) => {
    const routes = [...draft.routes];
    routes[index] = route;
    setDraft({ ...draft, routes });
  };
  const usedCredentialIDs = new Set(
    draft.routes.map((route) => route.credential_id).filter(Boolean),
  );

  return (
    <div className="space-y-5">
      <Section
        title="模型账号"
        description="ChatGPT 与 Grok 可以连接多个账号。重新连接同一账号会更新原账号，不会重复创建。"
      >
        <div className="grid gap-4 lg:grid-cols-2">
          {(["chatgpt", "grok"] as const).map((provider) => {
            const status = statuses.find((item) => item.provider === provider);
            const accounts = status?.accounts ?? [];
            return (
              <div
                key={provider}
                className="rounded-xl border border-slate-200 bg-white p-4"
              >
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <p className="text-sm font-semibold">{names[provider]}</p>
                    <p className="mt-1 text-xs text-slate-500">
                      {accounts.length
                        ? "已连接 " + accounts.length + " 个账号"
                        : status?.available
                          ? "尚未连接账号"
                          : "当前部署未开放"}
                    </p>
                    {status?.warning && (
                      <p className="mt-2 text-xs text-amber-700">
                        Hosted DEV 临时登录测试模式；正式使用仍需要批准的 OAuth
                        Client ID。
                      </p>
                    )}
                  </div>
                  {status?.available && (
                    <Button
                      disabled={chatgpt.isPending || grok.isPending}
                      onClick={() =>
                        provider === "chatgpt"
                          ? chatgpt.mutate()
                          : grok.mutate()
                      }
                    >
                      <Plus className="size-4" />
                      添加账号
                    </Button>
                  )}
                </div>

                <div className="mt-4 space-y-2">
                  {accounts.map((account) => {
                    const inUse = usedCredentialIDs.has(account.id);
                    return (
                      <div
                        key={account.id}
                        className="flex items-center justify-between gap-3 rounded-lg bg-slate-50 px-3 py-2"
                      >
                        <div className="min-w-0">
                          <p className="truncate text-sm font-medium">
                            {account.display_label}
                          </p>
                          <p className="text-[11px] text-slate-500">
                            {inUse ? "已被模型 Route 使用" : "可用于模型 Route"}
                          </p>
                        </div>
                        <Button
                          className="px-2"
                          disabled={disconnect.isPending || inUse}
                          title={
                            inUse
                              ? "先从模型 Route 中移除并保存，再断开账号"
                              : "断开账号"
                          }
                          onClick={() =>
                            disconnect.mutate({
                              provider,
                              credentialID: account.id,
                            })
                          }
                        >
                          <LogOut className="size-4" />
                        </Button>
                      </div>
                    );
                  })}
                </div>

                {provider === "chatgpt" && authorizationURL && (
                  <div className="mt-4 space-y-3 rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs">
                    <p>
                      仅限 DEV：完成 OpenAI 登录后，把最终 localhost callback
                      URL 粘贴到这里。
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
                      onChange={(event) => setManualURL(event.target.value)}
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
                  <div className="mt-4 space-y-2 rounded-lg bg-sky-50 p-3 text-sm">
                    <p>
                      在 xAI 授权页输入代码：
                      <strong className="ml-1 font-mono">{flow.code}</strong>
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
            );
          })}
        </div>
      </Section>

      <Section
        title="模型路由"
        description="按优先级从上到下尝试。只有模型请求本身出现可恢复故障时才切换；MCP 或任务状态错误不会换模型。"
      >
        <div className="space-y-3">
          {draft.routes.length === 0 && (
            <div className="rounded-xl border border-dashed border-slate-300 px-4 py-8 text-center text-sm text-slate-500">
              还没有模型 Route。添加第一条后即可配置 Cycle Agent。
            </div>
          )}
          {draft.routes.map((route, index) => (
            <RouteEditor
              key={route.id || String(index)}
              route={route}
              index={index}
              total={draft.routes.length}
              auth={statuses}
              timeout={draft.request_timeout_ms}
              onChange={(next) => updateRoute(index, next)}
              onMove={moveRoute}
              onRemove={() =>
                setDraft({
                  ...draft,
                  routes: draft.routes.filter((_, i) => i !== index),
                })
              }
            />
          ))}
        </div>

        <div className="mt-4 flex flex-wrap gap-2">
          <Button
            onClick={() =>
              setDraft({ ...draft, routes: [...draft.routes, newRoute()] })
            }
          >
            <Plus className="size-4" />
            添加 Route
          </Button>
          <Button
            variant="primary"
            onClick={() => save.mutate()}
            disabled={save.isPending}
          >
            保存模型路由
          </Button>
        </div>
      </Section>
    </div>
  );
}
