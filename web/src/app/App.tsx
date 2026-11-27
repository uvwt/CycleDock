import { DeviceSelectionProvider } from "../features/devices/DeviceSelectionProvider";
import { ModelSelectionProvider } from "../features/models/ModelSelectionProvider";
import { Suspense, lazy, startTransition, useEffect, useRef, useState } from "react";
import {
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api, APIError } from "../shared/api/client";
import { keys, queryClient } from "./query";
import { ErrorView, Spinner } from "../shared/ui/components";
import { NotificationPermissionPrompt } from "../shared/realtime/NotificationPermissionPrompt";
import { useCardNotifications } from "../shared/realtime/useCardNotifications";
import {
  checkNotificationWorkerUpdate,
  loadPushPublicKey,
} from "../shared/realtime/notifications";
import { createPendingNavigationConsumer } from "../shared/realtime/pendingNavigation";
import { Shell } from "../layouts/Shell";
import { ProjectProvider } from "./ProjectContext";
import { useProject } from "./project-context";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import {
  loadActivityPage,
  loadBoardPage,
  loadCardPage,
  loadChatHome,
  loadNexusDockPage,
  loadPlanPage,
  loadSettingsPage,
  preloadRouteForPathname,
} from "./route-preload";

const LoginPage = lazy(() =>
  import("./LoginPage").then((m) => ({ default: m.LoginPage })),
);
const OAuthConsentPage = lazy(() =>
  import("./OAuthConsentPage").then((m) => ({ default: m.OAuthConsentPage })),
);
const ChatHome = lazy(loadChatHome);
const BoardPage = lazy(loadBoardPage);
const PlanPage = lazy(loadPlanPage);
const ActivityPage = lazy(loadActivityPage);
const CardPage = lazy(loadCardPage);
const SettingsPage = lazy(loadSettingsPage);
const NexusDockPage = lazy(loadNexusDockPage);

if (typeof window !== "undefined") {
  preloadRouteForPathname(window.location.pathname);
}

function Authenticated() {
  const { t } = useTranslation();
  const me = useQuery({ queryKey: keys.me, queryFn: api.me, retry: false });
  const location = useLocation();
  const navigate = useNavigate();
  const navigateRef = useRef(navigate);
  useEffect(() => {
    navigateRef.current = navigate;
  }, [navigate]);
  useCardNotifications(me.data?.workspace.id || "");

  // 根路由 `/` 跳转回 `/chat` 前必须先完成首次待跳转深链检查，
  // 防止 iOS PWA 冷启动回落 manifest.start_url ("/") 时首页默认重定向抢跑覆盖通知目标。
  const [pendingNavigationChecked, setPendingNavigationChecked] = useState(
    () => typeof window !== "undefined" && window.location.pathname !== "/",
  );

  // 监听 Service Worker 推送点击导航（前台/热启动）、冷启动双通道消费，
  // 以及 iOS PWA 从后台切回前台（visibilitychange / pageshow / focus）时的主动拉取。
  useEffect(() => {
    let disposed = false;
    const consumer = createPendingNavigationConsumer({
      onNavigate: (targetPath, { replace }) => {
        if (disposed) return;
        preloadRouteForPathname(targetPath);
        navigateRef.current(targetPath, { replace });
      },
    });

    const runPendingCheck = () => {
      void consumer.checkPending({
        replace: window.location.pathname === "/",
      });
    };

    const handleMessage = (event: MessageEvent) => {
      consumer.handleWorkerMessage(event.data, {
        replace: window.location.pathname === "/",
      });
    };

    const handleVisibilityChange = () => {
      if (document.visibilityState !== "visible") return;
      runPendingCheck();
      void checkNotificationWorkerUpdate();
    };

    const handlePageShow = () => {
      runPendingCheck();
    };

    const handleFocus = () => {
      runPendingCheck();
    };

    const hasServiceWorker = "serviceWorker" in navigator;
    if (hasServiceWorker) {
      navigator.serviceWorker.addEventListener("message", handleMessage);
      void checkNotificationWorkerUpdate();
    }
    document.addEventListener("visibilitychange", handleVisibilityChange);
    window.addEventListener("pageshow", handlePageShow);
    window.addEventListener("focus", handleFocus);

    void consumer
      .checkPending({ replace: window.location.pathname === "/" })
      .then((navigated) => {
        if (disposed) return;
        // 若已触发通知深链跳转，必须将放行状态放入 startTransition，
        // 避免 React 19 紧急更新抢跑在 React Router startTransition 之前挂载 <Navigate to="/chat" />。
        if (navigated) {
          startTransition(() => setPendingNavigationChecked(true));
          return;
        }
        setPendingNavigationChecked(true);
      })
      .catch(() => {
        if (!disposed) {
          setPendingNavigationChecked(true);
        }
      });

    return () => {
      disposed = true;
      if (hasServiceWorker) {
        navigator.serviceWorker.removeEventListener("message", handleMessage);
      }
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      window.removeEventListener("pageshow", handlePageShow);
      window.removeEventListener("focus", handleFocus);
    };
  }, []);

  useEffect(() => {
    if (me.data) void loadPushPublicKey().catch(() => undefined);
  }, [me.data]);
  useEffect(() => {
    if (!me.data) return;
    const params = new URLSearchParams(location.search);
    const model = params.get("model_auth");
    const connection = params.get("agent_connections");
    if (!model && !connection) return;
    if (model === "chatgpt_connected" || connection === "nexusdock-connected") {
      toast.success("账号连接成功");
    } else if (connection === "nexusdock-invalid-state") {
      toast.warning("设备授权已过期或重复使用，请重新发起授权。");
    } else if (connection === "nexusdock-denied") {
      toast.info("已取消 NexusDock 设备授权，可随时在 NexusDock 页面中重新连接。");
    } else if (model || connection) {
      toast.warning("授权未完成，请检查连接状态");
    }
    void queryClient.invalidateQueries({ queryKey: keys.auth });
    void queryClient.invalidateQueries({ queryKey: keys.nexus });
    navigate(connection ? "/nexusdock" : "/settings/models", {
      replace: true,
    });
  }, [me.data, location.search, navigate]);
  if (me.isPending)
    return (
      <main className="grid h-full min-h-0 place-items-center">
        <Spinner text={t("正在验证会话…")} />
      </main>
    );
  if (me.error instanceof APIError && me.error.status === 401)
    return (
      <Suspense
        fallback={
          <main className="grid h-full min-h-0 place-items-center">
            <Spinner text={t("正在加载登录页…")} />
          </main>
        }
      >
        <LoginPage />
      </Suspense>
    );
  // 仅当首次加载尚无身份数据时展示全屏错误；后台重刷若偶发网络波动，保留当前已渲染的工作台与草稿。
  if (!me.data)
    return (
      <main className="mx-auto mt-20 w-full max-w-xl p-4">
        <ErrorView error={me.error} onRetry={() => void me.refetch()} />
      </main>
    );
  // MCP OAuth 授权确认页使用独立居中布局，不包裹侧边栏 Shell，避免主工作台干扰。
  if (location.pathname === "/oauth/consent") {
    return (
      <Suspense
        fallback={
          <main className="grid h-full min-h-0 place-items-center bg-canvas">
            <Spinner text={t("正在加载授权页…")} />
          </main>
        }
      >
        <OAuthConsentPage />
      </Suspense>
    );
  }
  return (
    <ProjectProvider>
      <DeviceSelectionProvider key={me.data.workspace.id}>
        <ModelSelectionProvider key={me.data.workspace.id}>
          <Shell>
            <NotificationPermissionPrompt />
            <Suspense
              fallback={
                <div className="flex h-full min-h-0 w-full animate-pulse p-6">
                  <div className="mx-auto w-full max-w-5xl space-y-6">
                    <div className="flex items-center justify-between">
                      <div className="h-8 w-44 rounded-xl bg-muted/70" />
                      <div className="h-8 w-24 rounded-lg bg-muted/60" />
                    </div>
                    <div className="h-32 w-full rounded-2xl bg-muted/40" />
                    <div className="h-64 w-full rounded-2xl bg-muted/30" />
                  </div>
                </div>
              }
            >
              <Routes>
              <Route
                path="/"
                element={
                  <RootRoute ready={pendingNavigationChecked} />
                }
              />
              <Route path="/chat" element={<ChatHome />} />
              <Route path="/chat/:projectID/:cardID" element={<CardPage />} />
              <Route path="/board" element={<BoardPage />} />
              <Route path="/board/:projectID" element={<BoardPage />} />
              <Route path="/plans" element={<PlanPage />} />
              <Route path="/plans/:projectID" element={<PlanPage />} />
              <Route path="/plans/:projectID/:cardID" element={<CardPage />} />
              <Route path="/activity" element={<ActivityPage />} />
              <Route path="/activity/:projectID" element={<ActivityPage />} />
              <Route path="/nexusdock" element={<NexusDockPage />} />
              <Route path="/cards/:cardID" element={<CardIDRedirect />} />
              <Route path="/chat/:cardID" element={<CardIDRedirect mode="chat" />} />
              <Route path="/cards/:projectID/:cardID" element={<CardPage />} />
              <Route
                path="/settings/*"
                element={<SettingsPage role={me.data.role} />}
              />
              <Route
                path="/projects/:projectID/cards/:cardID"
                element={<LegacyCardRedirect />}
              />
              <Route
                path="/projects/:projectID"
                element={<LegacyBoardRedirect />}
              />
              <Route path="*" element={<Navigate to="/chat" replace />} />
            </Routes>
          </Suspense>
        </Shell>
        </ModelSelectionProvider>
      </DeviceSelectionProvider>
    </ProjectProvider>
  );
}
function LegacyBoardRedirect() {
  const location = useLocation();
  return (
    <Navigate to={location.pathname.replace("/projects/", "/board/")} replace />
  );
}

function RootRoute({ ready }: { ready: boolean }) {
  const { t } = useTranslation();
  // 当通知深链已通过 history.replaceState 改写浏览器地址、React Router 仍在提交 transition 时，
  // 保持加载态，绝不渲染 <Navigate to="/chat" replace /> 将目标深链冲回首页。
  const deepLinkInFlight =
    typeof window !== "undefined" && window.location.pathname !== "/";
  if (!ready || deepLinkInFlight) {
    return (
      <main className="grid h-full min-h-0 place-items-center">
        <Spinner text={t("正在加载…")} />
      </main>
    );
  }
  return <Navigate to="/chat" replace />;
}
function LegacyCardRedirect() {
  const location = useLocation();
  const parts = location.pathname.split("/");
  return <Navigate to={`/cards/${parts[2]}/${parts[4]}`} replace />;
}

function CardIDRedirect({ mode = "cards" }: { mode?: "cards" | "chat" }) {
  const location = useLocation();
  const { projectID } = useProject();
  const parts = location.pathname.split("/").filter(Boolean);
  const cardID = parts[1] || "";
  if (!cardID) {
    return <Navigate to="/chat" replace />;
  }
  if (!projectID) {
    return <Navigate to="/chat" replace />;
  }
  const target =
    mode === "chat"
      ? `/chat/${encodeURIComponent(projectID)}/${encodeURIComponent(cardID)}`
      : `/cards/${encodeURIComponent(projectID)}/${encodeURIComponent(cardID)}`;
  return <Navigate to={`${target}${location.search}${location.hash}`} replace />;
}
export function App() {
  return <Authenticated />;
}
