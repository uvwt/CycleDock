import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Pin, Plus, Search } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { CardActions } from "../cards/CardActions";
import type { Card } from "../../shared/api/types";
import {
  ContextSidebar,
  ContextSidebarItem,
} from "../../shared/ui/ContextSidebar";
import {
  Button,
  HintTooltip,
  TextInput,
} from "../../shared/ui/components";

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

function getDiscussionStatus(
  card: Card,
  isActive: boolean,
  activeAttemptStatus?: string,
): "answering" | "unread" | "idle" {
  if (
    (isActive &&
      (activeAttemptStatus === "running" ||
        activeAttemptStatus === "queued")) ||
    card.current_attempt_status === "running" ||
    card.current_attempt_status === "queued"
  ) {
    return "answering";
  }

  // unread 由后端基于 message sequence + Human Source read cursor 计算；
  // 标题、置顶、状态变化不会推进这个游标。
  if (!isActive && card.unread) {
    return "unread";
  }

  return "idle";
}

function DiscussionStatusDot({
  status,
}: {
  status: "answering" | "unread" | "idle";
}) {
  if (status === "answering") {
    return (
      <span
        className="relative flex size-2.5 shrink-0 items-center justify-center"
        title="Agent 正在回答…"
        aria-label="Agent 正在回答…"
      >
        <span className="absolute inline-flex size-2 animate-ping rounded-full bg-emerald-400 opacity-75" />
        <span className="relative inline-flex size-2 rounded-full bg-emerald-500 shadow-[0_0_6px_rgba(16,185,129,0.5)]" />
      </span>
    );
  }

  if (status === "unread") {
    return (
      <span
        className="flex size-2.5 shrink-0 items-center justify-center"
        title="有新回复"
        aria-label="有新回复"
      >
        <span className="inline-flex size-2 rounded-full bg-sky-500 shadow-[0_0_4px_rgba(14,165,233,0.4)]" />
      </span>
    );
  }

  // 已看过或空闲状态不展示圆点，保留占位尺寸以保证标题纵向对齐
  return <span className="size-2.5 shrink-0" />;
}

export function DiscussionSidebar({
  discussions,
  activeCardID,
  activeAttemptStatus,
  onSelect,
  onCreate,
  className = "",
}: {
  discussions: Card[];
  activeCardID?: string;
  activeAttemptStatus?: string;
  onSelect: (card: Card) => void;
  onCreate: () => void;
  className?: string;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [term, setTerm] = useState("");
  const projectID = discussions[0]?.project_id || "";
  const mainDiscussion = useQuery({
    queryKey: keys.mainDiscussion(projectID),
    queryFn: () => api.mainDiscussion(projectID),
    enabled: Boolean(projectID),
  });
  const mainDiscussionID = mainDiscussion.data?.id || "";

  // 打开会话即推进服务端已读游标。成功后重新取列表，让服务端继续作为
  // unread 的唯一事实来源；不再使用浏览器 localStorage 推导。
  useEffect(() => {
    if (!activeCardID) return;
    const activeCard = discussions.find((card) => card.id === activeCardID);
    if (!activeCard?.unread) return;

    void api
      .markCardRead(activeCard.id)
      .then(() =>
        queryClient.invalidateQueries({
          queryKey: keys.cards(activeCard.project_id),
        }),
      )
      .catch(() => {
        // 读状态失败不影响会话本身；下一次列表刷新或再次打开时会重试。
      });
  }, [activeCardID, discussions]);

  const handleSelect = (card: Card) => {
    onSelect(card);
  };

  const pinMutation = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) =>
      api.pinCard(id, pinned),
    onSuccess: async (updated) => {
      await queryClient.invalidateQueries({
        queryKey: keys.cards(updated.project_id),
      });
      toast.success(updated.pinned_at ? "已置顶" : "已取消置顶");
    },
    onError: (err) => {
      toast.error("操作失败：" + String(err));
    },
  });

  const filtered = useMemo(() => {
    const query = term.trim().toLowerCase();
    return discussions
      .filter((card) => {
        if (!query) return true;
        return (card.title + " " + card.description)
          .toLowerCase()
          .includes(query);
      })
      .sort((a, b) => {
        if (mainDiscussionID) {
          if (a.id === mainDiscussionID) return -1;
          if (b.id === mainDiscussionID) return 1;
        }
        const aPinned = Boolean(a.pinned_at);
        const bPinned = Boolean(b.pinned_at);
        if (aPinned !== bPinned) return aPinned ? -1 : 1;
        if (aPinned && bPinned) {
          return (b.pinned_at || "").localeCompare(a.pinned_at || "");
        }
        return b.updated_at.localeCompare(a.updated_at);
      });
  }, [discussions, mainDiscussionID, term]);

  return (
    <ContextSidebar
      ariaLabel={t("讨论列表")}
      className={className}
      header={
        <>
          <div className="flex items-center justify-between gap-2 px-1">
            <div className="flex items-center gap-1.5">
              <span className="text-xs font-semibold text-foreground/80 tracking-wide">
                {t("讨论")}
              </span>
              {filtered.length > 0 && (
                <span className="rounded-full bg-muted/80 px-1.5 py-0.2 text-[10.5px] font-medium tabular-nums text-muted-foreground">
                  {filtered.length}
                </span>
              )}
            </div>
            <HintTooltip content={t("新建讨论")}>
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
          <label className="relative block">
            <span className="sr-only">{t("搜索讨论…")}</span>
            <Search className="pointer-events-none absolute top-2 left-2.5 size-3.5 text-muted-foreground/50" />
            <TextInput
              value={term}
              onChange={(event) => setTerm(event.target.value)}
              placeholder={t("搜索讨论…")}
              className="h-7.5 rounded-lg border-transparent bg-muted/50 pl-8 text-xs shadow-none placeholder:text-muted-foreground/50 transition-all focus:bg-surface focus:border-line/70 focus:shadow-2xs"
            />
          </label>
        </>
      }
    >
      {filtered.length ? (
        <div className="space-y-1">
          {filtered.map((card) => {
            const isMain = card.id === mainDiscussionID;
            return (
              <ContextSidebarItem
                key={card.id}
                active={card.id === activeCardID}
                onClick={() => handleSelect(card)}
                icon={
                  <DiscussionStatusDot
                    status={getDiscussionStatus(
                      card,
                      card.id === activeCardID,
                      activeAttemptStatus,
                    )}
                  />
                }
                title={card.title || t("新讨论")}
                actions={
                  <div className="flex items-center gap-0.5">
                    {/* 时间：悬浮时展示，与置顶按钮水平排列不冲突 */}
                    <span className="mr-0.5 hidden select-none text-[11px] font-normal tabular-nums text-faint group-hover:inline-block group-focus-within:inline-block">
                      {formatRelativeTime(card.updated_at)}
                    </span>

                    {/* 置顶标识与置顶按钮统一在一个位置：主讨论始终置顶且不可取消；普通讨论置顶时常显，未置顶时悬浮显 */}
                    {isMain ? (
                      <span
                        title={t("主讨论始终置顶")}
                        aria-label={t("主讨论始终置顶")}
                        className="grid size-6 place-items-center rounded-lg text-primary select-none cursor-default"
                      >
                        <Pin className="size-3.5 fill-primary/20 rotate-45" />
                      </span>
                    ) : (
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
                            ? "text-primary hover:text-primary/80 hover:bg-muted"
                            : "hidden text-muted-foreground/60 hover:text-foreground hover:bg-muted group-hover:grid group-focus-within:grid"
                        }`}
                      >
                        <Pin
                          className={`size-3.5 ${
                            card.pinned_at ? "fill-primary/20 rotate-45" : ""
                          }`}
                        />
                      </button>
                    )}

                    {/* 更多操作菜单：悬浮时显 */}
                    <div className="hidden group-hover:block group-focus-within:block has-[[data-state=open]]:block">
                      <CardActions
                        card={card}
                        menuSide="right"
                        preserve={isMain}
                        onDeleted={() => {
                          if (card.id === activeCardID) {
                            navigate(
                              `/chat?project=${encodeURIComponent(card.project_id)}`,
                            );
                          }
                        }}
                      />
                    </div>
                  </div>
                }
              />
            );
          })}
        </div>
      ) : (
        <div className="px-4 py-10 text-center text-sm text-faint">
          {term ? t("没有匹配的讨论") : t("还没有讨论")}
        </div>
      )}
    </ContextSidebar>
  );
}
