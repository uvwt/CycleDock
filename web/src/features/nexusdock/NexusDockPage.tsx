import { useState, useMemo } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Monitor,
  RefreshCw,
  Sparkles,
  Plug,
  Package,
  Search,
  ExternalLink,
  Copy,
  Check,
  ChevronDown,
  ChevronUp,
  Laptop,
  Terminal,
  Unlink,
  AlertCircle,
  Wrench,
  Smartphone,
  ShieldAlert,
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import type {
  DeviceDynamicMCP,
  DevicePlugin,
  DeviceSkill,
  DeviceToolGuard,
} from "../../shared/api/types";
import { keys } from "../../app/query";
import { Spinner, ErrorView, Button } from "../../shared/ui/components";
import { DeviceToolGuardSection } from "./DeviceToolGuardSection";

type SkillSourceFilter = "all" | "plugin" | "managed" | "shared";
type McpSourceFilter = "all" | "plugin" | "standalone";

export type SkillPluginGroup = {
  key: string;
  pluginName: string;
  pluginMeta?: DevicePlugin;
  items: DeviceSkill[];
};

export type McpPluginGroup = {
  key: string;
  pluginName: string;
  pluginMeta?: DevicePlugin;
  items: DeviceDynamicMCP[];
};

const UNNAMED_PLUGIN_KEY = "__unnamed_plugin__";
const DEFAULT_PLUGIN_GROUP_LIMIT = 4;
const DEFAULT_PLUGIN_SKILL_PREVIEW = 8;

function isPluginSkill(skill: DeviceSkill): boolean {
  return skill.source_type === "plugin" || Boolean(skill.plugin_name?.trim());
}

function isPluginMcp(mcp: DeviceDynamicMCP): boolean {
  return mcp.source_type === "plugin" || Boolean(mcp.plugin_name?.trim());
}

// 按所属插件与非插件来源切分 Skill，供设备详情页分组渲染与来源筛选使用。
export function groupDeviceSkills(
  skills: DeviceSkill[],
  pluginMap: Map<string, DevicePlugin>,
) {
  const pluginBucket = new Map<string, SkillPluginGroup>();
  const standaloneItems: DeviceSkill[] = [];
  const managedItems: DeviceSkill[] = [];
  const sharedItems: DeviceSkill[] = [];

  for (const skill of skills) {
    if (isPluginSkill(skill)) {
      const pluginName = skill.plugin_name?.trim() || "";
      const key = pluginName || UNNAMED_PLUGIN_KEY;
      let group = pluginBucket.get(key);
      if (!group) {
        group = {
          key,
          pluginName,
          pluginMeta: pluginName ? pluginMap.get(pluginName) : undefined,
          items: [],
        };
        pluginBucket.set(key, group);
      }
      group.items.push(skill);
      continue;
    }

    standaloneItems.push(skill);
    if (skill.source_type === "shared") {
      sharedItems.push(skill);
    } else {
      managedItems.push(skill);
    }
  }

  const pluginGroups = Array.from(pluginBucket.values()).sort((a, b) => {
    if (!a.pluginName && b.pluginName) return 1;
    if (a.pluginName && !b.pluginName) return -1;
    return a.pluginName.localeCompare(b.pluginName);
  });

  const pluginItemsCount = pluginGroups.reduce(
    (sum, group) => sum + group.items.length,
    0,
  );

  return {
    pluginGroups,
    pluginItemsCount,
    standaloneItems,
    managedItems,
    sharedItems,
  };
}

// 按所属插件与独立服务切分动态 MCP，避免插件自带 MCP 与独立注册服务混排。
export function groupDeviceDynamicMcp(
  mcps: DeviceDynamicMCP[],
  pluginMap: Map<string, DevicePlugin>,
) {
  const pluginBucket = new Map<string, McpPluginGroup>();
  const standaloneItems: DeviceDynamicMCP[] = [];

  for (const mcp of mcps) {
    if (isPluginMcp(mcp)) {
      const pluginName = mcp.plugin_name?.trim() || "";
      const key = pluginName || UNNAMED_PLUGIN_KEY;
      let group = pluginBucket.get(key);
      if (!group) {
        group = {
          key,
          pluginName,
          pluginMeta: pluginName ? pluginMap.get(pluginName) : undefined,
          items: [],
        };
        pluginBucket.set(key, group);
      }
      group.items.push(mcp);
      continue;
    }

    standaloneItems.push(mcp);
  }

  const pluginGroups = Array.from(pluginBucket.values()).sort((a, b) => {
    if (!a.pluginName && b.pluginName) return 1;
    if (a.pluginName && !b.pluginName) return -1;
    return a.pluginName.localeCompare(b.pluginName);
  });

  const pluginItemsCount = pluginGroups.reduce(
    (sum, group) => sum + group.items.length,
    0,
  );

  return {
    pluginGroups,
    pluginItemsCount,
    standaloneItems,
  };
}

export default function NexusDockPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<"all" | "online" | "offline">("all");
  const [selectedDeviceId, setSelectedDeviceId] = useState<string | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [showCapabilities, setShowCapabilities] = useState(false);
  const [showSkills, setShowSkills] = useState(false);
  const [showDynamicMcp, setShowDynamicMcp] = useState(false);
  const [skillSourceFilter, setSkillSourceFilter] = useState<SkillSourceFilter>("all");
  const [mcpSourceFilter, setMcpSourceFilter] = useState<McpSourceFilter>("all");
  const [skillPageSize, setSkillPageSize] = useState(12);
  const [mcpPageSize, setMcpPageSize] = useState(8);
  const [skillPluginGroupLimit, setSkillPluginGroupLimit] = useState(
    DEFAULT_PLUGIN_GROUP_LIMIT,
  );
  const [mcpPluginGroupLimit, setMcpPluginGroupLimit] = useState(
    DEFAULT_PLUGIN_GROUP_LIMIT,
  );
  const [collapsedSkillPlugins, setCollapsedSkillPlugins] = useState<
    Record<string, boolean>
  >({});
  const [collapsedMcpPlugins, setCollapsedMcpPlugins] = useState<
    Record<string, boolean>
  >({});
  const [expandedSkillPluginItems, setExpandedSkillPluginItems] = useState<
    Record<string, boolean>
  >({});
  const [isRefreshing, setIsRefreshing] = useState(false);

  const me = useQuery({ queryKey: keys.me, queryFn: api.me });
  const workspaceID = me.data?.workspace.id || "";
  const isOwner = me.data?.role === "owner";

  // 1. NexusDock 连接状态与管理
  const connection = useQuery({
    queryKey: keys.nexus,
    queryFn: api.nexus,
    staleTime: 15_000,
  });

  const connect = useMutation({
    mutationFn: api.nexusConnect,
    onSuccess: (data) => {
      window.location.assign(data.authorization_url);
    },
    onError: (e) => toast.error(t("发起 NexusDock 授权失败：") + String(e)),
  });

  const disconnect = useMutation({
    mutationFn: api.nexusDisconnect,
    onSuccess: () => {
      queryClient.setQueryData(
        keys.nexus,
        (old: { available?: boolean } | undefined) =>
          old
            ? { ...old, connected: false }
            : { available: true, connected: false },
      );
      queryClient.setQueryData(keys.nexusFleet(workspaceID), {
        available: true,
        connected: false,
        devices: [],
      });
      setSelectedDeviceId(null);
      toast.success(t("已断开 NexusDock 设备连接"));
      void queryClient.invalidateQueries({ queryKey: keys.nexus });
      void queryClient.invalidateQueries({
        queryKey: keys.nexusDevices(workspaceID),
      });
      void queryClient.invalidateQueries({
        queryKey: keys.nexusFleet(workspaceID),
      });
    },
    onError: (e) => toast.error(t("断开连接失败：") + String(e)),
  });

  // 2. NexusFleet 设备群快照（包含 Skill 与动态 MCP）
  const fleet = useQuery({
    queryKey: keys.nexusFleet(workspaceID),
    queryFn: () => api.nexusFleet(false),
    enabled: Boolean(workspaceID && connection.data?.connected),
    staleTime: 20_000,
  });

  // 3. 设备安全守卫开关与工作区全局守卫档位
  const guards = useQuery({
    queryKey: keys.deviceToolGuards(workspaceID),
    queryFn: api.listDeviceToolGuards,
    enabled: Boolean(workspaceID && connection.data?.connected),
  });

  const globalToolGuard = useQuery({
    queryKey: keys.toolGuard,
    queryFn: api.toolGuard,
    enabled: Boolean(workspaceID && connection.data?.connected),
  });

  const guardMap = useMemo(() => {
    const map = new Map<string, DeviceToolGuard>();
    for (const guard of guards.data || []) {
      map.set(guard.device_node_id, guard);
    }
    return map;
  }, [guards.data]);

  async function handleRefresh() {
    setIsRefreshing(true);
    try {
      const data = await api.nexusFleet(true);
      queryClient.setQueryData(keys.nexusFleet(workspaceID), data);
      void queryClient.invalidateQueries({ queryKey: keys.nexus });
      void queryClient.invalidateQueries({
        queryKey: keys.deviceToolGuards(workspaceID),
      });
      toast.success(t("已更新 NexusDock 设备与运行能力"));
    } catch (err) {
      toast.error(t("刷新失败：") + String(err));
    } finally {
      setIsRefreshing(false);
    }
  }

  function handleCopy(id: string) {
    void navigator.clipboard.writeText(id);
    setCopiedId(id);
    toast.success(t("已复制节点 ID"));
    setTimeout(() => {
      setCopiedId((curr) => (curr === id ? null : curr));
    }, 2000);
  }

  function handleSelectDevice(id: string) {
    setSelectedDeviceId(id);
    setSkillSourceFilter("all");
    setMcpSourceFilter("all");
    setSkillPageSize(12);
    setMcpPageSize(8);
    setSkillPluginGroupLimit(DEFAULT_PLUGIN_GROUP_LIMIT);
    setMcpPluginGroupLimit(DEFAULT_PLUGIN_GROUP_LIMIT);
    setCollapsedSkillPlugins({});
    setCollapsedMcpPlugins({});
    setExpandedSkillPluginItems({});
  }

  const devices = useMemo(() => fleet.data?.devices || [], [fleet.data?.devices]);

  // 统计指标
  const totalCount = devices.length;
  const onlineCount = devices.filter((d) => d.online).length;
  const offlineCount = totalCount - onlineCount;

  // 过滤后的设备列表
  const filteredDevices = useMemo(() => {
    const q = search.trim().toLowerCase();
    return devices.filter((device) => {
      if (statusFilter === "online" && !device.online) return false;
      if (statusFilter === "offline" && device.online) return false;
      if (!q) return true;

      if (device.name.toLowerCase().includes(q) || device.id.toLowerCase().includes(q)) return true;
      if (device.os?.toLowerCase().includes(q) || device.arch?.toLowerCase().includes(q)) return true;
      if (
        device.plugins?.some(
          (p) => p.name.toLowerCase().includes(q) || p.description?.toLowerCase().includes(q),
        )
      ) {
        return true;
      }
      if (
        device.skills?.some(
          (s) =>
            s.name.toLowerCase().includes(q) ||
            s.description?.toLowerCase().includes(q) ||
            s.plugin_name?.toLowerCase().includes(q),
        )
      ) {
        return true;
      }
      if (
        device.dynamic_mcp?.some(
          (m) =>
            m.name.toLowerCase().includes(q) ||
            m.display_name?.toLowerCase().includes(q) ||
            m.description?.toLowerCase().includes(q) ||
            m.plugin_name?.toLowerCase().includes(q),
        )
      ) {
        return true;
      }
      return false;
    });
  }, [devices, search, statusFilter]);

  // 选中的设备 ID：若未显式选择或当前选中项已被过滤，自动取第一台在线设备（或首台）
  const activeDeviceId = useMemo(() => {
    if (selectedDeviceId && filteredDevices.some((d) => d.id === selectedDeviceId)) {
      return selectedDeviceId;
    }
    const firstOnline = filteredDevices.find((d) => d.online);
    return firstOnline ? firstOnline.id : filteredDevices[0]?.id || null;
  }, [filteredDevices, selectedDeviceId]);

  const selectedDevice = useMemo(
    () => devices.find((d) => d.id === activeDeviceId),
    [devices, activeDeviceId],
  );

  const isConnected = Boolean(connection.data?.connected);

  const pluginMetaMap = useMemo(() => {
    const map = new Map<string, DevicePlugin>();
    for (const plugin of selectedDevice?.plugins || []) {
      map.set(plugin.name, plugin);
    }
    return map;
  }, [selectedDevice?.plugins]);

  const skillGrouping = useMemo(
    () => groupDeviceSkills(selectedDevice?.skills || [], pluginMetaMap),
    [selectedDevice?.skills, pluginMetaMap],
  );

  const mcpGrouping = useMemo(
    () => groupDeviceDynamicMcp(selectedDevice?.dynamic_mcp || [], pluginMetaMap),
    [selectedDevice?.dynamic_mcp, pluginMetaMap],
  );

  const skillsCount = selectedDevice?.skills?.length || 0;
  const mcpCount = selectedDevice?.dynamic_mcp?.length || 0;
  const installedPluginsCount = selectedDevice?.plugins?.length || 0;

  // 若切换设备或搜索后某个来源分类在当前设备上为空，平滑回退到“全部”视图
  const effectiveSkillFilter: SkillSourceFilter = useMemo(() => {
    if (skillSourceFilter === "plugin" && skillGrouping.pluginItemsCount === 0) return "all";
    if (skillSourceFilter === "managed" && skillGrouping.managedItems.length === 0) return "all";
    if (skillSourceFilter === "shared" && skillGrouping.sharedItems.length === 0) return "all";
    return skillSourceFilter;
  }, [skillSourceFilter, skillGrouping]);

  const effectiveMcpFilter: McpSourceFilter = useMemo(() => {
    if (mcpSourceFilter === "plugin" && mcpGrouping.pluginItemsCount === 0) return "all";
    if (mcpSourceFilter === "standalone" && mcpGrouping.standaloneItems.length === 0) return "all";
    return mcpSourceFilter;
  }, [mcpSourceFilter, mcpGrouping]);

  const showSkillFilterBar =
    skillsCount > 0 &&
    [
      skillGrouping.pluginItemsCount > 0,
      skillGrouping.managedItems.length > 0,
      skillGrouping.sharedItems.length > 0,
    ].filter(Boolean).length > 1;

  const showMcpFilterBar =
    mcpCount > 0 &&
    mcpGrouping.pluginItemsCount > 0 &&
    mcpGrouping.standaloneItems.length > 0;

  const visibleStandaloneSkills = useMemo(() => {
    if (effectiveSkillFilter === "plugin") return [];
    if (effectiveSkillFilter === "managed") return skillGrouping.managedItems;
    if (effectiveSkillFilter === "shared") return skillGrouping.sharedItems;
    return skillGrouping.standaloneItems;
  }, [effectiveSkillFilter, skillGrouping]);

  function renderSkillBadge(skill: DeviceSkill) {
    if (isPluginSkill(skill)) {
      const pluginLabel = skill.plugin_name?.trim()
        ? t("插件 · {{name}}", { name: skill.plugin_name.trim() })
        : t("插件技能");
      return (
        <span
          title={pluginLabel}
          className="max-w-[150px] shrink-0 truncate rounded border border-amber-500/25 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300"
        >
          {pluginLabel}
        </span>
      );
    }
    if (skill.source_type === "shared") {
      return (
        <span className="shrink-0 rounded border border-purple-500/20 bg-purple-500/10 px-1.5 py-0.5 text-[10px] font-medium text-purple-600 dark:text-purple-400">
          {t("共享技能")}
        </span>
      );
    }
    if (skill.source_type === "managed") {
      return (
        <span className="shrink-0 rounded border border-primary/20 bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">
          {t("内置 / 托管")}
        </span>
      );
    }
    if (skill.source_type) {
      return (
        <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
          {skill.source_type}
        </span>
      );
    }
    return null;
  }

  function renderSkillCard(skill: DeviceSkill) {
    const itemKey = `${skill.source_type || "default"}:${skill.plugin_name || ""}:${skill.name}`;
    return (
      <div
        key={itemKey}
        className="flex min-w-0 flex-col justify-between rounded-xl border border-line bg-canvas/40 p-3.5 shadow-2xs transition-all hover:border-line/90"
      >
        <div className="min-w-0">
          <div className="flex min-w-0 items-start justify-between gap-2">
            <h4 className="min-w-0 flex-1 truncate text-xs font-semibold text-foreground">
              {skill.name}
            </h4>
            {renderSkillBadge(skill)}
          </div>
          <p className="mt-1.5 line-clamp-2 break-words [overflow-wrap:anywhere] text-xs leading-relaxed text-muted-foreground">
            {skill.description || t("暂无技能描述")}
          </p>
        </div>
      </div>
    );
  }

  function renderDynamicMcpCard(mcp: DeviceDynamicMCP) {
    const title = mcp.display_name || mcp.name;
    const showRuntimeId = Boolean(mcp.display_name && mcp.display_name !== mcp.name);
    const fromPlugin = isPluginMcp(mcp);
    const pluginLabel = mcp.plugin_name?.trim()
      ? t("插件 · {{name}}", { name: mcp.plugin_name.trim() })
      : t("插件");

    return (
      <div
        key={mcp.name}
        className="flex min-w-0 flex-col justify-between rounded-xl border border-line bg-canvas/40 p-3.5 shadow-2xs transition-all hover:border-line/90"
      >
        <div className="min-w-0">
          <div className="flex min-w-0 items-start justify-between gap-2">
            <div className="min-w-0 flex-1">
              <h4 className="truncate text-xs font-semibold text-foreground">
                {title}
              </h4>
              {showRuntimeId && (
                <p className="mt-0.5 truncate font-mono text-[10.5px] text-faint">
                  {mcp.name}
                </p>
              )}
            </div>

            <div className="flex shrink-0 flex-wrap items-center justify-end gap-1">
              {fromPlugin ? (
                <span
                  title={pluginLabel}
                  className="max-w-[140px] truncate rounded border border-amber-500/25 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300"
                >
                  {pluginLabel}
                </span>
              ) : (
                <span className="rounded border border-line bg-canvas px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                  {t("独立服务")}
                </span>
              )}
              <span
                className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${
                  mcp.status === "active" || mcp.status === "running"
                    ? "border border-emerald-500/20 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
                    : "bg-muted text-muted-foreground"
                }`}
              >
                {mcp.status || t("活跃")}
              </span>
            </div>
          </div>

          <p className="mt-1.5 line-clamp-2 break-words [overflow-wrap:anywhere] text-xs leading-relaxed text-muted-foreground">
            {mcp.description || t("无服务描述")}
          </p>
        </div>

        {typeof mcp.tool_count === "number" && (
          <div className="mt-3 flex items-center justify-between border-t border-line/60 pt-2.5 text-[11px] text-muted-foreground">
            <span>{t("包含工具")}</span>
            <span className="font-medium text-foreground">
              {mcp.tool_count} {t("个")}
            </span>
          </div>
        )}
      </div>
    );
  }

  return (
    <main className="mx-auto flex-1 min-h-0 min-w-0 w-full max-w-7xl space-y-4 overflow-y-auto overflow-x-hidden px-4 py-4 pb-24 md:space-y-7 md:px-8 md:py-8 md:pb-24">
      {/* 统一全局页面头部 */}
      <header className="flex flex-wrap items-center justify-between gap-3 md:gap-4">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="hidden text-3xl font-semibold tracking-tight text-heading md:block">
              {t("远程设备")}
            </h1>
            {isConnected ? (
              <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2.5 py-0.5 text-xs font-medium text-emerald-600 dark:text-emerald-400">
                <span className="size-1.5 rounded-full bg-emerald-500 animate-pulse" />
                {t("已连接")}
              </span>
            ) : (
              <span className="inline-flex items-center gap-1.5 rounded-full border border-amber-500/20 bg-amber-500/10 px-2.5 py-0.5 text-xs font-medium text-amber-600 dark:text-amber-400">
                {t("未连接")}
              </span>
            )}
          </div>
          <p className="mt-1 hidden text-sm text-muted-foreground md:block">
            {t("连接并管理你的远程设备，让 Agent 在授权范围内调用设备协助完成工作。")}
          </p>
        </div>

        {/* 顶部操作区 */}
        <div className="flex flex-wrap items-center gap-2">
          {isConnected ? (
            <>
              <Button
                variant="secondary"
                size="sm"
                onClick={handleRefresh}
                disabled={isRefreshing || fleet.isPending}
              >
                <RefreshCw
                  className={`size-3.5 ${isRefreshing || fleet.isFetching ? "animate-spin" : ""}`}
                />
                <span>{t("刷新")}</span>
              </Button>

              {connection.data?.manage_url && (
                <a
                  href={connection.data.manage_url}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-line bg-surface px-3 text-xs font-medium text-foreground shadow-2xs transition hover:bg-muted active:scale-95"
                >
                  <ExternalLink className="size-3.5 text-muted-foreground" />
                  <span>{t("管理设备")}</span>
                </a>
              )}

              <Button
                variant="danger"
                size="sm"
                disabled={disconnect.isPending}
                onClick={() => {
                  if (window.confirm(t("确定要断开 NexusDock 设备连接吗？"))) {
                    disconnect.mutate();
                  }
                }}
              >
                <Unlink className="size-3.5" />
                <span>{t("断开连接")}</span>
              </Button>
            </>
          ) : (
            <Button
              variant="primary"
              disabled={connect.isPending}
              onClick={() => connect.mutate()}
            >
              {t("连接设备")}
            </Button>
          )}
        </div>
      </header>

      {/* 主体内容区 */}
      {connection.isPending ? (
        <div className="grid place-items-center py-20">
          <Spinner text={t("正在检查远程设备连接状态…")} />
        </div>
      ) : connection.error && !connection.data ? (
        <ErrorView error={connection.error} onRetry={() => void connection.refetch()} />
      ) : !isConnected ? (
        /* 未连接：引导连接卡片 */
        <div className="grid place-items-center py-16">
          <div className="w-full max-w-md rounded-2xl border border-line bg-surface p-8 text-center shadow-2xs">
            <div className="mx-auto grid size-14 place-items-center rounded-2xl bg-primary/10">
              <img
                src="/nexusdock-logo.png"
                alt="NexusDock"
                className="size-9 object-contain"
                draggable={false}
              />
            </div>
            <h2 className="mt-4 text-lg font-semibold text-foreground">
              {t("连接你的远程设备")}
            </h2>
            <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
              {t("连接后，Agent 可在你的授权下使用该设备上的技能、工具与本地环境协助完成任务。")}
            </p>
            <div className="mt-6 flex flex-col gap-2.5">
              <Button
                variant="primary"
                disabled={connect.isPending}
                onClick={() => connect.mutate()}
                className="w-full justify-center"
              >
                {t("立即连接设备")}
              </Button>
              <p className="text-[11px] text-faint">
                {t("连接后可随时在此查看设备状态，或断开连接。")}
              </p>
            </div>
          </div>
        </div>
      ) : fleet.isPending && !devices.length ? (
        /* 加载设备快照中 */
        <div className="grid place-items-center py-20">
          <Spinner text={t("正在连接远程设备并拉取能力快照…")} />
        </div>
      ) : fleet.error && !fleet.data ? (
        <div className="mx-auto max-w-md rounded-2xl border border-line bg-surface p-6 shadow-2xs">
          <ErrorView error={fleet.error} onRetry={() => void fleet.refetch()} />
        </div>
      ) : (
        /* 已连接：搜索筛选栏 + Master-Detail 工作台 */
        <div className="min-w-0 space-y-4">
          {/* 筛选与检索栏 */}
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="relative w-full min-w-0 flex-1 sm:min-w-[240px] sm:max-w-sm">
              <Search className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted-foreground/60" />
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t("搜索设备、插件、Skill 或 MCP…")}
                className="h-9 w-full rounded-xl border border-line bg-surface pl-9 pr-3 text-base md:text-xs text-foreground placeholder:text-muted-foreground/60 focus:border-line focus:outline-none focus:ring-1 focus:ring-primary shadow-2xs transition"
              />
            </div>

            {/* 状态过滤 Tab：与 ActivityPage / PlanPage 风格完全一致 */}
            <div className="flex max-w-full gap-1 overflow-x-auto rounded-xl border border-line bg-surface p-1">
              <button
                type="button"
                onClick={() => setStatusFilter("all")}
                className={`shrink-0 rounded-lg px-3 py-1.5 text-xs font-medium transition ${
                  statusFilter === "all"
                    ? "bg-muted text-heading font-semibold"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                {t("全部")} ({totalCount})
              </button>
              <button
                type="button"
                onClick={() => setStatusFilter("online")}
                className={`shrink-0 rounded-lg px-3 py-1.5 text-xs font-medium transition ${
                  statusFilter === "online"
                    ? "bg-muted text-heading font-semibold"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                {t("在线")} ({onlineCount})
              </button>
              <button
                type="button"
                onClick={() => setStatusFilter("offline")}
                className={`shrink-0 rounded-lg px-3 py-1.5 text-xs font-medium transition ${
                  statusFilter === "offline"
                    ? "bg-muted text-heading font-semibold"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                {t("离线")} ({offlineCount})
              </button>
            </div>
          </div>

          {/* 设备列表与详情主区域 */}
          <div className="grid grid-cols-1 gap-5 min-w-0 lg:grid-cols-[320px_minmax(0,1fr)]">
            {/* 左侧设备列表卡片 */}
            <div className="min-w-0 overflow-hidden rounded-2xl border border-line bg-surface p-2 shadow-2xs">
              <div className="px-2 py-2 text-xs font-semibold text-muted-foreground tracking-wide">
                {t("设备列表")} ({filteredDevices.length})
              </div>

              <div className="space-y-1">
                {filteredDevices.length === 0 ? (
                  <div className="py-12 text-center">
                    <Monitor className="mx-auto size-7 text-muted-foreground/60" />
                    <p className="mt-2 text-xs text-muted-foreground">{t("没有找到匹配的设备")}</p>
                  </div>
                ) : (
                  filteredDevices.map((device) => {
                    const isSelected = device.id === activeDeviceId;
                    const devSkillsCount = device.skills?.length || 0;
                    const devMcpCount = device.dynamic_mcp?.length || 0;
                    const devPluginsCount = device.plugins?.length || 0;
                    const guardEnabled = Boolean(guardMap.get(device.id)?.enabled);
                    const globalGuardMode = globalToolGuard.data?.mode || "block_high_risk";

                    return (
                      <button
                        key={device.id}
                        type="button"
                        onClick={() => handleSelectDevice(device.id)}
                        className={`group flex w-full flex-col rounded-xl p-3 text-left transition-all ${
                          isSelected
                            ? "bg-muted/90 text-foreground font-medium shadow-2xs border border-line"
                            : "text-muted-foreground hover:bg-muted/50 hover:text-foreground border border-transparent"
                        }`}
                      >
                        <div className="flex items-center justify-between gap-1.5 w-full">
                          <div className="flex items-center gap-2.5 min-w-0">
                            {/* 在线指示灯 */}
                            <span className="relative flex size-2 shrink-0">
                              {device.online ? (
                                <>
                                  <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-400 opacity-75" />
                                  <span className="relative inline-flex size-2 rounded-full bg-emerald-500" />
                                </>
                              ) : (
                                <span className="inline-flex size-2 rounded-full bg-muted-foreground/40" />
                              )}
                            </span>

                            <span className="truncate text-xs font-medium text-foreground">
                              {device.name}
                            </span>
                          </div>

                          {device.os && (
                            <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-normal bg-canvas text-muted-foreground border border-line/60">
                              {device.os}
                            </span>
                          )}
                        </div>

                        <div className="mt-2 flex flex-wrap items-center gap-2 text-[11px] text-faint">
                          {devPluginsCount > 0 && (
                            <>
                              <span>
                                {devPluginsCount} {t("插件")}
                              </span>
                              <span>·</span>
                            </>
                          )}
                          <span>{devSkillsCount} Skill</span>
                          <span>·</span>
                          <span>{devMcpCount} MCP</span>
                          {device.arch && (
                            <>
                              <span>·</span>
                              <span>{device.arch}</span>
                            </>
                          )}
                          {guardEnabled && (
                            <span
                              className={`ml-auto inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium ${
                                globalGuardMode === "strict"
                                  ? "border-red-500/20 bg-red-500/10 text-red-700 dark:text-red-300"
                                  : "border-orange-500/20 bg-orange-500/10 text-orange-700 dark:text-orange-300"
                              }`}
                            >
                              <ShieldAlert className="size-2.5 shrink-0" />
                              {globalGuardMode === "strict"
                                ? t("严格模式")
                                : t("仅拦截高危")}
                            </span>
                          )}
                        </div>
                      </button>
                    );
                  })
                )}
              </div>
            </div>

            {/* 右侧设备能力详情检查器 */}
            <div className="min-w-0 rounded-2xl border border-line bg-surface p-4 sm:p-6 shadow-2xs space-y-6">
              {selectedDevice ? (
                <>
                  {/* 设备概览 Header */}
                  <div className="flex flex-wrap items-center justify-between gap-4 border-b border-line pb-5">
                    <div className="flex min-w-0 flex-1 items-center gap-3.5">
                      <div
                        className={`grid size-12 shrink-0 place-items-center rounded-2xl ${
                          selectedDevice.online
                            ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20"
                            : "bg-muted text-muted-foreground border border-line"
                        }`}
                      >
                        {selectedDevice.os?.toLowerCase().includes("android") ? (
                          <Smartphone className="size-6" />
                        ) : selectedDevice.os?.toLowerCase().includes("linux") ? (
                          <Terminal className="size-6" />
                        ) : selectedDevice.os?.toLowerCase().includes("darwin") ||
                          selectedDevice.os?.toLowerCase().includes("mac") ? (
                          <Laptop className="size-6" />
                        ) : (
                          <Monitor className="size-6" />
                        )}
                      </div>

                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2.5">
                          <h2 className="min-w-0 max-w-full break-words [overflow-wrap:anywhere] text-xl font-semibold tracking-tight text-heading">
                            {selectedDevice.name}
                          </h2>
                          {selectedDevice.online ? (
                            <span className="inline-flex items-center gap-1.5 rounded-full bg-emerald-500/10 border border-emerald-500/20 px-2 py-0.5 text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
                              <span className="size-1.5 rounded-full bg-emerald-500 animate-pulse" />
                              {t("在线")}
                            </span>
                          ) : (
                            <span className="inline-flex items-center gap-1.5 rounded-full bg-muted border border-line px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                              {t("离线")}
                            </span>
                          )}
                        </div>

                        <div className="mt-1 flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
                          {selectedDevice.os && <span>{t("系统")}: {selectedDevice.os}</span>}
                          {selectedDevice.arch && <span>{t("架构")}: {selectedDevice.arch}</span>}
                          {selectedDevice.agent_version && <span>{t("版本")}: {selectedDevice.agent_version}</span>}
                          {installedPluginsCount > 0 && (
                            <span>
                              {t("已装插件")}: {installedPluginsCount}
                            </span>
                          )}
                        </div>
                      </div>
                    </div>

                    {/* 节点 ID 复制 */}
                    <button
                      type="button"
                      onClick={() => handleCopy(selectedDevice.id)}
                      title={t("复制节点 ID")}
                      className="inline-flex items-center gap-1.5 rounded-lg border border-line bg-canvas px-3 py-1.5 text-xs text-muted-foreground transition hover:bg-muted hover:text-foreground shadow-2xs active:scale-95"
                    >
                      {copiedId === selectedDevice.id ? (
                        <>
                          <Check className="size-3.5 text-emerald-600" />
                          <span className="font-mono text-emerald-600">{t("已复制")}</span>
                        </>
                      ) : (
                        <>
                          <Copy className="size-3.5" />
                          <span className="font-mono text-[11px]">
                            {selectedDevice.id.slice(0, 14)}…
                          </span>
                        </>
                      )}
                    </button>
                  </div>

                  {selectedDevice.error && (
                    <div className="flex min-w-0 items-start gap-2.5 rounded-xl border border-amber-500/20 bg-amber-500/10 p-3.5 text-xs text-amber-700 dark:text-amber-400">
                      <AlertCircle className="mt-0.5 size-4 shrink-0" />
                      <span className="min-w-0 flex-1 break-words [overflow-wrap:anywhere]">
                        {t("同步提示")}: {selectedDevice.error}
                      </span>
                    </div>
                  )}

                  {/* 设备独立安全守卫配置 */}
                  <DeviceToolGuardSection
                    key={selectedDevice.id}
                    workspaceID={workspaceID}
                    device={selectedDevice}
                    guard={guardMap.get(selectedDevice.id)}
                    globalMode={globalToolGuard.data?.mode}
                    isOwner={isOwner}
                  />

                  {/* 1. Skill 技能清单 */}
                  <div className="min-w-0 rounded-xl border border-line bg-canvas/30 p-4 shadow-2xs space-y-3">
                    <button
                      type="button"
                      onClick={() => setShowSkills((v) => !v)}
                      className="flex w-full min-w-0 items-center justify-between gap-2 text-xs font-semibold text-foreground hover:text-primary transition-colors cursor-pointer"
                    >
                      <div className="flex min-w-0 items-center gap-2">
                        <Sparkles className="size-4 shrink-0 text-primary" />
                        <h3 className="truncate text-sm font-semibold text-foreground">
                          {t("Skill 技能清单")}
                        </h3>
                        <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
                          {skillsCount}
                        </span>
                        {skillGrouping.pluginGroups.length > 0 && (
                          <span className="hidden sm:inline-flex shrink-0 items-center gap-1 rounded-full border border-amber-500/20 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:text-amber-300">
                            <Package className="size-3" />
                            {t("{{count}} 个插件", {
                              count: skillGrouping.pluginGroups.length,
                            })}
                          </span>
                        )}
                      </div>
                      <div className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                        <span>{showSkills ? t("收起") : t("展开")}</span>
                        {showSkills ? (
                          <ChevronUp className="size-4" />
                        ) : (
                          <ChevronDown className="size-4" />
                        )}
                      </div>
                    </button>

                    {showSkills && (
                      <div className="pt-2 border-t border-line/60 space-y-4">
                        {!skillsCount ? (
                          <div className="rounded-xl border border-dashed border-line p-6 text-center text-xs text-muted-foreground">
                            {t("当前设备未加载任何 Skill 技能")}
                          </div>
                        ) : (
                          <>
                            {/* 来源分类筛选 */}
                            {showSkillFilterBar && (
                              <div className="flex max-w-full items-center gap-1 overflow-x-auto rounded-xl border border-line bg-surface p-1">
                                <button
                                  type="button"
                                  onClick={() => setSkillSourceFilter("all")}
                                  className={`shrink-0 rounded-lg px-2.5 py-1 text-xs font-medium transition ${
                                    effectiveSkillFilter === "all"
                                      ? "bg-muted text-heading font-semibold"
                                      : "text-muted-foreground hover:text-foreground"
                                  }`}
                                >
                                  {t("全部")} ({skillsCount})
                                </button>
                                {skillGrouping.pluginItemsCount > 0 && (
                                  <button
                                    type="button"
                                    onClick={() => setSkillSourceFilter("plugin")}
                                    className={`shrink-0 rounded-lg px-2.5 py-1 text-xs font-medium transition ${
                                      effectiveSkillFilter === "plugin"
                                        ? "bg-muted text-heading font-semibold"
                                        : "text-muted-foreground hover:text-foreground"
                                    }`}
                                  >
                                    {t("插件提供")} ({skillGrouping.pluginItemsCount})
                                  </button>
                                )}
                                {skillGrouping.managedItems.length > 0 && (
                                  <button
                                    type="button"
                                    onClick={() => setSkillSourceFilter("managed")}
                                    className={`shrink-0 rounded-lg px-2.5 py-1 text-xs font-medium transition ${
                                      effectiveSkillFilter === "managed"
                                        ? "bg-muted text-heading font-semibold"
                                        : "text-muted-foreground hover:text-foreground"
                                    }`}
                                  >
                                    {t("内置 / 托管")} ({skillGrouping.managedItems.length})
                                  </button>
                                )}
                                {skillGrouping.sharedItems.length > 0 && (
                                  <button
                                    type="button"
                                    onClick={() => setSkillSourceFilter("shared")}
                                    className={`shrink-0 rounded-lg px-2.5 py-1 text-xs font-medium transition ${
                                      effectiveSkillFilter === "shared"
                                        ? "bg-muted text-heading font-semibold"
                                        : "text-muted-foreground hover:text-foreground"
                                    }`}
                                  >
                                    {t("共享技能")} ({skillGrouping.sharedItems.length})
                                  </button>
                                )}
                              </div>
                            )}

                            {/* A. 插件所属 Skill 分组视图 */}
                            {(effectiveSkillFilter === "all" ||
                              effectiveSkillFilter === "plugin") &&
                              skillGrouping.pluginGroups.length > 0 && (
                                <div className="space-y-3 min-w-0">
                                  {skillGrouping.pluginGroups
                                    .slice(0, skillPluginGroupLimit)
                                    .map((group) => {
                                      const isGroupCollapsed = Boolean(
                                        collapsedSkillPlugins[group.key],
                                      );
                                      const isItemsExpanded = Boolean(
                                        expandedSkillPluginItems[group.key],
                                      );
                                      const displayedItems = isItemsExpanded
                                        ? group.items
                                        : group.items.slice(0, DEFAULT_PLUGIN_SKILL_PREVIEW);
                                      const remainingItems =
                                        group.items.length - displayedItems.length;
                                      const groupTitle =
                                        group.pluginName || t("未命名插件");

                                      return (
                                        <div
                                          key={group.key}
                                          data-skill-plugin-group={group.key}
                                          className="min-w-0 rounded-xl border border-line bg-surface p-3.5 shadow-2xs space-y-2.5"
                                        >
                                          <button
                                            type="button"
                                            onClick={() =>
                                              setCollapsedSkillPlugins((prev) => ({
                                                ...prev,
                                                [group.key]: !prev[group.key],
                                              }))
                                            }
                                            className="flex w-full min-w-0 items-center justify-between gap-2 text-left cursor-pointer"
                                          >
                                            <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
                                              <span className="inline-flex size-6 shrink-0 items-center justify-center rounded-lg border border-amber-500/20 bg-amber-500/10 text-amber-600 dark:text-amber-400">
                                                <Package className="size-3.5" />
                                              </span>
                                              <span className="truncate text-xs font-semibold text-foreground">
                                                {groupTitle}
                                              </span>
                                              {group.pluginMeta?.version && (
                                                <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                                                  v{group.pluginMeta.version}
                                                </span>
                                              )}
                                              {group.pluginMeta &&
                                                !group.pluginMeta.enabled && (
                                                  <span className="shrink-0 rounded border border-line bg-canvas px-1.5 py-0.5 text-[10px] text-muted-foreground">
                                                    {t("插件已停用")}
                                                  </span>
                                                )}
                                              <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                                                {group.items.length} Skill
                                              </span>
                                            </div>
                                            <div className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
                                              <span>
                                                {isGroupCollapsed
                                                  ? t("展开")
                                                  : t("收起")}
                                              </span>
                                              {isGroupCollapsed ? (
                                                <ChevronDown className="size-3.5" />
                                              ) : (
                                                <ChevronUp className="size-3.5" />
                                              )}
                                            </div>
                                          </button>

                                          {!isGroupCollapsed && (
                                            <>
                                              {group.pluginMeta?.description && (
                                                <p className="text-[11px] leading-relaxed text-muted-foreground break-words [overflow-wrap:anywhere]">
                                                  {group.pluginMeta.description}
                                                </p>
                                              )}

                                              <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2 min-w-0">
                                                {displayedItems.map((skill) =>
                                                  renderSkillCard(skill),
                                                )}
                                              </div>

                                              {group.items.length >
                                                DEFAULT_PLUGIN_SKILL_PREVIEW && (
                                                <div className="flex justify-center pt-1">
                                                  <Button
                                                    variant="ghost"
                                                    size="sm"
                                                    onClick={() =>
                                                      setExpandedSkillPluginItems(
                                                        (prev) => ({
                                                          ...prev,
                                                          [group.key]: !prev[group.key],
                                                        }),
                                                      )
                                                    }
                                                    className="text-xs text-muted-foreground"
                                                  >
                                                    {isItemsExpanded
                                                      ? t("收起其余项")
                                                      : t("展开其余 {{count}} 项", {
                                                          count: remainingItems,
                                                        })}
                                                  </Button>
                                                </div>
                                              )}
                                            </>
                                          )}
                                        </div>
                                      );
                                    })}

                                  {skillGrouping.pluginGroups.length >
                                    DEFAULT_PLUGIN_GROUP_LIMIT && (
                                    <div className="flex items-center justify-center gap-2 pt-1">
                                      {skillPluginGroupLimit <
                                      skillGrouping.pluginGroups.length ? (
                                        <Button
                                          variant="secondary"
                                          size="sm"
                                          onClick={() =>
                                            setSkillPluginGroupLimit((curr) =>
                                              Math.min(
                                                curr + DEFAULT_PLUGIN_GROUP_LIMIT,
                                                skillGrouping.pluginGroups.length,
                                              ),
                                            )
                                          }
                                          className="text-xs"
                                        >
                                          {t("加载更多插件分组")} ({skillPluginGroupLimit}/
                                          {skillGrouping.pluginGroups.length})
                                        </Button>
                                      ) : (
                                        <Button
                                          variant="ghost"
                                          size="sm"
                                          onClick={() =>
                                            setSkillPluginGroupLimit(
                                              DEFAULT_PLUGIN_GROUP_LIMIT,
                                            )
                                          }
                                          className="text-xs text-muted-foreground"
                                        >
                                          {t("收起多余插件分组")}
                                        </Button>
                                      )}
                                    </div>
                                  )}
                                </div>
                              )}

                            {/* B. 内置 / 托管与共享 Skill 列表 */}
                            {effectiveSkillFilter !== "plugin" &&
                              visibleStandaloneSkills.length > 0 && (
                                <div className="space-y-2.5 min-w-0">
                                  {effectiveSkillFilter === "all" &&
                                    skillGrouping.pluginGroups.length > 0 && (
                                      <div className="flex items-center gap-2 pt-1">
                                        <span className="text-xs font-semibold text-muted-foreground">
                                          {t("独立与共享技能")}
                                        </span>
                                        <span className="rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                                          {visibleStandaloneSkills.length}
                                        </span>
                                      </div>
                                    )}

                                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 min-w-0">
                                    {visibleStandaloneSkills
                                      .slice(0, skillPageSize)
                                      .map((skill) => renderSkillCard(skill))}
                                  </div>

                                  {visibleStandaloneSkills.length > 12 && (
                                    <div className="flex items-center justify-center gap-2 pt-2">
                                      {skillPageSize <
                                      visibleStandaloneSkills.length ? (
                                        <Button
                                          variant="secondary"
                                          size="sm"
                                          onClick={() =>
                                            setSkillPageSize((curr) =>
                                              Math.min(
                                                curr + 12,
                                                visibleStandaloneSkills.length,
                                              ),
                                            )
                                          }
                                          className="text-xs"
                                        >
                                          {t("加载更多技能")} ({skillPageSize}/
                                          {visibleStandaloneSkills.length})
                                        </Button>
                                      ) : (
                                        <Button
                                          variant="ghost"
                                          size="sm"
                                          onClick={() => setSkillPageSize(12)}
                                          className="text-xs text-muted-foreground"
                                        >
                                          {t("收起多余技能")}
                                        </Button>
                                      )}
                                    </div>
                                  )}
                                </div>
                              )}
                          </>
                        )}
                      </div>
                    )}
                  </div>

                  {/* 2. 动态 MCP 服务 */}
                  <div className="min-w-0 rounded-xl border border-line bg-canvas/30 p-4 shadow-2xs space-y-3">
                    <button
                      type="button"
                      onClick={() => setShowDynamicMcp((v) => !v)}
                      className="flex w-full min-w-0 items-center justify-between gap-2 text-xs font-semibold text-foreground hover:text-primary transition-colors cursor-pointer"
                    >
                      <div className="flex min-w-0 items-center gap-2">
                        <Plug className="size-4 shrink-0 text-purple-600 dark:text-purple-400" />
                        <h3 className="truncate text-sm font-semibold text-foreground">
                          {t("动态 MCP 服务")}
                        </h3>
                        <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
                          {mcpCount}
                        </span>
                        {mcpGrouping.pluginGroups.length > 0 && (
                          <span className="hidden sm:inline-flex shrink-0 items-center gap-1 rounded-full border border-amber-500/20 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:text-amber-300">
                            <Package className="size-3" />
                            {t("{{count}} 个插件", {
                              count: mcpGrouping.pluginGroups.length,
                            })}
                          </span>
                        )}
                      </div>
                      <div className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                        <span>{showDynamicMcp ? t("收起") : t("展开")}</span>
                        {showDynamicMcp ? (
                          <ChevronUp className="size-4" />
                        ) : (
                          <ChevronDown className="size-4" />
                        )}
                      </div>
                    </button>

                    {showDynamicMcp && (
                      <div className="pt-2 border-t border-line/60 space-y-4">
                        {!mcpCount ? (
                          <div className="rounded-xl border border-dashed border-line p-6 text-center text-xs text-muted-foreground">
                            {t("当前设备未配置或运行动态 MCP 服务")}
                          </div>
                        ) : (
                          <>
                            {/* 来源分类筛选 */}
                            {showMcpFilterBar && (
                              <div className="flex max-w-full items-center gap-1 overflow-x-auto rounded-xl border border-line bg-surface p-1">
                                <button
                                  type="button"
                                  onClick={() => setMcpSourceFilter("all")}
                                  className={`shrink-0 rounded-lg px-2.5 py-1 text-xs font-medium transition ${
                                    effectiveMcpFilter === "all"
                                      ? "bg-muted text-heading font-semibold"
                                      : "text-muted-foreground hover:text-foreground"
                                  }`}
                                >
                                  {t("全部")} ({mcpCount})
                                </button>
                                <button
                                  type="button"
                                  onClick={() => setMcpSourceFilter("plugin")}
                                  className={`shrink-0 rounded-lg px-2.5 py-1 text-xs font-medium transition ${
                                    effectiveMcpFilter === "plugin"
                                      ? "bg-muted text-heading font-semibold"
                                      : "text-muted-foreground hover:text-foreground"
                                  }`}
                                >
                                  {t("插件提供")} ({mcpGrouping.pluginItemsCount})
                                </button>
                                <button
                                  type="button"
                                  onClick={() => setMcpSourceFilter("standalone")}
                                  className={`shrink-0 rounded-lg px-2.5 py-1 text-xs font-medium transition ${
                                    effectiveMcpFilter === "standalone"
                                      ? "bg-muted text-heading font-semibold"
                                      : "text-muted-foreground hover:text-foreground"
                                  }`}
                                >
                                  {t("独立服务")} ({mcpGrouping.standaloneItems.length})
                                </button>
                              </div>
                            )}

                            {/* A. 插件所属动态 MCP 分组视图 */}
                            {(effectiveMcpFilter === "all" ||
                              effectiveMcpFilter === "plugin") &&
                              mcpGrouping.pluginGroups.length > 0 && (
                                <div className="space-y-3 min-w-0">
                                  {mcpGrouping.pluginGroups
                                    .slice(0, mcpPluginGroupLimit)
                                    .map((group) => {
                                      const isGroupCollapsed = Boolean(
                                        collapsedMcpPlugins[group.key],
                                      );
                                      const groupTitle =
                                        group.pluginName || t("未命名插件");

                                      return (
                                        <div
                                          key={group.key}
                                          data-mcp-plugin-group={group.key}
                                          className="min-w-0 rounded-xl border border-line bg-surface p-3.5 shadow-2xs space-y-2.5"
                                        >
                                          <button
                                            type="button"
                                            onClick={() =>
                                              setCollapsedMcpPlugins((prev) => ({
                                                ...prev,
                                                [group.key]: !prev[group.key],
                                              }))
                                            }
                                            className="flex w-full min-w-0 items-center justify-between gap-2 text-left cursor-pointer"
                                          >
                                            <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
                                              <span className="inline-flex size-6 shrink-0 items-center justify-center rounded-lg border border-amber-500/20 bg-amber-500/10 text-amber-600 dark:text-amber-400">
                                                <Package className="size-3.5" />
                                              </span>
                                              <span className="truncate text-xs font-semibold text-foreground">
                                                {groupTitle}
                                              </span>
                                              {group.pluginMeta?.version && (
                                                <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                                                  v{group.pluginMeta.version}
                                                </span>
                                              )}
                                              {group.pluginMeta &&
                                                !group.pluginMeta.enabled && (
                                                  <span className="shrink-0 rounded border border-line bg-canvas px-1.5 py-0.5 text-[10px] text-muted-foreground">
                                                    {t("插件已停用")}
                                                  </span>
                                                )}
                                              <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                                                {group.items.length} MCP
                                              </span>
                                            </div>
                                            <div className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
                                              <span>
                                                {isGroupCollapsed
                                                  ? t("展开")
                                                  : t("收起")}
                                              </span>
                                              {isGroupCollapsed ? (
                                                <ChevronDown className="size-3.5" />
                                              ) : (
                                                <ChevronUp className="size-3.5" />
                                              )}
                                            </div>
                                          </button>

                                          {!isGroupCollapsed && (
                                            <>
                                              {group.pluginMeta?.description && (
                                                <p className="text-[11px] leading-relaxed text-muted-foreground break-words [overflow-wrap:anywhere]">
                                                  {group.pluginMeta.description}
                                                </p>
                                              )}

                                              <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2 min-w-0">
                                                {group.items.map((mcp) =>
                                                  renderDynamicMcpCard(mcp),
                                                )}
                                              </div>
                                            </>
                                          )}
                                        </div>
                                      );
                                    })}

                                  {mcpGrouping.pluginGroups.length >
                                    DEFAULT_PLUGIN_GROUP_LIMIT && (
                                    <div className="flex items-center justify-center gap-2 pt-1">
                                      {mcpPluginGroupLimit <
                                      mcpGrouping.pluginGroups.length ? (
                                        <Button
                                          variant="secondary"
                                          size="sm"
                                          onClick={() =>
                                            setMcpPluginGroupLimit((curr) =>
                                              Math.min(
                                                curr + DEFAULT_PLUGIN_GROUP_LIMIT,
                                                mcpGrouping.pluginGroups.length,
                                              ),
                                            )
                                          }
                                          className="text-xs"
                                        >
                                          {t("加载更多插件分组")} ({mcpPluginGroupLimit}/
                                          {mcpGrouping.pluginGroups.length})
                                        </Button>
                                      ) : (
                                        <Button
                                          variant="ghost"
                                          size="sm"
                                          onClick={() =>
                                            setMcpPluginGroupLimit(
                                              DEFAULT_PLUGIN_GROUP_LIMIT,
                                            )
                                          }
                                          className="text-xs text-muted-foreground"
                                        >
                                          {t("收起多余插件分组")}
                                        </Button>
                                      )}
                                    </div>
                                  )}
                                </div>
                              )}

                            {/* B. 独立配置的动态 MCP 服务列表 */}
                            {effectiveMcpFilter !== "plugin" &&
                              mcpGrouping.standaloneItems.length > 0 && (
                                <div className="space-y-2.5 min-w-0">
                                  {effectiveMcpFilter === "all" &&
                                    mcpGrouping.pluginGroups.length > 0 && (
                                      <div className="flex items-center gap-2 pt-1">
                                        <span className="text-xs font-semibold text-muted-foreground">
                                          {t("独立 MCP 服务")}
                                        </span>
                                        <span className="rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                                          {mcpGrouping.standaloneItems.length}
                                        </span>
                                      </div>
                                    )}

                                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 min-w-0">
                                    {mcpGrouping.standaloneItems
                                      .slice(0, mcpPageSize)
                                      .map((mcp) => renderDynamicMcpCard(mcp))}
                                  </div>

                                  {mcpGrouping.standaloneItems.length > 8 && (
                                    <div className="flex items-center justify-center gap-2 pt-2">
                                      {mcpPageSize <
                                      mcpGrouping.standaloneItems.length ? (
                                        <Button
                                          variant="secondary"
                                          size="sm"
                                          onClick={() =>
                                            setMcpPageSize((curr) =>
                                              Math.min(
                                                curr + 8,
                                                mcpGrouping.standaloneItems.length,
                                              ),
                                            )
                                          }
                                          className="text-xs"
                                        >
                                          {t("加载更多 MCP 服务")} ({mcpPageSize}/
                                          {mcpGrouping.standaloneItems.length})
                                        </Button>
                                      ) : (
                                        <Button
                                          variant="ghost"
                                          size="sm"
                                          onClick={() => setMcpPageSize(8)}
                                          className="text-xs text-muted-foreground"
                                        >
                                          {t("收起多余 MCP 服务")}
                                        </Button>
                                      )}
                                    </div>
                                  )}
                                </div>
                              )}
                          </>
                        )}
                      </div>
                    )}
                  </div>

                  {/* 3. 内置底层能力 */}
                  {selectedDevice.capabilities && selectedDevice.capabilities.length > 0 && (
                    <div className="min-w-0 rounded-xl border border-line bg-canvas/30 p-4 shadow-2xs">
                      <button
                        type="button"
                        onClick={() => setShowCapabilities((v) => !v)}
                        className="flex w-full min-w-0 items-center justify-between gap-2 text-xs font-medium text-foreground hover:text-primary transition-colors"
                      >
                        <div className="flex min-w-0 items-center gap-2">
                          <Wrench className="size-3.5 shrink-0 text-muted-foreground" />
                          <span className="truncate">{t("内置底层能力")} ({selectedDevice.capabilities.length})</span>
                        </div>
                        {showCapabilities ? (
                          <ChevronUp className="size-4 shrink-0 text-muted-foreground" />
                        ) : (
                          <ChevronDown className="size-4 shrink-0 text-muted-foreground" />
                        )}
                      </button>

                      {showCapabilities && (
                        <div className="mt-3 pt-3 border-t border-line/60 flex flex-wrap gap-1.5">
                          {selectedDevice.capabilities.map((cap) => (
                            <span
                              key={cap}
                              className="max-w-full break-all rounded-md border border-line bg-muted/60 px-2.5 py-1 font-mono text-[10.5px] text-muted-foreground"
                            >
                              {cap}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </>
              ) : (
                <div className="grid place-items-center py-24 text-center text-xs text-muted-foreground">
                  {t("请选择左侧设备以查看详情")}
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </main>
  );
}
