import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  BellRing,
  Bot,
  CheckCircle2,
  Clock,
  LoaderCircle,
  MessageCircle,
  MessageSquare,
  Pencil,
  Plus,
  QrCode,
  RefreshCw,
  Send,
  ShieldCheck,
  Trash2,
  Webhook,
  AlertTriangle,
} from "lucide-react";
import { QRCodeSVG } from "qrcode.react";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import type {
  RemoteChannel,
  RemoteChannelGroupPolicy,
  RemoteChannelInput,
  RemoteChannelPlatform,
  RemoteChannelPushFilter,
  RemoteChannelRole,
  RemoteChannelRuntimeStatus,
  RemoteChannelTransport,
  WeixinLoginSession,
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
  supportsQRLogin?: boolean;
  protocolHint: string;
};

const PLATFORMS: PlatformMeta[] = [
  {
    id: "weixin",
    name: "微信",
    shortName: "WX",
    tagline: "扫码绑定个人微信，免公网回调长轮询收发",
    badgeColor: "text-emerald-700 dark:text-emerald-300",
    accentBg: "bg-emerald-500/12 border-emerald-500/25",
    defaultTransport: "long_poll",
    supportsWebhook: false,
    supportsQRLogin: true,
    protocolHint: "iLink HTTP 长轮询 · 扫码获取加密 Bot Token",
  },
  {
    id: "wecom",
    name: "企业微信",
    shortName: "企微",
    tagline: "AI Bot 官方 WebSocket 长连接 / 群机器人 Webhook",
    badgeColor: "text-sky-700 dark:text-sky-300",
    accentBg: "bg-sky-500/12 border-sky-500/25",
    defaultTransport: "websocket",
    supportsWebhook: true,
    protocolHint: "openws.work.weixin.qq.com 免公网回调",
  },
  {
    id: "feishu",
    name: "飞书 / Lark",
    shortName: "飞书",
    tagline: "官方 WebSocket 长连接、话题隔离 / 群 Webhook",
    badgeColor: "text-blue-700 dark:text-blue-300",
    accentBg: "bg-blue-500/12 border-blue-500/25",
    defaultTransport: "websocket",
    supportsWebhook: true,
    protocolHint: "支持 Feishu / Lark 双域名与群 @ 策略",
  },
  {
    id: "dingtalk",
    name: "钉钉",
    shortName: "钉钉",
    tagline: "官方 Stream 长连接 / 加签群 Webhook 推送",
    badgeColor: "text-indigo-700 dark:text-indigo-300",
    accentBg: "bg-indigo-500/12 border-indigo-500/25",
    defaultTransport: "stream",
    supportsWebhook: true,
    protocolHint: "DingTalk Stream 免公网回调 · 支持加签 Secret",
  },
  {
    id: "qq",
    name: "QQ 机器人",
    shortName: "QQ",
    tagline: "QQ 开放平台 Bot API + WebSocket Gateway",
    badgeColor: "text-violet-700 dark:text-violet-300",
    accentBg: "bg-violet-500/12 border-violet-500/25",
    defaultTransport: "websocket",
    supportsWebhook: false,
    protocolHint: "C2C 单聊与群聊双向收发",
  },
  {
    id: "telegram",
    name: "Telegram",
    shortName: "TG",
    tagline: "官方 Bot API 长轮询，支持群 Topics 分流与代理",
    badgeColor: "text-cyan-700 dark:text-cyan-300",
    accentBg: "bg-cyan-500/12 border-cyan-500/25",
    defaultTransport: "long_poll",
    supportsWebhook: false,
    protocolHint: "可把 General 绑主聊天、另一 Topic 绑定时推送",
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

function defaultTransportFor(
  platform: RemoteChannelPlatform,
  role: RemoteChannelRole,
): RemoteChannelTransport {
  const meta = PLATFORM_MAP[platform];
  if (role === "scheduled_push" && meta.supportsWebhook) {
    return "webhook";
  }
  return meta.defaultTransport;
}

function buildInitialForm(
  platform: RemoteChannelPlatform,
  role: RemoteChannelRole,
  defaultProjectID: string,
  existing?: RemoteChannel,
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
      domain: existing.config?.domain ?? (existing.platform === "feishu" ? "feishu" : ""),
      ws_url: existing.config?.ws_url ?? "",
      bot_id: existing.config?.bot_id ?? "",
      app_id: existing.config?.app_id ?? "",
      client_id: existing.config?.client_id ?? "",
      target_chat_id: existing.config?.target_chat_id ?? "",
      target_chat_type: existing.config?.target_chat_type ?? "",
      message_thread_id:
        existing.config?.message_thread_id && existing.config.message_thread_id > 0
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
  const meta = PLATFORM_MAP[platform];
  return {
    name:
      role === "scheduled_push"
        ? `${meta.name} 定时任务推送`
        : `${meta.name} 主聊天同步`,
    platform,
    role,
    transport: defaultTransportFor(platform, role),
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

  const [roleFilter, setRoleFilter] = useState<"all" | RemoteChannelRole>("all");
  const [editorOpen, setEditorOpen] = useState(false);
  const [form, setForm] = useState<FormState | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<RemoteChannel | null>(null);
  const [testingID, setTestingID] = useState<string | null>(null);
  const [qrChannel, setQrChannel] = useState<RemoteChannel | null>(null);

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

  const channels = useMemo(() => channelsQuery.data ?? [], [channelsQuery.data]);
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

      const payload: RemoteChannelInput = {
        name: state.name.trim(),
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
      toast.success(form?.id ? "通道配置已更新" : "远程通道已创建");
      // 新建微信通道且未配置 token 时自动引导扫码登录
      if (saved.platform === "weixin" && !saved.has_secrets) {
        setQrChannel(saved);
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

  const openNew = (
    platform: RemoteChannelPlatform,
    role: RemoteChannelRole = "main_chat",
  ) => {
    const defaultProjectID = projectsQuery.data?.[0]?.id ?? "";
    setForm(buildInitialForm(platform, role, defaultProjectID));
    setEditorOpen(true);
  };

  const openEdit = (ch: RemoteChannel) => {
    setForm(buildInitialForm(ch.platform, ch.role, ch.project_id, ch));
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
      {/* 顶部工作台总览与平台网格 */}
      <Section
        title="远程通道工作台"
        description="统一接入微信、企业微信、飞书、钉钉、QQ 与 Telegram（全部免公网回调）。支持「项目主聊天双向同步」与「定时任务独立推送」两种角色。"
      >
        <div className="space-y-4">
          {/* 快捷角色提示条 */}
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="flex items-start justify-between gap-3 rounded-xl border border-line bg-muted/35 p-3.5">
              <div className="min-w-0 space-y-1">
                <div className="flex items-center gap-2">
                  <MessageSquare className="size-4 shrink-0 text-primary" />
                  <span className="text-[13px] font-semibold text-heading">
                    主聊天双向同步
                  </span>
                  <Badge variant="secondary">{mainChatCount}</Badge>
                </div>
                <p className="text-xs leading-5 text-muted-foreground">
                  外部 IM 消息经白名单校验后直达项目主聊天，仅回传最终回复、待验收、异常与安全审批，绝不刷屏中间进度。
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
                  单独接收所选项目或 Ongoing 卡片的巡检与定时汇报，绝不写入主聊天 Session，支持群 Webhook 快速接入。
                </p>
                <div className="pt-1 flex flex-wrap gap-1.5">
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => openNew("wecom", "scheduled_push")}
                  >
                    <Webhook className="size-3.5" />
                    企微定时群机器人
                  </Button>
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => openNew("feishu", "scheduled_push")}
                  >
                    <Webhook className="size-3.5" />
                    飞书定时群机器人
                  </Button>
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => openNew("dingtalk", "scheduled_push")}
                  >
                    <Webhook className="size-3.5" />
                    钉钉定时群机器人
                  </Button>
                </div>
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
                      onClick={() => openNew(p.id, "main_chat")}
                    >
                      <Plus className="size-3.5" />
                      绑定主聊天
                    </Button>
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => openNew(p.id, "scheduled_push")}
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
        description="支持多实例并行运行，可按角色筛选、一键测试连接、实时查看心跳与报错，所有凭据均在服务端加密存储并脱敏展示。"
      >
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap items-center gap-1 rounded-lg border border-line bg-muted/40 p-1">
              {(
                [
                  { id: "all", label: `全部 (${channels.length})` },
                  { id: "main_chat", label: `主聊天双向 (${mainChatCount})` },
                  {
                    id: "scheduled_push",
                    label: `定时独立推送 (${scheduledCount})`,
                  },
                ] as const
              ).map((item) => (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => setRoleFilter(item.id)}
                  className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                    roleFilter === item.id
                      ? "bg-surface text-primary shadow-xs ring-1 ring-line"
                      : "text-muted-foreground hover:text-heading"
                  }`}
                >
                  {item.label}
                </button>
              ))}
            </div>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => void channelsQuery.refetch()}
            >
              <RefreshCw className="size-3.5" />
              刷新状态
            </Button>
          </div>

          {filteredChannels.length === 0 ? (
            <div className="rounded-xl border border-dashed border-line bg-muted/20 px-4 py-8 text-center">
              <MessageCircle className="mx-auto mb-2 size-7 text-muted-foreground/60" />
              <p className="text-sm font-medium text-heading">
                还没有配置对应的远程通道
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                点击上方任意平台卡片的「绑定主聊天」或「定时推送」即可快速创建。
              </p>
            </div>
          ) : (
            <div className="space-y-3">
              {filteredChannels.map((ch) => {
                const meta = PLATFORM_MAP[ch.platform];
                const status = STATUS_META[ch.runtime_status] ?? STATUS_META.disabled;
                const project = projectsByID[ch.project_id];
                const secretEntries = Object.entries(ch.secret_hints ?? {});
                return (
                  <div
                    key={ch.id}
                    className="min-w-0 rounded-xl border border-line bg-surface p-4 space-y-3"
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
                                ? "主聊天双向同步"
                                : "定时任务独立推送"}
                            </Badge>
                            <Badge variant="outline" className="text-[11px]">
                              {transportLabel(ch.transport)}
                            </Badge>
                          </div>

                          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                            <span>
                              项目：{project ? project.name : ch.project_id.slice(0, 8)}
                            </span>
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
                                  同步 Web 对话回复：
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
                            {ch.config?.message_thread_id ? (
                              <span>Topic #{ch.config.message_thread_id}</span>
                            ) : null}
                          </div>

                          {secretEntries.length > 0 && (
                            <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
                              <ShieldCheck className="size-3.5 text-emerald-600 dark:text-emerald-400" />
                              {secretEntries.map(([k, v]) => (
                                <span
                                  key={k}
                                  className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground"
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
                        {ch.platform === "weixin" && (
                          <Button
                            size="sm"
                            variant="secondary"
                            onClick={() => setQrChannel(ch)}
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
                          配置
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

      {/* 通道新建/编辑弹窗 */}
      <Modal
        open={editorOpen && form !== null}
        onOpenChange={(open) => {
          setEditorOpen(open);
          if (!open) setForm(null);
        }}
        title={
          form?.id
            ? `编辑通道 · ${PLATFORM_MAP[form.platform].name}`
            : form
              ? `新建远程通道 · ${PLATFORM_MAP[form.platform].name}`
              : "配置远程通道"
        }
        description="所有敏感 Token / Secret / Webhook URL 均通过服务端 AES-256-GCM 加密存储，编辑时留空将保留已保存的凭据。"
      >
        {form && (
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault();
              if (!form.name.trim()) {
                toast.error("请填写通道名称");
                return;
              }
              if (!form.project_id) {
                toast.error("请选择绑定项目");
                return;
              }
              saveMutation.mutate(form);
            }}
          >
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="通道名称">
                <TextInput
                  value={form.name}
                  onChange={(e) =>
                    setForm({ ...form, name: e.target.value })
                  }
                  placeholder="例如：产品研发飞书主聊天"
                />
              </Field>
              <Field label="接入平台">
                <Select
                  disabled={Boolean(form.id)}
                  value={form.platform}
                  onChange={(e) => {
                    const nextPlatform = e.target.value as RemoteChannelPlatform;
                    setForm({
                      ...form,
                      platform: nextPlatform,
                      transport: defaultTransportFor(nextPlatform, form.role),
                      domain: nextPlatform === "feishu" ? "feishu" : "",
                    });
                  }}
                >
                  {PLATFORMS.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="通道角色">
                <Select
                  value={form.role}
                  onChange={(e) => {
                    const nextRole = e.target.value as RemoteChannelRole;
                    setForm({
                      ...form,
                      role: nextRole,
                      transport: defaultTransportFor(form.platform, nextRole),
                    });
                  }}
                >
                  <option value="main_chat">项目主聊天双向同步</option>
                  <option value="scheduled_push">定时任务独立推送</option>
                </Select>
              </Field>

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
                  {availableTransports(form.platform, form.role).map((tr) => (
                    <option key={tr} value={tr}>
                      {transportLabel(tr)}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
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
                  {(projectsQuery.data ?? []).map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </Select>
              </Field>

              {form.role === "scheduled_push" ? (
                <Field label="定时任务推送过滤">
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
                    <option value="warnings_only">
                      仅异常告警（warning / critical / 失败）
                    </option>
                  </Select>
                </Field>
              ) : (
                <Field label="群聊触发策略">
                  <Select
                    value={form.group_policy}
                    onChange={(e) =>
                      setForm({
                        ...form,
                        group_policy: e.target.value as RemoteChannelGroupPolicy,
                      })
                    }
                  >
                    <option value="mention_only">仅 @ 机器人触发（默认）</option>
                    <option value="allowlist">仅白名单群/成员放行</option>
                    <option value="all">放行所有群消息</option>
                  </Select>
                </Field>
              )}
            </div>

            {form.role === "scheduled_push" && (
              <Field
                label="推送目标 Ongoing 卡片（可选）"
                hint="留空则推送该项目下全部 Ongoing 定时任务的结果；指定后仅推送该卡片，绝不写入主聊天 Session。"
              >
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
            )}

            {/* 平台专属配置区 */}
            <div className="space-y-3 rounded-xl border border-line bg-muted/25 p-3.5">
              <p className="text-xs font-semibold text-heading">
                {PLATFORM_MAP[form.platform].name} 凭据与接入参数
              </p>

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
                  {(form.platform === "feishu" || form.platform === "dingtalk") && (
                    <Field
                      label="加签密钥 Secret（可选）"
                      hint={
                        form.secret_hints.webhook_secret
                          ? `已保存：${form.secret_hints.webhook_secret}`
                          : "若群机器人开启了签名校验请填写"
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
                  {form.platform === "weixin" && (
                    <div className="space-y-2 text-xs text-muted-foreground">
                      <p>
                        保存后点击「扫码登录」，使用手机微信扫描二维码即可自动获取并加密保存 Bot Token，无需本地微信客户端或公网回调。
                      </p>
                      <Field label="iLink 服务地址（默认无需修改）">
                        <TextInput
                          value={form.base_url}
                          onChange={(e) =>
                            setForm({ ...form, base_url: e.target.value })
                          }
                          placeholder="https://ilinkai.weixin.qq.com"
                        />
                      </Field>
                      {form.weixin_user_id && (
                        <p className="font-mono text-[11px]">
                          已绑定微信用户：{form.weixin_user_id}
                        </p>
                      )}
                    </div>
                  )}

                  {form.platform === "wecom" && (
                    <>
                      <Field label="企微 AI Bot ID">
                        <TextInput
                          value={form.bot_id}
                          onChange={(e) =>
                            setForm({ ...form, bot_id: e.target.value })
                          }
                          placeholder="在企业微信智能机器人后台获取"
                        />
                      </Field>
                      <Field
                        label="Bot Secret"
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
                          placeholder="输入企业微信 AI Bot Secret"
                        />
                      </Field>
                      <Field
                        label="默认推送会话 ID（可选）"
                        hint="收到首条私聊/群聊消息后会自动记忆，也可预先填写 userid 或 chatid"
                      >
                        <TextInput
                          value={form.target_chat_id}
                          onChange={(e) =>
                            setForm({ ...form, target_chat_id: e.target.value })
                          }
                          placeholder="userid / chatid"
                        />
                      </Field>
                    </>
                  )}

                  {form.platform === "feishu" && (
                    <>
                      <div className="grid gap-3 sm:grid-cols-2">
                        <Field label="平台域名">
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
                        <Field label="App ID">
                          <TextInput
                            value={form.app_id}
                            onChange={(e) =>
                              setForm({ ...form, app_id: e.target.value })
                            }
                            placeholder="cli_xxxxxxxx"
                          />
                        </Field>
                      </div>
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
                          placeholder="飞书应用 App Secret"
                        />
                      </Field>
                      <Field
                        label="默认接收会话 Chat ID（可选）"
                        hint="用于主动推送/测试发送，例如 oc_xxxx"
                      >
                        <TextInput
                          value={form.target_chat_id}
                          onChange={(e) =>
                            setForm({ ...form, target_chat_id: e.target.value })
                          }
                          placeholder="oc_xxxxxxxx"
                        />
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
                      <Field label="QQ Bot AppID">
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
                      <Field
                        label="默认推送 OpenID（可选）"
                        hint="支持 C2C user_openid 或群 group_openid"
                      >
                        <TextInput
                          value={form.target_chat_id}
                          onChange={(e) =>
                            setForm({ ...form, target_chat_id: e.target.value })
                          }
                          placeholder="openid"
                        />
                      </Field>
                    </>
                  )}

                  {form.platform === "telegram" && (
                    <>
                      <Field
                        label="Bot Token"
                        hint={
                          form.secret_hints.bot_token
                            ? `已保存：${form.secret_hints.bot_token}`
                            : "从 @BotFather 获取"
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
                      <div className="grid gap-3 sm:grid-cols-2">
                        <Field
                          label="Chat ID"
                          hint="私聊用户 ID 或超级群 ID（如 -100...）"
                        >
                          <TextInput
                            value={form.target_chat_id}
                            onChange={(e) =>
                              setForm({ ...form, target_chat_id: e.target.value })
                            }
                            placeholder="-1001234567890"
                          />
                        </Field>
                        <Field
                          label="Topic Thread ID（可选）"
                          hint="超级群分 Topic 绑定时填写，留空为 General"
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
                      </div>
                      <Field
                        label="HTTP/SOCKS 代理地址（可选）"
                        hint="国内网络环境可填写本地代理，如 http://127.0.0.1:7890"
                      >
                        <TextInput
                          value={form.proxy_url}
                          onChange={(e) =>
                            setForm({ ...form, proxy_url: e.target.value })
                          }
                          placeholder="http://127.0.0.1:7890"
                        />
                      </Field>
                    </>
                  )}
                </>
              )}
            </div>

            {form.role === "main_chat" && (
              <div className="space-y-3">
                <div className="flex items-center justify-between gap-3 rounded-xl border border-line px-3.5 py-2.5">
                  <div className="min-w-0">
                    <p className="text-xs font-semibold text-heading">
                      同步 Web 对话回复
                    </p>
                    <p className="text-[11px] text-muted-foreground">
                      默认已推送本通道回复、主动消息、子任务回调与审批提醒；开启后，你在
                      Web 主聊天发起对话的 AI
                      最终回复也会同步过来（不回传你输入的原文）
                    </p>
                  </div>
                  <Switch
                    checked={form.mirror_web_messages}
                    onCheckedChange={(checked) =>
                      setForm({ ...form, mirror_web_messages: checked })
                    }
                  />
                </div>

                <Field
                  label="入站白名单 allow_from（可选）"
                  hint="每行或逗号分隔用户 ID / 会话 ID；留空表示允许所有私聊（群聊仍受上方群聊策略约束）。"
                >
                  <TextArea
                    rows={2}
                    value={form.allow_from_text}
                    onChange={(e) =>
                      setForm({ ...form, allow_from_text: e.target.value })
                    }
                    placeholder="user_id_1&#10;chat_id_2"
                  />
                </Field>
              </div>
            )}

            <div className="flex items-center justify-between gap-3 pt-2">
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
                  }}
                >
                  取消
                </Button>
                <Button
                  type="submit"
                  variant="primary"
                  disabled={saveMutation.isPending}
                >
                  {saveMutation.isPending ? "保存中…" : "保存配置"}
                </Button>
              </div>
            </div>
          </form>
        )}
      </Modal>

      {/* 微信扫码登录弹窗 */}
      {qrChannel && (
        <WeixinQRLoginModal
          key={qrChannel.id}
          channel={qrChannel}
          onClose={() => setQrChannel(null)}
          onBound={() => {
            void queryClient.invalidateQueries({ queryKey: keys.channels });
            setQrChannel(null);
          }}
        />
      )}

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

function WeixinQRLoginModal({
  channel,
  onClose,
  onBound,
}: {
  channel: RemoteChannel;
  onClose: () => void;
  onBound: () => void;
}) {
  const [session, setSession] = useState<WeixinLoginSession | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [reloadTick, setReloadTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void api
      .startWeixinLogin({
        channel_id: channel.id,
        base_url: channel.config?.base_url,
      })
      .then((created) => {
        if (cancelled) return;
        setSession(created);
        setError(null);
        setLoading(false);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : "获取微信二维码失败");
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [channel.id, channel.config?.base_url, reloadTick]);

  useEffect(() => {
    if (!session) return;
    if (
      session.status === "confirmed" ||
      session.status === "expired" ||
      session.status === "error"
    ) {
      return;
    }

    let cancelled = false;
    const poll = async () => {
      try {
        const res = await api.pollWeixinLogin(session.session_id);
        if (cancelled) return;
        setSession(res.session);
        if (res.session.status === "confirmed") {
          toast.success("微信扫码绑定成功，已自动加密保存凭据");
          onBound();
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "轮询扫码状态失败");
        }
      }
    };

    const timer = window.setTimeout(() => {
      void poll();
    }, 1500);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [session, onBound]);

  const handleRefresh = () => {
    setLoading(true);
    setError(null);
    setSession(null);
    setReloadTick((n) => n + 1);
  };

  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      title="微信扫码登录"
      description="请使用手机微信扫描下方二维码并确认授权。登录成功后会自动将加密 Bot Token 写入当前通道。"
    >
      <div className="flex flex-col items-center gap-4 py-2">
        {loading ? (
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
                  绑定成功
                </span>
              ) : session.status === "scanned" ? (
                <span className="inline-flex items-center gap-1.5 font-medium text-amber-600">
                  <LoaderCircle className="size-4 animate-spin" />
                  已扫码，请在微信手机端点击确认…
                </span>
              ) : session.status === "expired" ? (
                <span className="inline-flex items-center gap-1.5 font-medium text-danger">
                  二维码已过期，请重新生成
                </span>
              ) : (
                <span className="inline-flex items-center gap-1.5 text-muted-foreground">
                  <Bot className="size-4" />
                  等待手机微信扫码…
                </span>
              )}
            </div>

            <div className="flex items-center gap-2">
              <Button variant="secondary" size="sm" onClick={handleRefresh}>
                <RefreshCw className="size-3.5" />
                刷新二维码
              </Button>
              <Button variant="ghost" size="sm" onClick={onClose}>
                关闭
              </Button>
            </div>
          </>
        ) : null}
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
