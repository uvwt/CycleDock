import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Bell, BellOff, CalendarClock, Pencil, Pin, PinOff, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { Card } from "../../shared/api/types";
import { api } from "../../shared/api/client";
import {
  patchCachedCard,
  removeCachedCard,
} from "../../shared/lib/card-cache";
import { useCardNotificationMute } from "../../shared/lib/useCardNotificationMute";
import {
  ContextSidebar,
  ContextSidebarItem,
  ContextSidebarSkeleton,
} from "../../shared/ui/ContextSidebar";
import { ConfirmDialog } from "../../shared/ui/components";
import { EditCardDialog } from "../../shared/ui/EditCardDialog";
import { stripMarkdownSummary } from "../../shared/lib/markdown";
import {
  CardStatusDot,
  getCardVisualStatus,
} from "../../shared/ui/CardStatusDot";
import { OngoingHealthBadge } from "../../shared/ui/OngoingHealthBadge";
import { CardActions } from "../cards/CardActions";

export function PlanSidebar({
  cards,
  activeCardID,
  activeAttemptStatus,
  onSelect,
  loading = false,
  className = "",
}: {
  cards: Card[];
  activeCardID: string;
  activeAttemptStatus?: string;
  onSelect: (card: Card) => void;
  loading?: boolean;
  className?: string;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
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

  const muteMutation = useCardNotificationMute();

  const deleteMutation = useMutation({
    mutationFn: (card: Card) => api.deleteCard(card.id),
    onSuccess: (_, deletedCard) => {
      removeCachedCard(queryClient, deletedCard);
      void queryClient.invalidateQueries({ queryKey: ["cards"] });
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

  if (loading && cards.length === 0) {
    return (
      <ContextSidebarSkeleton
        ariaLabel={t("定时列表")}
        className={className}
      />
    );
  }

  return (
    <ContextSidebar
      ariaLabel={t("定时任务列表")}
      hideHeaderOnMobile
      header={
        <div className="px-1 text-xs font-medium text-muted-foreground">
          {t("定时任务")}
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
              card.health === "critical" ||
              card.health === "acknowledged";

            const isMuted = Boolean(card.notifications_muted);
            const swipeActions = [
              {
                key: "mute",
                label: isMuted ? t("取消静音") : t("静音通知"),
                icon: isMuted ? (
                  <Bell className="size-4" />
                ) : (
                  <BellOff className="size-4" />
                ),
                variant: "secondary" as const,
                onClick: () => {
                  muteMutation.mutate({
                    id: card.id,
                    muted: !isMuted,
                    projectID: card.project_id,
                  });
                },
              },
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

            const visualStatus = getCardVisualStatus(
              card,
              card.id === activeCardID,
              activeAttemptStatus,
            );

            return (
              <ContextSidebarItem
                key={card.id}
                active={card.id === activeCardID}
                pinned={Boolean(card.pinned_at)}
                muted={isMuted}
                onClick={() => onSelect(card)}
                swipeActions={swipeActions}
                icon={
                  visualStatus !== "idle" ? (
                    <CardStatusDot status={visualStatus} />
                  ) : (
                    <CalendarClock className="size-4 text-faint" />
                  )
                }
                title={card.title || t("未命名定时任务")}
                description={summary || undefined}
                meta={
                  showHealth ? (
                    <OngoingHealthBadge health={card.health} size="xs" />
                  ) : undefined
                }
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
                      aria-label={card.pinned_at ? t("取消置顶") : t("置顶")}
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
                            `/plans/${encodeURIComponent(card.project_id)}`,
                          );
                        }
                      }}
                    />
                  </div>
                }
              />
            );
          })}
        </div>
      ) : (
        <div className="mx-1 rounded-xl border border-dashed border-line/60 bg-surface/40 px-4 py-8 text-center">
          <CalendarClock className="mx-auto mb-2 size-5 text-faint/70" />
          <p className="text-xs text-faint">{t("这个项目还没有定时任务")}</p>
        </div>
      )}

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t("删除这个定时任务？")}
        description={t(
          "删除定时任务将同步清理其所有执行记录和周期规则，该操作无法撤销。",
        )}
        confirmLabel={t("删除定时任务")}
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
