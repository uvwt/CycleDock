import { useMemo, useState } from "react";
import { MessageSquareText, Plus, Search } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Card, Project } from "../../shared/api/types";

export function DiscussionSidebar({
  projects,
  projectID,
  discussions,
  activeCardID,
  creating,
  onProjectChange,
  onSelect,
  onCreate,
  className = "",
}: {
  projects: Project[];
  projectID: string;
  discussions: Card[];
  activeCardID?: string;
  creating?: boolean;
  onProjectChange: (projectID: string) => void;
  onSelect: (card: Card) => void;
  onCreate: () => void;
  className?: string;
}) {
  const { t, i18n } = useTranslation();
  const [term, setTerm] = useState("");
  const filtered = useMemo(() => {
    const query = term.trim().toLowerCase();
    return discussions
      .filter((card) => {
        if (!query) return true;
        return (card.title + " " + card.description).toLowerCase().includes(query);
      })
      .sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  }, [discussions, term]);

  return (
    <aside
      className={`flex h-full min-h-0 w-full flex-col bg-slate-50/80 ${className}`}
      aria-label={t("讨论列表")}
    >
      <div className="space-y-3 border-b border-slate-200 p-3">
        <div className="flex items-center gap-2">
          <select
            aria-label={t("项目")}
            value={projectID}
            onChange={(event) => onProjectChange(event.target.value)}
            className="min-w-0 flex-1 rounded-xl border border-slate-200 bg-white px-3 py-2 text-sm outline-none focus:border-sky-500"
          >
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
          <button
            type="button"
            title={t("新建讨论")}
            aria-label={t("新建讨论")}
            disabled={!projectID || creating}
            onClick={onCreate}
            className="grid size-9 shrink-0 place-items-center rounded-xl border border-slate-200 bg-white text-slate-600 transition hover:bg-slate-100 disabled:opacity-40"
          >
            <Plus className="size-4" />
          </button>
        </div>
        <label className="relative block">
          <span className="sr-only">{t("搜索讨论…")}</span>
          <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-slate-400" />
          <input
            value={term}
            onChange={(event) => setTerm(event.target.value)}
            placeholder={t("搜索讨论…")}
            className="w-full rounded-xl border border-slate-200 bg-white py-2 pr-3 pl-9 text-sm outline-none focus:border-sky-500"
          />
        </label>
      </div>

      <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto p-2">
        {filtered.length ? (
          <div className="space-y-1">
            {filtered.map((card) => {
              const active = card.id === activeCardID;
              return (
                <button
                  key={card.id}
                  type="button"
                  onClick={() => onSelect(card)}
                  className={`w-full rounded-xl px-3 py-3 text-left transition ${active ? "bg-white shadow-sm ring-1 ring-slate-200" : "hover:bg-white/80"}`}
                >
                  <div className="flex items-start gap-2.5">
                    <MessageSquareText
                      className={`mt-0.5 size-4 shrink-0 ${active ? "text-sky-700" : "text-slate-400"}`}
                    />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium text-slate-800">
                        {card.title || t("新讨论")}
                      </p>
                      <p className="mt-1 line-clamp-2 text-xs leading-5 text-slate-500">
                        {card.description || t("继续对话，探索想法…")}
                      </p>
                      <p className="mt-1.5 text-[11px] text-slate-400">
                        {new Date(card.updated_at).toLocaleDateString(
                          i18n.language === "en" ? "en-US" : "zh-CN",
                        )}
                        {card.discussion_state === "archived"
                          ? ` · ${t("已归档")}`
                          : ""}
                      </p>
                    </div>
                  </div>
                </button>
              );
            })}
          </div>
        ) : (
          <div className="px-4 py-10 text-center text-sm text-slate-400">
            {term ? t("没有匹配的讨论") : t("还没有讨论")}
          </div>
        )}
      </div>
    </aside>
  );
}
