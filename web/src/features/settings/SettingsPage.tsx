import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useLocation, NavLink } from "react-router-dom";
import {
  Settings,
  UserRound,
  BrainCircuit,
  Bot,
  Monitor,
  Plug,
  FolderKanban,
  LogOut,
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import {
  setLanguage,
  useTheme,
  type Language,
  type Theme,
} from "../../shared/preferences";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  ErrorView,
  Field,
  Section,
  Spinner,
  TextArea,
  TextInput,
} from "../../shared/ui/components";
import { AgentSettings } from "./AgentSettings";
import { ModelsSettings } from "./ModelsSettings";
import { MCPSettings, NexusSettings } from "./ConnectionsSettings";
import type { Project } from "../../shared/api/types";
import { useProject } from "../../app/project-context";
import { ProjectSwitcher } from "../../shared/ui/ProjectSwitcher";

const menus = [
  {
    group: "个人",
    items: [
      { id: "general", title: "常规", icon: Settings },
      { id: "account", title: "账户", icon: UserRound },
    ],
  },
  {
    group: "AI 配置",
    items: [
      { id: "models", title: "模型服务", icon: BrainCircuit },
      { id: "agent", title: "Agent 设置", icon: Bot },
    ],
  },
  {
    group: "外部连接",
    items: [
      { id: "nexusdock", title: "NexusDock", icon: Monitor },
      { id: "mcp", title: "MCP 服务", icon: Plug },
    ],
  },
  {
    group: "工作空间",
    items: [{ id: "projects", title: "项目设置", icon: FolderKanban }],
  },
];
export default function SettingsPage({ role }: { role: string }) {
  const { t, i18n } = useTranslation();
  const [theme, changeTheme] = useTheme();
  const location = useLocation();
  const tab = location.pathname.split("/")[2] || "general";
  const current = menus.flatMap((g) => g.items).find((x) => x.id === tab);
  const me = useQuery({ queryKey: keys.me, queryFn: api.me });
  const isOwner = role === "owner";
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      window.location.assign("/");
    },
    onError: (e) => toast.error(String(e)),
  });
  return (
    <main className="mx-auto w-full min-w-0 max-w-6xl overflow-x-hidden px-4 py-7 sm:px-8 sm:py-8">
      <div className="mb-6">
        <h1 className="text-[22px] font-semibold tracking-tight text-slate-900">
          {t("设置")}
        </h1>
      </div>
      <div className="space-y-6">
        <nav
          aria-label={t("设置")}
          className="scrollbar-thin flex max-w-full flex-wrap gap-1 border-b border-slate-200 pb-2 md:flex-nowrap md:overflow-x-auto"
        >
          {menus.map((group) => (
            <div key={group.group} className="flex shrink-0 gap-1">
              <p className="sr-only">{t(group.group)}</p>
              {group.items.map((item) => (
                <NavLink
                  to={"/settings/" + item.id}
                  key={item.id}
                  className={({ isActive }) =>
                    `flex items-center gap-1.5 whitespace-nowrap rounded-md px-2.5 py-2 text-xs transition-colors ${isActive ? "bg-white font-medium text-sky-700 ring-1 ring-slate-200" : "text-slate-500 hover:bg-white hover:text-slate-900"}`
                  }
                >
                  <item.icon className="size-3.5" />
                  {t(item.title)}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
        <div className="min-w-0 space-y-5">
          <div className="mb-5 border-b border-slate-200 pb-4">
            <h2 className="text-base font-semibold">
              {t(current?.title || "常规")}
            </h2>
          </div>
          {tab === "general" && (
            <Section
              title={t("显示与偏好")}
              description={t(
                "选择界面语言与颜色模式，偏好仅保存在当前浏览器。",
              )}
            >
              <div className="space-y-4 text-[13px]">
                <Field label={t("界面语言")}>
                  <select
                    aria-label={t("界面语言")}
                    className="w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-[13px] leading-5"
                    value={i18n.resolvedLanguage === "en" ? "en" : "zh"}
                    onChange={(event) =>
                      setLanguage(event.target.value as Language)
                    }
                  >
                    <option value="zh">{t("简体中文")}</option>
                    <option value="en">{t("英语")}</option>
                  </select>
                </Field>
                <Field label={t("外观")}>
                  <select
                    aria-label={t("外观")}
                    className="w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-[13px] leading-5"
                    value={theme}
                    onChange={(event) =>
                      changeTheme(event.target.value as Theme)
                    }
                  >
                    <option value="light">{t("浅色")}</option>
                    <option value="dark">{t("深色")}</option>
                    <option value="system">{t("跟随系统")}</option>
                  </select>
                </Field>
              </div>
            </Section>
          )}
          {tab === "account" && (
            <Section title={t("账号与工作空间")}>
              <div className="space-y-4 text-sm">
                {me.isPending ? (
                  <Spinner />
                ) : me.error ? (
                  <ErrorView error={me.error} />
                ) : (
                  <>
                    <div>
                      <p className="text-xs text-slate-400">{t("显示名称")}</p>
                      <p className="mt-1 font-semibold">
                        {me.data?.display_name}
                      </p>
                    </div>
                    <div>
                      <p className="text-xs text-slate-400">{t("邮箱")}</p>
                      <p className="mt-1">{me.data?.email || t("未提供")}</p>
                    </div>
                    <div>
                      <p className="text-xs text-slate-400">{t("工作空间")}</p>
                      <p className="mt-1">
                        {me.data?.workspace.name} · {role}
                      </p>
                    </div>
                    <p className="text-xs text-slate-500">
                      {t(
                        "Hosted 身份在 NexusDock 管理；模型授权不属于登录身份。",
                      )}
                    </p>
                    <Button variant="danger" onClick={() => logout.mutate()}>
                      <LogOut className="size-4" />
                      {t("退出登录")}
                    </Button>
                  </>
                )}
              </div>
            </Section>
          )}
          {["models", "agent", "nexusdock", "mcp"].includes(tab) &&
            !isOwner && (
              <Section title={t("需要所有者权限")}>
                <p className="text-sm text-slate-500">
                  {t("这些设置只允许 Workspace owner 修改。")}
                </p>
              </Section>
            )}
          {tab === "models" && isOwner && <ModelsSettings />}
          {tab === "agent" && isOwner && <AgentSettings />}
          {tab === "nexusdock" && isOwner && <NexusSettings />}
          {tab === "mcp" && isOwner && <MCPSettings />}
          {tab === "projects" && <ProjectSettings />}
          {!current && (
            <Section title={t("未找到设置页面")}>
              <p className="text-slate-500">{t("请选择左侧的设置分类。")}</p>
            </Section>
          )}
        </div>
      </div>
    </main>
  );
}
function ProjectSettings() {
  const { t } = useTranslation();
  const { projects, projectID, setProjectID, isPending, error, refetch } =
    useProject();
  const current =
    projects.find((project) => project.id === projectID) || projects[0];

  if (isPending) return <Spinner />;
  if (error) return <ErrorView error={error} onRetry={refetch} />;
  if (!current)
    return (
      <Section title={t("项目设置")}>
        <p className="text-sm text-slate-500">
          {t("没有项目，请先创建项目。")}
        </p>
      </Section>
    );

  return (
    <ProjectForm
      key={current.id}
      project={current}
      projects={projects}
      onSelect={setProjectID}
    />
  );
}
function ProjectForm({
  project,
  projects,
  onSelect,
}: {
  project: Project;
  projects: Project[];
  onSelect: (id: string) => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(project.name),
    [description, setDescription] = useState(project.description),
    [instructions, setInstructions] = useState(project.instructions);
  const save = useMutation({
    mutationFn: () =>
      api.updateProject(project.id, {
        name: name.trim(),
        description: description.trim(),
        instructions: instructions.trim(),
      }),
    onSuccess: () => {
      toast.success(t("项目已保存"));
      void queryClient.invalidateQueries({ queryKey: keys.projects });
    },
    onError: (e) => toast.error(String(e)),
  });
  return (
    <Section
      title={t("项目设置")}
      description={t(
        "Project Instructions 只作用于选中的项目，不影响整个工作空间。",
      )}
    >
      <div className="space-y-4">
        <Field label={t("选择项目")}>
          <ProjectSwitcher
            projects={projects}
            projectID={project.id}
            onChange={onSelect}
          />
        </Field>
        <Field label={t("名称")}>
          <TextInput value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label={t("描述")}>
          <TextArea
            rows={3}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </Field>
        <Field label="Project Agent Instructions">
          <TextArea
            rows={6}
            value={instructions}
            onChange={(e) => setInstructions(e.target.value)}
            placeholder={t("此项目的长期 Agent 工作指令")}
          />
        </Field>
        <Button
          variant="primary"
          onClick={() => save.mutate()}
          disabled={!name.trim() || save.isPending}
        >
          {t("保存项目")}
        </Button>
      </div>
    </Section>
  );
}
