import { QueryClient } from '@tanstack/react-query'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: false,
      staleTime: 10_000,
      refetchOnWindowFocus: false,
    },
  },
})

export const queryKeys = {
  me: ['me'] as const,
  loginMethods: ['login-methods'] as const,
  projects: ['projects'] as const,
  cards: (projectID: string) => ['projects', projectID, 'cards'] as const,
  card: (cardID: string) => ['cards', cardID] as const,
  agentSettings: ['agent-settings'] as const,
  agentNexusDock: ['agent-nexusdock'] as const,
  modelAuth: ['model-auth'] as const,
  agentModels: (provider: string) => ['agent-models', provider] as const,
  agentMCPConnections: ['agent-mcp-connections'] as const,
}
