import { useState, type ReactNode } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ArrowLeft, Menu, ExternalLink } from "lucide-react";
import { api } from "../../shared/api/client";
import type { Identity } from "../../shared/api/types";
import { keys, queryClient } from "../../app-v2/query";
import { setLanguage, useTheme, type Theme } from "../../shared/preferences";
import {
  Button,
  Typography,
  LoadingButton,
} from "../../components/ui/controls";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
  SheetTrigger,
} from "../../components/ui/overlays";
import {
  DelayedSkeleton,
  ErrorState,
  SettingsSkeleton,
} from "../../components/ui/feedback";
import { AgentForm } from "./AgentForm";
import { MCPSettings } from "./MCPSettings";
import { ModelSettings } from "./ModelSettings";
import { ProjectForm } from "./ProjectForm";

const sections = [
  {
    group: "个人",
    items: [
      ["general", "常规"],
      ["account", "账户"],
    ],
  },
  {
    group: "AI",
    items: [
      ["models", "模型服务"],
      ["agent", "Agent 设置"],
    ],
  },
  {
    group: "外部连接",
    items: [
      ["nexusdock", "NexusDock"],
      ["mcp", "MCP 服务"],
    ],
  },
  { group: "工作空间", items: [["project", "项目设置"]] },
];

function General() {
  const { i18n, t } = useTranslation();
  const [theme, setTheme] = useTheme();
  return (
    <div className="space-y-6">
      <Typography as="h2" scale="section">
        {t("显示与偏好")}
      </Typography>
      <label className="block max-w-sm space-y-2">
        <Typography scale="control">{t("界面语言")}</Typography>
        <Select
          value={i18n.language === "en" ? "en" : "zh"}
          onValueChange={(v) => setLanguage(v as "zh" | "en")}
        >
          <SelectTrigger aria-label={t("界面语言")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="zh">{t("简体中文")}</SelectItem>
            <SelectItem value="en">English</SelectItem>
          </SelectContent>
        </Select>
      </label>
      <label className="block max-w-sm space-y-2">
        <Typography scale="control">{t("外观")}</Typography>
        <Select value={theme} onValueChange={(v) => setTheme(v as Theme)}>
          <SelectTrigger aria-label={t("外观")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="light">{t("浅色")}</SelectItem>
            <SelectItem value="dark">{t("深色")}</SelectItem>
            <SelectItem value="system">{t("跟随系统")}</SelectItem>
          </SelectContent>
        </Select>
      </label>
      <Typography scale="meta">
        {t("语言与外观仅保存在当前浏览器中。")}
      </Typography>
    </div>
  );
}

function Account({ me }: { me: Identity }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const workspaces = useQuery({
    queryKey: keys.workspaces,
    queryFn: api.workspaces,
  });
  const switcher = useMutation({
    mutationFn: api.switchWorkspace,
    onSuccess: () => {
      queryClient.clear();
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
    <div className="space-y-5">
      <div>
        <Typography scale="meta">{t("显示名称")}</Typography>
        <Typography scale="body">{me.display_name || t("未设置")}</Typography>
      </div>
      <div>
        <Typography scale="meta">{t("邮箱")}</Typography>
        <Typography scale="body">{me.email || t("未提供")}</Typography>
      </div>
      <div>
        <Typography scale="meta">{t("当前工作空间")}</Typography>
        <Typography scale="body">{me.workspace.name}</Typography>
      </div>
      {workspaces.data && (
        <div className="max-w-sm space-y-2">
          <Typography scale="control">{t("切换工作空间")}</Typography>
          <Select
            value={me.workspace.id}
            onValueChange={(id) => switcher.mutate(id)}
            disabled={switcher.isPending}
          >
            <SelectTrigger aria-label={t("工作空间")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {workspaces.data.map((w) => (
                <SelectItem key={w.id} value={w.id}>
                  {w.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}
      <Typography scale="meta">
        {t("Hosted 身份在 NexusDock 管理；模型账号授权与登录身份相互独立。")}
      </Typography>
      <LoadingButton loading={logout.isPending} onClick={() => logout.mutate()}>
        {t("退出登录")}
      </LoadingButton>
    </div>
  );
}

function NexusDock() {
  const { t } = useTranslation();
  const status = useQuery({
    queryKey: keys.nexus,
    queryFn: api.nexus,
    retry: false,
  });
  const connect = useMutation({
    mutationFn: api.nexusConnect,
    onSuccess: (data) => {
      window.location.href = data.authorization_url;
    },
    onError: (error) => toast.error(String(error)),
  });
  const disconnect = useMutation({
    mutationFn: api.nexusDisconnect,
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: keys.nexus }),
    onError: (error) => toast.error(String(error)),
  });
  if (status.isPending)
    return (
      <DelayedSkeleton>
        <SettingsSkeleton />
      </DelayedSkeleton>
    );
  if (status.isError)
    return (
      <ErrorState
        message={String(status.error)}
        onRetry={() => void status.refetch()}
      />
    );
  return (
    <div className="space-y-4">
      <Typography as="h2" scale="section">
        {t("NexusDock 设备授权")}
      </Typography>
      <Typography scale="control">
        {!status.data?.available
          ? t("当前环境未启用第一方 NexusDock 连接。")
          : status.data.connected
            ? t("已授权此工作空间访问你的 NexusDock 设备。")
            : t("尚未授权设备访问。")}
      </Typography>
      <div className="flex flex-wrap gap-2">
        {status.data?.available && !status.data.connected && (
          <LoadingButton
            variant="primary"
            loading={connect.isPending}
            onClick={() => connect.mutate()}
          >
            {t("连接 NexusDock")}
          </LoadingButton>
        )}
        {status.data?.connected && (
          <LoadingButton
            loading={disconnect.isPending}
            onClick={() => disconnect.mutate()}
          >
            {t("断开连接")}
          </LoadingButton>
        )}
        {status.data?.manage_url && (
          <Button
            onClick={() =>
              window.open(
                status.data!.manage_url,
                "_blank",
                "noopener,noreferrer",
              )
            }
          >
            <ExternalLink size={15} />
            {t("查看设备")}
          </Button>
        )}
      </div>
    </div>
  );
}

function OwnerOnly({ me, children }: { me: Identity; children: ReactNode }) {
  const { t } = useTranslation();
  if (me.role !== "owner")
    return (
      <ErrorState
        title={t("需要所有者权限")}
        message={t("这些设置仅允许 Workspace owner 管理。")}
      />
    );
  return <>{children}</>;
}

function SettingsContent({ section, me }: { section: string; me: Identity }) {
  const { t } = useTranslation();
  switch (section) {
    case "general":
      return <General />;
    case "account":
      return <Account me={me} />;
    case "models":
      return (
        <OwnerOnly me={me}>
          <ModelSettings />
        </OwnerOnly>
      );
    case "agent":
      return (
        <OwnerOnly me={me}>
          <AgentForm />
        </OwnerOnly>
      );
    case "nexusdock":
      return (
        <OwnerOnly me={me}>
          <NexusDock />
        </OwnerOnly>
      );
    case "mcp":
      return (
        <OwnerOnly me={me}>
          <MCPSettings />
        </OwnerOnly>
      );
    case "project":
      return (
        <OwnerOnly me={me}>
          <ProjectForm />
        </OwnerOnly>
      );
    default:
      return (
        <ErrorState
          title={t("没有这个设置页面")}
          message={t("请从设置导航中选择有效分类。")}
        />
      );
  }
}

export function SettingsV2({ me }: { me: Identity }) {
  const { t } = useTranslation();
  const { section = "general" } = useParams();
  const navigate = useNavigate();
  const [search] = useSearchParams();
  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  const back = `/projects/${search.get("project") || projects.data?.[0]?.id || ""}/discussions`;
  const [mobile, setMobile] = useState(false);
  const navigateSection = (next: string) => {
    navigate(
      `/settings/${next}${search.toString() ? "?" + search.toString() : ""}`,
    );
    setMobile(false);
  };
  const nav = (
    <nav className="product-settings-nav" aria-label={t("设置分类")}>
      {sections.map((group) => (
        <div className="product-settings-nav-group" key={t(group.group)}>
          <Typography scale="meta" className="product-settings-nav-label">
            {t(group.group)}
          </Typography>
          <div className="space-y-1">
            {group.items.map(([id, label]) => (
              <Button
                key={id}
                size="compact"
                variant={section === id ? "secondary" : "ghost"}
                className="w-full justify-start"
                onClick={() => navigateSection(id)}
                aria-current={section === id ? "page" : undefined}
              >
                {t(label)}
              </Button>
            ))}
          </div>
        </div>
      ))}
    </nav>
  );
  const label = t(
    sections.flatMap((g) => g.items).find(([id]) => id === section)?.[1] ||
      "设置",
  );
  return (
    <main className="v2 product-settings-page">
      <div className="product-settings-shell">
        <div className="product-settings-topbar">
          <div className="product-settings-brand">
            <img src="/cycledock-logo.png" alt="" />
            <Typography as="h1" scale="title">
              {t("设置")}
            </Typography>
          </div>
          <Button onClick={() => navigate(projects.data?.length ? back : "/")}>
            <ArrowLeft size={15} />
            {t("返回工作台")}
          </Button>
        </div>
        <div className="mb-4 flex items-center gap-2 md:hidden">
          <Sheet open={mobile} onOpenChange={setMobile}>
            <SheetTrigger asChild>
              <Button>
                <Menu size={15} />
                {t("设置分类")}
              </Button>
            </SheetTrigger>
            <SheetContent>
              <SheetTitle className="v2-text-section mb-2">
                {t("设置分类")}
              </SheetTitle>
              <SheetDescription className="v2-visually-hidden">
                {t("选择设置类别")}
              </SheetDescription>
              {nav}
            </SheetContent>
          </Sheet>
          <Typography scale="section">{label}</Typography>
        </div>
        <div className="product-settings-layout">
          <div className="hidden md:block">{nav}</div>
          <div className="product-settings-content">
            <Typography
              as="h2"
              scale="title"
              className="product-settings-content-title"
            >
              {label}
            </Typography>
            <SettingsContent section={section} me={me} />
          </div>
        </div>
      </div>
    </main>
  );
}
