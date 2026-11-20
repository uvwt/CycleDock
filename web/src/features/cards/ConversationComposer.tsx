import { useRef, useState } from "react";
import { ModelSetupNotice } from "../models/ModelSetupNotice";
import { useAgentStatus } from "../models/useAgentStatus";
import { ChatInput, ChatReplyBar } from "../../shared/ui/chat";
import { ArrowUp, ImagePlus, LoaderCircle, Square } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { QuotedMessage } from "../timeline/ConversationView";
import { ModelMenu } from "../models/ModelMenu";
import { ComposerImageTray } from "../timeline/AttachmentGallery";
import {
  MAX_COMPOSER_IMAGES,
  type ComposerImageItem,
} from "./useComposerImages";

export function ConversationComposer({
  cardID,
  projectID,
  selectedRouteId,
  comment,
  setComment,
  images,
  uploadingImages = false,
  onAddImages,
  onRemoveImage,
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
  images?: ComposerImageItem[];
  uploadingImages?: boolean;
  onAddImages?: (files: FileList | File[]) => void;
  onRemoveImage?: (localID: string) => void;
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
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [dragOver, setDragOver] = useState(false);

  const imageList = images ?? [];
  const hasReadyImages = imageList.some(
    (item) => item.status === "ready" && Boolean(item.attachment),
  );
  const hasContent = Boolean(comment.trim()) || hasReadyImages;

  const sendDisabled =
    sendPending ||
    uploadingImages ||
    !hasContent ||
    blocked ||
    !modelConfigured;
  const showStop = hasCurrent && !hasContent && !uploadingImages;
  const actionLabel = showStop ? t("中止") : t("发送");
  const canAttach =
    Boolean(onAddImages) &&
    !blocked &&
    imageList.length < MAX_COMPOSER_IMAGES;

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (!sendDisabled && !cancelPending) onSubmit();
      }}
      onDragOver={(event) => {
        if (!onAddImages || blocked) return;
        if (Array.from(event.dataTransfer.types).includes("Files")) {
          event.preventDefault();
          if (!dragOver) setDragOver(true);
        }
      }}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
          setDragOver(false);
        }
      }}
      onDrop={(event) => {
        setDragOver(false);
        if (!onAddImages || blocked) return;
        const files = Array.from(event.dataTransfer.files).filter((file) =>
          file.type.startsWith("image/"),
        );
        if (files.length > 0) {
          event.preventDefault();
          onAddImages(files);
        }
      }}
      className={`relative flex flex-col rounded-2xl border bg-surface p-2 shadow-xs transition-[border-color,box-shadow] focus-within:border-primary/60 ${
        dragOver
          ? "border-primary ring-2 ring-primary/25"
          : "border-line"
      }`}
    >
      {replyTo && onCancelReply && (
        <ChatReplyBar
          sender={replyTo.sender}
          text={replyTo.text}
          onClose={onCancelReply}
        />
      )}

      <ModelSetupNotice />

      {imageList.length > 0 && onRemoveImage && (
        <ComposerImageTray items={imageList} onRemove={onRemoveImage} />
      )}

      {/* 文本输入区域：占满行宽自适应高度，并监听剪贴板图片粘贴 */}
      <div className="min-w-0 flex-1 px-1">
        <ChatInput
          value={comment}
          onChange={setComment}
          placeholder={placeholder}
          readOnly={blocked}
          onPaste={(event) => {
            if (!onAddImages || blocked) return;
            const pastedFiles: File[] = [];
            for (const item of Array.from(event.clipboardData.items)) {
              if (item.kind === "file" && item.type.startsWith("image/")) {
                const file = item.getAsFile();
                if (file) pastedFiles.push(file);
              }
            }
            if (pastedFiles.length > 0) {
              event.preventDefault();
              onAddImages(pastedFiles);
            }
          }}
        />
      </div>

      {/* 底部操作栏：左侧放置模型切换与选图按钮，右侧放置发送/中止操作 */}
      <div className="flex items-center justify-between gap-2 px-1 pt-1.5">
        <div className="flex items-center gap-1.5 min-w-0">
          <ModelMenu
            variant="composer"
            cardID={cardID}
            projectID={projectID}
            selectedRouteId={selectedRouteId}
          />
          {onAddImages && (
            <>
              <input
                ref={fileInputRef}
                type="file"
                accept="image/png,image/jpeg,image/webp,image/gif"
                multiple
                className="hidden"
                data-testid="composer-image-input"
                onChange={(event) => {
                  const files = event.target.files;
                  if (files && files.length > 0) {
                    onAddImages(files);
                  }
                  event.target.value = "";
                }}
              />
              <button
                type="button"
                disabled={!canAttach}
                onClick={() => fileInputRef.current?.click()}
                title={t("添加图片（支持粘贴或拖拽）")}
                aria-label={t("添加图片")}
                data-testid="composer-image-button"
                className="inline-flex size-8 cursor-pointer items-center justify-center rounded-lg text-muted-foreground transition hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40"
              >
                <ImagePlus className="size-4" />
              </button>
            </>
          )}
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
            {cancelPending || uploadingImages ? (
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
