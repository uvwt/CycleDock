import { ArrowUpRight, Bot, Link2 } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import type { Card, CardDetail } from "../../shared/api/types";
import {
  cardStateLabel,
  runStateLabel,
} from "../../shared/presentation/status";

export function WorkSummary({
  detail,
  projectCards,
}: {
  detail: CardDetail;
  projectCards: Card[];
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const card = detail.card;
  const source = card.source_discussion_id
    ? projectCards.find((item) => item.id === card.source_discussion_id)
    : undefined;
  const derived =
    card.kind === "discussion"
      ? projectCards.filter((item) => item.source_discussion_id === card.id)
      : [];
  const latestResult = [...detail.timeline]
    .reverse()
    .find((event) => event.type === "result");
  const run = detail.current_run;

  function open(item: Card) {
    navigate(
      (item.kind === "discussion" ? "/chat/" : "/cards/") +
        item.project_id +
        "/" +
        item.id,
    );
  }

  if (card.kind === "discussion") {
    if (!derived.length) return null;
    return (
      <section className="shrink-0 border-b border-slate-100 bg-white px-4 py-2.5 sm:px-6">
        <div className="mx-auto flex max-w-3xl flex-wrap items-center gap-2">
          <span className="text-xs font-medium text-slate-400">
            {t("派生工作")}
          </span>
          {derived.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => open(item)}
              className="inline-flex max-w-full items-center gap-1.5 rounded-full bg-slate-50 px-2.5 py-1 text-xs text-slate-600 hover:bg-slate-100"
            >
              <span className="max-w-56 truncate">{item.title}</span>
              <span className="text-slate-400">{cardStateLabel(t, item)}</span>
              <ArrowUpRight className="size-3" />
            </button>
          ))}
        </div>
      </section>
    );
  }

  return (
    <section className="shrink-0 border-b border-slate-100 bg-slate-50/50 px-4 py-3 sm:px-6">
      <div className="mx-auto max-w-3xl">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-xs">
          <span className="font-medium text-slate-700">
            {t("状态")} · {cardStateLabel(t, card)}
          </span>
          <span className="inline-flex items-center gap-1 text-slate-500">
            <Bot className="size-3.5" />
            {run ? runStateLabel(t, run.status) : t("Agent 空闲")}
          </span>
          {(run?.current_stage || run?.progress_summary) && (
            <span className="min-w-0 truncate text-slate-500">
              {run.current_stage || run.progress_summary}
            </span>
          )}
          {source && (
            <button
              type="button"
              onClick={() => open(source)}
              className="inline-flex items-center gap-1 text-sky-700 hover:underline"
            >
              <Link2 className="size-3.5" />
              {t("来源讨论")} · {source.title}
            </button>
          )}
          {latestResult && (
            <button
              type="button"
              onClick={() =>
                document
                  .getElementById("event-" + latestResult.id)
                  ?.scrollIntoView({ behavior: "smooth", block: "center" })
              }
              className="inline-flex items-center gap-1 text-sky-700 hover:underline"
            >
              {t("最新结果")}
              <ArrowUpRight className="size-3" />
            </button>
          )}
        </div>
        {card.description && (
          <p className="mt-2 line-clamp-2 text-xs leading-5 text-slate-500">
            {card.description}
          </p>
        )}
      </div>
    </section>
  );
}
