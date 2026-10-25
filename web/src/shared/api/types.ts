export type Identity = {
  human_account_id: string;
  display_name: string;
  email?: string;
  role: string;
  workspace: { id: string; name: string };
  source: { id: string; name: string; kind: string };
};
export type Workspace = {
  id: string;
  name: string;
  role: string;
  is_personal: boolean;
};
export type LoginMethods = {
  password: boolean;
  providers: { id: string; name: string; start_url: string }[];
};
export type Project = {
  id: string;
  name: string;
  description: string;
  instructions: string;
  created_at: string;
  updated_at: string;
};
export type CardKind = "discussion" | "todo" | "ongoing";
export type TodoState = "todo" | "working" | "review" | "done";
export type OngoingState = "active" | "paused" | "archived";
export type Card = {
  id: string;
  project_id: string;
  kind: CardKind;
  title: string;
  description: string;
  discussion_state?: "active" | "archived";
  todo_state?: TodoState;
  ongoing_state?: OngoingState;
  source_discussion_id?: string;
  health?: string;
  current_attempt_status?: string;
  created_at: string;
  updated_at: string;
};
export type TimelineEvent = {
  id: string;
  sequence: number;
  attempt_id?: string;
  type:
    | "comment"
    | "progress"
    | "finding"
    | "result"
    | "checkin"
    | "confirm"
    | "reject"
    | "status_change";
  actor_kind: string;
  actor_name: string;
  content: string;
  checkin_status?: string;
  created_at: string;
};
export type Attempt = {
  id: string;
  turn_id: string;
  source_event_id?: string;
  attempt_number: number;
  reason: "initial" | "retry" | "recovery";
  retry_of_attempt_id?: string;
  status: string;
  current_stage?: string;
  progress_summary?: string;
  result_summary?: string;
  error_summary?: string;
  can_retry?: boolean;
  created_at: string;
  started_at?: string;
  finished_at?: string;
};
export type PendingTurn = {
  id: string;
  sequence: number;
  source: "human" | "schedule" | "system";
  dispatch_mode: string;
  source_event_id?: string;
  content: string;
  created_at: string;
};
export type Schedule = {
  id: string;
  enabled: boolean;
  frequency: "daily" | "weekly" | "monthly";
  day_of_week?: number;
  day_of_month?: number;
  hour: number;
  minute: number;
  timezone: string;
  next_trigger_at: string;
};
export type ScheduleInput = Omit<Schedule, "id" | "next_trigger_at">;
export type ProjectSchedule = Schedule & {
  card_id: string;
  card_title: string;
  ongoing_state: OngoingState;
  last_triggered_at?: string;
  last_attempt_status?: string;
  last_attempt_finished_at?: string;
};
export type ProjectActivity = {
  id: string;
  card_id: string;
  card_title: string;
  card_kind: CardKind;
  activity_type: "event" | "attempt" | "turn";
  status: string;
  content: string;
  actor_name?: string;
  occurred_at: string;
};
export type CardDetail = {
  card: Card;
  current_attempt: Attempt | null;
  timeline: TimelineEvent[];
  pending_turns: PendingTurn[];
  attempt_history: Attempt[];
  schedules: Schedule[];
  health: string;
  last_checkin: TimelineEvent | null;
};
export type Provider = "chatgpt" | "grok" | "openai_compatible";
export type ModelRoute = {
  id: string;
  enabled: boolean;
  provider_type: Provider;
  credential_id: string;
  provider_base_url: string;
  model: string;
  api_key_env: string;
};
export type AgentSettings = {
  configured: boolean;
  routes: ModelRoute[];
  system_instructions: string;
  source_id?: string;
};
export type ModelAuthAccount = {
  id: string;
  display_label: string;
  expires_at: string;
};
export type ModelAuth = {
  provider: "chatgpt" | "grok";
  available: boolean;
  connected: boolean;
  reason?: string;
  warning?: string;
  expires_at?: string;
  accounts: ModelAuthAccount[];
};
export type ModelAuthStart = {
  authorization_url: string;
  manual_callback?: boolean;
};
export type GrokStart = {
  flow_id: string;
  verification_url: string;
  user_code: string;
  expires_at: string;
  interval_seconds: number;
};
export type MCPConnection = {
  id: string;
  name: string;
  url: string;
  bearer_token_env: string;
  enabled: boolean;
  timeout_ms: number;
};
export type MCPInput = Omit<MCPConnection, "id">;
export type NexusConnection = {
  available: boolean;
  connected: boolean;
  manage_url?: string;
  connected_at?: string;
};
export type TestResult = {
  ok: boolean;
  code: string;
  message: string;
  duration_ms: number;
  tool_count?: number;
};
