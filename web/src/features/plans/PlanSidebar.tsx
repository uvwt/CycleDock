import { CalendarClock } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Card } from "../../shared/api/types";
import {
  ContextSidebar,
  ContextSidebarItem,
} from "../../shared/ui/ContextSidebar";

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
      {plans.map((card) => (
        <ContextSidebarItem
          key={card.id}
          active={card.id === activeCardID}
          onClick={() => onSelect(card)}
          icon={<CalendarClock className="size-4 text-faint" />}
          title={card.title || t("未命名计划")}
          description={card.description}
          meta={card.health || card.current_attempt_status || undefined}
        />
      ))}
    </ContextSidebar>
  );
}
