import { useMutation, useQuery } from "@tanstack/react-query";
import { NavLink, Navigate, useLocation } from "react-router-dom";
import { Bot, BrainCircuit, LogOut, Plug, Settings } from "lucide-react";
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
  Select,
  Spinner,
} from "../../shared/ui/components";
import { clearPushSubscription } from "../../shared/realtime/notifications";
import { NotificationSettings } from "./NotificationSettings";
import { ModelsSettings } from "./ModelsSettings";
import { MCPSettings } from "./ConnectionsSettings";
import { AuthorizedClientsSettings } from "./AuthorizedClientsSettings";

const menus = [
  {
    group: "个人",
    items: [{ id: "general", title: "常规", icon: Settings }],
  },
  {
    group: "AI 配置",
    items: [{ id: "models", title: "模型服务", icon: BrainCircuit }],
  },
  {
    group: "外部连接",
    items: [
      { id: "mcp", title: "MCP 服务", icon: Plug },
      { id: "clients", title: "AI 客户端", icon: Bot },
    ],
  },
];

export default function SettingsPage({ role }: { role: string }) {
  const { t, i18n } = useTranslation();
  const [theme, changeTheme] = useTheme();
  const location = useLocation();
  const tab = location.pathname.split("/")[2] || "general";
  const current = menus
    .flatMap((group) => group.items)
    .find((item) => item.id === tab);
  const me = useQuery({ queryKey: keys.me, queryFn: api.me });
  const isOwner = role === "owner";
  const logout = useMutation({
    mutationFn: async () => {
      await clearPushSubscription();
      await api.logout();
    },
    onSuccess: () => {
      queryClient.clear();
      window.location.assign("/");
    },
    onError: (error) => toast.error(String(error)),
  });

  // 旧入口只做兼容跳转，不再保留独立的账号、项目或 Agent 设置页面。
  if (tab === "account" || tab === "projects") {
    return <Navigate to="/settings/general" replace />;
  }
  if (tab === "agent") {
    return <Navigate to="/settings/models" replace />;
  }

  return (
    <main className="mx-auto w-full min-w-0 max-w-6xl overflow-x-hidden px-4 py-7 sm:px-8 sm:py-8">
      <div className="mb-6">
        <h1 className="text-[22px] font-semibold tracking-tight text-heading">
          {t("设置")}
        </h1>
      </div>
      <div className="space-y-6">
        <nav
          aria-label={t("设置")}
          className="scrollbar-thin flex max-w-full flex-wrap gap-1 border-b border-line pb-2 md:flex-nowrap md:overflow-x-auto"
        >
          {menus.map((group) => (
            <div key={group.group} className="flex shrink-0 gap-1">
              <p className="sr-only">{t(group.group)}</p>
              {group.items.map((item) => (
                <NavLink
                  to={"/settings/" + item.id}
                  key={item.id}
                  className={({ isActive }) =>
                    `flex items-center gap-1.5 whitespace-nowrap rounded-md px-2.5 py-2 text-xs transition-colors ${
                      isActive
                        ? "bg-surface font-medium text-sky-700 ring-1 ring-line"
                        : "text-muted-foreground hover:bg-surface hover:text-heading"
                    }`
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
          {tab === "general" && (
            <>
              <Section
                title={t("显示与偏好")}
                description={t(
                  "选择界面语言与颜色模式，偏好仅保存在当前浏览器。",
                )}
              >
                <div className="space-y-4 text-[13px]">
                  <NotificationSettings />
                  <Field label={t("界面语言")}>
                    <Select
                      aria-label={t("界面语言")}
                      value={i18n.resolvedLanguage === "en" ? "en" : "zh"}
                      onChange={(event) =>
                        setLanguage(event.target.value as Language)
                      }
                    >
                      <option value="zh">{t("简体中文")}</option>
                      <option value="en">{t("英语")}</option>
                    </Select>
                  </Field>
                  <Field label={t("外观")}>
                    <Select
                      aria-label={t("外观")}
                      value={theme}
                      onChange={(event) =>
                        changeTheme(event.target.value as Theme)
                      }
                    >
                      <option value="light">{t("浅色")}</option>
                      <option value="dark">{t("深色")}</option>
                      <option value="system">{t("跟随系统")}</option>
                    </Select>
                  </Field>
                </div>
              </Section>

              <Section
                title={t("当前账号")}
                description={t("查看当前登录邮箱，或安全退出 CycleDock。")}
              >
                {me.isPending ? (
                  <Spinner />
                ) : me.error ? (
                  <ErrorView error={me.error} />
                ) : (
                  <div className="flex flex-wrap items-center justify-between gap-4">
                    <div>
                      <p className="text-xs text-faint">{t("邮箱")}</p>
                      <p className="mt-1 text-sm font-medium text-foreground">
                        {me.data?.email || t("未提供")}
                      </p>
                    </div>
                    <Button
                      variant="danger"
                      disabled={logout.isPending}
                      onClick={() => logout.mutate()}
                    >
                      <LogOut className="size-4" />
                      {t("退出登录")}
                    </Button>
                  </div>
                )}
              </Section>
            </>
          )}

          {["models", "mcp"].includes(tab) && !isOwner && (
            <Section title={t("需要所有者权限")}>
              <p className="text-sm text-muted-foreground">
                {t("这些设置只允许 Workspace owner 修改。")}
              </p>
            </Section>
          )}
          {tab === "models" && isOwner && <ModelsSettings />}
          {tab === "mcp" && isOwner && <MCPSettings />}
          {tab === "clients" && <AuthorizedClientsSettings />}

          {!current && (
            <Section title={t("未找到设置页面")}>
              <p className="text-muted-foreground">
                {t("请选择左侧的设置分类。")}
              </p>
            </Section>
          )}
        </div>
      </div>
    </main>
  );
}
