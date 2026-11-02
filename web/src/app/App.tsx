import { DeviceSelectionProvider } from "../features/devices/DeviceSelectionProvider";
import { Suspense, lazy, useEffect } from "react";
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
import { loadPushPublicKey } from "../shared/realtime/notifications";
import { Shell } from "../layouts/Shell";
import { LoginPage } from "./LoginPage";
import { ProjectProvider } from "./ProjectContext";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
const ChatHome = lazy(() => import("../features/chat/ChatHome"));
const BoardPage = lazy(() => import("../features/board/BoardPage"));
const PlanPage = lazy(() => import("../features/plans/PlanPage"));
const ActivityPage = lazy(() => import("../features/activity/ActivityPage"));
const CardPage = lazy(() => import("../features/cards/CardPage"));
const SettingsPage = lazy(() => import("../features/settings/SettingsPage"));
const NexusDockPage = lazy(
  () => import("../features/nexusdock/NexusDockPage"),
);

function Authenticated() {
  const { t } = useTranslation();
  const me = useQuery({ queryKey: keys.me, queryFn: api.me, retry: false });
  const location = useLocation();
  const navigate = useNavigate();
  useCardNotifications(me.data?.workspace.id || "");

  // 监听来自 Service Worker 的通知点击跳转（热启动/前台）以及冷启动拉取（未被消费的 pending intent）
  useEffect(() => {
    if (!("serviceWorker" in navigator) || !navigator.serviceWorker) return;

    const navigateToTarget = (rawURL?: string | null) => {
      if (!rawURL) return;
      try {
        const parsed = new URL(rawURL, window.location.origin);
        if (parsed.origin !== window.location.origin) return;
        const targetPath = parsed.pathname + parsed.search + parsed.hash;
        const currentPath =
          window.location.pathname + window.location.search + window.location.hash;
        if (targetPath && targetPath !== currentPath) {
          navigate(targetPath);
        }
      } catch {
        // 忽略异常 URL
      }
    };

    const handleMessage = (event: MessageEvent) => {
      if (
        event.data?.type === "CYCLE_NAVIGATE" ||
        event.data?.type === "CYCLE_PENDING_NAVIGATION"
      ) {
        navigateToTarget(event.data.url);
      }
    };

    if (typeof navigator.serviceWorker.addEventListener === "function") {
      navigator.serviceWorker.addEventListener("message", handleMessage);
    }

    // 冷启动 / 页面加载就绪时，主动向 Service Worker 查询是否有未消费的通知点击意图
    const checkPendingNavigation = async () => {
      try {
        if (!navigator.serviceWorker.ready) return;
        const registration = await navigator.serviceWorker.ready;
        const targetWorker =
          registration?.active || navigator.serviceWorker.controller;
        if (!targetWorker || typeof targetWorker.postMessage !== "function") return;

        if (typeof MessageChannel !== "undefined") {
          const channel = new MessageChannel();
          channel.port1.onmessage = (event) => {
            if (event.data?.type === "CYCLE_PENDING_NAVIGATION") {
              navigateToTarget(event.data.url);
            }
          };
          targetWorker.postMessage(
            { type: "CYCLE_CHECK_PENDING_NAVIGATION" },
            [channel.port2],
          );
        } else {
          targetWorker.postMessage({ type: "CYCLE_CHECK_PENDING_NAVIGATION" });
        }
      } catch {
        // 忽略 SW 通信异常
      }
    };

    void checkPendingNavigation();

    return () => {
      if (typeof navigator.serviceWorker?.removeEventListener === "function") {
        navigator.serviceWorker.removeEventListener("message", handleMessage);
      }
    };
  }, [navigate]);

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
      <main className="grid min-h-screen place-items-center">
        <Spinner text={t("正在验证会话…")} />
      </main>
    );
  if (me.error instanceof APIError && me.error.status === 401)
    return <LoginPage />;
  if (me.error || !me.data)
    return (
      <main className="mx-auto mt-20 max-w-xl p-4">
        <ErrorView error={me.error} onRetry={() => void me.refetch()} />
      </main>
    );
  return (
    <ProjectProvider>
      <DeviceSelectionProvider key={me.data.workspace.id}>
        <Shell>
          <NotificationPermissionPrompt />
          <Suspense
            fallback={
              <div className="grid min-h-[50vh] place-items-center">
                <Spinner />
              </div>
            }
          >
            <Routes>
              <Route path="/" element={<Navigate to="/chat" replace />} />
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
function LegacyCardRedirect() {
  const location = useLocation();
  const parts = location.pathname.split("/");
  return <Navigate to={`/cards/${parts[2]}/${parts[4]}`} replace />;
}
export function App() {
  return <Authenticated />;
}
