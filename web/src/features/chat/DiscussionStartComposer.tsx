import { useDeviceSelection } from "../devices/device-selection";
import { ChatInput } from "../../shared/ui/chat";
import { useEffect, useState } from "react";
import { ArrowUp, LoaderCircle } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";

const STORAGE_KEY = "cycledock.new-discussion.drafts.v2";

type SavedDraft = {
  content: string;
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
    if (draft?.content) {
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
  const { target } = useDeviceSelection();
  const { t } = useTranslation();
  const navigate = useNavigate();
  const initial = readDraft(projectID);
  const [content, setContent] = useState(initial.content);
  const [sending, setSending] = useState(false);

  useEffect(() => {
    writeDraft(projectID, { content });
  }, [content, projectID]);

  async function createAndSend() {
    const value = content.trim();
    if (!value || sending) return;
    setSending(true);
    // 发起发送时固定设备选择；创建 Card 的网络等待期间切换设备不改变这条消息。
    const deviceTarget = target;
    try {
      const card = await api.createDiscussion(projectID);
      // 清理草稿并后台触发项目卡片列表更新，不阻塞导航跳转
      writeDraft(projectID);
      setContent("");
      void queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
      navigate(`/chat/${encodeURIComponent(projectID)}/${encodeURIComponent(card.id)}`, {
        state: {
          initialMessage: value,
          initialDeviceTarget: deviceTarget,
          initialIdempotencyKey: crypto.randomUUID(),
        },
      });
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("新讨论创建失败，草稿已保留"),
      );
      setSending(false);
    }
  }

  const placeholder = t("发送消息，开始讨论…");

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void createAndSend();
      }}
      className="mx-auto flex items-end gap-2 rounded-2xl border border-line bg-surface p-1.5 shadow-sm transition-[border-color,box-shadow] focus-within:border-primary/60"
    >
      <div className="flex min-w-0 flex-1 items-end pl-1.5">
        <ChatInput
          value={content}
          onChange={setContent}
          placeholder={placeholder}
          disabled={sending}
        />
      </div>

      <div className="flex shrink-0 items-center gap-1.5 pr-0.5 pb-1">
        <button
          type="submit"
          title={t("发送")}
          aria-label={t("发送")}
          disabled={sending || !content.trim()}
          className="grid size-9 shrink-0 place-items-center rounded-full bg-action text-action-foreground shadow-sm transition hover:bg-action-hover active:scale-95 disabled:cursor-not-allowed disabled:opacity-35"
        >
          {sending ? (
            <LoaderCircle className="size-4 animate-spin" />
          ) : (
            <ArrowUp className="size-4" />
          )}
        </button>
      </div>
    </form>
  );
}

