import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";
import { APIError } from "../shared/api/client";

export const keys = {
  me: ["me"] as const,
  workspaces: ["workspaces"] as const,
  projects: ["projects"] as const,
  cards: (id: string) => ["cards", id] as const,
  mainDiscussion: (id: string) => ["main-discussion", id] as const,
  card: (id: string) => ["card", id] as const,
  schedules: (id: string) => ["project-schedules", id] as const,
  activity: (id: string, sourceID = "") =>
    ["project-activity", id, sourceID] as const,
  activityAgents: (id: string) => ["activity-agents", id] as const,
  agent: ["agent-settings"] as const,
  agentStatus: ["agent-status"] as const,
  auth: ["model-auth"] as const,
  authorizedAIClients: ["authorized-ai-clients"] as const,
  models: (provider: string, credentialID = "") =>
    ["models", provider, credentialID] as const,
  providerModels: (baseURL: string) =>
    ["provider-models", baseURL] as const,
  mcp: ["mcp"] as const,
  nexus: ["nexus"] as const,
  nexusDevices: (workspaceID: string) =>
    ["nexus", "devices", workspaceID] as const,
  nexusFleet: (workspaceID: string) => ["nexus", "fleet", workspaceID] as const,
  deviceToolGuards: (workspaceID: string) =>
    ["device-tool-guards", workspaceID] as const,
};

function isSessionUnauthorized(error: unknown): error is APIError {
  return (
    error instanceof APIError &&
    error.status === 401 &&
    error.code === "unauthorized"
  );
}

// 任意业务查询或写入返回 401 unauthorized 时，立即触发顶层 keys.me 重新校验，
// 让会话过期统一收敛回 LoginPage，避免子页面单独渲染 "authentication required" 红框。
export const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (error, query) => {
      if (query.queryKey[0] !== keys.me[0] && isSessionUnauthorized(error)) {
        void queryClient.invalidateQueries({ queryKey: keys.me });
      }
    },
  }),
  mutationCache: new MutationCache({
    onError: (error) => {
      if (isSessionUnauthorized(error)) {
        void queryClient.invalidateQueries({ queryKey: keys.me });
      }
    },
  }),
  defaultOptions: {
    queries: {
      retry: (failureCount, error) => {
        if (error instanceof APIError && error.status === 401) {
          return false;
        }
        return failureCount < 1;
      },
      staleTime: 5000,
      refetchOnWindowFocus: true,
    },
  },
});
