import { useMemo, useState } from "react";
import { Plus, Search } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { CardActions } from "../cards/CardActions";
import type { Card } from "../../shared/api/types";
import {
  ContextSidebar,
  ContextSidebarItem,
} from "../../shared/ui/ContextSidebar";

export function DiscussionSidebar({
  discussions,
  activeCardID,
  onSelect,
  onCreate,
  className = "",
}: {
  discussions: Card[];
  activeCardID?: string;
  onSelect: (card: Card) => void;
  onCreate: () => void;
  className?: string;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [term, setTerm] = useState("");
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
    <ContextSidebar
      ariaLabel={t("讨论列表")}
      className={className}
      header={
        <>
          <div className="flex items-center justify-between gap-3 px-1">
            <span className="text-xs font-semibold text-slate-500">
              {t("讨论")}
            </span>
            <button
              type="button"
              title={t("新建讨论")}
              aria-label={t("新建讨论")}
              onClick={onCreate}
              className="grid size-8 shrink-0 place-items-center rounded-lg text-slate-500 transition hover:bg-white hover:text-slate-900 disabled:opacity-40"
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
              className="w-full rounded-xl border border-slate-200 bg-white py-2 pr-3 pl-9 text-sm outline-none focus:border-slate-400"
            />
          </label>
        </>
      }
    >
      {filtered.length ? (
        <div className="space-y-1">
          {filtered.map((card) => (
            <ContextSidebarItem
              key={card.id}
              active={card.id === activeCardID}
              onClick={() => onSelect(card)}
              title={card.title || t("新讨论")}
              actions={
                <CardActions
                  card={card}
                  menuSide="right"
                  onDeleted={() => {
                    if (card.id === activeCardID) {
                      navigate(
                        `/chat?project=${encodeURIComponent(card.project_id)}`,
                      );
                    }
                  }}
                />
              }
            />
          ))}
        </div>
      ) : (
        <div className="px-4 py-10 text-center text-sm text-slate-400">
          {term ? t("没有匹配的讨论") : t("还没有讨论")}
        </div>
      )}
    </ContextSidebar>
  );
}
