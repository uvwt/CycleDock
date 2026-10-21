import { useEffect, useRef, useState } from "react";
import {
  LoaderCircle,
  MessageSquarePlus,
  RotateCcw,
  Send,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";

const STORAGE_KEY = "cycledock.new-discussion.drafts.v1";

type SavedDraft = {
  content: string;
  pendingCardID?: string;
  idempotencyKey?: string;
};

function readDraft(projectID: string): SavedDraft {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return { content: "" };
    const drafts = JSON.parse(raw) as Record<string, SavedDraft>;
    return drafts[projectID] || { content: "" };
  } catch {
    return { content: "" };
  }
}

function writeDraft(projectID: string, draft?: SavedDraft) {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    const drafts = raw ? (JSON.parse(raw) as Record<string, SavedDraft>) : {};
    if (draft?.content || draft?.pendingCardID) drafts[projectID] = draft;
    else delete drafts[projectID];
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(drafts));
  } catch {
    // Local storage may be unavailable; in-memory state still protects the current session.
  }
}

export function NewDiscussionComposer({
  projectID,
  projectName,
  openToken,
}: {
  projectID: string;
  projectName: string;
  openToken: number;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const initial = readDraft(projectID);
  const [content, setContent] = useState(initial.content);
  const [pending, setPending] = useState<SavedDraft | null>(
    initial.pendingCardID ? initial : null,
  );
  const [expanded, setExpanded] = useState(
    Boolean(initial.content || initial.pendingCardID),
  );
  const [sending, setSending] = useState(false);
  const input = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (!openToken) return;
    const handle = window.setTimeout(() => {
      setExpanded(true);
      input.current?.focus();
    }, 0);
    return () => window.clearTimeout(handle);
  }, [openToken]);

  useEffect(() => {
    if (pending) return;
    writeDraft(projectID, { content });
  }, [content, pending, projectID]);

  async function finish(cardID: string, draft: SavedDraft) {
    await api.send(cardID, draft.content, "enqueue", draft.idempotencyKey);
    writeDraft(projectID);
    setPending(null);
    setContent("");
    await queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
    navigate(`/chat/${projectID}/${cardID}`);
  }

  async function submit() {
    const value = content.trim();
    if (!value || sending) return;
    setSending(true);
    try {
      const card = await api.createDiscussion(projectID);
      await queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
      const draft: SavedDraft = {
        content: value,
        pendingCardID: card.id,
        idempotencyKey: crypto.randomUUID(),
      };
      // Card 一旦创建就先持久保存其身份；首条 Comment 失败时绝不再创建第二张 Card。
      writeDraft(projectID, draft);
      setPending(draft);
      await finish(card.id, draft);
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("新讨论发送失败，草稿已保留"),
      );
    } finally {
      setSending(false);
    }
  }

  async function retry() {
    if (!pending?.pendingCardID || !pending.idempotencyKey || sending) return;
    setSending(true);
    try {
      await finish(pending.pendingCardID, pending);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("重试失败，草稿仍已保留"),
      );
    } finally {
      setSending(false);
    }
  }

  if (!expanded) {
    return (
      <button
        type="button"
        onClick={() => {
          setExpanded(true);
          window.setTimeout(() => input.current?.focus(), 0);
        }}
        className="fixed right-4 bottom-4 z-20 flex items-center gap-2 rounded-2xl border border-slate-200 bg-white px-4 py-3 text-sm font-medium text-slate-700 shadow-lg hover:bg-slate-50 sm:right-6 sm:bottom-6"
      >
        <MessageSquarePlus className="size-4 text-sky-700" />
        {t("新讨论")}
      </button>
    );
  }

  return (
    <section className="fixed right-3 bottom-3 left-3 z-20 rounded-2xl border border-slate-200 bg-white p-3 shadow-xl sm:right-6 sm:bottom-6 sm:left-auto sm:w-[min(520px,calc(100vw-3rem))]">
      <header className="mb-2 flex items-center justify-between gap-3 px-1">
        <p className="min-w-0 truncate text-xs font-semibold text-slate-500">
          {t("新讨论")} · {projectName}
        </p>
        {!pending && !content.trim() && (
          <button
            type="button"
            title={t("收起")}
            aria-label={t("收起")}
            onClick={() => setExpanded(false)}
            className="grid size-7 place-items-center rounded-lg text-slate-400 hover:bg-slate-100"
          >
            <X className="size-3.5" />
          </button>
        )}
      </header>

      {pending && (
        <div className="mb-2 rounded-xl bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800">
          {t(
            "讨论已创建，但首条消息还未确认送达。重试会复用同一 Card 和同一幂等键，不会重复创建讨论。",
          )}
        </div>
      )}
      <textarea
        ref={input}
        rows={expanded ? 3 : 1}
        value={pending?.content ?? content}
        readOnly={Boolean(pending)}
        onChange={(event) => setContent(event.target.value)}
        onKeyDown={(event) => {
          if (
            event.key === "Enter" &&
            !event.shiftKey &&
            !event.nativeEvent.isComposing &&
            !pending
          ) {
            event.preventDefault();
            void submit();
          }
        }}
        placeholder={t("从一句话开始新的讨论…")}
        className="max-h-40 min-h-20 w-full resize-none rounded-xl border border-slate-200 bg-white px-3 py-2.5 text-sm leading-6 outline-none focus:border-sky-500 read-only:bg-slate-50"
      />
      <div className="mt-2 flex items-center justify-between gap-2">
        <p className="text-[11px] text-slate-400">
          {pending
            ? t("原始草稿已锁定以保证安全重试")
            : t("Enter 发送 · Shift+Enter 换行")}
        </p>
        <div className="flex shrink-0 items-center gap-1.5">
          {pending?.pendingCardID && (
            <button
              type="button"
              onClick={() =>
                navigate(`/chat/${projectID}/${pending.pendingCardID}`)
              }
              className="rounded-xl px-3 py-2 text-xs font-medium text-slate-600 hover:bg-slate-100"
            >
              {t("打开讨论")}
            </button>
          )}
          <button
            type="button"
            disabled={
              sending || (pending ? !pending.pendingCardID : !content.trim())
            }
            onClick={() => void (pending ? retry() : submit())}
            className="grid size-9 place-items-center rounded-xl bg-slate-900 text-white disabled:opacity-40"
            title={pending ? t("重试发送") : t("发送")}
            aria-label={pending ? t("重试发送") : t("发送")}
          >
            {sending ? (
              <LoaderCircle className="size-4 animate-spin" />
            ) : pending ? (
              <RotateCcw className="size-4" />
            ) : (
              <Send className="size-4" />
            )}
          </button>
        </div>
      </div>
    </section>
  );
}
