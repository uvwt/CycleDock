import { useState } from "react";
import { CalendarClock, Pin, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { Card } from "../../shared/api/types";
import { api } from "../../shared/api/client";
import {
  ContextSidebar,
  ContextSidebarItem,
} from "../../shared/ui/ContextSidebar";
import { ConfirmDialog } from "../../shared/ui/components";
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
  const queryClient = useQueryClient();
  const [deleteTarget, setDeleteTarget] = useState<Card | null>(null);

  const pinMutation = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) =>
      api.pinCard(id, pinned),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["cards"] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (card: Card) => api.deleteCard(card.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["cards"] });
      setDeleteTarget(null);
    },
  });

  const plans = cards
    .filter((card) => card.kind === "ongoing")
    .sort((a, b) => {
      const aPinned = Boolean(a.pinned_at);
      const bPinned = Boolean(b.pinned_at);
      if (aPinned !== bPinned) return aPinned ? -1 : 1;
      if (aPinned && bPinned) {
        return (b.pinned_at || "").localeCompare(a.pinned_at || "");
      }
      return b.updated_at.localeCompare(a.updated_at);
    });

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

            const swipeActions = [
              {
                key: "pin",
                label: card.pinned_at ? t("取消置顶") : t("置顶"),
                icon: <Pin className="size-4 rotate-45" />,
                variant: "primary" as const,
                onClick: () => {
                  pinMutation.mutate({
                    id: card.id,
                    pinned: !card.pinned_at,
                  });
                },
              },
              {
                key: "delete",
                label: t("删除"),
                icon: <Trash2 className="size-4" />,
                variant: "danger" as const,
                onClick: () => {
                  setDeleteTarget(card);
                },
              },
            ];

            return (
              <ContextSidebarItem
                key={card.id}
                active={card.id === activeCardID}
                onClick={() => onSelect(card)}
                swipeActions={swipeActions}
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

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t("删除这个计划？")}
        description={t(
          "删除计划将同步清理其所有执行记录和周期规则，该操作无法撤销。",
        )}
        confirmLabel={t("删除计划")}
        pending={deleteMutation.isPending}
        onConfirm={() => {
          if (deleteTarget) {
            deleteMutation.mutate(deleteTarget);
          }
        }}
      />
    </ContextSidebar>
  );
}
