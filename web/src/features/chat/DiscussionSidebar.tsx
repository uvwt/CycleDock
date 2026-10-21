import { useMemo, useState } from "react";
import {
  MessageSquareText,
  PanelLeftClose,
  Plus,
  Search,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Card } from "../../shared/api/types";

function workState(card: Card) {
  if (card.kind === "todo") return card.todo_state;
  if (card.kind === "ongoing") return card.ongoing_state;
  return undefined;
}

export function DiscussionSidebar({
  discussions,
  allCards,
  activeCardID,
  creating,
  onSelect,
  onCreate,
  onCollapse,
  className = "",
}: {
  discussions: Card[];
  allCards: Card[];
  activeCardID?: string;
  creating?: boolean;
  onSelect: (card: Card) => void;
  onCreate: () => void;
  onCollapse?: () => void;
  className?: string;
}) {
  const { t, i18n } = useTranslation();
  const [term, setTerm] = useState("");
  const relatedByDiscussion = useMemo(() => {
    const groups = new Map<string, Card[]>();
    for (const card of allCards) {
      if (!card.source_discussion_id) continue;
      const existing = groups.get(card.source_discussion_id) || [];
      existing.push(card);
      groups.set(card.source_discussion_id, existing);
    }
    return groups;
  }, [allCards]);
  const filtered = useMemo(() => {
    const query = term.trim().toLowerCase();
    return discussions
      .filter((card) => {
        if (!query) return true;
        return (card.title + " " + card.description)
          .toLowerCase()
          .includes(query);
      })
      .sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  }, [discussions, term]);

  return (
    <aside
      className={`flex h-full min-h-0 w-full flex-col bg-slate-50/80 ${className}`}
      aria-label={t("讨论列表")}
    >
      <div className="flex items-center gap-2 border-b border-slate-200 p-3">
        <label className="relative min-w-0 flex-1">
          <span className="sr-only">{t("搜索讨论…")}</span>
          <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-slate-400" />
          <input
            value={term}
            onChange={(event) => setTerm(event.target.value)}
            placeholder={t("搜索讨论…")}
            className="w-full rounded-lg border border-slate-200 bg-white py-2 pr-9 pl-9 text-[13px] outline-none focus:border-sky-500"
          />
          {term && (
            <button
              type="button"
              title={t("清空搜索")}
              aria-label={t("清空搜索")}
              onClick={() => setTerm("")}
              className="absolute top-1.5 right-1.5 grid size-7 place-items-center rounded-lg text-slate-400 hover:bg-slate-100"
            >
              <X className="size-3.5" />
            </button>
          )}
        </label>
        <button
          type="button"
          title={t("新建讨论")}
          aria-label={t("新建讨论")}
          disabled={creating}
          onClick={onCreate}
          className="grid size-9 shrink-0 place-items-center rounded-lg border border-slate-200 bg-white text-slate-600 transition-colors hover:bg-slate-100 disabled:opacity-40"
        >
          <Plus className="size-4" />
        </button>
        {onCollapse && (
          <button
            type="button"
            title={t("收起讨论列表")}
            aria-label={t("收起讨论列表")}
            onClick={onCollapse}
            className="hidden size-9 shrink-0 place-items-center rounded-lg text-slate-400 hover:bg-white hover:text-slate-700 lg:grid"
          >
            <PanelLeftClose className="size-4" />
          </button>
        )}
      </div>

      <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto p-2">
        {filtered.length ? (
          <div className="space-y-1">
            {filtered.map((card) => {
              const active = card.id === activeCardID;
              const related = relatedByDiscussion.get(card.id) || [];
              const activeWork = related.filter((item) => {
                const state = workState(item);
                return (
                  (item.kind === "todo" && state !== "done") ||
                  (item.kind === "ongoing" && state === "active")
                );
              }).length;
              return (
                <button
                  key={card.id}
                  type="button"
                  onClick={() => onSelect(card)}
                  className={`w-full rounded-lg px-3 py-2.5 text-left transition-colors ${
                    active
                      ? "bg-white shadow-sm ring-1 ring-slate-200"
                      : "hover:bg-white/80"
                  }`}
                >
                  <div className="flex items-start gap-2.5">
                    <MessageSquareText
                      className={`mt-0.5 size-4 shrink-0 ${
                        active ? "text-sky-700" : "text-slate-400"
                      }`}
                    />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-[13px] font-medium text-slate-800">
                        {card.title || t("新讨论")}
                      </p>
                      <p className="mt-1 line-clamp-2 text-xs leading-5 text-slate-500">
                        {card.description || t("继续对话，探索想法…")}
                      </p>
                      <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-slate-400">
                        <span>
                          {new Date(card.updated_at).toLocaleDateString(
                            i18n.language === "en" ? "en-US" : "zh-CN",
                          )}
                        </span>
                        {!!related.length && (
                          <span>
                            {t("关联工作")} {related.length}
                            {activeWork > 0
                              ? ` · ${t("活跃")} ${activeWork}`
                              : ""}
                          </span>
                        )}
                        {card.discussion_state === "archived" && (
                          <span>{t("已归档")}</span>
                        )}
                      </div>
                    </div>
                  </div>
                </button>
              );
            })}
          </div>
        ) : term ? (
          <div className="px-4 py-10 text-center">
            <p className="text-[13px] text-slate-400">{t("没有匹配的讨论")}</p>
            <button
              type="button"
              onClick={() => setTerm("")}
              className="mt-3 text-[13px] font-medium text-sky-700 hover:underline"
            >
              {t("清空搜索")}
            </button>
          </div>
        ) : (
          <div className="px-4 py-10 text-center">
            <p className="text-[13px] text-slate-400">{t("这个项目还没有讨论")}</p>
            <button
              type="button"
              onClick={onCreate}
              disabled={creating}
              className="mt-3 text-[13px] font-medium text-sky-700 hover:underline disabled:opacity-40"
            >
              {t("新建讨论")}
            </button>
          </div>
        )}
      </div>
    </aside>
  );
}
