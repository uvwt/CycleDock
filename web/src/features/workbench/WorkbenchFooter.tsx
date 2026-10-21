import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useLocation, useNavigate } from "react-router-dom";
import {
  Building2,
  Check,
  ChevronUp,
  FolderKanban,
  LogOut,
  Plus,
  Settings,
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { ProjectDialog } from "./ProjectDialog";

function projectIDFromPath(pathname: string) {
  const board = pathname.match(/^\/board\/([^/]+)/);
  if (board) return board[1];
  const card = pathname.match(/^\/(?:chat|cards)\/([^/]+)\/[^/]+/);
  return card?.[1] || "";
}

export function WorkbenchFooter({ mode }: { mode: "discussion" | "work" }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const projectMenu = useRef<HTMLDetailsElement>(null);
  const accountMenu = useRef<HTMLDetailsElement>(null);
  const [projectDialogOpen, setProjectDialogOpen] = useState(false);
  const [projectName, setProjectName] = useState("");
  const [projectDescription, setProjectDescription] = useState("");
  const me = useQuery({ queryKey: keys.me, queryFn: api.me });
  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  const workspaces = useQuery({
    queryKey: keys.workspaces,
    queryFn: api.workspaces,
  });
  const pathProjectID = projectIDFromPath(location.pathname) || new URLSearchParams(location.search).get("project") || "";
  const currentProject =
    projects.data?.find((project) => project.id === pathProjectID) ||
    projects.data?.[0];

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
    onSuccess: (identity) => {
      queryClient.removeQueries({
        predicate: (query) => query.queryKey[0] !== "me",
      });
      queryClient.setQueryData(keys.me, identity);
      accountMenu.current?.removeAttribute("open");
      navigate(mode === "discussion" ? "/chat" : "/board", { replace: true });
      toast.success(t("已切换工作空间"));
    },
    onError: (error) =>
      toast.error(
        error instanceof Error ? error.message : t("工作空间切换失败"),
      ),
  });

  function projectRoute(projectID: string) {
    return mode === "discussion"
      ? "/chat?project=" + encodeURIComponent(projectID)
      : "/board/" + projectID;
  }

  function chooseProject(projectID: string) {
    projectMenu.current?.removeAttribute("open");
    navigate(projectRoute(projectID));
  }

  const createProject = useMutation({
    mutationFn: () =>
      api.createProject({
        name: projectName.trim(),
        description: projectDescription.trim(),
        instructions: "",
      }),
    onSuccess: async (created) => {
      setProjectDialogOpen(false);
      setProjectName("");
      setProjectDescription("");
      await queryClient.invalidateQueries({ queryKey: keys.projects });
      navigate(projectRoute(created.id));
    },
    onError: (error) =>
      toast.error(
        error instanceof Error ? error.message : t("创建项目失败"),
      ),
  });

  const identity = me.data;
  const initial = (identity?.display_name || identity?.source.name || "C")
    .trim()
    .slice(0, 1)
    .toUpperCase();

  return (
    <div className="shrink-0 border-t border-slate-200 bg-slate-50 p-2">
      <details ref={projectMenu} className="relative">
        <summary aria-label={t("项目菜单")} className="flex cursor-pointer list-none items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-slate-700 hover:bg-white">
          <FolderKanban className="size-4 shrink-0 text-slate-400" />
          <span className="min-w-0 flex-1 truncate font-medium">
            {projects.isPending
              ? t("读取项目…")
              : currentProject?.name || t("选择项目")}
          </span>
          <ChevronUp className="size-3.5 shrink-0 text-slate-400" />
        </summary>
        <div className="absolute bottom-11 left-0 z-50 w-[min(88vw,264px)] rounded-xl border border-slate-200 bg-white p-2 shadow-md">
          <div className="max-h-64 overflow-y-auto">
            {(projects.data || []).map((project) => (
              <button
                key={project.id}
                type="button"
                onClick={() => chooseProject(project.id)}
                className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[13px] hover:bg-slate-50"
              >
                <span className="min-w-0 flex-1 truncate">{project.name}</span>
                {project.id === currentProject?.id && (
                  <Check className="size-4 shrink-0 text-sky-700" />
                )}
              </button>
            ))}
            {!projects.isPending && !projects.data?.length && (
              <p className="px-3 py-5 text-center text-[13px] text-slate-400">
                {t("还没有项目")}
              </p>
            )}
          </div>
          <div className="mt-2 border-t border-slate-100 pt-2">
            <button
              type="button"
              onClick={() => {
                projectMenu.current?.removeAttribute("open");
                setProjectDialogOpen(true);
              }}
              className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[13px] font-medium text-sky-700 hover:bg-sky-50"
            >
              <Plus className="size-4" />
              {t("新建项目")}
            </button>
          </div>
        </div>
      </details>

      <details ref={accountMenu} className="relative">
        <summary aria-label={t("账户菜单")} className="flex cursor-pointer list-none items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-slate-700 hover:bg-white">
          <span className="grid size-7 shrink-0 place-items-center rounded-lg bg-slate-900 text-xs font-semibold text-white">
            {initial}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate font-medium">
              {identity?.display_name || identity?.source.name || t("账户")}
            </span>
            <span className="block truncate text-xs text-slate-400">
              {identity?.workspace.name || ""}
            </span>
          </span>
          <ChevronUp className="size-3.5 shrink-0 text-slate-400" />
        </summary>
        <div className="absolute bottom-12 left-0 z-50 w-[min(88vw,264px)] rounded-xl border border-slate-200 bg-white p-2 shadow-md">
          <div className="px-3 py-2">
            <p className="truncate text-[13px] font-semibold text-slate-900">
              {identity?.display_name}
            </p>
            <p className="mt-0.5 truncate text-xs text-slate-500">
              {identity?.email || identity?.source.name}
            </p>
          </div>
          <div className="my-1 border-t border-slate-100" />
          <p className="px-3 py-1.5 text-xs font-medium text-slate-400">
            {t("工作空间")}
          </p>
          {(workspaces.data || []).map((workspace) => (
            <button
              key={workspace.id}
              type="button"
              disabled={
                switchWorkspace.isPending || workspace.id === identity?.workspace.id
              }
              onClick={() => switchWorkspace.mutate(workspace.id)}
              className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[13px] hover:bg-slate-50 disabled:opacity-70"
            >
              <Building2 className="size-4 shrink-0 text-slate-400" />
              <span className="min-w-0 flex-1 truncate">{workspace.name}</span>
              {workspace.id === identity?.workspace.id && (
                <Check className="size-4 shrink-0 text-sky-700" />
              )}
            </button>
          ))}
          <div className="my-1 border-t border-slate-100" />
          <button
            type="button"
            onClick={() => {
              accountMenu.current?.removeAttribute("open");
              navigate("/settings/general");
            }}
            className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[13px] hover:bg-slate-50"
          >
            <Settings className="size-4" />
            {t("设置")}
          </button>
          <button
            type="button"
            onClick={() => logout.mutate()}
            disabled={logout.isPending}
            className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[13px] text-rose-700 hover:bg-rose-50 disabled:opacity-50"
          >
            <LogOut className="size-4" />
            {t("退出登录")}
          </button>
        </div>
      </details>

      <ProjectDialog
        open={projectDialogOpen}
        onOpenChange={(open) => {
          setProjectDialogOpen(open);
          if (!open && !createProject.isPending) {
            setProjectName("");
            setProjectDescription("");
          }
        }}
        name={projectName}
        description={projectDescription}
        setName={setProjectName}
        setDescription={setProjectDescription}
        pending={createProject.isPending}
        onSubmit={() => createProject.mutate()}
      />
    </div>
  );
}
