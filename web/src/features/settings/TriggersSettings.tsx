import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  AlarmClock,
  Check,
  Copy,
  ExternalLink,
  History,
  KeyRound,
  Pencil,
  Plus,
  Radar,
  RefreshCw,
  ShieldAlert,
  Trash2,
  Webhook,
  XCircle,
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import type {
  Card,
  CardWatcher,
  Project,
  TriggerDelivery,
  TriggerProvider,
  TriggerRoutingMode,
  TriggerWithSecretResponse,
  WorkspaceTrigger,
} from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import { Badge } from "../../shared/ui/badge";
import { Button } from "../../shared/ui/button";
import { Input } from "../../shared/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../shared/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../shared/ui/alert-dialog";
import { Field, Select, Switch } from "../../shared/ui/components";

function CardSurface({
  children,
  className = "",
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={`min-w-0 overflow-hidden rounded-xl border border-line bg-surface p-4 shadow-2xs ${className}`}
    >
      {children}
    </div>
  );
}

function SectionHead({
  title,
  description,
}: {
  title: string;
  description?: string;
}) {
  return (
    <div className="border-t border-line/60 pt-3">
      <h3 className="text-xs font-semibold text-foreground">{title}</h3>
      {description && (
        <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
      )}
    </div>
  );
}

function StatusPill({
  tone,
  label,
}: {
  tone: "ok" | "warn" | "bad" | "neutral";
  label: string;
}) {
  const toneClass =
    tone === "ok"
      ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
      : tone === "warn"
        ? "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300"
        : tone === "bad"
          ? "border-destructive/30 bg-destructive/10 text-destructive"
          : "border-line bg-muted text-muted-foreground";
  return (
    <span
      className={`inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-medium ${toneClass}`}
    >
      {label}
    </span>
  );
}
import {
  DEFAULT_GENERIC_SIGNATURE_HEADER,
  defaultAuthModeForProvider,
  emptyTriggerFormValues,
  formatRemainingCountdown,
  getCardRoute,
  toCreateTriggerInput,
  toUpdateTriggerInput,
  triggerFormSchema,
  triggerToFormValues,
  type TriggerFormValues,
} from "./trigger-form";

type SectionTab = "triggers" | "watchers" | "deliveries";

const providerLabel: Record<TriggerProvider, string> = {
  github: "GitHub",
  circleci: "CircleCI",
  generic: "Generic Webhook",
};

const routingModeLabel: Record<TriggerRoutingMode, string> = {
  match_watchers: "唤醒挂起等待的卡片",
  fixed_card: "触发指定卡片",
  create_todo: "自动创建 Todo 卡片",
};

const deliveryStatusConfig: Record<
  string,
  { label: string; tone: "ok" | "warn" | "bad" | "neutral" }
> = {
  triggered: { label: "已命中", tone: "ok" },
  filtered: { label: "已过滤", tone: "neutral" },
  ignored: { label: "未命中", tone: "neutral" },
  cooldown: { label: "冷却跳过", tone: "warn" },
  rejected: { label: "验签拒绝", tone: "bad" },
};

const watcherStatusConfig: Record<
  string,
  { label: string; tone: "ok" | "warn" | "bad" | "neutral" }
> = {
  armed: { label: "等待中", tone: "warn" },
  triggered: { label: "已唤醒", tone: "ok" },
  expired: { label: "已超时兜底", tone: "neutral" },
  cancelled: { label: "已取消", tone: "neutral" },
};

function buildWebhookURL(webhookPath: string): string {
  if (typeof window === "undefined" || !window.location?.origin) {
    return webhookPath;
  }
  return `${window.location.origin}${webhookPath}`;
}

function formatDateTime(value?: string): string {
  if (!value) return "—";
  return new Date(value).toLocaleString([], {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function summarizeFilter(trigger: WorkspaceTrigger, t: (k: string) => string) {
  const f = trigger.filter_config || {};
  const parts: string[] = [];
  if (f.events?.length) parts.push(`${t("事件")}: ${f.events.join(", ")}`);
  if (f.actions?.length) parts.push(`${t("动作")}: ${f.actions.join(", ")}`);
  if (f.conclusions?.length) {
    parts.push(`${t("结论")}: ${f.conclusions.join(", ")}`);
  }
  if (f.branches?.length) parts.push(`${t("分支")}: ${f.branches.join(", ")}`);
  if (f.repositories?.length) {
    parts.push(`${t("仓库")}: ${f.repositories.join(", ")}`);
  }
  if (f.match_key_path) parts.push(`Match Key: ${f.match_key_path}`);
  const matchCount = Object.keys(f.json_matches || {}).length;
  if (matchCount > 0) {
    parts.push(`JSON(${matchCount})`);
  }
  return parts;
}

export function TriggersSettings() {
  const { t, i18n } = useTranslation();
  const isEnglish = i18n.resolvedLanguage === "en";
  const navigate = useNavigate();

  const [tab, setTab] = useState<SectionTab>("triggers");
  const [selectedTriggerFilter, setSelectedTriggerFilter] = useState("");
  const [watcherArmedOnly, setWatcherArmedOnly] = useState(true);
  const [watcherProjectFilter, setWatcherProjectFilter] = useState("");
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  // 新建 / 编辑对话框与一次性密钥展示对话框
  const [formOpen, setFormOpen] = useState(false);
  const [editingTrigger, setEditingTrigger] = useState<WorkspaceTrigger | null>(
    null,
  );
  const [secretReveal, setSecretReveal] =
    useState<TriggerWithSecretResponse | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<WorkspaceTrigger | null>(
    null,
  );
  const [rotateTarget, setRotateTarget] = useState<WorkspaceTrigger | null>(
    null,
  );

  const projectsQuery = useQuery({
    queryKey: keys.projects,
    queryFn: () => api.projects(),
  });
  const projects = projectsQuery.data || [];

  const triggersQuery = useQuery({
    queryKey: keys.triggers,
    queryFn: () => api.triggers(),
  });
  const triggers = triggersQuery.data || [];

  const armedWatchersQuery = useQuery({
    queryKey: keys.workspaceWatchers(watcherArmedOnly, watcherProjectFilter),
    queryFn: () =>
      api.workspaceWatchers({
        armedOnly: watcherArmedOnly,
        projectID: watcherProjectFilter || undefined,
        limit: 100,
      }),
    refetchInterval: tab === "watchers" ? 15_000 : false,
  });
  const watchers = armedWatchersQuery.data;
  const watcherList = watchers ?? [];
  const armedCount = useMemo(
    () => (watchers || []).filter((w) => w.status === "armed").length,
    [watchers],
  );

  const deliveriesQuery = useQuery({
    queryKey: keys.triggerDeliveries(selectedTriggerFilter),
    queryFn: () =>
      api.triggerDeliveries(selectedTriggerFilter || undefined, 50),
    enabled: tab === "deliveries",
  });
  const deliveries = deliveriesQuery.data || [];

  useEffect(() => {
    if (tab !== "watchers" || armedCount === 0) return;
    const timer = window.setInterval(() => setNowMs(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, [tab, armedCount]);

  async function copyText(key: string, text: string, successLabel: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedKey(key);
      toast.success(successLabel);
      window.setTimeout(
        () => setCopiedKey((cur) => (cur === key ? null : cur)),
        1500,
      );
    } catch {
      toast.error(t("复制失败，请手动复制"));
    }
  }

  const toggleMutation = useMutation({
    mutationFn: (trigger: WorkspaceTrigger) =>
      api.updateTrigger(trigger.id, { enabled: !trigger.enabled }),
    onSuccess: (updated) => {
      queryClient.setQueryData<WorkspaceTrigger[]>(keys.triggers, (old) =>
        old ? old.map((item) => (item.id === updated.id ? updated : item)) : old,
      );
      toast.success(updated.enabled ? t("触发器已启用") : t("触发器已停用"));
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("更新触发器失败"));
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (trigger: WorkspaceTrigger) => api.deleteTrigger(trigger.id),
    onSuccess: (_, trigger) => {
      queryClient.setQueryData<WorkspaceTrigger[]>(keys.triggers, (old) =>
        old ? old.filter((item) => item.id !== trigger.id) : old,
      );
      if (selectedTriggerFilter === trigger.id) {
        setSelectedTriggerFilter("");
      }
      void queryClient.invalidateQueries({ queryKey: ["trigger-deliveries"] });
      setDeleteTarget(null);
      toast.success(t("触发器已删除"));
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("删除触发器失败"));
    },
  });

  const rotateMutation = useMutation({
    mutationFn: (trigger: WorkspaceTrigger) =>
      api.rotateTriggerSecret(trigger.id),
    onSuccess: (res) => {
      queryClient.setQueryData<WorkspaceTrigger[]>(keys.triggers, (old) =>
        old
          ? old.map((item) => (item.id === res.trigger.id ? res.trigger : item))
          : old,
      );
      setRotateTarget(null);
      setSecretReveal(res);
      toast.success(t("Webhook 密钥已轮换"));
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("轮换密钥失败"));
    },
  });

  const cancelWatcherMutation = useMutation({
    mutationFn: (watcher: CardWatcher) =>
      api.cancelCardWatcher(watcher.card_id, watcher.id, "manual_cancelled"),
    onSuccess: (updated) => {
      void queryClient.invalidateQueries({ queryKey: ["workspace-watchers"] });
      void queryClient.invalidateQueries({
        queryKey: keys.cardWatchers(updated.card_id),
      });
      toast.success(t("已取消该等待项"));
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("取消等待失败"));
    },
  });

  function openCreate() {
    setEditingTrigger(null);
    setFormOpen(true);
  }

  function openEdit(trigger: WorkspaceTrigger) {
    setEditingTrigger(trigger);
    setFormOpen(true);
  }

  function openDeliveriesFor(triggerID: string) {
    setSelectedTriggerFilter(triggerID);
    setTab("deliveries");
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h2 className="text-lg font-semibold text-foreground">
            {t("触发器与等待")}
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {t(
              "配置 GitHub / CircleCI / 通用 Webhook 触发器，排查投递日志，并管理各卡片挂起的事件等待与定时自唤醒。",
            )}
          </p>
        </div>
        <Button onClick={openCreate} className="w-full sm:w-auto shrink-0">
          <Plus className="size-4" />
          {t("新建触发器")}
        </Button>
      </div>

      {/* 子视图切换栏：桌面与移动端均可折行/自适应，避免横向溢出 */}
      <div className="flex flex-wrap items-center gap-1.5 rounded-xl border border-line bg-surface p-1">
        <button
          type="button"
          onClick={() => setTab("triggers")}
          className={`inline-flex flex-1 sm:flex-none items-center justify-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium transition cursor-pointer ${
            tab === "triggers"
              ? "bg-foreground text-background shadow-xs"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          <Webhook className="size-3.5 shrink-0" />
          <span>{t("Webhook 触发器")}</span>
          <span className="text-xs opacity-75">({triggers.length})</span>
        </button>

        <button
          type="button"
          onClick={() => setTab("watchers")}
          className={`inline-flex flex-1 sm:flex-none items-center justify-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium transition cursor-pointer ${
            tab === "watchers"
              ? "bg-foreground text-background shadow-xs"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          <Radar className="size-3.5 shrink-0" />
          <span>{t("挂起等待与唤醒")}</span>
          {armedCount > 0 && (
            <span className="rounded-full bg-amber-500/20 px-1.5 py-0.2 text-[11px] font-mono text-amber-600 dark:text-amber-300">
              {armedCount}
            </span>
          )}
        </button>

        <button
          type="button"
          onClick={() => setTab("deliveries")}
          className={`inline-flex flex-1 sm:flex-none items-center justify-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium transition cursor-pointer ${
            tab === "deliveries"
              ? "bg-foreground text-background shadow-xs"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          <History className="size-3.5 shrink-0" />
          <span>{t("投递日志")}</span>
        </button>
      </div>

      {/* Tab 1: Webhook 触发器列表 */}
      {tab === "triggers" && (
        <div className="space-y-3">
          {triggersQuery.isLoading ? (
            <CardSurface className="text-sm text-muted-foreground">
              {t("加载中…")}
            </CardSurface>
          ) : triggers.length === 0 ? (
            <CardSurface className="space-y-3 py-8 text-center">
              <div className="mx-auto flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
                <Webhook className="size-5" />
              </div>
              <div className="space-y-1">
                <p className="text-sm font-medium text-foreground">
                  {t("尚未配置任何 Webhook 触发器")}
                </p>
                <p className="mx-auto max-w-md text-xs text-muted-foreground">
                  {t(
                    "接入 GitHub Actions、CircleCI 或自定义 Webhook，可在外部构建完成或告警到达时自动唤醒对应卡片。",
                  )}
                </p>
              </div>
              <Button variant="outline" size="sm" onClick={openCreate}>
                <Plus className="size-3.5" />
                {t("创建第一个触发器")}
              </Button>
            </CardSurface>
          ) : (
            triggers.map((trigger) => {
              const endpointURL = buildWebhookURL(trigger.webhook_path);
              const filterBadges = summarizeFilter(trigger, t);
              const lastStatus = trigger.last_delivery_status
                ? deliveryStatusConfig[trigger.last_delivery_status]
                : undefined;

              return (
                <CardSurface key={trigger.id} className="space-y-3">
                  {/* 顶部名称 + 来源 + 开关 */}
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                    <div className="min-w-0 space-y-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="truncate text-sm font-semibold text-foreground">
                          {trigger.name}
                        </span>
                        <Badge variant="secondary" className="text-[11px]">
                          {providerLabel[trigger.provider]}
                        </Badge>
                        <StatusPill
                          tone={trigger.enabled ? "ok" : "neutral"}
                          label={trigger.enabled ? t("已启用") : t("已停用")}
                        />
                        {lastStatus && (
                          <StatusPill
                            tone={lastStatus.tone}
                            label={`${t("最近")}: ${t(lastStatus.label)}`}
                          />
                        )}
                      </div>

                      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                        <span>
                          {t("路由动作")}：{t(routingModeLabel[trigger.routing_mode])}
                        </span>
                        {trigger.routing_mode === "fixed_card" &&
                          trigger.target_card_id && (
                            <button
                              type="button"
                              onClick={() => {
                                if (trigger.target_card_project_id) {
                                  navigate(
                                    getCardRoute(
                                      trigger.target_card_project_id,
                                      trigger.target_card_id!,
                                      trigger.target_card_kind,
                                    ),
                                  );
                                }
                              }}
                              className="inline-flex items-center gap-1 font-medium text-foreground hover:underline cursor-pointer"
                            >
                              <span className="truncate max-w-[200px]">
                                {trigger.target_card_title || trigger.target_card_id}
                              </span>
                              <ExternalLink className="size-3 shrink-0" />
                            </button>
                          )}
                        {trigger.routing_mode === "create_todo" && (
                          <span>
                            {t("目标项目")}：
                            {trigger.target_project_name ||
                              trigger.target_project_id}
                          </span>
                        )}
                        <span>
                          {t("调度模式")}：
                          {trigger.dispatch_mode === "interrupt"
                            ? t("打断执行")
                            : t("排队执行")}
                        </span>
                        {trigger.cooldown_seconds > 0 && (
                          <span>
                            {t("冷却")}：{trigger.cooldown_seconds}s
                          </span>
                        )}
                      </div>
                    </div>

                    <div className="flex items-center gap-2 self-end sm:self-start shrink-0">
                      <Switch
                        checked={trigger.enabled}
                        onCheckedChange={() => toggleMutation.mutate(trigger)}
                        aria-label={t("启用触发器")}
                      />
                    </div>
                  </div>

                  {/* Webhook URL + 密钥脱敏提示 */}
                  <div className="grid min-w-0 grid-cols-1 gap-2 rounded-lg border border-line/70 bg-background/60 p-2.5 text-xs">
                    <div className="flex min-w-0 flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
                      <span className="text-[11px] font-medium text-muted-foreground shrink-0">
                        Webhook Endpoint
                      </span>
                      <div className="flex w-full min-w-0 items-center gap-1.5 sm:w-auto sm:max-w-[75%]">
                        <code className="min-w-0 flex-1 truncate font-mono text-[11px] text-foreground">
                          {endpointURL}
                        </code>
                        <button
                          type="button"
                          onClick={() =>
                            void copyText(
                              `url-${trigger.id}`,
                              endpointURL,
                              t("已复制 Webhook URL"),
                            )
                          }
                          className="inline-flex h-6 shrink-0 items-center gap-1 rounded border border-line bg-surface px-2 text-[11px] text-muted-foreground hover:text-foreground cursor-pointer"
                        >
                          {copiedKey === `url-${trigger.id}` ? (
                            <Check className="size-3 text-emerald-500" />
                          ) : (
                            <Copy className="size-3" />
                          )}
                          <span>{t("复制")}</span>
                        </button>
                      </div>
                    </div>

                    <div className="flex min-w-0 flex-wrap items-center justify-between gap-2 text-[11px] text-muted-foreground">
                      <span className="min-w-0 break-words">
                        {t("密钥指纹")}：
                        <code className="ml-1 font-mono text-foreground">
                          {trigger.secret_hint || "••••"}
                        </code>
                        <span className="ml-2 text-faint">
                          ({t("历史明文密钥不可回看，如需更换请轮换密钥")})
                        </span>
                      </span>
                      <span>
                        {t("最近触发")}：
                        {formatDateTime(
                          trigger.last_delivery_at || trigger.last_triggered_at,
                        )}
                      </span>
                    </div>
                  </div>

                  {/* 匹配条件摘要 */}
                  {filterBadges.length > 0 && (
                    <div className="flex flex-wrap gap-1.5">
                      {filterBadges.map((item) => (
                        <Badge
                          key={item}
                          variant="outline"
                          className="max-w-full truncate font-normal text-[11px]"
                        >
                          {item}
                        </Badge>
                      ))}
                    </div>
                  )}

                  {/* 底部操作栏 */}
                  <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line/60 pt-2.5">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => openEdit(trigger)}
                      >
                        <Pencil className="size-3.5" />
                        {t("编辑")}
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setRotateTarget(trigger)}
                      >
                        <KeyRound className="size-3.5" />
                        {t("轮换密钥")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => openDeliveriesFor(trigger.id)}
                      >
                        <History className="size-3.5" />
                        {t("查看日志")}
                      </Button>
                    </div>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                      onClick={() => setDeleteTarget(trigger)}
                    >
                      <Trash2 className="size-3.5" />
                      {t("删除")}
                    </Button>
                  </div>
                </CardSurface>
              );
            })
          )}
        </div>
      )}

      {/* Tab 2: 全局挂起 Watchers / 定时自唤醒总览 */}
      {tab === "watchers" && (
        <div className="space-y-3">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex flex-wrap items-center gap-2">
              <div className="w-full sm:w-48">
                <Select
                  value={watcherProjectFilter}
                  onChange={(e) => setWatcherProjectFilter(e.target.value)}
                >
                  <option value="">{t("全部项目")}</option>
                  {projects.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </Select>
              </div>
              <label className="inline-flex items-center gap-2 rounded-lg border border-line bg-surface px-3 py-1.5 text-xs text-foreground cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={watcherArmedOnly}
                  onChange={(e) => setWatcherArmedOnly(e.target.checked)}
                  className="accent-foreground"
                />
                <span>{t("仅显示等待中 (Armed)")}</span>
              </label>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void armedWatchersQuery.refetch()}
              disabled={armedWatchersQuery.isFetching}
              className="self-start sm:self-auto"
            >
              <RefreshCw
                className={`size-3.5 ${armedWatchersQuery.isFetching ? "animate-spin" : ""}`}
              />
              {t("刷新")}
            </Button>
          </div>

          {armedWatchersQuery.isLoading ? (
            <CardSurface className="text-sm text-muted-foreground">
              {t("加载中…")}
            </CardSurface>
          ) : watcherList.length === 0 ? (
            <CardSurface className="space-y-2 py-8 text-center">
              <div className="mx-auto flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
                <Radar className="size-5" />
              </div>
              <p className="text-sm font-medium text-foreground">
                {watcherArmedOnly
                  ? t("当前没有挂起中的等待或定时唤醒")
                  : t("暂无等待与唤醒记录")}
              </p>
              <p className="mx-auto max-w-md text-xs text-muted-foreground">
                {t(
                  "当 Agent 注册一次性事件等待（如等待 PR CI 完成）或设置延时自唤醒时，会实时汇总显示在这里。",
                )}
              </p>
            </CardSurface>
          ) : (
            <div className="space-y-2.5">
              {watcherList.map((watcher) => {
                const isEvent = watcher.kind === "event";
                const isArmed = watcher.status === "armed";
                const statusMeta =
                  watcherStatusConfig[watcher.status] ||
                  watcherStatusConfig.armed;
                const countdown = isArmed
                  ? formatRemainingCountdown(watcher.expires_at, nowMs, isEnglish)
                  : "";
                const cancelling =
                  cancelWatcherMutation.isPending &&
                  cancelWatcherMutation.variables?.id === watcher.id;

                return (
                  <CardSurface key={watcher.id} className="space-y-2.5">
                    <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                      <div className="min-w-0 space-y-1">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <span className="inline-flex items-center gap-1 text-xs font-medium text-foreground">
                            {isEvent ? (
                              <Radar className="size-3.5 text-amber-500 shrink-0" />
                            ) : (
                              <AlarmClock className="size-3.5 text-sky-500 shrink-0" />
                            )}
                            {isEvent ? t("事件等待") : t("定时自唤醒")}
                          </span>
                          <StatusPill
                            tone={statusMeta.tone}
                            label={t(statusMeta.label)}
                          />
                          <Badge
                            variant="outline"
                            className="text-[11px] font-normal"
                          >
                            {watcher.dispatch_mode === "interrupt"
                              ? t("打断执行")
                              : t("排队执行")}
                          </Badge>
                          {watcher.project_name && (
                            <span className="text-xs text-muted-foreground">
                              · {watcher.project_name}
                            </span>
                          )}
                        </div>

                        {watcher.project_id ? (
                          <button
                            type="button"
                            onClick={() =>
                              navigate(
                                getCardRoute(
                                  watcher.project_id!,
                                  watcher.card_id,
                                  watcher.card_kind,
                                ),
                              )
                            }
                            className="inline-flex max-w-full items-center gap-1 text-sm font-medium text-foreground hover:underline text-left cursor-pointer"
                          >
                            <span className="truncate">
                              {watcher.card_title || watcher.card_id}
                            </span>
                            <ExternalLink className="size-3.5 shrink-0 text-muted-foreground" />
                          </button>
                        ) : (
                          <p className="text-sm font-medium text-foreground truncate">
                            {watcher.card_title || watcher.card_id}
                          </p>
                        )}
                      </div>

                      {isArmed && (
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={cancelling}
                          onClick={() => cancelWatcherMutation.mutate(watcher)}
                          className="self-start shrink-0 hover:border-destructive/40 hover:bg-destructive/10 hover:text-destructive"
                        >
                          <XCircle className="size-3.5" />
                          {cancelling ? t("取消中…") : t("取消等待")}
                        </Button>
                      )}
                    </div>

                    {isEvent && watcher.match_key && (
                      <div className="flex items-center justify-between gap-2 rounded-lg border border-line/70 bg-background/60 px-2.5 py-1.5">
                        <code className="min-w-0 flex-1 break-all font-mono text-xs text-foreground">
                          {watcher.match_key}
                        </code>
                        <button
                          type="button"
                          onClick={() =>
                            void copyText(
                              `mk-${watcher.id}`,
                              watcher.match_key!,
                              t("已复制 Match Key"),
                            )
                          }
                          className="inline-flex size-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground cursor-pointer"
                          title={t("复制 Match Key")}
                        >
                          {copiedKey === `mk-${watcher.id}` ? (
                            <Check className="size-3.5 text-emerald-500" />
                          ) : (
                            <Copy className="size-3.5" />
                          )}
                        </button>
                      </div>
                    )}

                    {watcher.note && (
                      <p className="text-xs leading-relaxed text-secondary-foreground break-words">
                        {watcher.note}
                      </p>
                    )}

                    <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                      <span>
                        {isEvent ? t("超时兜底") : t("唤醒时间")}：
                        {formatDateTime(watcher.expires_at)}
                      </span>
                      {countdown ? (
                        <span className="font-medium text-amber-600 dark:text-amber-400 tabular-nums">
                          {countdown}
                        </span>
                      ) : watcher.resolved_at ? (
                        <span>
                          {t("结束于")} {formatDateTime(watcher.resolved_at)}
                        </span>
                      ) : null}
                    </div>
                  </CardSurface>
                );
              })}
            </div>
          )}
        </div>
      )}

      {/* Tab 3: Webhook 投递与匹配日志 */}
      {tab === "deliveries" && (
        <div className="space-y-3">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div className="w-full sm:w-64">
              <Select
                value={selectedTriggerFilter}
                onChange={(e) => setSelectedTriggerFilter(e.target.value)}
              >
                <option value="">{t("全部触发器")}</option>
                {triggers.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </Select>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void deliveriesQuery.refetch()}
              disabled={deliveriesQuery.isFetching}
              className="self-start sm:self-auto"
            >
              <RefreshCw
                className={`size-3.5 ${deliveriesQuery.isFetching ? "animate-spin" : ""}`}
              />
              {t("刷新")}
            </Button>
          </div>

          {deliveriesQuery.isLoading ? (
            <CardSurface className="text-sm text-muted-foreground">
              {t("加载中…")}
            </CardSurface>
          ) : deliveries.length === 0 ? (
            <CardSurface className="space-y-2 py-8 text-center">
              <div className="mx-auto flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
                <History className="size-5" />
              </div>
              <p className="text-sm font-medium text-foreground">
                {t("暂无 Webhook 投递记录")}
              </p>
              <p className="mx-auto max-w-md text-xs text-muted-foreground">
                {t(
                  "外部平台向 Webhook Endpoint 推送事件后，这里会记录验签结果、Match Key 和触发详情。",
                )}
              </p>
            </CardSurface>
          ) : (
            <div className="space-y-2.5">
              {deliveries.map((delivery) => (
                <DeliveryRow
                  key={delivery.id}
                  delivery={delivery}
                  onNavigateCard={(projectID, cardID, kind) =>
                    navigate(getCardRoute(projectID, cardID, kind))
                  }
                />
              ))}
            </div>
          )}
        </div>
      )}

      {/* 新建 / 编辑触发器弹窗 */}
      <TriggerFormDialog
        open={formOpen}
        onOpenChange={setFormOpen}
        editing={editingTrigger}
        projects={projects}
        onCreated={(res) => {
          void queryClient.invalidateQueries({ queryKey: keys.triggers });
          setFormOpen(false);
          setSecretReveal(res);
          toast.success(t("触发器已创建"));
        }}
        onUpdated={() => {
          void queryClient.invalidateQueries({ queryKey: keys.triggers });
          setFormOpen(false);
          toast.success(t("触发器已更新"));
        }}
      />

      {/* 一次性 Secret 展示弹窗（创建或轮换后仅显示一次） */}
      <Dialog
        open={Boolean(secretReveal)}
        onOpenChange={(open) => {
          if (!open) setSecretReveal(null);
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <ShieldAlert className="size-4 text-amber-500" />
              <span>{t("请立即保存 Webhook 密钥")}</span>
            </DialogTitle>
            <DialogDescription>
              {t(
                "出于安全考虑，明文密钥仅在本次创建或轮换时展示一次，关闭此窗口后将无法再次查看。",
              )}
            </DialogDescription>
          </DialogHeader>

          {secretReveal && (
            <div className="space-y-3 py-1">
              <Field label="Webhook Endpoint URL">
                <div className="flex items-center gap-2">
                  <Input
                    readOnly
                    value={buildWebhookURL(secretReveal.trigger.webhook_path)}
                    className="font-mono text-xs"
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      void copyText(
                        "reveal-url",
                        buildWebhookURL(secretReveal.trigger.webhook_path),
                        t("已复制 Webhook URL"),
                      )
                    }
                  >
                    {copiedKey === "reveal-url" ? (
                      <Check className="size-3.5 text-emerald-500" />
                    ) : (
                      <Copy className="size-3.5" />
                    )}
                    {t("复制")}
                  </Button>
                </div>
              </Field>

              <Field label={t("Webhook Secret（仅展示一次）")}>
                <div className="flex items-center gap-2">
                  <Input
                    readOnly
                    value={secretReveal.secret}
                    className="font-mono text-xs"
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      void copyText(
                        "reveal-secret",
                        secretReveal.secret,
                        t("已复制 Webhook 密钥"),
                      )
                    }
                  >
                    {copiedKey === "reveal-secret" ? (
                      <Check className="size-3.5 text-emerald-500" />
                    ) : (
                      <Copy className="size-3.5" />
                    )}
                    {t("复制")}
                  </Button>
                </div>
              </Field>

              <p className="rounded-lg border border-amber-500/30 bg-amber-500/[0.06] px-3 py-2 text-xs text-muted-foreground">
                {secretReveal.trigger.auth_mode === "bearer"
                  ? t("请在外部请求头中携带 Authorization: Bearer <Secret>。")
                  : secretReveal.trigger.provider === "github"
                    ? t("请在 GitHub Webhook 设置中将 Secret 填入对应栏位，签名头为 X-Hub-Signature-256。")
                    : secretReveal.trigger.provider === "circleci"
                      ? t("请在 CircleCI Webhook 设置中填入该 Secret，签名头为 circleci-signature。")
                      : t("请使用 HMAC-SHA256 对原始请求体签名，并通过请求头 {{header}} 发送。", {
                          header:
                            secretReveal.trigger.signature_header ||
                            DEFAULT_GENERIC_SIGNATURE_HEADER,
                        })}
              </p>
            </div>
          )}

          <DialogFooter>
            <Button onClick={() => setSecretReveal(null)}>
              {t("我已保存，关闭")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 轮换密钥确认 */}
      <AlertDialog
        open={Boolean(rotateTarget)}
        onOpenChange={(open) => {
          if (!open) setRotateTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("轮换 Webhook 密钥？")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                "轮换后旧密钥会立即失效，外部平台（GitHub / CircleCI 等）必须同步更新为新密钥才能继续通过验签。",
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("取消")}</AlertDialogCancel>
            <AlertDialogAction
              disabled={rotateMutation.isPending}
              onClick={() => {
                if (rotateTarget) rotateMutation.mutate(rotateTarget);
              }}
            >
              {rotateMutation.isPending ? t("轮换中…") : t("确认轮换")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* 删除触发器确认 */}
      <AlertDialog
        open={Boolean(deleteTarget)}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("删除此触发器？")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                "删除「{{name}}」后，该 Webhook Endpoint 将立刻停止接收事件，关联的投递历史也会一并清除。",
                { name: deleteTarget?.name || "" },
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("取消")}</AlertDialogCancel>
            <AlertDialogAction
              disabled={deleteMutation.isPending}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => {
                if (deleteTarget) deleteMutation.mutate(deleteTarget);
              }}
            >
              {deleteMutation.isPending ? t("删除中…") : t("确认删除")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function DeliveryRow({
  delivery,
  onNavigateCard,
}: {
  delivery: TriggerDelivery;
  onNavigateCard: (
    projectID: string,
    cardID: string,
    kind: Card["kind"],
  ) => void;
}) {
  const { t } = useTranslation();
  const statusMeta =
    deliveryStatusConfig[delivery.status] || deliveryStatusConfig.ignored;

  return (
    <CardSurface className="space-y-2">
      <div className="flex flex-col gap-1.5 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-wrap items-center gap-1.5 min-w-0">
          <StatusPill tone={statusMeta.tone} label={t(statusMeta.label)} />
          <Badge variant="secondary" className="text-[11px]">
            {providerLabel[delivery.provider] || delivery.provider}
          </Badge>
          {delivery.trigger_name && (
            <span className="truncate text-xs font-medium text-foreground">
              {delivery.trigger_name}
            </span>
          )}
          {delivery.event_type && (
            <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-secondary-foreground">
              {delivery.event_type}
              {delivery.event_action ? `.${delivery.event_action}` : ""}
            </code>
          )}
        </div>
        <span className="text-xs text-muted-foreground tabular-nums shrink-0">
          {formatDateTime(delivery.received_at)}
        </span>
      </div>

      {delivery.payload_summary && (
        <p className="text-xs leading-relaxed text-secondary-foreground break-words">
          {delivery.payload_summary}
        </p>
      )}

      {delivery.reason && delivery.status !== "triggered" && (
        <p className="text-xs text-muted-foreground break-words">
          {t("原因")}：<code className="font-mono">{delivery.reason}</code>
        </p>
      )}

      {delivery.match_keys.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {delivery.match_keys.map((key) => (
            <code
              key={key}
              className="max-w-full break-all rounded border border-line/70 bg-background/60 px-2 py-0.5 font-mono text-[11px] text-foreground"
            >
              {key}
            </code>
          ))}
        </div>
      )}

      {delivery.linked_cards && delivery.linked_cards.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
          <span className="text-[11px] text-muted-foreground">
            {t("关联卡片")}：
          </span>
          {delivery.linked_cards.map((card) => (
            <button
              key={card.id}
              type="button"
              onClick={() => onNavigateCard(card.project_id, card.id, card.kind)}
              className="inline-flex max-w-full items-center gap-1 rounded-md border border-line bg-surface px-2 py-0.5 text-xs font-medium text-foreground hover:bg-muted cursor-pointer"
            >
              <span className="truncate max-w-[220px]">{card.title}</span>
              <ExternalLink className="size-3 shrink-0 text-muted-foreground" />
            </button>
          ))}
        </div>
      )}
    </CardSurface>
  );
}

function TriggerFormDialog({
  open,
  onOpenChange,
  editing,
  projects,
  onCreated,
  onUpdated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editing: WorkspaceTrigger | null;
  projects: Project[];
  onCreated: (res: TriggerWithSecretResponse) => void;
  onUpdated: (trigger: WorkspaceTrigger) => void;
}) {
  const { t } = useTranslation();
  const isEdit = Boolean(editing);
  const defaultProjectID = projects[0]?.id || "";

  const form = useForm<TriggerFormValues>({
    resolver: zodResolver(triggerFormSchema),
    defaultValues: emptyTriggerFormValues(defaultProjectID),
  });

  const provider = form.watch("provider");
  const authMode = form.watch("auth_mode");
  const routingMode = form.watch("routing_mode");
  const selectedProjectID = form.watch("target_project_id");

  useEffect(() => {
    if (!open) return;
    if (editing) {
      form.reset(triggerToFormValues(editing, defaultProjectID));
    } else {
      form.reset(emptyTriggerFormValues(defaultProjectID));
    }
  }, [open, editing, defaultProjectID, form]);

  // 固定卡片模式下按所选项目加载可选卡片列表
  const cardsQuery = useQuery({
    queryKey: ["trigger-form-cards", selectedProjectID],
    queryFn: () => api.cards(selectedProjectID),
    enabled: open && routingMode === "fixed_card" && Boolean(selectedProjectID),
  });
  const projectCards = cardsQuery.data || [];

  const saveMutation = useMutation({
    mutationFn: async (values: TriggerFormValues) => {
      if (editing) {
        const updated = await api.updateTrigger(
          editing.id,
          toUpdateTriggerInput(values),
        );
        return { kind: "updated" as const, updated };
      }
      const created = await api.createTrigger(toCreateTriggerInput(values));
      return { kind: "created" as const, created };
    },
    onSuccess: (res) => {
      if (res.kind === "created") {
        onCreated(res.created);
      } else {
        onUpdated(res.updated);
      }
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("保存触发器失败"));
    },
  });

  const errors = form.formState.errors;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[88dvh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>
            {isEdit ? t("编辑 Webhook 触发器") : t("新建 Webhook 触发器")}
          </DialogTitle>
          <DialogDescription>
            {isEdit
              ? t("调整路由动作、过滤条件或启停状态。来源类型与认证模式创建后固定。")
              : t(
                  "创建后将自动生成专属 Webhook URL 与一次性密钥，可用于 GitHub、CircleCI 或任意外部系统。",
                )}
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4 py-1"
          onSubmit={form.handleSubmit((values) => saveMutation.mutate(values))}
        >
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t("触发器名称")} error={errors.name?.message}>
              <Input
                placeholder={t("例如：GitHub CI 完成唤醒")}
                {...form.register("name")}
              />
            </Field>

            <Field label={t("来源类型")}>
              <Select
                disabled={isEdit}
                value={provider}
                onChange={(e) => {
                  const next = e.target.value as TriggerProvider;
                  form.setValue("provider", next);
                  form.setValue("auth_mode", defaultAuthModeForProvider(next));
                  if (next === "github" && !form.getValues("events_text")) {
                    form.setValue("events_text", "workflow_run");
                  }
                }}
              >
                <option value="github">GitHub</option>
                <option value="circleci">CircleCI</option>
                <option value="generic">Generic Webhook</option>
              </Select>
            </Field>
          </div>

          {provider === "generic" && (
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label={t("认证方式")}>
                <Select
                  disabled={isEdit}
                  value={authMode}
                  onChange={(e) =>
                    form.setValue(
                      "auth_mode",
                      e.target.value as TriggerFormValues["auth_mode"],
                    )
                  }
                >
                  <option value="generic_hmac_sha256">HMAC-SHA256 签名</option>
                  <option value="bearer">Authorization Bearer Token</option>
                </Select>
              </Field>

              {authMode === "generic_hmac_sha256" && (
                <Field
                  label={t("签名请求头")}
                  error={errors.signature_header?.message}
                >
                  <Input
                    placeholder={DEFAULT_GENERIC_SIGNATURE_HEADER}
                    {...form.register("signature_header")}
                  />
                </Field>
              )}
            </div>
          )}

          {!isEdit && (
            <Field
              label={t("自定义密钥（可选）")}
              hint={t("留空将自动生成高强度随机密钥，仅创建时展示一次")}
              error={errors.secret?.message}
            >
              <Input
                type="password"
                autoComplete="new-password"
                placeholder={t("留空自动生成（至少 16 位）")}
                {...form.register("secret")}
              />
            </Field>
          )}

          <SectionHead title={t("路由与执行动作")} />

          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t("命中后的路由方式")}>
              <Select
                value={routingMode}
                onChange={(e) =>
                  form.setValue(
                    "routing_mode",
                    e.target.value as TriggerRoutingMode,
                  )
                }
              >
                <option value="match_watchers">
                  {t("唤醒挂起等待的卡片")}
                </option>
                <option value="fixed_card">{t("触发指定卡片")}</option>
                <option value="create_todo">{t("自动创建 Todo 卡片")}</option>
              </Select>
            </Field>

            <Field label={t("调度模式")}>
              <Select
                value={form.watch("dispatch_mode")}
                onChange={(e) =>
                  form.setValue(
                    "dispatch_mode",
                    e.target.value as TriggerFormValues["dispatch_mode"],
                  )
                }
              >
                <option value="enqueue">{t("排队执行 (enqueue)")}</option>
                <option value="interrupt">{t("打断当前执行 (interrupt)")}</option>
              </Select>
            </Field>
          </div>

          {(routingMode === "create_todo" || routingMode === "fixed_card") && (
            <div className="grid gap-3 sm:grid-cols-2">
              <Field
                label={t("所属项目")}
                error={errors.target_project_id?.message}
              >
                <Select
                  value={selectedProjectID}
                  onChange={(e) => {
                    form.setValue("target_project_id", e.target.value);
                    form.setValue("target_card_id", "");
                  }}
                >
                  <option value="">{t("请选择项目")}</option>
                  {projects.map((project) => (
                    <option key={project.id} value={project.id}>
                      {project.name}
                    </option>
                  ))}
                </Select>
              </Field>

              {routingMode === "fixed_card" && (
                <Field
                  label={t("目标卡片")}
                  error={errors.target_card_id?.message}
                >
                  <Select
                    value={form.watch("target_card_id")}
                    onChange={(e) =>
                      form.setValue("target_card_id", e.target.value)
                    }
                    disabled={!selectedProjectID || cardsQuery.isLoading}
                  >
                    <option value="">
                      {cardsQuery.isLoading ? t("加载卡片…") : t("请选择卡片")}
                    </option>
                    {projectCards.map((card) => (
                      <option key={card.id} value={card.id}>
                        [{card.kind}] {card.title}
                      </option>
                    ))}
                  </Select>
                </Field>
              )}
            </div>
          )}

          <div className="grid gap-3 sm:grid-cols-2">
            <Field
              label={t("冷却时间（秒）")}
              hint={t("0 表示不限制，防止高频抖动重复唤醒")}
              error={errors.cooldown_seconds?.message}
            >
              <Input
                type="number"
                min={0}
                max={86400}
                {...form.register("cooldown_seconds", { valueAsNumber: true })}
              />
            </Field>

            <div className="flex items-end pb-1">
              <label className="inline-flex items-center gap-2 text-xs font-medium text-foreground cursor-pointer select-none">
                <Switch
                  checked={form.watch("enabled")}
                  onCheckedChange={(checked) => form.setValue("enabled", checked)}
                />
                <span>{t("创建后立即启用")}</span>
              </label>
            </div>
          </div>

          <SectionHead
            title={t("过滤与匹配条件（可选）")}
            description={t("多个值用逗号分隔；留空表示不过滤。")}
          />

          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t("事件类型 (events)")}>
              <Input
                placeholder={
                  provider === "github"
                    ? "workflow_run, pull_request"
                    : provider === "circleci"
                      ? "workflow-completed"
                      : "deploy.finished"
                }
                {...form.register("events_text")}
              />
            </Field>

            <Field label={t("动作 (actions)")}>
              <Input
                placeholder="completed, opened"
                {...form.register("actions_text")}
              />
            </Field>

            <Field label={t("结论 / 状态 (conclusions)")}>
              <Input
                placeholder="success, failure"
                {...form.register("conclusions_text")}
              />
            </Field>

            <Field label={t("分支 (branches)")}>
              <Input
                placeholder="main, release/*"
                {...form.register("branches_text")}
              />
            </Field>

            <Field label={t("仓库 (repositories)")}>
              <Input
                placeholder="owner/repo"
                {...form.register("repositories_text")}
              />
            </Field>

            {provider === "generic" && (
              <Field
                label={t("Match Key 提取路径")}
                error={errors.match_key_path?.message}
              >
                <Input
                  placeholder="meta.match_key"
                  {...form.register("match_key_path")}
                />
              </Field>
            )}
          </div>

          {provider === "generic" && (
            <Field
              label={t("JSON 字段精确匹配")}
              hint={t("每行一条 path=value，例如 status=failed")}
              error={errors.json_matches_text?.message}
            >
              <textarea
                rows={3}
                placeholder={"event.status=failed\nenv=prod"}
                className="w-full rounded-lg border border-line bg-surface px-3 py-2 font-mono text-xs text-foreground placeholder:text-faint focus:border-accent focus:outline-none"
                {...form.register("json_matches_text")}
              />
            </Field>
          )}

          <DialogFooter className="gap-2 pt-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
            >
              {t("取消")}
            </Button>
            <Button type="submit" disabled={saveMutation.isPending}>
              {saveMutation.isPending
                ? t("保存中…")
                : isEdit
                  ? t("保存更改")
                  : t("创建触发器")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
