import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  Bot,
  ExternalLink,
  GripVertical,
  LogOut,
  Plus,
  TestTube2,
  Trash2,
} from "lucide-react";
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
  useSortable,
  arrayMove,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
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
  Checkbox,
  ErrorView,
  Field,
  Modal,
  Section,
  Select,
  Spinner,
  TextInput,
} from "../../shared/ui/components";

const names: Record<Provider, string> = {
  chatgpt: "ChatGPT",
  grok: "Grok",
  openai_compatible: "自定义 API",
};

function OpenAILogo({ className = "size-4" }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      className={className}
      aria-hidden="true"
    >
      <path d="M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.0729zm-9.022 12.6081a4.4755 4.4755 0 0 1-2.8764-1.0408l.1419-.0804 4.7783-2.7582a.7948.7948 0 0 0 .3927-.6813v-6.7369l2.02 1.1686a.071.071 0 0 1 .038.052v5.5826a4.504 4.504 0 0 1-4.4945 4.4944zm-9.6607-4.1254a4.4708 4.4708 0 0 1-.5346-3.0137l.142.0852 4.783 2.7582a.7712.7712 0 0 0 .7806 0l5.8428-3.3685v2.3324a.0804.0804 0 0 1-.0332.0615L9.74 19.9502a4.4992 4.4992 0 0 1-6.1408-1.6464zM2.3408 7.8956a4.485 4.485 0 0 1 2.3655-1.9728V11.6a.7664.7664 0 0 0 .3879.6765l5.8144 3.3543-2.0201 1.1685a.0757.0757 0 0 1-.071 0l-4.8303-2.7865A4.504 4.504 0 0 1 2.3408 7.872zm16.5963 3.8558L13.1038 8.364 15.1192 7.2a.0757.0757 0 0 1 .071 0l4.8303 2.7913a4.4944 4.4944 0 0 1-.6765 8.1042v-5.6772a.79.79 0 0 0-.407-.667zm2.0107-3.0231l-.142-.0852-4.7735-2.7818a.7759.7759 0 0 0-.7854 0L9.409 9.2297V6.8974a.0662.0662 0 0 1 .0284-.0615l4.8303-2.7866a4.4992 4.4992 0 0 1 6.6802 4.66zM8.3065 12.863l-2.02-1.1638a.0804.0804 0 0 1-.038-.0567V6.0742a4.4992 4.4992 0 0 1 7.3757-3.4537l-.142.0805L8.704 5.459a.7948.7948 0 0 0-.3927.6813zm1.0976-2.3654l2.602-1.4998 2.6069 1.4998v2.9994l-2.5974 1.4997-2.6067-1.4997Z" />
    </svg>
  );
}

function GrokLogo({ className = "size-4" }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      className={className}
      aria-hidden="true"
    >
      <path d="M6.469 8.776L16.512 23h-4.464L2.005 8.776H6.47zm-.004 7.9l2.233 3.164L6.467 23H2l4.465-6.324zM22 2.582V23h-3.659V7.764L22 2.582zM22 1l-9.952 14.095-2.233-3.163L17.533 1H22z" />
    </svg>
  );
}

function ProviderLogo({
  provider,
  className = "size-4",
}: {
  provider: Provider;
  className?: string;
}) {
  if (provider === "chatgpt") {
    return <OpenAILogo className={className} />;
  }
  if (provider === "grok") {
    return <GrokLogo className={className} />;
  }
  return <Bot className={className} />;
}

function providerAccounts(auth: ModelAuth[], provider: Provider) {
  if (provider === "openai_compatible") return [];
  return auth.find((item) => item.provider === provider)?.accounts ?? [];
}

function SortableRouteEditor({
  route,
  auth,
  onChange,
  onRemove,
}: {
  route: ModelRoute;
  auth: ModelAuth[];
  onChange: (route: ModelRoute) => void;
  onRemove: () => void;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: route.id });

  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  const subscription = route.provider_type !== "openai_compatible";
  const accounts = providerAccounts(auth, route.provider_type);
  const currentAccount = accounts.find((a) => a.id === route.credential_id);
  const accountLabel =
    currentAccount?.display_label || route.credential_id || "未指定账号";

  const catalog = useQuery({
    queryKey: keys.models(route.provider_type, route.credential_id),
    queryFn: () =>
      api.models(
        route.provider_type as "chatgpt" | "grok",
        route.credential_id,
      ),
    enabled: subscription && !!route.credential_id,
  });

  // 当账号模型目录加载完成且当前未选择模型时，默认选中第一项
  useEffect(() => {
    if (subscription && !route.model && catalog.data && catalog.data.length > 0) {
      onChange({
        ...route,
        model: catalog.data[0].id,
      });
    }
  }, [subscription, route, catalog.data, onChange]);

  const test = useMutation({
    mutationFn: () =>
      api.testProvider({
        provider_type: route.provider_type,
        credential_id: route.credential_id,
        provider_base_url: route.provider_base_url,
        model: route.model,
        api_key_env: route.api_key_env,
      }),
    onSuccess: (result) =>
      toast[result.ok ? "success" : "warning"](result.message),
    onError: (error) => toast.error(String(error)),
  });

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={`rounded-xl border border-line bg-surface p-3.5 space-y-3 transition-all ${
        isDragging ? "z-10 opacity-70 shadow-lg ring-2 ring-primary/40" : ""
      }`}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <button
            type="button"
            className="cursor-grab active:cursor-grabbing rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground touch-none"
            aria-label="拖拽调整优先级"
            title="拖拽调整优先级"
            {...attributes}
            {...listeners}
          >
            <GripVertical className="size-4" />
          </button>
          <div className="flex items-center gap-1.5">
            <ProviderLogo provider={route.provider_type} className="size-4 shrink-0 text-foreground" />
            <p className="text-sm font-semibold">{names[route.provider_type]}</p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button
            className="h-7 px-2.5 text-xs"
            disabled={
              test.isPending ||
              !route.model.trim() ||
              (subscription && !route.credential_id)
            }
            onClick={() => test.mutate()}
            title="测试此 Route"
          >
            <TestTube2 className="size-3.5" />
            测试此 Route
          </Button>
          <label className="flex cursor-pointer items-center gap-1.5 text-xs text-secondary-foreground">
            <Checkbox
              checked={route.enabled}
              onCheckedChange={(checked) =>
                onChange({ ...route, enabled: Boolean(checked) })
              }
            />
            启用
          </label>
          <Button
            aria-label="删除模型 Route"
            onClick={onRemove}
            className="h-7 px-2"
            title="删除此 Route"
          >
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      </div>

      {subscription ? (
        <div className="grid gap-3 sm:grid-cols-2">
          <div
            className="flex h-9 w-full items-center rounded-md border border-line bg-muted/40 px-3 text-[13px] font-medium text-foreground select-none truncate"
            title={accountLabel}
            aria-label="账号"
          >
            {accountLabel}
          </div>

          {catalog.data?.length ? (
            <Select
              value={route.model}
              onChange={(event) =>
                onChange({ ...route, model: event.target.value })
              }
              aria-label="模型"
            >
              <option value="">选择模型</option>
              {catalog.data.map((model) => (
                <option key={model.id} value={model.id}>
                  {model.name}
                </option>
              ))}
            </Select>
          ) : (
            <TextInput
              disabled={!route.credential_id}
              value={route.model}
              onChange={(event) =>
                onChange({ ...route, model: event.target.value })
              }
              placeholder={
                !route.credential_id ? "先选择账号" : "模型 ID"
              }
              aria-label="模型"
            />
          )}
        </div>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          <div
            className="flex h-9 w-full items-center rounded-md border border-line bg-muted/40 px-3 text-[13px] font-medium text-foreground select-none truncate"
            title={`${route.provider_base_url || "未设置 Base URL"}${route.api_key_env ? ` (环境变量: ${route.api_key_env})` : ""}`}
            aria-label="服务商 Endpoint"
          >
            {route.provider_base_url || "未设置 Base URL"}
          </div>

          <TextInput
            value={route.model}
            onChange={(event) =>
              onChange({ ...route, model: event.target.value })
            }
            placeholder="输入模型 ID，如 deepseek-chat"
            aria-label="模型"
          />
        </div>
      )}

      {catalog.isError && (
        <p className="text-xs text-warning">
          无法获取模型目录，可手动输入模型 ID。
        </p>
      )}
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

  // 如果未配置过路由或有已登录账号尚未在路由中，默认展示已连接账号的默认模型路由
  const baseSettings = useMemo(() => {
    if (!settings.data) return null;

    const currentRoutes = [...settings.data.routes];
    const existingCredentialIDs = new Set(
      currentRoutes.map((r) => r.credential_id).filter(Boolean),
    );

    const missingAccounts = (auth.data ?? []).flatMap((status) =>
      status.accounts
        .filter((account) => !existingCredentialIDs.has(account.id))
        .map((account) => ({
          provider: status.provider,
          account,
        })),
    );

    if (missingAccounts.length === 0) return settings.data;

    const autoRoutes: ModelRoute[] = missingAccounts.map(
      ({ provider, account }) => {
        const cached = queryClient.getQueryData<{ id: string; name: string }[]>(
          keys.models(provider, account.id),
        );
        return {
          id: crypto.randomUUID(),
          enabled: true,
          provider_type: provider,
          credential_id: account.id,
          provider_base_url: "",
          model: cached?.[0]?.id ?? "",
          api_key_env: "",
        };
      },
    );

    return {
      ...settings.data,
      routes: [...currentRoutes, ...autoRoutes],
    };
  }, [settings.data, auth.data]);

  const draft = override ?? baseSettings;
  const [manualURL, setManualURL] = useState("");
  const [authorizationURL, setAuthorizationURL] = useState("");
  const [flow, setFlow] = useState<{
    id: string;
    code: string;
    url: string;
  } | null>(null);

  // 预拉取已连接账号的模型目录，确保点击加号或自动添加时能立即获取默认模型
  useEffect(() => {
    if (!auth.data) return;
    for (const status of auth.data) {
      for (const account of status.accounts) {
        void queryClient.prefetchQuery({
          queryKey: keys.models(status.provider, account.id),
          queryFn: () => api.models(status.provider, account.id),
        });
      }
    }
  }, [auth.data]);

  const addRouteForAccount = (
    provider: "chatgpt" | "grok",
    credentialID: string,
  ) => {
    if (!draft) return;
    const cached = queryClient.getQueryData<{ id: string; name: string }[]>(
      keys.models(provider, credentialID),
    );
    const defaultModel = cached?.[0]?.id ?? "";
    const newRouteItem: ModelRoute = {
      id: crypto.randomUUID(),
      enabled: true,
      provider_type: provider,
      credential_id: credentialID,
      provider_base_url: "",
      model: defaultModel,
      api_key_env: "",
    };
    setDraft({
      ...draft,
      routes: [...draft.routes, newRouteItem],
    });
    if (!defaultModel) {
      void queryClient.prefetchQuery({
        queryKey: keys.models(provider, credentialID),
        queryFn: () => api.models(provider, credentialID),
      });
    }
    toast.success("已添加模型 Route");
  };

  const [addingOpenAI, setAddingOpenAI] = useState(false);
  const [newBaseURL, setNewBaseURL] = useState("");
  const [newAPIKeyEnv, setNewAPIKeyEnv] = useState("");

  const configuredOpenAIProviders = useMemo(() => {
    const map = new Map<
      string,
      { baseURL: string; apiKeyEnv: string; routeCount: number }
    >();
    for (const route of draft?.routes ?? []) {
      if (route.provider_type === "openai_compatible") {
        const key = route.provider_base_url.trim();
        if (!key) continue;
        const existing = map.get(key);
        if (existing) {
          existing.routeCount += 1;
        } else {
          map.set(key, {
            baseURL: key,
            apiKeyEnv: route.api_key_env.trim(),
            routeCount: 1,
          });
        }
      }
    }
    return Array.from(map.values());
  }, [draft?.routes]);

  const addRouteForOpenAIProvider = (baseURL: string, apiKeyEnv: string) => {
    if (!draft) return;
    const newRouteItem: ModelRoute = {
      id: crypto.randomUUID(),
      enabled: true,
      provider_type: "openai_compatible",
      credential_id: "",
      provider_base_url: baseURL,
      model: "",
      api_key_env: apiKeyEnv,
    };
    setDraft({
      ...draft,
      routes: [...draft.routes, newRouteItem],
    });
    toast.success("已添加模型 Route，请在下方填写模型 ID");
  };

  const sensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: {
        distance: 5,
      },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );

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
      const updatedAuth = await queryClient.fetchQuery({
        queryKey: keys.auth,
        queryFn: api.authStatuses,
      });
      const currentCredentialIDs = new Set(
        (draft?.routes ?? []).map((r) => r.credential_id),
      );
      const chatgptAccounts =
        updatedAuth.find((item) => item.provider === "chatgpt")?.accounts ?? [];
      const newAccounts = chatgptAccounts.filter(
        (acc) => !currentCredentialIDs.has(acc.id),
      );
      if (newAccounts.length > 0 && draft) {
        const appendedRoutes: ModelRoute[] = newAccounts.map((account) => {
          const cached = queryClient.getQueryData<{ id: string; name: string }[]>(
            keys.models("chatgpt", account.id),
          );
          return {
            id: crypto.randomUUID(),
            enabled: true,
            provider_type: "chatgpt",
            credential_id: account.id,
            provider_base_url: "",
            model: cached?.[0]?.id ?? "",
            api_key_env: "",
          };
        });
        setDraft({
          ...draft,
          routes: [...draft.routes, ...appendedRoutes],
        });
        for (const acc of newAccounts) {
          void queryClient.prefetchQuery({
            queryKey: keys.models("chatgpt", acc.id),
            queryFn: () => api.models("chatgpt", acc.id),
          });
        }
      }
      toast.success("ChatGPT 账号已连接并已添加到模型路由");
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
        const updatedAuth = await queryClient.fetchQuery({
          queryKey: keys.auth,
          queryFn: api.authStatuses,
        });
        const currentCredentialIDs = new Set(
          (draft?.routes ?? []).map((r) => r.credential_id),
        );
        const grokAccounts =
          updatedAuth.find((item) => item.provider === "grok")?.accounts ?? [];
        const newAccounts = grokAccounts.filter(
          (acc) => !currentCredentialIDs.has(acc.id),
        );
        if (newAccounts.length > 0 && draft) {
          const appendedRoutes: ModelRoute[] = newAccounts.map((account) => {
            const cached = queryClient.getQueryData<{ id: string; name: string }[]>(
              keys.models("grok", account.id),
            );
            return {
              id: crypto.randomUUID(),
              enabled: true,
              provider_type: "grok",
              credential_id: account.id,
              provider_base_url: "",
              model: cached?.[0]?.id ?? "",
              api_key_env: "",
            };
          });
          setDraft({
            ...draft,
            routes: [...draft.routes, ...appendedRoutes],
          });
          for (const acc of newAccounts) {
            void queryClient.prefetchQuery({
              queryKey: keys.models("grok", acc.id),
              queryFn: () => api.models("grok", acc.id),
            });
          }
        }
        toast.success("Grok 账号已连接并已添加到模型路由");
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

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    const oldIndex = draft.routes.findIndex((r) => r.id === active.id);
    const newIndex = draft.routes.findIndex((r) => r.id === over.id);
    if (oldIndex !== -1 && newIndex !== -1) {
      setDraft({ ...draft, routes: arrayMove(draft.routes, oldIndex, newIndex) });
    }
  };

  const updateRoute = (index: number, route: ModelRoute) => {
    const routes = [...draft.routes];
    routes[index] = route;
    setDraft({ ...draft, routes });
  };

  const removeRoute = (index: number) => {
    setDraft({
      ...draft,
      routes: draft.routes.filter((_, i) => i !== index),
    });
  };

  const routeIds = draft.routes.map((route, i) => route.id || String(i));

  return (
    <div className="space-y-5">
      <Section
        title="模型账号与服务"
        description="支持 ChatGPT 与 Grok 订阅账号连接，也支持直接添加 OpenAI 兼容的自定义 API 服务。"
      >
        <div className="grid gap-4 lg:grid-cols-3">
          {(["chatgpt", "grok"] as const).map((provider) => {
            const status = statuses.find((item) => item.provider === provider);
            const accounts = status?.accounts ?? [];
            return (
              <div
                key={provider}
                className="flex flex-col justify-between rounded-xl border border-line bg-surface p-4"
              >
                <div>
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <div className="flex items-center gap-1.5">
                        <ProviderLogo provider={provider} className="size-4 shrink-0 text-foreground" />
                        <p className="text-sm font-semibold">{names[provider]}</p>
                      </div>
                      <p className="mt-1 text-xs text-muted-foreground">
                        {accounts.length
                          ? "已连接 " + accounts.length + " 个账号"
                          : status?.available
                            ? "尚未连接账号"
                            : "当前部署未开放"}
                      </p>
                      {status?.warning && (
                        <p className="mt-2 text-xs text-warning">
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
                      const routeCount = draft.routes.filter(
                        (route) => route.credential_id === account.id,
                      ).length;
                      const inUse = routeCount > 0;
                      return (
                        <div
                          key={account.id}
                          className="flex items-center justify-between gap-3 rounded-lg bg-canvas px-3 py-2"
                        >
                          <div className="min-w-0">
                            <p className="truncate text-sm font-medium">
                              {account.display_label}
                            </p>
                            <p className="text-[11px] text-muted-foreground">
                              {routeCount > 0
                                ? `已配置 ${routeCount} 条模型 Route`
                                : "可用于模型 Route"}
                            </p>
                          </div>
                          <div className="flex items-center gap-1.5">
                            <Button
                              className="px-2"
                              title="为此账号再添加一条模型 Route"
                              aria-label="为此账号再添加一条模型 Route"
                              onClick={() =>
                                addRouteForAccount(provider, account.id)
                              }
                            >
                              <Plus className="size-4" />
                            </Button>
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
                        </div>
                      );
                    })}
                  </div>
                </div>

                {provider === "chatgpt" && authorizationURL && (
                  <div className="mt-4 space-y-3 rounded-lg border border-warning-line bg-warning-muted p-3 text-xs">
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

          {/* 第三个并列卡片：自定义 OpenAI 的 API */}
          <div className="flex flex-col justify-between rounded-xl border border-line bg-surface p-4">
            <div>
              <div className="flex items-start justify-between gap-3">
                <div>
                  <div className="flex items-center gap-1.5">
                    <ProviderLogo provider="openai_compatible" className="size-4 shrink-0 text-foreground" />
                    <p className="text-sm font-semibold">{names.openai_compatible}</p>
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {configuredOpenAIProviders.length
                      ? `已配置 ${configuredOpenAIProviders.length} 个服务商`
                      : "自建 API、DeepSeek、Ollama 等"}
                  </p>
                </div>
                <Button onClick={() => setAddingOpenAI(true)}>
                  <Plus className="size-4" />
                  添加 API
                </Button>
              </div>

              <div className="mt-4 space-y-2">
                {configuredOpenAIProviders.length === 0 ? (
                  <div className="rounded-lg bg-canvas px-3 py-2.5 text-xs text-muted-foreground">
                    点击右上角“添加 API”填写服务商 Base URL 与环境变量，再在服务商旁点击加号添加模型 Route。
                  </div>
                ) : (
                  configuredOpenAIProviders.map((item) => (
                    <div
                      key={item.baseURL}
                      className="flex items-center justify-between gap-3 rounded-lg bg-canvas px-3 py-2"
                    >
                      <div className="min-w-0">
                        <p className="truncate text-sm font-medium" title={item.baseURL}>
                          {item.baseURL}
                        </p>
                        <p className="truncate text-[11px] text-muted-foreground">
                          {item.apiKeyEnv ? `环境变量: ${item.apiKeyEnv} · ` : ""}
                          {item.routeCount > 0
                            ? `已配置 ${item.routeCount} 条模型 Route`
                            : "可用于模型 Route"}
                        </p>
                      </div>
                      <div className="flex items-center gap-1.5">
                        <Button
                          className="px-2"
                          title="为此服务商添加一条模型 Route"
                          aria-label="为此服务商添加一条模型 Route"
                          onClick={() =>
                            addRouteForOpenAIProvider(item.baseURL, item.apiKeyEnv)
                          }
                        >
                          <Plus className="size-4" />
                        </Button>
                        <Button
                          className="px-2"
                          title="删除此服务商及其所有路由"
                          aria-label="删除此服务商及其所有路由"
                          onClick={() => {
                            setDraft({
                              ...draft,
                              routes: draft.routes.filter(
                                (r) =>
                                  r.provider_type !== "openai_compatible" ||
                                  r.provider_base_url.trim() !== item.baseURL,
                              ),
                            });
                            toast.success("已移除该服务商及其模型 Route");
                          }}
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </div>
                    </div>
                  ))
                )}
              </div>
            </div>
          </div>
        </div>
      </Section>

      <Section
        title="模型路由"
        description="按优先级从上到下尝试。可直接拖拽调整顺序。只有模型请求本身出现可恢复故障时才切换；MCP 或任务状态错误不会换模型。"
      >
        {draft.routes.length === 0 ? (
          <div className="rounded-xl border border-dashed border-line-strong px-4 py-8 text-center text-sm text-muted-foreground">
            还没有模型 Route。请在上方卡片中添加账号或自定义 API。
          </div>
        ) : (
          <DndContext
            sensors={sensors}
            collisionDetection={closestCenter}
            onDragEnd={handleDragEnd}
          >
            <SortableContext
              items={routeIds}
              strategy={verticalListSortingStrategy}
            >
              <div className="space-y-3">
                {draft.routes.map((route, index) => (
                  <SortableRouteEditor
                    key={route.id || String(index)}
                    route={route}
                    auth={statuses}
                    onChange={(next) => updateRoute(index, next)}
                    onRemove={() => removeRoute(index)}
                  />
                ))}
              </div>
            </SortableContext>
          </DndContext>
        )}

        <div className="mt-4 flex flex-wrap gap-2">
          <Button
            variant="primary"
            onClick={() => save.mutate()}
            disabled={save.isPending}
          >
            保存模型路由
          </Button>
        </div>
      </Section>

      <Modal
        open={addingOpenAI}
        onOpenChange={(open) => {
          setAddingOpenAI(open);
          if (!open) {
            setNewBaseURL("");
            setNewAPIKeyEnv("");
          }
        }}
        title="配置服务商接口"
        description="配置兼容 OpenAI 规范的自定义模型服务商 Endpoint 与环境变量。"
      >
        <div className="space-y-4 pt-1">
          <Field label="Base URL">
            <TextInput
              value={newBaseURL}
              onChange={(e) => setNewBaseURL(e.target.value)}
              placeholder="Base URL，如 https://api.deepseek.com/v1"
            />
          </Field>
          <Field label="API Key 环境变量名">
            <TextInput
              value={newAPIKeyEnv}
              onChange={(e) => setNewAPIKeyEnv(e.target.value)}
              placeholder="API Key 环境变量名，如 DEEPSEEK_API_KEY"
            />
          </Field>
          <div className="flex justify-end gap-2 pt-2">
            <Button
              variant="ghost"
              onClick={() => {
                setAddingOpenAI(false);
                setNewBaseURL("");
                setNewAPIKeyEnv("");
              }}
            >
              取消
            </Button>
            <Button
              variant="primary"
              disabled={!newBaseURL.trim()}
              onClick={() => {
                const url = newBaseURL.trim();
                const env = newAPIKeyEnv.trim();
                addRouteForOpenAIProvider(url, env);
                setAddingOpenAI(false);
                setNewBaseURL("");
                setNewAPIKeyEnv("");
              }}
            >
              保存并添加 Route
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
