import { useMemo, type ReactNode } from "react";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  BellOff,
  BriefcaseBusiness,
  CalendarClock,
  Cpu,
  MessageCircle,
  Pin,
  Settings,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useProject } from "../../app/project-context";
import { keys } from "../../app/query";
import { api } from "../api/client";
import { ProjectSwitcherWithSettings } from "../../features/projects/ProjectSwitcherWithSettings";
import { CardStatusDot, type CardVisualStatus } from "./CardStatusDot";
import { getKindVisualStatus } from "../lib/card-focus";
import { SwipeableRow, type SwipeActionItem } from "./SwipeableRow";
import { Skeleton } from "./skeleton";

function MobileSidebarHeader() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const { projects, projectID, setProjectID } = useProject();

  function switchProject(nextProjectID: string) {
    setProjectID(nextProjectID);
    if (location.pathname.startsWith("/chat")) {
      navigate(`/chat?project=${encodeURIComponent(nextProjectID)}`);
      return;
    }
    if (
      location.pathname.startsWith("/board") ||
      location.pathname.startsWith("/cards/")
    ) {
      navigate(`/board/${encodeURIComponent(nextProjectID)}`);
      return;
    }
    if (location.pathname.startsWith("/plans")) {
      navigate(`/plans/${encodeURIComponent(nextProjectID)}`);
      return;
    }
    if (location.pathname.startsWith("/activity")) {
      navigate(`/activity/${encodeURIComponent(nextProjectID)}`);
    }
  }

  return (
    <div className="flex shrink-0 items-center justify-between gap-2 px-2.5 pt-[calc(env(safe-area-inset-top,0px)+0.5rem)] pb-1 md:hidden">
      <div className="min-w-0 flex-1">
        <ProjectSwitcherWithSettings
          minimal
          projects={projects}
          projectID={projectID}
          onChange={switchProject}
        />
      </div>
      <div className="flex shrink-0 items-center gap-0.5">
        <NavLink
          to="/nexusdock"
          title={t("设备")}
          aria-label={t("设备")}
          className={({ isActive }) =>
            `inline-flex size-8 items-center justify-center rounded-lg transition ${
              isActive
                ? "bg-primary/10 text-primary"
                : "text-muted-foreground hover:bg-muted/70 hover:text-foreground"
            }`
          }
        >
          <Cpu className="size-4" />
        </NavLink>
        <NavLink
          to="/settings/general"
          onClick={() => projectID && setProjectID(projectID)}
          title={t("设置")}
          aria-label={t("设置")}
          className={({ isActive }) =>
            `inline-flex size-8 items-center justify-center rounded-lg transition ${
              isActive
                ? "bg-primary/10 text-primary"
                : "text-muted-foreground hover:bg-muted/70 hover:text-foreground"
            }`
          }
        >
          <Settings className="size-4" />
        </NavLink>
      </div>
    </div>
  );
}

function MobileSidebarFooter() {
  const { t } = useTranslation();
  const location = useLocation();
  const { projectID } = useProject();

  const viewedCardID = useMemo(() => {
    const match = /^\/(?:chat|cards|plans)\/[^/]+\/([^/]+)\/?$/.exec(
      location.pathname,
    );
    if (!match?.[1]) return "";
    try {
      return decodeURIComponent(match[1]);
    } catch {
      return match[1];
    }
  }, [location.pathname]);

  const cardsQuery = useQuery({
    queryKey: keys.cards(projectID),
    queryFn: () => api.cards(projectID),
    enabled: Boolean(projectID),
  });
  const projectCards = useMemo(
    () => cardsQuery.data || [],
    [cardsQuery.data],
  );

  const primaryNav: {
    to: string;
    activePath: string;
    label: string;
    icon: typeof MessageCircle;
    visualStatus?: CardVisualStatus;
  }[] = [
    {
      to: projectID
        ? `/chat?project=${encodeURIComponent(projectID)}`
        : "/chat",
      activePath: "/chat",
      label: t("聊天"),
      icon: MessageCircle,
      visualStatus: getKindVisualStatus(
        projectCards,
        "discussion",
        viewedCardID,
      ),
    },
    {
      to: projectID ? `/board/${encodeURIComponent(projectID)}` : "/board",
      activePath: "/board",
      label: t("任务"),
      icon: BriefcaseBusiness,
      visualStatus: getKindVisualStatus(projectCards, "todo", viewedCardID),
    },
    {
      to: projectID ? `/plans/${encodeURIComponent(projectID)}` : "/plans",
      activePath: "/plans",
      label: t("定时"),
      icon: CalendarClock,
      visualStatus: getKindVisualStatus(projectCards, "ongoing", viewedCardID),
    },
    {
      to: projectID
        ? `/activity/${encodeURIComponent(projectID)}`
        : "/activity",
      activePath: "/activity",
      label: t("活动"),
      icon: Activity,
    },
  ];

  function isNavActive(activePath: string) {
    if (activePath === "/board") {
      return (
        location.pathname.startsWith("/board") ||
        location.pathname.startsWith("/cards/")
      );
    }
    if (activePath === "/plans") {
      return location.pathname.startsWith("/plans");
    }
    if (activePath === "/activity") {
      return location.pathname.startsWith("/activity");
    }
    return location.pathname.startsWith("/chat");
  }

  return (
    <div className="shrink-0 px-2.5 pt-1.5 pb-[max(0.75rem,env(safe-area-inset-bottom,0px))] md:hidden">
      <nav
        aria-label={t("工作区导航")}
        className="grid grid-cols-4 gap-1 rounded-2xl border border-line/70 bg-muted/60 p-1 shadow-2xs"
      >
        {primaryNav.map((item) => {
          const active = isNavActive(item.activePath);
          return (
            <NavLink
              key={item.activePath}
              to={item.to}
              className={`relative flex items-center justify-center gap-1 rounded-xl py-2 text-xs transition-all ${
                active
                  ? "bg-surface font-semibold text-heading shadow-xs ring-1 ring-line/60"
                  : "font-medium text-muted-foreground hover:text-foreground"
              }`}
            >
              <item.icon
                className={`size-3.5 shrink-0 ${
                  active ? "text-primary" : "opacity-75"
                }`}
              />
              <span className="truncate">{item.label}</span>
              {item.visualStatus && item.visualStatus !== "idle" && (
                <span className="pointer-events-none absolute top-1 right-1.5">
                  <CardStatusDot status={item.visualStatus} />
                </span>
              )}
            </NavLink>
          );
        })}
      </nav>
    </div>
  );
}

export function ContextSidebar({
  ariaLabel,
  header,
  hideHeaderOnMobile = false,
  children,
  className = "",
}: {
  ariaLabel: string;
  header?: ReactNode;
  hideHeaderOnMobile?: boolean;
  children: ReactNode;
  className?: string;
}) {
  return (
    <aside
      className={`flex h-full min-h-0 w-full flex-col bg-canvas ${className}`}
      aria-label={ariaLabel}
    >
      <MobileSidebarHeader />
      {header ? (
        <div
          className={`space-y-2 px-3 pt-2.5 pb-1.5 md:pt-3.5 md:pb-2 ${
            hideHeaderOnMobile ? "hidden md:block" : ""
          }`}
        >
          {header}
        </div>
      ) : null}
      <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-2 pt-1.5 pb-3 md:pt-0">
        {children}
      </div>
      <MobileSidebarFooter />
    </aside>
  );
}

/**
 * 侧边栏骨架屏
 * 用于在项目切换或初次拉取卡片列表时提供平滑的占位体验
 */
export function ContextSidebarSkeleton({
  ariaLabel = "加载中…",
  itemCount = 6,
  className = "",
}: {
  ariaLabel?: string;
  itemCount?: number;
  className?: string;
}) {
  return (
    <ContextSidebar
      ariaLabel={ariaLabel}
      className={className}
      header={
        <div className="space-y-3 px-1">
          <div className="flex items-center justify-between">
            <Skeleton className="h-4 w-16" />
            <Skeleton className="size-6 rounded-lg" />
          </div>
          <Skeleton className="h-8 w-full rounded-xl" />
        </div>
      }
    >
      <div className="space-y-1.5 pt-1">
        {Array.from({ length: itemCount }).map((_, i) => (
          <div
            key={i}
            className="flex items-center gap-2.5 rounded-xl px-2.5 py-2.5"
          >
            <Skeleton className="size-4 shrink-0 rounded" />
            <div className="flex-1 space-y-1.5 min-w-0">
              <Skeleton
                className={`h-3.5 ${
                  i % 3 === 0 ? "w-4/5" : i % 3 === 1 ? "w-2/3" : "w-3/4"
                }`}
              />
              <Skeleton
                className={`h-2.5 ${i % 2 === 0 ? "w-1/2" : "w-1/3"}`}
              />
            </div>
          </div>
        ))}
      </div>
    </ContextSidebar>
  );
}

export function ContextSidebarItem({
  active,
  pinned = false,
  muted = false,
  icon,
  title,
  description,
  meta,
  metaHoverOnly = false,
  actions,
  swipeActions,
  onClick,
}: {
  active: boolean;
  pinned?: boolean;
  muted?: boolean;
  icon?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  meta?: ReactNode;
  metaHoverOnly?: boolean;
  actions?: ReactNode;
  swipeActions?: SwipeActionItem[];
  onClick: () => void;
}) {
  const { t } = useTranslation();
  const content = (
    <div
      className={`group relative flex w-full items-center rounded-xl transition-all duration-150 ${
        active
          ? "bg-surface text-foreground font-medium shadow-2xs border border-line/70"
          : "text-muted-foreground hover:bg-surface/60 hover:text-foreground border border-transparent"
      }`}
    >
      <button
        type="button"
        onClick={onClick}
        aria-current={active ? "page" : undefined}
        className={`min-w-0 flex-1 rounded-xl px-3 py-2 text-left transition-colors ${
          actions && !description
            ? "group-hover:pr-14 group-focus-within:pr-14 has-[[data-state=open]]:pr-14"
            : ""
        }`}
      >
        <div className={`flex gap-2.5 ${description ? "items-start" : "items-center"}`}>
          {icon ? <div className={`shrink-0 ${description ? "mt-0.5" : ""}`}>{icon}</div> : null}
          <div className="min-w-0 flex-1">
            <div
              className={`flex items-center justify-between gap-1.5 ${
                actions && description
                  ? "group-hover:pr-14 group-focus-within:pr-14"
                  : ""
              }`}
            >
              <div className="flex min-w-0 items-center gap-1.5">
                <p className="truncate text-[13px] leading-5">
                  {title}
                </p>
                {pinned ? (
                  <Pin className="size-3 shrink-0 text-amber-500 fill-amber-500 rotate-45" />
                ) : null}
                {muted ? (
                  <span title={t("通知已静音")} aria-label={t("通知已静音")}>
                    <BellOff className="size-3 shrink-0 text-muted-foreground/75" />
                  </span>
                ) : null}
              </div>
              {meta && !description ? (
                <span
                  className={`shrink-0 text-[11px] font-normal tabular-nums text-faint group-hover:text-muted-foreground transition-opacity duration-150 ${
                    metaHoverOnly
                      ? "group-focus-within:opacity-100 group-hover:opacity-100 [@media(hover:hover)]:opacity-0"
                      : ""
                  }`}
                >
                  {meta}
                </span>
              ) : null}
            </div>
            {description ? (
              <p className="mt-0.5 line-clamp-2 text-xs font-normal leading-normal text-muted-foreground">
                {description}
              </p>
            ) : null}
            {meta && description ? (
              <p
                className={`mt-0.5 text-[11px] font-normal text-faint transition-opacity duration-150 ${
                  metaHoverOnly
                    ? "group-focus-within:opacity-100 group-hover:opacity-100 [@media(hover:hover)]:opacity-0"
                    : ""
                }`}
              >
                {meta}
              </p>
            ) : null}
          </div>
        </div>
      </button>
      {/* 桌面端 hover 时展示 actions，移动端触控屏避免与手势冲突；滑开抽屉时隐藏 */}
      {actions ? (
        <div
          className={`hidden [@media(hover:hover)]:flex opacity-0 pointer-events-none transition-opacity duration-150 group-hover:opacity-100 group-hover:pointer-events-auto group-focus-within:opacity-100 group-focus-within:pointer-events-auto has-[[data-state=open]]:opacity-100 has-[[data-state=open]]:pointer-events-auto absolute right-1.5 items-center rounded-lg bg-surface/90 backdrop-blur-xs group-data-[swipe-open=true]/swipe:hidden ${
            description ? "top-1.5" : "top-1/2 -translate-y-1/2"
          }`}
        >
          {actions}
        </div>
      ) : null}
    </div>
  );

  if (swipeActions && swipeActions.length > 0) {
    return (
      <SwipeableRow
        actions={swipeActions}
        className="rounded-xl my-0.5"
        contentClassName={active ? "bg-surface" : "bg-canvas"}
      >
        {content}
      </SwipeableRow>
    );
  }

  return <div className="my-0.5">{content}</div>;
}


