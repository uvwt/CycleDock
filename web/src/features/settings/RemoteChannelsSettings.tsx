import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowUpRight,
  BellRing,
  Bot,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Clock,
  Globe,
  LoaderCircle,
  MessageCircle,
  MessageSquare,
  Pencil,
  Plus,
  QrCode,
  Radio,
  RefreshCw,
  Send,
  ShieldCheck,
  Star,
  Trash2,
  Webhook,
} from "lucide-react";
import { QRCodeSVG } from "qrcode.react";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import type {
  ProjectPrimaryPushMode,
  ProjectPrimaryPushRoute,
  QRConnectSession,
  RemoteChannel,
  RemoteChannelGroupPolicy,
  RemoteChannelInput,
  RemoteChannelPlatform,
  RemoteChannelPushFilter,
  RemoteChannelRole,
  RemoteChannelRuntimeStatus,
  RemoteChannelTransport,
} from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import {
  Badge,
  Button,
  ConfirmDialog,
  ErrorView,
  Field,
  Modal,
  Section,
  Select,
  Spinner,
  Switch,
  TextArea,
  TextInput,
} from "../../shared/ui/components";

type PlatformMeta = {
  id: RemoteChannelPlatform;
  name: string;
  shortName: string;
  tagline: string;
  badgeColor: string;
  accentBg: string;
  defaultTransport: RemoteChannelTransport;
  supportsWebhook: boolean;
  supportsQRLogin: boolean;
  qrAppLabel: string;
  protocolHint: string;
};

const PLATFORMS: PlatformMeta[] = [
  {
    id: "weixin",
    name: "微信",
    shortName: "WX",
    tagline: "手机微信扫一扫直接连接，免公网回调长轮询收发",
    badgeColor: "text-emerald-700 dark:text-emerald-300",
    accentBg: "bg-emerald-500/12 border-emerald-500/25",
    defaultTransport: "long_poll",
    supportsWebhook: false,
    supportsQRLogin: true,
    qrAppLabel: "微信",
    protocolHint: "支持扫码一键接入 · iLink 长轮询",
  },
  {
    id: "wecom",
    name: "企业微信",
    shortName: "企微",
    tagline: "扫码一键创建智能机器人，或改用手动填写 / 群 Webhook",
    badgeColor: "text-sky-700 dark:text-sky-300",
    accentBg: "bg-sky-500/12 border-sky-500/25",
    defaultTransport: "websocket",
    supportsWebhook: true,
    supportsQRLogin: true,
    qrAppLabel: "企业微信",
    protocolHint: "支持扫码一键接入 · AI Bot WebSocket",
  },
  {
    id: "feishu",
    name: "飞书 / Lark",
    shortName: "飞书",
    tagline: "扫码一键创建机器人，支持飞书中国版与 Lark 国际版",
    badgeColor: "text-blue-700 dark:text-blue-300",
    accentBg: "bg-blue-500/12 border-blue-500/25",
    defaultTransport: "websocket",
    supportsWebhook: true,
    supportsQRLogin: true,
    qrAppLabel: "飞书 / Lark",
    protocolHint: "支持扫码一键接入 · WebSocket 长连接",
  },
  {
    id: "dingtalk",
    name: "钉钉",
    shortName: "钉钉",
    tagline: "钉钉扫码自动注册 Stream 长连接应用，免手填凭据",
    badgeColor: "text-indigo-700 dark:text-indigo-300",
    accentBg: "bg-indigo-500/12 border-indigo-500/25",
    defaultTransport: "stream",
    supportsWebhook: true,
    supportsQRLogin: true,
    qrAppLabel: "钉钉",
    protocolHint: "支持扫码一键接入 · Stream 长连接",
  },
  {
    id: "qq",
    name: "QQ 机器人",
    shortName: "QQ",
    tagline: "手机 QQ 扫码直接绑定机器人，支持单聊与群聊双向收发",
    badgeColor: "text-violet-700 dark:text-violet-300",
    accentBg: "bg-violet-500/12 border-violet-500/25",
    defaultTransport: "websocket",
    supportsWebhook: false,
    supportsQRLogin: true,
    qrAppLabel: "手机 QQ",
    protocolHint: "支持扫码一键接入 · Bot WebSocket",
  },
  {
    id: "telegram",
    name: "Telegram",
    shortName: "TG",
    tagline: "只需填写 1 个 Bot Token，发一条消息即自动绑定会话",
    badgeColor: "text-cyan-700 dark:text-cyan-300",
    accentBg: "bg-cyan-500/12 border-cyan-500/25",
    defaultTransport: "long_poll",
    supportsWebhook: false,
    supportsQRLogin: false,
    qrAppLabel: "Telegram",
    protocolHint: "极简单输入框接入 · 支持 Topics 与代理",
  },
];

const PLATFORM_MAP = Object.fromEntries(
  PLATFORMS.map((p) => [p.id, p]),
) as Record<RemoteChannelPlatform, PlatformMeta>;

const STATUS_META: Record<
  RemoteChannelRuntimeStatus,
  { label: string; dot: string; tone: string }
> = {
  connected: {
    label: "已连接",
    dot: "bg-emerald-500",
    tone: "text-emerald-700 dark:text-emerald-300 bg-emerald-500/10 border-emerald-500/20",
  },
  starting: {
    label: "连接中",
    dot: "bg-amber-500 animate-pulse",
    tone: "text-amber-700 dark:text-amber-300 bg-amber-500/10 border-amber-500/20",
  },
  reconnecting: {
    label: "重连中",
    dot: "bg-amber-500 animate-pulse",
    tone: "text-amber-700 dark:text-amber-300 bg-amber-500/10 border-amber-500/20",
  },
  error: {
    label: "异常",
    dot: "bg-rose-500",
    tone: "text-rose-700 dark:text-rose-300 bg-rose-500/10 border-rose-500/20",
  },
  disabled: {
    label: "已停用",
    dot: "bg-muted-foreground/50",
    tone: "text-muted-foreground bg-muted/60 border-line",
  },
};

type FormState = {
  id?: string;
  name: string;
  platform: RemoteChannelPlatform;
  role: RemoteChannelRole;
  transport: RemoteChannelTransport;
  enabled: boolean;
  project_id: string;
  target_card_id: string;
  push_filter: RemoteChannelPushFilter;
  mirror_web_messages: boolean;
  group_policy: RemoteChannelGroupPolicy;
  allow_from_text: string;
  // config
  base_url: string;
  domain: string;
  ws_url: string;
  bot_id: string;
  app_id: string;
  client_id: string;
  target_chat_id: string;
  target_chat_type: string;
  message_thread_id: string;
  proxy_url: string;
  weixin_user_id: string;
  // secrets (留空代表编辑时保持不变)
  bot_token: string;
  secret: string;
  app_secret: string;
  client_secret: string;
  webhook_url: string;
  webhook_secret: string;
  secret_hints: Record<string, string>;
};

type QRModalTarget = {
  platform: RemoteChannelPlatform;
  role: RemoteChannelRole;
  project_id: string;
  channel?: RemoteChannel;
};

function defaultTransportFor(
  platform: RemoteChannelPlatform,
  role: RemoteChannelRole,
  preferWebhook = false,
): RemoteChannelTransport {
  const meta = PLATFORM_MAP[platform];
  if ((preferWebhook || role === "scheduled_push") && meta.supportsWebhook) {
    return "webhook";
  }
  return meta.defaultTransport;
}

function defaultChannelName(
  platform: RemoteChannelPlatform,
  role: RemoteChannelRole,
): string {
  const meta = PLATFORM_MAP[platform];
  return role === "scheduled_push" ? `${meta.name} 定时推送` : meta.name;
}

function buildInitialForm(
  platform: RemoteChannelPlatform,
  role: RemoteChannelRole,
  defaultProjectID: string,
  existing?: RemoteChannel,
  preferWebhook = false,
): FormState {
  if (existing) {
    return {
      id: existing.id,
      name: existing.name,
      platform: existing.platform,
      role: existing.role,
      transport: existing.transport,
      enabled: existing.enabled,
      project_id: existing.project_id,
      target_card_id: existing.target_card_id || "",
      push_filter: existing.push_filter || "all",
      mirror_web_messages: existing.mirror_web_messages,
      group_policy: existing.group_policy || "mention_only",
      allow_from_text: (existing.allow_from ?? []).join("\n"),
      base_url: existing.config?.base_url ?? "",
      domain:
        existing.config?.domain ??
        (existing.platform === "feishu" ? "feishu" : ""),
      ws_url: existing.config?.ws_url ?? "",
      bot_id: existing.config?.bot_id ?? "",
      app_id: existing.config?.app_id ?? "",
      client_id: existing.config?.client_id ?? "",
      target_chat_id: existing.config?.target_chat_id ?? "",
      target_chat_type: existing.config?.target_chat_type ?? "",
      message_thread_id: existing.config?.message_thread_id
        ? String(existing.config.message_thread_id)
        : "",
      proxy_url: existing.config?.proxy_url ?? "",
      weixin_user_id: existing.config?.weixin_user_id ?? "",
      bot_token: "",
      secret: "",
      app_secret: "",
      client_secret: "",
      webhook_url: "",
      webhook_secret: "",
      secret_hints: existing.secret_hints ?? {},
    };
  }
  return {
    name: defaultChannelName(platform, role),
    platform,
    role,
    transport: defaultTransportFor(platform, role, preferWebhook),
    enabled: true,
    project_id: defaultProjectID,
    target_card_id: "",
    push_filter: "all",
    mirror_web_messages: false,
    group_policy: "mention_only",
    allow_from_text: "",
    base_url: "",
    domain: platform === "feishu" ? "feishu" : "",
    ws_url: "",
    bot_id: "",
    app_id: "",
    client_id: "",
    target_chat_id: "",
    target_chat_type: "",
    message_thread_id: "",
    proxy_url: "",
    weixin_user_id: "",
    bot_token: "",
    secret: "",
    app_secret: "",
    client_secret: "",
    webhook_url: "",
    webhook_secret: "",
    secret_hints: {},
  };
}

export function RemoteChannelsSettings() {
  const channelsQuery = useQuery({
    queryKey: keys.channels,
    queryFn: api.channels,
    refetchInterval: 10_000,
  });
  const projectsQuery = useQuery({
    queryKey: keys.projects,
    queryFn: api.projects,
  });

  const [roleFilter, setRoleFilter] = useState<"all" | RemoteChannelRole>(
    "all",
  );
  const [routeProjectID, setRouteProjectID] = useState<string>("");
  const [editorOpen, setEditorOpen] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [form, setForm] = useState<FormState | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<RemoteChannel | null>(null);
  const [testingID, setTestingID] = useState<string | null>(null);
  const [qrTarget, setQrTarget] = useState<QRModalTarget | null>(null);

  const selectedProjectID = form?.project_id || "";
  const cardsQuery = useQuery({
    queryKey: keys.cards(selectedProjectID),
    queryFn: () => api.cards(selectedProjectID),
    enabled: editorOpen && Boolean(selectedProjectID),
  });

  const ongoingCards = useMemo(
    () => (cardsQuery.data ?? []).filter((c) => c.kind === "ongoing"),
    [cardsQuery.data],
  );

  const projectsByID = useMemo(
    () =>
      Object.fromEntries((projectsQuery.data ?? []).map((p) => [p.id, p])),
    [projectsQuery.data],
  );

  const channels = useMemo(
    () => channelsQuery.data?.channels ?? [],
    [channelsQuery.data],
  );
  const primaryPushRoutes = useMemo(
    () => channelsQuery.data?.primary_push_routes ?? [],
    [channelsQuery.data],
  );
  const primaryRoutesByProject = useMemo(
    () =>
      Object.fromEntries(
        primaryPushRoutes.map((r) => [r.project_id, r]),
      ) as Record<string, ProjectPrimaryPushRoute>,
    [primaryPushRoutes],
  );

  const projects = useMemo(
    () => projectsQuery.data ?? [],
    [projectsQuery.data],
  );
  const activeRouteProjectID = useMemo(() => {
    if (routeProjectID && projects.some((p) => p.id === routeProjectID)) {
      return routeProjectID;
    }
    // 优先选中当前已配置了双向对话通道的项目，避免默认落在空白项目上导致控制条找不到已有通道
    const firstMainChatProjectID = channels.find(
      (c) =>
        c.role === "main_chat" && projects.some((p) => p.id === c.project_id),
    )?.project_id;
    if (firstMainChatProjectID) {
      return firstMainChatProjectID;
    }
    return projects[0]?.id ?? "";
  }, [projects, routeProjectID, channels]);

  const activeProjectMainChatChannels = useMemo(
    () =>
      channels.filter(
        (c) =>
          c.role === "main_chat" && c.project_id === activeRouteProjectID,
      ),
    [channels, activeRouteProjectID],
  );

  const activePrimaryRoute: ProjectPrimaryPushRoute | null = useMemo(() => {
    if (!activeRouteProjectID) {
      return null;
    }
    return (
      primaryRoutesByProject[activeRouteProjectID] ?? {
        project_id: activeRouteProjectID,
        mode: "last_active",
      }
    );
  }, [activeRouteProjectID, primaryRoutesByProject]);
  const filteredChannels = useMemo(
    () =>
      roleFilter === "all"
        ? channels
        : channels.filter((c) => c.role === roleFilter),
    [channels, roleFilter],
  );

  const mainChatCount = channels.filter((c) => c.role === "main_chat").length;
  const scheduledCount = channels.filter(
    (c) => c.role === "scheduled_push",
  ).length;

  const saveMutation = useMutation({
    mutationFn: async (state: FormState) => {
      const allowFrom = state.allow_from_text
        .split(/[\n,]+/)
        .map((s) => s.trim())
        .filter(Boolean);
      const threadNum = state.message_thread_id.trim()
        ? Number.parseInt(state.message_thread_id.trim(), 10)
        : 0;
      const finalName =
        state.name.trim() || defaultChannelName(state.platform, state.role);

      const payload: RemoteChannelInput = {
        name: finalName,
        platform: state.platform,
        role: state.role,
        transport: state.transport,
        enabled: state.enabled,
        project_id: state.project_id,
        target_card_id:
          state.role === "scheduled_push" && state.target_card_id
            ? state.target_card_id
            : null,
        push_filter:
          state.role === "scheduled_push" ? state.push_filter : "all",
        mirror_web_messages:
          state.role === "main_chat" ? state.mirror_web_messages : false,
        group_policy: state.group_policy,
        allow_from: allowFrom,
        config: {
          base_url: state.base_url.trim() || undefined,
          domain: state.domain.trim() || undefined,
          ws_url: state.ws_url.trim() || undefined,
          bot_id: state.bot_id.trim() || undefined,
          app_id: state.app_id.trim() || undefined,
          client_id: state.client_id.trim() || undefined,
          target_chat_id: state.target_chat_id.trim() || undefined,
          target_chat_type: state.target_chat_type.trim() || undefined,
          message_thread_id:
            Number.isFinite(threadNum) && threadNum > 0 ? threadNum : undefined,
          proxy_url: state.proxy_url.trim() || undefined,
          weixin_user_id: state.weixin_user_id.trim() || undefined,
        },
        secrets: {
          bot_token: state.bot_token.trim() || undefined,
          secret: state.secret.trim() || undefined,
          app_secret: state.app_secret.trim() || undefined,
          client_secret: state.client_secret.trim() || undefined,
          webhook_url: state.webhook_url.trim() || undefined,
          webhook_secret: state.webhook_secret.trim() || undefined,
        },
      };

      if (state.id) {
        return api.updateChannel(state.id, payload);
      }
      return api.createChannel(payload);
    },
    onSuccess: (saved) => {
      void queryClient.invalidateQueries({ queryKey: keys.channels });
      setEditorOpen(false);
      setForm(null);
      setAdvancedOpen(false);
      toast.success(form?.id ? "通道配置已更新" : "远程通道已创建并启动");
      if (PLATFORM_MAP[saved.platform].supportsQRLogin && !saved.has_secrets) {
        setQrTarget({
          platform: saved.platform,
          role: saved.role,
          project_id: saved.project_id,
          channel: saved,
        });
      }
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "保存通道失败");
    },
  });

  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      api.toggleChannel(id, enabled),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.channels });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "切换状态失败");
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deleteChannel(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.channels });
      setDeleteTarget(null);
      toast.success("已删除远程通道");
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "删除失败");
    },
  });

  const updatePrimaryRouteMutation = useMutation({
    mutationFn: ({
      projectID,
      mode,
      channelID,
    }: {
      projectID: string;
      mode: ProjectPrimaryPushMode;
      channelID?: string | null;
    }) =>
      api.updateProjectPrimaryPushRoute(projectID, {
        mode,
        channel_id: mode === "channel" ? (channelID ?? null) : null,
      }),
    onSuccess: (updated) => {
      void queryClient.invalidateQueries({ queryKey: keys.channels });
      if (updated.mode === "none") {
        toast.success("已切换为仅网页端接收主聊天与系统通知");
      } else if (updated.mode === "last_active") {
        toast.success("已切换为自动跟随最近活跃通道");
      } else if (updated.effective_channel_name) {
        toast.success(`已设「${updated.effective_channel_name}」为主推通道`);
      } else {
        toast.success("已更新项目主推通道配置");
      }
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "更新主推策略失败");
    },
  });

  const handleSelectPrimaryMode = (nextMode: ProjectPrimaryPushMode) => {
    if (!activeRouteProjectID) return;
    if (nextMode === "channel") {
      const preferred =
        activePrimaryRoute?.channel_id &&
        activeProjectMainChatChannels.some(
          (c) => c.id === activePrimaryRoute.channel_id,
        )
          ? activePrimaryRoute.channel_id
          : (activePrimaryRoute?.effective_channel_id &&
              activeProjectMainChatChannels.some(
                (c) => c.id === activePrimaryRoute.effective_channel_id,
              )
              ? activePrimaryRoute.effective_channel_id
              : (activeProjectMainChatChannels.find((c) => c.enabled)?.id ??
                activeProjectMainChatChannels[0]?.id));
      if (!preferred) {
        toast.info("当前项目暂无双向对话通道，请先接入一个双向通道");
        return;
      }
      updatePrimaryRouteMutation.mutate({
        projectID: activeRouteProjectID,
        mode: "channel",
        channelID: preferred,
      });
      return;
    }
    updatePrimaryRouteMutation.mutate({
      projectID: activeRouteProjectID,
      mode: nextMode,
      channelID: null,
    });
  };

  const handleSetChannelAsPrimary = (ch: RemoteChannel) => {
    setRouteProjectID(ch.project_id);
    updatePrimaryRouteMutation.mutate({
      projectID: ch.project_id,
      mode: "channel",
      channelID: ch.id,
    });
  };

  const handleTest = async (ch: RemoteChannel) => {
    setTestingID(ch.id);
    try {
      const res = await api.testChannel(ch.id);
      if (res.ok) {
        toast.success(res.message || "测试消息已发送");
      } else {
        toast.error(res.message || "测试失败");
      }
      void queryClient.invalidateQueries({ queryKey: keys.channels });
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "连接测试失败");
    } finally {
      setTestingID(null);
    }
  };

  // 点击平台按钮：5 个支持扫码的平台默认直接弹出「扫码一键连接」，Telegram 直接打开单输入框极简表单。
  const openConnect = (
    platform: RemoteChannelPlatform,
    role: RemoteChannelRole = "main_chat",
  ) => {
    const defaultProjectID = projectsQuery.data?.[0]?.id ?? "";
    const meta = PLATFORM_MAP[platform];
    if (meta.supportsQRLogin) {
      setQrTarget({
        platform,
        role,
        project_id: defaultProjectID,
      });
      return;
    }
    setForm(buildInitialForm(platform, role, defaultProjectID));
    setAdvancedOpen(false);
    setEditorOpen(true);
  };

  const openManualEditor = (
    platform: RemoteChannelPlatform,
    role: RemoteChannelRole,
    projectID: string,
    preferWebhook = false,
  ) => {
    setQrTarget(null);
    setForm(buildInitialForm(platform, role, projectID, undefined, preferWebhook));
    setAdvancedOpen(false);
    setEditorOpen(true);
  };

  const openEdit = (ch: RemoteChannel) => {
    setForm(buildInitialForm(ch.platform, ch.role, ch.project_id, ch));
    setAdvancedOpen(false);
    setEditorOpen(true);
  };

  if (channelsQuery.isPending || projectsQuery.isPending) {
    return (
      <Section title="远程通道">
        <Spinner text="正在加载远程通道配置…" />
      </Section>
    );
  }

  if (channelsQuery.error) {
    return (
      <Section title="远程通道">
        <ErrorView
          error={channelsQuery.error}
          onRetry={() => void channelsQuery.refetch()}
        />
      </Section>
    );
  }

  return (
    <div className="min-w-0 space-y-5">
      {/* 项目级「主聊天与系统通知推送」唯一主推控制条 */}
      {activePrimaryRoute && (
        <div
          data-testid="primary-push-route-bar"
          className="rounded-xl border border-line bg-surface p-4 shadow-xs"
        >
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex min-w-0 items-center gap-2">
                <span className="flex size-7 shrink-0 items-center justify-center rounded-lg border border-primary/25 bg-primary/10 text-primary">
                  <Radio className="size-3.5" />
                </span>
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 className="text-sm font-semibold text-heading">
                      主聊天与系统通知推送
                    </h3>
                    <Badge variant="outline" className="text-[11px]">
                      唯一主推防轰炸
                    </Badge>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    主聊天主动消息、系统提醒与安全审批挂起仅推往 1 个主力通道；各通道专属对话仍独立回复。
                  </p>
                </div>
              </div>
              {projects.length > 1 && (
                <div className="w-full sm:w-44">
                  <Select
                    aria-label="选择主推路由项目"
                    value={activeRouteProjectID}
                    onChange={(e) => setRouteProjectID(e.target.value)}
                  >
                    {projects.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name}
                      </option>
                    ))}
                  </Select>
                </div>
              )}
            </div>

            <div className="flex flex-col gap-2.5 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between">
              <div
                role="group"
                aria-label="主推路由模式"
                className="inline-flex w-full flex-wrap items-center gap-1 rounded-lg border border-line bg-muted/40 p-1 sm:w-auto"
              >
                {(
                  [
                    {
                      id: "last_active",
                      label: "自动跟随最近活跃",
                      icon: Radio,
                    },
                    {
                      id: "channel",
                      label: "指定主力通道",
                      icon: Star,
                    },
                    {
                      id: "none",
                      label: "仅网页端",
                      icon: Globe,
                    },
                  ] as const
                ).map((item) => {
                  const Icon = item.icon;
                  const selected = activePrimaryRoute.mode === item.id;
                  const disabled =
                    updatePrimaryRouteMutation.isPending ||
                    (item.id === "channel" &&
                      activeProjectMainChatChannels.length === 0);
                  return (
                    <button
                      key={item.id}
                      type="button"
                      data-testid={`primary-mode-${item.id}`}
                      disabled={disabled}
                      onClick={() => handleSelectPrimaryMode(item.id)}
                      className={`inline-flex flex-1 items-center justify-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-medium transition-colors sm:flex-none ${
                        selected
                          ? "bg-surface text-heading shadow-xs"
                          : "text-muted-foreground hover:text-heading"
                      } disabled:cursor-not-allowed disabled:opacity-50`}
                    >
                      <Icon className="size-3.5 shrink-0" />
                      <span className="truncate">{item.label}</span>
                    </button>
                  );
                })}
              </div>

              <div className="flex min-w-0 flex-1 items-center sm:justify-end">
                {activePrimaryRoute.mode === "channel" && (
                  <div className="w-full sm:max-w-xs">
                    <Select
                      aria-label="选择指定主力通道"
                      data-testid="primary-channel-select"
                      value={activePrimaryRoute.channel_id ?? ""}
                      disabled={
                        updatePrimaryRouteMutation.isPending ||
                        activeProjectMainChatChannels.length === 0
                      }
                      onChange={(e) => {
                        const nextID = e.target.value;
                        if (!nextID) return;
                        updatePrimaryRouteMutation.mutate({
                          projectID: activeRouteProjectID,
                          mode: "channel",
                          channelID: nextID,
                        });
                      }}
                    >
                      {!activePrimaryRoute.channel_id && (
                        <option value="">请选择主力双向通道（当前已静默）</option>
                      )}
                      {activeProjectMainChatChannels.map((ch) => (
                        <option key={ch.id} value={ch.id}>
                          {ch.name}
                          {!ch.enabled ? "（已停用）" : ""}
                        </option>
                      ))}
                    </Select>
                  </div>
                )}

                {activePrimaryRoute.mode === "last_active" && (
                  <div
                    data-testid="primary-last-active-hint"
                    className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground"
                  >
                    {activePrimaryRoute.effective_channel_id ? (
                      <>
                        <span>当前跟随：</span>
                        <Badge
                          variant="default"
                          className="gap-1 text-[11px]"
                        >
                          <Radio className="size-3" />
                          {activePrimaryRoute.effective_channel_name ||
                            activePrimaryRoute.effective_channel_id.slice(0, 8)}
                        </Badge>
                      </>
                    ) : (
                      <span>
                        暂无活跃通道，在任意已启用的双向通道发一条消息即可自动生效
                      </span>
                    )}
                  </div>
                )}

                {activePrimaryRoute.mode === "none" && (
                  <span
                    data-testid="primary-none-hint"
                    className="text-xs text-muted-foreground"
                  >
                    免打扰中：主聊天通知与审批仅在网页端 / Web Push 提醒
                  </span>
                )}
              </div>
            </div>
          </div>
        </div>
      )}

      {/* 顶部工作台总览与平台网格 */}
      <Section
        title="远程通道工作台"
        description="微信、企业微信、飞书、钉钉、QQ 支持手机扫码一键接入，免去手动填写 AppID / Secret / Chat ID；Telegram 仅需填写 Bot Token。"
      >
        <div className="space-y-4">
          {/* 快捷角色提示条 */}
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="flex items-start justify-between gap-3 rounded-xl border border-line bg-muted/35 p-3.5">
              <div className="min-w-0 space-y-1">
                <div className="flex items-center gap-2">
                  <MessageSquare className="size-4 shrink-0 text-primary" />
                  <span className="text-[13px] font-semibold text-heading">
                    双向对话（专属独立讨论）
                  </span>
                  <Badge variant="secondary">{mainChatCount}</Badge>
                </div>
                <p className="text-xs leading-5 text-muted-foreground">
                  每条通道自动拥有独立讨论卡，消息与子任务回调不混入项目主聊天；主聊天系统与待验收/审批提醒仍会单向推送。
                </p>
              </div>
            </div>

            <div className="flex items-start justify-between gap-3 rounded-xl border border-line bg-muted/35 p-3.5">
              <div className="min-w-0 space-y-1">
                <div className="flex items-center gap-2">
                  <BellRing className="size-4 shrink-0 text-amber-600 dark:text-amber-400" />
                  <span className="text-[13px] font-semibold text-heading">
                    定时任务独立推送
                  </span>
                  <Badge variant="secondary">{scheduledCount}</Badge>
                </div>
                <p className="text-xs leading-5 text-muted-foreground">
                  Ongoing 巡检汇报完成后直接推送到指定群或私聊，仅单向推送、不接收入站消息。
                </p>
              </div>
            </div>
          </div>

          {/* 6 大平台卡片网格 */}
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {PLATFORMS.map((p) => {
              const bound = channels.filter((c) => c.platform === p.id);
              const connectedCount = bound.filter(
                (c) => c.enabled && c.runtime_status === "connected",
              ).length;
              return (
                <div
                  key={p.id}
                  className="flex min-w-0 flex-col justify-between gap-3 rounded-xl border border-line bg-surface p-4 transition-colors hover:border-primary/40"
                >
                  <div className="min-w-0 space-y-2">
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex min-w-0 items-center gap-2.5">
                        <div
                          className={`flex size-9 shrink-0 items-center justify-center rounded-lg border text-xs font-bold ${p.accentBg} ${p.badgeColor}`}
                        >
                          {p.shortName}
                        </div>
                        <div className="min-w-0">
                          <h3 className="truncate text-sm font-semibold text-heading">
                            {p.name}
                          </h3>
                          <p className="truncate text-[11px] text-muted-foreground">
                            {p.protocolHint}
                          </p>
                        </div>
                      </div>
                      {bound.length > 0 && (
                        <Badge
                          variant="secondary"
                          className="shrink-0 text-[11px]"
                        >
                          {connectedCount}/{bound.length} 在线
                        </Badge>
                      )}
                    </div>
                    <p className="text-xs leading-5 text-muted-foreground">
                      {p.tagline}
                    </p>
                  </div>

                  <div className="flex flex-wrap items-center gap-2 pt-1">
                    <Button
                      variant="primary"
                      size="sm"
                      onClick={() => openConnect(p.id, "main_chat")}
                    >
                      {p.supportsQRLogin ? (
                        <QrCode className="size-3.5" />
                      ) : (
                        <Plus className="size-3.5" />
                      )}
                      {p.supportsQRLogin ? "扫码接入" : "配置接入"}
                    </Button>
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => openConnect(p.id, "scheduled_push")}
                    >
                      <Clock className="size-3.5" />
                      定时推送
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </Section>

      {/* 已配置的通道实例列表 */}
      <Section
        title="已配置的通道实例"
        description="支持多实例并行运行，可随时重新扫码换绑、测试发送或按需调整高级策略。"
      >
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-1.5 rounded-lg border border-line bg-muted/40 p-1">
              {(
                [
                  { id: "all", label: `全部 (${channels.length})` },
                  { id: "main_chat", label: `双向对话 (${mainChatCount})` },
                  {
                    id: "scheduled_push",
                    label: `定时推送 (${scheduledCount})`,
                  },
                ] as const
              ).map((tab) => (
                <button
                  key={tab.id}
                  type="button"
                  onClick={() => setRoleFilter(tab.id)}
                  className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                    roleFilter === tab.id
                      ? "bg-surface text-heading shadow-xs"
                      : "text-muted-foreground hover:text-heading"
                  }`}
                >
                  {tab.label}
                </button>
              ))}
            </div>
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <ShieldCheck className="size-3.5 text-emerald-600 dark:text-emerald-400" />
              AES-256-GCM 凭据加密 · 永不返回明文密钥
            </div>
          </div>

          {filteredChannels.length === 0 ? (
            <div className="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-line bg-muted/20 px-4 py-10 text-center">
              <MessageCircle className="size-8 text-muted-foreground/60" />
              <p className="text-sm font-medium text-heading">
                尚未配置远程通道
              </p>
              <p className="max-w-md text-xs text-muted-foreground">
                在上方平台卡片点击「扫码接入」，手机扫一扫即可完成绑定并立即上线。
              </p>
            </div>
          ) : (
            <div className="space-y-3">
              {filteredChannels.map((ch) => {
                const meta = PLATFORM_MAP[ch.platform];
                const status =
                  STATUS_META[ch.runtime_status] ?? STATUS_META.disabled;
                const project = projectsByID[ch.project_id];
                const projectRoute = primaryRoutesByProject[ch.project_id];
                const isEffectivePrimary =
                  ch.role === "main_chat" &&
                  projectRoute?.effective_channel_id === ch.id;
                const isConfiguredPrimaryDisabled =
                  ch.role === "main_chat" &&
                  projectRoute?.mode === "channel" &&
                  projectRoute?.channel_id === ch.id &&
                  !isEffectivePrimary;
                const canQuickSetPrimary =
                  ch.role === "main_chat" &&
                  ch.enabled &&
                  !(
                    projectRoute?.mode === "channel" &&
                    projectRoute?.channel_id === ch.id
                  );
                const secretEntries = Object.entries(ch.secret_hints ?? {});
                return (
                  <div
                    key={ch.id}
                    className="min-w-0 space-y-3 rounded-xl border border-line bg-surface p-4"
                  >
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="flex min-w-0 items-start gap-3">
                        <div
                          className={`mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-lg border text-xs font-bold ${meta.accentBg} ${meta.badgeColor}`}
                        >
                          {meta.shortName}
                        </div>
                        <div className="min-w-0 space-y-1">
                          <div className="flex flex-wrap items-center gap-2">
                            <span className="truncate text-sm font-semibold text-heading">
                              {ch.name}
                            </span>
                            <span
                              className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium ${status.tone}`}
                            >
                              <span
                                className={`size-1.5 rounded-full ${status.dot}`}
                              />
                              {status.label}
                            </span>
                            <Badge variant="secondary" className="text-[11px]">
                              {ch.role === "main_chat"
                                ? "双向对话"
                                : "定时推送"}
                            </Badge>
                            <Badge variant="outline" className="text-[11px]">
                              {transportLabel(ch.transport)}
                            </Badge>
                            {isEffectivePrimary && (
                              <Badge
                                variant="default"
                                data-testid={`primary-badge-${ch.id}`}
                                className="gap-1 bg-amber-500/15 text-[11px] text-amber-700 border-amber-500/30 dark:text-amber-300"
                              >
                                <Star className="size-3 fill-current" />
                                {projectRoute?.mode === "last_active"
                                  ? "当前活跃主推"
                                  : "主推"}
                              </Badge>
                            )}
                            {isConfiguredPrimaryDisabled && (
                              <Badge
                                variant="outline"
                                className="gap-1 border-amber-500/35 text-[11px] text-amber-700 dark:text-amber-300"
                              >
                                已设主推（停用中）
                              </Badge>
                            )}
                          </div>

                          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                            <span>
                              项目：{project ? project.name : ch.project_id.slice(0, 8)}
                            </span>
                            {ch.role === "main_chat" && (
                              ch.conversation_card_id ? (
                                <Link
                                  to={`/cards/${ch.project_id}/${ch.conversation_card_id}`}
                                  className="inline-flex items-center gap-1 font-medium text-primary hover:underline"
                                >
                                  专属讨论卡
                                  <ArrowUpRight className="size-3" />
                                </Link>
                              ) : (
                                <span>专属讨论卡：首条消息自动创建</span>
                              )
                            )}
                            {ch.role === "scheduled_push" && (
                              <span>
                                范围：
                                {ch.target_card_id
                                  ? `指定 Ongoing (${ch.target_card_id.slice(0, 8)})`
                                  : "项目全部 Ongoing"}{" "}
                                ·{" "}
                                {ch.push_filter === "warnings_only"
                                  ? "仅异常告警"
                                  : "全部结果"}
                              </span>
                            )}
                            {ch.role === "main_chat" && (
                              <>
                                <span>
                                  同时推送主聊天人工对话：
                                  {ch.mirror_web_messages ? "已开启" : "关闭"}
                                </span>
                                <span>
                                  白名单：
                                  {ch.allow_from?.length
                                    ? `${ch.allow_from.length} 项`
                                    : "全部放行"}
                                </span>
                              </>
                            )}
                            {ch.config?.target_chat_id && (
                              <span className="font-mono text-[11px]">
                                已绑定会话：{ch.config.target_chat_id}
                              </span>
                            )}
                          </div>

                          {secretEntries.length > 0 && (
                            <div className="flex flex-wrap items-center gap-2 pt-0.5">
                              {secretEntries.map(([k, v]) => (
                                <span
                                  key={k}
                                  className="inline-flex items-center gap-1 rounded-md border border-line bg-muted/40 px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground"
                                >
                                  {k}: {v}
                                </span>
                              ))}
                            </div>
                          )}
                        </div>
                      </div>

                      <div className="flex shrink-0 flex-wrap items-center gap-2">
                        <Switch
                          aria-label={`启停 ${ch.name}`}
                          checked={ch.enabled}
                          disabled={toggleMutation.isPending}
                          onCheckedChange={(checked) =>
                            toggleMutation.mutate({ id: ch.id, enabled: checked })
                          }
                        />
                        {canQuickSetPrimary && (
                          <Button
                            size="sm"
                            variant="secondary"
                            data-testid={`set-primary-${ch.id}`}
                            disabled={updatePrimaryRouteMutation.isPending}
                            onClick={() => handleSetChannelAsPrimary(ch)}
                          >
                            <Star className="size-3.5" />
                            设为主推
                          </Button>
                        )}
                        {meta.supportsQRLogin && ch.transport !== "webhook" && (
                          <Button
                            size="sm"
                            variant="secondary"
                            onClick={() =>
                              setQrTarget({
                                platform: ch.platform,
                                role: ch.role,
                                project_id: ch.project_id,
                                channel: ch,
                              })
                            }
                          >
                            <QrCode className="size-3.5" />
                            {ch.has_secrets ? "重新扫码" : "扫码登录"}
                          </Button>
                        )}
                        <Button
                          size="sm"
                          variant="secondary"
                          disabled={testingID === ch.id}
                          onClick={() => void handleTest(ch)}
                        >
                          {testingID === ch.id ? (
                            <LoaderCircle className="size-3.5 animate-spin" />
                          ) : (
                            <Send className="size-3.5" />
                          )}
                          测试发送
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => openEdit(ch)}
                        >
                          <Pencil className="size-3.5" />
                          设置
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => setDeleteTarget(ch)}
                        >
                          <Trash2 className="size-3.5 text-danger" />
                        </Button>
                      </div>
                    </div>

                    {ch.last_error && (
                      <div className="flex items-start gap-2 rounded-lg border border-danger-line bg-danger-muted px-3 py-2 text-xs text-danger">
                        <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                        <span className="break-all">{ch.last_error}</span>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </Section>

      {/* 五平台统一扫码一键接入 / 换绑弹窗 */}
      {qrTarget && (
        <ChannelQRConnectModal
          key={`${qrTarget.platform}-${qrTarget.channel?.id ?? "new"}`}
          target={qrTarget}
          projects={projects}
          onClose={() => setQrTarget(null)}
          onBound={() => {
            void queryClient.invalidateQueries({ queryKey: keys.channels });
            setQrTarget(null);
          }}
          onSwitchManual={(projectID, preferWebhook) =>
            openManualEditor(
              qrTarget.platform,
              qrTarget.role,
              projectID,
              preferWebhook,
            )
          }
        />
      )}

      {/* 极简表单弹窗（Telegram 单输入框 / 手动 ID+Secret / Webhook / 编辑高级设置） */}
      <Modal
        open={editorOpen && form !== null}
        onOpenChange={(open) => {
          setEditorOpen(open);
          if (!open) {
            setForm(null);
            setAdvancedOpen(false);
          }
        }}
        title={
          form?.id
            ? `编辑${PLATFORM_MAP[form.platform].name}`
            : `接入${form ? PLATFORM_MAP[form.platform].name : "远程通道"}`
        }
        description={
          form?.transport === "webhook"
            ? "填写群机器人 Webhook 地址即可接收定时任务结果推送。"
            : form?.platform === "telegram"
              ? "只需填写 Bot Token；保存后给机器人发送任意一条消息，系统会自动记住对话 Chat ID。"
              : "填写应用凭据即可立即连接；不常用参数已收进下方「高级设置」。"
        }
      >
        {form && (
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault();
              if (!form.project_id) {
                toast.error("请选择绑定项目");
                return;
              }
              saveMutation.mutate(form);
            }}
          >
            {/* 顶部保留项目选择；支持扫码的平台在新建手动模式提供一键切回扫码 */}
            <div className="flex flex-wrap items-end justify-between gap-2">
              <div className="min-w-[200px] flex-1">
                <Field label="绑定项目">
                  <Select
                    value={form.project_id}
                    onChange={(e) =>
                      setForm({
                        ...form,
                        project_id: e.target.value,
                        target_card_id: "",
                      })
                    }
                  >
                    {projects.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name}
                      </option>
                    ))}
                  </Select>
                </Field>
              </div>
              {!form.id && PLATFORM_MAP[form.platform].supportsQRLogin && (
                <Button
                  type="button"
                  size="sm"
                  variant="secondary"
                  onClick={() => {
                    setEditorOpen(false);
                    setQrTarget({
                      platform: form.platform,
                      role: form.role,
                      project_id: form.project_id,
                    });
                    setForm(null);
                  }}
                >
                  <QrCode className="size-3.5" />
                  改用扫码连接
                </Button>
              )}
            </div>

            {/* 核心首屏：仅保留 1~2 个必填凭据框 */}
            <div className="space-y-3 rounded-xl border border-line bg-muted/25 p-3.5">
              {form.transport === "webhook" ? (
                <>
                  <Field
                    label="群机器人 Webhook URL"
                    hint={
                      form.secret_hints.webhook_url
                        ? `已保存：${form.secret_hints.webhook_url}（留空保持不变）`
                        : "填写企业微信 / 飞书 / 钉钉自定义群机器人的 Webhook 地址"
                    }
                  >
                    <TextInput
                      type="password"
                      value={form.webhook_url}
                      onChange={(e) =>
                        setForm({ ...form, webhook_url: e.target.value })
                      }
                      placeholder="https://..."
                    />
                  </Field>
                  {(form.platform === "dingtalk" ||
                    form.platform === "feishu") && (
                    <Field
                      label="加签密钥（可选）"
                      hint={
                        form.secret_hints.webhook_secret
                          ? `已保存：${form.secret_hints.webhook_secret}`
                          : "如机器人开启了安全加签校验再填写"
                      }
                    >
                      <TextInput
                        type="password"
                        value={form.webhook_secret}
                        onChange={(e) =>
                          setForm({ ...form, webhook_secret: e.target.value })
                        }
                        placeholder="SEC..."
                      />
                    </Field>
                  )}
                </>
              ) : (
                <>
                  {form.platform === "telegram" && (
                    <Field
                      label="Bot Token"
                      hint={
                        form.secret_hints.bot_token
                          ? `已保存：${form.secret_hints.bot_token}（留空保持不变）`
                          : "从 @BotFather 获取；保存后给机器人发一句消息即自动绑定会话"
                      }
                    >
                      <TextInput
                        type="password"
                        value={form.bot_token}
                        onChange={(e) =>
                          setForm({ ...form, bot_token: e.target.value })
                        }
                        placeholder="123456:ABC-DEF..."
                      />
                    </Field>
                  )}

                  {form.platform === "wecom" && (
                    <>
                      <Field label="Bot ID">
                        <TextInput
                          value={form.bot_id}
                          onChange={(e) =>
                            setForm({ ...form, bot_id: e.target.value })
                          }
                          placeholder="企业微信智能机器人 Bot ID"
                        />
                      </Field>
                      <Field
                        label="Secret"
                        hint={
                          form.secret_hints.secret
                            ? `已保存：${form.secret_hints.secret}（留空保持不变）`
                            : undefined
                        }
                      >
                        <TextInput
                          type="password"
                          value={form.secret}
                          onChange={(e) =>
                            setForm({ ...form, secret: e.target.value })
                          }
                          placeholder="输入 Bot Secret"
                        />
                      </Field>
                    </>
                  )}

                  {form.platform === "feishu" && (
                    <>
                      <div className="grid gap-3 sm:grid-cols-2">
                        <Field label="App ID">
                          <TextInput
                            value={form.app_id}
                            onChange={(e) =>
                              setForm({ ...form, app_id: e.target.value })
                            }
                            placeholder="cli_..."
                          />
                        </Field>
                        <Field
                          label="App Secret"
                          hint={
                            form.secret_hints.app_secret
                              ? `已保存：${form.secret_hints.app_secret}`
                              : undefined
                          }
                        >
                          <TextInput
                            type="password"
                            value={form.app_secret}
                            onChange={(e) =>
                              setForm({ ...form, app_secret: e.target.value })
                            }
                            placeholder="App Secret"
                          />
                        </Field>
                      </div>
                      <Field label="平台版本">
                        <Select
                          value={form.domain || "feishu"}
                          onChange={(e) =>
                            setForm({ ...form, domain: e.target.value })
                          }
                        >
                          <option value="feishu">飞书中国版（feishu.cn）</option>
                          <option value="lark">Lark 国际版（larksuite.com）</option>
                        </Select>
                      </Field>
                    </>
                  )}

                  {form.platform === "dingtalk" && (
                    <>
                      <Field label="Client ID（AppKey）">
                        <TextInput
                          value={form.client_id}
                          onChange={(e) =>
                            setForm({ ...form, client_id: e.target.value })
                          }
                          placeholder="钉钉应用 Client ID"
                        />
                      </Field>
                      <Field
                        label="Client Secret（AppSecret）"
                        hint={
                          form.secret_hints.client_secret
                            ? `已保存：${form.secret_hints.client_secret}`
                            : undefined
                        }
                      >
                        <TextInput
                          type="password"
                          value={form.client_secret}
                          onChange={(e) =>
                            setForm({ ...form, client_secret: e.target.value })
                          }
                          placeholder="钉钉应用 Client Secret"
                        />
                      </Field>
                    </>
                  )}

                  {form.platform === "qq" && (
                    <>
                      <Field label="AppID">
                        <TextInput
                          value={form.app_id}
                          onChange={(e) =>
                            setForm({ ...form, app_id: e.target.value })
                          }
                          placeholder="QQ 开放平台 AppID"
                        />
                      </Field>
                      <Field
                        label="AppSecret"
                        hint={
                          form.secret_hints.secret
                            ? `已保存：${form.secret_hints.secret}`
                            : undefined
                        }
                      >
                        <TextInput
                          type="password"
                          value={form.secret}
                          onChange={(e) =>
                            setForm({ ...form, secret: e.target.value })
                          }
                          placeholder="QQ 开放平台 AppSecret"
                        />
                      </Field>
                    </>
                  )}

                  {form.platform === "weixin" && (
                    <div className="space-y-2 text-xs text-muted-foreground">
                      <p>
                        微信通过官方扫码直接获取加密 Bot Token，无需手动填写密钥。
                      </p>
                      {form.weixin_user_id && (
                        <p className="font-mono text-[11px] text-heading">
                          已绑定微信账号：{form.weixin_user_id}
                        </p>
                      )}
                    </div>
                  )}
                </>
              )}
            </div>

            {/* 定时推送专属精简选项（仅 role=scheduled_push 时展开） */}
            {form.role === "scheduled_push" && (
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="推送过滤">
                  <Select
                    value={form.push_filter}
                    onChange={(e) =>
                      setForm({
                        ...form,
                        push_filter: e.target.value as RemoteChannelPushFilter,
                      })
                    }
                  >
                    <option value="all">全部巡检与汇报结果</option>
                    <option value="warnings_only">仅异常告警</option>
                  </Select>
                </Field>
                <Field label="目标 Ongoing 卡片（可选）">
                  <Select
                    value={form.target_card_id}
                    onChange={(e) =>
                      setForm({ ...form, target_card_id: e.target.value })
                    }
                  >
                    <option value="">该项目全部 Ongoing 卡片</option>
                    {ongoingCards.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.title}
                      </option>
                    ))}
                  </Select>
                </Field>
              </div>
            )}

            {/* 可折叠高级设置面板：低频字段全部默认填好并收在此处 */}
            <div className="rounded-xl border border-line bg-surface">
              <button
                type="button"
                aria-expanded={advancedOpen}
                onClick={() => setAdvancedOpen((v) => !v)}
                className="flex w-full items-center justify-between px-3.5 py-2.5 text-left text-xs font-medium text-muted-foreground transition-colors hover:text-heading"
              >
                <span className="inline-flex items-center gap-1.5">
                  {advancedOpen ? (
                    <ChevronDown className="size-3.5" />
                  ) : (
                    <ChevronRight className="size-3.5" />
                  )}
                  高级设置（名称、角色、群策略、白名单、默认会话与代理）
                </span>
                <span className="text-[11px] text-muted-foreground/80">
                  {advancedOpen ? "收起" : "展开"}
                </span>
              </button>

              {advancedOpen && (
                <div className="space-y-3 border-t border-line px-3.5 py-3">
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label="通道名称">
                      <TextInput
                        value={form.name}
                        onChange={(e) =>
                          setForm({ ...form, name: e.target.value })
                        }
                        placeholder={defaultChannelName(
                          form.platform,
                          form.role,
                        )}
                      />
                    </Field>
                    <Field label="通道角色">
                      <Select
                        value={form.role}
                        onChange={(e) => {
                          const nextRole = e.target.value as RemoteChannelRole;
                          setForm({
                            ...form,
                            role: nextRole,
                            transport: defaultTransportFor(
                              form.platform,
                              nextRole,
                            ),
                          });
                        }}
                      >
                        <option value="main_chat">双向对话（专属独立讨论）</option>
                        <option value="scheduled_push">定时任务独立推送</option>
                      </Select>
                    </Field>
                  </div>

                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label="传输模式">
                      <Select
                        value={form.transport}
                        onChange={(e) =>
                          setForm({
                            ...form,
                            transport: e.target.value as RemoteChannelTransport,
                          })
                        }
                      >
                        {availableTransports(form.platform, form.role).map(
                          (tr) => (
                            <option key={tr} value={tr}>
                              {transportLabel(tr)}
                            </option>
                          ),
                        )}
                      </Select>
                    </Field>

                    {form.role === "main_chat" && (
                      <Field label="群聊触发策略">
                        <Select
                          value={form.group_policy}
                          onChange={(e) =>
                            setForm({
                              ...form,
                              group_policy: e.target
                                .value as RemoteChannelGroupPolicy,
                            })
                          }
                        >
                          <option value="mention_only">
                            仅 @ 机器人触发（默认）
                          </option>
                          <option value="allowlist">仅白名单群/成员放行</option>
                          <option value="all">放行所有群消息</option>
                        </Select>
                      </Field>
                    )}
                  </div>

                  {form.transport !== "webhook" && (
                    <div className="grid gap-3 sm:grid-cols-2">
                      <Field
                        label="默认推送会话 ID（可选）"
                        hint="收到首条合法消息后会自动记忆，无需手动填写"
                      >
                        <TextInput
                          value={form.target_chat_id}
                          onChange={(e) =>
                            setForm({ ...form, target_chat_id: e.target.value })
                          }
                          placeholder="chat_id / open_id"
                        />
                      </Field>
                      {form.platform === "telegram" && (
                        <Field
                          label="Topic Thread ID（可选）"
                          hint="Telegram 超级群分 Topic 时使用"
                        >
                          <TextInput
                            value={form.message_thread_id}
                            onChange={(e) =>
                              setForm({
                                ...form,
                                message_thread_id: e.target.value,
                              })
                            }
                            placeholder="例如：42"
                          />
                        </Field>
                      )}
                      {form.platform === "weixin" && (
                        <Field label="iLink 服务地址（可选）">
                          <TextInput
                            value={form.base_url}
                            onChange={(e) =>
                              setForm({ ...form, base_url: e.target.value })
                            }
                            placeholder="https://ilinkai.weixin.qq.com"
                          />
                        </Field>
                      )}
                    </div>
                  )}

                  {form.platform === "telegram" && (
                    <Field
                      label="HTTP/SOCKS 代理地址（可选）"
                      hint="本地或内网访问 Telegram API 可配置代理，如 http://127.0.0.1:7890"
                    >
                      <TextInput
                        value={form.proxy_url}
                        onChange={(e) =>
                          setForm({ ...form, proxy_url: e.target.value })
                        }
                        placeholder="http://127.0.0.1:7890"
                      />
                    </Field>
                  )}

                  {form.role === "main_chat" && (
                    <>
                      <Field
                        label="入站白名单 allow_from（可选）"
                        hint="每行或逗号分隔用户 ID / 会话 ID；留空表示允许私聊。"
                      >
                        <TextArea
                          rows={2}
                          value={form.allow_from_text}
                          onChange={(e) =>
                            setForm({
                              ...form,
                              allow_from_text: e.target.value,
                            })
                          }
                          placeholder="user_id_1&#10;chat_id_2"
                        />
                      </Field>

                      <div className="flex items-center justify-between gap-3 rounded-lg border border-line px-3 py-2">
                        <div className="min-w-0">
                          <p className="text-xs font-medium text-heading">
                            同时推送主聊天人工对话
                          </p>
                          <p className="text-[11px] text-muted-foreground">
                            本通道专属讨论卡的回复、子任务回调，以及项目主聊天的系统汇报/待验收/审批提醒默认都会推送；开启后，你在 Web 主聊天手动发起对话的 AI 最终回复也会附带【主聊天】标识单向推送到这里（不写入本通道专属讨论上下文）。
                          </p>
                        </div>
                        <Switch
                          checked={form.mirror_web_messages}
                          onCheckedChange={(checked) =>
                            setForm({ ...form, mirror_web_messages: checked })
                          }
                        />
                      </div>
                    </>
                  )}
                </div>
              )}
            </div>

            <div className="flex items-center justify-between gap-3 pt-1">
              <label className="inline-flex items-center gap-2 text-xs text-muted-foreground">
                <Switch
                  checked={form.enabled}
                  onCheckedChange={(checked) =>
                    setForm({ ...form, enabled: checked })
                  }
                />
                保存后立即启用连接
              </label>
              <div className="flex items-center gap-2">
                <Button
                  variant="ghost"
                  onClick={() => {
                    setEditorOpen(false);
                    setForm(null);
                    setAdvancedOpen(false);
                  }}
                >
                  取消
                </Button>
                <Button
                  type="submit"
                  variant="primary"
                  disabled={saveMutation.isPending}
                >
                  {saveMutation.isPending ? "保存中…" : "保存并连接"}
                </Button>
              </div>
            </div>
          </form>
        )}
      </Modal>

      {/* 删除确认框 */}
      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        title="删除远程通道"
        description={`确定删除「${deleteTarget?.name ?? ""}」吗？删除后将立即断开连接并清除加密凭据，已同步的历史对话不受影响。`}
        confirmLabel="确认删除"
        pending={deleteMutation.isPending}
        onConfirm={() => {
          if (deleteTarget) deleteMutation.mutate(deleteTarget.id);
        }}
      />
    </div>
  );
}

function ChannelQRConnectModal({
  target,
  projects,
  onClose,
  onBound,
  onSwitchManual,
}: {
  target: QRModalTarget;
  projects: Array<{ id: string; name: string }>;
  onClose: () => void;
  onBound: () => void;
  onSwitchManual: (projectID: string, preferWebhook: boolean) => void;
}) {
  const meta = PLATFORM_MAP[target.platform];
  const isRebind = Boolean(target.channel);
  const [projectID, setProjectID] = useState(
    target.channel?.project_id ?? target.project_id ?? projects[0]?.id ?? "",
  );
  const [domain, setDomain] = useState(
    target.channel?.config?.domain || (target.platform === "feishu" ? "feishu" : ""),
  );
  const [session, setSession] = useState<QRConnectSession | null>(null);
  const [loadedRequestKey, setLoadedRequestKey] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reloadTick, setReloadTick] = useState(0);
  const activeSessionIDRef = useRef<string | null>(null);

  const missingProject = !isRebind && !projectID;
  const requestKey = `${target.platform}:${target.channel?.id ?? "new"}:${projectID}:${domain}:${reloadTick}`;
  const loading = !missingProject && loadedRequestKey !== requestKey;

  useEffect(() => {
    activeSessionIDRef.current = session?.session_id ?? null;
  }, [session?.session_id]);

  // 启动或刷新二维码会话
  useEffect(() => {
    if (missingProject) {
      return;
    }

    let cancelled = false;
    void api
      .startChannelQRConnect(
        isRebind && target.channel
          ? {
              channel_id: target.channel.id,
              domain: target.platform === "feishu" ? domain : undefined,
            }
          : {
              platform: target.platform,
              project_id: projectID,
              role: target.role,
              domain: target.platform === "feishu" ? domain : undefined,
            },
      )
      .then((created) => {
        if (cancelled) return;
        setSession(created);
        setError(null);
        setLoadedRequestKey(requestKey);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(
          err instanceof Error ? err.message : `获取${meta.name}二维码失败`,
        );
        setLoadedRequestKey(requestKey);
      });

    return () => {
      cancelled = true;
    };
  }, [
    missingProject,
    requestKey,
    isRebind,
    target.channel,
    target.platform,
    target.role,
    projectID,
    domain,
    meta.name,
  ]);

  // 按服务端建议间隔轮询确认状态；确认后后端已自动建卡并加密启用。
  useEffect(() => {
    if (!session) return;
    if (
      session.status === "confirmed" ||
      session.status === "expired" ||
      session.status === "error" ||
      session.status === "cancelled"
    ) {
      return;
    }

    let cancelled = false;
    const delay = Math.max(1200, Math.min(session.interval_ms || 2000, 5000));
    const timer = window.setTimeout(() => {
      void api
        .pollChannelQRConnect(session.session_id)
        .then((res) => {
          if (cancelled) return;
          setSession(res.session);
          if (res.session.status === "confirmed") {
            toast.success(`${meta.name}扫码连接成功，已自动加密保存并上线`);
            onBound();
          }
        })
        .catch((err) => {
          if (!cancelled) {
            setError(err instanceof Error ? err.message : "轮询扫码状态失败");
          }
        });
    }, delay);

    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [session, meta.name, onBound]);

  const handleClose = () => {
    const sid = activeSessionIDRef.current;
    if (
      sid &&
      session &&
      (session.status === "wait" || session.status === "scanned")
    ) {
      void api.cancelChannelQRConnect(sid).catch(() => {});
    }
    onClose();
  };

  const handleRefresh = () => {
    setSession(null);
    setReloadTick((n) => n + 1);
  };

  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) handleClose();
      }}
      title={
        isRebind
          ? `重新扫码绑定 · ${target.channel?.name ?? meta.name}`
          : `${meta.name}扫码一键连接`
      }
      description={`使用手机${meta.qrAppLabel}扫一扫下方二维码，确认后自动创建并上线长连接，无需手动配置任何密钥。`}
    >
      <div className="space-y-4 py-1">
        {/* 首屏最精简的选择器：绑定项目 +（飞书双域名切换） */}
        {(!isRebind || target.platform === "feishu") && (
          <div className="grid gap-3 sm:grid-cols-2">
            {!isRebind && (
              <Field label="绑定项目">
                <Select
                  value={projectID}
                  onChange={(e) => {
                    setSession(null);
                    setProjectID(e.target.value);
                  }}
                >
                  {projects.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </Select>
              </Field>
            )}
            {target.platform === "feishu" && (
              <Field label="平台版本">
                <Select
                  value={domain || "feishu"}
                  onChange={(e) => {
                    setSession(null);
                    setDomain(e.target.value);
                  }}
                >
                  <option value="feishu">飞书中国版（feishu.cn）</option>
                  <option value="lark">Lark 国际版（larksuite.com）</option>
                </Select>
              </Field>
            )}
          </div>
        )}

        <div className="flex flex-col items-center gap-3.5 py-2">
          {missingProject ? (
            <div className="w-full space-y-3">
              <ErrorView
                error={new Error("请先创建一个项目后再绑定远程通道")}
                onRetry={handleRefresh}
              />
            </div>
          ) : loading ? (
            <div className="flex h-52 w-52 items-center justify-center rounded-2xl border border-line bg-muted/30">
              <Spinner text="生成二维码…" />
            </div>
          ) : error ? (
            <div className="w-full space-y-3">
              <ErrorView error={new Error(error)} onRetry={handleRefresh} />
            </div>
          ) : session ? (
            <>
              <div className="rounded-2xl border border-line bg-white p-4 shadow-xs">
                <QRCodeSVG
                  value={session.qrcode_content}
                  size={196}
                  level="M"
                />
              </div>

              <div className="flex items-center gap-2 text-xs">
                {session.status === "confirmed" ? (
                  <span className="inline-flex items-center gap-1.5 font-medium text-emerald-600">
                    <CheckCircle2 className="size-4" />
                    绑定成功，正在启用连接…
                  </span>
                ) : session.status === "scanned" ? (
                  <span className="inline-flex items-center gap-1.5 font-medium text-amber-600">
                    <LoaderCircle className="size-4 animate-spin" />
                    {session.message || `已扫码，请在${meta.qrAppLabel}确认…`}
                  </span>
                ) : session.status === "expired" ? (
                  <span className="inline-flex items-center gap-1.5 font-medium text-danger">
                    二维码已过期，请点击刷新重新生成
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1.5 text-muted-foreground">
                    <Bot className="size-4" />
                    {session.message || `等待手机${meta.qrAppLabel}扫码…`}
                  </span>
                )}
              </div>

              <div className="flex items-center gap-2">
                <Button variant="secondary" size="sm" onClick={handleRefresh}>
                  <RefreshCw className="size-3.5" />
                  刷新二维码
                </Button>
                <Button variant="ghost" size="sm" onClick={handleClose}>
                  关闭
                </Button>
              </div>
            </>
          ) : null}
        </div>

        {/* 底部保留轻量切换入口，满足已有企业应用凭据或群 Webhook 用户 */}
        {!isRebind && (
          <div className="flex flex-wrap items-center justify-center gap-2 border-t border-line pt-3">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => onSwitchManual(projectID, false)}
            >
              <Pencil className="size-3.5" />
              改用手动填写
            </Button>
            {meta.supportsWebhook && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => onSwitchManual(projectID, true)}
              >
                <Webhook className="size-3.5" />
                改用群机器人 Webhook
              </Button>
            )}
          </div>
        )}
      </div>
    </Modal>
  );
}

function availableTransports(
  platform: RemoteChannelPlatform,
  role: RemoteChannelRole,
): RemoteChannelTransport[] {
  const meta = PLATFORM_MAP[platform];
  const base = [meta.defaultTransport];
  if (role === "scheduled_push" && meta.supportsWebhook) {
    return ["webhook", meta.defaultTransport];
  }
  return base;
}

function transportLabel(tr: RemoteChannelTransport): string {
  switch (tr) {
    case "long_poll":
      return "HTTP 长轮询";
    case "websocket":
      return "WebSocket 长连接";
    case "stream":
      return "Stream 长连接";
    case "webhook":
      return "群机器人 Webhook";
    default:
      return tr;
  }
}
