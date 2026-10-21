import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import {
  ArrowLeft,
  ArrowUp,
  PanelRight,
  Zap,
  ListPlus,
  Sparkles,
  Square,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { useCardStream } from "../../shared/realtime/useCardStream";
import {
  Button,
  ErrorView,
  Sheet,
  Spinner,
  TextArea,
} from "../../shared/ui/components";
import { ConversationView } from "../timeline/ConversationView";
import { CardDetails } from "./CardDetails";
import type { CardDetail } from "../../shared/api/types";

function useElapsed(startedAt?: string, active = false) {
  const [now, setNow] = useState(0);
  useEffect(() => {
    if (!active) return;
    const handle = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(handle);
  }, [active]);
  if (!startedAt) return null;
  return Math.max(0, Math.floor((now - new Date(startedAt).getTime()) / 1000));
}
function prettyTime(seconds: number | null) {
  return seconds === null
    ? "—"
    : `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}
export default function CardPage() {
  const { projectID = "", cardID = "" } = useParams();
  const navigate = useNavigate();
  const detail = useQuery({
    queryKey: keys.card(cardID),
    queryFn: () => api.card(cardID),
    enabled: !!cardID,
  });
  const stream = useCardStream(cardID, projectID);
  const card = detail.data?.card;
  const [drawer, setDrawer] = useState(false),
    [comment, setComment] = useState("");
  const [submitMode, setSubmitMode] = useState<"enqueue" | "interrupt">(
    "enqueue",
  );
  const isDiscussion = card?.kind === "discussion";
  const waiting = detail.data?.current_run?.status === "waiting_human";
  const current = detail.data?.current_run;
  const hasCurrent =
    !!current &&
    ["queued", "running", "waiting_human"].includes(current.status);
  const elapsed = useElapsed(
    current?.started_at,
    current?.status === "running",
  );
  const blocked = isDiscussion && card?.discussion_state === "archived";
  const canGenerate = isDiscussion && !blocked;
  const sync = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.card(cardID) }),
      queryClient.invalidateQueries({ queryKey: keys.cards(projectID) }),
    ]);
  };
  const send = useMutation({
    mutationFn: (mode: "enqueue" | "interrupt" | "reply") =>
      mode === "reply"
        ? api.reply(cardID, comment.trim())
        : api.send(cardID, comment.trim(), mode),
    onSuccess: async () => {
      setComment("");
      setSubmitMode("enqueue");
      await sync();
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "发送失败"),
  });
  const generate = useMutation({
    mutationFn: () =>
      api.send(
        cardID,
        "请根据当前 Discussion 的上下文生成具体工作。明确结束条件的创建 Todo；长期维护或周期执行的创建 Ongoing。使用 CycleDock 工作创建工具，记录来源 Discussion；不要转换当前 Discussion。",
        "enqueue",
      ),
    onSuccess: () => {
      toast.success("已提交生成任务指令");
      void sync();
    },
    onError: (e) => toast.error(String(e)),
  });
  const cancel = useMutation({
    mutationFn: () => api.cancelRun(cardID, current!.id),
    onSuccess: sync,
    onError: (e) => toast.error(String(e)),
  });
  function submit() {
    if (!comment.trim() || blocked || send.isPending) return;
    send.mutate(waiting ? "reply" : submitMode);
  }
  function back() {
    navigate(isDiscussion ? "/chat" : "/board/" + projectID);
  }
  return (
    <main className="flex h-[calc(100dvh-4rem)] flex-col bg-white">
      <div className="flex shrink-0 items-center justify-between gap-3 border-b border-slate-100 px-4 py-3 sm:px-6">
        <div className="flex min-w-0 items-center gap-3">
          <button
            aria-label="返回"
            onClick={back}
            className="rounded-lg p-1.5 text-slate-500 hover:bg-slate-100"
          >
            <ArrowLeft className="size-5" />
          </button>
          <div className="min-w-0">
            <h1 className="max-w-[55vw] truncate text-sm font-semibold text-slate-900 md:text-base">
              {card?.title || "新讨论"}
            </h1>
            <p className="mt-0.5 text-xs text-slate-400">
              {isDiscussion
                ? "Discussion"
                : card?.kind === "todo"
                  ? "Todo"
                  : "Ongoing"}{" "}
              ·{" "}
              {isDiscussion
                ? card?.discussion_state
                : card?.todo_state || card?.ongoing_state}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {current && (
            <span
              role="status"
              className="inline-flex max-w-[36vw] shrink-0 items-center gap-1.5 truncate rounded-full bg-slate-50 px-2 py-1.5 text-[11px] text-slate-500 sm:max-w-none sm:px-3 sm:text-xs"
            >
              {current.status === "running" && (
                <span className="live-dot size-1.5 rounded-full bg-sky-500" />
              )}
              {current.status === "queued"
                ? "排队中"
                : current.status === "waiting_human"
                  ? "等待回复"
                  : current.status === "running"
                    ? "运行中"
                    : current.status}
              {current.status === "running" && (
                <span> · {prettyTime(elapsed)}</span>
              )}
            </span>
          )}
          <button
            className="rounded-xl border border-slate-200 p-2 text-slate-600 hover:bg-slate-50"
            title="任务详情"
            aria-label="任务详情"
            onClick={() => setDrawer(true)}
          >
            <PanelRight className="size-5" />
          </button>
        </div>
      </div>
      {detail.isPending ? (
        <div className="grid flex-1 place-items-center">
          <Spinner text="正在加载会话…" />
        </div>
      ) : detail.error || !detail.data ? (
        <div className="mx-auto mt-12 w-full max-w-xl p-5">
          <ErrorView
            error={detail.error}
            onRetry={() => void detail.refetch()}
          />
        </div>
      ) : (
        <>
          <ConversationView
            events={detail.data.timeline}
            live={stream.live}
            cardKind={detail.data.card.kind}
          />
          <div className="shrink-0 border-t border-slate-100 bg-white px-4 pt-3 pb-4 sm:px-6">
            <div className="mx-auto max-w-3xl space-y-2">
              {canGenerate && (
                <div className="flex justify-end">
                  <Button
                    variant="ghost"
                    disabled={generate.isPending}
                    onClick={() => generate.mutate()}
                  >
                    <Sparkles className="size-4 text-sky-700" />
                    生成任务
                  </Button>
                </div>
              )}
              {blocked && (
                <p className="text-center text-xs text-slate-400">
                  此讨论已归档，不能继续发送消息
                </p>
              )}
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  submit();
                }}
                className="rounded-2xl border border-slate-200 bg-white p-2 shadow-sm focus-within:border-sky-300"
              >
                <TextArea
                  value={comment}
                  onChange={(e) => setComment(e.target.value)}
                  readOnly={!!blocked}
                  placeholder={
                    blocked
                      ? "讨论已归档"
                      : waiting
                        ? "回复 Agent，继续执行…"
                        : "发送消息，继续推进任务…"
                  }
                  rows={2}
                  className="max-h-40 min-h-16 resize-none border-0 bg-transparent shadow-none focus:border-0"
                  onKeyDown={(e) => {
                    if (
                      e.key === "Enter" &&
                      !e.shiftKey &&
                      !e.nativeEvent.isComposing
                    ) {
                      e.preventDefault();
                      submit();
                    }
                  }}
                />
                <div className="flex flex-wrap items-center justify-between gap-2 px-1 pb-1">
                  <div className="flex items-center gap-2">
                    {hasCurrent && !waiting && (
                      <select
                        aria-label="任务发送方式"
                        value={submitMode}
                        onChange={(e) =>
                          setSubmitMode(
                            e.target.value as "enqueue" | "interrupt",
                          )
                        }
                        className="rounded-lg border border-slate-200 px-2 py-1.5 text-xs text-slate-600"
                      >
                        <option value="enqueue">加入队列</option>
                        <option value="interrupt">立即转向</option>
                      </select>
                    )}
                    {waiting && (
                      <span className="text-xs text-amber-600">
                        等待人工回复
                      </span>
                    )}
                    {!stream.connected && (
                      <span
                        title={stream.lastError}
                        className="text-[11px] text-slate-400"
                      >
                        正在连接实时通道…
                      </span>
                    )}
                  </div>
                  <div className="flex items-center gap-2">
                    {hasCurrent && !waiting && (
                      <Button
                        type="button"
                        variant="ghost"
                        title="取消执行"
                        onClick={() => cancel.mutate()}
                        disabled={cancel.isPending}
                      >
                        <Square className="size-3" />
                      </Button>
                    )}
                    <Button
                      variant={
                        submitMode === "interrupt" ? "danger" : "primary"
                      }
                      type="submit"
                      disabled={send.isPending || !comment.trim() || !!blocked}
                    >
                      {submitMode === "interrupt" && !waiting ? (
                        <Zap className="size-4" />
                      ) : waiting ? (
                        <ListPlus className="size-4" />
                      ) : (
                        <ArrowUp className="size-4" />
                      )}
                      {waiting
                        ? "回复并继续"
                        : submitMode === "interrupt"
                          ? "立即转向"
                          : "发送"}
                    </Button>
                  </div>
                </div>
              </form>
              <p className="text-center text-[11px] text-slate-400">
                CycleDock
                保存重要事件与结果，不保存模型内部思维链或原始工具记录。
              </p>
            </div>
          </div>
        </>
      )}
      <Sheet open={drawer} onOpenChange={setDrawer} title="任务详情">
        {detail.data ? (
          <CardDetails
            key={detail.data.card.id}
            detail={detail.data as CardDetail}
            onDeleted={back}
          />
        ) : (
          <Spinner />
        )}
      </Sheet>
    </main>
  );
}
