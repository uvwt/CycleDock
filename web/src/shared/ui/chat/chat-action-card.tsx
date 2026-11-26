import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import {
  CheckCircle2,
  Clock,
  Sparkles,
  ArrowUpRight,
  FileQuestion,
  Loader2,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { cn } from "@/shared/lib/utils";
import { api, APIError } from "@/shared/api/client";
import { keys } from "@/app/query";
import type { Card, CardDetail } from "@/shared/api/types";
import { isReviewTodoCard } from "@/shared/lib/review-acceptance";
import { stripMarkdownSummary } from "@/shared/lib/markdown";
import { useCompleteReviewCard } from "@/shared/lib/useCompleteReviewCard";
import { CardStatusDot, getCardVisualStatus } from "@/shared/ui/CardStatusDot";

const chatCardVariants = cva(
  "group/card block select-none rounded-xl border transition-all duration-150 text-left outline-none focus-visible:ring-2 focus-visible:ring-primary/40",
  {
    variants: {
      variant: {
        default:
          "border-line/70 bg-muted/40 hover:bg-muted/75 hover:border-primary/40 hover:shadow-2xs active:scale-[0.99] dark:bg-muted/20 dark:hover:bg-muted/40",
        elevated:
          "border-line bg-surface shadow-xs hover:border-primary/50 hover:shadow-sm active:scale-[0.99]",
        ghost:
          "border-transparent bg-muted/40 hover:bg-muted/70 hover:border-line/60",
      },
      size: {
        sm: "p-2 max-w-sm text-xs",
        default: "p-2.5 sm:p-3 w-full max-w-lg text-sm",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

export interface ChatActionCardProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof chatCardVariants> {
  cardId: string;
  projectId?: string;
  fallbackTitle?: string;
  onOpenCard?: (cardId: string, card?: Card) => void;
}

export const ChatActionCard = React.forwardRef<
  HTMLDivElement,
  ChatActionCardProps
>(
  (
    {
      cardId,
      projectId,
      fallbackTitle,
      variant,
      size,
      className,
      onOpenCard,
      ...props
    },
    ref,
  ) => {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const completeReview = useCompleteReviewCard();

    const {
      data: cardDetail,
      isLoading,
      isError,
    } = useQuery<CardDetail | { card: null; notFound: true }>({
      queryKey: keys.card(cardId),
      queryFn: async () => {
        try {
          return await api.card(cardId);
        } catch (err) {
          // 当卡片被删除或不存在返回 404/410 时，视为正常业务状态缓存，避免反复发起网络重试与界面闪烁
          if (
            err instanceof APIError &&
            (err.status === 404 || err.status === 410)
          ) {
            return { card: null, notFound: true };
          }
          throw err;
        }
      },
      enabled: Boolean(cardId),
      staleTime: 1000 * 60 * 10,
      gcTime: 1000 * 60 * 30,
      retry: (failureCount, error) => {
        if (
          error instanceof APIError &&
          (error.status === 404 || error.status === 410)
        ) {
          return false;
        }
        return failureCount < 1;
      },
      refetchOnWindowFocus: false,
    });

    const isNotFound = Boolean(
      cardDetail && "notFound" in cardDetail && cardDetail.notFound,
    );
    const card = cardDetail && "card" in cardDetail ? cardDetail.card : null;

    const targetProject = card?.project_id || projectId;
    const targetUrl = React.useMemo(() => {
      if (card?.kind === "discussion") {
        return targetProject
          ? `/chat/${targetProject}/${card.id}`
          : `/chat/${card.id}`;
      }
      if (card?.kind === "ongoing") {
        return targetProject
          ? `/plans/${targetProject}/${card.id}`
          : `/cards/${card?.id || cardId}`;
      }
      return targetProject
        ? `/cards/${targetProject}/${card?.id || cardId}`
        : `/cards/${cardId}`;
    }, [card, targetProject, cardId]);

    const cleanDescription = React.useMemo(
      () => stripMarkdownSummary(card?.description),
      [card?.description],
    );

    const visualStatus = React.useMemo(() => {
      if (!card) return "idle";
      return getCardVisualStatus(card);
    }, [card]);

    const kindIcon = React.useMemo(() => {
      const kind = card?.kind;
      if (kind === "todo") {
        return <CheckCircle2 className="size-3.5 sm:size-4 text-sky-500 shrink-0" />;
      }
      if (kind === "ongoing") {
        return <Clock className="size-3.5 sm:size-4 text-amber-500 shrink-0" />;
      }
      if (kind === "discussion") {
        return <Sparkles className="size-3.5 sm:size-4 text-purple-500 shrink-0" />;
      }
      return <CheckCircle2 className="size-3.5 sm:size-4 text-primary shrink-0" />;
    }, [card?.kind]);

    const statusBadge = React.useMemo(() => {
      if (!card) return null;
      const statusKey =
        card.kind === "todo"
          ? card.todo_state || "todo"
          : card.kind === "ongoing"
            ? card.ongoing_state || "active"
            : card.discussion_state || "active";

      const statusLabels: Record<string, { label: string; color: string }> = {
        todo: {
          label: t("待办"),
          color: "bg-muted text-muted-foreground border-line",
        },
        working: {
          label: t("进行中"),
          color:
            "bg-sky-500/10 text-sky-700 dark:text-sky-300 border-sky-500/20",
        },
        review: {
          label: t("待确认"),
          color:
            "bg-amber-500/10 text-amber-700 dark:text-amber-300 border-amber-500/20",
        },
        done: {
          label: t("已完成"),
          color:
            "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 border-emerald-500/20",
        },
        active: {
          label: t("进行中"),
          color:
            "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 border-emerald-500/20",
        },
        paused: {
          label: t("已暂停"),
          color: "bg-muted text-muted-foreground border-line",
        },
        archived: {
          label: t("已归档"),
          color: "bg-muted text-muted-foreground border-line",
        },
      };

      const s = statusLabels[statusKey] || {
        label: statusKey,
        color: "bg-muted text-muted-foreground border-line",
      };

      return (
        <span
          className={cn(
            "inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] font-medium border leading-none whitespace-nowrap shrink-0",
            s.color,
          )}
        >
          {s.label}
        </span>
      );
    }, [card, t]);

    const handleClick = (e: React.MouseEvent) => {
      // Allow ctrl/meta key or middle click to open new tab natively
      if (e.ctrlKey || e.metaKey || e.button === 1) {
        return;
      }

      e.preventDefault();

      if (onOpenCard) {
        onOpenCard(cardId, card ?? undefined);
        return;
      }

      navigate(targetUrl);
    };

    if (isLoading) {
      return (
        <div
          ref={ref}
          className={cn(
            chatCardVariants({ variant, size }),
            "my-2 flex items-center gap-2.5 opacity-85",
            className,
          )}
          {...props}
        >
          <Loader2 className="size-4 animate-spin text-muted-foreground shrink-0" />
          <div className="flex-1 min-w-0">
            <div className="h-4 w-3/4 rounded bg-muted animate-pulse mb-1" />
            <div className="h-3 w-1/3 rounded bg-muted animate-pulse" />
          </div>
        </div>
      );
    }

    if (isNotFound || isError || (!card && !fallbackTitle)) {
      return (
        <div
          ref={ref}
          className={cn(
            chatCardVariants({ variant: "ghost", size }),
            "my-2 flex items-center gap-2 text-muted-foreground border border-dashed border-line",
            className,
          )}
          {...props}
        >
          <FileQuestion className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate text-xs">
            {t("引用卡片不存在或已被移除 ({{id}})", { id: cardId.slice(0, 8) })}
          </span>
        </div>
      );
    }

    const title = card?.title || fallbackTitle || cardId;
    const canCompleteReview = isReviewTodoCard(card);
    const shortCardId = cardId.slice(0, 8);
    const kindLabel =
      card?.kind === "todo"
        ? t("任务")
        : card?.kind === "ongoing"
          ? t("定时")
          : card?.kind === "discussion"
            ? t("讨论")
            : t("卡片");

    return (
      <div
        ref={ref}
        role="button"
        tabIndex={0}
        onClick={handleClick}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            handleClick(e as unknown as React.MouseEvent);
          }
        }}
        className={cn(
          chatCardVariants({ variant, size }),
          "my-2 not-prose cursor-pointer",
          className,
        )}
        {...props}
      >
        {/*
         * 响应式单树双布局：
         * - 手机端（< sm）：上下两层，上层给标题（最多 2 行）与清洗后的摘要释放接近整卡宽度，
         *   下层独立承载状态徽标/状态点与待确认「完成」快捷按钮，避免窄屏挤压标题；
         * - 桌面端（>= sm）：通过 sm:contents 展开为原有 4 列紧凑横向网格，
         *   避免重复渲染按钮 DOM 破坏无障碍与自动化选择器唯一性。
         */}
        <div className="flex flex-col sm:grid sm:grid-cols-[auto_minmax(0,auto)_minmax(max-content,1fr)_auto] sm:items-start">
          {/* 上层：图标 + 标题与摘要 */}
          <div className="flex items-start gap-2.5 sm:contents">
            {/* 左侧类型图标独立微底座 */}
            <div className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg border border-line/60 bg-surface shadow-2xs sm:col-start-1 sm:row-start-1 sm:row-span-2 sm:mr-3 sm:mt-0 sm:size-8">
              {kindIcon}
            </div>

            {/* 标题与清洗后摘要 */}
            <div className="min-w-0 flex-1 sm:contents">
              <h4 className="line-clamp-2 text-sm font-semibold leading-snug text-foreground group-hover/card:text-primary transition-colors sm:col-start-2 sm:row-start-1 sm:line-clamp-none sm:truncate sm:leading-normal">
                {title}
              </h4>

              {cleanDescription ? (
                <p className="mt-1 line-clamp-1 text-xs text-muted-foreground leading-normal sm:col-start-2 sm:col-span-2 sm:row-start-2 sm:mt-0.5 sm:w-0 sm:min-w-full">
                  {cleanDescription}
                </p>
              ) : (
                <div className="mt-0.5 hidden items-center gap-1.5 text-[11px] text-faint sm:col-start-2 sm:col-span-2 sm:row-start-2 sm:flex sm:w-0 sm:min-w-full">
                  <span>{kindLabel}</span>
                  <span>•</span>
                  <span>{shortCardId}</span>
                </div>
              )}
            </div>
          </div>

          {/* 下层：手机端独立状态与操作栏；桌面端通过 sm:contents 归位到标题右侧与最右操作区 */}
          <div
            data-chat-card-footer="true"
            className="mt-2 flex items-center justify-between gap-2 border-t border-line/50 pt-2 sm:contents"
          >
            {/* 状态徽标与状态点（无 card 兜底时展示种类与短 ID） */}
            <div className="flex min-w-0 items-center gap-1.5 sm:col-start-3 sm:row-start-1 sm:ml-2 sm:self-center sm:justify-self-start">
              {card ? (
                <>
                  {statusBadge}
                  {visualStatus !== "idle" && (
                    <CardStatusDot
                      status={visualStatus}
                      className="size-1.5"
                    />
                  )}
                  {!cleanDescription && (
                    <span className="truncate text-[11px] text-faint sm:hidden">
                      {kindLabel} • {shortCardId}
                    </span>
                  )}
                </>
              ) : (
                <div className="flex items-center gap-1.5 text-[11px] text-faint sm:hidden">
                  <span>{kindLabel}</span>
                  <span>•</span>
                  <span>{shortCardId}</span>
                </div>
              )}
            </div>

            {/* 操作区：待确认完成快捷按钮 + 桌面端跳转指示箭头 */}
            <div className="flex shrink-0 items-center gap-1.5 sm:col-start-4 sm:row-start-1 sm:row-span-2 sm:ml-3 sm:pt-0.5">
              {canCompleteReview && card && (
                <button
                  type="button"
                  data-chat-card-complete="true"
                  disabled={completeReview.isPending}
                  title={t("验收完成")}
                  aria-label={t("验收完成")}
                  onClick={(e) => {
                    e.stopPropagation();
                    e.preventDefault();
                    if (completeReview.isPending) return;
                    completeReview.mutate({
                      id: card.id,
                      projectID: card.project_id || projectId,
                    });
                  }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.stopPropagation();
                    }
                  }}
                  className="inline-flex min-h-7 shrink-0 items-center gap-1 rounded-full border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1 text-xs font-medium leading-none whitespace-nowrap text-emerald-700 transition-colors hover:bg-emerald-500/20 active:scale-95 disabled:opacity-60 sm:min-h-0 sm:px-2 sm:py-0.5 sm:text-[11px] dark:border-emerald-400/30 dark:bg-emerald-500/15 dark:text-emerald-300 dark:hover:bg-emerald-500/25"
                >
                  {completeReview.isPending ? (
                    <Loader2 className="size-3 animate-spin" />
                  ) : (
                    <CheckCircle2 className="size-3" />
                  )}
                  <span>{t("完成")}</span>
                </button>
              )}
              <div className="hidden text-muted-foreground/50 group-hover/card:text-primary group-hover/card:translate-x-0.5 group-hover/card:-translate-y-0.5 transition-all sm:block">
                <ArrowUpRight className="size-4" />
              </div>
            </div>
          </div>
        </div>
      </div>
    );
  },
);
ChatActionCard.displayName = "ChatActionCard";
