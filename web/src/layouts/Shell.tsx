import type { ReactNode } from "react";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { BriefcaseBusiness, MessageCircle, Settings } from "lucide-react";
import { useTranslation } from "react-i18next";

export function Shell({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const focusedConversation =
    /^\/(?:chat|cards)\/[^/]+\/[^/]+\/?$/.test(location.pathname);

  const primaryNav = [
    { to: "/chat", label: t("聊天"), icon: MessageCircle },
    { to: "/board", label: t("工作"), icon: BriefcaseBusiness },
  ];

  function primaryActive(to: string, routerActive: boolean) {
    if (to === "/board") {
      return routerActive || location.pathname.startsWith("/cards/");
    }
    return routerActive;
  }

  const logo = (
    <img
      src="/cycledock-logo.png"
      alt=""
      className="size-8 object-contain"
      draggable={false}
    />
  );

  return (
    <div className="min-h-screen bg-slate-50 text-slate-800 md:flex">
      <aside className="sticky top-0 hidden h-screen w-16 shrink-0 flex-col items-center border-r border-slate-200 bg-white py-3 md:flex">
        <button
          type="button"
          onClick={() => navigate("/chat")}
          title="CycleDock"
          aria-label="CycleDock"
          className="grid size-10 place-items-center rounded-xl transition hover:bg-slate-50"
        >
          {logo}
        </button>

        <nav
          aria-label={t("主导航")}
          className="mt-6 flex w-full flex-col items-center gap-1"
        >
          {primaryNav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              title={item.label}
              aria-label={item.label}
              className={({ isActive }) =>
                `grid size-10 place-items-center rounded-xl transition ${
                  primaryActive(item.to, isActive)
                    ? "bg-slate-100 text-slate-900"
                    : "text-slate-400 hover:bg-slate-50 hover:text-slate-800"
                }`
              }
            >
              <item.icon className="size-5" />
            </NavLink>
          ))}
        </nav>

        <div className="mt-auto">
          <NavLink
            to="/settings/general"
            title={t("设置")}
            aria-label={t("设置")}
            className={({ isActive }) =>
              `grid size-10 place-items-center rounded-xl transition ${
                isActive
                  ? "bg-slate-100 text-slate-900"
                  : "text-slate-400 hover:bg-slate-50 hover:text-slate-800"
              }`
            }
          >
            <Settings className="size-5" />
          </NavLink>
        </div>
      </aside>

      <div className="min-w-0 flex-1">
        {!focusedConversation && (
          <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-slate-200 bg-white/95 px-3 backdrop-blur md:hidden">
            <button
              type="button"
              onClick={() => navigate("/chat")}
              title="CycleDock"
              aria-label="CycleDock"
              className="grid size-9 place-items-center rounded-xl"
            >
              {logo}
            </button>
            <nav aria-label={t("主导航")} className="flex items-center gap-1">
              {primaryNav.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  title={item.label}
                  aria-label={item.label}
                  className={({ isActive }) =>
                    `grid size-9 place-items-center rounded-xl transition ${
                      primaryActive(item.to, isActive)
                        ? "bg-slate-100 text-slate-900"
                        : "text-slate-400"
                    }`
                  }
                >
                  <item.icon className="size-[18px]" />
                </NavLink>
              ))}
              <NavLink
                to="/settings/general"
                title={t("设置")}
                aria-label={t("设置")}
                className={({ isActive }) =>
                  `grid size-9 place-items-center rounded-xl transition ${
                    isActive
                      ? "bg-slate-100 text-slate-900"
                      : "text-slate-400"
                  }`
                }
              >
                <Settings className="size-[18px]" />
              </NavLink>
            </nav>
          </header>
        )}
        {children}
      </div>
    </div>
  );
}
