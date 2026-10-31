import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useLocation, useNavigate } from "react-router-dom";
import { Expand, X } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { useProject } from "../../app/project-context";
import { useDeviceSelection } from "../devices/device-selection";
import { ConversationComposer } from "../cards/ConversationComposer";
import { PendingTurnQueue } from "../cards/PendingTurnQueue";
import {
  ConversationView,
  type QuotedMessage,
} from "../timeline/ConversationView";
import { useCardStream } from "../../shared/realtime/useCardStream";
import {
  cardMessageEvent,
  type CardMessageDetail,
} from "../../shared/realtime/cardUpdates";
import { Spinner } from "../../shared/ui/components";

function unreadKey(projectID: string) {
  return `cycledock.main-chat.unread.${projectID}`;
}

function readUnread(projectID: string) {
  const value = Number(localStorage.getItem(unreadKey(projectID)));
  return Number.isFinite(value) && value > 0 ? value : 0;
}

export function MainChatDock() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const { projectID } = useProject();
  const { target } = useDeviceSelection();
  const focused = /^\/(?:chat|cards|plans)\/[^/]+\/[^/]+\/?$/.test(
    location.pathname,
  );
  const hidden = focused || location.pathname.startsWith("/chat");
  const [open, setOpen] = useState(false);
  const [peek, setPeek] = useState("");
  const [unread, setUnread] = useState(0);
  const [comment, setComment] = useState("");
  const [replyTo, setReplyTo] = useState<QuotedMessage | null>(null);
  const [cancelingTurnID, setCancelingTurnID] = useState("");
  const [steeringTurnID, setSteeringTurnID] = useState("");
  const [retryingAttemptID, setRetryingAttemptID] = useState("");

  const main = useQuery({
    queryKey: keys.mainDiscussion(projectID),
    queryFn: () => api.mainDiscussion(projectID),
    enabled: Boolean(projectID),
  });
  const cardID = main.data?.id || "";
  const detail = useQuery({
    queryKey: keys.card(cardID),
    queryFn: () => api.card(cardID),
    enabled: open && Boolean(cardID),
  });
  const stream = useCardStream(open ? cardID : "", projectID);

  useEffect(() => {
    setOpen(false);
    setPeek("");
    setUnread(projectID ? readUnread(projectID) : 0);
  }, [projectID]);

  useEffect(() => {
    if (hidden) setOpen(false);
  }, [hidden]);

  useEffect(() => {
    if (!peek) return;
    const timer = window.setTimeout(() => setPeek(""), 5000);
    return () => window.clearTimeout(timer);
  }, [peek]);

  const openChat = useMutation({
    mutationFn: () => api.openMainDiscussion(projectID),
    onSuccess: async (card) => {
      queryClient.setQueryData(keys.mainDiscussion(projectID), card);
      await queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("发送失败")),
  });

  const card = detail.data?.card;
  const current = detail.data?.current_attempt;
  const waiting = current?.status === "waiting_human";
  const hasCurrent =
    Boolean(current) &&
    ["queued", "running", "waiting_human"].includes(current?.status || "");
  const blocked = card?.discussion_state === "archived";

  async function sync() {
    if (!cardID) return;
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.card(cardID) }),
      queryClient.invalidateQueries({ queryKey: keys.cards(projectID) }),
    ]);
  }

  const send = useMutation({
    mutationFn: (mode: "enqueue" | "reply") => {
      const text = comment.trim();
      const content = replyTo
        ? `> ${replyTo.sender}：\n> ${replyTo.text.trim().split("\n").join("\n> ")}\n\n${text}`
        : text;
      if (mode === "reply") {
        if (!current || current.status !== "waiting_human") {
          return Promise.reject(new Error(t("当前回复已失效，请刷新后重试")));
        }
        return api.reply(cardID, content, current.id, undefined, target);
      }
      return api.send(cardID, content, "enqueue", undefined, target);
    },
    onSuccess: async () => {
      setComment("");
      setReplyTo(null);
      await sync();
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("发送失败")),
  });

  const choose = useMutation({
    mutationFn: (choice: {
      value: string;
      attemptID: string;
      messageID: string;
    }) =>
      api.reply(
        cardID,
        choice.value,
        choice.attemptID,
        choice.messageID,
        target,
      ),
    onSuccess: sync,
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("发送失败")),
  });

  const cancelAttempt = useMutation({
    mutationFn: () => api.cancelAttempt(cardID, current!.id),
    onSuccess: sync,
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("取消失败")),
  });

  const cancelTurn = useMutation({
    mutationFn: (turnID: string) => api.cancelTurn(cardID, turnID),
    onMutate: (turnID) => setCancelingTurnID(turnID),
    onSuccess: sync,
    onSettled: () => setCancelingTurnID(""),
  });

  const steerTurn = useMutation({
    mutationFn: (turnID: string) => api.steerTurn(cardID, turnID),
    onMutate: (turnID) => setSteeringTurnID(turnID),
    onSuccess: sync,
    onSettled: () => setSteeringTurnID(""),
  });

  const retryAttempt = useMutation({
    mutationFn: (attemptID: string) => api.retryAttempt(cardID, attemptID),
    onMutate: (attemptID) => setRetryingAttemptID(attemptID),
    onSuccess: sync,
    onSettled: () => setRetryingAttemptID(""),
  });

  useEffect(() => {
    if (!projectID) return;
    const onMessage = (event: Event) => {
      const detail = (event as CustomEvent<CardMessageDetail>).detail;
      if (
        !detail ||
        detail.project_id !== projectID ||
        !detail.body ||
        detail.viewing
      ) {
        return;
      }
      if (cardID) {
        void queryClient.invalidateQueries({ queryKey: keys.card(cardID) });
      }
      void queryClient.invalidateQueries({
        queryKey: keys.mainDiscussion(projectID),
      });
      if (open) return;
      setPeek(detail.body);
      setUnread((current) => {
        const next = current + 1;
        localStorage.setItem(unreadKey(projectID), String(next));
        return next;
      });
    };
    window.addEventListener(cardMessageEvent, onMessage);
    return () => window.removeEventListener(cardMessageEvent, onMessage);
  }, [cardID, open, projectID]);

  const badge = unread > 9 ? "9+" : String(unread);

  function show() {
    setOpen(true);
    setPeek("");
    setUnread(0);
    localStorage.setItem(unreadKey(projectID), "0");
    if (!cardID) openChat.mutate();
  }

  if (!projectID || hidden) return null;

  return (
    <div className="fixed right-4 bottom-[max(1.25rem,env(safe-area-inset-bottom))] z-40 flex flex-col items-end gap-2">
      {peek && !open && (
        <button
          type="button"
          onClick={show}
          className="max-w-[min(20rem,calc(100vw-2rem))] rounded-2xl border border-line bg-surface px-3 py-2 text-left text-xs text-foreground shadow-lg"
        >
          <span className="line-clamp-2">{peek}</span>
        </button>
      )}

      {open && (
        <section
          aria-label={t("主聊天")}
          className="flex h-[min(560px,70dvh)] w-[min(400px,calc(100vw-2rem))] flex-col overflow-hidden rounded-2xl border border-line bg-canvas shadow-xl"
        >
          <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3">
            <img
              src="/cycledock-logo.png"
              alt=""
              className="size-6 object-contain"
              draggable={false}
            />
            <h2 className="min-w-0 flex-1 truncate text-sm font-semibold">
              {card?.title || t("主聊天")}
            </h2>
            <button
              type="button"
              title={t("在完整页面打开")}
              aria-label={t("在完整页面打开")}
              disabled={!cardID}
              onClick={() =>
                cardID &&
                navigate(
                  `/chat/${encodeURIComponent(projectID)}/${encodeURIComponent(cardID)}`,
                )
              }
              className="grid size-8 place-items-center rounded-lg text-muted-foreground hover:bg-muted disabled:opacity-40"
            >
              <Expand className="size-4" />
            </button>
            <button
              type="button"
              title={t("关闭主聊天")}
              aria-label={t("关闭主聊天")}
              onClick={() => setOpen(false)}
              className="grid size-8 place-items-center rounded-lg text-muted-foreground hover:bg-muted"
            >
              <X className="size-4" />
            </button>
          </header>

          <div className="flex min-h-0 flex-1 flex-col">
            {!cardID || detail.isPending || openChat.isPending ? (
              <div className="grid flex-1 place-items-center">
                <Spinner />
              </div>
            ) : (
              <ConversationView
                events={detail.data?.timeline || []}
                live={stream.live}
                cardKind="discussion"
                pendingTurns={detail.data?.pending_turns || []}
                currentAttempt={current || null}
                attemptHistory={detail.data?.attempt_history || []}
                bottomSpacerClass="h-4 shrink-0"
                retryingAttemptID={retryingAttemptID}
                onRetryAttempt={(attemptID) => retryAttempt.mutate(attemptID)}
                onChoose={(choice) => choose.mutate(choice)}
                choicePending={choose.isPending}
                waitingAttemptID={waiting ? current?.id : undefined}
                onReply={setReplyTo}
              />
            )}
          </div>

          <div className="shrink-0 space-y-2 border-t border-line bg-canvas p-3">
            <PendingTurnQueue
              turns={detail.data?.pending_turns || []}
              cancelingTurnID={cancelingTurnID}
              steeringTurnID={steeringTurnID}
              onCancel={(turnID) => cancelTurn.mutate(turnID)}
              onSteer={(turnID) => steerTurn.mutate(turnID)}
              onEdit={async (turnID, content) => {
                await api.editTurn(cardID, turnID, content);
                await sync();
              }}
            />
            <ConversationComposer
              comment={comment}
              setComment={setComment}
              replyTo={replyTo}
              onCancelReply={() => setReplyTo(null)}
              blocked={Boolean(blocked) || !cardID}
              hasCurrent={hasCurrent}
              sendPending={send.isPending}
              streamConnected={stream.connected || !cardID}
              streamError={stream.lastError}
              cancelPending={cancelAttempt.isPending}
              onSubmit={() => {
                if (!comment.trim() || blocked || send.isPending) return;
                send.mutate(waiting ? "reply" : "enqueue");
              }}
              onCancel={() => cancelAttempt.mutate()}
            />
          </div>
        </section>
      )}

      {!open && (
        <button
          type="button"
          title={t("打开主聊天")}
          aria-label={t("打开主聊天")}
          onClick={show}
          className="relative grid size-14 place-items-center rounded-full border border-line bg-surface shadow-lg transition hover:bg-muted active:scale-95"
        >
          <img
            src="/cycledock-logo.png"
            alt=""
            className="size-8 object-contain"
            draggable={false}
          />
          {unread > 0 && (
            <span className="absolute -top-1 -right-1 grid min-w-5 place-items-center rounded-full bg-primary px-1 text-[10px] font-semibold text-primary-foreground">
              {badge}
            </span>
          )}
        </button>
      )}
    </div>
  );
}
