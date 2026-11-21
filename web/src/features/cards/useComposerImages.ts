import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import type { AttachmentMeta, Identity } from "../../shared/api/types";
import { keys } from "../../app/query";
import { safeRandomUUID } from "../../shared/lib/utils";

export const MAX_COMPOSER_IMAGES = 8;
const MAX_IMAGE_UPLOAD_BYTES = 20 * 1024 * 1024;
export const MAX_TEXT_UPLOAD_BYTES = 2 * 1024 * 1024;

const ALLOWED_IMAGE_MIME_TYPES = new Set([
  "image/jpeg",
  "image/png",
  "image/webp",
  "image/gif",
]);

const ALLOWED_TEXT_EXTENSIONS = new Set([
  ".txt",
  ".log",
  ".md",
  ".markdown",
  ".json",
  ".jsonl",
  ".ndjson",
  ".yaml",
  ".yml",
  ".toml",
  ".ini",
  ".cfg",
  ".conf",
  ".csv",
  ".tsv",
  ".xml",
  ".sql",
  ".sh",
  ".bash",
  ".zsh",
  ".ps1",
  ".bat",
  ".cmd",
  ".go",
  ".mod",
  ".sum",
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
  ".diff",
  ".patch",
  ".dockerfile",
  ".makefile",
  ".gitignore",
  ".editorconfig",
  ".properties",
  ".env.example",
  ".env.sample",
  ".env.template",
]);

const ALLOWED_SPECIAL_TEXT_NAMES = new Set([
  "dockerfile",
  "makefile",
  "gnumakefile",
  "readme",
  "license",
  "changelog",
  "procfile",
  "jenkinsfile",
  "vagrantfile",
  ".gitignore",
  ".gitattributes",
  ".dockerignore",
  ".editorconfig",
  ".npmrc",
  ".nvmrc",
  ".env.example",
  ".env.sample",
  ".env.template",
]);

export const COMPOSER_FILE_ACCEPT = [
  "image/png",
  "image/jpeg",
  "image/webp",
  "image/gif",
  ".txt",
  ".log",
  ".md",
  ".json",
  ".yaml",
  ".yml",
  ".toml",
  ".ini",
  ".conf",
  ".csv",
  ".tsv",
  ".xml",
  ".sql",
  ".sh",
  ".ps1",
  ".go",
  ".ts",
  ".tsx",
  ".js",
  ".jsx",
  ".py",
  ".rs",
  ".java",
  ".c",
  ".cpp",
  ".h",
  ".css",
  ".html",
  ".diff",
  ".patch",
].join(",");

const CLIENT_COMPRESS_MAX_DIM = 1600;
const CLIENT_COMPRESS_QUALITY = 0.82;

export type ComposerImageItem = {
  localID: string;
  kind: "image" | "text";
  previewURL: string;
  name: string;
  sizeBytes: number;
  status: "uploading" | "ready" | "error";
  error?: string;
  attachment?: AttachmentMeta;
};

export function isTextAttachmentMeta(att: AttachmentMeta): boolean {
  if (att.kind === "text") return true;
  if (att.kind === "image") return false;
  return Boolean(att.mime_type && !att.mime_type.toLowerCase().startsWith("image/"));
}

export function isImageFile(file: File): boolean {
  const mime = (file.type || "").toLowerCase();
  if (ALLOWED_IMAGE_MIME_TYPES.has(mime)) return true;
  const lower = (file.name || "").toLowerCase();
  return (
    lower.endsWith(".png") ||
    lower.endsWith(".jpg") ||
    lower.endsWith(".jpeg") ||
    lower.endsWith(".webp") ||
    lower.endsWith(".gif")
  );
}

export function isAllowedTextFile(file: File): boolean {
  const name = (file.name || "").trim().toLowerCase();
  if (!name) return false;
  if (ALLOWED_SPECIAL_TEXT_NAMES.has(name)) return true;
  if (
    name.endsWith(".env.example") ||
    name.endsWith(".env.sample") ||
    name.endsWith(".env.template")
  ) {
    return true;
  }
  const dotIdx = name.lastIndexOf(".");
  if (dotIdx <= 0) return false;
  const ext = name.slice(dotIdx);
  return ALLOWED_TEXT_EXTENSIONS.has(ext);
}

// 前端预压缩：大尺寸非 GIF 静态图在上传前通过 Canvas 缩放压缩，显著降低上传带宽与等待时间；
// 后端仍会兜底做魔数校验与服务端压缩，避免信任客户端。
async function prepareImageForUpload(file: File): Promise<File> {
  if (file.type === "image/gif" || file.size < 350 * 1024) {
    return file;
  }
  if (typeof createImageBitmap !== "function" || typeof document === "undefined") {
    return file;
  }
  let bitmap: ImageBitmap | null = null;
  try {
    bitmap = await createImageBitmap(file);
    const { width, height } = bitmap;
    const maxEdge = Math.max(width, height);
    const scale =
      maxEdge > CLIENT_COMPRESS_MAX_DIM ? CLIENT_COMPRESS_MAX_DIM / maxEdge : 1;
    const targetW = Math.max(1, Math.round(width * scale));
    const targetH = Math.max(1, Math.round(height * scale));
    const canvas = document.createElement("canvas");
    canvas.width = targetW;
    canvas.height = targetH;
    const ctx = canvas.getContext("2d");
    if (!ctx) return file;
    ctx.drawImage(bitmap, 0, 0, targetW, targetH);

    // WebP 支持透明度且体积通常比 JPEG/PNG 更小；不支持时降级为 PNG/JPEG。
    const preferredType = "image/webp";
    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob(resolve, preferredType, CLIENT_COMPRESS_QUALITY),
    );
    if (!blob || blob.size >= file.size) {
      return file;
    }
    const baseName = (file.name || "image").replace(/\.[^.]+$/, "");
    const ext = blob.type === "image/webp" ? ".webp" : ".jpg";
    return new File([blob], baseName + ext, { type: blob.type });
  } catch {
    return file;
  } finally {
    bitmap?.close();
  }
}

export function useComposerImages({
  cardID,
  disabled = false,
}: {
  cardID?: string;
  disabled?: boolean;
}) {
  const queryClient = useQueryClient();
  const [items, setItems] = useState<ComposerImageItem[]>([]);
  const itemsRef = useRef<ComposerImageItem[]>(items);

  useEffect(() => {
    itemsRef.current = items;
  }, [items]);

  useEffect(() => {
    return () => {
      for (const item of itemsRef.current) {
        if (item.previewURL.startsWith("blob:")) {
          URL.revokeObjectURL(item.previewURL);
        }
      }
    };
  }, []);

  const addFiles = useCallback(
    async (fileList: FileList | File[]) => {
      if (disabled) return;
      const classified: { file: File; kind: "image" | "text" }[] = [];
      for (const file of Array.from(fileList)) {
        if (isImageFile(file)) {
          if (file.size > MAX_IMAGE_UPLOAD_BYTES) {
            toast.error(`图片过大（超过 20 MB）：${file.name || "未命名图片"}`);
            continue;
          }
          classified.push({ file, kind: "image" });
          continue;
        }
        if (isAllowedTextFile(file)) {
          if (file.size === 0) {
            toast.error(`文件为空，无法上传：${file.name}`);
            continue;
          }
          if (file.size > MAX_TEXT_UPLOAD_BYTES) {
            toast.error(`文本文件过大（超过 2 MB）：${file.name}`);
            continue;
          }
          classified.push({ file, kind: "text" });
          continue;
        }
        toast.error(
          `不支持的文件格式：${file.name || file.type}（仅支持图片与常见纯文本/日志/代码文件，不支持 PDF 或二进制文件）`,
        );
      }
      if (classified.length === 0) return;

      const remaining = MAX_COMPOSER_IMAGES - itemsRef.current.length;
      if (remaining <= 0) {
        toast.error(`单条消息最多附带 ${MAX_COMPOSER_IMAGES} 个附件`);
        return;
      }
      const accepted = classified.slice(0, remaining);
      if (accepted.length < classified.length) {
        toast.warning(`单条消息最多 ${MAX_COMPOSER_IMAGES} 个附件，已忽略多余文件`);
      }

      let workspaceID = queryClient.getQueryData<Identity>(keys.me)?.workspace?.id;
      if (!workspaceID) {
        try {
          const me = await queryClient.fetchQuery({
            queryKey: keys.me,
            queryFn: api.me,
          });
          workspaceID = me.workspace.id;
        } catch {
          toast.error("无法获取当前工作区信息，请刷新后重试");
          return;
        }
      }

      const pendingItems = accepted.map(({ file, kind }) => ({
        localID: safeRandomUUID(),
        kind,
        file,
        previewURL: kind === "image" ? URL.createObjectURL(file) : "",
      }));

      setItems((prev) => [
        ...prev,
        ...pendingItems.map((entry) => ({
          localID: entry.localID,
          kind: entry.kind,
          previewURL: entry.previewURL,
          name: entry.file.name || (entry.kind === "image" ? "image" : "file.txt"),
          sizeBytes: entry.file.size,
          status: "uploading" as const,
        })),
      ]);

      await Promise.all(
        pendingItems.map(async (entry) => {
          try {
            const prepared =
              entry.kind === "image"
                ? await prepareImageForUpload(entry.file)
                : entry.file;
            const meta = await api.uploadAttachment(workspaceID!, prepared, cardID);
            setItems((prev) =>
              prev.map((item) =>
                item.localID === entry.localID
                  ? {
                      ...item,
                      status: "ready",
                      attachment: meta,
                      name: meta.file_name || item.name,
                      sizeBytes: meta.size_bytes || item.sizeBytes,
                    }
                  : item,
              ),
            );
          } catch (err) {
            const msg = err instanceof Error ? err.message : "附件上传失败";
            toast.error(msg);
            setItems((prev) =>
              prev.map((item) =>
                item.localID === entry.localID
                  ? { ...item, status: "error", error: msg }
                  : item,
              ),
            );
          }
        }),
      );
    },
    [cardID, disabled, queryClient],
  );

  const removeItem = useCallback((localID: string) => {
    setItems((prev) => {
      const target = prev.find((item) => item.localID === localID);
      if (target?.previewURL.startsWith("blob:")) {
        URL.revokeObjectURL(target.previewURL);
      }
      return prev.filter((item) => item.localID !== localID);
    });
  }, []);

  const clear = useCallback(() => {
    setItems((prev) => {
      for (const item of prev) {
        if (item.previewURL.startsWith("blob:")) {
          URL.revokeObjectURL(item.previewURL);
        }
      }
      return [];
    });
  }, []);

  const readyAttachments: AttachmentMeta[] = items
    .filter((item): item is ComposerImageItem & { attachment: AttachmentMeta } =>
      item.status === "ready" && Boolean(item.attachment),
    )
    .map((item) => item.attachment);

  const uploading = items.some((item) => item.status === "uploading");

  return {
    items,
    readyAttachments,
    uploading,
    addFiles,
    removeItem,
    clear,
  };
}
