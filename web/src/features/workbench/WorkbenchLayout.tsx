import { useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import {
  ClipboardCheck,
  LayoutDashboard,
  MessageSquareText,
  PanelLeftClose,
  PanelLeftOpen,
  Repeat,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Card } from "../../shared/api/types";
import { Sheet } from "../../shared/ui/components";
import { DiscussionSidebar } from "../chat/DiscussionSidebar";
import { WorkbenchFooter } from "./WorkbenchFooter";

const COLLAPSE_KEY = "cycledock.sidebar.collapsed";
type Mode = "discussion" | "work";

function initialCollapsed() {
  return typeof window !== "undefined" &&
    window.localStorage.getItem(COLLAPSE_KEY) === "1";
}

export function WorkbenchLayout({
  mode,
  projectID,
  cards,
  activeDiscussionID,
  creatingDiscussion,
  onSelectDiscussion,
  onCreateDiscussion,
  fill = false,
  children,
}: {
  mode: Mode;
  projectID: string;
  cards: Card[];
  activeDiscussionID?: string;
  creatingDiscussion?: boolean;
  onSelectDiscussion?: (card: Card) => void;
  onCreateDiscussion?: () => void;
  fill?: boolean;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(initialCollapsed);
  const [mobileOpen, setMobileOpen] = useState(false);
  const discussions = cards.filter((card) => card.kind === "discussion");
  const activeView = new URLSearchParams(location.search).get("view") || "all";

  function setDesktopCollapsed(value: boolean) {
    setCollapsed(value);
    window.localStorage.setItem(COLLAPSE_KEY, value ? "1" : "0");
  }

  function go(to: string) {
    setMobileOpen(false);
    navigate(to);
  }

  const sidebar = (
    <aside className="flex h-full min-h-0 flex-col bg-slate-50" aria-label={t("主导航")}>
      <div className="shrink-0 border-b border-slate-200 px-3 pt-3 pb-3">
        <div className="mb-4 flex items-center justify-between px-1">
          <button
            type="button"
            onClick={() => go(projectID ? "/chat?project=" + encodeURIComponent(projectID) : "/chat")}
            className="flex items-center gap-2 text-left"
            title="CycleDock"
          >
            <img src="/cycledock-logo.png" alt="" className="size-8 shrink-0" />
            <span className="text-sm font-semibold tracking-tight text-slate-900">CycleDock</span>
          </button>
          <button
            type="button"
            onClick={() => setDesktopCollapsed(true)}
            title={t("收起讨论列表")}
            aria-label={t("收起讨论列表")}
            className="hidden size-8 place-items-center rounded-lg text-slate-400 hover:bg-white lg:grid"
          >
            <PanelLeftClose className="size-4" />
          </button>
        </div>
        <nav className="grid grid-cols-2 gap-1 rounded-lg bg-slate-200/70 p-1" aria-label={t("主导航")}>
          <button
            type="button"
            onClick={() => go(projectID ? "/chat?project=" + encodeURIComponent(projectID) : "/chat")}
            className={`flex items-center justify-center gap-2 rounded-lg px-2 py-2 text-[13px] transition-colors ${mode === "discussion" ? "bg-white font-medium text-slate-900 ring-1 ring-slate-200" : "text-slate-500 hover:text-slate-800"}`}
            aria-current={mode === "discussion" ? "page" : undefined}
          >
            <MessageSquareText className="size-4" />{t("讨论")}
          </button>
          <button
            type="button"
            onClick={() => go(projectID ? "/board/" + projectID : "/board")}
            className={`flex items-center justify-center gap-2 rounded-lg px-2 py-2 text-[13px] transition-colors ${mode === "work" ? "bg-white font-medium text-slate-900 ring-1 ring-slate-200" : "text-slate-500 hover:text-slate-800"}`}
            aria-current={mode === "work" ? "page" : undefined}
          >
            <LayoutDashboard className="size-4" />{t("工作")}
          </button>
        </nav>
      </div>

      {mode === "discussion" ? (
        <DiscussionSidebar
          discussions={discussions}
          allCards={cards}
          activeCardID={activeDiscussionID}
          creating={creatingDiscussion}
          onSelect={(card) => {
            setMobileOpen(false);
            onSelectDiscussion?.(card);
          }}
          onCreate={() => {
            setMobileOpen(false);
            onCreateDiscussion?.();
          }}
        />
      ) : (
        <nav aria-label={t("工作视图")} className="min-h-0 flex-1 space-y-1 overflow-y-auto px-3 py-4">
          <p className="px-3 pb-2 text-[11px] font-medium text-slate-400">{t("工作视图")}</p>
          {[
            { id: "all", label: t("全部工作"), icon: LayoutDashboard, count: cards.filter((c) => c.kind !== "discussion").length },
            { id: "review", label: t("待我审核"), icon: ClipboardCheck, count: cards.filter((c) => c.kind === "todo" && c.todo_state === "review").length },
            { id: "ongoing", label: t("持续事项"), icon: Repeat, count: cards.filter((c) => c.kind === "ongoing").length },
          ].map((view) => (
            <button
              key={view.id}
              type="button"
              onClick={() => go("/board/" + projectID + (view.id === "all" ? "" : "?view=" + view.id))}
              className={`flex w-full items-center gap-2.5 rounded-lg px-3 py-2.5 text-left text-[13px] transition-colors ${activeView === view.id ? "bg-white font-medium text-slate-900 ring-1 ring-slate-200" : "text-slate-600 hover:bg-white/80"}`}
              aria-current={activeView === view.id ? "page" : undefined}
            >
              <view.icon className="size-4 shrink-0 text-slate-500" />
              <span className="min-w-0 flex-1 truncate">{view.label}</span>
              <span className="text-xs text-slate-400">{view.count}</span>
            </button>
          ))}
        </nav>
      )}

      <WorkbenchFooter mode={mode} />
    </aside>
  );

  return (
    <div className={`relative min-w-0 overflow-x-hidden ${fill ? "h-dvh min-h-0" : "min-h-dvh"} ${collapsed ? "lg:block" : "lg:grid lg:grid-cols-[264px_minmax(0,1fr)]"}`}>
      {!collapsed && (
        <div className={`hidden border-r border-slate-200 lg:block ${fill ? "h-full min-h-0" : "sticky top-0 h-dvh self-start"}`}>
          {sidebar}
        </div>
      )}
      <div className={`relative min-w-0 ${fill ? "min-h-0 overflow-hidden" : ""}`}>
        <button
          type="button"
          onClick={() => {
            if (window.matchMedia("(min-width: 1024px)").matches) setDesktopCollapsed(false);
            else setMobileOpen(true);
          }}
          title={t("打开导航")}
          aria-label={t("打开导航")}
          className={`absolute top-3 left-3 z-40 grid size-9 place-items-center rounded-lg border border-slate-200 bg-white text-slate-600 shadow-sm hover:bg-slate-50 ${collapsed ? "" : "lg:hidden"}`}
        >
          <PanelLeftOpen className="size-4" />
        </button>
        {children}
      </div>
      <Sheet open={mobileOpen} onOpenChange={setMobileOpen} title="CycleDock" side="left">
        <div className="-m-5 h-[calc(100dvh-4.25rem)]">{sidebar}</div>
      </Sheet>
    </div>
  );
}
