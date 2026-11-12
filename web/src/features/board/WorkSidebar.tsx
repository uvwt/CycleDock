import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  ChevronDown,
  ChevronRight,
  Pencil,
  Pin,
  PinOff,
  Trash2,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { Card, TodoState } from "../../shared/api/types";
import { api } from "../../shared/api/client";
import {
  patchCachedCard,
  removeCachedCard,
} from "../../shared/lib/card-cache";
import {
  ContextSidebar,
  ContextSidebarItem,
  ContextSidebarSkeleton,
} from "../../shared/ui/ContextSidebar";
import { Badge, ConfirmDialog } from "../../shared/ui/components";
import { EditCardDialog } from "../../shared/ui/EditCardDialog";
import {
  CardStatusDot,
  getCardVisualStatus,
} from "../../shared/ui/CardStatusDot";
import { stripMarkdownSummary } from "../../shared/lib/markdown";
import { CardActions } from "../cards/CardActions";

type WorkGroup = TodoState;

const todoGroups: TodoState[] = ["todo", "working", "review", "done"];

function groupForCard(card?: Card): WorkGroup | "" {
  if (!card) return "";
  if (card.kind === "todo" && card.todo_state) return card.todo_state;
  return "";
}

export function WorkSidebar({
  cards,
  activeCardID,
  activeAttemptStatus,
  onSelect,
  loading = false,
  className = "",
}: {
  cards: Card[];
  activeCardID?: string;
  activeAttemptStatus?: string;
  onSelect: (card: Card) => void;
  loading?: boolean;
  className?: string;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const workCards = useMemo(
    () =>
      cards
        .filter((card) => card.kind === "todo")
        .sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
    [cards],
  );
  const activeCard = useMemo(
    () => workCards.find((card) => card.id === activeCardID),
    [activeCardID, workCards],
  );
  const activeGroup = groupForCard(activeCard);
  const activeKey = `${activeCardID || ""}:${activeGroup || ""}`;
  const defaultExpandedGroups = useMemo<WorkGroup[]>(() => {
    if (activeGroup === "working" || activeGroup === "review") {
      return ["working", "review"];
    }
    return [activeGroup || "todo"];
  }, [activeGroup]);

  const [manualExpansion, setManualExpansion] = useState<{
    activeKey: string;
    expandedGroups: WorkGroup[];
  } | null>(null);

  const expandedGroups =
    manualExpansion?.activeKey === activeKey
      ? manualExpansion.expandedGroups
      : defaultExpandedGroups;

  const groups = useMemo(
    () =>
      todoGroups.map((group) => ({
        group,
        cards: workCards.filter((card) => card.todo_state === group),
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
        return t("待验收");
      case "done":
        return t("已完成");
    }
  };

  const queryClient = useQueryClient();
  const [deleteTarget, setDeleteTarget] = useState<Card | null>(null);
  const [editTarget, setEditTarget] = useState<Card | null>(null);

  const pinMutation = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) =>
      api.pinCard(id, pinned),
    onMutate: ({ id, pinned }) => {
      const target = cards.find((item) => item.id === id);
      const prevPinnedAt = target?.pinned_at;
      patchCachedCard(queryClient, id, target?.project_id, {
        pinned_at: pinned ? new Date().toISOString() : undefined,
      });
      return { prevPinnedAt, cardProjectID: target?.project_id };
    },
    onSuccess: (updated) => {
      patchCachedCard(queryClient, updated.id, updated.project_id, updated);
      void queryClient.invalidateQueries({ queryKey: ["cards"] });
    },
    onError: (_err, variables, context) => {
      patchCachedCard(queryClient, variables.id, context?.cardProjectID, {
        pinned_at: context?.prevPinnedAt,
      });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (card: Card) => api.deleteCard(card.id),
    onSuccess: (_, deletedCard) => {
      removeCachedCard(queryClient, deletedCard);
      void queryClient.invalidateQueries({ queryKey: ["cards"] });
      setDeleteTarget(null);
    },
  });

  if (loading && cards.length === 0) {
    return (
      <ContextSidebarSkeleton
        ariaLabel={t("任务列表")}
        className={className}
      />
    );
  }

  return (
    <ContextSidebar
      ariaLabel={t("任务列表")}
      className={className}
      hideHeaderOnMobile
      header={
        <div className="px-1 text-xs font-medium text-muted-foreground">
          {t("任务")}
        </div>
      }
    >
      <div className="space-y-1.5">
        {groups.map(({ group, cards: groupCards }) => {
          const isExpanded = expandedGroups.includes(group);
          const containsActive = activeGroup === group;
          return (
            <section key={group}>
              <button
                type="button"
                aria-expanded={isExpanded}
                onClick={() =>
                  setManualExpansion({
                    activeKey,
                    expandedGroups: isExpanded
                      ? expandedGroups.filter((g) => g !== group)
                      : [...expandedGroups, group],
                  })
                }
                className={`flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left transition ${
                  containsActive
                    ? "text-heading"
                    : "text-secondary-foreground hover:bg-surface/70"
                }`}
              >
                {isExpanded ? (
                  <ChevronDown className="size-4 shrink-0 text-faint" />
                ) : (
                  <ChevronRight className="size-4 shrink-0 text-faint" />
                )}
                <span className="min-w-0 flex-1 truncate text-sm font-normal">
                  {labelFor(group)}
                </span>
                <Badge
                  variant="secondary"
                  className="px-1.5 py-0 text-[11px] font-normal text-faint"
                >
                  {groupCards.length}
                </Badge>
              </button>

              {isExpanded ? (
                <div className="mt-1 space-y-1 pl-2">
                  {groupCards.length ? (
                    groupCards.map((card) => {
                      const active = card.id === activeCardID;
                      const summary = stripMarkdownSummary(card.description);
                      const swipeActions = [
                        {
                          key: "pin",
                          label: card.pinned_at ? t("取消置顶") : t("置顶"),
                          icon: card.pinned_at ? (
                            <PinOff className="size-4" />
                          ) : (
                            <Pin className="size-4 rotate-45" />
                          ),
                          variant: "primary" as const,
                          onClick: () => {
                            pinMutation.mutate({
                              id: card.id,
                              pinned: !card.pinned_at,
                            });
                          },
                        },
                        {
                          key: "edit",
                          label: t("编辑"),
                          icon: <Pencil className="size-4" />,
                          variant: "secondary" as const,
                          onClick: () => {
                            setEditTarget(card);
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
                          active={active}
                          pinned={Boolean(card.pinned_at)}
                          onClick={() => onSelect(card)}
                          swipeActions={swipeActions}
                          icon={
                            <CardStatusDot
                              status={getCardVisualStatus(
                                card,
                                active,
                                activeAttemptStatus,
                              )}
                            />
                          }
                          title={card.title || t("未命名任务")}
                          description={summary || undefined}
                          actions={
                            <div className="flex items-center gap-0.5">
                              <button
                                type="button"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  pinMutation.mutate({
                                    id: card.id,
                                    pinned: !card.pinned_at,
                                  });
                                }}
                                title={card.pinned_at ? t("取消置顶") : t("置顶")}
                                aria-label={
                                  card.pinned_at ? t("取消置顶") : t("置顶")
                                }
                                className={`grid size-6 place-items-center rounded-lg transition-all ${
                                  card.pinned_at
                                    ? "text-amber-500 hover:bg-muted hover:text-amber-600"
                                    : "text-muted-foreground/60 hover:bg-muted hover:text-foreground"
                                }`}
                              >
                                {card.pinned_at ? (
                                  <PinOff className="size-3.5" />
                                ) : (
                                  <Pin className="size-3.5" />
                                )}
                              </button>
                              <CardActions
                                card={card}
                                menuSide="right"
                                onDeleted={() => {
                                  if (card.id === activeCardID) {
                                    navigate(
                                      `/board/${encodeURIComponent(card.project_id)}`,
                                    );
                                  }
                                }}
                              />
                            </div>
                          }
                        />
                      );
                    })
                  ) : (
                    <div className="mx-1 rounded-xl border border-dashed border-line/60 bg-surface/40 px-3 py-4 text-center text-xs text-faint">
                      {t("该状态暂无任务")}
                    </div>
                  )}
                </div>
              ) : null}
            </section>
          );
        })}
      </div>

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t("删除这个任务？")}
        description={t(
          "删除任务将同步清理其所有执行尝试和产物，该操作无法撤销。",
        )}
        confirmLabel={t("删除任务")}
        pending={deleteMutation.isPending}
        onConfirm={() => {
          if (deleteTarget) {
            deleteMutation.mutate(deleteTarget);
          }
        }}
      />

      <EditCardDialog
        card={editTarget}
        open={Boolean(editTarget)}
        onOpenChange={(open) => !open && setEditTarget(null)}
      />
    </ContextSidebar>
  );
}
