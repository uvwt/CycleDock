import {
  Suspense,
  lazy,
  useMemo,
  useState,
  type MouseEvent,
  type ReactNode,
} from "react";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  BriefcaseBusiness,
  CalendarClock,
  Cpu,
  MessageCircle,
  PanelLeft,
  Settings,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useProject } from "../app/project-context";
import { keys } from "../app/query";
import { preloadRouteForPathname } from "../app/route-preload";
import { api } from "../shared/api/client";
import type { Card } from "../shared/api/types";
import { ProjectSwitcherWithSettings } from "../features/projects/ProjectSwitcherWithSettings";
import { ContextSidebarVisibility } from "../app/context-sidebar";
import {
  CardStatusDot,
  type CardVisualStatus,
} from "../shared/ui/CardStatusDot";
import { getKindVisualStatus } from "../shared/lib/card-focus";
import {
  QuickCardSwitcherModal,
  QuickCardSwitcherTrigger,
} from "../shared/ui/MobileQuickCardSwitcher";
import { HintTooltip, Sheet, Spinner } from "../shared/ui/components";
import { MainChatDock } from "../features/chat/MainChatDock";

const DiscussionSidebar = lazy(() =>
  import("../features/chat/DiscussionSidebar").then((m) => ({
    default: m.DiscussionSidebar,
  })),
);
const WorkSidebar = lazy(() =>
  import("../features/board/WorkSidebar").then((m) => ({
    default: m.WorkSidebar,
  })),
);
const PlanSidebar = lazy(() =>
  import("../features/plans/PlanSidebar").then((m) => ({
    default: m.PlanSidebar,
  })),
);

function getResetPath(pathname: string, projectID: string): string {
  if (pathname.startsWith("/board") || pathname.startsWith("/cards/")) {
    return projectID ? `/board/${encodeURIComponent(projectID)}` : "/board";
  }
  if (pathname.startsWith("/plans")) {
    return projectID ? `/plans/${encodeURIComponent(projectID)}` : "/plans";
  }
  if (pathname.startsWith("/activity")) {
    return projectID ? `/activity/${encodeURIComponent(projectID)}` : "/activity";
  }
  if (pathname.startsWith("/nexusdock")) {
    return "/nexusdock";
  }
  if (pathname.startsWith("/settings")) {
    return "/settings/general";
  }
  return projectID ? `/chat?project=${encodeURIComponent(projectID)}` : "/chat";
}

export function Shell({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const { projects, projectID, setProjectID } = useProject();
  const [quickSwitcherOpen, setQuickSwitcherOpen] = useState(false);
  const [mobileDrawerOpen, setMobileDrawerOpen] = useState(false);
  const [listVisibility, setListVisibility] = useState({
    "/chat": true,
    "/board": true,
    "/plans": true,
  });
  const listSection = location.pathname.startsWith("/chat")
    ? "/chat"
    : location.pathname.startsWith("/cards/")
      ? "/board"
      : /^\/plans\/[^/]+\/[^/]+\/?$/.test(location.pathname)
        ? "/plans"
        : null;
  const listVisible = listSection ? listVisibility[listSection] : true;
  const focusedConversation = /^\/(?:chat|cards|plans)\/[^/]+\/[^/]+\/?$/.test(
    location.pathname,
  );
  const isChatHome =
    location.pathname === "/chat" || location.pathname === "/chat/";

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
    {
      to: "/nexusdock",
      activePath: "/nexusdock",
      label: "NexusDock",
      icon: Cpu,
    },
  ];

  function primaryActive(activePath: string, routerActive: boolean) {
    if (activePath === "/board") {
      return (
        routerActive ||
        location.pathname.startsWith("/board") ||
        location.pathname.startsWith("/cards/")
      );
    }
    if (activePath === "/plans") {
      return routerActive || location.pathname.startsWith("/plans");
    }
    if (activePath === "/activity") {
      return routerActive || location.pathname.startsWith("/activity");
    }
    if (activePath === "/nexusdock") {
      return routerActive || location.pathname.startsWith("/nexusdock");
    }
    return routerActive || location.pathname.startsWith("/chat");
  }

  function selectPrimary(event: MouseEvent<HTMLAnchorElement>, path: string) {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey)
      return;
    if (path !== "/chat" && path !== "/board" && path !== "/plans") return;

    // 再次点击当前入口只切换列表，保留正在查看的会话和草稿。
    if (path === listSection) {
      event.preventDefault();
      setListVisibility((current) => ({ ...current, [path]: !current[path] }));
      return;
    }
    setListVisibility((current) => ({ ...current, [path]: true }));
  }

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

  const logo = (
    <img
      src="/cycledock-logo.png"
      alt=""
      className="size-8 object-contain"
      draggable={false}
    />
  );

  const handleLogoClick = () => {
    navigate(getResetPath(location.pathname, projectID));
  };

  return (
    <div
      className={`relative flex h-full min-h-0 w-full flex-col overflow-hidden text-strong md:flex-row ${
        focusedConversation ? "bg-surface" : "bg-canvas"
      }`}
    >
      <aside className="sticky top-0 hidden h-screen w-16 shrink-0 flex-col items-center border-r border-line/70 bg-surface/90 py-3.5 backdrop-blur-md md:flex">
        <button
          type="button"
          onClick={handleLogoClick}
          title="CycleDock"
          aria-label="CycleDock"
          className="grid size-10 place-items-center rounded-xl transition hover:bg-muted/70 active:scale-95"
        >
          {logo}
        </button>

        <nav
          aria-label={t("主导航")}
          className="mt-6 flex w-full flex-col items-center gap-1.5"
        >
          {primaryNav.map((item) => (
            <HintTooltip
              key={item.activePath}
              content={item.label}
              side="right"
            >
              <NavLink
                to={item.to}
                aria-label={item.label}
                aria-expanded={
                  item.activePath === listSection ? listVisible : undefined
                }
                onMouseEnter={() => preloadRouteForPathname(item.activePath)}
                onFocus={() => preloadRouteForPathname(item.activePath)}
                onClick={(event) => selectPrimary(event, item.activePath)}
                className={({ isActive }) => {
                  const active = primaryActive(item.activePath, isActive);
                  return `group relative grid size-10 place-items-center rounded-xl transition-all duration-150 active:scale-95 ${
                    active
                      ? "bg-primary/10 text-primary font-semibold shadow-2xs"
                      : "text-muted-foreground/70 hover:bg-muted/70 hover:text-foreground"
                  }`;
                }}
              >
                {({ isActive }) => {
                  const active = primaryActive(item.activePath, isActive);
                  return (
                    <>
                      {active && (
                        <span className="absolute -left-3 top-2.5 bottom-2.5 w-1 rounded-r-full bg-primary transition-all" />
                      )}
                      <item.icon className="size-5 transition-transform duration-150 group-hover:scale-105" />
                      {item.visualStatus && item.visualStatus !== "idle" && (
                        <span className="pointer-events-none absolute top-1.5 right-1.5">
                          <CardStatusDot status={item.visualStatus} />
                        </span>
                      )}
                    </>
                  );
                }}
              </NavLink>
            </HintTooltip>
          ))}
        </nav>

        <div className="mt-auto flex flex-col items-center gap-1.5">
          {projectID && (
            <HintTooltip content={t("快速切换卡片 (⌘K)")} side="right">
              <div>
                <QuickCardSwitcherTrigger
                  projectID={projectID}
                  activeCardID={viewedCardID || undefined}
                  onClick={() => {
                    if (focusedConversation || isChatHome) {
                      window.dispatchEvent(
                        new CustomEvent("cycledock:open-quick-switcher"),
                      );
                    } else {
                      setQuickSwitcherOpen(true);
                    }
                  }}
                  className="size-10 rounded-xl border-transparent bg-transparent shadow-none hover:bg-muted/70"
                />
              </div>
            </HintTooltip>
          )}
          <ProjectSwitcherWithSettings
            compact
            projects={projects}
            projectID={projectID}
            onChange={switchProject}
          />
          <HintTooltip content={t("设置")} side="right">
            <NavLink
              to="/settings/general"
              onMouseEnter={() => preloadRouteForPathname("/settings/general")}
              onFocus={() => preloadRouteForPathname("/settings/general")}
              onClick={() => projectID && setProjectID(projectID)}
              aria-label={t("设置")}
              className={({ isActive }) =>
                `group relative grid size-10 place-items-center rounded-xl transition-all duration-150 active:scale-95 ${
                  isActive
                    ? "bg-primary/10 text-primary font-semibold shadow-2xs"
                    : "text-muted-foreground/70 hover:bg-muted/70 hover:text-foreground"
                }`
              }
            >
              {({ isActive }) => (
                <>
                  {isActive && (
                    <span className="absolute -left-3 top-2.5 bottom-2.5 w-1 rounded-r-full bg-primary transition-all" />
                  )}
                  <Settings className="size-5 transition-transform duration-150 group-hover:scale-105" />
                </>
              )}
            </NavLink>
          </HintTooltip>
        </div>
      </aside>

      <div className="flex h-full min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-surface">
        {!focusedConversation && !isChatHome && (
          <header className="sticky top-0 z-30 grid h-[calc(3.25rem+env(safe-area-inset-top,0px))] grid-cols-[2.25rem_minmax(0,1fr)_2.25rem] items-center gap-2 bg-surface/90 px-3 pt-[env(safe-area-inset-top,0px)] backdrop-blur-md md:hidden">
            <button
              type="button"
              onClick={() => setMobileDrawerOpen(true)}
              title={t("打开侧栏")}
              aria-label={t("打开侧栏")}
              className="inline-flex size-8 items-center justify-center rounded-lg border border-line bg-surface text-muted-foreground shadow-xs transition hover:bg-muted hover:text-foreground"
            >
              <PanelLeft className="size-4" />
            </button>
            <div className="truncate text-center text-sm font-semibold text-heading">
              {location.pathname.startsWith("/board")
                ? t("任务")
                : location.pathname.startsWith("/plans")
                  ? t("定时")
                  : location.pathname.startsWith("/activity")
                    ? t("活动")
                    : location.pathname.startsWith("/nexusdock")
                      ? t("设备")
                      : location.pathname.startsWith("/settings")
                        ? t("设置")
                        : "CycleDock"}
            </div>
            <div className="flex items-center justify-end">
              {projectID && (
                <QuickCardSwitcherTrigger
                  projectID={projectID}
                  onClick={() => setQuickSwitcherOpen(true)}
                />
              )}
            </div>
          </header>
        )}
        {!focusedConversation && !isChatHome && (
          <Sheet
            open={mobileDrawerOpen}
            onOpenChange={setMobileDrawerOpen}
            side="left"
            padded={false}
            showCloseButton={false}
            className="bg-canvas"
          >
            <div
              className="flex h-full min-h-0 flex-col"
              onClick={(e) => {
                if ((e.target as HTMLElement).closest("a")) {
                  setMobileDrawerOpen(false);
                }
              }}
            >
              {mobileDrawerOpen && (
                <Suspense
                  fallback={
                    <div className="grid flex-1 place-items-center p-6">
                      <Spinner />
                    </div>
                  }
                >
                  {location.pathname.startsWith("/board") ? (
                    <WorkSidebar
                      cards={projectCards}
                      onSelect={(card: Card) => {
                        setMobileDrawerOpen(false);
                        navigate(
                          `/cards/${encodeURIComponent(card.project_id)}/${encodeURIComponent(card.id)}`,
                        );
                      }}
                      loading={cardsQuery.isPending}
                    />
                  ) : location.pathname.startsWith("/plans") ? (
                    <PlanSidebar
                      cards={projectCards.filter(
                        (card) => card.kind === "ongoing",
                      )}
                      activeCardID=""
                      onSelect={(card: Card) => {
                        setMobileDrawerOpen(false);
                        navigate(
                          `/plans/${encodeURIComponent(card.project_id)}/${encodeURIComponent(card.id)}`,
                        );
                      }}
                      loading={cardsQuery.isPending}
                    />
                  ) : (
                    <DiscussionSidebar
                      discussions={projectCards
                        .filter((card) => card.kind === "discussion")
                        .sort((a, b) =>
                          b.updated_at.localeCompare(a.updated_at),
                        )}
                      onSelect={(card: Card) => {
                        setMobileDrawerOpen(false);
                        navigate(
                          `/chat/${encodeURIComponent(card.project_id)}/${encodeURIComponent(card.id)}`,
                        );
                      }}
                      onCreate={() => {
                        setMobileDrawerOpen(false);
                        navigate(
                          projectID
                            ? `/chat?project=${encodeURIComponent(projectID)}`
                            : "/chat",
                        );
                      }}
                      loading={cardsQuery.isPending}
                    />
                  )}
                </Suspense>
              )}
            </div>
          </Sheet>
        )}
        {!focusedConversation && !isChatHome && projectID && (
          <QuickCardSwitcherModal
            projectID={projectID}
            open={quickSwitcherOpen}
            onOpenChange={setQuickSwitcherOpen}
          />
        )}
        <ContextSidebarVisibility.Provider value={listVisible}>
          {children}
        </ContextSidebarVisibility.Provider>
      </div>
      <MainChatDock />
    </div>
  );
}
