import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2, RotateCcw, Sparkles } from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import type { CardKind, ModelRoute, Provider } from "../../shared/api/types";
import { cn } from "../../shared/lib/utils";
import { ProviderIcon } from "../models/provider-logos";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../../shared/ui/dropdown-menu";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "../../shared/ui/sheet";
import { ConfirmDialog } from "../../shared/ui/components";

const providerLabels: Record<Provider, string> = {
  chatgpt: "ChatGPT",
  grok: "Grok",
  openai_compatible: "自定义 API",
};

/** 匹配视口是否为移动端，决定重试模型选择使用底部抽屉还是下拉菜单。 */
function useIsMobileViewport() {
  const [isMobile, setIsMobile] = useState(() =>
    typeof window !== "undefined" && window.matchMedia
      ? window.matchMedia("(max-width: 767px)").matches
      : false,
  );

  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const media = window.matchMedia("(max-width: 767px)");
    const handler = (event: MediaQueryListEvent) => setIsMobile(event.matches);
    media.addEventListener("change", handler);
    return () => media.removeEventListener("change", handler);
  }, []);

  return isMobile;
}

/** 根据卡片类型推导工作区默认路由，与后端模型路由优先级保持一致。 */
function resolveDefaultRoute(
  routes: ModelRoute[],
  cardKind?: CardKind,
  cardPreferredRouteID?: string | null,
  settings?: {
    default_model_route_id?: string;
    discussion_model_route_id?: string;
    plan_model_route_id?: string;
  },
): ModelRoute | undefined {
  if (cardPreferredRouteID) {
    const matched = routes.find((r) => r.id === cardPreferredRouteID);
    if (matched) return matched;
  }
  const kindDefaultID =
    cardKind === "discussion"
      ? settings?.discussion_model_route_id
      : cardKind === "ongoing"
        ? settings?.plan_model_route_id
        : undefined;
  const targetID = kindDefaultID || settings?.default_model_route_id;
  if (targetID) {
    const matched = routes.find((r) => r.id === targetID);
    if (matched) return matched;
  }
  return routes[0];
}

function getRouteSubLabel(route: ModelRoute, t: (key: string) => string) {
  if (route.api_credential_name?.trim()) {
    return route.api_credential_name.trim();
  }
  return t(providerLabels[route.provider_type] || route.provider_type);
}

export interface BubbleRetryMenuProps {
  /** 气泡对应的回复模型名称，用于在菜单中高亮“上次使用”。 */
  replyModel?: string;
  /** 当前卡片类型，用于提示默认模型解析结果。 */
  cardKind?: CardKind;
  /** 当前卡片偏好模型路由 ID。 */
  cardPreferredRouteID?: string | null;
  /** 触发按钮对齐方向（人类气泡右对齐，AI 气泡左对齐）。 */
  align?: "start" | "end";
  /** 当前是否处于历史中间轮次（重跑会截断其后消息，触发前需要二次确认）。 */
  willTruncateFollowing?: boolean;
  /** 禁用原因或禁用态（例如已有正在进行的重试或当前卡片正在运行）。 */
  disabled?: boolean;
  /** 当前按钮自身是否处于提交 loading 中。 */
  loading?: boolean;
  /** 自定义触发器渲染（用于适配 ChatBubbleAction 或失败执行行的重试按钮）。 */
  renderTrigger: (props: {
    onClick?: () => void;
    disabled: boolean;
    loading: boolean;
    open: boolean;
    title: string;
    icon: ReactNode;
  }) => ReactNode;
  /** 选定模型路由（null 表示使用当前默认/原默认路由）后执行重试。 */
  onConfirmRetry: (preferredModelRouteID: string | null) => Promise<void> | void;
  /** 菜单打开状态变化时回调，方便移动端维持气泡操作栏可见。 */
  onOpenChange?: (open: boolean) => void;
}

/**
 * 气泡与失败执行通用的「重试 / 切换模型重新回答」菜单。
 * - 当工作区仅存在 0~1 条可选模型路由（或非 owner 无法拉取路由列表）时，点击直接按默认模型触发重试；
 * - 存在多个可用模型路由时，桌面端展开 DropdownMenu，手机端弹出底部 Sheet；
 * - 若重跑位于历史中间轮次，会先弹出二次确认框提示将清空该点之后的对话记录。
 */
export function BubbleRetryMenu({
  replyModel,
  cardKind,
  cardPreferredRouteID,
  align = "start",
  willTruncateFollowing = false,
  disabled = false,
  loading = false,
  renderTrigger,
  onConfirmRetry,
  onOpenChange,
}: BubbleRetryMenuProps) {
  const { t } = useTranslation();
  const isMobile = useIsMobileViewport();
  const [menuOpen, setMenuOpen] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [pendingRouteID, setPendingRouteID] = useState<string | null>(null);
  const [pendingRouteLabel, setPendingRouteLabel] = useState<string>("");
  const [submitting, setSubmitting] = useState(false);

  const me = useQuery({ queryKey: keys.me, queryFn: api.me });
  const owner = me.data?.role === "owner";

  const agentQuery = useQuery({
    queryKey: keys.agent,
    queryFn: api.agentSettings,
    enabled: owner,
    staleTime: 30_000,
  });

  const enabledRoutes = useMemo(
    () => (agentQuery.data?.routes || []).filter((route) => route.enabled),
    [agentQuery.data?.routes],
  );

  const defaultRoute = useMemo(
    () =>
      resolveDefaultRoute(
        enabledRoutes,
        cardKind,
        cardPreferredRouteID,
        agentQuery.data,
      ),
    [enabledRoutes, cardKind, cardPreferredRouteID, agentQuery.data],
  );

  // 非 owner 或可用路由不超过 1 条时，免去多余的选单步骤，点击即按默认模型重试。
  const hasMultipleRoutes = owner && enabledRoutes.length > 1;
  const isBusy = loading || submitting;
  const isDisabled = disabled || isBusy;

  const triggerTitle = isBusy
    ? t("正在重新生成...")
    : hasMultipleRoutes
      ? t("切换模型或重新回答")
      : t("重新回答");

  const triggerIcon = isBusy ? (
    <Loader2 className="size-3.5 animate-spin" />
  ) : (
    <RotateCcw className="size-3.5" />
  );

  function setOpenState(next: boolean) {
    setMenuOpen(next);
    onOpenChange?.(next);
  }

  async function executeRetry(routeID: string | null) {
    if (isBusy) return;
    setSubmitting(true);
    try {
      await onConfirmRetry(routeID);
      setConfirmOpen(false);
    } finally {
      setSubmitting(false);
    }
  }

  function handleChooseRoute(routeID: string | null, routeLabel?: string) {
    setOpenState(false);
    if (willTruncateFollowing) {
      setPendingRouteID(routeID);
      setPendingRouteLabel(routeLabel || "");
      setConfirmOpen(true);
      return;
    }
    void executeRetry(routeID);
  }

  const normalizedReplyModel = replyModel?.trim().toLowerCase() || "";
  const defaultOptionHint =
    replyModel?.trim() || defaultRoute?.model || undefined;

  const confirmDescription = pendingRouteLabel
    ? t(
        "将使用 {{model}} 重新生成该轮回答，并清空此消息之后的后续对话记录。",
        { model: pendingRouteLabel },
      )
    : t("将重新生成该轮回答，并清空此消息之后的后续对话记录。");

  // 仅单模型或非 owner 时直接渲染普通按钮，点击即可重试（必要时仍弹截断确认）
  if (!hasMultipleRoutes) {
    return (
      <>
        {renderTrigger({
          onClick: () => {
            if (isDisabled) return;
            handleChooseRoute(null, defaultOptionHint);
          },
          disabled: isDisabled,
          loading: isBusy,
          open: false,
          title: triggerTitle,
          icon: triggerIcon,
        })}
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={(next) => {
            if (!submitting) setConfirmOpen(next);
          }}
          title={t("确认重新生成回答？")}
          description={confirmDescription}
          confirmLabel={t("重新回答")}
          pending={submitting}
          onConfirm={() => {
            void executeRetry(pendingRouteID);
          }}
        />
      </>
    );
  }

  // 手机端：底部 Sheet，增大点击热区并避免小屏弹出菜单溢出裁切
  if (isMobile) {
    return (
      <>
        {renderTrigger({
          onClick: () => {
            if (!isDisabled) setOpenState(true);
          },
          disabled: isDisabled,
          loading: isBusy,
          open: menuOpen,
          title: triggerTitle,
          icon: triggerIcon,
        })}
        <Sheet open={menuOpen} onOpenChange={setOpenState}>
          <SheetContent
            side="bottom"
            className="gap-0 rounded-t-2xl border-line bg-surface p-0 pb-[max(1rem,env(safe-area-inset-bottom,0px))]"
          >
            <SheetHeader className="border-b border-line/60 px-4 py-3 text-left">
              <SheetTitle className="text-sm font-semibold">
                {t("选择模型重新回答")}
              </SheetTitle>
              <SheetDescription className="text-xs text-muted-foreground">
                {willTruncateFollowing
                  ? t("仅对本次重试生效，确认后将重新生成该轮并清空后续消息。")
                  : t("仅对本次重试生效，不会修改当前卡片默认模型。")}
              </SheetDescription>
            </SheetHeader>
            <div className="max-h-[60vh] overflow-y-auto px-2 py-2 space-y-1">
              <button
                type="button"
                data-retry-model-option="default"
                onClick={() => handleChooseRoute(null, defaultOptionHint)}
                className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-muted active:bg-muted"
              >
                <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                  <Sparkles className="size-4" />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium text-foreground">
                    {t("使用当前默认重试")}
                  </div>
                  {defaultOptionHint && (
                    <div className="truncate text-xs text-muted-foreground">
                      {replyModel
                        ? t("上次使用：{{model}}", { model: replyModel })
                        : defaultOptionHint}
                    </div>
                  )}
                </div>
              </button>

              <div className="my-1.5 border-t border-line/60" />
              <div className="px-3 py-1 text-[11px] font-medium text-faint">
                {t("切换指定模型")}
              </div>

              {enabledRoutes.map((route) => {
                const isLastUsed =
                  Boolean(normalizedReplyModel) &&
                  route.model.trim().toLowerCase() === normalizedReplyModel;
                const subLabel = getRouteSubLabel(route, t);
                return (
                  <button
                    key={route.id}
                    type="button"
                    data-retry-model-option={route.id}
                    onClick={() => handleChooseRoute(route.id, route.model)}
                    className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-muted active:bg-muted"
                  >
                    <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-line/60 bg-canvas">
                      <ProviderIcon
                        provider={route.provider_type}
                        className="size-4"
                      />
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5">
                        <span className="truncate text-sm font-medium text-foreground">
                          {route.model}
                        </span>
                        {isLastUsed && (
                          <span className="shrink-0 rounded-full bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">
                            {t("上次模型")}
                          </span>
                        )}
                      </div>
                      <div className="truncate text-xs text-muted-foreground">
                        {subLabel}
                      </div>
                    </div>
                  </button>
                );
              })}
            </div>
          </SheetContent>
        </Sheet>
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={(next) => {
            if (!submitting) setConfirmOpen(next);
          }}
          title={t("确认重新生成回答？")}
          description={confirmDescription}
          confirmLabel={t("重新回答")}
          pending={submitting}
          onConfirm={() => {
            void executeRetry(pendingRouteID);
          }}
        />
      </>
    );
  }

  // 桌面端：紧凑 DropdownMenu
  return (
    <>
      <DropdownMenu open={menuOpen} onOpenChange={setOpenState}>
        <DropdownMenuTrigger asChild disabled={isDisabled}>
          {renderTrigger({
            disabled: isDisabled,
            loading: isBusy,
            open: menuOpen,
            title: triggerTitle,
            icon: triggerIcon,
          })}
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align={align}
          sideOffset={6}
          collisionPadding={12}
          className="w-64 max-w-[calc(100vw-1.5rem)] rounded-xl border border-line/80 bg-surface/95 p-1 shadow-lg backdrop-blur-md"
        >
          <DropdownMenuItem
            data-retry-model-option="default"
            onSelect={() => handleChooseRoute(null, defaultOptionHint)}
            className="group flex cursor-pointer items-center gap-2 rounded-lg px-2.5 py-1.5 text-xs outline-none transition-colors hover:bg-muted/70 focus:bg-muted/70"
          >
            <Sparkles className="size-3.5 shrink-0 text-primary" />
            <div className="min-w-0 flex-1">
              <div className="truncate font-medium text-foreground">
                {t("使用当前默认重试")}
              </div>
              {defaultOptionHint && (
                <div className="truncate text-[11px] text-faint">
                  {replyModel
                    ? t("上次使用：{{model}}", { model: replyModel })
                    : defaultOptionHint}
                </div>
              )}
            </div>
          </DropdownMenuItem>

          <DropdownMenuSeparator className="my-1 bg-line/60" />
          <DropdownMenuLabel className="px-2.5 py-1 text-[10px] font-medium text-faint">
            {t("切换指定模型")}
          </DropdownMenuLabel>

          <div className="max-h-60 overflow-y-auto space-y-0.5">
            {enabledRoutes.map((route) => {
              const isLastUsed =
                Boolean(normalizedReplyModel) &&
                route.model.trim().toLowerCase() === normalizedReplyModel;
              const subLabel = getRouteSubLabel(route, t);
              return (
                <DropdownMenuItem
                  key={route.id}
                  data-retry-model-option={route.id}
                  onSelect={() => handleChooseRoute(route.id, route.model)}
                  title={`${route.model} (${subLabel})`}
                  className={cn(
                    "group flex cursor-pointer items-center gap-2 rounded-lg px-2.5 py-1.5 text-xs outline-none transition-colors hover:bg-muted/70 focus:bg-muted/70",
                    isLastUsed && "bg-muted/50",
                  )}
                >
                  <ProviderIcon
                    provider={route.provider_type}
                    className="size-3.5 shrink-0"
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-1.5">
                      <span className="truncate font-medium text-foreground">
                        {route.model}
                      </span>
                      {isLastUsed && (
                        <span className="shrink-0 rounded bg-primary/10 px-1 py-0.5 text-[10px] font-medium leading-none text-primary">
                          {t("上次模型")}
                        </span>
                      )}
                    </div>
                    <div className="truncate text-[11px] text-faint">
                      {subLabel}
                    </div>
                  </div>
                </DropdownMenuItem>
              );
            })}
          </div>
        </DropdownMenuContent>
      </DropdownMenu>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={(next) => {
          if (!submitting) setConfirmOpen(next);
        }}
        title={t("确认重新生成回答？")}
        description={confirmDescription}
        confirmLabel={t("重新回答")}
        pending={submitting}
        onConfirm={() => {
          void executeRetry(pendingRouteID);
        }}
      />
    </>
  );
}
