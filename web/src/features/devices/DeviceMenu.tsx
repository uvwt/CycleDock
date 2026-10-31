import { useDeviceSelection } from "./device-selection";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Check,
  ChevronDown,
  LoaderCircle,
  Monitor,
  RefreshCw,
  Settings,
} from "lucide-react";
import { api, APIError } from "../../shared/api/client";
import { keys } from "../../app/query";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../../shared/ui/dropdown-menu";

export function DeviceMenu() {
  const { target, selectTarget } = useDeviceSelection();
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const me = useQuery({ queryKey: keys.me, queryFn: api.me });
  const devices = useQuery({
    queryKey: keys.nexusDevices(me.data?.workspace.id || ""),
    queryFn: api.nexusDevices,
    enabled: Boolean(me.data?.workspace.id),
    retry: false,
    // 收起时维持在线数量；展开后加快刷新，后台标签页不轮询。
    refetchInterval: (query) =>
      query.state.error || !query.state.data?.connected
        ? false
        : open
          ? 10_000
          : 30_000,
  });
  const owner = me.data?.role === "owner";
  const reconnect =
    devices.error instanceof APIError &&
    devices.error.code === "nexusdock_reconnect_required";
  const list = [...(devices.data?.devices || [])].sort(
    (a, b) =>
      Number(b.online) - Number(a.online) || a.name.localeCompare(b.name),
  );
  const selectedOnline = list.some(
    (device) => device.id === target?.node_id && device.online,
  );
  const online = list.filter((device) => device.online).length;
  const connected = devices.data?.connected && !devices.error;

  return (
    <DropdownMenu
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) void devices.refetch();
      }}
    >
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={t("设备在线列表")}
          title={t("设备在线列表")}
          className="flex h-8 shrink-0 items-center gap-1.5 rounded-lg border border-line bg-surface/90 px-2.5 text-xs text-muted-foreground shadow-sm backdrop-blur transition hover:bg-muted data-[state=open]:bg-muted"
        >
          <Monitor className="size-3.5" />
          <span className="max-w-16 truncate sm:max-w-40">
            {target ? target.name || target.node_id : t("设备")}
          </span>
          {devices.isPending ? (
            <LoaderCircle className="size-3 animate-spin" />
          ) : (
            <>
              <span
                aria-hidden="true"
                className={`size-1.5 rounded-full ${devices.error || (target && !selectedOnline) ? "bg-amber-500" : connected && online ? "bg-emerald-500" : "bg-placeholder"}`}
              />
              {connected && !target && (
                <span className="tabular-nums">{online}</span>
              )}
            </>
          )}
          <ChevronDown className="size-3 text-faint" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        sideOffset={8}
        collisionPadding={12}
        className="w-80 max-w-[calc(100vw-1.5rem)] rounded-xl p-1.5 shadow-lg"
      >
        <DropdownMenuLabel className="flex items-center justify-between px-2.5 py-2">
          <span className="text-sm font-semibold">{t("NexusDock 设备")}</span>
          {connected && (
            <span className="text-xs font-normal text-faint">
              {t("{{online}} / {{total}} 在线", { online, total: list.length })}
            </span>
          )}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={() => selectTarget(null)}
          className="cursor-pointer rounded-lg px-2.5 py-2 text-xs"
        >
          <span className="flex-1">{t("不指定设备")}</span>
          {!target && <Check className="size-3.5" />}
        </DropdownMenuItem>
        <p className="px-2.5 pb-2 text-[11px] leading-5 text-faint">
          {t("选择仅作用于之后发送的消息。")}
        </p>
        {target && !selectedOnline && (
          <p className="px-2.5 pb-2 text-[11px] text-amber-600 dark:text-amber-400">
            {t("所选设备当前不可用，可以切换或取消指定。")}
          </p>
        )}
        {devices.isPending ? (
          <p
            className="flex items-center justify-center gap-2 px-3 py-8 text-xs text-faint"
            role="status"
          >
            <LoaderCircle className="size-4 animate-spin" />
            {t("正在读取设备…")}
          </p>
        ) : devices.error ? (
          <p
            className="px-3 py-6 text-center text-xs leading-5 text-muted-foreground"
            role="status"
          >
            {t(
              reconnect
                ? "设备授权已过期，请重新连接。"
                : "设备状态暂时不可用，请重试。",
            )}
          </p>
        ) : !devices.data?.available ? (
          <p className="px-3 py-6 text-center text-xs leading-5 text-muted-foreground">
            {t("当前部署尚未启用 NexusDock 设备连接。")}
          </p>
        ) : !devices.data.connected ? (
          <p className="px-3 py-6 text-center text-xs leading-5 text-muted-foreground">
            {t(
              owner
                ? "连接 NexusDock，查看设备在线状态。"
                : "请联系工作空间所有者连接 NexusDock。",
            )}
          </p>
        ) : !list.length ? (
          <p className="px-3 py-6 text-center text-xs text-muted-foreground">
            {t("此授权账号下暂无设备。")}
          </p>
        ) : (
          <ul
            aria-label={t("设备列表")}
            className="max-h-72 overflow-y-auto px-1 py-1"
          >
            {list.map((device) => (
              <li key={device.id}>
                <DropdownMenuItem
                  disabled={!device.online}
                  onSelect={() =>
                    selectTarget({
                      node_id: device.id,
                      name: device.name || device.id,
                    })
                  }
                  className="flex cursor-pointer items-center gap-3 rounded-lg px-2 py-2.5 data-[disabled]:opacity-60"
                >
                  <div className="grid size-9 shrink-0 place-items-center rounded-lg bg-canvas text-faint">
                    <Monitor className="size-4" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <p
                      className="truncate text-sm text-foreground"
                      title={device.name || device.id}
                    >
                      {device.name || device.id}
                    </p>
                    <p className="mt-0.5 truncate text-[11px] text-faint">
                      {[device.os, device.arch].filter(Boolean).join(" · ") ||
                        t("系统未知")}
                    </p>
                  </div>
                  <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                    <span
                      aria-hidden="true"
                      className={`size-1.5 rounded-full ${device.online ? "bg-emerald-500" : "bg-placeholder"}`}
                    />
                    {t(device.online ? "在线" : "离线")}
                  </span>
                  {target?.node_id === device.id && (
                    <Check className="size-3.5 shrink-0 text-sky-700 dark:text-sky-400" />
                  )}
                </DropdownMenuItem>
              </li>
            ))}
          </ul>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          disabled={devices.isFetching}
          onSelect={(event) => {
            event.preventDefault();
            void devices.refetch();
          }}
          className="cursor-pointer rounded-lg px-2.5 py-2 text-xs"
        >
          <RefreshCw
            className={`size-3.5 ${devices.isFetching ? "animate-spin" : ""}`}
          />
          {t("刷新设备状态")}
        </DropdownMenuItem>
        {owner && devices.data?.available !== false && (
          <DropdownMenuItem
            onSelect={() => navigate("/settings/nexusdock")}
            className="cursor-pointer rounded-lg px-2.5 py-2 text-xs"
          >
            <Settings className="size-3.5" />
            {t(
              reconnect || !devices.data?.connected
                ? "连接设备"
                : "设备连接设置",
            )}
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
