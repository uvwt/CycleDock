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
    <main className="mx-auto max-w-6xl px-4 py-9 sm:px-8">
      <div className="mb-8">
        <p className="text-xs font-semibold tracking-widest text-sky-700">
          PREFERENCES
        </p>
        <h1 className="mt-2 text-3xl font-semibold text-slate-900">设置</h1>
      </div>
      <div className="grid gap-6 md:grid-cols-[208px_minmax(0,1fr)]">
        <nav
          aria-label="设置导航"
          className="flex gap-2 overflow-x-auto md:flex-col md:gap-5"
        >
          {menus.map((group) => (
            <div key={group.group} className="flex shrink-0 gap-1 md:block">
              <p className="hidden px-3 pb-2 text-xs font-semibold text-slate-400 md:block">
                {group.group}
              </p>
              {group.items.map((item) => (
                <NavLink
                  to={"/settings/" + item.id}
                  key={item.id}
                  className={({ isActive }) =>
                    `flex items-center gap-2.5 whitespace-nowrap rounded-xl px-3 py-2.5 text-sm transition md:mb-1 ${isActive ? "bg-white font-semibold text-sky-700 shadow-sm" : "text-slate-500 hover:bg-white hover:text-slate-900"}`
                  }
                >
                  <item.icon className="size-4" />
                  {item.title}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
        <div className="min-w-0 space-y-5">
          <div className="mb-5 border-b border-slate-200 pb-4">
            <h2 className="text-xl font-semibold">
              {current?.title || "常规"}
            </h2>
          </div>
          {tab === "general" && (
            <Section
              title="显示与偏好"
              description="界面采用简洁的高对比度浅色主题。"
            >
              <div className="space-y-4 text-sm">
                <div className="flex items-center justify-between">
                  <span>界面语言</span>
                  <span className="text-slate-500">中文（当前默认）</span>
                </div>
                <div className="border-t border-slate-100" />
                <div className="flex items-center justify-between">
                  <span>外观</span>
                  <span className="text-slate-500">白灰色 · 浅色</span>
                </div>
                <p className="text-xs text-slate-400">
                  多语言、主题个性化切换将在完整 i18n 与主题持久化接入后开放。
                </p>
              </div>
            </Section>
          )}
          {tab === "account" && (
            <Section title="账号与工作空间">
              <div className="space-y-4 text-sm">
                {me.isPending ? (
                  <Spinner />
                ) : me.error ? (
                  <ErrorView error={me.error} />
                ) : (
                  <>
                    <div>
                      <p className="text-xs text-slate-400">显示名称</p>
                      <p className="mt-1 font-semibold">
                        {me.data?.display_name}
                      </p>
                    </div>
                    <div>
                      <p className="text-xs text-slate-400">邮箱</p>
                      <p className="mt-1">{me.data?.email || "未提供"}</p>
                    </div>
                    <div>
                      <p className="text-xs text-slate-400">工作空间</p>
                      <p className="mt-1">
                        {me.data?.workspace.name} · {role}
                      </p>
                    </div>
                    <p className="text-xs text-slate-500">
                      Hosted 身份在 NexusDock 管理；模型授权不属于登录身份。
                    </p>
                    <Button variant="danger" onClick={() => logout.mutate()}>
                      <LogOut className="size-4" />
                      退出登录
                    </Button>
                  </>
                )}
              </div>
            </Section>
          )}
          {["models", "agent", "nexusdock", "mcp"].includes(tab) &&
            !isOwner && (
              <Section title="需要所有者权限">
                <p className="text-sm text-slate-500">
                  这些设置只允许 Workspace owner 修改。
                </p>
              </Section>
            )}
          {tab === "models" && isOwner && <ModelsSettings />}
          {tab === "agent" && isOwner && <AgentSettings />}
          {tab === "nexusdock" && isOwner && <NexusSettings />}
          {tab === "mcp" && isOwner && <MCPSettings />}
          {tab === "projects" && <ProjectSettings />}
          {!current && (
            <Section title="未找到设置页面">
              <p className="text-slate-500">请选择左侧的设置分类。</p>
            </Section>
          )}
        </div>
      </div>
    </main>
  );
}
function ProjectSettings() {
  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  const [projectID, setProjectID] = useState("");
  const current =
    projects.data?.find((p) => p.id === projectID) || projects.data?.[0];
  if (projects.isPending) return <Spinner />;
  if (projects.error) return <ErrorView error={projects.error} />;
  if (!current)
    return (
      <Section title="项目设置">
        <p className="text-sm text-slate-500">没有项目，请先创建项目。</p>
      </Section>
    );
  return (
    <ProjectForm
      key={current.id}
      project={current}
      projects={projects.data || []}
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
      toast.success("项目已保存");
      void queryClient.invalidateQueries({ queryKey: keys.projects });
    },
    onError: (e) => toast.error(String(e)),
  });
  return (
    <Section
      title="项目设置"
      description="Project Instructions 只作用于选中的项目，不影响整个工作空间。"
    >
      <div className="space-y-4">
        <Field label="选择项目">
          <select
            value={project.id}
            onChange={(e) => onSelect(e.target.value)}
            className="w-full rounded-xl border border-slate-200 bg-white p-2.5 text-sm"
          >
            {projects.map((p) => (
              <option value={p.id} key={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="名称">
          <TextInput value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="描述">
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
            placeholder="此项目的长期 Agent 工作指令"
          />
        </Field>
        <Button
          variant="primary"
          onClick={() => save.mutate()}
          disabled={!name.trim() || save.isPending}
        >
          保存项目
        </Button>
      </div>
    </Section>
  );
}
