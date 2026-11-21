import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { useNavigate } from "react-router-dom";
import {
  AlarmClock,
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  KeyRound,
  Plus,
  Radar,
  RefreshCw,
  ShieldAlert,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import type {
  CardKind,
  CardWatcher,
  Project,
  TriggerAuthMode,
  TriggerDelivery,
  TriggerDeliveryStatus,
  TriggerProvider,
  TriggerRoutingMode,
  TriggerWithSecretResponse,
  WorkspaceTrigger,
} from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  Checkbox,
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
import { Badge } from "../../shared/ui/badge";
import {
  DEFAULT_GENERIC_SIGNATURE_HEADER,
  defaultAuthModeForProvider,
  emptyTriggerFormValues,
  formatRemainingCountdown,
  getCardRoute,
  hasAdvancedTriggerConfig,
  toCreateTriggerInput,
  toUpdateTriggerInput,
  triggerFormSchema,
  triggerToFormValues,
  type TriggerFormValues,
} from "./trigger-form";

function buildWebhookURL(webhookPath: string): string {
  if (!webhookPath) return "";
  if (/^https?:\/\//i.test(webhookPath)) return webhookPath;
  const origin = typeof window !== "undefined" ? window.location.origin : "";
  return `${origin}${webhookPath.startsWith("/") ? "" : "/"}${webhookPath}`;
}

function formatDateTime(value?: string) {
  if (!value) return "";
  return new Date(value).toLocaleString([], {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatWakeupTime(value: string, isEnglish: boolean) {
  if (!value) return "";
  const date = new Date(value);
  const now = new Date();
  const sameDay =
    date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate();
  const timeStr = date.toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
  if (sameDay) {
    return isEnglish ? `Today ${timeStr}` : `今天 ${timeStr}`;
  }
  return date.toLocaleString([], {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

const providerLabels: Record<TriggerProvider, string> = {
  github: "GitHub",
  circleci: "CircleCI",
  generic: "通用",
};

const routingSummaryLabels: Record<TriggerRoutingMode, string> = {
  match_watchers: "自动唤醒等待任务",
  fixed_card: "发送到指定任务",
  create_todo: "自动新建待办任务",
};

const deliveryStatusText: Record<
  TriggerDeliveryStatus,
  { label: string; className: string }
> = {
  triggered: {
    label: "已唤醒",
    className: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  },
  filtered: {
    label: "未满足筛选",
    className: "bg-muted text-muted-foreground",
  },
  ignored: {
    label: "已忽略",
    className: "bg-muted text-muted-foreground",
  },
  cooldown: {
    label: "冷却跳过",
    className: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  },
  rejected: {
    label: "验证失败",
    className: "bg-destructive/15 text-destructive",
  },
};

const watcherStatusText: Record<string, string> = {
  armed: "等待中",
  triggered: "已唤醒",
  expired: "已超时兜底",
  cancelled: "已取消",
};

export function TriggersSettings() {
  const { t, i18n } = useTranslation();
  const isEnglish = i18n.resolvedLanguage === "en";
  const navigate = useNavigate();

  const [showHistory, setShowHistory] = useState(false);
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [createOpen, setCreateOpen] = useState(false);
  const [selectedTriggerID, setSelectedTriggerID] = useState<string | null>(
    null,
  );
  const [secretReveal, setSecretReveal] =
    useState<TriggerWithSecretResponse | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  const projectsQuery = useQuery({
    queryKey: keys.projects,
    queryFn: () => api.projects(),
  });
  const projects = projectsQuery.data || [];

  const triggersQuery = useQuery({
    queryKey: keys.triggers,
    queryFn: () => api.triggers(),
  });
  const triggersData = triggersQuery.data;
  const triggers = useMemo(() => triggersData || [], [triggersData]);

  const selectedTrigger = useMemo(
    () => triggers.find((item) => item.id === selectedTriggerID) || null,
    [triggers, selectedTriggerID],
  );

  const watchersQuery = useQuery({
    queryKey: keys.workspaceWatchers(!showHistory, ""),
    queryFn: () =>
      api.workspaceWatchers({
        armedOnly: !showHistory,
        limit: 100,
      }),
    refetchInterval: 15_000,
  });
  const watchersData = watchersQuery.data;
  const watchers = useMemo(() => watchersData || [], [watchersData]);
  const hasArmedWatchers = useMemo(
    () => watchers.some((w) => w.status === "armed"),
    [watchers],
  );

  useEffect(() => {
    if (!hasArmedWatchers) return;
    const timer = window.setInterval(() => setNowMs(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, [hasArmedWatchers]);

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
      toast.success(updated.enabled ? t("已启用该接入") : t("已停用该接入"));
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("更新失败"));
    },
  });

  const cancelWatcherMutation = useMutation({
    mutationFn: (watcher: CardWatcher) =>
      api.cancelCardWatcher(watcher.card_id, watcher.id, "manual_cancelled"),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["workspace-watchers"] });
      toast.success(t("已取消等待"));
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("取消等待失败"));
    },
  });

  return (
    <div className="space-y-6">
      {/* 区块一：Webhook 接入 */}
      <Section
        title={t("Webhook 接入")}
        description={t(
          "接入外部事件自动唤醒任务，也可直接让 AI 自动帮你配置。",
        )}
      >
        <div className="mb-5 flex items-center justify-between gap-3">
          <p className="text-sm text-muted-foreground">{t("已配置的接入")}</p>
          <Button onClick={() => setCreateOpen(true)}>
            <Plus className="size-4" />
            {t("添加接入")}
          </Button>
        </div>

        {triggersQuery.isPending ? (
          <Spinner />
        ) : triggersQuery.error ? (
          <ErrorView error={triggersQuery.error} />
        ) : (
          <div className="space-y-2">
            {triggers.length === 0 && (
              <p className="rounded-xl border border-dashed border-line p-6 text-center text-sm text-faint">
                {t("还没有配置任何 Webhook 接入")}
              </p>
            )}

            {triggers.map((trigger) => {
              const summaryText = trigger.last_triggered_at
                ? `${t(routingSummaryLabels[trigger.routing_mode])} · ${t("最近触发于")} ${formatDateTime(trigger.last_triggered_at)}`
                : `${t(routingSummaryLabels[trigger.routing_mode])} · ${t("尚未触发")}`;

              return (
                <div
                  key={trigger.id}
                  className="flex w-full items-center justify-between gap-4 rounded-xl border border-line p-4 transition-colors hover:border-line-strong hover:bg-muted/30"
                >
                  <button
                    type="button"
                    onClick={() => setSelectedTriggerID(trigger.id)}
                    className="flex-1 min-w-0 text-left cursor-pointer outline-none"
                  >
                    <div className="flex flex-wrap items-center gap-2">
                      <p className="truncate font-semibold text-foreground">
                        {trigger.name}
                      </p>
                      <Badge variant="secondary" className="text-[11px]">
                        {t(providerLabels[trigger.provider])}
                      </Badge>
                    </div>
                    <p className="mt-1 truncate text-xs text-muted-foreground">
                      {summaryText}
                    </p>
                  </button>
                  <div className="flex items-center gap-2 shrink-0">
                    <Switch
                      checked={trigger.enabled}
                      disabled={toggleMutation.isPending}
                      onCheckedChange={() => toggleMutation.mutate(trigger)}
                      aria-label={
                        trigger.enabled ? t("停用接入") : t("启用接入")
                      }
                    />
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Section>

      {/* 区块二：等待中的任务 */}
      <Section
        title={t("等待中的任务")}
        description={t("AI 挂起等待外部结果或定时提醒的任务会显示在这里。")}
      >
        <div className="mb-5 flex items-center justify-between gap-3">
          <p className="text-sm text-muted-foreground">
            {showHistory ? t("全部等待记录") : t("当前等待中")}
          </p>
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setShowHistory((prev) => !prev)}
            >
              {showHistory ? t("只看等待中") : t("查看历史")}
            </Button>
          </div>
        </div>

        {watchersQuery.isPending ? (
          <Spinner />
        ) : watchersQuery.error ? (
          <ErrorView error={watchersQuery.error} />
        ) : (
          <div className="space-y-2">
            {watchers.length === 0 && (
              <p className="rounded-xl border border-dashed border-line p-6 text-center text-sm text-faint">
                {showHistory
                  ? t("暂无历史等待记录")
                  : t("当前没有等待中的任务")}
              </p>
            )}

            {watchers.map((watcher) => {
              const isEvent = watcher.kind === "event";
              const isArmed = watcher.status === "armed";
              const countdown = isArmed
                ? formatRemainingCountdown(watcher.expires_at, nowMs, isEnglish)
                : "";
              const cancelling =
                cancelWatcherMutation.isPending &&
                cancelWatcherMutation.variables?.id === watcher.id;

              let statusLine: string;
              if (isArmed) {
                if (isEvent) {
                  statusLine = countdown
                    ? `${t("等待外部事件")} · ${countdown}${t("超时兜底")}`
                    : `${t("等待外部事件")} · ${formatWakeupTime(watcher.expires_at, isEnglish)}`;
                } else {
                  statusLine = `${t("定时唤醒")} · ${formatWakeupTime(watcher.expires_at, isEnglish)}`;
                }
              } else {
                const stateText = t(
                  watcherStatusText[watcher.status] || watcher.status,
                );
                const when = formatDateTime(
                  watcher.resolved_at || watcher.expires_at,
                );
                statusLine = when ? `${stateText} · ${when}` : stateText;
              }

              const canOpenCard = Boolean(watcher.project_id);

              return (
                <div
                  key={watcher.id}
                  className="flex w-full items-center justify-between gap-4 rounded-xl border border-line p-4 transition-colors hover:border-line-strong hover:bg-muted/30"
                >
                  <button
                    type="button"
                    disabled={!canOpenCard}
                    onClick={() => {
                      if (!watcher.project_id) return;
                      navigate(
                        getCardRoute(
                          watcher.project_id,
                          watcher.card_id,
                          watcher.card_kind,
                        ),
                      );
                    }}
                    className={`flex-1 min-w-0 text-left outline-none ${canOpenCard ? "cursor-pointer" : "cursor-default"}`}
                    title={watcher.note || watcher.match_key || undefined}
                  >
                    <div className="flex flex-wrap items-center gap-2">
                      {isEvent ? (
                        <Radar className="size-3.5 shrink-0 text-amber-500" />
                      ) : (
                        <AlarmClock className="size-3.5 shrink-0 text-sky-500" />
                      )}
                      <p className="truncate font-semibold text-foreground">
                        {watcher.card_title || t("未命名任务")}
                      </p>
                    </div>
                    <p className="mt-1 truncate text-xs text-muted-foreground">
                      {statusLine}
                    </p>
                  </button>
                  {isArmed && (
                    <div className="shrink-0">
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={cancelling}
                        onClick={() => cancelWatcherMutation.mutate(watcher)}
                      >
                        {cancelling ? t("取消中…") : t("取消")}
                      </Button>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </Section>

      {/* 新建接入弹窗 */}
      <Modal
        open={createOpen}
        onOpenChange={setCreateOpen}
        title={t("添加 Webhook 接入")}
      >
        <TriggerFormBody
          key="new"
          editing={null}
          projects={projects}
          onCancel={() => setCreateOpen(false)}
          onCreated={(res) => {
            void queryClient.invalidateQueries({ queryKey: keys.triggers });
            setCreateOpen(false);
            setSecretReveal(res);
            toast.success(t("已添加 Webhook 接入"));
          }}
        />
      </Modal>

      {/* 详情 / 编辑 / 投递记录弹窗 */}
      <Modal
        open={Boolean(selectedTrigger)}
        onOpenChange={(open) => {
          if (!open) setSelectedTriggerID(null);
        }}
        title={selectedTrigger ? selectedTrigger.name : t("接入详情")}
      >
        {selectedTrigger && (
          <TriggerDetailModalContent
            key={selectedTrigger.id}
            trigger={selectedTrigger}
            projects={projects}
            copiedKey={copiedKey}
            onCopy={copyText}
            onUpdated={() => {
              void queryClient.invalidateQueries({ queryKey: keys.triggers });
              setSelectedTriggerID(null);
              toast.success(t("已保存修改"));
            }}
            onRotated={(res) => {
              void queryClient.invalidateQueries({ queryKey: keys.triggers });
              setSelectedTriggerID(null);
              setSecretReveal(res);
              toast.success(t("已生成新密钥"));
            }}
            onDeleted={() => {
              void queryClient.invalidateQueries({ queryKey: keys.triggers });
              void queryClient.invalidateQueries({
                queryKey: ["trigger-deliveries"],
              });
              setSelectedTriggerID(null);
              toast.success(t("已删除该接入"));
            }}
          />
        )}
      </Modal>

      {/* 一次性密钥提示弹窗 */}
      <Modal
        open={Boolean(secretReveal)}
        onOpenChange={(open) => {
          if (!open) setSecretReveal(null);
        }}
        title={t("保存回调密钥")}
      >
        {secretReveal && (
          <div className="space-y-4">
            <p className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 p-3 text-xs text-amber-800 dark:text-amber-200">
              <ShieldAlert className="mt-0.5 size-4 shrink-0" />
              <span>
                {t("密钥只显示这一次，关闭后无法找回。请立即复制并配置到外部平台。")}
              </span>
            </p>

            <Field label={t("回调地址")}>
              <div className="flex items-center gap-2">
                <TextInput
                  readOnly
                  value={buildWebhookURL(secretReveal.trigger.webhook_path)}
                  className="font-mono text-xs"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    void copyText(
                      "reveal-url",
                      buildWebhookURL(secretReveal.trigger.webhook_path),
                      t("已复制回调地址"),
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

            <Field label={t("签名密钥")}>
              <div className="flex items-center gap-2">
                <TextInput
                  readOnly
                  value={secretReveal.secret}
                  className="font-mono text-xs"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    void copyText(
                      "reveal-secret",
                      secretReveal.secret,
                      t("已复制密钥"),
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

            <div className="flex justify-end pt-2">
              <Button onClick={() => setSecretReveal(null)}>
                {t("我已保存")}
              </Button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}

function TriggerDetailModalContent({
  trigger,
  projects,
  copiedKey,
  onCopy,
  onUpdated,
  onRotated,
  onDeleted,
}: {
  trigger: WorkspaceTrigger;
  projects: Project[];
  copiedKey: string | null;
  onCopy: (key: string, text: string, label: string) => Promise<void>;
  onUpdated: () => void;
  onRotated: (res: TriggerWithSecretResponse) => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [view, setView] = useState<"edit" | "logs">("edit");
  const [confirmRotate, setConfirmRotate] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const endpointURL = buildWebhookURL(trigger.webhook_path);

  const deliveriesQuery = useQuery({
    queryKey: keys.triggerDeliveries(trigger.id),
    queryFn: () => api.triggerDeliveries(trigger.id, 30),
    enabled: view === "logs",
  });
  const deliveries = deliveriesQuery.data || [];

  const rotateMutation = useMutation({
    mutationFn: () => api.rotateTriggerSecret(trigger.id),
    onSuccess: (res) => {
      setConfirmRotate(false);
      onRotated(res);
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("轮换密钥失败"));
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => api.deleteTrigger(trigger.id),
    onSuccess: () => {
      setConfirmDelete(false);
      onDeleted();
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("删除失败"));
    },
  });

  return (
    <div className="space-y-4">
      {/* 顶部快捷信息：回调地址 + 复制 + 轮换密钥 */}
      <div className="space-y-2.5 rounded-xl border border-line bg-muted/20 p-3.5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Badge variant="secondary" className="text-[11px]">
              {t(providerLabels[trigger.provider])}
            </Badge>
            <span>
              {t("密钥尾号")} {trigger.secret_hint || "****"}
            </span>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={rotateMutation.isPending}
            onClick={() => setConfirmRotate(true)}
          >
            <KeyRound className="size-3.5" />
            {t("轮换密钥")}
          </Button>
        </div>

        <div className="flex items-center gap-2">
          <TextInput
            readOnly
            value={endpointURL}
            className="font-mono text-xs"
          />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() =>
              void onCopy(`url-${trigger.id}`, endpointURL, t("已复制回调地址"))
            }
          >
            {copiedKey === `url-${trigger.id}` ? (
              <Check className="size-3.5 text-emerald-500" />
            ) : (
              <Copy className="size-3.5" />
            )}
            {t("复制回调地址")}
          </Button>
        </div>
      </div>

      {/* 弹窗内轻量切换：编辑配置 / 最近记录 */}
      <div className="flex items-center justify-between border-b border-line pb-2">
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => setView("edit")}
            className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-colors cursor-pointer ${
              view === "edit"
                ? "bg-muted text-foreground"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t("编辑配置")}
          </button>
          <button
            type="button"
            onClick={() => setView("logs")}
            className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-colors cursor-pointer ${
              view === "logs"
                ? "bg-muted text-foreground"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t("查看最近记录")}
          </button>
        </div>
        {view === "logs" && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={deliveriesQuery.isFetching}
            onClick={() => void deliveriesQuery.refetch()}
          >
            <RefreshCw
              className={`size-3.5 ${deliveriesQuery.isFetching ? "animate-spin" : ""}`}
            />
            {t("刷新")}
          </Button>
        )}
      </div>

      {view === "edit" ? (
        <TriggerFormBody
          editing={trigger}
          projects={projects}
          onUpdated={onUpdated}
          onDelete={() => setConfirmDelete(true)}
          deleting={deleteMutation.isPending}
        />
      ) : (
        <TriggerDeliveriesList
          loading={deliveriesQuery.isPending}
          error={deliveriesQuery.error}
          deliveries={deliveries}
          onNavigateCard={(projectID, cardID, kind) =>
            navigate(getCardRoute(projectID, cardID, kind))
          }
        />
      )}

      <ConfirmDialog
        open={confirmRotate}
        onOpenChange={setConfirmRotate}
        title={t("轮换密钥")}
        description={t(
          "生成新密钥后旧密钥会立即失效，外部平台需同步更新。确认继续？",
        )}
        confirmLabel={t("确认轮换")}
        pending={rotateMutation.isPending}
        onConfirm={() => rotateMutation.mutate()}
      />

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={t("删除接入")}
        description={t("删除后此回调地址将不再接收外部事件。确认删除？")}
        confirmLabel={t("确认删除")}
        pending={deleteMutation.isPending}
        onConfirm={() => deleteMutation.mutate()}
      />
    </div>
  );
}

function TriggerDeliveriesList({
  loading,
  error,
  deliveries,
  onNavigateCard,
}: {
  loading: boolean;
  error: unknown;
  deliveries: TriggerDelivery[];
  onNavigateCard: (projectID: string, cardID: string, kind?: CardKind) => void;
}) {
  const { t } = useTranslation();

  if (loading) return <Spinner />;
  if (error) return <ErrorView error={error} />;
  if (deliveries.length === 0) {
    return (
      <p className="rounded-xl border border-dashed border-line p-6 text-center text-sm text-faint">
        {t("还没有收到过外部事件")}
      </p>
    );
  }

  return (
    <div className="space-y-2 max-h-[50dvh] overflow-y-auto pr-1">
      {deliveries.map((item) => {
        const badge = deliveryStatusText[item.status] || {
          label: item.status,
          className: "bg-muted text-muted-foreground",
        };
        return (
          <div
            key={item.id}
            className="rounded-xl border border-line p-3 space-y-1.5 text-xs"
          >
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex flex-wrap items-center gap-2 min-w-0">
                <span
                  className={`inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium ${badge.className}`}
                >
                  {t(badge.label)}
                </span>
                {item.event_type && (
                  <span className="truncate font-medium text-foreground">
                    {item.event_type}
                  </span>
                )}
              </div>
              <span className="text-muted-foreground tabular-nums">
                {formatDateTime(item.received_at)}
              </span>
            </div>
            {item.payload_summary && (
              <p className="text-muted-foreground break-words">
                {item.payload_summary}
              </p>
            )}
            {item.reason && (
              <p className="text-muted-foreground break-words">
                {item.reason}
              </p>
            )}
            {item.linked_cards && item.linked_cards.length > 0 && (
              <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
                {item.linked_cards.map((card) => (
                  <button
                    key={card.id}
                    type="button"
                    onClick={() =>
                      onNavigateCard(
                        card.project_id,
                        card.id,
                        card.kind,
                      )
                    }
                    className="inline-flex items-center rounded-md border border-line bg-surface px-2 py-0.5 text-[11px] text-foreground hover:bg-muted cursor-pointer"
                  >
                    {card.title}
                  </button>
                ))}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

function TriggerFormBody({
  editing,
  projects,
  onCancel,
  onCreated,
  onUpdated,
  onDelete,
  deleting,
}: {
  editing: WorkspaceTrigger | null;
  projects: Project[];
  onCancel?: () => void;
  onCreated?: (res: TriggerWithSecretResponse) => void;
  onUpdated?: (trigger: WorkspaceTrigger) => void;
  onDelete?: () => void;
  deleting?: boolean;
}) {
  const { t } = useTranslation();
  const isEdit = Boolean(editing);
  const defaultProjectID = projects[0]?.id || "";
  const [showAdvanced, setShowAdvanced] = useState(() =>
    editing ? hasAdvancedTriggerConfig(editing) : false,
  );

  const form = useForm<TriggerFormValues>({
    resolver: zodResolver(triggerFormSchema),
    defaultValues: editing
      ? triggerToFormValues(editing, defaultProjectID)
      : emptyTriggerFormValues(defaultProjectID),
  });

  const provider = useWatch({ control: form.control, name: "provider" });
  const authMode = useWatch({ control: form.control, name: "auth_mode" });
  const routingMode = useWatch({ control: form.control, name: "routing_mode" });
  const enabled = useWatch({ control: form.control, name: "enabled" });
  const dispatchMode = useWatch({ control: form.control, name: "dispatch_mode" });
  const targetCardID = useWatch({ control: form.control, name: "target_card_id" });
  const selectedProjectID = useWatch({
    control: form.control,
    name: "target_project_id",
  });

  const cardsQuery = useQuery({
    queryKey: ["trigger-form-cards", selectedProjectID],
    queryFn: () => api.cards(selectedProjectID),
    enabled: routingMode === "fixed_card" && Boolean(selectedProjectID),
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
        onCreated?.(res.created);
      } else {
        onUpdated?.(res.updated);
      }
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("保存失败"));
    },
  });

  const errors = form.formState.errors;

  return (
    <form
      className="space-y-4"
      onSubmit={form.handleSubmit((values) => saveMutation.mutate(values))}
    >
      {/* 默认极简区：仅名称与来源 */}
      <Field label={t("名称")} error={errors.name?.message}>
        <TextInput
          placeholder={t("例如：GitHub 构建通知")}
          {...form.register("name")}
        />
      </Field>

      <Field label={t("来源")}>
        <Select
          disabled={isEdit}
          value={provider}
          onChange={(e) => {
            const next = e.target.value as TriggerProvider;
            form.setValue("provider", next);
            form.setValue("auth_mode", defaultAuthModeForProvider(next));
            if (next === "generic" && !form.getValues("signature_header")) {
              form.setValue(
                "signature_header",
                DEFAULT_GENERIC_SIGNATURE_HEADER,
              );
            }
          }}
        >
          <option value="github">GitHub</option>
          <option value="circleci">CircleCI</option>
          <option value="generic">{t("通用")}</option>
        </Select>
      </Field>

      {isEdit && (
        <label className="inline-flex items-center gap-2 text-sm text-foreground cursor-pointer select-none">
          <Checkbox
            checked={enabled}
            onCheckedChange={(checked) =>
              form.setValue("enabled", checked === true)
            }
          />
          <span>{t("启用该接入")}</span>
        </label>
      )}

      {/* 默认折叠的高级选项区 */}
      <div className="rounded-xl border border-line">
        <button
          type="button"
          onClick={() => setShowAdvanced((prev) => !prev)}
          className="flex w-full items-center justify-between px-3.5 py-2.5 text-xs font-medium text-muted-foreground hover:text-foreground transition-colors cursor-pointer"
        >
          <span>{t("高级选项")}</span>
          {showAdvanced ? (
            <ChevronDown className="size-3.5" />
          ) : (
            <ChevronRight className="size-3.5" />
          )}
        </button>

        {showAdvanced && (
          <div className="space-y-3 border-t border-line px-3.5 py-3.5">
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label={t("收到事件后")}>
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
                    {t("自动唤醒等待中的任务")}
                  </option>
                  <option value="fixed_card">{t("发送到指定任务")}</option>
                  <option value="create_todo">{t("自动新建待办任务")}</option>
                </Select>
              </Field>

              <Field label={t("执行方式")}>
                <Select
                  value={dispatchMode}
                  onChange={(e) =>
                    form.setValue(
                      "dispatch_mode",
                      e.target.value as TriggerFormValues["dispatch_mode"],
                    )
                  }
                >
                  <option value="enqueue">{t("排队执行")}</option>
                  <option value="interrupt">{t("优先插队")}</option>
                </Select>
              </Field>
            </div>

            {routingMode !== "match_watchers" && (
              <div className="grid gap-3 sm:grid-cols-2">
                <Field
                  label={t("目标项目")}
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
                    {projects.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name}
                      </option>
                    ))}
                  </Select>
                </Field>

                {routingMode === "fixed_card" && (
                  <Field
                    label={t("目标任务")}
                    error={errors.target_card_id?.message}
                  >
                    <Select
                      value={targetCardID}
                      onChange={(e) =>
                        form.setValue("target_card_id", e.target.value)
                      }
                      disabled={!selectedProjectID || cardsQuery.isLoading}
                    >
                      <option value="">
                        {cardsQuery.isLoading ? t("加载中…") : t("请选择任务")}
                      </option>
                      {projectCards.map((c) => (
                        <option key={c.id} value={c.id}>
                          {c.title}
                        </option>
                      ))}
                    </Select>
                  </Field>
                )}
              </div>
            )}

            <div className="grid gap-3 sm:grid-cols-2">
              <Field label={t("冷却时间（秒）")} error={errors.cooldown_seconds?.message}>
                <TextInput
                  type="number"
                  min={0}
                  max={86400}
                  {...form.register("cooldown_seconds", { valueAsNumber: true })}
                />
              </Field>

              {provider === "generic" && (
                <Field label={t("验证方式")}>
                  <Select
                    disabled={isEdit}
                    value={authMode}
                    onChange={(e) =>
                      form.setValue(
                        "auth_mode",
                        e.target.value as TriggerAuthMode,
                      )
                    }
                  >
                    <option value="generic_hmac_sha256">{t("签名验证")}</option>
                    <option value="bearer">{t("令牌验证")}</option>
                  </Select>
                </Field>
              )}
            </div>

            {provider === "generic" && authMode === "generic_hmac_sha256" && (
              <Field
                label={t("签名请求头")}
                error={errors.signature_header?.message}
              >
                <TextInput
                  placeholder={DEFAULT_GENERIC_SIGNATURE_HEADER}
                  {...form.register("signature_header")}
                />
              </Field>
            )}

            <div className="grid gap-3 sm:grid-cols-2">
              <Field label={t("仅接收这些事件")}>
                <TextInput
                  placeholder={t("逗号分隔，留空表示全部")}
                  {...form.register("events_text")}
                />
              </Field>
              <Field label={t("仅接收这些动作")}>
                <TextInput
                  placeholder={t("例如：completed")}
                  {...form.register("actions_text")}
                />
              </Field>
              <Field label={t("仅接收这些结果")}>
                <TextInput
                  placeholder={t("例如：success, failure")}
                  {...form.register("conclusions_text")}
                />
              </Field>
              <Field label={t("限定分支")}>
                <TextInput
                  placeholder={t("例如：main, release/*")}
                  {...form.register("branches_text")}
                />
              </Field>
            </div>

            <Field label={t("限定仓库")}>
              <TextInput
                placeholder={t("例如：owner/repo")}
                {...form.register("repositories_text")}
              />
            </Field>

            {provider === "generic" && (
              <>
                <Field label={t("关联标识字段路径")}>
                  <TextInput
                    placeholder="data.task_key"
                    {...form.register("match_key_path")}
                  />
                </Field>
                <Field
                  label={t("额外字段筛选")}
                  error={errors.json_matches_text?.message}
                >
                  <TextArea
                    rows={3}
                    placeholder={"每行一条，格式：字段路径=期望值"}
                    {...form.register("json_matches_text")}
                  />
                </Field>
              </>
            )}
          </div>
        )}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 pt-2">
        <div>
          {onDelete && (
            <Button
              type="button"
              variant="danger"
              disabled={saveMutation.isPending || deleting}
              onClick={onDelete}
            >
              <Trash2 className="size-4" />
              {t("删除")}
            </Button>
          )}
        </div>
        <div className="flex items-center gap-2">
          {onCancel && (
            <Button
              type="button"
              variant="ghost"
              disabled={saveMutation.isPending}
              onClick={onCancel}
            >
              {t("取消")}
            </Button>
          )}
          <Button type="submit" disabled={saveMutation.isPending}>
            {saveMutation.isPending
              ? t("保存中…")
              : isEdit
                ? t("保存修改")
                : t("创建接入")}
          </Button>
        </div>
      </div>
    </form>
  );
}
