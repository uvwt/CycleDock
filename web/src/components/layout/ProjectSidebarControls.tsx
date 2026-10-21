import { useTranslation } from "react-i18next";
import { useState, type ReactNode } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { ChevronDown, Plus, Settings2, LogOut } from "lucide-react";
import { api } from "../../shared/api/client";
import type { Identity, Project } from "../../shared/api/types";
import { keys, queryClient } from "../../app-v2/query";
import {
  Avatar,
  Button,
  Input,
  LoadingButton,
  Typography,
} from "../ui/controls";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/overlays";

export type Mode = "discussions" | "work";

export function ProjectCreate({
  trigger,
  mode = "discussions",
  controlledOpen,
  onOpenChange,
}: {
  trigger?: ReactNode;
  mode?: Mode;
  controlledOpen?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [localOpen, setLocalOpen] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const open = controlledOpen ?? localOpen;
  const changeOpen = onOpenChange ?? setLocalOpen;
  const create = useMutation({
    mutationFn: () =>
      api.createProject({
        name: name.trim(),
        description: description.trim(),
        instructions: "",
      }),
    onSuccess: async (project) => {
      await queryClient.invalidateQueries({ queryKey: keys.projects });
      changeOpen(false);
      setName("");
      setDescription("");
      navigate(`/projects/${project.id}/${mode}`);
      toast.success(t("项目已创建"));
    },
  });
  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      {trigger}
      <DialogContent>
        <DialogTitle className="v2-text-section">{t("新建项目")}</DialogTitle>
        <DialogDescription className="v2-text-control mt-1 text-[var(--v2-muted)]">
          {t("项目创建后仍停留在当前讨论或工作模式。")}
        </DialogDescription>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            if (name.trim()) create.mutate();
          }}
          className="mt-5 space-y-4"
        >
          <label className="block space-y-1">
            <Typography scale="control">{t("项目名称")}</Typography>
            <Input
              required
              maxLength={120}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("例如：产品规划")}
              autoFocus
            />
          </label>
          <label className="block space-y-1">
            <Typography scale="control">{t("项目简介（可选）")}</Typography>
            <Input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t("项目目标与范围")}
            />
          </label>
          {create.isError && (
            <p className="v2-text-control text-[var(--v2-danger)]" role="alert">
              {String(create.error)}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" onClick={() => changeOpen(false)}>
              {t("取消")}
            </Button>
            <LoadingButton
              type="submit"
              variant="primary"
              disabled={!name.trim()}
              loading={create.isPending}
            >
              {t("创建")}
            </LoadingButton>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function ProjectChooser({
  projects,
  project,
  mode,
}: {
  projects: Project[];
  project?: Project;
  mode: Mode;
}) {
  const { t } = useTranslation();
  const [createOpen, setCreateOpen] = useState(false);
  const navigate = useNavigate();
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            className="w-full justify-between overflow-hidden"
          >
            <span className="min-w-0 truncate text-left">
              {project?.name ?? t("选择项目")}
            </span>
            <ChevronDown size={14} />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent className="min-w-56">
          {projects.map((item) => (
            <DropdownMenuItem
              key={item.id}
              onSelect={() => navigate(`/projects/${item.id}/${mode}`)}
            >
              {item.name}
            </DropdownMenuItem>
          ))}
          <div className="v2-separator my-1" />
          <DropdownMenuItem onSelect={() => setCreateOpen(true)}>
            <Plus size={15} />
            {t("新建项目")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <ProjectCreate
        mode={mode}
        controlledOpen={createOpen}
        onOpenChange={setCreateOpen}
      />
    </>
  );
}

export function AccountChooser({ me }: { me: Identity }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const workspaces = useQuery({
    queryKey: keys.workspaces,
    queryFn: api.workspaces,
    staleTime: 30_000,
  });
  const switchTo = useMutation({
    mutationFn: api.switchWorkspace,
    onSuccess: async () => {
      // workspace 是服务端 session scope，切换后必须丢弃旧 workspace 缓存。
      queryClient.clear();
      await queryClient.invalidateQueries();
      navigate("/", { replace: true });
    },
    onError: (error) => toast.error(String(error)),
  });
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      navigate("/", { replace: true });
    },
  });
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" className="w-full justify-start">
          <Avatar
            fallback={(me.display_name || "U").slice(0, 1)}
            className="size-6"
          />
          <span className="min-w-0 flex-1 truncate text-left">
            {me.display_name || me.email || t("账户")}
          </span>
          <ChevronDown size={14} />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="min-w-56">
        <div className="px-2 py-2">
          <Typography scale="control" className="font-semibold">
            {me.workspace.name}
          </Typography>
          <Typography scale="meta" className="max-w-48 truncate">
            {me.email || me.display_name}
          </Typography>
        </div>
        {workspaces.data?.map((w) => (
          <DropdownMenuItem
            key={w.id}
            disabled={switchTo.isPending || w.id === me.workspace.id}
            onSelect={() => switchTo.mutate(w.id)}
          >
            {w.name}
            {w.id === me.workspace.id ? t(" · 当前") : ""}
          </DropdownMenuItem>
        ))}
        <div className="v2-separator my-1" />
        <DropdownMenuItem onSelect={() => navigate("/settings/general")}>
          <Settings2 size={15} />
          {t("设置")}
        </DropdownMenuItem>
        <DropdownMenuItem
          disabled={logout.isPending}
          onSelect={() => logout.mutate()}
        >
          <LogOut size={15} />
          {t("退出登录")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
