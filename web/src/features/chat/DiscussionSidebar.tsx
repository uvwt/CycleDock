import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  BellOff,
  CheckCheck,
  ChevronDown,
  ChevronRight,
  Loader2,
  MessageSquareText,
  Pin,
  PinOff,
  Plus,
  Search,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import {
  patchCachedCard,
  removeCachedCard,
} from "../../shared/lib/card-cache";
import { useCardNotificationMute } from "../../shared/lib/useCardNotificationMute";
import { activeCardAutoReadSuppression } from "../../shared/lib/card-read-state";
import {
  useBatchMarkCardsRead,
  useCardReadState,
} from "../../shared/lib/useCardReadState";
import { CardActions } from "../cards/CardActions";
import { CardRowActionSheet } from "../cards/CardRowActionSheet";
import { buildCardSwipeActions } from "../cards/card-swipe-actions";
import type { Card } from "../../shared/api/types";
import {
  ContextSidebar,
  ContextSidebarItem,
  ContextSidebarSkeleton,
} from "../../shared/ui/ContextSidebar";
import {
  Badge,
  Button,
  ConfirmDialog,
  HintTooltip,
  TextInput,
} from "../../shared/ui/components";
import { EditCardDialog } from "../../shared/ui/EditCardDialog";
import {
  buildDiscussionListSections,
  getUnreadDiscussionCardIDs,
  shouldDefaultExpandMutedDiscussions,
} from "./discussion-sidebar-model";

function formatRelativeTime(dateStr?: string) {
  if (!dateStr) return "";
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  if (Number.isNaN(diffMs)) return "";
  const diffMins = Math.floor(diffMs / (1000 * 60));
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60));
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));
  if (diffMins < 5) return "刚刚";
  if (diffMins < 60) return `${diffMins}m`;
  if (diffHours < 24) return `${diffHours}h`;
  if (diffDays === 1) return "昨天";
  if (diffDays < 7) return `${diffDays}d`;
  return `${date.getMonth() + 1}/${date.getDate()}`;
}

import { CardStatusDot, getCardVisualStatus } from "../../shared/ui/CardStatusDot";

export function DiscussionSidebar({
  discussions,
  activeCardID,
  activeAttemptStatus,
  onSelect,
  onCreate,
  loading = false,
  className = "",
}: {
  discussions: Card[];
  activeCardID?: string;
  activeAttemptStatus?: string;
  onSelect: (card: Card) => void;
  onCreate: () => void;
  loading?: boolean;
  className?: string;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [term, setTerm] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<Card | null>(null);
  const [editTarget, setEditTarget] = useState<Card | null>(null);
  const [sheetTargetID, setSheetTargetID] = useState<string | null>(null);

  const projectID = discussions[0]?.project_id || "";
  const mainDiscussion = useQuery({
    queryKey: keys.mainDiscussion(projectID),
    queryFn: () => api.mainDiscussion(projectID),
    enabled: Boolean(projectID),
  });
  const mainDiscussionID = mainDiscussion.data?.id || "";

  const handleSelect = (card: Card) => {
    activeCardAutoReadSuppression.clearSuppressed(card.id);
    if (card.id === activeCardID && card.marked_unread) {
      readStateMutation.mutate({
        id: card.id,
        unread: false,
        projectID: card.project_id,
        isActive: true,
      });
    }
    onSelect(card);
  };

  const pinMutation = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) =>
      api.pinCard(id, pinned),
    onMutate: async ({ id, pinned }) => {
      const target = discussions.find((item) => item.id === id);
      const prevPinnedAt = target?.pinned_at;
      patchCachedCard(queryClient, id, target?.project_id || projectID, {
        pinned_at: pinned ? new Date().toISOString() : undefined,
      });
      return { prevPinnedAt, cardProjectID: target?.project_id || projectID };
    },
    onSuccess: (updated) => {
      patchCachedCard(queryClient, updated.id, updated.project_id, updated);
      void queryClient.invalidateQueries({
        queryKey: keys.cards(updated.project_id),
      });
      toast.success(updated.pinned_at ? "已置顶" : "已取消置顶");
    },
    onError: (err, variables, context) => {
      patchCachedCard(queryClient, variables.id, context?.cardProjectID, {
        pinned_at: context?.prevPinnedAt,
      });
      toast.error("操作失败：" + String(err));
    },
  });

  const muteMutation = useCardNotificationMute();
  const readStateMutation = useCardReadState();
  const batchMarkRead = useBatchMarkCardsRead(projectID);
  const sheetTarget = useMemo(
    () => discussions.find((item) => item.id === sheetTargetID) || null,
    [discussions, sheetTargetID],
  );

  const deleteMutation = useMutation({
    mutationFn: (card: Card) => api.deleteCard(card.id),
    onSuccess: (_, card) => {
      removeCachedCard(queryClient, card);
      void queryClient.invalidateQueries({
        queryKey: keys.cards(card.project_id),
      });
      toast.success("讨论已删除");
      if (card.id === activeCardID) {
        navigate(`/chat?project=${encodeURIComponent(card.project_id)}`);
      }
      setDeleteTarget(null);
    },
    onError: (err) => {
      toast.error("删除失败：" + (err instanceof Error ? err.message : String(err)));
    },
  });

  const sections = useMemo(
    () =>
      buildDiscussionListSections(discussions, {
        mainDiscussionID,
        searchQuery: term,
        activeCardID,
        activeAttemptStatus,
      }),
    [discussions, mainDiscussionID, term, activeCardID, activeAttemptStatus],
  );

  const mutedUnreadIDs = useMemo(
    () => getUnreadDiscussionCardIDs(sections.muted, activeCardID),
    [sections.muted, activeCardID],
  );

  const hasSearch = term.trim().length > 0;
  const activeInMuted = useMemo(
    () =>
      Boolean(
        activeCardID && sections.muted.some((card) => card.id === activeCardID),
      ),
    [sections.muted, activeCardID],
  );
  const mutedExpansionContextKey = `${activeCardID || ""}:${activeInMuted ? "muted" : "normal"}:${hasSearch ? "search" : "browse"}`;
  const defaultMutedExpanded = shouldDefaultExpandMutedDiscussions(
    sections.muted,
    { activeCardID, searchQuery: term },
  );
  const [manualMutedExpansion, setManualMutedExpansion] = useState<{
    contextKey: string;
    expanded: boolean;
  } | null>(null);
  const mutedExpanded =
    manualMutedExpansion?.contextKey === mutedExpansionContextKey
      ? manualMutedExpansion.expanded
      : defaultMutedExpanded;

  const renderDiscussionItem = (card: Card) => {
    const isMain = card.id === mainDiscussionID;
    const isPinned = isMain || Boolean(card.pinned_at);
    const isMuted = Boolean(card.notifications_muted);
    const isActive = card.id === activeCardID;
    const swipeActions = buildCardSwipeActions({
      card,
      isMain,
      isActive,
      t,
      onToggleRead: (nextUnread) => {
        readStateMutation.mutate({
          id: card.id,
          unread: nextUnread,
          projectID: card.project_id,
          isActive,
        });
      },
      onTogglePin: !isMain
        ? () => {
            pinMutation.mutate({
              id: card.id,
              pinned: !card.pinned_at,
            });
          }
        : undefined,
      onToggleMute: () => {
        muteMutation.mutate({
          id: card.id,
          muted: !isMuted,
          projectID: card.project_id,
        });
      },
      onDelete: !isMain
        ? () => {
            setDeleteTarget(card);
          }
        : undefined,
    });

    return (
      <ContextSidebarItem
        key={card.id}
        active={isActive}
        pinned={isPinned}
        muted={isMuted}
        onClick={() => handleSelect(card)}
        onLongPress={() => setSheetTargetID(card.id)}
        swipeActions={swipeActions.length ? swipeActions : undefined}
        icon={
          <CardStatusDot
            status={getCardVisualStatus(card, isActive, activeAttemptStatus)}
            muted={isMuted}
          />
        }
        title={card.title || t("新讨论")}
        actions={
          <div className="flex items-center gap-0.5">
            {/* 时间：悬浮时展示，与置顶按钮水平排列不冲突 */}
            <span className="mr-0.5 select-none text-[11px] font-normal tabular-nums text-faint">
              {formatRelativeTime(card.updated_at)}
            </span>

            {/* 桌面端快捷置顶切换（显隐由 ContextSidebarItem 的 opacity 统一控制，避免 display:none 导致关闭菜单瞬间坐标归零） */}
            {!isMain && (
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  pinMutation.mutate({
                    id: card.id,
                    pinned: !card.pinned_at,
                  });
                }}
                title={card.pinned_at ? t("取消置顶") : t("置顶讨论")}
                aria-label={card.pinned_at ? t("取消置顶") : t("置顶讨论")}
                className={`grid size-6 place-items-center rounded-lg transition-all ${
                  card.pinned_at
                    ? "text-amber-500 hover:text-amber-600 hover:bg-muted"
                    : "text-muted-foreground/60 hover:text-foreground hover:bg-muted"
                }`}
              >
                {card.pinned_at ? (
                  <PinOff className="size-3.5" />
                ) : (
                  <Pin className="size-3.5" />
                )}
              </button>
            )}

            <CardActions
              card={card}
              menuSide="right"
              preserve={isMain}
              isActive={isActive}
              onDeleted={() => {
                if (card.id === activeCardID) {
                  navigate(
                    `/chat?project=${encodeURIComponent(card.project_id)}`,
                  );
                }
              }}
            />
          </div>
        }
      />
    );
  };

  if (loading && discussions.length === 0) {
    return (
      <ContextSidebarSkeleton
        ariaLabel={t("讨论列表")}
        className={className}
      />
    );
  }

  return (
    <ContextSidebar
      ariaLabel={t("讨论列表")}
      className={className}
      header={
        <>
          <div className="hidden items-center justify-between gap-2 px-1 md:flex">
            <div className="flex items-center gap-1.5">
              <span className="text-xs font-semibold text-foreground/80 tracking-wide">
                {t("讨论")}
              </span>
              {sections.totalCount > 0 && (
                <span className="rounded-full bg-muted/80 px-1.5 py-0.2 text-[10.5px] font-medium tabular-nums text-muted-foreground">
                  {sections.totalCount}
                </span>
              )}
            </div>
            <HintTooltip content={t("新建讨论")} side="bottom">
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label={t("新建讨论")}
                onClick={onCreate}
                className="size-7 rounded-lg text-muted-foreground hover:bg-surface hover:text-foreground hover:shadow-2xs transition-all"
              >
                <Plus className="size-3.5" />
              </Button>
            </HintTooltip>
          </div>
          <div className="flex items-center gap-1.5">
            <label className="relative block min-w-0 flex-1">
              <span className="sr-only">{t("搜索讨论…")}</span>
              <Search className="pointer-events-none absolute top-2 left-2.5 size-3.5 text-muted-foreground/50" />
              <TextInput
                value={term}
                onChange={(event) => setTerm(event.target.value)}
                placeholder={t("搜索讨论…")}
                className="h-7.5 rounded-lg border-transparent bg-muted/50 pl-8 text-xs shadow-none placeholder:text-muted-foreground/50 transition-all focus:bg-surface focus:border-line/70 focus:shadow-2xs"
              />
            </label>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label={t("新建讨论")}
              onClick={onCreate}
              className="size-7.5 shrink-0 rounded-lg border border-line/70 bg-surface text-muted-foreground shadow-2xs transition-all hover:bg-muted hover:text-foreground md:hidden"
            >
              <Plus className="size-3.5" />
            </Button>
          </div>
        </>
      }
    >
      {sections.totalCount > 0 ? (
        <div className="space-y-1">
          {sections.pinned.map(renderDiscussionItem)}

          {/* 顶部单行免打扰折叠入口：紧跟主聊天与置顶下方，避免长讨论列表需要翻到底才能找到免打扰会话 */}
          {sections.muted.length > 0 ? (
            <section
              data-discussion-group="muted"
              className="my-1 rounded-xl border border-line/60 bg-muted/35 p-0.5"
            >
              <div className="flex items-center gap-1">
                <button
                  type="button"
                  aria-expanded={mutedExpanded}
                  onClick={() =>
                    setManualMutedExpansion({
                      contextKey: mutedExpansionContextKey,
                      expanded: !mutedExpanded,
                    })
                  }
                  className={`flex min-w-0 flex-1 items-center gap-1.5 rounded-lg px-2 py-1.5 text-left transition ${
                    activeInMuted
                      ? "text-heading"
                      : "text-muted-foreground hover:bg-surface/70 hover:text-foreground"
                  }`}
                >
                  {mutedExpanded ? (
                    <ChevronDown className="size-3.5 shrink-0 text-faint" />
                  ) : (
                    <ChevronRight className="size-3.5 shrink-0 text-faint" />
                  )}
                  <BellOff className="size-3 shrink-0 text-muted-foreground/75" />
                  <span className="min-w-0 flex-1 truncate text-xs font-medium">
                    {t("免打扰")}
                  </span>
                  {mutedUnreadIDs.length > 0 ? (
                    <span
                      data-muted-unread-summary="true"
                      className="inline-flex shrink-0 items-center gap-1 rounded-full bg-surface/90 px-1.5 py-0.5 text-[10px] font-normal leading-none tabular-nums text-muted-foreground"
                    >
                      <span
                        aria-hidden="true"
                        className="inline-flex size-1.5 rounded-full bg-muted-foreground/50"
                      />
                      {t("{{count}} 未读", { count: mutedUnreadIDs.length })}
                    </span>
                  ) : null}
                  <Badge
                    variant="secondary"
                    className="px-1.5 py-0 text-[11px] font-normal tabular-nums text-faint"
                  >
                    {sections.muted.length}
                  </Badge>
                </button>
                {mutedUnreadIDs.length > 0 ? (
                  <HintTooltip content={t("全部已读")} side="top">
                    <button
                      type="button"
                      disabled={batchMarkRead.isPending}
                      aria-label={t("全部已读")}
                      onClick={() =>
                        void batchMarkRead.markBatchRead(mutedUnreadIDs)
                      }
                      className="inline-flex h-7 shrink-0 items-center gap-1 rounded-lg px-1.5 text-[11px] font-medium text-muted-foreground transition hover:bg-surface/80 hover:text-foreground disabled:opacity-50"
                    >
                      {batchMarkRead.isPending ? (
                        <Loader2 className="size-3.5 animate-spin" />
                      ) : (
                        <CheckCheck className="size-3.5" />
                      )}
                    </button>
                  </HintTooltip>
                ) : null}
              </div>

              {mutedExpanded ? (
                <div className="mt-0.5 space-y-0.5 border-t border-divider/70 pt-1 pl-1">
                  {sections.muted.map(renderDiscussionItem)}
                </div>
              ) : null}
            </section>
          ) : null}

          {sections.regular.map(renderDiscussionItem)}
        </div>
      ) : (
        <div className="mx-1 mt-1 rounded-xl border border-dashed border-line/60 bg-surface/40 px-4 py-8 text-center">
          <MessageSquareText className="mx-auto mb-2 size-5 text-faint/70" />
          <p className="text-xs font-medium text-muted-foreground">
            {term ? t("没有匹配的讨论") : t("还没有讨论")}
          </p>
          {!term && (
            <p className="mt-1 text-[11px] text-faint">
              {t("新建一个讨论，直接发送第一条消息。")}
            </p>
          )}
        </div>
      )}

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t("删除这个讨论？")}
        description={t(
          "已生成的 Todo / Ongoing 不会被删除，但这个讨论及其会话内容将不再保留。",
        )}
        confirmLabel={t("删除讨论")}
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

      <CardRowActionSheet
        card={sheetTarget}
        open={Boolean(sheetTarget)}
        onOpenChange={(open) => !open && setSheetTargetID(null)}
        preserve={Boolean(sheetTarget && sheetTarget.id === mainDiscussionID)}
        isActive={Boolean(sheetTarget && sheetTarget.id === activeCardID)}
        readPending={readStateMutation.isPending}
        pinPending={pinMutation.isPending}
        mutePending={muteMutation.isPending}
        deletePending={deleteMutation.isPending}
        onToggleRead={(target, nextUnread) => {
          readStateMutation.mutate({
            id: target.id,
            unread: nextUnread,
            projectID: target.project_id,
            isActive: target.id === activeCardID,
          });
        }}
        onTogglePin={(target) => {
          pinMutation.mutate({
            id: target.id,
            pinned: !target.pinned_at,
          });
        }}
        onToggleMute={(target) => {
          muteMutation.mutate({
            id: target.id,
            muted: !target.notifications_muted,
            projectID: target.project_id,
          });
        }}
        onEdit={(target) => setEditTarget(target)}
        onDelete={(target) => setDeleteTarget(target)}
      />
    </ContextSidebar>
  );
}
