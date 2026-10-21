import { useState } from "react";
import { Check, Copy } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useTranslation } from "react-i18next";

export function RichText({ content }: { content: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  return (
    <div className="prose-chat text-sm text-slate-700">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
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
                {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
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
