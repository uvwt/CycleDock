import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  Building2,
  Check,
  ChevronDown,
  FolderKanban,
  LogOut,
  Plus,
  Search,
  Settings,
  UserRound,
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../shared/api/client";
import type { Identity } from "../shared/api/types";
import { keys, queryClient } from "../app/query";
import { Spinner } from "../shared/ui/components";

function projectIDFromPath(pathname: string) {
  const board = pathname.match(/^\/board\/([^/]+)/);
  if (board) return board[1];
  const card = pathname.match(/^\/(?:chat|cards)\/([^/]+)\/[^/]+/);
  return card?.[1] || "";
}

export function Shell({
  identity,
  children,
}: {
  identity: Identity;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const projectMenu = useRef<HTMLDetailsElement>(null);
  const accountMenu = useRef<HTMLDetailsElement>(null);
  const [projectSearch, setProjectSearch] = useState("");
  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  const workspaces = useQuery({
    queryKey: keys.workspaces,
    queryFn: api.workspaces,
  });
  const pathProjectID = projectIDFromPath(location.pathname);
  const currentProject =
    projects.data?.find((project) => project.id === pathProjectID) ||
    (!pathProjectID ? projects.data?.[0] : undefined);
  const filteredProjects = useMemo(() => {
    const term = projectSearch.trim().toLowerCase();
    if (!term) return projects.data || [];
    return (projects.data || []).filter((project) =>
      `${project.name} ${project.description}`.toLowerCase().includes(term),
    );
  }, [projectSearch, projects.data]);

  useEffect(() => {
    projectMenu.current?.removeAttribute("open");
    accountMenu.current?.removeAttribute("open");
  }, [location.pathname]);

  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      window.location.assign("/");
    },
    onError: (error) => toast.error(String(error)),
  });
  const switchWorkspace = useMutation({
    mutationFn: api.switchWorkspace,
    onSuccess: (nextIdentity) => {
      queryClient.removeQueries({
        predicate: (query) => query.queryKey[0] !== "me",
      });
      queryClient.setQueryData(keys.me, nextIdentity);
      accountMenu.current?.removeAttribute("open");
      navigate("/board", { replace: true });
      toast.success(t("已切换工作空间"));
    },
    onError: (error) =>
      toast.error(
        error instanceof Error ? error.message : t("工作空间切换失败"),
      ),
  });

  function chooseProject(projectID: string) {
    projectMenu.current?.removeAttribute("open");
    setProjectSearch("");
    navigate("/board/" + projectID);
  }

  return (
    <div className="min-h-screen bg-slate-50 text-slate-800">
      <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-slate-200 bg-white/95 px-3 backdrop-blur sm:px-5">
        <button
          type="button"
          onClick={() =>
            navigate(currentProject ? "/board/" + currentProject.id : "/board")
          }
          className="flex min-w-0 items-center gap-2 rounded-xl px-1.5 py-1 text-left hover:bg-slate-50"
          aria-label={t("返回项目看板")}
        >
          <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-slate-900 text-sm font-bold text-white">
            C
          </span>
          <span className="hidden font-semibold sm:inline">CycleDock</span>
        </button>

        <div className="flex min-w-0 items-center gap-1.5 sm:gap-2">
          <details ref={projectMenu} className="relative">
            <summary className="flex h-9 max-w-[46vw] cursor-pointer list-none items-center gap-2 rounded-xl border border-slate-200 bg-white px-3 text-sm font-medium text-slate-700 hover:bg-slate-50 sm:max-w-72">
              <FolderKanban className="size-4 shrink-0 text-slate-400" />
              <span className="truncate">
                {projects.isPending
                  ? t("读取项目…")
                  : currentProject?.name || t("选择项目")}
              </span>
              <ChevronDown className="size-3.5 shrink-0 text-slate-400" />
            </summary>
            <div className="absolute top-11 right-0 z-50 w-[min(88vw,320px)] rounded-2xl border border-slate-200 bg-white p-2 shadow-xl">
              <label className="relative block">
                <span className="sr-only">{t("搜索项目…")}</span>
                <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-slate-400" />
                <input
                  value={projectSearch}
                  onChange={(event) => setProjectSearch(event.target.value)}
                  placeholder={t("搜索项目…")}
                  className="w-full rounded-xl border border-slate-200 bg-white py-2 pr-3 pl-9 text-sm outline-none focus:border-sky-500"
                />
              </label>
              <div className="scrollbar-thin mt-2 max-h-64 overflow-y-auto">
                {filteredProjects.map((project) => (
                  <button
                    key={project.id}
                    type="button"
                    onClick={() => chooseProject(project.id)}
                    className="flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left text-sm hover:bg-slate-50"
                  >
                    <span className="min-w-0 flex-1 truncate">
                      {project.name}
                    </span>
                    {project.id === currentProject?.id && (
                      <Check className="size-4 shrink-0 text-sky-700" />
                    )}
                  </button>
                ))}
                {!projects.isPending && !filteredProjects.length && (
                  <p className="px-3 py-6 text-center text-sm text-slate-400">
                    {projectSearch ? t("没有匹配的项目") : t("还没有项目")}
                  </p>
                )}
              </div>
              <div className="mt-2 border-t border-slate-100 pt-2">
                <button
                  type="button"
                  onClick={() => {
                    projectMenu.current?.removeAttribute("open");
                    navigate("/board?newProject=1");
                  }}
                  className="flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left text-sm font-medium text-sky-700 hover:bg-sky-50"
                >
                  <Plus className="size-4" />
                  {t("新建项目")}
                </button>
              </div>
            </div>
          </details>

          <details ref={accountMenu} className="relative">
            <summary
              className="grid size-9 cursor-pointer list-none place-items-center rounded-xl border border-slate-200 bg-white text-slate-600 hover:bg-slate-50"
              aria-label={t("账户菜单")}
              title={t("账户菜单")}
            >
              <UserRound className="size-4.5" />
            </summary>
            <div className="absolute top-11 right-0 z-50 w-[min(90vw,320px)] rounded-2xl border border-slate-200 bg-white p-2 shadow-xl">
              <div className="px-3 py-2">
                <p className="truncate text-sm font-semibold text-slate-900">
                  {identity.display_name}
                </p>
                <p className="mt-0.5 truncate text-xs text-slate-500">
                  {identity.email || identity.source.name}
                </p>
              </div>
              <div className="my-1 border-t border-slate-100" />
              <p className="px-3 py-1.5 text-[11px] font-semibold tracking-wide text-slate-400">
                {t("工作空间")}
              </p>
              {workspaces.isPending ? (
                <div className="px-3 py-2">
                  <Spinner />
                </div>
              ) : (
                <div className="space-y-0.5">
                  {(workspaces.data || []).map((workspace) => (
                    <button
                      key={workspace.id}
                      type="button"
                      disabled={
                        switchWorkspace.isPending ||
                        workspace.id === identity.workspace.id
                      }
                      onClick={() => switchWorkspace.mutate(workspace.id)}
                      className="flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left text-sm hover:bg-slate-50 disabled:opacity-70"
                    >
                      <Building2 className="size-4 shrink-0 text-slate-400" />
                      <span className="min-w-0 flex-1 truncate">
                        {workspace.name}
                      </span>
                      {workspace.id === identity.workspace.id && (
                        <Check className="size-4 shrink-0 text-sky-700" />
                      )}
                    </button>
                  ))}
                </div>
              )}
              <div className="my-1 border-t border-slate-100" />
              <button
                type="button"
                onClick={() => navigate("/settings/general")}
                className="flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left text-sm hover:bg-slate-50"
              >
                <Settings className="size-4" />
                {t("设置")}
              </button>
              <button
                type="button"
                onClick={() => logout.mutate()}
                disabled={logout.isPending}
                className="flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left text-sm text-rose-700 hover:bg-rose-50 disabled:opacity-50"
              >
                <LogOut className="size-4" />
                {t("退出登录")}
              </button>
            </div>
          </details>
        </div>
      </header>
      <div className="min-w-0">{children}</div>
    </div>
  );
}
