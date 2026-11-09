import { useEffect, useState } from "react";
import { X, Activity, Clock, Layers, AlertCircle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import type { TraceOverview, TraceEvent } from "../../shared/api/types";

export function AgentTracePanel({
  cardID,
  attemptID,
  onClose,
}: {
  cardID: string;
  attemptID: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [overview, setOverview] = useState<TraceOverview | null>(null);
  const [events, setEvents] = useState<TraceEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<"timeline" | "diagnostics">("timeline");

  useEffect(() => {
    let cancelled = false;
    async function load() {
      setLoading(true);
      setError(null);
      try {
        const [ov, ev] = await Promise.all([
          api.getAttemptTrace(cardID, attemptID),
          api.getAttemptTraceEvents(cardID, attemptID),
        ]);
        if (!cancelled) {
          setOverview(ov);
          setEvents(ev.events);
        }
      } catch (err: any) {
        if (!cancelled) {
          setError(err.message || "加载失败");
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    }
    load();
    return () => {
      cancelled = true;
    };
  }, [cardID, attemptID]);

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/20 backdrop-blur-[1px] sm:bg-transparent sm:backdrop-blur-none">
      <div className="flex h-full w-full flex-col border-l border-line bg-surface shadow-2xl sm:max-w-[640px]">
        {/* 头部 */}
        <div className="flex items-center justify-between border-b border-line px-4 py-3">
          <div className="flex items-center gap-2">
            <Activity className="size-4 text-sky-600" />
            <h2 className="text-sm font-semibold">{t("Agent Trace")}</h2>
            <span className="text-[10px] text-faint font-mono">{attemptID.slice(0, 8)}</span>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-full p-1 hover:bg-surface-hover text-faint hover:text-ink"
          >
            <X className="size-4" />
          </button>
        </div>

        {/* 概览统计 */}
        {overview && (
          <div className="grid grid-cols-3 gap-2 border-b border-line bg-surface-muted/30 px-4 py-3 text-center">
            <div>
              <div className="text-[10px] text-faint">{t("状态")}</div>
              <div className="text-xs font-medium">{overview.status}</div>
            </div>
            <div>
              <div className="text-[10px] text-faint">{t("模型请求")}</div>
              <div className="text-xs font-medium">{overview.model_call_count}</div>
            </div>
            <div>
              <div className="text-[10px] text-faint">{t("压缩次数")}</div>
              <div className="text-xs font-medium">{overview.compaction_count}</div>
            </div>
          </div>
        )}

        {/* 标签页 */}
        <div className="flex border-b border-line px-4">
          <button
            type="button"
            onClick={() => setActiveTab("timeline")}
            className={`border-b-2 px-3 py-2 text-xs font-medium ${
              activeTab === "timeline"
                ? "border-sky-600 text-sky-700"
                : "border-transparent text-faint hover:text-ink"
            }`}
          >
            {t("执行链路")}
          </button>
          <button
            type="button"
            onClick={() => setActiveTab("diagnostics")}
            className={`border-b-2 px-3 py-2 text-xs font-medium ${
              activeTab === "diagnostics"
                ? "border-sky-600 text-sky-700"
                : "border-transparent text-faint hover:text-ink"
            }`}
          >
            {t("诊断")}
          </button>
        </div>

        {/* 内容区 */}
        <div className="flex-1 overflow-y-auto p-4">
          {loading && (
            <div className="flex h-full items-center justify-center text-xs text-faint">
              {t("加载中...")}
            </div>
          )}
          {error && (
            <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 p-3 text-xs text-red-700 dark:border-red-900/50 dark:bg-red-950/20 dark:text-red-400">
              <AlertCircle className="size-4 shrink-0" />
              <span>{error}</span>
            </div>
          )}

          {!loading && !error && activeTab === "timeline" && (
            <div className="space-y-4">
              {overview?.model_calls.map((call) => (
                <div key={call.id} className="rounded-xl border border-line bg-surface p-3 text-xs">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2 font-medium">
                      <Clock className="size-3.5 text-sky-600" />
                      <span>
                        {call.purpose === "compaction" ? t("上下文压缩") : t("模型请求")} #{call.call_index}
                      </span>
                    </div>
                    <span className="text-[10px] tabular-nums text-faint">{call.duration_ms}ms</span>
                  </div>
                  <div className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 text-[11px] text-muted">
                    <div>
                      {t("模型")}: <span className="text-ink">{call.model_id}</span>
                    </div>
                    <div>
                      {t("路由")}: <span className="text-ink font-mono">{call.model_route_id?.slice(0, 8) || "-"}</span>
                    </div>
                    <div>
                      {t("输入")}: <span className="text-ink">{call.input_tokens} tokens</span>
                    </div>
                    <div>
                      {t("输出")}: <span className="text-ink">{call.output_tokens} tokens</span>
                    </div>
                    <div className="col-span-2">
                      {t("用量来源")}: <span className="text-ink">{call.usage_source}</span>
                    </div>
                  </div>
                  {call.status === "failed" && (
                    <div className="mt-2 rounded bg-red-50 p-1.5 text-[10px] text-red-700 dark:bg-red-950/30 dark:text-red-400">
                      {call.error_summary}
                    </div>
                  )}
                </div>
              ))}

              {events.length > 0 && (
                <div className="mt-6 border-t border-line pt-4">
                  <h3 className="mb-3 flex items-center gap-1.5 text-xs font-semibold">
                    <Layers className="size-3.5" />
                    {t("会话事件")}
                  </h3>
                  <div className="space-y-2">
                    {events.map((ev) => {
                      const isCompaction = ev.event?.actions?.compaction;
                      const isTool = ev.event?.content?.parts?.some((p: any) => p.functionCall || p.functionResponse);
                      if (!isCompaction && !isTool) return null;
                      return (
                        <div key={ev.id} className="rounded-lg border border-line/60 bg-surface-muted/20 p-2 text-[11px]">
                          <div className="flex justify-between text-faint">
                            <span>Seq: {ev.sequence}</span>
                            <span>{new Date(ev.occurred_at).toLocaleTimeString()}</span>
                          </div>
                          <div className="mt-1 font-medium text-ink">
                            {isCompaction ? t("上下文压缩记录") : isTool ? t("工具调用/返回") : t("事件")}
                          </div>
                          {isCompaction && (
                            <div className="mt-1 text-muted line-clamp-2">
                              {ev.event.actions.compaction.compactedContent?.parts?.[0]?.text || ""}
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                </div>
              )}
            </div>
          )}

          {!loading && !error && activeTab === "diagnostics" && overview && (
            <div className="space-y-4 text-xs">
              <div className="rounded-xl border border-line p-3">
                <h3 className="mb-2 font-semibold">{t("Token 统计")}</h3>
                <div className="space-y-1 text-muted">
                  <div className="flex justify-between">
                    <span>{t("总输入")}</span>
                    <span className="text-ink">{overview.total_input_tokens}</span>
                  </div>
                  <div className="flex justify-between">
                    <span>{t("总输出")}</span>
                    <span className="text-ink">{overview.total_output_tokens}</span>
                  </div>
                </div>
              </div>
              {overview.error_summary && (
                <div className="rounded-xl border border-red-200 bg-red-50 p-3 text-red-700 dark:border-red-900/50 dark:bg-red-950/20 dark:text-red-400">
                  <h3 className="mb-1 font-semibold">{t("执行错误")}</h3>
                  <p>{overview.error_summary}</p>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
