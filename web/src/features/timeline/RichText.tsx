import {
  memo,
  useCallback,
  useMemo,
  useState,
  type AnchorHTMLAttributes,
  type DetailedHTMLProps,
  type MouseEvent,
} from "react";
import { Check, Copy } from "lucide-react";
import ReactMarkdown, { defaultUrlTransform } from "react-markdown";
import remarkGfm from "remark-gfm";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { useProject } from "../../app/project-context";
import { ChatActionCard } from "../../shared/ui/chat";

/**
 * 允许应用专属协议 cycledock:// 与 card:，避免被 ReactMarkdown 默认安全协议白名单过滤为空字符串
 */
function customUrlTransform(url: string): string {
  if (url.startsWith("cycledock://") || url.startsWith("card:")) {
    return url;
  }
  return defaultUrlTransform(url);
}

function parseCardLink(href?: string): { cardId: string; projectId?: string } | null {
  if (!href) return null;

  let path = href.trim();
  // 兼容同源完整 URL 提取 pathname
  if (path.startsWith("http://") || path.startsWith("https://")) {
    try {
      const url = new URL(path);
      path = url.pathname;
    } catch {
      // 忽略无法解析的 URL，保持原 path
    }
  }

  // Match cycledock://card/:cardId or cycledock://card/:projectId/:cardId
  if (path.startsWith("cycledock://card/")) {
    const parts = path.replace("cycledock://card/", "").split("/").filter(Boolean);
    if (parts.length === 1) {
      return { cardId: parts[0] };
    }
    if (parts.length >= 2) {
      return { projectId: parts[0], cardId: parts[1] };
    }
  }

  // Match [card:UUID] or card:UUID
  if (path.startsWith("card:")) {
    const cardId = path.replace("card:", "").trim();
    if (cardId) return { cardId };
  }

  // Match /cards/:projectId/:cardId or /cards/:cardId 以及 /chat/:projectId/:cardId 或 /chat/:cardId
  const match = path.match(/^\/(?:cards|chat)\/([^/?#]+)(?:\/([^/?#]+))?/);
  if (match) {
    if (match[2]) {
      return { projectId: match[1], cardId: match[2] };
    }
    return { cardId: match[1] };
  }

  return null;
}

/**
 * 将正文中裸露的 card:UUID 或 cycledock://card/... 规范化为 Markdown 链接，
 * 避免因缺少 Markdown 括号而无法触发 parseCardLink 拦截渲染。
 */
function normalizeCardMentions(text: string): string {
  if (!text) return "";

  // 1. 将未被 Markdown 链接包裹的 cycledock://card/... 转为 [查看卡片详情](cycledock://card/...)
  let result = text.replace(
    /(^|[^(\]="'])(cycledock:\/\/card\/[a-zA-Z0-9_-]+(?:\/[a-zA-Z0-9_-]+)?)/g,
    "$1[查看卡片详情]($2)",
  );

  // 2. 将未被 Markdown 链接包裹的 card:UUID 转为 [查看卡片详情](card:UUID)
  result = result.replace(
    /(^|[^(\]="'])(card:([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}))/g,
    "$1[查看卡片详情]($2)",
  );

  return result;
}

export const RichText = memo(function RichText({
  content,
  className = "text-[15px] leading-relaxed",
}: {
  content: string;
  className?: string;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { projectID } = useProject();
  const [copied, setCopied] = useState(false);

  const handleLinkClick = useCallback(
    (e: MouseEvent<HTMLAnchorElement>, href: string | undefined) => {
      if (!href) return;

      // 优先拦截卡片协议点击跳转，确保普通 a 标签降级时也能成功跳转
      const cardRef = parseCardLink(href);
      if (cardRef) {
        e.preventDefault();
        const targetProj = cardRef.projectId || projectID;
        const targetUrl = targetProj
          ? `/cards/${encodeURIComponent(targetProj)}/${encodeURIComponent(cardRef.cardId)}`
          : `/cards/${encodeURIComponent(cardRef.cardId)}`;
        navigate(targetUrl);
        return;
      }

      // 检查是否为应用内部相对路径或同源完整 URL
      let isInternal = false;
      let targetPath = href;

      if (href.startsWith("/")) {
        isInternal = true;
        targetPath = href;
      } else if (
        href.startsWith(window.location.origin) ||
        (!href.startsWith("http://") &&
          !href.startsWith("https://") &&
          !href.startsWith("mailto:") &&
          !href.startsWith("tel:") &&
          !href.startsWith("#"))
      ) {
        try {
          const url = new URL(href, window.location.origin);
          if (url.origin === window.location.origin) {
            isInternal = true;
            targetPath = url.pathname + url.search + url.hash;
          }
        } catch {
          // 忽略解析异常
        }
      }

      if (isInternal) {
        e.preventDefault();

        // 智能补齐 projectID：如果形如 /cards/:cardID 或 /chat/:cardID（只有两段）
        const cardMatch = targetPath.match(/^\/(cards|chat)\/([^/?#]+)(.*)$/);
        if (cardMatch && projectID) {
          const [, section, cardID, rest] = cardMatch;
          // 如果第二段不是当前 projectID 且后续没有更多段，说明缺失 projectID
          const segments = targetPath.split("?")[0].split("#")[0].split("/").filter(Boolean);
          if (segments.length === 2) {
            const resolvedUrl = `/${section}/${encodeURIComponent(projectID)}/${encodeURIComponent(cardID)}${rest}`;
            navigate(resolvedUrl);
            return;
          }
        }

        navigate(targetPath);
      }
    },
    [navigate, projectID],
  );

  const normalizedContent = useMemo(
    () => normalizeCardMentions(content),
    [content],
  );

  const markdownComponents = useMemo(
    () => ({
      a: ({
        href,
        children,
        ...props
      }: DetailedHTMLProps<
        AnchorHTMLAttributes<HTMLAnchorElement>,
        HTMLAnchorElement
      >) => {
        const cardRef = parseCardLink(href);
        if (cardRef) {
          const fallbackTitle =
            typeof children === "string" ? children : undefined;
          return (
            <ChatActionCard
              cardId={cardRef.cardId}
              projectId={cardRef.projectId || projectID}
              fallbackTitle={fallbackTitle}
            />
          );
        }

        const isExternal =
          Boolean(href) &&
          (href!.startsWith("http://") || href!.startsWith("https://")) &&
          !href!.startsWith(window.location.origin);

        return (
          <a
            href={href}
            onClick={(e) => handleLinkClick(e, href)}
            target={isExternal ? "_blank" : undefined}
            rel={isExternal ? "noopener noreferrer" : undefined}
            className="font-medium text-primary underline underline-offset-4 decoration-primary/40 hover:decoration-primary transition-colors cursor-pointer"
            {...props}
          >
            {children}
          </a>
        );
      },
      blockquote: ({ children }: { children?: React.ReactNode }) => (
        <div className="my-2 flex overflow-hidden rounded-lg border-l-3 border-primary bg-surface/80 dark:bg-surface/50 px-3 py-1.5 text-xs text-muted-foreground shadow-2xs">
          <div className="min-w-0 flex-1 leading-relaxed">{children}</div>
        </div>
      ),
      pre: ({ children }: { children?: React.ReactNode }) => (
        <div className="relative">
          <button
            title={t("复制代码")}
            aria-label={t("复制代码")}
            onClick={async (event) => {
              const code =
                event.currentTarget.parentElement?.querySelector("code")
                  ?.textContent || "";
              await navigator.clipboard.writeText(code);
              setCopied(true);
              window.setTimeout(() => setCopied(false), 1400);
            }}
            className="absolute top-2 right-2 z-10 rounded-lg bg-slate-700/80 p-1.5 text-white hover:bg-slate-600"
          >
            {copied ? (
              <Check className="size-3.5" />
            ) : (
              <Copy className="size-3.5" />
            )}
          </button>
          <pre>{children}</pre>
        </div>
      ),
    }),
    [handleLinkClick, projectID, t, copied],
  );

  return (
    <div className={`prose-chat text-foreground ${className}`}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        urlTransform={customUrlTransform}
        components={markdownComponents}
      >
        {normalizedContent}
      </ReactMarkdown>
    </div>
  );
});

