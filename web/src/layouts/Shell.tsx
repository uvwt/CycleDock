import { useState, type ReactNode } from "react";
import { NavLink, useNavigate } from "react-router-dom";
import { useMutation } from "@tanstack/react-query";
import {
  Bot,
  LayoutDashboard,
  Menu,
  MessageCircle,
  Plus,
  Settings,
  LogOut,
  ExternalLink,
} from "lucide-react";
import { api } from "../shared/api/client";
import type { Identity } from "../shared/api/types";
import { queryClient } from "../app/query";
import { Button, Sheet } from "../shared/ui/components";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";

export function Shell({
  identity,
  children,
}: {
  identity: Identity;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const [drawer, setDrawer] = useState(false);
  const navigate = useNavigate();
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      window.location.assign("/");
    },
    onError: (e) => toast.error(String(e)),
  });
  const nav = [
    { to: "/chat", label: t("聊天"), icon: MessageCircle },
    { to: "/board", label: t("看板"), icon: LayoutDashboard },
  ];
  return (
    <div className="min-h-screen bg-slate-50 text-slate-800">
      <header className="sticky top-0 z-20 flex h-16 items-center justify-between border-b border-slate-200 bg-white/95 px-4 backdrop-blur md:px-8">
        <div className="flex min-w-0 items-center gap-3">
          <button
            onClick={() => navigate("/chat")}
            className="flex items-center gap-2 font-semibold tracking-tight"
          >
            <span className="grid size-9 place-items-center rounded-xl bg-slate-900 font-bold text-white">
              C
            </span>
            <span className="hidden sm:inline">CycleDock</span>
          </button>
          <span className="hidden text-slate-300 sm:inline">/</span>
          <span className="hidden max-w-48 truncate text-xs text-slate-500 sm:inline">
            {identity.workspace.name}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <nav
            aria-label={t("主导航")}
            className="flex rounded-xl bg-slate-100 p-1"
          >
            {nav.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                className={({ isActive }) =>
                  `flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm transition ${isActive ? "bg-white font-semibold text-slate-900 shadow-sm" : "text-slate-500 hover:text-slate-900"}`
                }
              >
                <item.icon className="size-4" />
                <span className="hidden min-[450px]:inline">{item.label}</span>
              </NavLink>
            ))}
          </nav>
          <button
            onClick={() => navigate("/settings/general")}
            title={t("设置")}
            aria-label={t("设置")}
            className="rounded-xl p-2 text-slate-600 hover:bg-slate-100"
          >
            <Settings className="size-5" />
          </button>
          <button
            onClick={() => setDrawer(true)}
            title={t("导航抽屉")}
            aria-label={t("导航抽屉")}
            className="rounded-xl p-2 text-slate-600 hover:bg-slate-100"
          >
            <Menu className="size-5" />
          </button>
        </div>
      </header>
      <div>{children}</div>
      <Sheet
        open={drawer}
        onOpenChange={setDrawer}
        side="right"
        title="CycleDock"
      >
        <div className="mb-6 rounded-2xl bg-slate-50 p-4">
          <p className="font-semibold">{identity.display_name}</p>
          <p className="truncate text-xs text-slate-500">
            {identity.email || identity.workspace.name}
          </p>
        </div>
        <div className="space-y-1">
          {[
            ...nav,
            { to: "/settings/general", label: t("设置"), icon: Settings },
          ].map((item) => (
            <button
              key={item.to}
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
        <p className="mt-6 flex items-center justify-center gap-2 text-xs text-slate-400">
          <Plus className="size-3" />
          Human + Agent <ExternalLink className="size-3" />
        </p>
      </Sheet>
    </div>
  );
}
