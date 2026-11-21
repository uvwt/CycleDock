import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { ImageOff, Loader2, X, ZoomIn } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { AttachmentMeta } from "../../shared/api/types";
import type { ComposerImageItem } from "../cards/useComposerImages";

/** 输入框待发送图片缩略图条，展示上传中/失败/就绪状态并支持移除。 */
export function ComposerImageTray({
  items,
  onRemove,
}: {
  items: ComposerImageItem[];
  onRemove: (localID: string) => void;
}) {
  const { t } = useTranslation();
  if (items.length === 0) return null;

  return (
    <div
      className="flex flex-wrap gap-2 px-2 pt-1.5 pb-1"
      data-testid="composer-image-tray"
    >
      {items.map((item) => (
        <div
          key={item.localID}
          className="group relative size-16 overflow-hidden rounded-xl border border-line bg-muted/40 shadow-2xs"
        >
          <img
            src={item.previewURL}
            alt={item.name}
            className={`size-full object-cover transition ${
              item.status === "uploading" ? "opacity-55 blur-[1px]" : ""
            } ${item.status === "error" ? "opacity-40 grayscale" : ""}`}
          />
          {item.status === "uploading" && (
            <div className="absolute inset-0 flex items-center justify-center bg-black/25 text-white">
              <Loader2 className="size-4 animate-spin" />
            </div>
          )}
          {item.status === "error" && (
            <div
              title={item.error}
              className="absolute inset-0 flex items-center justify-center bg-danger/25 px-1 text-center text-[10px] font-medium text-danger"
            >
              {t("失败")}
            </div>
          )}
          <button
            type="button"
            onClick={() => onRemove(item.localID)}
            title={t("移除图片")}
            aria-label={t("移除图片")}
            className="absolute top-1 right-1 grid size-5 cursor-pointer place-items-center rounded-full bg-black/65 text-white opacity-90 transition hover:bg-black"
          >
            <X className="size-3" />
          </button>
        </div>
      ))}
    </div>
  );
}

/** 气泡内单张附件图片：加载失败（含 410 Gone 过期清理或 404）时优雅渲染为柔和占位，不出现裂图。 */
function AttachmentImageCard({
  attachment,
  onPreview,
}: {
  attachment: AttachmentMeta;
  onPreview: (attachment: AttachmentMeta) => void;
}) {
  const { t } = useTranslation();
  const [failed, setFailed] = useState(false);

  // 若元数据已明确标记为 purged/expired 或运行时请求返回 404/410 触发 onError，直接渲染柔和占位避免无意义请求或破图。
  if (failed || attachment.status === "purged" || attachment.status === "expired") {
    return (
      <div
        data-testid="attachment-expired-placeholder"
        className="flex min-h-[88px] min-w-[148px] max-w-[240px] flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-line/80 bg-muted/50 px-4 py-4 text-center text-muted-foreground"
      >
        <ImageOff className="size-5 opacity-70" />
        <span className="text-xs font-medium">{t("图片已过期")}</span>
      </div>
    );
  }

  return (
    <button
      type="button"
      onClick={() => onPreview(attachment)}
      className="group relative block overflow-hidden rounded-xl border border-line/60 bg-muted/20 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/50"
      title={t("点击查看大图")}
    >
      <img
        src={attachment.url}
        alt={t("消息图片")}
        loading="lazy"
        onError={() => setFailed(true)}
        className="max-h-64 min-h-[72px] w-full max-w-[280px] object-cover transition duration-200 group-hover:scale-[1.02]"
      />
      <span className="pointer-events-none absolute right-2 bottom-2 grid size-6 place-items-center rounded-full bg-black/50 text-white opacity-0 transition group-hover:opacity-100">
        <ZoomIn className="size-3.5" />
      </span>
    </button>
  );
}

/** 聊天气泡内的图片附件网格，并自带大图灯箱预览弹窗。 */
export function MessageAttachmentGrid({
  attachments,
}: {
  attachments: AttachmentMeta[];
}) {
  const { t } = useTranslation();
  const [lightbox, setLightbox] = useState<AttachmentMeta | null>(null);

  useEffect(() => {
    if (!lightbox) return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") setLightbox(null);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [lightbox]);

  if (!attachments || attachments.length === 0) return null;

  const gridCols =
    attachments.length === 1
      ? "grid-cols-1"
      : attachments.length === 2
        ? "grid-cols-2"
        : "grid-cols-2 sm:grid-cols-3";

  return (
    <>
      <div
        className={`mt-2 grid gap-2 ${gridCols}`}
        data-testid="message-attachment-grid"
      >
        {attachments.map((att) => (
          <AttachmentImageCard
            key={att.id}
            attachment={att}
            onPreview={setLightbox}
          />
        ))}
      </div>

      {lightbox &&
        typeof document !== "undefined" &&
        createPortal(
          <div
            role="dialog"
            aria-modal="true"
            aria-label={t("图片大图预览")}
            data-testid="attachment-lightbox"
            onClick={() => setLightbox(null)}
            className="fixed inset-0 z-[100] flex items-center justify-center bg-black/80 p-4 backdrop-blur-xs"
          >
            <button
              type="button"
              onClick={() => setLightbox(null)}
              aria-label={t("关闭预览")}
              className="absolute top-4 right-4 grid size-9 cursor-pointer place-items-center rounded-full bg-white/15 text-white transition hover:bg-white/25"
            >
              <X className="size-5" />
            </button>
            <img
              src={lightbox.url}
              alt={t("消息图片大图")}
              onClick={(e) => e.stopPropagation()}
              className="max-h-[88vh] max-w-[92vw] rounded-xl object-contain shadow-2xl"
            />
          </div>,
          document.body,
        )}
    </>
  );
}
