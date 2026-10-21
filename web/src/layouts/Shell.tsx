import { useState, type ReactNode } from "react";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { useMutation } from "@tanstack/react-query";
import {
  Bot,
  LayoutDashboard,
  LogOut,
  Menu,
  MessageCircle,
  Settings,
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../shared/api/client";
import type { Identity } from "../shared/api/types";
import { queryClient } from "../app/query";
import { Button, Sheet } from "../shared/ui/components";

export function Shell({
  identity,
  children,
}: {
  identity: Identity;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const [drawer, setDrawer] = useState(false);
  const focusedConversation =
    /^\/(?:chat|cards)\/[^/]+\/[^/]+\/?$/.test(location.pathname);
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      window.location.assign("/");
    },
    onError: (error) => toast.error(String(error)),
  });
  const nav = [
    { to: "/chat", label: t("聊天"), icon: MessageCircle },
    { to: "/board", label: t("看板"), icon: LayoutDashboard },
    { to: "/settings/general", label: t("设置"), icon: Settings },
  ];

  return (
    <div className="min-h-screen bg-slate-50 text-slate-800 md:flex">
      <aside className="sticky top-0 hidden h-screen w-16 shrink-0 flex-col items-center border-r border-slate-200 bg-white py-3 md:flex">
        <button
          type="button"
          onClick={() => navigate("/chat")}
          title="CycleDock"
          aria-label="CycleDock"
          className="grid size-10 place-items-center rounded-xl bg-slate-900 font-bold text-white"
        >
          C
        </button>
        <nav
          aria-label={t("主导航")}
          className="mt-6 flex w-full flex-col items-center gap-1"
        >
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              title={item.label}
              aria-label={item.label}
              className={({ isActive }) =>
                `grid size-10 place-items-center rounded-xl transition ${isActive ? "bg-slate-100 text-slate-900" : "text-slate-400 hover:bg-slate-50 hover:text-slate-800"}`
              }
            >
              <item.icon className="size-5" />
            </NavLink>
          ))}
        </nav>
        <button
          type="button"
          onClick={() => setDrawer(true)}
          title={t("导航抽屉")}
          aria-label={t("导航抽屉")}
          className="mt-auto grid size-10 place-items-center rounded-xl text-slate-400 hover:bg-slate-100 hover:text-slate-700"
        >
          <Menu className="size-5" />
        </button>
      </aside>

      <div className="min-w-0 flex-1">
        {!focusedConversation && (
          <header className="sticky top-0 z-30 flex h-16 items-center justify-between border-b border-slate-200 bg-white/95 px-4 backdrop-blur md:hidden">
            <button
              type="button"
              onClick={() => navigate("/chat")}
              className="flex items-center gap-2 font-semibold"
            >
              <span className="grid size-8 place-items-center rounded-lg bg-slate-900 text-sm font-bold text-white">
                C
              </span>
              <span>CycleDock</span>
            </button>
            <nav aria-label={t("主导航")} className="flex items-center gap-1">
              {nav.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  title={item.label}
                  aria-label={item.label}
                  className={({ isActive }) =>
                    `grid size-9 place-items-center rounded-xl transition ${isActive ? "bg-slate-100 text-slate-900" : "text-slate-400"}`
                  }
                >
                  <item.icon className="size-4.5" />
                </NavLink>
              ))}
              <button
                type="button"
                onClick={() => setDrawer(true)}
                title={t("导航抽屉")}
                aria-label={t("导航抽屉")}
                className="grid size-9 place-items-center rounded-xl text-slate-400"
              >
                <Menu className="size-4.5" />
              </button>
            </nav>
          </header>
        )}
        {children}
      </div>

      <Sheet
        open={drawer}
        onOpenChange={setDrawer}
        side="right"
        title="CycleDock"
      >
        <div className="mb-5 rounded-2xl bg-slate-50 p-4">
          <p className="font-semibold">{identity.display_name}</p>
          <p className="mt-1 truncate text-xs text-slate-500">
            {identity.email || identity.workspace.name}
          </p>
        </div>
        <div className="space-y-1">
          {nav.map((item) => (
            <button
              key={item.to}
              type="button"
              onClick={() => {
                setDrawer(false);
                navigate(item.to);
              }}
              className="flex w-full items-center gap-3 rounded-xl px-3 py-3 text-left text-sm text-slate-700 hover:bg-slate-100"
            >
              <item.icon className="size-5" />
              {item.label}
            </button>
          ))}
        </div>
        <div className="my-5 border-t border-slate-100" />
        <p className="px-3 pb-2 text-xs font-medium text-slate-400">
          {t("工作空间")}
        </p>
        <div className="flex items-center gap-2 rounded-xl px-3 py-2 text-sm">
          <Bot className="size-4" />
          {identity.workspace.name}
        </div>
        <Button
          className="mt-6 w-full"
          variant="ghost"
          onClick={() => logout.mutate()}
        >
          <LogOut className="size-4" />
          {t("退出登录")}
        </Button>
      </Sheet>
    </div>
  );
}
