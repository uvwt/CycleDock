import { useState, useMemo } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Cpu,
  Monitor,
  RefreshCw,
  Sparkles,
  Plug,
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
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import { Spinner, ErrorView, Button } from "../../shared/ui/components";

export default function NexusDockPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<"all" | "online" | "offline">("all");
  const [selectedDeviceId, setSelectedDeviceId] = useState<string | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [showCapabilities, setShowCapabilities] = useState(false);
  const [isRefreshing, setIsRefreshing] = useState(false);

  const me = useQuery({ queryKey: keys.me, queryFn: api.me });
  const workspaceID = me.data?.workspace.id || "";

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
    onSuccess: async () => {
      toast.success(t("已断开 NexusDock 设备连接"));
      await queryClient.invalidateQueries({ queryKey: keys.nexus });
      await queryClient.invalidateQueries({ queryKey: keys.nexusFleet(workspaceID) });
      setSelectedDeviceId(null);
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

  async function handleRefresh() {
    setIsRefreshing(true);
    try {
      const data = await api.nexusFleet(true);
      queryClient.setQueryData(keys.nexusFleet(workspaceID), data);
      await queryClient.invalidateQueries({ queryKey: keys.nexus });
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
        device.skills?.some(
          (s) => s.name.toLowerCase().includes(q) || s.description?.toLowerCase().includes(q),
        )
      ) {
        return true;
      }
      if (
        device.dynamic_mcp?.some(
          (m) =>
            m.name.toLowerCase().includes(q) ||
            m.display_name?.toLowerCase().includes(q) ||
            m.description?.toLowerCase().includes(q),
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

  return (
    <main className="mx-auto min-h-[calc(100dvh-3.5rem)] max-w-[1600px] space-y-7 px-5 py-8 md:min-h-dvh md:px-8">
      {/* 统一全局页面头部 */}
      <header className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-3xl font-semibold tracking-tight text-heading">
              {t("NexusDock")}
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
          <p className="mt-1 text-sm text-muted-foreground">
            {isConnected && connection.data?.authorized_subject
              ? `${t("已授权账号")}: ${connection.data.authorized_subject}`
              : t("管理连接的本地与远程设备节点，查看动态加载的 Skill 与 MCP 工具能力。")}
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
              <Cpu className="size-4" />
              {t("连接设备")}
            </Button>
          )}
        </div>
      </header>

      {/* 主体内容区 */}
      {connection.isPending ? (
        <div className="grid place-items-center py-20">
          <Spinner text={t("正在检查 NexusDock 连接状态…")} />
        </div>
      ) : connection.error ? (
        <ErrorView error={connection.error} onRetry={() => void connection.refetch()} />
      ) : !isConnected ? (
        /* 未连接：引导连接卡片 */
        <div className="grid place-items-center py-16">
          <div className="w-full max-w-md rounded-2xl border border-line bg-surface p-8 text-center shadow-2xs">
            <div className="mx-auto grid size-14 place-items-center rounded-2xl bg-primary/10 text-primary">
              <Cpu className="size-7" />
            </div>
            <h2 className="mt-4 text-lg font-semibold text-foreground">
              {t("连接 NexusDock 远程设备")}
            </h2>
            <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
              {t("通过第一方设备授权，CycleDock 可直接发现并调用您设备上的 Skill 技能、动态 MCP 服务与本地运行能力，无需手动配置 Token 或 MCP URL。")}
            </p>
            <div className="mt-6 flex flex-col gap-2.5">
              <Button
                variant="primary"
                disabled={connect.isPending}
                onClick={() => connect.mutate()}
                className="w-full justify-center"
              >
                <Cpu className="size-4" />
                {t("立即授权连接")}
              </Button>
              <p className="text-[11px] text-faint">
                {t("登录账号与设备授权账号可相互独立；授权时可在 NexusDock 确认具体身份。")}
              </p>
            </div>
          </div>
        </div>
      ) : fleet.isPending && !devices.length ? (
        /* 加载设备快照中 */
        <div className="grid place-items-center py-20">
          <Spinner text={t("正在连接 NexusDock 并拉取各设备能力快照…")} />
        </div>
      ) : fleet.error ? (
        <div className="mx-auto max-w-md rounded-2xl border border-line bg-surface p-6 shadow-2xs">
          <ErrorView error={fleet.error} onRetry={() => void fleet.refetch()} />
        </div>
      ) : (
        /* 已连接：搜索筛选栏 + Master-Detail 工作台 */
        <div className="space-y-4">
          {/* 筛选与检索栏 */}
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="relative min-w-[240px] max-w-sm flex-1">
              <Search className="pointer-events-none absolute left-3 top-2.5 size-4 text-muted-foreground/60" />
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t("搜索设备、Skill 或 MCP…")}
                className="h-9 w-full rounded-xl border border-line bg-surface pl-9 pr-3 text-xs text-foreground placeholder:text-muted-foreground/60 focus:border-line focus:outline-none focus:ring-1 focus:ring-primary shadow-2xs transition"
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
          <div className="grid grid-cols-1 gap-5 lg:grid-cols-[320px_minmax(0,1fr)]">
            {/* 左侧设备列表卡片 */}
            <div className="overflow-hidden rounded-2xl border border-line bg-surface p-2 shadow-2xs">
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
                    const skillsCount = device.skills?.length || 0;
                    const mcpCount = device.dynamic_mcp?.length || 0;

                    return (
                      <button
                        key={device.id}
                        type="button"
                        onClick={() => setSelectedDeviceId(device.id)}
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

                        <div className="mt-2 flex items-center gap-2 text-[11px] text-faint">
                          <span>{skillsCount} Skill</span>
                          <span>·</span>
                          <span>{mcpCount} MCP</span>
                          {device.arch && (
                            <>
                              <span>·</span>
                              <span>{device.arch}</span>
                            </>
                          )}
                        </div>
                      </button>
                    );
                  })
                )}
              </div>
            </div>

            {/* 右侧设备能力详情检查器 */}
            <div className="min-w-0 rounded-2xl border border-line bg-surface p-6 shadow-2xs space-y-6">
              {selectedDevice ? (
                <>
                  {/* 设备概览 Header */}
                  <div className="flex flex-wrap items-center justify-between gap-4 border-b border-line pb-5">
                    <div className="flex items-center gap-3.5">
                      <div
                        className={`grid size-12 place-items-center rounded-2xl ${
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

                      <div>
                        <div className="flex items-center gap-2.5">
                          <h2 className="text-xl font-semibold tracking-tight text-heading">
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
                    <div className="flex items-center gap-2.5 rounded-xl border border-amber-500/20 bg-amber-500/10 p-3.5 text-xs text-amber-700 dark:text-amber-400">
                      <AlertCircle className="size-4 shrink-0" />
                      <span>{t("同步提示")}: {selectedDevice.error}</span>
                    </div>
                  )}

                  {/* 1. Skill 技能清单 */}
                  <div className="space-y-3">
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-2">
                        <Sparkles className="size-4 text-primary" />
                        <h3 className="text-sm font-semibold text-foreground">
                          {t("Skill 技能清单")}
                        </h3>
                        <span className="rounded-full bg-muted px-2 py-0.2 text-xs font-medium text-muted-foreground">
                          {selectedDevice.skills?.length || 0}
                        </span>
                      </div>
                    </div>

                    {!selectedDevice.skills?.length ? (
                      <div className="rounded-xl border border-dashed border-line p-6 text-center text-xs text-muted-foreground">
                        {t("当前设备未加载任何 Skill 技能")}
                      </div>
                    ) : (
                      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                        {selectedDevice.skills.map((skill) => (
                          <div
                            key={skill.name}
                            className="flex flex-col justify-between rounded-xl border border-line bg-canvas/40 p-4 shadow-2xs hover:border-line/90 transition-all"
                          >
                            <div>
                              <div className="flex items-start justify-between gap-2">
                                <h4 className="text-xs font-semibold text-foreground truncate">
                                  {skill.name}
                                </h4>
                                {skill.source_type && (
                                  <span
                                    className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${
                                      skill.source_type === "managed"
                                        ? "bg-primary/10 text-primary border border-primary/20"
                                        : skill.source_type === "shared"
                                          ? "bg-purple-500/10 text-purple-600 dark:text-purple-400 border border-purple-500/20"
                                          : "bg-muted text-muted-foreground"
                                    }`}
                                  >
                                    {skill.source_type === "managed"
                                      ? t("官方内置")
                                      : skill.source_type === "shared"
                                        ? t("共享技能")
                                        : skill.source_type}
                                  </span>
                                )}
                              </div>
                              <p className="mt-1.5 text-xs text-muted-foreground line-clamp-2 leading-relaxed">
                                {skill.description || t("暂无技能描述")}
                              </p>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>

                  {/* 2. 动态 MCP 服务 */}
                  <div className="space-y-3">
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-2">
                        <Plug className="size-4 text-purple-600 dark:text-purple-400" />
                        <h3 className="text-sm font-semibold text-foreground">
                          {t("动态 MCP 服务")}
                        </h3>
                        <span className="rounded-full bg-muted px-2 py-0.2 text-xs font-medium text-muted-foreground">
                          {selectedDevice.dynamic_mcp?.length || 0}
                        </span>
                      </div>
                    </div>

                    {!selectedDevice.dynamic_mcp?.length ? (
                      <div className="rounded-xl border border-dashed border-line p-6 text-center text-xs text-muted-foreground">
                        {t("当前设备未配置或运行动态 MCP 服务")}
                      </div>
                    ) : (
                      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                        {selectedDevice.dynamic_mcp.map((mcp) => (
                          <div
                            key={mcp.name}
                            className="flex flex-col justify-between rounded-xl border border-line bg-canvas/40 p-4 shadow-2xs hover:border-line/90 transition-all"
                          >
                            <div>
                              <div className="flex items-start justify-between gap-2">
                                <h4 className="text-xs font-semibold text-foreground truncate">
                                  {mcp.display_name || mcp.name}
                                </h4>
                                <span
                                  className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${
                                    mcp.status === "active" || mcp.status === "running"
                                      ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20"
                                      : "bg-muted text-muted-foreground"
                                  }`}
                                >
                                  {mcp.status || t("活跃")}
                                </span>
                              </div>
                              <p className="mt-1.5 text-xs text-muted-foreground line-clamp-2 leading-relaxed">
                                {mcp.description || t("无服务描述")}
                              </p>
                            </div>

                            {typeof mcp.tool_count === "number" && (
                              <div className="mt-3 pt-2.5 border-t border-line/60 flex items-center justify-between text-[11px] text-muted-foreground">
                                <span>{t("包含工具")}</span>
                                <span className="font-medium text-foreground">
                                  {mcp.tool_count} {t("个")}
                                </span>
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    )}
                  </div>

                  {/* 3. 内置底层能力 */}
                  {selectedDevice.capabilities && selectedDevice.capabilities.length > 0 && (
                    <div className="rounded-xl border border-line bg-canvas/30 p-4 shadow-2xs">
                      <button
                        type="button"
                        onClick={() => setShowCapabilities((v) => !v)}
                        className="flex w-full items-center justify-between text-xs font-medium text-foreground hover:text-primary transition-colors"
                      >
                        <div className="flex items-center gap-2">
                          <Wrench className="size-3.5 text-muted-foreground" />
                          <span>{t("内置底层能力")} ({selectedDevice.capabilities.length})</span>
                        </div>
                        {showCapabilities ? (
                          <ChevronUp className="size-4 text-muted-foreground" />
                        ) : (
                          <ChevronDown className="size-4 text-muted-foreground" />
                        )}
                      </button>

                      {showCapabilities && (
                        <div className="mt-3 pt-3 border-t border-line/60 flex flex-wrap gap-1.5">
                          {selectedDevice.capabilities.map((cap) => (
                            <span
                              key={cap}
                              className="rounded-md border border-line bg-muted/60 px-2.5 py-1 font-mono text-[10.5px] text-muted-foreground"
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
