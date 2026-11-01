import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "./dropdown-menu";
import {
  Compass,
  MessageCircle,
  BriefcaseBusiness,
  CalendarClock,
  Activity,
  Cpu,
} from "lucide-react";
import { useProject } from "../../app/project-context";

export function MobileGlobalNavMenu() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { projectID } = useProject();

  const navItems = [
    {
      to: projectID ? `/chat?project=${encodeURIComponent(projectID)}` : "/chat",
      label: t("聊天"),
      icon: MessageCircle,
    },
    {
      to: projectID ? `/board/${encodeURIComponent(projectID)}` : "/board",
      label: t("工作"),
      icon: BriefcaseBusiness,
    },
    {
      to: projectID ? `/plans/${encodeURIComponent(projectID)}` : "/plans",
      label: t("计划"),
      icon: CalendarClock,
    },
    {
      to: projectID ? `/activity/${encodeURIComponent(projectID)}` : "/activity",
      label: t("活动"),
      icon: Activity,
    },
    {
      to: "/nexusdock",
      label: t("远程设备"),
      icon: Cpu,
    },
  ];

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={t("切换页面")}
          title={t("切换页面")}
          className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg border border-line bg-surface/90 text-muted-foreground shadow-sm backdrop-blur transition hover:bg-muted hover:text-foreground data-[state=open]:bg-muted data-[state=open]:text-foreground md:hidden"
        >
          <Compass className="size-4" />
        </button>
      </DropdownMenuTrigger>

      <DropdownMenuContent
        side="bottom"
        align="start"
        sideOffset={8}
        collisionPadding={12}
        className="w-48 p-1.5 shadow-lg rounded-xl"
      >
        <DropdownMenuLabel className="px-2.5 py-1 text-xs font-semibold text-muted-foreground">
          {t("页面导航")}
        </DropdownMenuLabel>
        <DropdownMenuSeparator className="my-1" />
        <DropdownMenuGroup className="space-y-0.5">
          {navItems.map((item) => {
            const Icon = item.icon;
            return (
              <DropdownMenuItem
                key={item.label}
                onSelect={() => navigate(item.to)}
                className="flex cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm text-secondary-foreground hover:bg-muted hover:text-heading"
              >
                <Icon className="size-4 shrink-0 text-muted-foreground" />
                <span>{item.label}</span>
              </DropdownMenuItem>
            );
          })}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
