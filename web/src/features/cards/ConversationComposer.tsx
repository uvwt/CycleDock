import { ModelSetupNotice } from "../models/ModelSetupNotice";
import { useAgentStatus } from "../models/useAgentStatus";
import { ChatInput, ChatReplyBar } from "../../shared/ui/chat";
import { ArrowUp, LoaderCircle, Square } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { QuotedMessage } from "../timeline/ConversationView";
import { ModelMenu } from "../models/ModelMenu";

export function ConversationComposer({
  cardID,
  projectID,
  selectedRouteId,
  comment,
  setComment,
  replyTo,
  onCancelReply,
  blocked,
  hasCurrent,
  sendPending,
  streamConnected,
  streamError,
  cancelPending,
  onSubmit,
  onCancel,
}: {
  cardID?: string;
  projectID?: string;
  selectedRouteId?: string | null;
  comment: string;
  setComment: (value: string) => void;
  replyTo?: QuotedMessage | null;
  onCancelReply?: () => void;
  expanded?: boolean;
  setExpanded?: (value: boolean) => void;
  blocked: boolean;
  hasCurrent: boolean;
  sendPending: boolean;
  streamConnected: boolean;
  streamError?: string;
  cancelPending: boolean;
  onSubmit: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const agentStatus = useAgentStatus();
  const modelConfigured = agentStatus.data?.model_configured === true;
  const placeholder = blocked ? t("讨论已归档") : t("发送消息，继续推进任务…");

  const sendDisabled =
    sendPending || !comment.trim() || blocked || !modelConfigured;
  const showStop = hasCurrent && !comment.trim();
  const actionLabel = showStop ? t("中止") : t("发送");

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (!sendDisabled && !cancelPending) onSubmit();
      }}
      className="relative flex flex-col rounded-2xl border border-line bg-surface p-2 shadow-xs transition-[border-color,box-shadow] focus-within:border-primary/60"
    >
      {replyTo && onCancelReply && (
        <ChatReplyBar
          sender={replyTo.sender}
          text={replyTo.text}
          onClose={onCancelReply}
        />
      )}

      <ModelSetupNotice />

      {/* 文本输入区域：占满行宽自适应高度 */}
      <div className="min-w-0 flex-1 px-1">
        <ChatInput
          value={comment}
          onChange={setComment}
          placeholder={placeholder}
          readOnly={blocked}
        />
      </div>

      {/* 底部操作栏：左侧放置模型切换等上下文控制，右侧放置发送/中止操作 */}
      <div className="flex items-center justify-between gap-2 px-1 pt-1.5">
        <div className="flex items-center gap-1.5 min-w-0">
          <ModelMenu
            variant="composer"
            cardID={cardID}
            projectID={projectID}
            selectedRouteId={selectedRouteId}
          />
        </div>

        <div className="flex shrink-0 items-center gap-1.5">
          {!streamConnected && (
            <span title={streamError} className="mr-1 text-[11px] text-faint">
              {t("正在连接…")}
            </span>
          )}
          <button
            type={showStop ? "button" : "submit"}
            title={actionLabel}
            aria-label={actionLabel}
            onClick={showStop ? onCancel : undefined}
            disabled={cancelPending || (!showStop && sendDisabled)}
            className={`inline-flex shrink-0 items-center justify-center gap-1.5 rounded-full shadow-sm transition active:scale-95 disabled:cursor-not-allowed disabled:opacity-35 ${
              showStop
                ? "size-8.5 bg-danger text-white hover:bg-danger/90"
                : "size-8.5 bg-action text-action-foreground hover:bg-action-hover"
            }`}
          >
            {cancelPending ? (
              <LoaderCircle className="size-4 animate-spin" />
            ) : showStop ? (
              <Square className="size-3.5 fill-current" />
            ) : (
              <ArrowUp className="size-4" />
            )}
          </button>
        </div>
      </div>
    </form>
  );
}
