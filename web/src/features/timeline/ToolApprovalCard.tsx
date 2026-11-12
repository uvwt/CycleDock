import { useState } from "react";
import {
  ShieldAlert,
  ShieldCheck,
  ShieldX,
  Check,
  X,
  Terminal,
  RefreshCw,
  ChevronDown,
  ChevronUp,
  Loader2,
  CheckCheck,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import type { CardDetail, ToolApproval } from "../../shared/api/types";
import { api } from "../../shared/api/client";
import { queryClient, keys } from "../../app/query";

export function ToolApprovalCard({
  cardID,
  approval,
  fallbackContent,
}: {
  cardID?: string;
  approval?: ToolApproval;
  fallbackContent?: string;
}) {
  const { t } = useTranslation();
  const [rejecting, setRejecting] = useState(false);
  const [rejectComment, setRejectComment] = useState("");
  const [submitting, setSubmitting] = useState<string | null>(null);
  // 参数默认折叠，避免高危工具参数 JSON 在未点开时撑满半屏对话流
  const [expandedArgs, setExpandedArgs] = useState(false);
  // 已审批（允许/拒绝）后默认收敛为单行轻量状态条，按需点击才展开历史参数与原因
  const [expandedHistory, setExpandedHistory] = useState(false);

  if (!approval) {
    return (
      <div className="my-2 rounded-xl border border-amber-500/40 bg-amber-500/5 p-3.5 text-xs text-foreground">
        <div className="flex items-center gap-2 font-medium text-amber-600 dark:text-amber-400">
          <ShieldAlert className="size-4 shrink-0" />
          <span>{t("安全守卫拦截通知")}</span>
        </div>
        <p className="mt-1.5 text-muted-foreground">{fallbackContent}</p>
      </div>
    );
  }

  const isPending = approval.status === "pending";
  const isApproved = approval.status === "approved";

  const riskBadgeMap: Record<string, { label: string; className: string }> = {
    critical: {
      label: t("极高危 (Critical)"),
      className:
        "border-red-500/40 bg-red-500/15 text-red-700 dark:text-red-300",
    },
    high: {
      label: t("高风险 (High)"),
      className:
        "border-orange-500/40 bg-orange-500/15 text-orange-700 dark:text-orange-300",
    },
    medium: {
      label: t("中等风险 (Medium)"),
      className:
        "border-amber-500/40 bg-amber-500/15 text-amber-700 dark:text-amber-300",
    },
    low: {
      label: t("低风险 (Low)"),
      className:
        "border-sky-500/40 bg-sky-500/15 text-sky-700 dark:text-sky-300",
    },
  };

  const riskMeta = riskBadgeMap[approval.risk_level] || riskBadgeMap.high;
  const formattedArgs = JSON.stringify(approval.arguments || {}, null, 2);

  async function handleAction(
    action: "approve_once" | "approve_session" | "reject",
  ) {
    if (!cardID || submitting) return;
    setSubmitting(action);
    try {
      const resolved = await api.resolveToolApproval(
        cardID,
        approval!.id,
        action,
        action === "reject" ? rejectComment.trim() : undefined,
      );
      if (resolved?.approval) {
        queryClient.setQueryData<CardDetail>(keys.card(cardID), (old) =>
          old && old.card
            ? {
                ...old,
                timeline: old.timeline.map((ev) =>
                  ev.tool_approval?.id === approval!.id
                    ? { ...ev, tool_approval: resolved.approval }
                    : ev,
                ),
              }
            : old,
        );
      }
      void queryClient.invalidateQueries({ queryKey: keys.card(cardID) });
      if (action === "approve_once") {
        toast.success(t("已批准本次执行，正在恢复任务"));
      } else if (action === "approve_session") {
        toast.success(t("已批准并加入本卡片免审白名单"));
      } else {
        toast.info(t("已拒绝该工具调用并通知模型调整方案"));
      }
      setRejecting(false);
      setExpandedHistory(false);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      setSubmitting(null);
    }
  }

  if (!isPending && !expandedHistory) {
    return (
      <div
        data-tool-approval-card
        data-approval-status={approval.status}
        className="my-1.5"
      >
        <button
          type="button"
          onClick={() => setExpandedHistory(true)}
          className="inline-flex max-w-full items-center gap-2 rounded-xl border border-line/70 bg-muted/30 px-3 py-1.5 text-left text-xs text-muted-foreground transition-colors hover:border-line hover:bg-muted/60"
        >
          {isApproved ? (
            <ShieldCheck className="size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
          ) : (
            <ShieldX className="size-3.5 shrink-0 text-rose-600 dark:text-rose-400" />
          )}
          <span className="font-mono font-medium text-foreground">
            {approval.tool_name}
          </span>
          <span className="shrink-0">
            · {isApproved ? t("已允许执行") : t("已拒绝执行")}
          </span>
          {approval.resolution_comment && (
            <span className="truncate text-faint">
              · {approval.resolution_comment}
            </span>
          )}
          <ChevronDown className="size-3.5 shrink-0 text-faint" />
        </button>
      </div>
    );
  }

  return (
    <div
      data-tool-approval-card
      data-approval-status={approval.status}
      className={`my-2 w-full max-w-2xl overflow-hidden rounded-2xl border shadow-xs transition-colors ${
        isPending
          ? "border-amber-500/50 bg-amber-500/[0.04] dark:border-amber-500/40 dark:bg-amber-950/20"
          : isApproved
            ? "border-emerald-500/40 bg-emerald-500/[0.03] dark:border-emerald-500/30 dark:bg-emerald-950/15"
            : "border-rose-500/40 bg-rose-500/[0.03] dark:border-rose-500/30 dark:bg-rose-950/15"
      }`}
    >
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line/60 px-4 py-3">
        <div className="flex items-center gap-2">
          {isPending ? (
            <ShieldAlert className="size-4.5 shrink-0 text-amber-600 dark:text-amber-400" />
          ) : isApproved ? (
            <ShieldCheck className="size-4.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
          ) : (
            <ShieldX className="size-4.5 shrink-0 text-rose-600 dark:text-rose-400" />
          )}
          <span className="text-xs font-semibold tracking-tight text-heading">
            {isPending
              ? t("工具调用安全守卫 · 等待人工确认")
              : isApproved
                ? t("工具调用安全守卫 · 已允许执行")
                : t("工具调用安全守卫 · 已拒绝执行")}
          </span>
        </div>

        <div className="flex flex-wrap items-center gap-1.5">
          <span
            className={`inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-medium ${riskMeta.className}`}
          >
            {riskMeta.label}
          </span>
          <span
            className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium ${
              approval.reversible
                ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
                : "border-rose-500/30 bg-rose-500/10 text-rose-700 dark:text-rose-300"
            }`}
          >
            <RefreshCw className="size-2.5" />
            {approval.reversible ? t("可逆操作") : t("不可逆操作")}
          </span>
          {!isPending && (
            <button
              type="button"
              onClick={() => setExpandedHistory(false)}
              className="ml-1 inline-flex items-center gap-1 rounded-lg px-2 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              <span>{t("收起详情")}</span>
              <ChevronUp className="size-3" />
            </button>
          )}
        </div>
      </div>

      {/* Body */}
      <div className="space-y-3 p-4 text-xs">
        <div className="flex flex-col gap-1">
          <span className="text-[11px] font-medium text-faint">
            {t("拦截原因与风险说明")}
          </span>
          <p className="text-xs font-medium leading-relaxed text-foreground">
            {approval.reason ||
              t("该操作涉及潜在写入或系统变更副作用，需人工确认。")}
          </p>
        </div>

        <div className="overflow-hidden rounded-xl border border-line/80 bg-surface/90">
          <div className="flex items-center justify-between border-b border-line/60 bg-muted/40 px-3 py-2">
            <div className="flex items-center gap-1.5 font-mono text-xs font-semibold text-primary">
              <Terminal className="size-3.5 shrink-0" />
              <span>{approval.tool_name}</span>
            </div>
            <button
              type="button"
              onClick={() => setExpandedArgs((v) => !v)}
              className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
            >
              <span>{expandedArgs ? t("收起参数") : t("展开参数")}</span>
              {expandedArgs ? (
                <ChevronUp className="size-3" />
              ) : (
                <ChevronDown className="size-3" />
              )}
            </button>
          </div>
          {expandedArgs && (
            <pre className="max-h-52 overflow-auto p-3 font-mono text-[11px] leading-relaxed text-foreground/90 whitespace-pre-wrap break-all">
              {formattedArgs}
            </pre>
          )}
        </div>

        {!isPending && approval.resolution_comment && (
          <div className="rounded-lg border border-line/60 bg-muted/30 px-3 py-2 text-xs">
            <span className="font-medium text-muted-foreground">
              {t("审批备注：")}
            </span>
            <span className="text-foreground">
              {approval.resolution_comment}
            </span>
          </div>
        )}

        {isPending && cardID && (
          <div className="space-y-2.5 pt-1">
            {rejecting ? (
              <div className="space-y-2 rounded-xl border border-rose-500/40 bg-surface p-3">
                <label className="block text-[11px] font-medium text-rose-600 dark:text-rose-400">
                  {t("拒绝原因或给主模型的修改建议（可选）")}
                </label>
                <textarea
                  value={rejectComment}
                  onChange={(e) => setRejectComment(e.target.value)}
                  rows={2}
                  placeholder={t(
                    "例如：不要直接删除生产配置，请先备份或改用只读检查命令...",
                  )}
                  className="w-full resize-none rounded-lg border border-line bg-muted/20 p-2 text-xs text-foreground outline-none focus:border-primary"
                />
                <div className="flex items-center justify-end gap-2">
                  <button
                    type="button"
                    disabled={Boolean(submitting)}
                    onClick={() => setRejecting(false)}
                    className="rounded-lg px-2.5 py-1 text-xs font-medium text-muted-foreground hover:bg-muted"
                  >
                    {t("取消")}
                  </button>
                  <button
                    type="button"
                    disabled={Boolean(submitting)}
                    onClick={() => void handleAction("reject")}
                    className="inline-flex items-center gap-1.5 rounded-lg bg-rose-600 px-3 py-1.5 text-xs font-medium text-white shadow-2xs hover:bg-rose-700 disabled:opacity-50"
                  >
                    {submitting === "reject" ? (
                      <Loader2 className="size-3.5 animate-spin" />
                    ) : (
                      <X className="size-3.5" />
                    )}
                    <span>{t("确认拒绝并反馈给模型")}</span>
                  </button>
                </div>
              </div>
            ) : (
              <div className="flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  data-action="approve-once"
                  disabled={Boolean(submitting)}
                  onClick={() => void handleAction("approve_once")}
                  className="inline-flex items-center gap-1.5 rounded-xl bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground shadow-2xs transition hover:bg-primary/90 disabled:opacity-50"
                >
                  {submitting === "approve_once" ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : (
                    <Check className="size-3.5" />
                  )}
                  <span>{t("允许本次执行")}</span>
                </button>

                <button
                  type="button"
                  data-action="approve-session"
                  disabled={Boolean(submitting)}
                  onClick={() => void handleAction("approve_session")}
                  className="inline-flex items-center gap-1.5 rounded-xl border border-line bg-surface px-3 py-1.5 text-xs font-medium text-foreground shadow-2xs transition hover:bg-muted disabled:opacity-50"
                >
                  {submitting === "approve_session" ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : (
                    <CheckCheck className="size-3.5 text-emerald-600 dark:text-emerald-400" />
                  )}
                  <span>{t("本卡片内同类操作不再询问")}</span>
                </button>

                <button
                  type="button"
                  data-action="reject"
                  disabled={Boolean(submitting)}
                  onClick={() => setRejecting(true)}
                  className="inline-flex items-center gap-1.5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-3 py-1.5 text-xs font-medium text-rose-700 transition hover:bg-rose-500/20 disabled:opacity-50 dark:text-rose-300"
                >
                  <X className="size-3.5" />
                  <span>{t("拒绝并补充说明")}</span>
                </button>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
