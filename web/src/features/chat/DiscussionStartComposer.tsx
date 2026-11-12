import { ModelSetupNotice } from "../models/ModelSetupNotice";
import { useAgentStatus } from "../models/useAgentStatus";
import { useDeviceSelection } from "../devices/device-selection";
import type { DeviceTarget } from "../../shared/api/types";
import { safeRandomUUID } from "../../shared/lib/utils";
import { ChatInput } from "../../shared/ui/chat";
import { useEffect, useState } from "react";
import { ArrowUp, LoaderCircle, RotateCcw } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { loadCardPage } from "../../app/route-preload";
import { upsertCachedProjectCard } from "../../shared/lib/card-cache";

const STORAGE_KEY = "cycledock.new-discussion.drafts.v2";

type SavedDraft = {
  content: string;
  deviceTarget?: DeviceTarget | null;
  preferredModelRouteID?: string;
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

import { ModelMenu } from "../models/ModelMenu";
export function DiscussionStartComposer({ projectID }: { projectID: string }) {
  const { target } = useDeviceSelection();
  const [draftRouteId, setDraftRouteId] = useState<string | null>(null);
  const { t } = useTranslation();
  const agentStatus = useAgentStatus();
  const modelConfigured = agentStatus.data?.model_configured === true;
  const navigate = useNavigate();
  const initial = readDraft(projectID);
  const [content, setContent] = useState(initial.content);
  const [pending, setPending] = useState<SavedDraft | null>(
    initial.pendingCardID ? initial : null,
  );
  const [sending, setSending] = useState(false);

  useEffect(() => {
    if (pending) return;
    writeDraft(projectID, { content });
  }, [content, pending, projectID]);

  async function finish(cardID: string, draft: SavedDraft) {
    await api.send(
      cardID,
      draft.content,
      "enqueue",
      draft.idempotencyKey,
      draft.deviceTarget,
      draft.preferredModelRouteID,
    );
    writeDraft(projectID);
    setPending(null);
    setContent("");
    void queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
    navigate(
      `/chat/${encodeURIComponent(projectID)}/${encodeURIComponent(cardID)}`,
    );
  }

  async function createAndSend() {
    const value = content.trim();
    if (!value || sending || !modelConfigured) return;
    setSending(true);
    void loadCardPage();
    // 发起发送时固定设备选择与模型选择；创建 Card 的网络等待期间切换不改变这条消息。
    const deviceTarget = target;
    const preferredModelRouteID = draftRouteId || undefined;
    try {
      const card = await api.createDiscussion(projectID, preferredModelRouteID);
      upsertCachedProjectCard(queryClient, projectID, card);
      void queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
      const draft: SavedDraft = {
        content: value,
        deviceTarget,
        preferredModelRouteID,
        pendingCardID: card.id,
        idempotencyKey: safeRandomUUID(),
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
    if (
      !pending?.pendingCardID ||
      !pending.idempotencyKey ||
      sending ||
      !modelConfigured
    )
      return;
    setSending(true);
    void loadCardPage();
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
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void (pending ? retry() : createAndSend());
      }}
      className="mx-auto flex flex-col rounded-2xl border border-line bg-surface p-2 shadow-xs transition-[border-color,box-shadow] focus-within:border-primary/60"
    >
      <ModelSetupNotice />
      {/* 文本输入区域：占满行宽自适应高度 */}
      <div className="min-w-0 flex-1 px-1">
        <ChatInput
          value={value}
          onChange={setContent}
          placeholder={placeholder}
          readOnly={Boolean(pending)}
          disabled={sending}
        />
      </div>

      {/* 底部操作栏：左侧放置模型切换等控制，右侧放置发送/重试 */}
      <div className="flex items-center justify-between gap-2 px-1 pt-1">
        <div className="flex items-center gap-1.5 min-w-0">
          <ModelMenu
            variant="composer"
            selectedRouteId={draftRouteId}
            onSelectRouteId={setDraftRouteId}
          />
        </div>

        <div className="flex shrink-0 items-center gap-1.5">
          {!sending && pending?.pendingCardID && (
            <button
              type="button"
              onMouseDown={(event) => event.preventDefault()}
              onClick={() =>
                navigate(
                  `/chat/${encodeURIComponent(projectID)}/${encodeURIComponent(pending.pendingCardID!)}`,
                )
              }
              className="rounded-lg px-2.5 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted"
            >
              {t("打开讨论")}
            </button>
          )}
          <button
            type="submit"
            title={pending ? t("重试发送") : t("发送")}
            aria-label={pending ? t("重试发送") : t("发送")}
            disabled={
              sending || !modelConfigured || (!pending && !content.trim())
            }
            className="grid size-9 shrink-0 place-items-center rounded-full bg-action text-action-foreground shadow-sm transition hover:bg-action-hover active:scale-95 disabled:cursor-not-allowed disabled:opacity-35"
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
      </div>
    </form>
  );
}
