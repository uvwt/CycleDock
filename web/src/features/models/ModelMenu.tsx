import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Sparkles, Check, ChevronDown, Settings } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import { cn } from "../../shared/lib/utils";
import { useModelSelection } from "./model-selection";
import { ProviderIcon } from "./provider-logos";
import type { Provider } from "../../shared/api/types";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../../shared/ui/dropdown-menu";

const providerLabels: Record<Provider, string> = {
  chatgpt: "ChatGPT",
  grok: "Grok",
  openai_compatible: "自定义 API",
};

export interface ModelMenuProps {
  className?: string;
  variant?: "composer" | "toolbar";
  align?: "start" | "end" | "center";
  cardID?: string;
  projectID?: string;
  selectedRouteId?: string | null;
  onSelectRouteId?: (routeId: string | null) => void;
}

export function ModelMenu({
  className,
  variant = "composer",
  align,
  cardID,
  projectID,
  selectedRouteId: controlledRouteId,
  onSelectRouteId,
}: ModelMenuProps = {}) {
  const fallback = useModelSelection();
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);

  const me = useQuery({ queryKey: keys.me, queryFn: api.me });
  const owner = me.data?.role === "owner";

  const agentQuery = useQuery({
    queryKey: keys.agent,
    queryFn: api.agentSettings,
    staleTime: 30_000,
  });

  const routes = (agentQuery.data?.routes || []).filter((r) => r.enabled);

  // 优先级：外部受控指定 > Context 草稿 > null (跟随默认)
  const currentRouteId =
    controlledRouteId !== undefined
      ? controlledRouteId
      : (fallback?.selectedRouteId ?? null);

  const selectedRoute = routes.find((r) => r.id === currentRouteId);

  // 解析当前工作区默认路由，为“跟随默认路由”选项提供明确上下文提示
  const defaultRouteId =
    agentQuery.data?.discussion_model_route_id ||
    agentQuery.data?.default_model_route_id;
  const defaultRoute =
    routes.find((r) => r.id === defaultRouteId) || routes[0];

  const label = selectedRoute ? selectedRoute.model : t("默认模型");

  const setModelMutation = useMutation({
    mutationFn: (routeId: string | null) => {
      if (!cardID) return Promise.resolve(null);
      return api.setCardModelRoute(cardID, routeId);
    },
    onSuccess: (updatedCard) => {
      if (!cardID) return;
      queryClient.setQueryData(keys.card(cardID), (old: any) => {
        if (!old) return old;
        return {
          ...old,
          card: {
            ...old.card,
            preferred_model_route_id: updatedCard
              ? updatedCard.preferred_model_route_id
              : null,
          },
        };
      });
      if (projectID) {
        queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
        queryClient.invalidateQueries({ queryKey: keys.mainDiscussion(projectID) });
      }
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("更新卡片模型失败"));
    },
  });

  const handleSelectRoute = (routeId: string | null) => {
    if (onSelectRouteId) {
      onSelectRouteId(routeId);
    } else if (fallback?.selectRouteId) {
      fallback.selectRouteId(routeId);
    }
    if (cardID) {
      setModelMutation.mutate(routeId);
    }
  };

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        {variant === "composer" ? (
          <button
            type="button"
            aria-label={t("选择模型")}
            title={
              selectedRoute
                ? t("当前指定模型：{{model}}", { model: selectedRoute.model })
                : defaultRoute
                  ? t("跟随默认路由（当前解析：{{model}}）", {
                      model: defaultRoute.model,
                    })
                  : t("跟随默认路由")
            }
            className={cn(
              "group inline-flex h-7 items-center gap-1.5 rounded-lg border border-line/60 bg-surface/70 px-2 text-xs text-muted-foreground transition hover:border-line hover:bg-surface hover:text-foreground active:scale-95 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring data-[state=open]:border-line data-[state=open]:bg-surface data-[state=open]:text-foreground",
              className,
            )}
          >
            {selectedRoute ? (
              <ProviderIcon
                provider={selectedRoute.provider_type}
                className="size-3 text-muted-foreground group-hover:text-foreground shrink-0"
              />
            ) : (
              <Sparkles className="size-3 text-primary shrink-0" />
            )}
            <span className="max-w-[120px] truncate sm:max-w-[180px] font-medium">
              {label}
            </span>
            <ChevronDown className="size-3 text-faint transition-transform duration-200 group-data-[state=open]:rotate-180 shrink-0" />
          </button>
        ) : (
          <button
            type="button"
            aria-label={t("选择模型")}
            title={
              selectedRoute
                ? t("当前指定模型：{{model}}", { model: selectedRoute.model })
                : defaultRoute
                  ? t("跟随默认路由（当前解析：{{model}}）", {
                      model: defaultRoute.model,
                    })
                  : t("跟随默认路由")
            }
            className={cn(
              "group inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg border border-line bg-surface px-2.5 text-xs leading-none text-muted-foreground shadow-xs transition hover:bg-muted hover:text-foreground data-[state=open]:bg-muted data-[state=open]:text-foreground",
              className,
            )}
          >
            {selectedRoute ? (
              <ProviderIcon
                provider={selectedRoute.provider_type}
                className="size-3.5 text-muted-foreground group-hover:text-foreground shrink-0"
              />
            ) : (
              <Sparkles className="size-3.5 text-primary shrink-0" />
            )}
            <span className="max-w-20 truncate sm:max-w-36 font-medium">
              {label}
            </span>
            <ChevronDown className="size-3 text-faint transition-transform duration-200 group-data-[state=open]:rotate-180 shrink-0" />
          </button>
        )}
      </DropdownMenuTrigger>

      <DropdownMenuContent
        align={align || (variant === "composer" ? "start" : "end")}
        sideOffset={6}
        collisionPadding={12}
        className="w-60 max-w-[calc(100vw-1.5rem)] rounded-xl border border-line/80 bg-surface/95 p-1 shadow-lg backdrop-blur-md"
      >
        {/* 跟随工作区默认路由 */}
        <DropdownMenuItem
          onSelect={() => handleSelectRoute(null)}
          title={
            defaultRoute
              ? t("跟随默认路由（当前解析：{{model}}）", {
                  model: defaultRoute.model,
                })
              : t("跟随默认路由")
          }
          className={cn(
            "group flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-xs cursor-pointer outline-none transition-colors",
            currentRouteId === null
              ? "bg-muted font-medium text-foreground"
              : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
          )}
        >
          <Sparkles
            className={cn(
              "size-3.5 shrink-0 transition-colors",
              currentRouteId === null
                ? "text-primary"
                : "text-muted-foreground group-hover:text-foreground",
            )}
          />
          <div className="flex flex-1 items-center justify-between min-w-0 gap-2">
            <span className="truncate">{t("跟随默认路由")}</span>
            {defaultRoute && (
              <span className="text-[11px] text-faint truncate max-w-[90px] font-normal">
                {defaultRoute.model}
              </span>
            )}
          </div>
          {currentRouteId === null && (
            <Check className="size-3.5 text-primary shrink-0 ml-1" />
          )}
        </DropdownMenuItem>

        {/* 指定模型列表 */}
        {routes.length > 0 && (
          <>
            <DropdownMenuSeparator className="my-1 bg-line/60" />
            <div className="max-h-56 overflow-y-auto space-y-0.5">
              {routes.map((route) => {
                const isSelected = currentRouteId === route.id;
                return (
                  <DropdownMenuItem
                    key={route.id}
                    onSelect={() => handleSelectRoute(route.id)}
                    title={`${route.model} (${providerLabels[route.provider_type] || route.provider_type})`}
                    className={cn(
                      "group flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-xs cursor-pointer outline-none transition-colors",
                      isSelected
                        ? "bg-muted font-medium text-foreground"
                        : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                    )}
                  >
                    <ProviderIcon
                      provider={route.provider_type}
                      className={cn(
                        "size-3.5 shrink-0 transition-colors",
                        isSelected
                          ? "text-foreground"
                          : "text-muted-foreground group-hover:text-foreground",
                      )}
                    />
                    <span className="flex-1 truncate text-foreground">
                      {route.model}
                    </span>
                    {isSelected && (
                      <Check className="size-3.5 text-primary shrink-0 ml-1" />
                    )}
                  </DropdownMenuItem>
                );
              })}
            </div>
          </>
        )}

        {routes.length === 0 && !agentQuery.isLoading && (
          <div className="px-3 py-3 text-center text-xs text-muted-foreground">
            {t("工作区暂无已启用的模型路由")}
            <p className="mt-0.5 text-[11px] text-faint">
              {t("可在模型配置中添加提供商与路由")}
            </p>
          </div>
        )}

        {owner && (
          <>
            <DropdownMenuSeparator className="my-1 bg-line/60" />
            <DropdownMenuItem
              onSelect={() => navigate("/settings/models")}
              className="flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-xs text-muted-foreground hover:bg-muted/60 hover:text-foreground cursor-pointer outline-none transition-colors"
            >
              <Settings className="size-3.5 shrink-0 text-muted-foreground" />
              <span>{t("配置模型路由与提供商")}</span>
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
