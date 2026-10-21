import { Suspense, lazy, useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Navigate,
  Route,
  Routes,
  useLocation,
  useParams,
} from "react-router-dom";
import { toast } from "sonner";
import { api, APIError } from "../shared/api/client";
import { keys, queryClient } from "./query";
import {
  DelayedSkeleton,
  ErrorState,
  SidebarSkeleton,
} from "../components/ui/feedback";
const LoginV2 = lazy(() =>
  import("../routes/login/LoginV2").then((m) => ({ default: m.LoginV2 })),
);
const ProjectFrame = lazy(() =>
  import("../components/layout/ProjectFrame").then((m) => ({
    default: m.ProjectFrame,
  })),
);
const SettingsV2 = lazy(() =>
  import("../routes/settings/SettingsV2").then((m) => ({
    default: m.SettingsV2,
  })),
);

/** OAuth 回跳结果处理一次；不信任 URL 参数作为成功凭证，只重新查询服务端状态。 */
function AuthenticatedV2() {
  const me = useQuery({ queryKey: keys.me, queryFn: api.me, retry: false });
  const location = useLocation();
  useEffect(() => {
    if (!me.data) return;
    const params = new URLSearchParams(location.search);
    if (!params.has("model_auth") && !params.has("agent_connections")) return;
    void queryClient.invalidateQueries({ queryKey: keys.auth });
    void queryClient.invalidateQueries({ queryKey: keys.nexus });
    toast.info("授权流程已返回，请在连接状态中核对结果");
    window.history.replaceState(null, "", location.pathname);
  }, [me.data, location.pathname, location.search]);

  if (me.isPending)
    return (
      <div className="v2 min-h-dvh">
        <DelayedSkeleton>
          <SidebarSkeleton />
        </DelayedSkeleton>
      </div>
    );
  if (me.error instanceof APIError && me.error.status === 401)
    return <LoginV2 />;
  if (me.error || !me.data)
    return (
      <div className="v2 min-h-dvh p-8">
        <ErrorState
          message={String(me.error || "身份信息不可用")}
          onRetry={() => void me.refetch()}
        />
      </div>
    );
  return (
    <Suspense
      fallback={
        <div className="v2 min-h-dvh">
          <DelayedSkeleton>
            <SidebarSkeleton />
          </DelayedSkeleton>
        </div>
      }
    >
      <Routes>
        <Route path="/" element={<ProjectStart mode="discussions" />} />
        <Route
          path="/projects/:projectID/discussions"
          element={<ProjectFrame me={me.data} page="discussion" />}
        />
        <Route
          path="/projects/:projectID/discussions/:cardID"
          element={<ProjectFrame me={me.data} page="card" />}
        />
        <Route
          path="/projects/:projectID/work"
          element={<ProjectFrame me={me.data} page="work" />}
        />
        <Route
          path="/projects/:projectID/work/:cardID"
          element={<ProjectFrame me={me.data} page="card" />}
        />
        <Route
          path="/settings/:section"
          element={<SettingsV2 me={me.data} />}
        />
        <Route
          path="/settings"
          element={<Navigate to="/settings/general" replace />}
        />
        <Route path="/board" element={<ProjectStart mode="work" />} />
        <Route path="/chat" element={<ProjectStart mode="discussions" />} />
        <Route
          path="/board/:projectID"
          element={<OldProjectRedirect mode="work" />}
        />
        <Route
          path="/chat/:projectID/:cardID"
          element={<OldCardRedirect mode="discussions" />}
        />
        <Route
          path="/cards/:projectID/:cardID"
          element={<OldCardRedirect mode="work" />}
        />
        <Route
          path="/projects/:projectID"
          element={<OldProjectRedirect mode="discussions" />}
        />
        <Route
          path="/projects/:projectID/cards/:cardID"
          element={<OldCardRedirect mode="work" />}
        />
        <Route path="*" element={<ProjectStart mode="discussions" />} />
      </Routes>
    </Suspense>
  );
}

function ProjectStart({ mode }: { mode: "discussions" | "work" }) {
  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  if (projects.isPending)
    return (
      <div className="v2 min-h-dvh">
        <DelayedSkeleton>
          <SidebarSkeleton />
        </DelayedSkeleton>
      </div>
    );
  if (projects.isError)
    return (
      <div className="v2 min-h-dvh p-8">
        <ErrorState
          message={String(projects.error)}
          onRetry={() => void projects.refetch()}
        />
      </div>
    );
  if (!projects.data?.length)
    return <ProjectFrame page="discussion" startEmpty />;
  return <Navigate to={`/projects/${projects.data[0].id}/${mode}`} replace />;
}

function OldProjectRedirect({ mode }: { mode: "discussions" | "work" }) {
  const { projectID } = useParams();
  return <Navigate to={`/projects/${projectID}/${mode}`} replace />;
}
function OldCardRedirect({ mode }: { mode: "discussions" | "work" }) {
  const { projectID, cardID } = useParams();
  return <Navigate to={`/projects/${projectID}/${mode}/${cardID}`} replace />;
}

export function AppV2() {
  return <AuthenticatedV2 />;
}
