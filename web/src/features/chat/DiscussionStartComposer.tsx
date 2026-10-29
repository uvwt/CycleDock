import { ComposerInput } from "../../shared/ui/ComposerInput";
import { useEffect, useState } from "react";
import { ArrowUp, LoaderCircle, RotateCcw } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";

const STORAGE_KEY = "cycledock.new-discussion.drafts.v2";

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
    if (draft?.content || draft?.pendingCardID) {
      drafts[projectID] = draft;
    } else {
      delete drafts[projectID];
    }
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(drafts));
  } catch {
    // Storage 不可用时仍保留当前组件内存状态，不阻断创建讨论。
  }
}

export function DiscussionStartComposer({ projectID }: { projectID: string }) {
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

  useEffect(() => {
    if (pending) return;
    writeDraft(projectID, { content });
  }, [content, pending, projectID]);

  async function finish(cardID: string, draft: SavedDraft) {
    await api.send(cardID, draft.content, "enqueue", draft.idempotencyKey);
    writeDraft(projectID);
    setPending(null);
    setContent("");
    setExpanded(false);
    await queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
    navigate(`/chat/${projectID}/${cardID}`);
  }

  async function createAndSend() {
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
      // Card 创建成功后先固化 Card ID 与幂等键；首条消息失败时只能重试该 Card，
      // 不能重新创建第二张 Discussion。
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

  const value = pending?.content ?? content;
  const placeholder = t("发送消息，开始讨论…");

  return (
    <div>
      {pending && (
        <div className="mb-2 rounded-xl bg-warning-muted px-3 py-2 text-xs leading-5 text-warning">
          {t(
            "讨论已创建，但首条消息还未确认送达。重试会复用同一 Card，不会重复创建讨论。",
          )}
        </div>
      )}

      <form
        onSubmit={(event) => {
          event.preventDefault();
          void (pending ? retry() : createAndSend());
        }}
        className={`mx-auto border bg-surface transition-all focus-within:border-line-strong ${
          expanded
            ? "rounded-2xl border-line p-2 shadow-sm"
            : "flex items-center rounded-full border-line py-1 pr-1 pl-4 shadow-sm"
        }`}
      >
        <ComposerInput
          value={value}
          onChange={setContent}
          expanded={expanded}
          onExpandedChange={setExpanded}
          placeholder={placeholder}
          readOnly={Boolean(pending)}
          keepExpanded={Boolean(pending)}
        />

        <div
          className={
            expanded
              ? "flex items-center justify-end gap-2 px-1 pb-1"
              : "shrink-0"
          }
        >
          {pending?.pendingCardID && expanded && (
            <button
              type="button"
              onMouseDown={(event) => event.preventDefault()}
              onClick={() =>
                navigate(`/chat/${projectID}/${pending.pendingCardID}`)
              }
              className="rounded-lg px-2.5 py-2 text-xs font-medium text-muted-foreground hover:bg-muted"
            >
              {t("打开讨论")}
            </button>
          )}
          <button
            type="submit"
            title={pending ? t("重试发送") : t("发送")}
            aria-label={pending ? t("重试发送") : t("发送")}
            disabled={sending || (!pending && !content.trim())}
            className="grid size-9 place-items-center rounded-full bg-action text-action-foreground transition hover:bg-action-hover disabled:opacity-30"
          >
            {sending ? (
              <LoaderCircle className="size-4 animate-spin" />
            ) : pending ? (
              <RotateCcw className="size-4" />
            ) : (
              <ArrowUp className="size-4" />
            )}
          </button>
        </div>
      </form>
    </div>
  );
}
