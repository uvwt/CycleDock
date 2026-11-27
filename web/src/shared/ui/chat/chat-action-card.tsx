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
        default: "px-3 py-2.5 sm:p-3 w-full max-w-lg text-sm",
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
         * - 手机端（< sm）：上轻下稳，顶栏放轻量图标/状态胶囊与右侧快捷操作（普通卡放淡箭头平衡视觉），
         *   下方 100% 整卡宽度留给主标题（最多 2 行）与清洗后摘要，无分割线、左轴统一对齐；
         * - 桌面端（>= sm）：通过 sm:contents 展开为原有 4 列紧凑横向网格，
         *   避免重复渲染按钮 DOM 破坏无障碍与自动化选择器唯一性。
         */}
        <div className="flex flex-col sm:grid sm:grid-cols-[auto_minmax(0,auto)_minmax(max-content,1fr)_auto] sm:items-start">
          {/* 手机端顶栏：轻量元信息 + 右侧操作；桌面端通过 sm:contents 归位到各自列 */}
          <div
            data-chat-card-header="true"
            className="flex items-center justify-between gap-2 sm:contents"
          >
            <div className="flex min-w-0 items-center gap-1.5 sm:contents">
              {/* 类型图标：手机端轻量化直接呈现在元信息行首，桌面端保留独立微底座 */}
              <div className="inline-flex shrink-0 items-center justify-center sm:col-start-1 sm:row-start-1 sm:row-span-2 sm:mr-3 sm:grid sm:size-8 sm:place-items-center sm:rounded-lg sm:border sm:border-line/60 sm:bg-surface sm:shadow-2xs">
                {kindIcon}
              </div>

              {/* 状态徽标与非空闲状态点 */}
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
                  </>
                ) : (
                  <span className="truncate text-[11px] font-medium text-muted-foreground sm:hidden">
                    {kindLabel}
                  </span>
                )}
              </div>
            </div>

            {/* 操作区：待确认完成快捷按钮 + 跳转指示箭头（手机端普通卡保留淡箭头做视觉平衡，桌面端始终显示） */}
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
                  className="inline-flex min-h-[26px] shrink-0 items-center gap-1 rounded-full border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-0.5 text-[11px] font-medium leading-none whitespace-nowrap text-emerald-700 transition-colors hover:bg-emerald-500/20 active:scale-95 disabled:opacity-60 sm:min-h-0 sm:px-2 dark:border-emerald-400/30 dark:bg-emerald-500/15 dark:text-emerald-300 dark:hover:bg-emerald-500/25"
                >
                  {completeReview.isPending ? (
                    <Loader2 className="size-3 animate-spin" />
                  ) : (
                    <CheckCircle2 className="size-3" />
                  )}
                  <span>{t("完成")}</span>
                </button>
              )}
              <div
                className={cn(
                  "text-muted-foreground/45 group-hover/card:text-primary group-hover/card:translate-x-0.5 group-hover/card:-translate-y-0.5 transition-all sm:text-muted-foreground/50",
                  canCompleteReview ? "hidden sm:block" : "block",
                )}
              >
                <ArrowUpRight className="size-3.5 sm:size-4" />
              </div>
            </div>
          </div>

          {/* 手机端主内容区：整卡宽度承载标题与摘要，重心下沉更稳；桌面端归位到中间信息列 */}
          <div className="mt-2 min-w-0 sm:contents">
            <h4 className="line-clamp-2 text-sm font-semibold leading-snug text-foreground group-hover/card:text-primary transition-colors sm:col-start-2 sm:row-start-1 sm:line-clamp-none sm:truncate sm:leading-normal">
              {title}
            </h4>

            {cleanDescription ? (
              <p className="mt-0.5 line-clamp-1 text-xs text-muted-foreground leading-normal sm:col-start-2 sm:col-span-2 sm:row-start-2 sm:w-0 sm:min-w-full">
                {cleanDescription}
              </p>
            ) : (
              <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-faint sm:col-start-2 sm:col-span-2 sm:row-start-2 sm:w-0 sm:min-w-full">
                <span>{kindLabel}</span>
                <span>•</span>
                <span>{shortCardId}</span>
              </div>
            )}
          </div>
        </div>
      </div>
    );
  },
);
ChatActionCard.displayName = "ChatActionCard";
