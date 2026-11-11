import { useEffect, useState, useMemo } from "react";
import {
  X,
  Activity,
  Clock,
  AlertCircle,
  ChevronRight,
  ChevronDown,
  Wrench,
  Brain,
  FileText,
  Minimize2,
  Copy,
  Check,
} from "lucide-react";
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
  const [activeTab, setActiveTab] = useState<"timeline" | "compaction" | "diagnostics">("timeline");
  const [expandedCallIDs, setExpandedCallIDs] = useState<Set<string>>(new Set());
  const [expandedEventIDs, setExpandedEventIDs] = useState<Set<string>>(new Set());
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const [ov, ev] = await Promise.all([
          api.getAttemptTrace(cardID, attemptID),
          api.getAttemptTraceEvents(cardID, attemptID),
        ]);
        if (!cancelled) {
          setOverview(ov);
          setEvents(ev.events);
          setError(null);
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
    // 针对正在运行的 Attempt，开启 3 秒轻量轮询更新执行链路
    const interval = window.setInterval(load, 3000);
    return () => {
      cancelled = true;
      window.clearInterval(interval);
    };
  }, [cardID, attemptID]);

  const toggleCall = (id: string) => {
    setExpandedCallIDs((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleEvent = (id: string) => {
    setExpandedEventIDs((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const copyToClipboard = (text: string, key: string) => {
    navigator.clipboard?.writeText(text);
    setCopiedKey(key);
    setTimeout(() => setCopiedKey(null), 1500);
  };

  // 解析并提取事件详情
  const parsedEvents = useMemo(() => {
    return events.map((ev) => {
      const raw = ev.event || {};
      const actions = raw.actions || {};
      const compaction = actions.compaction;
      const content = raw.content || {};
      const parts: any[] = content.parts || [];

      // 提取思考（Thought）、工具调用（FunctionCall）、工具返回（FunctionResponse）、正文文本
      const thoughts: string[] = [];
      const toolCalls: { name: string; args: any; callId?: string }[] = [];
      const toolResponses: { name: string; response: any; callId?: string }[] = [];
      const textParts: string[] = [];

      for (const p of parts) {
        if (p.thought || p.thoughtSignature) {
          if (p.text) thoughts.push(p.text);
        } else if (p.functionCall) {
          toolCalls.push({
            name: p.functionCall.name,
            args: p.functionCall.args,
            callId: p.functionCall.id,
          });
        } else if (p.functionResponse) {
          toolResponses.push({
            name: p.functionResponse.name,
            response: p.functionResponse.response,
            callId: p.functionResponse.id,
          });
        } else if (p.text) {
          textParts.push(p.text);
        }
      }

      return {
        ...ev,
        author: raw.author || "system",
        compaction,
        thoughts,
        toolCalls,
        toolResponses,
        textParts,
      };
    });
  }, [events]);

  const compactionRecords = useMemo(() => {
    return parsedEvents.filter((e) => Boolean(e.compaction));
  }, [parsedEvents]);

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/40 backdrop-blur-xs sm:bg-black/20">
      <div className="flex h-full w-full flex-col border-l border-border bg-background text-foreground shadow-2xl sm:max-w-[720px] transition-all">
        {/* 头部：高对比标题与关闭按钮 */}
        <div className="flex items-center justify-between border-b border-border bg-card px-4 py-3">
          <div className="flex items-center gap-2">
            <Activity className="size-4.5 text-sky-600 dark:text-sky-400" />
            <h2 className="text-sm font-bold text-foreground tracking-tight">{t("Agent 执行链路追踪 (Trace)")}</h2>
            <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] font-mono font-medium text-foreground/80">
              {attemptID.slice(0, 8)}
            </span>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground transition-colors"
          >
            <X className="size-4.5" />
          </button>
        </div>

        {/* 概览统计指标：高清晰卡片展示 */}
        {overview && (
          <div className="grid grid-cols-4 gap-2 border-b border-border bg-muted/40 p-3 text-center">
            <div className="rounded-lg bg-card p-2 border border-border/80 shadow-2xs">
              <div className="text-[11px] font-medium text-muted-foreground">{t("状态")}</div>
              <div className="mt-0.5 text-xs font-bold text-foreground capitalize">{overview.status}</div>
            </div>
            <div className="rounded-lg bg-card p-2 border border-border/80 shadow-2xs">
              <div className="text-[11px] font-medium text-muted-foreground">{t("模型请求")}</div>
              <div className="mt-0.5 text-xs font-bold text-sky-600 dark:text-sky-400">{overview.model_call_count}</div>
            </div>
            <div className="rounded-lg bg-card p-2 border border-border/80 shadow-2xs">
              <div className="text-[11px] font-medium text-muted-foreground">{t("上下文压缩")}</div>
              <div className="mt-0.5 text-xs font-bold text-amber-600 dark:text-amber-400">{overview.compaction_count}</div>
            </div>
            <div className="rounded-lg bg-card p-2 border border-border/80 shadow-2xs">
              <div className="text-[11px] font-medium text-muted-foreground">{t("消耗 Tokens")}</div>
              <div className="mt-0.5 text-xs font-bold text-foreground">
                {(overview.total_input_tokens + overview.total_output_tokens).toLocaleString()}
              </div>
            </div>
          </div>
        )}

        {/* 标签栏导航 */}
        <div className="flex border-b border-border bg-card px-4">
          <button
            type="button"
            onClick={() => setActiveTab("timeline")}
            className={`border-b-2 px-3 py-2.5 text-xs font-semibold transition-colors ${
              activeTab === "timeline"
                ? "border-sky-600 text-sky-600 dark:border-sky-400 dark:text-sky-400"
                : "border-transparent text-muted-foreground hover:text-foreground"
            }`}
          >
            {t("完整执行链路")}
          </button>
          <button
            type="button"
            onClick={() => setActiveTab("compaction")}
            className={`flex items-center gap-1.5 border-b-2 px-3 py-2.5 text-xs font-semibold transition-colors ${
              activeTab === "compaction"
                ? "border-sky-600 text-sky-600 dark:border-sky-400 dark:text-sky-400"
                : "border-transparent text-muted-foreground hover:text-foreground"
            }`}
          >
            <span>{t("压缩详情对比")}</span>
            {compactionRecords.length > 0 && (
              <span className="rounded-full bg-amber-500/15 px-1.5 py-0.2 text-[10px] font-bold text-amber-700 dark:text-amber-300">
                {compactionRecords.length}
              </span>
            )}
          </button>
          <button
            type="button"
            onClick={() => setActiveTab("diagnostics")}
            className={`border-b-2 px-3 py-2.5 text-xs font-semibold transition-colors ${
              activeTab === "diagnostics"
                ? "border-sky-600 text-sky-600 dark:border-sky-400 dark:text-sky-400"
                : "border-transparent text-muted-foreground hover:text-foreground"
            }`}
          >
            {t("性能与诊断概览")}
          </button>
        </div>

        {/* 主内容区域 */}
        <div className="flex-1 overflow-y-auto p-4 space-y-4">
          {loading && (
            <div className="flex h-64 flex-col items-center justify-center gap-2 text-sm text-muted-foreground">
              <Clock className="size-5 animate-spin text-sky-600" />
              <span>{t("正在加载执行链路证据...")}</span>
            </div>
          )}
          {error && (
            <div className="flex items-start gap-2.5 rounded-xl border border-red-300 bg-red-50/90 p-4 text-xs font-medium text-red-800 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
              <AlertCircle className="size-4.5 shrink-0 mt-0.5" />
              <div>
                <div className="font-bold">{t("加载链路失败")}</div>
                <div className="mt-1 leading-relaxed">{error}</div>
              </div>
            </div>
          )}

          {/* TAB 1: 完整执行链路（包含模型请求、思考、工具调用与响应） */}
          {!loading && !error && activeTab === "timeline" && (
            <div className="space-y-4">
              {/* 模型调用记录列表 */}
              <div className="space-y-3">
                <div className="text-xs font-bold text-foreground/90 uppercase tracking-wider flex items-center justify-between">
                  <span>{t("模型请求与路由调用")} ({overview?.model_calls.length || 0})</span>
                  <span className="text-[11px] font-normal text-muted-foreground">{t("点击展开详细参数与 Token 指标")}</span>
                </div>

                {overview?.model_calls.map((call) => {
                  const isExpanded = expandedCallIDs.has(call.id);
                  const isCompaction = call.purpose === "compaction";

                  return (
                    <div
                      key={call.id}
                      className="rounded-xl border border-border bg-card shadow-2xs overflow-hidden transition-all"
                    >
                      <button
                        type="button"
                        onClick={() => toggleCall(call.id)}
                        className="w-full flex items-center justify-between p-3.5 text-left hover:bg-muted/50 transition-colors"
                      >
                        <div className="flex items-center gap-2.5 min-w-0">
                          {isExpanded ? (
                            <ChevronDown className="size-4 shrink-0 text-muted-foreground" />
                          ) : (
                            <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                          )}
                          <span
                            className={`inline-flex shrink-0 items-center gap-1 rounded px-2 py-0.5 text-[11px] font-semibold ${
                              isCompaction
                                ? "bg-amber-100 text-amber-800 dark:bg-amber-950/60 dark:text-amber-300 border border-amber-300/60"
                                : "bg-sky-100 text-sky-800 dark:bg-sky-950/60 dark:text-sky-300 border border-sky-300/60"
                            }`}
                          >
                            {isCompaction ? t("压缩摘要") : t("Agent 请求")} #{call.call_index}
                          </span>
                          <span className="truncate text-xs font-bold text-foreground">{call.model_id}</span>
                          <span className="text-[11px] text-muted-foreground font-mono">({call.provider})</span>
                        </div>

                        <div className="flex items-center gap-3 shrink-0 text-xs">
                          <span className="font-semibold tabular-nums text-foreground/90">{call.duration_ms} ms</span>
                          <span
                            className={`rounded px-1.5 py-0.5 text-[10px] font-bold ${
                              call.status === "success"
                                ? "bg-emerald-100 text-emerald-800 dark:bg-emerald-950/50 dark:text-emerald-300"
                                : "bg-red-100 text-red-800 dark:bg-red-950/50 dark:text-red-300"
                            }`}
                          >
                            {call.status}
                          </span>
                        </div>
                      </button>

                      {isExpanded && (
                        <div className="border-t border-border bg-muted/20 p-3.5 text-xs space-y-3">
                          <div className="grid grid-cols-2 sm:grid-cols-3 gap-2.5">
                            <div className="rounded border border-border/80 bg-card p-2">
                              <span className="text-[11px] text-muted-foreground block">{t("实际命中路由 ID")}</span>
                              <span className="font-mono text-xs font-semibold text-foreground select-all">
                                {call.model_route_id || t("未关联固定路由")}
                              </span>
                            </div>
                            <div className="rounded border border-border/80 bg-card p-2">
                              <span className="text-[11px] text-muted-foreground block">{t("Token 统计来源")}</span>
                              <span className="font-semibold text-foreground capitalize">{call.usage_source}</span>
                            </div>
                            <div className="rounded border border-border/80 bg-card p-2">
                              <span className="text-[11px] text-muted-foreground block">{t("输入 / 输出 Tokens")}</span>
                              <span className="font-semibold text-foreground">
                                {call.input_tokens.toLocaleString()} / {call.output_tokens.toLocaleString()}
                              </span>
                            </div>
                            {call.cached_input_tokens > 0 && (
                              <div className="rounded border border-border/80 bg-card p-2">
                                <span className="text-[11px] text-muted-foreground block">{t("缓存命中的输入")}</span>
                                <span className="font-semibold text-foreground">{call.cached_input_tokens.toLocaleString()}</span>
                              </div>
                            )}
                            {call.reasoning_output_tokens > 0 && (
                              <div className="rounded border border-border/80 bg-card p-2">
                                <span className="text-[11px] text-muted-foreground block">{t("推理思维输出 Tokens")}</span>
                                <span className="font-semibold text-foreground">{call.reasoning_output_tokens.toLocaleString()}</span>
                              </div>
                            )}
                            <div className="rounded border border-border/80 bg-card p-2">
                              <span className="text-[11px] text-muted-foreground block">{t("调用起始时间")}</span>
                              <span className="text-[11px] text-foreground font-mono">
                                {call.started_at ? new Date(call.started_at).toLocaleTimeString() : "-"}
                              </span>
                            </div>
                          </div>

                          {call.error_summary && (
                            <div className="rounded-lg border border-red-300 bg-red-50 p-2.5 text-xs text-red-800 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
                              <span className="font-bold block mb-1">{t("调用异常报错")}:</span>
                              <pre className="font-mono whitespace-pre-wrap break-all text-[11px]">{call.error_summary}</pre>
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>

              {/* 会话事件流（工具调用、执行返回与思考过程详情） */}
              <div className="mt-6 space-y-3 pt-4 border-t border-border">
                <div className="text-xs font-bold text-foreground/90 uppercase tracking-wider flex items-center justify-between">
                  <span>{t("会话执行事件流（工具调用 / 思考 / 压缩）")}</span>
                  <span className="text-[11px] font-normal text-muted-foreground">{t("按时序排查真实调用结果")}</span>
                </div>

                <div className="space-y-2.5">
                  {parsedEvents.map((ev) => {
                    const isExpanded = expandedEventIDs.has(ev.id);
                    const hasTools = ev.toolCalls.length > 0 || ev.toolResponses.length > 0;
                    const hasThoughts = ev.thoughts.length > 0;
                    const hasCompaction = Boolean(ev.compaction);

                    if (!hasTools && !hasThoughts && !hasCompaction) {
                      return null;
                    }

                    return (
                      <div
                        key={ev.id}
                        className="rounded-xl border border-border bg-card shadow-2xs overflow-hidden transition-all"
                      >
                        <button
                          type="button"
                          onClick={() => toggleEvent(ev.id)}
                          className="w-full flex items-center justify-between p-3 text-left hover:bg-muted/40 transition-colors"
                        >
                          <div className="flex items-center gap-2 min-w-0">
                            {isExpanded ? (
                              <ChevronDown className="size-4 shrink-0 text-muted-foreground" />
                            ) : (
                              <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                            )}
                            <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-mono font-bold text-foreground">
                              #{ev.sequence}
                            </span>

                            {hasCompaction && (
                              <span className="inline-flex items-center gap-1 rounded bg-amber-100 dark:bg-amber-950/60 px-2 py-0.5 text-[11px] font-bold text-amber-800 dark:text-amber-300">
                                <Minimize2 className="size-3" />
                                {t("上下文压缩")}
                              </span>
                            )}

                            {ev.toolCalls.length > 0 && (
                              <span className="inline-flex items-center gap-1 rounded bg-indigo-100 dark:bg-indigo-950/60 px-2 py-0.5 text-[11px] font-bold text-indigo-800 dark:text-indigo-300">
                                <Wrench className="size-3" />
                                {t("工具调用")}: {ev.toolCalls.map((tc) => tc.name).join(", ")}
                              </span>
                            )}

                            {ev.toolResponses.length > 0 && (
                              <span className="inline-flex items-center gap-1 rounded bg-emerald-100 dark:bg-emerald-950/60 px-2 py-0.5 text-[11px] font-bold text-emerald-800 dark:text-emerald-300">
                                <Check className="size-3" />
                                {t("工具返回")}: {ev.toolResponses.map((tr) => tr.name).join(", ")}
                              </span>
                            )}

                            {hasThoughts && (
                              <span className="inline-flex items-center gap-1 rounded bg-purple-100 dark:bg-purple-950/60 px-2 py-0.5 text-[11px] font-bold text-purple-800 dark:text-purple-300">
                                <Brain className="size-3" />
                                {t("模型思考过程")}
                              </span>
                            )}
                          </div>

                          <span className="text-[11px] font-mono text-muted-foreground shrink-0">
                            {new Date(ev.occurred_at).toLocaleTimeString()}
                          </span>
                        </button>

                        {isExpanded && (
                          <div className="border-t border-border bg-muted/15 p-3.5 space-y-3.5 text-xs">
                            {/* 1. 工具调用参数详情 */}
                            {ev.toolCalls.map((tc, idx) => (
                              <div key={idx} className="rounded-lg border border-indigo-200 dark:border-indigo-900/60 bg-card p-3">
                                <div className="flex items-center justify-between mb-1.5">
                                  <div className="flex items-center gap-1.5 font-bold text-indigo-700 dark:text-indigo-400">
                                    <Wrench className="size-3.5" />
                                    <span>{t("工具参数")}：{tc.name}</span>
                                  </div>
                                  <button
                                    type="button"
                                    onClick={() => copyToClipboard(JSON.stringify(tc.args, null, 2), `tc-${ev.id}-${idx}`)}
                                    className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
                                  >
                                    {copiedKey === `tc-${ev.id}-${idx}` ? (
                                      <Check className="size-3 text-emerald-600" />
                                    ) : (
                                      <Copy className="size-3" />
                                    )}
                                    <span>{copiedKey === `tc-${ev.id}-${idx}` ? t("已复制") : t("复制 JSON")}</span>
                                  </button>
                                </div>
                                <pre className="rounded bg-muted/60 p-2.5 font-mono text-[11px] leading-relaxed overflow-x-auto text-foreground">
                                  {JSON.stringify(tc.args, null, 2)}
                                </pre>
                              </div>
                            ))}

                            {/* 2. 工具执行结果返回详情 */}
                            {ev.toolResponses.map((tr, idx) => (
                              <div key={idx} className="rounded-lg border border-emerald-200 dark:border-emerald-900/60 bg-card p-3">
                                <div className="flex items-center justify-between mb-1.5">
                                  <div className="flex items-center gap-1.5 font-bold text-emerald-700 dark:text-emerald-400">
                                    <Check className="size-3.5" />
                                    <span>{t("工具返回结果")}：{tr.name}</span>
                                  </div>
                                  <button
                                    type="button"
                                    onClick={() => copyToClipboard(JSON.stringify(tr.response, null, 2), `tr-${ev.id}-${idx}`)}
                                    className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
                                  >
                                    {copiedKey === `tr-${ev.id}-${idx}` ? (
                                      <Check className="size-3 text-emerald-600" />
                                    ) : (
                                      <Copy className="size-3" />
                                    )}
                                    <span>{copiedKey === `tr-${ev.id}-${idx}` ? t("已复制") : t("复制 JSON")}</span>
                                  </button>
                                </div>
                                <pre className="rounded bg-muted/60 p-2.5 font-mono text-[11px] leading-relaxed max-h-72 overflow-y-auto text-foreground">
                                  {typeof tr.response === "string" ? tr.response : JSON.stringify(tr.response, null, 2)}
                                </pre>
                              </div>
                            ))}

                            {/* 3. 模型思考详情 */}
                            {ev.thoughts.map((th, idx) => (
                              <div key={idx} className="rounded-lg border border-purple-200 dark:border-purple-900/60 bg-purple-50/50 dark:bg-purple-950/20 p-3">
                                <div className="font-bold text-purple-800 dark:text-purple-300 flex items-center gap-1.5 mb-1.5">
                                  <Brain className="size-3.5" />
                                  <span>{t("模型思考推导（Chain of Thought）")}</span>
                                </div>
                                <div className="text-foreground text-[12px] leading-relaxed whitespace-pre-wrap">
                                  {th}
                                </div>
                              </div>
                            ))}

                            {/* 4. 上下文压缩摘要 */}
                            {ev.compaction && (
                              <div className="rounded-lg border border-amber-300 dark:border-amber-900/60 bg-amber-50/50 dark:bg-amber-950/20 p-3">
                                <div className="font-bold text-amber-800 dark:text-amber-300 flex items-center justify-between mb-1.5">
                                  <div className="flex items-center gap-1.5">
                                    <Minimize2 className="size-3.5" />
                                    <span>{t("上下文压缩摘要事实")}</span>
                                  </div>
                                  <span className="text-[11px] text-muted-foreground font-mono">
                                    {ev.compaction.startTimestamp ? new Date(ev.compaction.startTimestamp).toLocaleTimeString() : ""} ~{" "}
                                    {ev.compaction.endTimestamp ? new Date(ev.compaction.endTimestamp).toLocaleTimeString() : ""}
                                  </span>
                                </div>
                                <div className="text-foreground text-[12px] leading-relaxed whitespace-pre-wrap bg-card p-2.5 rounded border border-border/80">
                                  {ev.compaction.compactedContent?.parts?.[0]?.text || t("无文字摘要")}
                                </div>
                              </div>
                            )}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>
          )}

          {/* TAB 2: 压缩详情对比专区 */}
          {!loading && !error && activeTab === "compaction" && (
            <div className="space-y-4">
              {compactionRecords.length === 0 ? (
                <div className="flex h-48 flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border p-6 text-center text-muted-foreground">
                  <FileText className="size-6 text-muted-foreground/60" />
                  <p className="text-xs">{t("本次 Attempt 未发生上下文压缩（未触发 Token 预算阈值）")}</p>
                </div>
              ) : (
                <div className="space-y-4">
                  {compactionRecords.map((rec, index) => (
                    <div key={rec.id} className="rounded-xl border border-border bg-card p-4 space-y-3 shadow-2xs">
                      <div className="flex items-center justify-between border-b border-border pb-2.5">
                        <div className="flex items-center gap-2">
                          <span className="rounded bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300 font-bold px-2 py-0.5 text-xs">
                            {t("第 {{index}} 次压缩", { index: index + 1 })}
                          </span>
                          <span className="text-xs text-muted-foreground font-mono">Seq: {rec.sequence}</span>
                        </div>
                        <span className="text-xs font-mono text-muted-foreground">
                          {new Date(rec.occurred_at).toLocaleString()}
                        </span>
                      </div>

                      <div className="grid grid-cols-2 gap-3 text-xs">
                        <div className="rounded-lg border border-border p-2.5 bg-muted/30">
                          <span className="text-muted-foreground text-[11px] block">{t("压缩覆盖时段起点")}</span>
                          <span className="font-mono font-medium text-foreground">
                            {rec.compaction?.startTimestamp ? new Date(rec.compaction.startTimestamp).toLocaleString() : "-"}
                          </span>
                        </div>
                        <div className="rounded-lg border border-border p-2.5 bg-muted/30">
                          <span className="text-muted-foreground text-[11px] block">{t("压缩覆盖时段终点")}</span>
                          <span className="font-mono font-medium text-foreground">
                            {rec.compaction?.endTimestamp ? new Date(rec.compaction.endTimestamp).toLocaleString() : "-"}
                          </span>
                        </div>
                      </div>

                      <div>
                        <div className="flex items-center justify-between mb-1.5">
                          <span className="text-xs font-bold text-foreground">{t("压缩生成的持久化总结摘要")}</span>
                          <button
                            type="button"
                            onClick={() => copyToClipboard(rec.compaction?.compactedContent?.parts?.[0]?.text || "", `comp-${rec.id}`)}
                            className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
                          >
                            {copiedKey === `comp-${rec.id}` ? <Check className="size-3 text-emerald-600" /> : <Copy className="size-3" />}
                            <span>{copiedKey === `comp-${rec.id}` ? t("已复制") : t("复制摘要")}</span>
                          </button>
                        </div>
                        <div className="rounded-lg border border-border bg-muted/20 p-3 text-xs leading-relaxed text-foreground font-sans whitespace-pre-wrap">
                          {rec.compaction?.compactedContent?.parts?.[0]?.text || t("（无摘要文本）")}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {/* TAB 3: 诊断概览 */}
          {!loading && !error && activeTab === "diagnostics" && overview && (
            <div className="space-y-4 text-xs">
              <div className="rounded-xl border border-border bg-card p-4 shadow-2xs space-y-3">
                <h3 className="font-bold text-sm text-foreground">{t("Token 消耗与容量指标")}</h3>
                <div className="space-y-2">
                  <div className="flex justify-between items-center py-1.5 border-b border-border/60">
                    <span className="text-muted-foreground">{t("累计输入 Tokens")}</span>
                    <span className="font-bold text-foreground font-mono">{overview.total_input_tokens.toLocaleString()}</span>
                  </div>
                  <div className="flex justify-between items-center py-1.5 border-b border-border/60">
                    <span className="text-muted-foreground">{t("累计输出 Tokens")}</span>
                    <span className="font-bold text-foreground font-mono">{overview.total_output_tokens.toLocaleString()}</span>
                  </div>
                  <div className="flex justify-between items-center py-1.5">
                    <span className="text-muted-foreground font-semibold">{t("总计 Tokens")}</span>
                    <span className="font-bold text-sky-600 dark:text-sky-400 font-mono text-sm">
                      {(overview.total_input_tokens + overview.total_output_tokens).toLocaleString()}
                    </span>
                  </div>
                </div>
              </div>

              {overview.error_summary && (
                <div className="rounded-xl border border-red-300 bg-red-50 p-4 text-red-900 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-200">
                  <h3 className="font-bold text-sm mb-1.5 flex items-center gap-1.5">
                    <AlertCircle className="size-4" />
                    <span>{t("执行中断/失败原因")}</span>
                  </h3>
                  <pre className="font-mono text-xs whitespace-pre-wrap break-all leading-relaxed">
                    {overview.error_summary}
                  </pre>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

