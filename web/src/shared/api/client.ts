import type {
  AgentSettings,
  Card,
  CardDetail,
  GrokStart,
  Identity,
  LoginMethods,
  MCPConnection,
  MCPInput,
  ModelAuth,
  ModelAuthStart,
  NexusConnection,
  Project,
  ProjectActivity,
  ProjectSchedule,
  Provider,
  Schedule,
  ScheduleInput,
  TestResult,
  TodoState,
  Workspace,
} from "./types";

export class APIError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}
async function request<T>(
  path: string,
  method = "GET",
  body?: unknown,
  idempotency?: boolean | string,
): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (idempotency)
    headers["Idempotency-Key"] =
      typeof idempotency === "string" ? idempotency : crypto.randomUUID();
  const response = await fetch("/api/v1" + path, {
    method,
    credentials: "same-origin",
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!response.ok) {
    const error = (await response.json().catch(() => null)) as {
      error?: { code?: string; message?: string };
    } | null;
    throw new APIError(
      response.status,
      error?.error?.code ?? "http_error",
      error?.error?.message ?? `HTTP ${response.status}`,
    );
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}
export const api = {
  loginMethods: () => request<LoginMethods>("/auth/methods"),
  login: (password: string) =>
    request<Identity>("/auth/login", "POST", { password }),
  logout: () => request<void>("/auth/logout", "POST", {}),
  me: () => request<Identity>("/me"),
  workspaces: () => request<Workspace[]>("/workspaces"),
  switchWorkspace: (id: string) =>
    request<Identity>(`/workspaces/${id}/switch`, "POST", {}),
  projects: () => request<Project[]>("/projects"),
  createProject: (value: {
    name: string;
    description: string;
    instructions: string;
  }) => request<Project>("/projects", "POST", value),
  updateProject: (
    id: string,
    value: { name: string; description: string; instructions: string },
  ) => request<Project>(`/projects/${id}`, "PATCH", value),
  cards: (project: string) => request<Card[]>(`/projects/${project}/cards`),
  projectSchedules: (project: string) =>
    request<ProjectSchedule[]>(`/projects/${project}/schedules`),
  projectActivity: (project: string, limit = 60) =>
    request<ProjectActivity[]>(
      `/projects/${project}/activity?limit=${encodeURIComponent(String(limit))}`,
    ),
  createDiscussion: (project: string) =>
    request<Card>(`/projects/${project}/discussions`, "POST", {}),
  createTodo: (project: string, title: string, description: string) =>
    request<Card>(`/projects/${project}/todos`, "POST", {
      title,
      description,
    }),
  card: (id: string) => request<CardDetail>(`/cards/${id}`),
  updateCard: (id: string, title: string, description: string) =>
    request<Card>(`/cards/${id}`, "PATCH", { title, description }),
  moveTodo: (id: string, state: TodoState) =>
    request<Card>(`/cards/${id}/move`, "POST", { state }),
  archiveDiscussion: (id: string) =>
    request<Card>(`/cards/${id}/discussion-state`, "POST", {
      state: "archived",
    }),
  deleteDiscussion: (id: string) => request<void>(`/cards/${id}`, "DELETE"),
  ongoingState: (id: string, state: string) =>
    request<Card>(`/cards/${id}/ongoing-state`, "POST", { state }),
  send: (
    id: string,
    content: string,
    dispatch_mode: "enqueue" | "interrupt",
    idempotencyKey?: string,
  ) =>
    request<{ event_id: string; turn_id: string }>(
      `/cards/${id}/comments`,
      "POST",
      { content, dispatch_mode },
      idempotencyKey || true,
    ),
  reply: (id: string, content: string) =>
    request<{ event_id: string; turn_id: string }>(
      `/cards/${id}/reply`,
      "POST",
      { content },
      true,
    ),
  cancelAttempt: (id: string, attemptID: string) =>
    request<void>(`/cards/${id}/attempts/${attemptID}/cancel`, "POST", {}),
  cancelTurn: (id: string, turnID: string) =>
    request<void>(`/cards/${id}/turns/${turnID}/cancel`, "POST", {}),
  retryAttempt: (id: string, attemptID: string) =>
    request<{ attempt_id: string }>(
      `/cards/${id}/attempts/${attemptID}/retry`,
      "POST",
      {},
    ),
  createSchedule: (id: string, input: ScheduleInput) =>
    request<Schedule>(`/cards/${id}/schedules`, "POST", input),
  updateSchedule: (id: string, schedule: string, input: ScheduleInput) =>
    request<Schedule>(`/cards/${id}/schedules/${schedule}`, "PUT", input),
  deleteSchedule: (id: string, schedule: string) =>
    request<void>(`/cards/${id}/schedules/${schedule}`, "DELETE"),
  agentSettings: () => request<AgentSettings>("/agent/settings"),
  saveAgent: (x: Omit<AgentSettings, "configured" | "source_id">) =>
    request<AgentSettings>("/agent/settings", "PUT", x),
  authStatuses: () => request<ModelAuth[]>("/agent/model-auth"),
  models: (p: "chatgpt" | "grok", credentialID?: string) =>
    request<{ id: string; name: string }[]>(
      `/agent/models?provider=${p}${credentialID ? `&credential_id=${encodeURIComponent(credentialID)}` : ""}`,
    ),
  chatgptStart: () =>
    request<ModelAuthStart>("/agent/model-auth/chatgpt/start", "POST", {}),
  chatgptComplete: (url: string) =>
    request<void>("/agent/model-auth/chatgpt/complete", "POST", {
      callback_url: url,
    }),
  grokStart: () =>
    request<GrokStart>("/agent/model-auth/grok/start", "POST", {}),
  grokPoll: (id: string) =>
    request<{ status: string }>("/agent/model-auth/grok/poll", "POST", {
      flow_id: id,
    }),
  modelDisconnect: (p: string, credentialID: string) =>
    request<void>(
      `/agent/model-auth/${p}/${encodeURIComponent(credentialID)}`,
      "DELETE",
    ),
  testProvider: (value: {
    provider_type: Provider;
    credential_id: string;
    provider_base_url: string;
    model: string;
    api_key_env: string;
  }) => request<TestResult>("/agent/provider-test", "POST", value),
  nexus: () => request<NexusConnection>("/agent/nexusdock"),
  nexusConnect: () =>
    request<{ authorization_url: string }>(
      "/agent/nexusdock/connect",
      "POST",
      {},
    ),
  nexusDisconnect: () => request<void>("/agent/nexusdock", "DELETE"),
  mcp: () => request<MCPConnection[]>("/agent/mcp-connections"),
  addMCP: (value: MCPInput) =>
    request<MCPConnection>("/agent/mcp-connections", "POST", value),
  updateMCP: (id: string, value: MCPInput) =>
    request<MCPConnection>(`/agent/mcp-connections/${id}`, "PUT", value),
  removeMCP: (id: string) =>
    request<void>(`/agent/mcp-connections/${id}`, "DELETE"),
  testMCP: (
    value: Pick<MCPInput, "name" | "url" | "bearer_token_env" | "timeout_ms">,
  ) =>
    request<TestResult>("/agent/mcp-test", "POST", {
      name: value.name,
      url: value.url,
      bearer_token_env: value.bearer_token_env,
      timeout_ms: value.timeout_ms,
    }),
};
