import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import {
  Check,
  Copy,
  Download,
  ExternalLink,
  FileCode2,
  FileText,
  FileX2,
  ImageOff,
  Loader2,
  X,
  ZoomIn,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import type { AttachmentMeta } from "../../shared/api/types";
import {
  isTextAttachmentMeta,
  type ComposerImageItem,
} from "../cards/useComposerImages";

const CODE_EXTENSIONS = new Set([
  ".go",
  ".ts",
  ".tsx",
  ".js",
  ".jsx",
  ".mjs",
  ".cjs",
  ".py",
  ".rs",
  ".java",
  ".kt",
  ".kts",
  ".swift",
  ".rb",
  ".php",
  ".c",
  ".cc",
  ".cpp",
  ".cxx",
  ".h",
  ".hh",
  ".hpp",
  ".cs",
  ".css",
  ".scss",
  ".less",
  ".html",
  ".htm",
  ".sql",
  ".sh",
  ".bash",
  ".zsh",
  ".ps1",
  ".json",
  ".yaml",
  ".yml",
  ".toml",
  ".xml",
  ".diff",
  ".patch",
]);

function isCodeFileName(fileName?: string): boolean {
  if (!fileName) return false;
  const lower = fileName.toLowerCase();
  const dotIdx = lower.lastIndexOf(".");
  if (dotIdx <= 0) return false;
  return CODE_EXTENSIONS.has(lower.slice(dotIdx));
}

function formatFileSize(bytes?: number): string {
  if (!bytes || bytes <= 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) {
    const kb = bytes / 1024;
    return `${kb >= 100 ? kb.toFixed(0) : kb.toFixed(1)} KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function withDownloadQuery(url: string): string {
  return url.includes("?") ? `${url}&download=1` : `${url}?download=1`;
}

/** 输入框待发送附件条：同时展示图片缩略图与文本/代码文件芯片，展示上传中/失败/就绪状态并支持移除。 */
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
      {items.map((item) =>
        item.kind === "text" ? (
          <div
            key={item.localID}
            data-testid="composer-text-chip"
            className={`group relative flex h-16 max-w-[220px] min-w-[144px] items-center gap-2.5 rounded-xl border px-3 pr-7 shadow-2xs transition ${
              item.status === "error"
                ? "border-danger/50 bg-danger/5"
                : "border-line bg-muted/40"
            }`}
          >
            <div className="grid size-8 shrink-0 place-items-center rounded-lg bg-surface text-muted-foreground ring-1 ring-line/60">
              {item.status === "uploading" ? (
                <Loader2 className="size-4 animate-spin text-primary" />
              ) : isCodeFileName(item.name) ? (
                <FileCode2 className="size-4" />
              ) : (
                <FileText className="size-4" />
              )}
            </div>
            <div className="min-w-0 flex-1">
              <div
                title={item.name}
                className="truncate text-xs font-medium text-foreground"
              >
                {item.name}
              </div>
              <div className="mt-0.5 text-[11px] tabular-nums text-muted-foreground">
                {item.status === "error" ? (
                  <span title={item.error} className="text-danger">
                    {t("上传失败")}
                  </span>
                ) : item.status === "uploading" ? (
                  t("上传中...")
                ) : (
                  formatFileSize(item.sizeBytes)
                )}
              </div>
            </div>
            <button
              type="button"
              onClick={() => onRemove(item.localID)}
              title={t("移除附件")}
              aria-label={t("移除附件")}
              className="absolute top-1.5 right-1.5 grid size-5 cursor-pointer place-items-center rounded-full bg-black/55 text-white opacity-85 transition hover:bg-black"
            >
              <X className="size-3" />
            </button>
          </div>
        ) : (
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
        ),
      )}
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
  if (
    failed ||
    attachment.status === "purged" ||
    attachment.status === "expired"
  ) {
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
      className="group relative block overflow-hidden rounded-xl border border-line/70 bg-muted/30 text-left shadow-2xs transition hover:border-primary/50 focus-visible:outline-2 focus-visible:outline-primary cursor-zoom-in"
    >
      <img
        src={attachment.url}
        alt={t("消息附件图片")}
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

/** 气泡与待办详情中的纯文本/日志/代码附件卡片：支持点击展开预览、直接下载及过期占位。 */
function AttachmentTextCard({
  attachment,
  expired,
  onPreview,
}: {
  attachment: AttachmentMeta;
  expired: boolean;
  onPreview: (attachment: AttachmentMeta) => void;
}) {
  const { t } = useTranslation();
  const fileName = attachment.file_name?.trim() || t("文本文件");
  const isExpired =
    expired || attachment.status === "purged" || attachment.status === "expired";

  if (isExpired) {
    return (
      <div
        data-testid="text-attachment-expired-placeholder"
        className="flex min-w-0 max-w-[320px] items-center gap-2.5 rounded-xl border border-dashed border-line/80 bg-muted/45 px-3 py-2.5 text-muted-foreground"
      >
        <div className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted/70">
          <FileX2 className="size-4 opacity-70" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-medium line-through opacity-75">
            {fileName}
          </div>
          <div className="mt-0.5 text-[11px]">{t("文件已过期")}</div>
        </div>
      </div>
    );
  }

  const metaParts: string[] = [formatFileSize(attachment.size_bytes)];
  if (attachment.line_count && attachment.line_count > 0) {
    metaParts.push(t("{{count}} 行", { count: attachment.line_count }));
  }

  return (
    <div
      data-testid="text-attachment-card"
      className="group flex min-w-0 max-w-[340px] items-center gap-2 rounded-xl border border-line/80 bg-surface/90 px-2.5 py-2 shadow-2xs transition hover:border-primary/45 hover:bg-surface"
    >
      <button
        type="button"
        onClick={() => onPreview(attachment)}
        data-testid="text-attachment-preview-trigger"
        className="flex min-w-0 flex-1 cursor-pointer items-center gap-2.5 text-left focus-visible:outline-none"
      >
        <div className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted/70 text-foreground/80 transition group-hover:bg-primary/10 group-hover:text-primary">
          {isCodeFileName(fileName) ? (
            <FileCode2 className="size-4" />
          ) : (
            <FileText className="size-4" />
          )}
        </div>
        <div className="min-w-0 flex-1">
          <div
            title={fileName}
            className="truncate text-xs font-medium text-foreground"
          >
            {fileName}
          </div>
          <div className="mt-0.5 flex items-center gap-1.5 text-[11px] tabular-nums text-muted-foreground">
            <span>{metaParts.join(" · ")}</span>
          </div>
        </div>
      </button>
      <a
        href={withDownloadQuery(attachment.url)}
        download={fileName}
        title={t("下载文件")}
        aria-label={t("下载文件")}
        onClick={(e) => e.stopPropagation()}
        className="grid size-7 shrink-0 cursor-pointer place-items-center rounded-lg text-muted-foreground transition hover:bg-muted hover:text-foreground"
      >
        <Download className="size-3.5" />
      </a>
    </div>
  );
}

/** 文本附件预览弹窗：透明解压展示 UTF-8 原文，支持一键复制与下载。 */
function TextAttachmentPreviewModal({
  attachment,
  onExpired,
  onClose,
}: {
  attachment: AttachmentMeta;
  onExpired: (id: string) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [state, setState] = useState<{
    attachmentID: string;
    content: string;
    loading: boolean;
    errorMsg: string | null;
    expired: boolean;
  }>({
    attachmentID: attachment.id,
    content: "",
    loading: true,
    errorMsg: null,
    expired: false,
  });
  const [copied, setCopied] = useState(false);

  const fileName = attachment.file_name?.trim() || t("文本文件");
  const loading = state.attachmentID !== attachment.id || state.loading;
  const content = state.attachmentID === attachment.id ? state.content : "";
  const errorMsg = state.attachmentID === attachment.id ? state.errorMsg : null;
  const expired = state.attachmentID === attachment.id ? state.expired : false;

  useEffect(() => {
    const controller = new AbortController();

    fetch(attachment.url, {
      credentials: "same-origin",
      signal: controller.signal,
    })
      .then(async (res) => {
        if (res.status === 410 || res.status === 404) {
          setState({
            attachmentID: attachment.id,
            content: "",
            loading: false,
            errorMsg: null,
            expired: true,
          });
          onExpired(attachment.id);
          return;
        }
        if (!res.ok) {
          throw new Error(`HTTP ${res.status}`);
        }
        const text = await res.text();
        setState({
          attachmentID: attachment.id,
          content: text,
          loading: false,
          errorMsg: null,
          expired: false,
        });
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        setState({
          attachmentID: attachment.id,
          content: "",
          loading: false,
          errorMsg: err instanceof Error ? err.message : t("加载文件内容失败"),
          expired: false,
        });
      });

    return () => controller.abort();
  }, [attachment.id, attachment.url, onExpired, t]);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      toast.error(t("复制失败"));
    }
  }

  if (typeof document === "undefined") return null;

  const metaParts: string[] = [formatFileSize(attachment.size_bytes)];
  if (attachment.line_count && attachment.line_count > 0) {
    metaParts.push(t("{{count}} 行", { count: attachment.line_count }));
  }

  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label={fileName}
      data-testid="text-attachment-preview-modal"
      onClick={onClose}
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/65 p-3 sm:p-6 backdrop-blur-xs"
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="flex max-h-[86vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-2xl"
      >
        <div className="flex items-center justify-between gap-3 border-b border-line/70 px-4 py-3">
          <div className="flex min-w-0 items-center gap-2.5">
            <div className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted/70 text-foreground/80">
              {isCodeFileName(fileName) ? (
                <FileCode2 className="size-4" />
              ) : (
                <FileText className="size-4" />
              )}
            </div>
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold text-foreground">
                {fileName}
              </div>
              <div className="text-[11px] tabular-nums text-muted-foreground">
                {metaParts.join(" · ")}
              </div>
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-1.5">
            {!loading && !expired && !errorMsg && (
              <>
                <button
                  type="button"
                  onClick={() => void handleCopy()}
                  className="inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-lg border border-line bg-canvas px-2.5 text-xs font-medium text-foreground transition hover:bg-muted"
                >
                  {copied ? (
                    <Check className="size-3.5 text-emerald-600" />
                  ) : (
                    <Copy className="size-3.5" />
                  )}
                  <span className="hidden sm:inline">
                    {copied ? t("已复制") : t("复制")}
                  </span>
                </button>
                <a
                  href={withDownloadQuery(attachment.url)}
                  download={fileName}
                  className="inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-lg border border-line bg-canvas px-2.5 text-xs font-medium text-foreground transition hover:bg-muted"
                >
                  <Download className="size-3.5" />
                  <span className="hidden sm:inline">{t("下载")}</span>
                </a>
              </>
            )}
            <button
              type="button"
              onClick={onClose}
              aria-label={t("关闭预览")}
              className="grid size-8 cursor-pointer place-items-center rounded-lg text-muted-foreground transition hover:bg-muted hover:text-foreground"
            >
              <X className="size-4" />
            </button>
          </div>
        </div>

        <div className="min-h-[200px] flex-1 overflow-auto bg-canvas/70 p-4">
          {loading ? (
            <div className="flex h-48 items-center justify-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              <span>{t("正在加载文件...")}</span>
            </div>
          ) : expired ? (
            <div className="flex h-48 flex-col items-center justify-center gap-2 text-muted-foreground">
              <FileX2 className="size-6 opacity-70" />
              <span className="text-sm font-medium">{t("文件已过期")}</span>
            </div>
          ) : errorMsg ? (
            <div className="flex h-48 items-center justify-center text-xs text-danger">
              {errorMsg}
            </div>
          ) : (
            <pre
              data-testid="text-attachment-preview-content"
              className="m-0 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap text-foreground"
            >
              {content}
            </pre>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}

/** 复用的应用内图片灯箱组件：支持 Escape 关闭、外链 referrerPolicy="no-referrer" 与可选原图打开入口。 */
export function ImageLightboxModal({
  src,
  alt,
  openOriginalUrl,
  onClose,
}: {
  src: string;
  alt: string;
  openOriginalUrl?: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  if (typeof document === "undefined") return null;

  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label={t("图片大图预览")}
      data-testid="attachment-lightbox"
      onClick={onClose}
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/80 p-4 backdrop-blur-xs"
    >
      <div
        className="absolute top-4 right-4 flex items-center gap-2"
        onClick={(e) => e.stopPropagation()}
      >
        {openOriginalUrl && (
          <a
            href={openOriginalUrl}
            target="_blank"
            rel="noopener noreferrer"
            aria-label={t("打开原图")}
            title={t("打开原图")}
            data-testid="lightbox-open-original"
            className="inline-flex h-9 items-center gap-1.5 rounded-full bg-white/15 px-3 text-xs font-medium text-white transition hover:bg-white/25"
          >
            <ExternalLink className="size-3.5" />
            <span>{t("打开原图")}</span>
          </a>
        )}
        <button
          type="button"
          onClick={onClose}
          aria-label={t("关闭预览")}
          className="grid size-9 cursor-pointer place-items-center rounded-full bg-white/15 text-white transition hover:bg-white/25"
        >
          <X className="size-5" />
        </button>
      </div>
      <img
        src={src}
        alt={alt}
        referrerPolicy="no-referrer"
        onClick={(e) => e.stopPropagation()}
        className="max-h-[88vh] max-w-[92vw] rounded-xl object-contain shadow-2xl"
      />
    </div>,
    document.body,
  );
}

/** 单张外链消息图片卡片：支持加载失败回退到可点击的外链提示，点击正常图片打开应用内灯箱。 */
function MessageExternalImageCard({
  src,
  alt,
  single,
  onPreview,
}: {
  src: string;
  alt: string;
  single: boolean;
  onPreview: (src: string) => void;
}) {
  const { t } = useTranslation();
  const [failed, setFailed] = useState(false);

  if (failed) {
    return (
      <div
        data-testid="message-image-fallback"
        className={`flex flex-col items-start justify-center gap-1.5 rounded-xl border border-dashed border-border/80 bg-muted/40 px-3 py-3 text-xs text-muted-foreground ${
          single ? "min-w-[220px] max-w-sm" : "min-h-28"
        }`}
      >
        <div className="flex items-center gap-1.5 font-medium text-foreground/80">
          <ImageOff className="size-4 shrink-0 opacity-75" />
          <span>{t("图片加载失败")}</span>
        </div>
        <a
          href={src}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex max-w-full items-center gap-1 break-all text-[11px] text-primary underline underline-offset-2 hover:opacity-80"
        >
          <ExternalLink className="size-3 shrink-0" />
          <span className="truncate">{t("打开原图")}</span>
        </a>
      </div>
    );
  }

  return (
    <button
      type="button"
      onClick={() => onPreview(src)}
      data-testid="message-image-item"
      aria-label={t("点击查看大图")}
      className={`group relative overflow-hidden rounded-xl border border-border/60 bg-muted/30 p-0 text-left transition hover:border-primary/40 focus:outline-none focus:ring-2 focus:ring-primary/30 cursor-zoom-in ${
        single ? "block max-w-full" : "block w-full"
      }`}
    >
      <img
        src={src}
        alt={alt}
        loading="lazy"
        referrerPolicy="no-referrer"
        onError={() => setFailed(true)}
        className={
          single
            ? "block max-h-96 max-w-full w-auto rounded-xl object-contain"
            : "h-40 w-full object-cover sm:h-48"
        }
      />
      <span className="pointer-events-none absolute right-2 bottom-2 grid size-7 place-items-center rounded-full bg-black/50 text-white opacity-0 transition group-hover:opacity-100">
        <ZoomIn className="size-3.5" />
      </span>
    </button>
  );
}

/** Agent 通过 reply_message.images 发送的外链图片展示区：单图自适应、多图网格，支持灯箱预览与失败回退。 */
export function MessageImageGallery({
  images,
  alt,
  hasContentAbove,
}: {
  images: string[];
  alt?: string;
  hasContentAbove: boolean;
}) {
  const { t } = useTranslation();
  const [lightboxSrc, setLightboxSrc] = useState<string | null>(null);

  if (!images || images.length === 0) return null;

  const isSingle = images.length === 1;
  const gridCols = isSingle
    ? "grid-cols-1"
    : images.length === 2
      ? "grid-cols-2"
      : "grid-cols-2 sm:grid-cols-3";
  const imageAlt = alt?.trim() || t("消息图片");

  return (
    <>
      <div
        data-quote-ignore="true"
        data-testid="message-image-gallery"
        data-image-count={images.length}
        data-has-above={hasContentAbove ? "true" : "false"}
        className={`${hasContentAbove ? "mt-3" : ""} ${
          isSingle ? "flex" : `grid gap-2 ${gridCols}`
        }`}
      >
        {images.map((src, index) => (
          <MessageExternalImageCard
            key={`${src}-${index}`}
            src={src}
            alt={imageAlt}
            single={isSingle}
            onPreview={setLightboxSrc}
          />
        ))}
      </div>

      {lightboxSrc && (
        <ImageLightboxModal
          src={lightboxSrc}
          alt={alt?.trim() || t("消息图片大图")}
          openOriginalUrl={lightboxSrc}
          onClose={() => setLightboxSrc(null)}
        />
      )}
    </>
  );
}

/** 聊天气泡与待办详情内的附件展示区：图片网格+大图灯箱、纯文本/代码文件卡片+在线预览与下载。 */
export function MessageAttachmentGrid({
  attachments,
}: {
  attachments: AttachmentMeta[];
}) {
  const { t } = useTranslation();
  const [lightbox, setLightbox] = useState<AttachmentMeta | null>(null);
  const [previewText, setPreviewText] = useState<AttachmentMeta | null>(null);
  const [runtimeExpiredIDs, setRuntimeExpiredIDs] = useState<
    Record<string, boolean>
  >({});

  if (!attachments || attachments.length === 0) return null;

  const imageAttachments = attachments.filter(
    (att) => !isTextAttachmentMeta(att),
  );
  const textAttachments = attachments.filter((att) =>
    isTextAttachmentMeta(att),
  );

  const gridCols =
    imageAttachments.length === 1
      ? "grid-cols-1"
      : imageAttachments.length === 2
        ? "grid-cols-2"
        : "grid-cols-2 sm:grid-cols-3";

  return (
    <>
      {imageAttachments.length > 0 && (
        <div
          className={`mt-2 grid gap-2 ${gridCols}`}
          data-testid="message-attachment-grid"
        >
          {imageAttachments.map((att) => (
            <AttachmentImageCard
              key={att.id}
              attachment={att}
              onPreview={setLightbox}
            />
          ))}
        </div>
      )}

      {textAttachments.length > 0 && (
        <div
          className="mt-2 flex flex-wrap gap-2"
          data-testid="message-text-attachment-list"
        >
          {textAttachments.map((att) => (
            <AttachmentTextCard
              key={att.id}
              attachment={att}
              expired={Boolean(runtimeExpiredIDs[att.id])}
              onPreview={setPreviewText}
            />
          ))}
        </div>
      )}

      {lightbox && (
        <ImageLightboxModal
          src={lightbox.url}
          alt={t("消息图片大图")}
          openOriginalUrl={lightbox.url}
          onClose={() => setLightbox(null)}
        />
      )}

      {previewText && (
        <TextAttachmentPreviewModal
          attachment={previewText}
          onExpired={(id) =>
            setRuntimeExpiredIDs((prev) => ({ ...prev, [id]: true }))
          }
          onClose={() => setPreviewText(null)}
        />
      )}
    </>
  );
}
