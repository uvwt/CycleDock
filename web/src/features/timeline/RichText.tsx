import {
  Children,
  isValidElement,
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type AnchorHTMLAttributes,
  type ComponentProps,
  type DetailedHTMLProps,
  type ImgHTMLAttributes,
  type MouseEvent,
  type ReactElement,
  type ReactNode,
  type TdHTMLAttributes,
} from "react";
import { Check, Copy, ExternalLink, ImageOff } from "lucide-react";
import ReactMarkdown, { defaultUrlTransform } from "react-markdown";
import rehypeHighlight from "rehype-highlight";
import remarkGfm from "remark-gfm";
import { common } from "lowlight";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import nginx from "highlight.js/lib/languages/nginx";
import powershell from "highlight.js/lib/languages/powershell";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { useProject } from "../../app/project-context";
import { ChatActionCard } from "../../shared/ui/chat";
import { ImageLightboxModal } from "./AttachmentGallery";
import {
  normalizeCardMentions,
  parseCardLink,
} from "./cardLinkFormatting";

// 在 lowlight 常用语言包的基础上补充运维/开发高频语言，避免引入全量数百种冷门语法导致前端包膨胀。
const HIGHLIGHT_LANGUAGES = {
  ...common,
  dockerfile,
  nginx,
  powershell,
};

const HIGHLIGHT_ALIASES = {
  typescript: ["tsx"],
  javascript: ["jsx"],
  dockerfile: ["docker"],
  powershell: ["ps1", "pwsh"],
  ini: ["toml", "dotenv", "env"],
  bash: ["zsh", "sh"],
  plaintext: ["text", "txt"],
};

const REMARK_PLUGINS = [remarkGfm];
const REHYPE_PLUGINS = [
  [
    rehypeHighlight,
    {
      languages: HIGHLIGHT_LANGUAGES,
      aliases: HIGHLIGHT_ALIASES,
      plainText: ["text", "txt", "plaintext"],
      detect: false,
    },
  ],
] as NonNullable<ComponentProps<typeof ReactMarkdown>["rehypePlugins"]>;

/**
 * 从 <pre><code className="hljs language-xxx"> 中提取用户标注的语言名称，用于代码块头部展示。
 */
function extractCodeLanguage(children: ReactNode): string | null {
  if (!isValidElement(children)) return null;
  const props = (children as ReactElement<{ className?: string }>).props;
  const className = typeof props?.className === "string" ? props.className : "";
  const match = className.match(/\blanguage-([a-zA-Z0-9_+-]+)\b/);
  if (!match) return null;
  return match[1].toLowerCase();
}

/**
 * 独立代码块组件：
 * - 每个代码块独立持有 copied 状态与清理定时器，避免同一条消息中多个代码块联动误显示“已复制”
 * - 展示语言标签与固定头部栏，移动端和桌面端均可清晰操作且不遮挡第一行代码
 */
function CodeBlock({ children }: { children?: ReactNode }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const preRef = useRef<HTMLPreElement>(null);
  const timerRef = useRef<number | null>(null);
  const language = extractCodeLanguage(children);

  useEffect(() => {
    return () => {
      if (timerRef.current !== null) {
        window.clearTimeout(timerRef.current);
      }
    };
  }, []);

  const handleCopy = useCallback(async () => {
    const code = preRef.current?.querySelector("code")?.textContent ?? "";
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      if (timerRef.current !== null) {
        window.clearTimeout(timerRef.current);
      }
      timerRef.current = window.setTimeout(() => {
        setCopied(false);
        timerRef.current = null;
      }, 1400);
    } catch {
      // 忽略剪贴板权限拒绝，避免抛出未捕获异常
    }
  }, []);

  return (
    <div className="prose-chat-codeblock group my-3 overflow-hidden rounded-xl border border-slate-700/60 bg-[var(--code-background)]">
      <div className="flex items-center justify-between gap-2 border-b border-slate-700/60 bg-slate-900/45 px-3.5 py-1.5 text-xs text-slate-300">
        <span className="truncate font-mono text-[11px] font-medium tracking-wide text-slate-300/90 select-none">
          {language || "code"}
        </span>
        <button
          type="button"
          title={copied ? t("已复制") : t("复制代码")}
          aria-label={copied ? t("已复制") : t("复制代码")}
          data-copied={copied ? "true" : "false"}
          onClick={handleCopy}
          className="inline-flex shrink-0 items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-medium text-slate-300 transition-colors hover:bg-slate-700/70 hover:text-white"
        >
          {copied ? (
            <>
              <Check className="size-3.5 text-emerald-400" />
              <span className="text-emerald-300">{t("已复制")}</span>
            </>
          ) : (
            <>
              <Copy className="size-3.5" />
              <span>{t("复制代码")}</span>
            </>
          )}
        </button>
      </div>
      <pre ref={preRef} className="scrollbar-thin">
        {children}
      </pre>
    </div>
  );
}

/** Markdown 内嵌兜底图片组件：补充 no-referrer、lazy、合理尺寸圆角，并支持灯箱放大与失败回退。 */
function MarkdownImage({ src, alt }: { src?: string; alt?: string }) {
  const { t } = useTranslation();
  const [failed, setFailed] = useState(false);
  const [lightboxOpen, setLightboxOpen] = useState(false);

  if (!src) return null;
  const label = alt?.trim() || t("消息图片");

  if (failed) {
    return (
      <span
        data-testid="markdown-image-fallback"
        className="my-2 inline-flex max-w-full items-center gap-2 rounded-xl border border-dashed border-border/80 bg-muted/40 px-3 py-2 text-xs text-muted-foreground"
      >
        <ImageOff className="size-3.5 shrink-0 opacity-75" />
        <span>{t("图片加载失败")}</span>
        <a
          href={src}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 text-primary underline underline-offset-2 hover:opacity-80"
        >
          <ExternalLink className="size-3 shrink-0" />
          <span>{t("打开原图")}</span>
        </a>
      </span>
    );
  }

  return (
    <>
      <img
        src={src}
        alt={label}
        loading="lazy"
        referrerPolicy="no-referrer"
        onError={() => setFailed(true)}
        onClick={() => setLightboxOpen(true)}
        className="my-2 block max-h-96 max-w-full cursor-zoom-in rounded-xl border border-border/60 bg-muted/30 object-contain"
      />
      {lightboxOpen && (
        <ImageLightboxModal
          src={src}
          alt={label}
          openOriginalUrl={src}
          onClose={() => setLightboxOpen(false)}
        />
      )}
    </>
  );
}

/**
 * 允许应用专属协议 cycledock:// 与 card:，避免被 ReactMarkdown 默认安全协议白名单过滤为空字符串
 */
function customUrlTransform(url: string): string {
  if (url.startsWith("cycledock://") || url.startsWith("card:")) {
    return url;
  }
  return defaultUrlTransform(url);
}


export const RichText = memo(function RichText({
  content,
  className = "text-[15px] leading-relaxed",
  inline = false,
}: {
  content: string;
  className?: string;
  inline?: boolean;
}) {
  const navigate = useNavigate();
  const { projectID } = useProject();

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
      p: ({ children }: { children?: ReactNode }) => {
        const items = Children.toArray(children).filter(
          (child) => !(typeof child === "string" && child.trim() === ""),
        );
        const hasCardChild = items.some(
          (child) =>
            isValidElement<{ href?: string }>(child) &&
            typeof child.props?.href === "string" &&
            parseCardLink(child.props.href) !== null,
        );
        if (hasCardChild) {
          // ChatActionCard 本身渲染为块级 div，避免落入 <p> 内造成非法嵌套与多余间距
          if (items.length === 1) {
            return <>{children}</>;
          }
          return <div className="my-1.5">{children}</div>;
        }
        if (inline) {
          return <span>{children}</span>;
        }
        return <p>{children}</p>;
      },
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
      img: ({
        src,
        alt,
      }: DetailedHTMLProps<
        ImgHTMLAttributes<HTMLImageElement>,
        HTMLImageElement
      >) => <MarkdownImage src={src} alt={alt} />,
      blockquote: ({ children }: { children?: React.ReactNode }) => (
        <div className="my-2 flex overflow-hidden rounded-lg border-l-3 border-primary bg-surface/80 dark:bg-surface/50 px-3 py-1.5 text-xs text-muted-foreground shadow-2xs">
          <div className="min-w-0 flex-1 leading-relaxed">{children}</div>
        </div>
      ),
      pre: ({ children }: { children?: ReactNode }) => (
        <CodeBlock>{children}</CodeBlock>
      ),
      table: ({ children }: { children?: ReactNode }) => (
        // 独立横向滚动容器：避免外层气泡折行压缩列宽，同时配合移动端手势避让
        <div
          data-chat-table-scroll="true"
          className="prose-chat-table-wrap scrollbar-thin my-2 w-full max-w-full overflow-x-auto rounded-lg border border-border/60"
        >
          <table className="w-max min-w-full border-collapse text-left text-sm">
            {children}
          </table>
        </div>
      ),
      td: ({
        children,
        style,
        colSpan,
        rowSpan,
      }: DetailedHTMLProps<
        TdHTMLAttributes<HTMLTableCellElement>,
        HTMLTableCellElement
      >) => (
        <td style={style} colSpan={colSpan} rowSpan={rowSpan}>
          <div className="prose-chat-table-cell">{children}</div>
        </td>
      ),
    }),
    [handleLinkClick, inline, projectID],
  );

  if (inline) {
    return (
      <span className={`prose-chat inline text-foreground ${className}`}>
        <ReactMarkdown
          remarkPlugins={REMARK_PLUGINS}
          rehypePlugins={REHYPE_PLUGINS}
          urlTransform={customUrlTransform}
          components={markdownComponents}
        >
          {normalizedContent}
        </ReactMarkdown>
      </span>
    );
  }

  return (
    <div className={`prose-chat text-foreground ${className}`}>
      <ReactMarkdown
        remarkPlugins={REMARK_PLUGINS}
        rehypePlugins={REHYPE_PLUGINS}
        urlTransform={customUrlTransform}
        components={markdownComponents}
      >
        {normalizedContent}
      </ReactMarkdown>
    </div>
  );
});

