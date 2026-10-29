import { useMemo, useState } from "react";
import {
  Activity,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Circle,
  ClipboardCheck,
  PlayCircle,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Card, TodoState } from "../../shared/api/types";
import {
  ContextSidebar,
  ContextSidebarItem,
} from "../../shared/ui/ContextSidebar";

type WorkGroup = TodoState | "ongoing";

const todoGroups: TodoState[] = ["todo", "working", "review", "done"];

function groupForCard(card?: Card): WorkGroup | "" {
  if (!card) return "";
  if (card.kind === "ongoing") return "ongoing";
  if (card.kind === "todo" && card.todo_state) return card.todo_state;
  return "";
}

function groupIcon(group: WorkGroup, active: boolean) {
  const className = `size-4 ${active ? "text-sky-700" : "text-faint"}`;
  switch (group) {
    case "todo":
      return <Circle className={className} />;
    case "working":
      return <PlayCircle className={className} />;
    case "review":
      return <ClipboardCheck className={className} />;
    case "done":
      return <CheckCircle2 className={className} />;
    case "ongoing":
      return <Activity className={className} />;
  }
}

export function WorkSidebar({
  cards,
  activeCardID,
  onSelect,
  className = "",
}: {
  cards: Card[];
  activeCardID?: string;
  onSelect: (card: Card) => void;
  className?: string;
}) {
  const { t } = useTranslation();
  const workCards = useMemo(
    () =>
      cards
        .filter((card) => card.kind === "todo" || card.kind === "ongoing")
        .sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
    [cards],
  );
  const activeCard = useMemo(
    () => workCards.find((card) => card.id === activeCardID),
    [activeCardID, workCards],
  );
  const activeGroup = groupForCard(activeCard);
  const activeKey = `${activeCardID || ""}:${activeGroup || ""}`;
  const [manualExpansion, setManualExpansion] = useState<{
    activeKey: string;
    group: WorkGroup | "";
  } | null>(null);
  const expanded =
    manualExpansion?.activeKey === activeKey
      ? manualExpansion.group
      : activeGroup || "todo";

  const groups = useMemo(
    () =>
      [...todoGroups, "ongoing" as const].map((group) => ({
        group,
        cards:
          group === "ongoing"
            ? workCards.filter((card) => card.kind === "ongoing")
            : workCards.filter(
                (card) => card.kind === "todo" && card.todo_state === group,
              ),
      })),
    [workCards],
  );

  const labelFor = (group: WorkGroup) => {
    switch (group) {
      case "todo":
        return t("待办");
      case "working":
        return t("进行中");
      case "review":
        return t("待审核");
      case "done":
        return t("已完成");
      case "ongoing":
        return t("持续事项");
    }
  };

  return (
    <ContextSidebar
      ariaLabel={t("工作列表")}
      className={className}
      header={
        <div className="px-1 text-xs font-semibold text-muted-foreground">
          {t("工作")}
        </div>
      }
    >
      <div className="space-y-1.5">
        {groups.map(({ group, cards: groupCards }) => {
          const isExpanded = expanded === group;
          const containsActive = activeGroup === group;
          return (
            <section key={group}>
              <button
                type="button"
                aria-expanded={isExpanded}
                onClick={() =>
                  setManualExpansion({
                    activeKey,
                    group: isExpanded ? "" : group,
                  })
                }
                className={`flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left transition ${
                  containsActive
                    ? "text-heading"
                    : "text-secondary hover:bg-surface/70"
                }`}
              >
                {isExpanded ? (
                  <ChevronDown className="size-4 shrink-0 text-faint" />
                ) : (
                  <ChevronRight className="size-4 shrink-0 text-faint" />
                )}
                <span className="min-w-0 flex-1 truncate text-sm font-semibold">
                  {labelFor(group)}
                </span>
                <span className="rounded-full bg-surface px-2 py-0.5 text-[11px] text-faint ring-1 ring-line">
                  {groupCards.length}
                </span>
              </button>

              {isExpanded ? (
                <div className="mt-1 space-y-1 pl-2">
                  {groupCards.length ? (
                    groupCards.map((card) => {
                      const active = card.id === activeCardID;
                      return (
                        <ContextSidebarItem
                          key={card.id}
                          active={active}
                          onClick={() => onSelect(card)}
                          icon={groupIcon(group, active)}
                          title={card.title || t("未命名任务")}
                          description={card.description}
                          meta={
                            card.kind === "ongoing"
                              ? card.health || card.ongoing_state
                              : card.current_attempt_status || undefined
                          }
                        />
                      );
                    })
                  ) : (
                    <div className="px-3 py-5 text-center text-xs text-faint">
                      {t("该状态暂无任务")}
                    </div>
                  )}
                </div>
              ) : null}
            </section>
          );
        })}
      </div>
    </ContextSidebar>
  );
}
