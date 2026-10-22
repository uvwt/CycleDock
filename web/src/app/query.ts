import { QueryClient } from "@tanstack/react-query";
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, staleTime: 5000, refetchOnWindowFocus: true },
  },
});
export const keys = {
  me: ["me"] as const,
  workspaces: ["workspaces"] as const,
  projects: ["projects"] as const,
  cards: (id: string) => ["cards", id] as const,
  card: (id: string) => ["card", id] as const,
  schedules: (id: string) => ["project-schedules", id] as const,
  activity: (id: string) => ["project-activity", id] as const,
  agent: ["agent-settings"] as const,
  auth: ["model-auth"] as const,
  models: (provider: string, credentialID = "") =>
    ["models", provider, credentialID] as const,
  mcp: ["mcp"] as const,
  nexus: ["nexus"] as const,
};
