import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import {
  ExternalLink,
  CheckCircle2,
  Clock,
  Sparkles,
  ChevronRight,
  FileQuestion,
  Loader2,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { cn } from "@/shared/lib/utils";
import { api } from "@/shared/api/client";
import { keys } from "@/app/query";
import type { Card } from "@/shared/api/types";
import { CardStatusDot, getCardVisualStatus } from "@/shared/ui/CardStatusDot";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/shared/ui/sheet";

const chatCardVariants = cva(
  "group/card block select-none rounded-xl border transition-all duration-150 text-left overflow-hidden outline-none focus-visible:ring-2 focus-visible:ring-primary/40",
  {
    variants: {
      variant: {
        default:
          "border-line/80 bg-surface/90 hover:bg-surface hover:border-primary/40 hover:shadow-xs active:scale-[0.99] dark:bg-surface/60 dark:hover:bg-surface/90",
        elevated:
          "border-line bg-surface shadow-xs hover:border-primary/50 hover:shadow-sm active:scale-[0.99]",
        ghost:
          "border-transparent bg-muted/40 hover:bg-muted/70 hover:border-line/60",
      },
      size: {
        sm: "p-2.5 max-w-sm text-xs",
        default: "p-3 max-w-md text-sm",
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
    const [mobileDrawerOpen, setMobileDrawerOpen] = React.useState(false);

    const {
      data: cardDetail,
      isLoading,
      isError,
    } = useQuery({
      queryKey: keys.card(cardId),
      queryFn: () => api.card(cardId),
      enabled: Boolean(cardId),
      staleTime: 1000 * 30,
    });

    const card = cardDetail?.card;

    const isMobile = React.useMemo(() => {
      if (typeof window === "undefined") return false;
      return window.innerWidth < 768;
    }, []);

    const targetProject = card?.project_id || projectId || "current";
    const targetUrl = React.useMemo(() => {
      if (!card) {
        return `/cards/${targetProject}/${cardId}`;
      }
      if (card.kind === "discussion") {
        return `/chat/${targetProject}/${card.id}`;
      }
      return `/cards/${targetProject}/${card.id}`;
    }, [card, targetProject, cardId]);

    const visualStatus = React.useMemo(() => {
      if (!card) return "idle";
      return getCardVisualStatus(card);
    }, [card]);

    const kindIcon = React.useMemo(() => {
      const kind = card?.kind;
      if (kind === "todo") {
        return <CheckCircle2 className="size-4 text-sky-500 shrink-0" />;
      }
      if (kind === "ongoing") {
        return <Clock className="size-4 text-amber-500 shrink-0" />;
      }
      if (kind === "discussion") {
        return <Sparkles className="size-4 text-purple-500 shrink-0" />;
      }
      return <CheckCircle2 className="size-4 text-primary shrink-0" />;
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
            "inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] font-medium border leading-none shrink-0",
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
        onOpenCard(cardId, card);
        return;
      }

      if (isMobile) {
        setMobileDrawerOpen(true);
      } else {
        navigate(targetUrl);
      }
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

    if (isError || (!card && !fallbackTitle)) {
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
    const kindLabel =
      card?.kind === "todo"
        ? t("任务")
        : card?.kind === "ongoing"
          ? t("例行工作")
          : card?.kind === "discussion"
            ? t("讨论")
            : t("卡片");

    return (
      <>
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
            "my-2.5 not-prose cursor-pointer",
            className,
          )}
          {...props}
        >
          <div className="flex items-start justify-between gap-2">
            <div className="flex items-center gap-1.5 min-w-0">
              {kindIcon}
              <span className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">
                {kindLabel}
              </span>
            </div>
            <div className="flex items-center gap-1.5 shrink-0">
              {statusBadge}
              {card && (
                <CardStatusDot
                  status={visualStatus}
                  className="size-2 ml-0.5"
                />
              )}
            </div>
          </div>

          <div className="mt-1.5 flex items-baseline justify-between gap-2">
            <h4 className="font-medium text-foreground text-sm line-clamp-2 leading-snug group-hover/card:text-primary transition-colors">
              {title}
            </h4>
          </div>

          {card?.description && (
            <p className="mt-1 text-xs text-muted-foreground line-clamp-1">
              {card.description}
            </p>
          )}

          <div className="mt-2.5 flex items-center justify-between border-t border-line/60 pt-2 text-[11px] text-muted-foreground">
            <span className="truncate">ID: {cardId.slice(0, 8)}</span>
            <div className="inline-flex items-center gap-1 text-primary group-hover/card:translate-x-0.5 transition-transform font-medium">
              <span>{t("查看详情")}</span>
              <ChevronRight className="size-3" />
            </div>
          </div>
        </div>

        {/* Mobile preview drawer */}
        <Sheet open={mobileDrawerOpen} onOpenChange={setMobileDrawerOpen}>
          <SheetContent side="bottom" className="max-h-[85vh] rounded-t-2xl sm:max-w-md sm:rounded-none">
            <SheetHeader>
              <SheetTitle>{title}</SheetTitle>
              <p className="text-xs text-muted-foreground">
                {kindLabel} · ID: {cardId.slice(0, 8)}
              </p>
            </SheetHeader>
            <div className="space-y-4 px-4 pb-6 pt-2">
              <div className="flex items-center justify-between rounded-lg bg-muted/50 p-3">
                <div className="flex items-center gap-2">
                  {kindIcon}
                  <span className="font-medium text-sm">{kindLabel}</span>
                </div>
                <div className="flex items-center gap-2">
                  {statusBadge}
                  {card && <CardStatusDot status={visualStatus} />}
                </div>
              </div>

              {card?.description && (
                <div className="space-y-1">
                  <span className="text-xs font-semibold text-muted-foreground">
                    {t("描述")}
                  </span>
                  <p className="rounded-lg border border-line bg-surface p-3 text-sm leading-relaxed text-foreground whitespace-pre-wrap">
                    {card.description}
                  </p>
                </div>
              )}

              <div className="flex gap-2 pt-2">
                <button
                  type="button"
                  onClick={() => {
                    setMobileDrawerOpen(false);
                    navigate(targetUrl);
                  }}
                  className="flex-1 inline-flex items-center justify-center gap-2 rounded-xl bg-primary px-4 py-2.5 text-sm font-medium text-primary-foreground shadow-sm active:scale-[0.98] transition"
                >
                  <span>{t("打开卡片详情页")}</span>
                  <ExternalLink className="size-4" />
                </button>
              </div>
            </div>
          </SheetContent>
        </Sheet>
      </>
    );
  },
);
ChatActionCard.displayName = "ChatActionCard";
