import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Sparkles, Check, ChevronDown } from "lucide-react";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import { useModelSelection } from "./model-selection";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../../shared/ui/dropdown-menu";

export function ModelMenu({ className }: { className?: string } = {}) {
  const { selectedRouteId, selectRouteId } = useModelSelection();
  const [open, setOpen] = useState(false);

  const agentQuery = useQuery({
    queryKey: keys.agent,
    queryFn: api.agentSettings,
    staleTime: 30_000,
  });

  const routes = (agentQuery.data?.routes || []).filter((r) => r.enabled);
  const selectedRoute = routes.find((r) => r.id === selectedRouteId);

  // 如果选中的 route 不在可用列表里，但在展示时需友好提示或降级
  const label = selectedRoute ? selectedRoute.model : "默认模型";

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger
        className={`inline-flex items-center gap-1.5 rounded-full border border-border/70 bg-background/80 px-2.5 py-1 text-xs font-medium text-foreground transition-all duration-150 hover:bg-accent/40 active:scale-95 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring ${className || ""}`}
        title="选择对话大语言模型"
        aria-label="选择模型"
      >
        <Sparkles className="h-3.5 w-3.5 text-primary shrink-0" />
        <span className="max-w-[120px] truncate sm:max-w-[160px]">{label}</span>
        <ChevronDown className="h-3 w-3 opacity-60 shrink-0" />
      </DropdownMenuTrigger>

      <DropdownMenuContent align="end" className="w-56 p-1 text-xs">
        <DropdownMenuLabel className="px-2 py-1 text-[11px] font-semibold text-muted-foreground">
          模型偏好
        </DropdownMenuLabel>

        <DropdownMenuItem
          className="flex items-center justify-between px-2 py-1.5 cursor-pointer rounded-sm"
          onClick={() => selectRouteId(null)}
        >
          <div className="flex flex-col">
            <span className="font-medium text-foreground">跟随默认路由</span>
            <span className="text-[10px] text-muted-foreground">
              按工作区配置优先级与容错
            </span>
          </div>
          {selectedRouteId === null && (
            <Check className="h-3.5 w-3.5 text-primary shrink-0 ml-2" />
          )}
        </DropdownMenuItem>

        {routes.length > 0 && <DropdownMenuSeparator className="my-1" />}

        {routes.map((route) => {
          const isSelected = selectedRouteId === route.id;
          return (
            <DropdownMenuItem
              key={route.id}
              className="flex items-center justify-between px-2 py-1.5 cursor-pointer rounded-sm"
              onClick={() => selectRouteId(route.id)}
            >
              <div className="flex flex-col truncate pr-2">
                <span className="font-medium text-foreground truncate">
                  {route.model}
                </span>
                <span className="text-[10px] text-muted-foreground uppercase">
                  {route.provider_type}
                </span>
              </div>
              {isSelected && (
                <Check className="h-3.5 w-3.5 text-primary shrink-0" />
              )}
            </DropdownMenuItem>
          );
        })}

        {routes.length === 0 && !agentQuery.isLoading && (
          <div className="px-2 py-2 text-center text-[11px] text-muted-foreground">
            工作区暂无已启用的模型路由
          </div>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
