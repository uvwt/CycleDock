import { CalendarClock } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Card } from "../../shared/api/types";
import {
  ContextSidebar,
  ContextSidebarItem,
} from "../../shared/ui/ContextSidebar";
import { stripMarkdownSummary } from "../../shared/lib/markdown";
import { OngoingHealthBadge } from "../../shared/ui/OngoingHealthBadge";

export function PlanSidebar({
  cards,
  activeCardID,
  onSelect,
}: {
  cards: Card[];
  activeCardID: string;
  onSelect: (card: Card) => void;
}) {
  const { t } = useTranslation();
  const plans = cards
    .filter((card) => card.kind === "ongoing")
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at));

  return (
    <ContextSidebar
      ariaLabel={t("计划列表")}
      header={
        <div className="px-1 text-xs font-medium text-muted-foreground">
          {t("计划")}
        </div>
      }
    >
      {plans.length ? (
        <div className="space-y-1">
          {plans.map((card) => {
            const summary = stripMarkdownSummary(card.description);
            const showHealth =
              card.health === "healthy" ||
              card.health === "warning" ||
              card.health === "critical";
            return (
              <ContextSidebarItem
                key={card.id}
                active={card.id === activeCardID}
                onClick={() => onSelect(card)}
                icon={<CalendarClock className="size-4 text-faint" />}
                title={card.title || t("未命名计划")}
                description={summary || undefined}
                meta={
                  showHealth ? (
                    <OngoingHealthBadge health={card.health} size="xs" />
                  ) : undefined
                }
              />
            );
          })}
        </div>
      ) : (
        <div className="mx-1 rounded-xl border border-dashed border-line/60 bg-surface/40 px-4 py-8 text-center">
          <CalendarClock className="mx-auto mb-2 size-5 text-faint/70" />
          <p className="text-xs text-faint">{t("这个项目还没有计划")}</p>
        </div>
      )}
    </ContextSidebar>
  );
}
