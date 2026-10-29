import { useState, type MouseEvent, type ReactNode } from "react";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import {
  Activity,
  BriefcaseBusiness,
  CalendarClock,
  MessageCircle,
  Settings,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useProject } from "../app/project-context";
import { ProjectSwitcher } from "../shared/ui/ProjectSwitcher";
import { ContextSidebarVisibility } from "../app/context-sidebar";

export function Shell({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const { projects, projectID, setProjectID } = useProject();
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

  const primaryNav = [
    {
      to: projectID
        ? `/chat?project=${encodeURIComponent(projectID)}`
        : "/chat",
      activePath: "/chat",
      label: t("聊天"),
      icon: MessageCircle,
    },
    {
      to: projectID ? `/board/${encodeURIComponent(projectID)}` : "/board",
      activePath: "/board",
      label: t("工作"),
      icon: BriefcaseBusiness,
    },
    {
      to: projectID ? `/plans/${encodeURIComponent(projectID)}` : "/plans",
      activePath: "/plans",
      label: t("计划"),
      icon: CalendarClock,
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

  function createProject() {
    navigate(
      projectID
        ? `/board/${encodeURIComponent(projectID)}?new=project`
        : "/board?new=project",
    );
  }

  function manageProjects() {
    if (projectID) setProjectID(projectID);
    navigate("/settings/projects");
  }

  const logo = (
    <img
      src="/cycledock-logo.png"
      alt=""
      className="size-8 object-contain"
      draggable={false}
    />
  );

  return (
    <div className="min-h-screen bg-canvas text-strong md:flex">
      <aside className="sticky top-0 hidden h-screen w-16 shrink-0 flex-col items-center border-r border-line bg-surface py-3 md:flex">
        <button
          type="button"
          onClick={() => navigate("/chat")}
          title="CycleDock"
          aria-label="CycleDock"
          className="grid size-10 place-items-center rounded-xl transition hover:bg-canvas"
        >
          {logo}
        </button>

        <nav
          aria-label={t("主导航")}
          className="mt-6 flex w-full flex-col items-center gap-1"
        >
          {primaryNav.map((item) => (
            <NavLink
              key={item.activePath}
              to={item.to}
              title={item.label}
              aria-label={item.label}
              aria-expanded={
                item.activePath === listSection ? listVisible : undefined
              }
              onClick={(event) => selectPrimary(event, item.activePath)}
              className={({ isActive }) =>
                `grid size-10 place-items-center rounded-xl transition ${
                  primaryActive(item.activePath, isActive)
                    ? "bg-muted text-heading"
                    : "text-faint hover:bg-canvas hover:text-strong"
                }`
              }
            >
              <item.icon className="size-5" />
            </NavLink>
          ))}
        </nav>

        <div className="mt-auto flex flex-col items-center gap-1">
          <ProjectSwitcher
            compact
            projects={projects}
            projectID={projectID}
            onChange={switchProject}
            onCreate={createProject}
            onManage={manageProjects}
          />
          <NavLink
            to="/settings/general"
            onClick={() => projectID && setProjectID(projectID)}
            title={t("设置")}
            aria-label={t("设置")}
            className={({ isActive }) =>
              `grid size-10 place-items-center rounded-xl transition ${
                isActive
                  ? "bg-muted text-heading"
                  : "text-faint hover:bg-canvas hover:text-strong"
              }`
            }
          >
            <Settings className="size-5" />
          </NavLink>
        </div>
      </aside>

      <div className="min-w-0 flex-1">
        {!focusedConversation && (
          <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-line bg-surface/95 px-3 backdrop-blur md:hidden">
            <button
              type="button"
              onClick={() => navigate("/chat")}
              title="CycleDock"
              aria-label="CycleDock"
              className="grid size-9 place-items-center rounded-xl"
            >
              {logo}
            </button>
            <nav aria-label={t("主导航")} className="flex items-center gap-1">
              {primaryNav.map((item) => (
                <NavLink
                  key={item.activePath}
                  to={item.to}
                  title={item.label}
                  aria-label={item.label}
                  className={({ isActive }) =>
                    `grid size-9 place-items-center rounded-xl transition ${
                      primaryActive(item.activePath, isActive)
                        ? "bg-muted text-heading"
                        : "text-faint"
                    }`
                  }
                >
                  <item.icon className="size-[18px]" />
                </NavLink>
              ))}
              <ProjectSwitcher
                compact
                projects={projects}
                projectID={projectID}
                onChange={switchProject}
                onCreate={createProject}
                onManage={manageProjects}
              />
              <NavLink
                to="/settings/general"
                onClick={() => projectID && setProjectID(projectID)}
                title={t("设置")}
                aria-label={t("设置")}
                className={({ isActive }) =>
                  `grid size-9 place-items-center rounded-xl transition ${
                    isActive ? "bg-muted text-heading" : "text-faint"
                  }`
                }
              >
                <Settings className="size-[18px]" />
              </NavLink>
            </nav>
          </header>
        )}
        <ContextSidebarVisibility.Provider value={listVisible}>
          {children}
        </ContextSidebarVisibility.Provider>
      </div>
    </div>
  );
}
