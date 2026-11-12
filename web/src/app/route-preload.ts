export const loadChatHome = () => import("../features/chat/ChatHome");
export const loadBoardPage = () => import("../features/board/BoardPage");
export const loadPlanPage = () => import("../features/plans/PlanPage");
export const loadActivityPage = () =>
  import("../features/activity/ActivityPage");
export const loadCardPage = () => import("../features/cards/CardPage");
export const loadSettingsPage = () =>
  import("../features/settings/SettingsPage");
export const loadNexusDockPage = () =>
  import("../features/nexusdock/NexusDockPage");

// 按路径提前拉取对应路由分包，使首屏身份校验（/api/v1/me）与页面 JS 下载并行执行。
export function preloadRouteForPathname(pathname: string) {
  if (
    pathname === "/" ||
    pathname === "/chat" ||
    pathname === "/chat/"
  ) {
    void loadChatHome();
    return;
  }
  if (
    /^\/(?:chat|cards|plans)\/[^/]+\/[^/]+/.test(pathname) ||
    /^\/projects\/[^/]+\/cards\/[^/]+/.test(pathname)
  ) {
    void loadCardPage();
    return;
  }
  if (pathname.startsWith("/board") || pathname.startsWith("/projects/")) {
    void loadBoardPage();
    return;
  }
  if (pathname.startsWith("/plans")) {
    void loadPlanPage();
    return;
  }
  if (pathname.startsWith("/activity")) {
    void loadActivityPage();
    return;
  }
  if (pathname.startsWith("/nexusdock")) {
    void loadNexusDockPage();
    return;
  }
  if (pathname.startsWith("/settings")) {
    void loadSettingsPage();
  }
}
