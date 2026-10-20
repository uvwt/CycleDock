export class APIError extends Error {
  status: number
  code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

export type Identity = {
  human_account_id: string
  display_name: string
  email?: string
  role: string
  workspace: { id: string; name: string }
  source: { id: string; name: string; kind: 'human' }
}

export type LoginMethods = {
  password: boolean
  providers: Array<{ id: string; name: string; start_url: string }>
}

export type Project = {
  id: string
  name: string
  description: string
  instructions: string
  created_at: string
  updated_at: string
}

export type Card = {
  id: string
  project_id: string
  kind: 'normal' | 'ongoing'
  title: string
  description: string
  normal_column?: NormalColumn
  ongoing_state?: OngoingState
  health?: Health
  current_run_status?: RunStatus
  created_at: string
  updated_at: string
}

export type NormalColumn = 'discussion' | 'todo' | 'working' | 'review' | 'done'
export type OngoingState = 'active' | 'paused' | 'archived'
export type Health = 'unknown' | 'healthy' | 'warning' | 'critical'
export type RunStatus =
  | 'queued'
  | 'running'
  | 'waiting_human'
  | 'completed'
  | 'failed'
  | 'superseded'
  | 'expired'
  | 'cancelled'

export type CurrentRun = {
  id: string
  status: RunStatus
  current_stage?: string
  progress_summary?: string
  result_summary?: string
  error_summary?: string
  created_at: string
}

export type TimelineEvent = {
  id: string
  sequence: number
  type: string
  content: string
  checkin_status?: Health
  actor_name: string
  actor_kind: string
  run_id?: string
  created_at: string
}

export type QueueItem = {
  id: string
  sequence: number
  cause: string
  dispatch_mode: 'enqueue' | 'interrupt'
  content: string
  created_at: string
}

export type WorkHistoryItem = {
  trigger_id: string
  run_id?: string
  sequence: number
  cause: string
  status: RunStatus
  content: string
  retry_of_trigger_id?: string
  current_stage?: string
  progress_summary?: string
  result_summary?: string
  error_summary?: string
  cancel_reason?: string
  can_retry: boolean
  created_at: string
  finished_at?: string
}

export type Schedule = {
  id: string
  enabled: boolean
  frequency: 'daily' | 'weekly' | 'monthly'
  day_of_week?: number
  day_of_month?: number
  hour: number
  minute: number
  timezone: string
  next_trigger_at: string
  last_triggered_at?: string
}

export type CardDetail = {
  card: Card
  current_run: CurrentRun | null
  timeline: TimelineEvent[]
  queue: QueueItem[]
  work_history: WorkHistoryItem[]
  schedules: Schedule[]
  health: Health
  last_checkin: TimelineEvent | null
}

export type ScheduleInput = {
  enabled?: boolean
  frequency: Schedule['frequency']
  day_of_week?: number
  day_of_month?: number
  hour: number
  minute: number
  timezone: string
}

export type AgentSettings = {
  configured: boolean
  enabled: boolean
  provider_base_url: string
  model: string
  api_key_env: string
  request_timeout_ms: number
  max_tool_rounds: number
  system_instructions: string
  source_id?: string
}

export type MCPConnection = {
  id: string
  name: string
  url: string
  bearer_token_env: string
  enabled: boolean
  timeout_ms: number
}

export type MCPConnectionInput = Omit<MCPConnection, 'id'>

export type ConnectionTestResult = {
  ok: boolean
  code: string
  message: string
  tool_count?: number
  duration_ms: number
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  if (init?.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (!response.ok) {
    let code = 'request_failed'
    let message = `Request failed with status ${response.status}`
    try {
      const body = (await response.json()) as { error?: { code?: string; message?: string } }
      code = body.error?.code ?? code
      message = body.error?.message ?? message
    } catch {
      // Non-JSON failures are still surfaced with the HTTP status.
    }
    throw new APIError(response.status, code, message)
  }
  if (response.status === 204) {
    return undefined as T
  }
  return (await response.json()) as T
}

function jsonBody(value: unknown): string {
  return JSON.stringify(value)
}

function idempotencyHeaders(): HeadersInit {
  const key =
    typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : `web-${Date.now()}-${Math.random().toString(36).slice(2)}`
  return { 'Idempotency-Key': key }
}

export const api = {
  loginMethods: () => request<LoginMethods>('/api/v1/auth/methods'),
  login: (password: string) =>
    request<Identity>('/api/v1/auth/login', {
      method: 'POST',
      body: jsonBody({ password }),
    }),
  logout: () => request<void>('/api/v1/auth/logout', { method: 'POST', body: '{}' }),
  me: () => request<Identity>('/api/v1/me'),

  projects: () => request<Project[]>('/api/v1/projects'),
  createProject: (input: Pick<Project, 'name' | 'description' | 'instructions'>) =>
    request<Project>('/api/v1/projects', { method: 'POST', body: jsonBody(input) }),
  updateProject: (projectID: string, input: Pick<Project, 'name' | 'description' | 'instructions'>) =>
    request<Project>(`/api/v1/projects/${projectID}`, { method: 'PATCH', body: jsonBody(input) }),
  projectCards: (projectID: string) => request<Card[]>(`/api/v1/projects/${projectID}/cards`),
  createCard: (projectID: string, input: { kind: Card['kind']; title: string; description: string }) =>
    request<Card>(`/api/v1/projects/${projectID}/cards`, {
      method: 'POST',
      body: jsonBody(input),
    }),

  card: (cardID: string) => request<CardDetail>(`/api/v1/cards/${cardID}`),
  updateCard: (cardID: string, input: { title: string; description: string }) =>
    request<Card>(`/api/v1/cards/${cardID}`, { method: 'PATCH', body: jsonBody(input) }),
  moveCard: (cardID: string, column: NormalColumn) =>
    request<Card>(`/api/v1/cards/${cardID}/move`, {
      method: 'POST',
      body: jsonBody({ column }),
    }),
  setOngoingState: (cardID: string, state: OngoingState) =>
    request<Card>(`/api/v1/cards/${cardID}/ongoing-state`, {
      method: 'POST',
      body: jsonBody({ state }),
    }),
  comment: (cardID: string, content: string, dispatchMode: 'enqueue' | 'interrupt') =>
    request<{ event_id: string; trigger_id: string; run_id?: string }>(
      `/api/v1/cards/${cardID}/comments`,
      {
        method: 'POST',
        headers: idempotencyHeaders(),
        body: jsonBody({ content, dispatch_mode: dispatchMode }),
      },
    ),
  reply: (cardID: string, content: string) =>
    request<{ event_id: string; trigger_id: string; run_id?: string }>(
      `/api/v1/cards/${cardID}/reply`,
      {
        method: 'POST',
        headers: idempotencyHeaders(),
        body: jsonBody({ content }),
      },
    ),
  cancelPendingTrigger: (cardID: string, triggerID: string, reason = '') =>
    request<void>(`/api/v1/cards/${cardID}/triggers/${triggerID}/cancel`, {
      method: 'POST',
      body: jsonBody({ reason }),
    }),
  retryTrigger: (cardID: string, triggerID: string) =>
    request<{ trigger_id: string; reused: boolean }>(
      `/api/v1/cards/${cardID}/triggers/${triggerID}/retry`,
      {
        method: 'POST',
        headers: idempotencyHeaders(),
        body: '{}',
      },
    ),
  cancelRun: (cardID: string, runID: string, reason = '') =>
    request<void>(`/api/v1/cards/${cardID}/runs/${runID}/cancel`, {
      method: 'POST',
      body: jsonBody({ reason }),
    }),

  createSchedule: (cardID: string, input: ScheduleInput) =>
    request<Schedule>(`/api/v1/cards/${cardID}/schedules`, {
      method: 'POST',
      body: jsonBody(input),
    }),
  updateSchedule: (cardID: string, scheduleID: string, input: ScheduleInput) =>
    request<Schedule>(`/api/v1/cards/${cardID}/schedules/${scheduleID}`, {
      method: 'PUT',
      body: jsonBody(input),
    }),
  deleteSchedule: (cardID: string, scheduleID: string) =>
    request<void>(`/api/v1/cards/${cardID}/schedules/${scheduleID}`, { method: 'DELETE' }),

  agentSettings: () => request<AgentSettings>('/api/v1/agent/settings'),
  saveAgentSettings: (input: Omit<AgentSettings, 'configured' | 'source_id'>) =>
    request<AgentSettings>('/api/v1/agent/settings', {
      method: 'PUT',
      body: jsonBody(input),
    }),
  testAgentProvider: (input: Pick<AgentSettings, 'provider_base_url' | 'model' | 'api_key_env' | 'request_timeout_ms'>) =>
    request<ConnectionTestResult>('/api/v1/agent/provider-test', {
      method: 'POST',
      body: jsonBody(input),
    }),
  testAgentMCP: (input: Pick<MCPConnectionInput, 'name' | 'url' | 'bearer_token_env' | 'timeout_ms'>) =>
    request<ConnectionTestResult>('/api/v1/agent/mcp-test', {
      method: 'POST',
      body: jsonBody(input),
    }),
  agentMCPConnections: () => request<MCPConnection[]>('/api/v1/agent/mcp-connections'),
  createAgentMCPConnection: (input: MCPConnectionInput) =>
    request<MCPConnection>('/api/v1/agent/mcp-connections', {
      method: 'POST',
      body: jsonBody(input),
    }),
  updateAgentMCPConnection: (connectionID: string, input: MCPConnectionInput) =>
    request<MCPConnection>(`/api/v1/agent/mcp-connections/${connectionID}`, {
      method: 'PUT',
      body: jsonBody(input),
    }),
  deleteAgentMCPConnection: (connectionID: string) =>
    request<void>(`/api/v1/agent/mcp-connections/${connectionID}`, { method: 'DELETE' }),
}
