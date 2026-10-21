import { useState, type ReactNode } from "react";
import { PanelLeftOpen } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Card } from "../../shared/api/types";
import { Sheet } from "../../shared/ui/components";
import { DiscussionSidebar } from "../chat/DiscussionSidebar";

const COLLAPSE_KEY = "cycledock.discussions.collapsed";

function initialCollapsed() {
  if (typeof window === "undefined") return false;
  return window.localStorage.getItem(COLLAPSE_KEY) === "1";
}

export function WorkbenchLayout({
  cards,
  activeDiscussionID,
  creatingDiscussion,
  onSelectDiscussion,
  onCreateDiscussion,
  fill = false,
  children,
}: {
  cards: Card[];
  activeDiscussionID?: string;
  creatingDiscussion?: boolean;
  onSelectDiscussion: (card: Card) => void;
  onCreateDiscussion: () => void;
  fill?: boolean;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const [collapsed, setCollapsed] = useState(initialCollapsed);
  const [mobileOpen, setMobileOpen] = useState(false);
  const discussions = cards.filter((card) => card.kind === "discussion");

  function setDesktopCollapsed(value: boolean) {
    setCollapsed(value);
    window.localStorage.setItem(COLLAPSE_KEY, value ? "1" : "0");
  }

  const sidebar = (
    <DiscussionSidebar
      discussions={discussions}
      allCards={cards}
      activeCardID={activeDiscussionID}
      creating={creatingDiscussion}
      onSelect={(card) => {
        setMobileOpen(false);
        onSelectDiscussion(card);
      }}
      onCreate={() => {
        setMobileOpen(false);
        onCreateDiscussion();
      }}
      onCollapse={() => setDesktopCollapsed(true)}
    />
  );

  return (
    <div
      className={`relative min-w-0 ${
        fill ? "h-[calc(100dvh-3.5rem)] min-h-0" : "min-h-[calc(100dvh-3.5rem)]"
      } ${collapsed ? "lg:block" : "lg:grid lg:grid-cols-[280px_minmax(0,1fr)]"}`}
    >
      {!collapsed && (
        <div
          className={`hidden border-r border-slate-200 lg:block ${
            fill
              ? "h-full min-h-0"
              : "sticky top-14 h-[calc(100dvh-3.5rem)] self-start"
          }`}
        >
          {sidebar}
        </div>
      )}

      <div
        className={`relative min-w-0 ${fill ? "min-h-0 overflow-hidden" : ""}`}
      >
        <button
          type="button"
          onClick={() => {
            if (window.matchMedia("(min-width: 1024px)").matches) {
              setDesktopCollapsed(false);
            } else {
              setMobileOpen(true);
            }
          }}
          title={t("打开讨论列表")}
          aria-label={t("打开讨论列表")}
          className={`absolute top-3 left-3 z-20 grid size-9 place-items-center rounded-xl border border-slate-200 bg-white/95 text-slate-600 shadow-sm backdrop-blur hover:bg-slate-50 ${
            collapsed ? "" : "lg:hidden"
          }`}
        >
          <PanelLeftOpen className="size-4" />
        </button>
        {children}
      </div>

      <Sheet
        open={mobileOpen}
        onOpenChange={setMobileOpen}
        title={t("项目讨论")}
        side="left"
      >
        <div className="-m-5 h-[calc(100dvh-4.25rem)]">{sidebar}</div>
      </Sheet>
    </div>
  );
}
