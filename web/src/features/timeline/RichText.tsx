import { useState } from "react";
import { Check, Copy } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useTranslation } from "react-i18next";

export function RichText({
  content,
  className = "text-[15px] leading-relaxed",
}: {
  content: string;
  className?: string;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  return (
    <div className={`prose-chat text-foreground ${className}`}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          blockquote: ({ children }) => (
            <div className="my-2 flex overflow-hidden rounded-lg border-l-3 border-primary bg-surface/80 dark:bg-surface/50 px-3 py-1.5 text-xs text-muted-foreground shadow-2xs">
              <div className="min-w-0 flex-1 leading-relaxed">{children}</div>
            </div>
          ),
          pre: ({ children }) => (
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
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
