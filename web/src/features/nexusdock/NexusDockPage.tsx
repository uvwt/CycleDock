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
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import { Spinner, ErrorView, Button } from "../../shared/ui/components";

export default function NexusDockPage() {
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
    onError: (e) => toast.error("发起 NexusDock 授权失败：" + String(e)),
  });

  const disconnect = useMutation({
    mutationFn: api.nexusDisconnect,
    onSuccess: async () => {
      toast.success("已断开 NexusDock 设备连接");
      await queryClient.invalidateQueries({ queryKey: keys.nexus });
      await queryClient.invalidateQueries({ queryKey: keys.nexusFleet(workspaceID) });
      setSelectedDeviceId(null);
    },
    onError: (e) => toast.error("断开连接失败：" + String(e)),
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
      toast.success("已更新 NexusDock 设备与运行能力");
    } catch (err) {
      toast.error("刷新失败：" + String(err));
    } finally {
      setIsRefreshing(false);
    }
  }

  function handleCopy(id: string) {
    void navigator.clipboard.writeText(id);
    setCopiedId(id);
    toast.success("已复制节点 ID");
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
      if (device.skills?.some((s) => s.name.toLowerCase().includes(q) || s.description?.toLowerCase().includes(q))) {
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

  // 选中的设备 ID：若未显式选择或当前选中项已被搜索过滤，自动取第一台在线设备（或首台）
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
    <div className="flex h-full w-full min-w-0 flex-col overflow-hidden bg-background">
      {/* 顶部导航与连接控制栏 */}
      <header className="flex shrink-0 items-center justify-between gap-4 border-b border-line bg-surface/80 px-4 py-3 backdrop-blur sm:px-6">
        <div className="flex items-center gap-3">
          <div className="grid size-9 place-items-center rounded-xl bg-primary/10 text-primary">
            <Cpu className="size-5" />
          </div>
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-base font-semibold text-foreground">NexusDock</h1>
              {isConnected ? (
                <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2 py-0.5 text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
                  <span className="size-1.5 rounded-full bg-emerald-500 animate-pulse" />
                  已连接
                </span>
              ) : (
                <span className="inline-flex items-center gap-1.5 rounded-full border border-amber-500/20 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-600 dark:text-amber-400">
                  未连接
                </span>
              )}
            </div>
            <p className="text-xs text-muted-foreground truncate max-w-md">
              {isConnected && connection.data?.authorized_subject
                ? `授权账号: ${connection.data.authorized_subject}`
                : "远程设备集群 · 动态 Skill · 本地 MCP"}
            </p>
          </div>
        </div>

        {/* 顶部操作区 */}
        <div className="flex items-center gap-2">
          {isConnected ? (
            <>
              <button
                type="button"
                onClick={handleRefresh}
                disabled={isRefreshing || fleet.isPending}
                className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-line bg-surface px-3 text-xs font-medium text-foreground shadow-2xs transition hover:bg-muted active:scale-95 disabled:opacity-50"
              >
                <RefreshCw
                  className={`size-3.5 ${isRefreshing || fleet.isFetching ? "animate-spin" : ""}`}
                />
                <span className="hidden sm:inline">刷新</span>
              </button>

              {connection.data?.manage_url && (
                <a
                  href={connection.data.manage_url}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-line bg-surface px-3 text-xs font-medium text-foreground shadow-2xs transition hover:bg-muted active:scale-95"
                >
                  <ExternalLink className="size-3.5 text-muted-foreground" />
                  <span className="hidden sm:inline">管理设备</span>
                </a>
              )}

              <Button
                variant="danger"
                size="sm"
                disabled={disconnect.isPending}
                onClick={() => {
                  if (window.confirm("确定要断开 NexusDock 设备连接吗？")) {
                    disconnect.mutate();
                  }
                }}
                className="h-8 px-3 text-xs"
              >
                <Unlink className="size-3.5" />
                <span className="hidden sm:inline">断开连接</span>
              </Button>
            </>
          ) : (
            <Button
              variant="primary"
              size="sm"
              disabled={connect.isPending}
              onClick={() => connect.mutate()}
              className="h-8 px-3 text-xs"
            >
              <Cpu className="size-3.5" />
              连接 NexusDock 设备
            </Button>
          )}
        </div>
      </header>

      {/* 主体区域 */}
      <main className="flex min-h-0 flex-1 overflow-hidden">
        {connection.isPending ? (
          <div className="flex flex-1 items-center justify-center p-8">
            <Spinner text="正在检查 NexusDock 连接状态…" />
          </div>
        ) : !isConnected ? (
          /* 未连接时的引导面板 */
          <div className="flex flex-1 items-center justify-center p-6">
            <div className="w-full max-w-md rounded-2xl border border-line bg-surface p-8 text-center shadow-xs">
              <div className="mx-auto grid size-14 place-items-center rounded-2xl bg-primary/10 text-primary">
                <Cpu className="size-7" />
              </div>
              <h2 className="mt-4 text-lg font-semibold text-foreground">
                连接 NexusDock 远程设备
              </h2>
              <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
                通过第一方设备授权，CycleDock 可直接发现并调用您设备上的 Skill 技能、动态 MCP 服务与本地运行能力，无需手动配置 Token 或 MCP URL。
              </p>
              <div className="mt-6 flex flex-col gap-2">
                <Button
                  variant="primary"
                  disabled={connect.isPending}
                  onClick={() => connect.mutate()}
                  className="w-full justify-center"
                >
                  <Cpu className="size-4" />
                  立即授权连接
                </Button>
                <p className="text-[11px] text-faint">
                  登录账号与设备授权账号可相互独立；授权时可在 NexusDock 确认具体身份。
                </p>
              </div>
            </div>
          </div>
        ) : fleet.isPending && !devices.length ? (
          /* 加载设备数据中 */
          <div className="flex flex-1 items-center justify-center p-8">
            <Spinner text="正在连接 NexusDock 并拉取各设备能力快照…" />
          </div>
        ) : fleet.error ? (
          <div className="flex flex-1 items-center justify-center p-6">
            <div className="w-full max-w-md rounded-2xl border border-line bg-surface p-6 shadow-xs">
              <ErrorView error={fleet.error} onRetry={() => void fleet.refetch()} />
            </div>
          </div>
        ) : (
          /* 已连接：左侧设备主列表 + 右侧能力详情检查器 (Master-Detail) */
          <div className="flex flex-1 min-h-0 w-full overflow-hidden">
            {/* 左侧设备列表栏 */}
            <aside className="flex w-72 sm:w-80 shrink-0 flex-col border-r border-line bg-canvas/40">
              {/* 搜索与分类 Tab */}
              <div className="p-3 border-b border-line space-y-2">
                <div className="relative">
                  <Search className="pointer-events-none absolute left-2.5 top-2.5 size-3.5 text-muted-foreground" />
                  <input
                    type="text"
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                    placeholder="搜索设备、Skill 或 MCP..."
                    className="h-8 w-full rounded-lg border border-line bg-surface pl-8 pr-3 text-xs text-foreground placeholder:text-muted-foreground focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary shadow-2xs"
                  />
                </div>

                <div className="flex items-center gap-1 rounded-lg border border-line/70 bg-surface/80 p-0.5 text-[11px]">
                  <button
                    type="button"
                    onClick={() => setStatusFilter("all")}
                    className={`flex-1 rounded-md py-1 text-center font-medium transition ${
                      statusFilter === "all"
                        ? "bg-muted text-foreground font-semibold shadow-2xs"
                        : "text-muted-foreground hover:text-foreground"
                    }`}
                  >
                    全部 ({totalCount})
                  </button>
                  <button
                    type="button"
                    onClick={() => setStatusFilter("online")}
                    className={`flex-1 rounded-md py-1 text-center font-medium transition ${
                      statusFilter === "online"
                        ? "bg-muted text-foreground font-semibold shadow-2xs"
                        : "text-muted-foreground hover:text-foreground"
                    }`}
                  >
                    在线 ({onlineCount})
                  </button>
                  <button
                    type="button"
                    onClick={() => setStatusFilter("offline")}
                    className={`flex-1 rounded-md py-1 text-center font-medium transition ${
                      statusFilter === "offline"
                        ? "bg-muted text-foreground font-semibold shadow-2xs"
                        : "text-muted-foreground hover:text-foreground"
                    }`}
                  >
                    离线 ({offlineCount})
                  </button>
                </div>
              </div>

              {/* 设备列表 */}
              <div className="scrollbar-thin flex-1 overflow-y-auto p-2 space-y-1">
                {filteredDevices.length === 0 ? (
                  <div className="py-12 text-center">
                    <Monitor className="mx-auto size-7 text-muted-foreground/60" />
                    <p className="mt-2 text-xs text-muted-foreground">没有找到匹配的设备</p>
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
                        className={`group flex w-full flex-col rounded-xl p-2.5 text-left transition-all ${
                          isSelected
                            ? "bg-surface text-foreground font-medium shadow-2xs border border-primary/30 ring-1 ring-primary/20"
                            : "text-muted-foreground hover:bg-surface/70 hover:text-foreground border border-transparent"
                        }`}
                      >
                        <div className="flex items-center justify-between gap-1.5 w-full">
                          <div className="flex items-center gap-2 min-w-0">
                            {/* 在线指示原点 */}
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
                            <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-normal bg-muted text-muted-foreground">
                              {device.os}
                            </span>
                          )}
                        </div>

                        <div className="mt-1.5 flex items-center gap-2 text-[11px] text-faint">
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
            </aside>

            {/* 右侧设备能力详情面板 (Detail Inspector) */}
            <section className="flex flex-1 min-w-0 flex-col overflow-y-auto bg-surface/20">
              {selectedDevice ? (
                <div className="mx-auto w-full max-w-4xl p-4 sm:p-6 space-y-6">
                  {/* 设备概览 Hero 区域 */}
                  <div className="rounded-2xl border border-line bg-surface p-5 shadow-2xs">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <div className="flex items-center gap-3">
                        <div
                          className={`grid size-11 place-items-center rounded-2xl ${
                            selectedDevice.online
                              ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
                              : "bg-muted text-muted-foreground"
                          }`}
                        >
                          {selectedDevice.os?.toLowerCase().includes("android") ? (
                            <Smartphone className="size-5.5" />
                          ) : selectedDevice.os?.toLowerCase().includes("linux") ? (
                            <Terminal className="size-5.5" />
                          ) : selectedDevice.os?.toLowerCase().includes("darwin") ||
                            selectedDevice.os?.toLowerCase().includes("mac") ? (
                            <Laptop className="size-5.5" />
                          ) : (
                            <Monitor className="size-5.5" />
                          )}
                        </div>

                        <div>
                          <div className="flex items-center gap-2">
                            <h2 className="text-lg font-bold text-foreground">
                              {selectedDevice.name}
                            </h2>
                            {selectedDevice.online ? (
                              <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/10 px-2 py-0.5 text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
                                <span className="size-1.5 rounded-full bg-emerald-500 animate-pulse" />
                                在线
                              </span>
                            ) : (
                              <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                                离线
                              </span>
                            )}
                          </div>

                          <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                            {selectedDevice.os && <span>系统: {selectedDevice.os}</span>}
                            {selectedDevice.arch && <span>架构: {selectedDevice.arch}</span>}
                            {selectedDevice.agent_version && <span>版本: {selectedDevice.agent_version}</span>}
                          </div>
                        </div>
                      </div>

                      {/* 节点 ID 复制 */}
                      <button
                        type="button"
                        onClick={() => handleCopy(selectedDevice.id)}
                        title="复制节点 ID"
                        className="inline-flex items-center gap-1.5 rounded-lg border border-line bg-canvas/60 px-2.5 py-1.5 text-xs text-muted-foreground transition hover:bg-muted hover:text-foreground shadow-2xs active:scale-95"
                      >
                        {copiedId === selectedDevice.id ? (
                          <>
                            <Check className="size-3.5 text-emerald-600" />
                            <span className="font-mono text-emerald-600">已复制</span>
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
                      <div className="mt-3 flex items-center gap-2 rounded-xl border border-amber-500/20 bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">
                        <AlertCircle className="size-4 shrink-0" />
                        <span>同步提示: {selectedDevice.error}</span>
                      </div>
                    )}
                  </div>

                  {/* 1. Skill 技能卡片列表 */}
                  <div className="space-y-3">
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-2">
                        <Sparkles className="size-4 text-primary" />
                        <h3 className="text-sm font-semibold text-foreground">
                          Skill 技能清单
                        </h3>
                        <span className="rounded-full bg-muted px-2 py-0.2 text-xs font-medium text-muted-foreground">
                          {selectedDevice.skills?.length || 0}
                        </span>
                      </div>
                    </div>

                    {!selectedDevice.skills?.length ? (
                      <div className="rounded-xl border border-dashed border-line p-6 text-center text-xs text-muted-foreground">
                        当前设备未加载任何 Skill 技能
                      </div>
                    ) : (
                      <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
                        {selectedDevice.skills.map((skill) => (
                          <div
                            key={skill.name}
                            className="flex flex-col justify-between rounded-xl border border-line bg-surface p-3.5 shadow-2xs hover:border-line/90 transition-colors"
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
                                      ? "官方内置"
                                      : skill.source_type === "shared"
                                        ? "共享技能"
                                        : skill.source_type}
                                  </span>
                                )}
                              </div>
                              <p className="mt-1.5 text-xs text-muted-foreground line-clamp-2 leading-relaxed">
                                {skill.description || "暂无技能描述"}
                              </p>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>

                  {/* 2. 动态 MCP 服务卡片列表 */}
                  <div className="space-y-3">
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-2">
                        <Plug className="size-4 text-purple-600 dark:text-purple-400" />
                        <h3 className="text-sm font-semibold text-foreground">
                          动态 MCP 服务
                        </h3>
                        <span className="rounded-full bg-muted px-2 py-0.2 text-xs font-medium text-muted-foreground">
                          {selectedDevice.dynamic_mcp?.length || 0}
                        </span>
                      </div>
                    </div>

                    {!selectedDevice.dynamic_mcp?.length ? (
                      <div className="rounded-xl border border-dashed border-line p-6 text-center text-xs text-muted-foreground">
                        当前设备未配置或运行动态 MCP 服务
                      </div>
                    ) : (
                      <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
                        {selectedDevice.dynamic_mcp.map((mcp) => (
                          <div
                            key={mcp.name}
                            className="flex flex-col justify-between rounded-xl border border-line bg-surface p-3.5 shadow-2xs hover:border-line/90 transition-colors"
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
                                  {mcp.status || "活跃"}
                                </span>
                              </div>
                              <p className="mt-1.5 text-xs text-muted-foreground line-clamp-2 leading-relaxed">
                                {mcp.description || "无服务描述"}
                              </p>
                            </div>

                            {typeof mcp.tool_count === "number" && (
                              <div className="mt-3 pt-2 border-t border-line/60 flex items-center justify-between text-[11px] text-muted-foreground">
                                <span>包含工具</span>
                                <span className="font-medium text-foreground">
                                  {mcp.tool_count} 个
                                </span>
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    )}
                  </div>

                  {/* 3. 设备内置基础工具与能力 (Capabilities) */}
                  {selectedDevice.capabilities && selectedDevice.capabilities.length > 0 && (
                    <div className="rounded-xl border border-line bg-surface p-4 shadow-2xs">
                      <button
                        type="button"
                        onClick={() => setShowCapabilities((v) => !v)}
                        className="flex w-full items-center justify-between text-xs font-medium text-foreground hover:text-primary transition-colors"
                      >
                        <div className="flex items-center gap-2">
                          <Wrench className="size-3.5 text-muted-foreground" />
                          <span>内置底层能力 ({selectedDevice.capabilities.length})</span>
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
                              className="rounded-md border border-line bg-muted/60 px-2 py-0.5 font-mono text-[10.5px] text-muted-foreground"
                            >
                              {cap}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              ) : (
                <div className="flex flex-1 items-center justify-center p-8 text-center text-xs text-muted-foreground">
                  请选择左侧设备以查看详情
                </div>
              )}
            </section>
          </div>
        )}
      </main>
    </div>
  );
}
