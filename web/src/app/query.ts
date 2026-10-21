import { QueryClient } from "@tanstack/react-query";
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, staleTime: 5000, refetchOnWindowFocus: true },
  },
});
export const keys = {
  me: ["me"] as const,
  projects: ["projects"] as const,
  cards: (id: string) => ["cards", id] as const,
  card: (id: string) => ["card", id] as const,
  agent: ["agent-settings"] as const,
  auth: ["model-auth"] as const,
  models: (id: string) => ["models", id] as const,
  mcp: ["mcp"] as const,
  nexus: ["nexus"] as const,
};
