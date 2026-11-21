// Local visual smoke fixture. Never used by the production build or API.
// Run after npm run build: node e2e/mock-server.mjs
import http from "node:http";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../dist",
);
const project = {
  id: "demo-project",
  name: "NexusDock 运维",
  description: "服务器和自动化任务",
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};
const secondProject = {
  id: "demo-project-b",
  name: "产品发布",
  description: "发布计划与上线检查",
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};
const projects = [project, secondProject];
let nexusConnected = true;
const now = () => new Date().toISOString();
const hoursAgo = (hours) =>
  new Date(Date.now() - hours * 60 * 60 * 1000).toISOString();
let remoteChannels = [
  {
    id: "ch-feishu-main",
    workspace_id: "demo-workspace",
    name: "运维主聊天 · 飞书机器人",
    platform: "feishu",
    role: "main_chat",
    transport: "websocket",
    enabled: true,
    project_id: project.id,
    target_card_id: null,
    push_filter: "all",
    mirror_web_messages: true,
    group_policy: "mention_only",
    allow_from: ["ou_ops_lead", "oc_ops_group"],
    config: { domain: "feishu", app_id: "cli_a8f92e10", target_chat_id: "oc_ops_group" },
    has_secrets: true,
    secret_hints: { app_secret: "fs_****9a21" },
    runtime_status: "connected",
    last_error: "",
    last_heartbeat_at: now(),
    created_at: hoursAgo(12),
    updated_at: now(),
  },
  {
    id: "ch-wecom-scheduled",
    workspace_id: "demo-workspace",
    name: "企微值班群 · 定时巡检独立推送",
    platform: "wecom",
    role: "scheduled_push",
    transport: "webhook",
    enabled: true,
    project_id: project.id,
    target_card_id: null,
    push_filter: "warnings_only",
    mirror_web_messages: false,
    group_policy: "mention_only",
    allow_from: [],
    config: {},
    has_secrets: true,
    secret_hints: { webhook_url: "https://qyapi.weixin.qq.com/****8b3c" },
    runtime_status: "connected",
    last_error: "",
    last_heartbeat_at: now(),
    created_at: hoursAgo(8),
    updated_at: now(),
  },
  {
    id: "ch-weixin-main",
    workspace_id: "demo-workspace",
    name: "个人微信 · 随身助手",
    platform: "weixin",
    role: "main_chat",
    transport: "long_poll",
    enabled: true,
    project_id: secondProject.id,
    target_card_id: null,
    push_filter: "all",
    mirror_web_messages: false,
    group_policy: "mention_only",
    allow_from: [],
    config: { base_url: "https://ilinkai.weixin.qq.com" },
    has_secrets: false,
    secret_hints: {},
    runtime_status: "starting",
    last_error: "等待扫码绑定微信账号",
    created_at: hoursAgo(2),
    updated_at: now(),
  },
  {
    id: "ch-tg-topics",
    workspace_id: "demo-workspace",
    name: "Telegram 巡检 Topic",
    platform: "telegram",
    role: "scheduled_push",
    transport: "long_poll",
    enabled: true,
    project_id: secondProject.id,
    target_card_id: null,
    push_filter: "all",
    mirror_web_messages: false,
    group_policy: "mention_only",
    allow_from: [],
    config: { target_chat_id: "-100238901234", message_thread_id: 42 },
    has_secrets: true,
    secret_hints: { bot_token: "7412****x9Qf" },
    runtime_status: "connected",
    last_error: "",
    last_heartbeat_at: now(),
    created_at: hoursAgo(5),
    updated_at: now(),
  },
];
const wxLoginSessions = new Map();
let deviceToolGuards = [
  {
    id: "guard-rn",
    device_node_id: "node-rn",
    device_name: "RN Server",
    enabled: false,
    created_at: now(),
    updated_at: now(),
  },
];
const minutesFromNow = (minutes) =>
  new Date(Date.now() + minutes * 60 * 1000).toISOString();

let triggers = [
  {
    id: "trig-gh-ci",
    name: "GitHub Actions CI 完成唤醒",
    provider: "github",
    auth_mode: "github_hmac_sha256",
    signature_header: "X-Hub-Signature-256",
    secret_hint: "whsec_9a...4f2c",
    webhook_path: "/api/v1/webhooks/trig-gh-ci",
    enabled: true,
    routing_mode: "match_watchers",
    dispatch_mode: "enqueue",
    filter_config: {
      events: ["workflow_run"],
      actions: ["completed"],
      repositories: ["cycledock/cycledock"],
    },
    cooldown_seconds: 15,
    last_triggered_at: hoursAgo(1),
    last_delivery_status: "triggered",
    last_delivery_reason: "matched 1 armed watcher",
    last_delivery_at: hoursAgo(1),
    created_at: hoursAgo(24),
    updated_at: hoursAgo(1),
  },
  {
    id: "trig-generic-alert",
    name: "生产告警自动建单",
    provider: "generic",
    auth_mode: "bearer",
    signature_header: "",
    secret_hint: "tok_7e...18bd",
    webhook_path: "/api/v1/webhooks/trig-generic-alert",
    enabled: true,
    routing_mode: "create_todo",
    target_project_id: project.id,
    target_project_name: project.name,
    dispatch_mode: "interrupt",
    filter_config: {
      json_matches: { severity: "critical", env: "prod" },
      match_key_path: "alert.fingerprint",
    },
    cooldown_seconds: 60,
    last_delivery_status: "filtered",
    last_delivery_reason: "filter_config json_matches mismatch",
    last_delivery_at: hoursAgo(3),
    created_at: hoursAgo(48),
    updated_at: hoursAgo(3),
  },
  {
    id: "trig-circleci-deploy",
    name: "CircleCI 发布流水线",
    provider: "circleci",
    auth_mode: "circleci_hmac_sha256",
    signature_header: "circleci-signature",
    secret_hint: "cci_3c...89a0",
    webhook_path: "/api/v1/webhooks/trig-circleci-deploy",
    enabled: false,
    routing_mode: "fixed_card",
    target_card_id: "ongoing-1",
    target_card_project_id: project.id,
    target_card_kind: "ongoing",
    target_card_title: "每天检查服务器健康",
    dispatch_mode: "enqueue",
    filter_config: {
      events: ["workflow-completed"],
      branches: ["main"],
    },
    cooldown_seconds: 0,
    created_at: hoursAgo(72),
    updated_at: hoursAgo(12),
  },
];

let triggerDeliveries = [
  {
    id: "deliv-1",
    trigger_id: "trig-gh-ci",
    trigger_name: "GitHub Actions CI 完成唤醒",
    delivery_id: "gh-987654",
    provider: "github",
    event_type: "workflow_run",
    event_action: "completed",
    signature_valid: true,
    status: "triggered",
    reason: "",
    match_keys: [
      "github:cycledock/cycledock:pr:128:ci_completed",
      "github:cycledock/cycledock:branch:feat/triggers-and-watchers-ui:ci_completed",
    ],
    payload_summary:
      "GitHub workflow_run.completed: CI on cycledock/cycledock@feat/triggers-and-watchers-ui (conclusion=success)",
    triggered_turn_id: "turn-ci-1",
    triggered_watcher_turn_ids: ["turn-ci-1"],
    linked_cards: [
      {
        id: "todo-1",
        project_id: project.id,
        kind: "todo",
        title: "检查 RN 服务器异常日志",
      },
    ],
    received_at: hoursAgo(1),
  },
  {
    id: "deliv-2",
    trigger_id: "trig-generic-alert",
    trigger_name: "生产告警自动建单",
    delivery_id: "alert-551",
    provider: "generic",
    event_type: "alert.firing",
    event_action: "",
    signature_valid: true,
    status: "filtered",
    reason: "filter_config json_matches mismatch (severity=warning)",
    match_keys: ["generic:trig-generic-alert:disk-usage-node-2"],
    payload_summary: "Generic webhook alert.firing: severity=warning on node-2",
    triggered_watcher_turn_ids: [],
    linked_cards: [],
    received_at: hoursAgo(3),
  },
  {
    id: "deliv-3",
    trigger_id: "trig-gh-ci",
    trigger_name: "GitHub Actions CI 完成唤醒",
    delivery_id: "gh-987600",
    provider: "github",
    event_type: "workflow_run",
    event_action: "completed",
    signature_valid: false,
    status: "rejected",
    reason: "invalid signature",
    match_keys: [],
    payload_summary: "Signature verification failed for incoming GitHub webhook",
    triggered_watcher_turn_ids: [],
    linked_cards: [],
    received_at: hoursAgo(6),
  },
];

let cardWatchers = [
  {
    id: "watcher-1",
    card_id: "todo-1",
    project_id: project.id,
    project_name: project.name,
    card_title: "检查 RN 服务器异常日志",
    card_kind: "todo",
    kind: "event",
    match_key: "github:cycledock/cycledock:pr:128:ci_completed",
    note: "等待 PR #128 CI 构建完成后自动复核日志并推进验收",
    dispatch_mode: "enqueue",
    status: "armed",
    expires_at: minutesFromNow(42),
    created_at: hoursAgo(0.3),
    updated_at: hoursAgo(0.3),
  },
  {
    id: "watcher-2",
    card_id: "todo-1",
    project_id: project.id,
    project_name: project.name,
    card_title: "检查 RN 服务器异常日志",
    card_kind: "todo",
    kind: "wakeup",
    note: "15 分钟后自动复查容器重启计数与内存趋势",
    dispatch_mode: "interrupt",
    status: "armed",
    expires_at: minutesFromNow(15),
    created_at: hoursAgo(0.1),
    updated_at: hoursAgo(0.1),
  },
  {
    id: "watcher-3",
    card_id: "ongoing-1",
    project_id: project.id,
    project_name: project.name,
    card_title: "每天检查服务器健康",
    card_kind: "ongoing",
    kind: "wakeup",
    note: "2 小时后复检磁盘水位",
    dispatch_mode: "enqueue",
    status: "armed",
    expires_at: minutesFromNow(120),
    created_at: hoursAgo(0.5),
    updated_at: hoursAgo(0.5),
  },
  {
    id: "watcher-4",
    card_id: "todo-1",
    project_id: project.id,
    project_name: project.name,
    card_title: "检查 RN 服务器异常日志",
    card_kind: "todo",
    kind: "event",
    match_key: "github:cycledock/cycledock:pr:127:ci_completed",
    note: "上一轮 PR #127 CI 等待",
    dispatch_mode: "enqueue",
    status: "triggered",
    expires_at: hoursAgo(2),
    resolved_reason: "matched webhook delivery",
    resolved_at: hoursAgo(2.5),
    created_at: hoursAgo(3),
    updated_at: hoursAgo(2.5),
  },
];
const cards = [
  {
    id: "discussion-1",
    project_id: project.id,
    kind: "discussion",
    title: "讨论服务器巡检计划",
    description: "决定每天的检查范围和告警方式。",
    discussion_state: "active",
    created_at: now(),
    updated_at: now(),
  },
  {
    id: "discussion-b",
    project_id: secondProject.id,
    kind: "discussion",
    title: "讨论版本发布",
    description: "确认发布窗口、检查项与回滚条件。",
    discussion_state: "active",
    created_at: now(),
    updated_at: now(),
  },
  {
    id: "todo-b",
    project_id: secondProject.id,
    kind: "todo",
    source_discussion_id: "discussion-b",
    title: "准备发布清单",
    description: "核对变更、备份和回滚步骤",
    todo_state: "todo",
    created_at: now(),
    updated_at: now(),
  },
  {
    id: "todo-1",
    project_id: project.id,
    kind: "todo",
    source_discussion_id: "discussion-1",
    title: "检查 RN 服务器异常日志",
    description: "重点检查最近24小时的 Docker 和系统错误日志",
    todo_state: "working",
    current_attempt_status: "running",
    pending_tool_approval_count: 1,
    created_at: now(),
    updated_at: now(),
  },
  {
    id: "ongoing-1",
    project_id: project.id,
    kind: "ongoing",
    source_discussion_id: "discussion-1",
    title: "每天检查服务器健康",
    description: "CPU、内存、容器运行状态",
    ongoing_state: "active",
    health: "healthy",
    created_at: now(),
    updated_at: now(),
  },
];
let seq = 10;
const events = {
  "discussion-1": [
    {
      id: "event-1",
      sequence: 1,
      type: "comment",
      content: "我们需要每天自动检查服务器状态。",
      actor_kind: "human",
      actor_name: "测试用户",
      created_at: hoursAgo(3),
    },
    {
      id: "event-2",
      sequence: 2,
      type: "finding",
      content: "建议监控 CPU、内存和容器健康，并记录异常。",
      actor_kind: "cycle_agent",
      actor_name: "Cycle Agent",
      created_at: now(),
    },
  ],
  "discussion-b": [
    {
      id: "event-b1",
      sequence: 1,
      type: "comment",
      content: "这周安排一次版本发布。",
      actor_kind: "human",
      actor_name: "测试用户",
      created_at: now(),
    },
  ],
  "todo-b": [],
  "todo-1": [
    {
      id: "event-8",
      sequence: 1,
      type: "comment",
      content: "先检查昨天的日志。",
      actor_kind: "human",
      actor_name: "测试用户",
      created_at: hoursAgo(5),
    },
    {
      id: "event-9",
      sequence: 2,
      type: "comment",
      content: "先检查上一轮日志。",
      actor_kind: "human",
      actor_name: "测试用户",
      created_at: hoursAgo(1),
    },
    {
      id: "event-3",
      sequence: 3,
      type: "comment",
      content: "检查一下 RN 最近的错误日志。",
      actor_kind: "human",
      actor_name: "测试用户",
      created_at: now(),
    },
    {
      id: "event-4",
      sequence: 4,
      type: "status_change",
      content: "todo -> working",
      actor_kind: "cycle_agent",
      actor_name: "Cycle Agent",
      created_at: now(),
    },
    {
      id: "event-5",
      sequence: 5,
      type: "finding",
      content: "已经找到日志目录，正在分析错误频次。",
      actor_kind: "cycle_agent",
      actor_name: "Cycle Agent",
      attempt_id: "attempt-running",
      created_at: now(),
    },
    {
      id: "event-7",
      sequence: 6,
      type: "comment",
      content: "顺便检查磁盘空间。",
      actor_kind: "human",
      actor_name: "测试用户",
      created_at: now(),
    },
    {
      id: "event-10",
      sequence: 7,
      type: "result",
      content: "**上一轮日志检查完成**\n\n没有发现新的错误。",
      actor_kind: "cycle_agent",
      actor_name: "Cycle Agent",
      attempt_id: "attempt-completed",
      created_at: now(),
    },
    {
      id: "event-tool-approval-1",
      sequence: 8,
      type: "tool_approval",
      content: "工具「exec_command」调用已被安全守卫拦截并挂起（风险等级：high）。等待人工确认后恢复执行。",
      actor_kind: "cycle_agent",
      actor_name: "Cycle Agent",
      tool_approval_id: "approval-demo-1",
      tool_approval: {
        id: "approval-demo-1",
        tool_name: "exec_command",
        arguments: {
          node_id: "node-rn",
          cmd: "docker restart nginx-proxy && rm -f /var/log/nginx/error.log.old",
        },
        risk_level: "high",
        reversible: false,
        reason: "命令包含重启线上网关容器及删除历史日志文件，属于不可逆高风险写操作，需人工确认。",
        scope_type: "device",
        scope_target: "node-rn",
        scope_label: "RN Server",
        allow_session_whitelist: true,
        status: "pending",
        created_at: now(),
      },
      created_at: now(),
    },
  ],
  "ongoing-1": [
    {
      id: "event-6",
      sequence: 6,
      type: "checkin",
      content: "资源使用正常",
      checkin_status: "healthy",
      actor_kind: "cycle_agent",
      actor_name: "Cycle Agent",
      created_at: now(),
    },
  ],
};
const failFirstComment = new Set();
let failNextDiscussionCreate = false;
let settings = {
  configured: true,
  api_credentials: [
    {
      id: "00000000-0000-4000-8000-000000000201",
      name: "api.example.com",
      base_url: "https://api.example.com/v1",
      api_key_env: "CYCLEDOCK_MODEL_API_KEY",
      api_key_configured: true,
    },
  ],
  routes: [
    {
      id: "00000000-0000-4000-8000-000000000101",
      enabled: true,
      provider_type: "openai_compatible",
      credential_id: "",
      api_credential_id: "00000000-0000-4000-8000-000000000201",
      api_credential_name: "api.example.com",
      provider_base_url: "https://api.example.com/v1",
      model: "example-model",
      api_key_env: "CYCLEDOCK_MODEL_API_KEY",
      api_key_configured: true,
    },
  ],
  system_instructions: "先检查现场，再确认结果",
};
let toolGuardSettings = {
  mode: "block_high_risk",
  model_route_id: "00000000-0000-4000-8000-000000000101",
  timeout_seconds: 10,
  custom_rules: "允许在独立 Git worktree 中自由修改测试代码；涉及线上容器重启、删除日志或修改生产配置必须触发人工确认。",
};
const session = {
  human_account_id: "demo-human",
  display_name: "测试用户",
  role: "owner",
  email: "demo@example.com",
  workspace: { id: "demo-workspace", name: "演示空间" },
  source: { id: "demo-source", name: "测试用户", kind: "human" },
};
const workspaces = [
  {
    id: "demo-workspace",
    name: "演示空间",
    role: "owner",
    is_personal: true,
  },
  {
    id: "demo-workspace-2",
    name: "第二空间",
    role: "member",
    is_personal: false,
  },
];
let mcp = [
  {
    id: "mcp-demo",
    name: "示例 MCP",
    url: "https://example.com/mcp",
    enabled: true,
    token_configured: false,
    timeout_ms: 30000,
    tool_guard_enabled: true,
  },
];
const data = (response, body, status = 200) => {
  response.writeHead(status, {
    "Content-Type": "application/json; charset=utf-8",
    "Cache-Control": "no-store",
  });
  response.end(JSON.stringify(body));
};
const failure = (res) =>
  data(
    res,
    { error: { code: "not_found", message: "Mock endpoint unavailable" } },
    404,
  );
async function body(req) {
  let raw = "";
  for await (const part of req) raw += part;
  return raw ? JSON.parse(raw) : {};
}
function createInitialCardSchedules() {
  return {
    "ongoing-1": [
      {
        id: "schedule-demo",
        enabled: true,
        frequency: "daily",
        hour: 9,
        minute: 0,
        timezone: "Asia/Shanghai",
        next_trigger_at: new Date(Date.now() + 86400000).toISOString(),
      },
    ],
  };
}
let cardSchedules = createInitialCardSchedules();
function details(id) {
  const card = cards.find((x) => x.id === id);
  if (!card) return null;
  return {
    card,
    timeline: events[id] || [],
    current_attempt: card.current_attempt_status
      ? {
          id: "attempt-running",
          turn_id: "turn-running",
          source_event_id: "event-3",
          attempt_number: 1,
          reason: "initial",
          status: card.current_attempt_status,
          current_stage: "分析错误日志",
          created_at: now(),
          started_at: new Date(Date.now() - 32000).toISOString(),
        }
      : null,
    pending_turns:
      id === "todo-1"
        ? [
            {
              id: "turn-queued",
              sequence: 6,
              source: "human",
              dispatch_mode: "enqueue",
              source_event_id: "event-7",
              content: "顺便检查磁盘空间。",
              created_at: now(),
            },
          ]
        : [],
    attempt_history:
      id === "todo-1"
        ? [
            {
              id: "attempt-failed",
              turn_id: "turn-failed",
              source_event_id: "event-8",
              attempt_number: 1,
              reason: "initial",
              status: "failed",
              error_summary: "远程节点暂时不可用",
              can_retry: true,
              created_at: hoursAgo(5),
              started_at: hoursAgo(5),
              finished_at: hoursAgo(4.9),
            },
            {
              id: "attempt-completed",
              turn_id: "turn-completed",
              source_event_id: "event-9",
              attempt_number: 1,
              reason: "initial",
              status: "completed",
              result_summary: "**上一轮日志检查完成**\n\n没有发现新的错误。",
              can_retry: false,
              created_at: hoursAgo(1),
              started_at: hoursAgo(1),
              finished_at: hoursAgo(0.9),
            },
            {
              id: "attempt-schedule-history",
              turn_id: "turn-schedule-history",
              attempt_number: 1,
              reason: "initial",
              status: "completed",
              result_summary: "巡检完成",
              can_retry: false,
              created_at: hoursAgo(2),
              started_at: hoursAgo(2),
              finished_at: hoursAgo(1.9),
            },
          ]
        : [],
    schedules: card.kind === "ongoing" ? cardSchedules[id] || [] : [],
    health: card.health || "unknown",
    last_checkin: id === "ongoing-1" ? events[id][0] : null,
    has_more_before: (events[id] || []).length > 50,
  };
}
http
  .createServer(async (req, res) => {
    const url = new URL(req.url || "/", "http://localhost");
    const p = url.pathname;
    try {
      if (p.startsWith("/api/v1")) {
        const route = p.slice(7);
        if (route === "/me") return data(res, session);
        if (route === "/workspaces" && req.method === "GET")
          return data(res, workspaces);
        const workspaceSwitch = route.match(/^\/workspaces\/([^/]+)\/switch$/);
        if (workspaceSwitch && req.method === "POST") {
          const workspace = workspaces.find(
            (item) => item.id === workspaceSwitch[1],
          );
          if (!workspace) return failure(res);
          session.workspace = { id: workspace.id, name: workspace.name };
          session.role = workspace.role;
          return data(res, session);
        }
        if (route === "/auth/methods")
          return data(res, { password: true, providers: [] });
        if (
          route === "/__fixture/fail-next-discussion" &&
          req.method === "POST"
        ) {
          failNextDiscussionCreate = true;
          return data(res, { ok: true });
        }
        if (route === "/projects" && req.method === "GET")
          return data(res, projects);
        if (route === "/projects" && req.method === "POST") {
          return data(
            res,
            { ...project, ...(await body(req)), id: "project-new" },
            201,
          );
        }
        if (route === `/projects/${project.id}` && req.method === "PATCH") {
          Object.assign(project, await body(req));
          return data(res, project);
        }
        const activityAgents = [
          {
            id: "11111111-1111-4111-8111-111111111111",
            name: "Cycle Agent",
            kind: "cycle_agent",
          },
          {
            id: "22222222-2222-4222-8222-222222222222",
            name: "Codex",
            kind: "oauth_client",
          },
          {
            id: "33333333-3333-4333-8333-333333333333",
            name: "Claude Code",
            kind: "oauth_client",
          },
        ];
        const agentsPath = route.match(
          /^\/projects\/([^/]+)\/activity\/agents$/,
        );
        if (agentsPath && req.method === "GET")
          return data(res, activityAgents);
        const activityPath = route.match(/^\/projects\/([^/]+)\/activity$/);
        if (activityPath && req.method === "GET") {
          const sourceID = url.searchParams.get("source_id");
          const items =
            activityPath[1] === project.id
              ? [
                  {
                    id: "activity-cycle",
                    card_id: "todo-1",
                    card_title: "检查 RN 服务器异常日志",
                    card_kind: "todo",
                    activity_type: "attempt",
                    status: "running",
                    content: "Cycle 正在检查日志",
                    source_id: activityAgents[0].id,
                    source_kind: "cycle_agent",
                    actor_name: "Cycle Agent",
                    occurred_at: now(),
                  },
                  {
                    id: "activity-codex",
                    card_id: "ongoing-1",
                    card_title: "每天检查服务器健康",
                    card_kind: "ongoing",
                    activity_type: "event",
                    status: "checkin",
                    content: "Codex 已完成巡检",
                    source_id: activityAgents[1].id,
                    source_kind: "oauth_client",
                    actor_name: "Codex",
                    occurred_at: hoursAgo(1),
                  },
                ]
              : [];
          return data(
            res,
            items
              .filter((item) => !sourceID || item.source_id === sourceID)
              .slice(0, Number(url.searchParams.get("limit") || 60)),
          );
        }
        const projectSchedules = route.match(
          /^\/projects\/([^/]+)\/schedules$/,
        );
        if (projectSchedules && req.method === "GET") {
          return data(
            res,
            cards
              .filter(
                (card) =>
                  card.project_id === projectSchedules[1] &&
                  card.kind === "ongoing",
              )
              .flatMap((card) =>
                details(card.id).schedules.map((schedule) => ({
                  ...schedule,
                  card_id: card.id,
                  card_title: card.title,
                  ongoing_state: card.ongoing_state,
                })),
              ),
          );
        }
        const mainDiscussion = route.match(
          /^\/projects\/([^/]+)\/main-discussion$/,
        );
        if (mainDiscussion && (req.method === "GET" || req.method === "POST")) {
          const projectID = decodeURIComponent(mainDiscussion[1]);
          let card = cards.find(
            (item) => item.project_id === projectID && item.main_chat,
          );
          if (!card && req.method === "POST") {
            card = {
              id: "main-" + projectID,
              project_id: projectID,
              kind: "discussion",
              title: "主聊天",
              description: "项目的主聊天。",
              discussion_state: "active",
              pinned_at: now(),
              main_chat: true,
              created_at: now(),
              updated_at: now(),
            };
            cards.unshift(card);
            events[card.id] = [];
          }
          if (!card) return failure(res);
          return data(res, card, req.method === "POST" ? 201 : 200);
        }
        const projectCards = route.match(/^\/projects\/([^/]+)\/cards$/);
        if (projectCards && req.method === "GET") {
          return data(
            res,
            cards.filter((card) => card.project_id === projectCards[1]),
          );
        }
        const projectDoneCards = route.match(/^\/projects\/([^/]+)\/cards\/done$/);
        if (projectDoneCards && req.method === "GET") {
          const limit = Number(url.searchParams.get("limit") || 30);
          const offset = Number(url.searchParams.get("offset") || 0);
          const doneList = cards.filter(
            (card) =>
              card.project_id === projectDoneCards[1] &&
              card.kind === "todo" &&
              card.todo_state === "done",
          );
          const paged = doneList.slice(offset, offset + limit);
          return data(res, {
            cards: paged,
            total_count: doneList.length,
            has_more: offset + paged.length < doneList.length,
          });
        }
        if (
          route === `/projects/${project.id}/discussions` &&
          req.method === "POST"
        ) {
          if (failNextDiscussionCreate) {
            failNextDiscussionCreate = false;
            return data(
              res,
              {
                error: {
                  code: "fixture_discussion_create_failure",
                  message: "模拟 Discussion 创建失败",
                },
              },
              503,
            );
          }
          const card = {
            id: "discussion-" + Date.now(),
            project_id: project.id,
            kind: "discussion",
            title: "",
            description: "",
            discussion_state: "active",
            created_at: now(),
            updated_at: now(),
          };
          cards.unshift(card);
          events[card.id] = [];
          failFirstComment.add(card.id);
          return data(res, card, 201);
        }
        if (
          route === `/projects/${project.id}/todos` &&
          req.method === "POST"
        ) {
          const input = await body(req);
          const card = {
            id: "todo-" + Date.now(),
            project_id: project.id,
            kind: "todo",
            title: input.title,
            description: input.description || "",
            todo_state: input.auto_start ? "working" : "todo",
            current_attempt_status: input.auto_start ? "running" : undefined,
            created_at: now(),
            updated_at: now(),
          };
          cards.unshift(card);
          events[card.id] = input.auto_start
            ? [
                {
                  id: "event-" + ++seq,
                  sequence: seq,
                  type: "status_change",
                  content: "todo -> working",
                  actor_kind: "cycle_agent",
                  actor_name: "Cycle",
                  created_at: now(),
                },
              ]
            : [];
          return data(res, card, 201);
        }
        const cardMatch = route.match(/^\/cards\/([^/]+)(?:\/(.*))?$/);
        if (cardMatch) {
          const [, id, rest] = cardMatch;
          const card = cards.find((x) => x.id === id);
          if (!card) return failure(res);
          if (rest === "stream") {
            res.writeHead(200, {
              "Content-Type": "text/event-stream",
              "Cache-Control": "no-cache",
              Connection: "keep-alive",
            });
            res.write(": connected\n\n");
            if (id === "todo-1") {
              const send = (event, obj) =>
                res.write(`event: ${event}\ndata: ${JSON.stringify(obj)}\n\n`);
              const timers = [
                setTimeout(
                  () =>
                    send("attempt.started", {
                      attempt_id: "attempt-running",
                      started_at: new Date(Date.now() - 35000).toISOString(),
                    }),
                  500,
                ),
                setTimeout(
                  () =>
                    send("reply.preview", {
                      attempt_id: "attempt-running",
                      reply_id: "preview-logs", order: 0,
                      text: "正在读取 RN 服务器日志。",
                    }),
                  1100,
                ),
                setTimeout(
                  () =>
                    send("reply.preview", {
                      attempt_id: "attempt-running",
                      reply_id: "preview-logs", order: 0,
                      text: "正在读取 RN 服务器日志。\n\n已定位 Docker 容器的错误记录。",
                    }),
                  1600,
                ),
              ];
              req.on("close", () => timers.forEach(clearTimeout));
            }
            return;
          }
          if (!rest && req.method === "GET") return data(res, details(id));
          if (rest === "timeline" && req.method === "GET") {
            const beforeSeq = Number(
              url.searchParams.get("before_sequence") || Number.MAX_SAFE_INTEGER,
            );
            const limit = Number(url.searchParams.get("limit") || 50);
            const allEvents = (events[id] || []).filter(
              (item) => (item.sequence || 0) < beforeSeq,
            );
            const hasMoreBefore = allEvents.length > limit;
            const slice = hasMoreBefore
              ? allEvents.slice(allEvents.length - limit)
              : allEvents;
            return data(res, {
              timeline: slice,
              has_more_before: hasMoreBefore,
              oldest_sequence: slice.length > 0 ? slice[0].sequence : 0,
            });
          }
          if (!rest && req.method === "PATCH") {
            Object.assign(card, await body(req));
            return data(res, card);
          }
          if (rest === "pin" && req.method === "POST") {
            const input = await body(req);
            card.pinned_at = input.pinned ? now() : undefined;
            card.updated_at = now();
            return data(res, card);
          }
          if (rest === "notifications" && req.method === "POST") {
            const input = await body(req);
            card.notifications_muted = Boolean(input.muted);
            card.updated_at = now();
            return data(res, card);
          }
          if (!rest && req.method === "DELETE") {
            cards.splice(cards.indexOf(card), 1);
            return data(res, null, 204);
          }
          if (rest === "move" && req.method === "POST") {
            const input = await body(req);
            const old = card.todo_state;
            card.todo_state = input.state;
            events[id].push({
              id: "event-" + ++seq,
              sequence: seq,
              type: "status_change",
              actor_kind: "human",
              actor_name: "测试用户",
              content: old + " -> " + input.state,
              created_at: now(),
            });
            return data(res, card);
          }
          if (rest === "discussion-state" && req.method === "POST") {
            card.discussion_state = "archived";
            return data(res, card);
          }
          if (rest === "read" && req.method === "POST") {
            const list = events[id] || [];
            const latestSeq = list.reduce(
              (max, item) => Math.max(max, item.sequence || 0),
              0,
            );
            card.last_read_message_sequence = latestSeq;
            card.marked_unread = false;
            card.unread = false;
            return data(res, {
              last_read_message_sequence: latestSeq,
              marked_unread: false,
              unread: false,
            });
          }
          if (rest === "unread" && req.method === "POST") {
            const lastReadSeq = card.last_read_message_sequence || 0;
            card.marked_unread = true;
            card.unread = true;
            return data(res, {
              last_read_message_sequence: lastReadSeq,
              marked_unread: true,
              unread: true,
            });
          }
          if (rest === "ongoing-state" && req.method === "POST") {
            card.ongoing_state = (await body(req)).state;
            return data(res, card);
          }
          if (rest === "start" && req.method === "POST") {
            if (card.kind === "todo" && card.todo_state === "todo") {
              card.todo_state = "working";
            }
            card.current_attempt_status = "running";
            card.updated_at = now();
            events[id] = events[id] || [];
            events[id].push({
              id: "event-" + ++seq,
              sequence: seq,
              type: "status_change",
              content: "todo -> working",
              actor_kind: "cycle_agent",
              actor_name: "Cycle",
              created_at: now(),
            });
            return data(
              res,
              { turn_id: "turn-start-" + Date.now(), reused: false },
              201,
            );
          }
          if (rest === "comments" && req.method === "POST") {
            const input = await body(req);
            if (failFirstComment.delete(id)) {
              return data(
                res,
                {
                  error: {
                    code: "fixture_first_message_failure",
                    message: "模拟首条消息网络失败，请重试",
                  },
                },
                503,
              );
            }
            for (const item of events[id] || []) {
              if (item.tool_approval?.status === "pending") {
                item.tool_approval.status = "superseded";
                item.tool_approval.resolution_comment = "已有后续新对话，旧审批已自动失效";
                item.tool_approval.resolved_at = now();
              }
            }
            card.pending_tool_approval_count = 0;
            events[id].push({
              id: "event-" + ++seq,
              sequence: seq,
              type: "comment",
              content: input.content,
              actor_kind: "human",
              actor_name: "测试用户",
              created_at: now(),
            });
            return data(
              res,
              { event_id: "event-" + seq, turn_id: "turn-demo" },
              201,
            );
          }
          if (rest === "reply" && req.method === "POST") {
            const input = await body(req);
            events[id].push({
              id: "event-" + ++seq,
              sequence: seq,
              type: "comment",
              content: input.content,
              actor_kind: "human",
              actor_name: "测试用户",
              created_at: now(),
            });
            return data(
              res,
              { event_id: "event-" + seq, turn_id: "turn-demo" },
              201,
            );
          }
          if (rest?.startsWith("tool-approvals/") && rest.endsWith("/resolve") && req.method === "POST") {
            const approvalID = rest.split("/")[1];
            const input = await body(req);
            const ev = (events[id] || []).find((e) => e.tool_approval?.id === approvalID);
            if (ev && ev.tool_approval) {
              ev.tool_approval.status = input.action === "reject" ? "rejected" : "approved";
              ev.tool_approval.resolution_comment = input.comment || "";
              ev.tool_approval.resolved_at = now();
            }
            return data(res, {
              approval: ev?.tool_approval,
              turn_id: "turn-demo",
            });
          }
          if (rest === "schedules" && req.method === "POST") {
            const created = {
              id: "schedule-" + Date.now(),
              ...(await body(req)),
              enabled: true,
              next_trigger_at: now(),
            };
            cardSchedules[id] = [...(cardSchedules[id] || []), created];
            return data(res, created, 201);
          }
          if (rest?.startsWith("schedules/")) {
            const scheduleID = rest.slice("schedules/".length);
            const list = cardSchedules[id] || [];
            const index = list.findIndex((item) => item.id === scheduleID);
            if (index === -1) return failure(res);
            if (req.method === "DELETE") {
              cardSchedules[id] = list.filter((item) => item.id !== scheduleID);
              res.writeHead(204, { "Cache-Control": "no-store" });
              res.end();
              return;
            }
            if (req.method === "PUT") {
              const updated = {
                ...list[index],
                ...(await body(req)),
                id: scheduleID,
              };
              list[index] = updated;
              return data(res, updated, 200);
            }
          }
          if (rest && !rest.startsWith("watchers/") && rest.includes("/cancel"))
            return data(res, {});
          if (rest?.includes("/retry"))
            return data(res, { attempt_id: "attempt-retry-demo" });
        }
        if (route === "/agent/status") return data(res, { model_configured: settings.routes.some((route) => route.enabled) });
        if (route === "/agent/api-credentials") {
          if (req.method === "GET")
            return data(res, settings.api_credentials || []);
          if (req.method === "POST") {
            const input = await body(req);
            const baseURL = String(input.base_url || "")
              .trim()
              .replace(/\/+$/, "");
            let defaultName = baseURL;
            try {
              defaultName = new URL(baseURL).hostname || baseURL;
            } catch {}
            const created = {
              id: "00000000-0000-4000-8000-" + String(Date.now()).slice(-12),
              name: String(input.name || "").trim() || defaultName,
              base_url: baseURL,
              api_key_env: String(input.api_key_env || "").trim(),
              api_key_configured: Boolean(
                String(input.api_key || "").trim() ||
                  String(input.api_key_env || "").trim(),
              ),
            };
            settings.api_credentials = [
              ...(settings.api_credentials || []),
              created,
            ];
            return data(res, created, 201);
          }
        }
        if (route.startsWith("/agent/api-credentials/")) {
          const credID = decodeURIComponent(route.split("/").at(-1));
          const list = settings.api_credentials || [];
          const existing = list.find((c) => c.id === credID);
          if (!existing) return failure(res);
          if (req.method === "DELETE") {
            settings.api_credentials = list.filter((c) => c.id !== credID);
            settings.routes = (settings.routes || []).filter(
              (r) => r.api_credential_id !== credID,
            );
            return data(res, null, 204);
          }
          if (req.method === "PUT") {
            const input = await body(req);
            const baseURL = String(input.base_url || existing.base_url)
              .trim()
              .replace(/\/+$/, "");
            existing.base_url = baseURL;
            if (String(input.name || "").trim()) {
              existing.name = String(input.name).trim();
            }
            if (input.clear_api_key) {
              existing.api_key_configured = false;
              existing.api_key_env = "";
            } else if (String(input.api_key || "").trim()) {
              existing.api_key_configured = true;
            }
            settings.routes = (settings.routes || []).map((r) =>
              r.api_credential_id === credID
                ? {
                    ...r,
                    api_credential_name: existing.name,
                    provider_base_url: existing.base_url,
                    api_key_configured: existing.api_key_configured,
                    api_key_env: existing.api_key_env,
                  }
                : r,
            );
            return data(res, existing);
          }
        }
        if (route === "/agent/settings") {
          if (req.method === "PUT") {
            const incoming = await body(req);
            const creds = [...(settings.api_credentials || [])];
            const nextRoutes = (incoming.routes || []).map((r) => {
              if ((r.provider_type || "openai_compatible") !== "openai_compatible")
                return r;
              const normalizedURL = String(r.provider_base_url || "")
                .trim()
                .replace(/\/+$/, "");
              let cred =
                creds.find((c) => c.id === r.api_credential_id) ||
                creds.find((c) => c.base_url === normalizedURL);
              if (!cred && normalizedURL) {
                let host = normalizedURL;
                try {
                  host = new URL(normalizedURL).hostname || normalizedURL;
                } catch {}
                cred = {
                  id:
                    "00000000-0000-4000-8000-" +
                    String(Date.now() + Math.floor(Math.random() * 1000)).slice(
                      -12,
                    ),
                  name: host,
                  base_url: normalizedURL,
                  api_key_env: String(r.api_key_env || "").trim(),
                  api_key_configured: Boolean(
                    String(r.api_key || "").trim() ||
                      String(r.api_key_env || "").trim(),
                  ),
                };
                creds.push(cred);
              } else if (cred && String(r.api_key || "").trim()) {
                cred.api_key_configured = true;
              }
              return {
                ...r,
                api_credential_id: cred ? cred.id : r.api_credential_id,
                api_credential_name: cred ? cred.name : r.api_credential_name,
                provider_base_url: cred ? cred.base_url : normalizedURL,
                api_key_env: cred ? cred.api_key_env : r.api_key_env,
                api_key_configured: cred
                  ? cred.api_key_configured
                  : Boolean(r.api_key_configured),
              };
            });
            settings = {
              ...settings,
              ...incoming,
              api_credentials: creds,
              routes: nextRoutes,
            };
            return data(res, settings);
          }
          return data(res, settings);
        }
        if (route === "/agent/provider-models" && req.method === "POST") {
          return data(res, [
            { id: "example-model", name: "Example Model" },
            { id: "example-model-pro", name: "Example Model Pro" },
          ]);
        }
        if (route === "/agent/model-auth")
          return data(res, [
            {
              provider: "chatgpt",
              available: false,
              connected: false,
              accounts: [],
            },
            {
              provider: "grok",
              available: true,
              connected: false,
              accounts: [],
            },
          ]);
        if (route === "/agent/nexusdock/devices")
          return data(res, { available: true, connected: nexusConnected, devices: nexusConnected ? [
            { id: "node-rn", name: "RN Server", online: true, os: "linux", arch: "amd64", agent_version: "1.1.1" },
            { id: "node-mac", name: "MacBook Pro", online: true, os: "darwin", arch: "arm64", agent_version: "1.1.1" },
            { id: "node-win", name: "Office PC", online: false, os: "windows", arch: "amd64", agent_version: "1.1.0" },
          ] : [] });
        if (route === "/agent/nexusdock/fleet")
          return data(res, {
            available: true,
            connected: nexusConnected,
            devices: nexusConnected
              ? [
                  {
                    id: "node-rn",
                    name: "RN Server",
                    online: true,
                    os: "linux",
                    arch: "amd64",
                    agent_version: "1.1.1",
                    capabilities: ["exec_command", "file_edit"],
                    plugins: [
                      {
                        name: "cloudflare",
                        version: "1.2.0",
                        enabled: true,
                        description: "Cloudflare Workers、Pages 与 MCP 管理套件",
                        skills_count: 2,
                        mcp_count: 1,
                        format: "portable",
                      },
                      {
                        name: "context7",
                        version: "0.9.1",
                        enabled: true,
                        description: "实时文档检索与上下文增强插件",
                        skills_count: 0,
                        mcp_count: 1,
                        format: "portable",
                      },
                    ],
                    skills: [
                      {
                        name: "docker-ops",
                        description: "巡检与管理容器运行状态",
                        source_type: "managed",
                      },
                      {
                        name: "cloudflare",
                        description: "部署与诊断 Cloudflare Workers 与 KV/D1 资源",
                        source_type: "plugin",
                        plugin_name: "cloudflare",
                      },
                      {
                        name: "durable-objects",
                        description: "管理 Durable Objects 状态与迁移",
                        source_type: "plugin",
                        plugin_name: "cloudflare",
                      },
                      {
                        name: "team-standards",
                        description: "团队共享研发与发布检查清单",
                        source_type: "shared",
                      },
                    ],
                    dynamic_mcp: [
                      {
                        name: "plugin.cloudflare.cloudflare-api",
                        display_name: "cloudflare-api",
                        description: "Cloudflare API 管理接口",
                        status: "active",
                        tool_count: 14,
                        source_type: "plugin",
                        plugin_name: "cloudflare",
                      },
                      {
                        name: "plugin.context7.context7",
                        display_name: "context7",
                        description: "第三方库官方文档检索服务",
                        status: "active",
                        tool_count: 2,
                        source_type: "plugin",
                        plugin_name: "context7",
                      },
                      {
                        name: "bitwarden",
                        display_name: "Bitwarden",
                        description: "独立托管的凭据与密钥库 MCP 服务",
                        status: "active",
                        tool_count: 6,
                        source_type: "managed",
                      },
                    ],
                  },
                  {
                    id: "node-mac",
                    name: "MacBook Pro",
                    online: true,
                    os: "darwin",
                    arch: "arm64",
                    agent_version: "1.1.1",
                    capabilities: ["exec_command", "browser_open"],
                    skills: [],
                    dynamic_mcp: [],
                  },
                  {
                    id: "node-win",
                    name: "Office PC",
                    online: false,
                    os: "windows",
                    arch: "amd64",
                    agent_version: "1.1.0",
                    capabilities: [],
                    skills: [],
                    dynamic_mcp: [],
                  },
                ]
              : [],
          });
        if (route === "/agent/nexusdock") {
          if (req.method === "DELETE") nexusConnected = false;
          return data(res, { available: true, connected: nexusConnected });
        }
        if (route === "/agent/device-tool-guards") {
          return data(res, deviceToolGuards);
        }
        if (route === "/agent/tool-guard") {
          if (req.method === "GET") {
            return data(res, toolGuardSettings);
          }
          if (req.method === "PUT") {
            const input = await body(req);
            const mode = String(input.mode || "").trim();
            if (!["block_high_risk", "strict"].includes(mode)) {
              return data(
                res,
                {
                  error: {
                    code: "invalid_mode",
                    message: "mode must be block_high_risk or strict",
                  },
                },
                400,
              );
            }
            const timeout =
              input.timeout_seconds === undefined || input.timeout_seconds === null
                ? 10
                : input.timeout_seconds;
            if (typeof timeout !== "number" || !Number.isInteger(timeout) || timeout < 2 || timeout > 60) {
              return data(
                res,
                {
                  error: {
                    code: "invalid_timeout",
                    message: "timeout_seconds must be between 2 and 60",
                  },
                },
                400,
              );
            }
            toolGuardSettings = {
              mode,
              model_route_id:
                typeof input.model_route_id === "string" ? input.model_route_id.trim() : "",
              timeout_seconds: timeout,
              custom_rules:
                typeof input.custom_rules === "string" ? input.custom_rules : "",
            };
            return data(res, toolGuardSettings);
          }
        }
        if (route.startsWith("/agent/device-tool-guards/")) {
          const nodeID = decodeURIComponent(route.split("/").at(-1));
          const existing = deviceToolGuards.find((g) => g.device_node_id === nodeID);
          if (req.method === "GET") {
            if (existing) return data(res, existing);
            return data(res, { device_node_id: nodeID, enabled: false });
          }
          if (req.method === "PUT") {
            const input = await body(req);
            const allowedKeys = new Set([
              "device_node_id",
              "device_name",
              "enabled",
            ]);
            if (
              !input ||
              typeof input !== "object" ||
              Array.isArray(input) ||
              Object.keys(input).some((key) => !allowedKeys.has(key))
            ) {
              return data(
                res,
                { error: { code: "invalid_json", message: "invalid JSON request" } },
                400,
              );
            }
            if (
              input.device_node_id !== undefined &&
              input.device_node_id !== null &&
              String(input.device_node_id).trim() !== "" &&
              String(input.device_node_id).trim() !== nodeID
            ) {
              return data(
                res,
                {
                  error: {
                    code: "invalid_node_id",
                    message: "device_node_id must match URL nodeID",
                  },
                },
                400,
              );
            }
            if (typeof input.enabled !== "boolean") {
              return data(
                res,
                { error: { code: "invalid_json", message: "invalid JSON request" } },
                400,
              );
            }
            const normalized = {
              device_node_id: nodeID,
              device_name:
                typeof input.device_name === "string" ? input.device_name.trim() : "",
              enabled: input.enabled,
            };
            if (existing) {
              Object.assign(existing, normalized, { updated_at: now() });
              return data(res, existing);
            }
            const created = { id: "guard-" + Date.now(), ...normalized, created_at: now(), updated_at: now() };
            deviceToolGuards.push(created);
            return data(res, created);
          }
          if (req.method === "DELETE") {
            deviceToolGuards = deviceToolGuards.filter((g) => g.device_node_id !== nodeID);
            return data(res, { ok: true });
          }
        }
        if (route === "/agent/mcp-connections") {
          if (req.method === "GET") return data(res, mcp);
          if (req.method === "POST") {
            const { bearer_token, ...settings } = await body(req);
            const item = { id: "mcp-" + Date.now(), ...settings, token_configured: !!bearer_token };
            mcp.push(item);
            return data(res, item, 201);
          }
        }
        if (route.startsWith("/agent/mcp-connections/")) {
          const id = route.split("/").at(-1);
          const item = mcp.find((x) => x.id === id);
          if (req.method === "DELETE") {
            mcp = mcp.filter((x) => x.id !== id);
            return data(res, {}, 204);
          }
          if (req.method === "PUT") {
            const { bearer_token, clear_bearer_token, ...settings } = await body(req);
            Object.assign(item, settings);
            if (bearer_token) item.token_configured = true;
            if (clear_bearer_token) item.token_configured = false;
            return data(res, item);
          }
        }
        if (route === "/agent/mcp-test") {
          const input = await body(req);
          // Match Go decodeJSON's strict contract; passing MCP.enabled would fail in production.
          const allowed = ["name", "url", "bearer_token", "connection_id", "clear_bearer_token", "timeout_ms"];
          if (Object.keys(input).some((key) => !allowed.includes(key)))
            return data(
              res,
              {
                error: {
                  code: "invalid_json",
                  message: "unexpected MCP test field",
                },
              },
              400,
            );
          return data(res, {
            ok: true,
            code: "ok",
            message: "测试连接成功",
            duration_ms: 52,
            tool_count: 2,
          });
        }
        if (route === "/agent/provider-test")
          return data(res, {
            ok: true,
            code: "ok",
            message: "测试连接成功",
            duration_ms: 52,
            tool_count: 2,
          });
        if (route === "/channels" && req.method === "GET")
          return data(res, { channels: remoteChannels });
        if (route === "/channels" && req.method === "POST") {
          const input = await body(req);
          const created = {
            id: "ch-" + Date.now(),
            workspace_id: "demo-workspace",
            name: input.name || "新建通道",
            platform: input.platform,
            role: input.role,
            transport: input.transport,
            enabled: input.enabled ?? true,
            project_id: input.project_id,
            target_card_id: input.target_card_id || null,
            push_filter: input.push_filter || "all",
            mirror_web_messages: Boolean(input.mirror_web_messages),
            group_policy: input.group_policy || "mention_only",
            allow_from: input.allow_from || [],
            config: input.config || {},
            has_secrets: Boolean(
              input.secrets && Object.values(input.secrets).some(Boolean),
            ),
            secret_hints: input.secrets
              ? Object.fromEntries(
                  Object.entries(input.secrets)
                    .filter(([, v]) => Boolean(v))
                    .map(([k]) => [k, "****configured"]),
                )
              : {},
            runtime_status: input.enabled === false ? "disabled" : "connected",
            last_error: "",
            created_at: now(),
            updated_at: now(),
          };
          remoteChannels = [created, ...remoteChannels];
          return data(res, { channel: created }, 201);
        }
        if (route === "/channels/weixin/login/start" && req.method === "POST") {
          const input = await body(req);
          const sid = "wx-sess-" + Date.now();
          const sess = {
            session_id: sid,
            channel_id: input.channel_id || "",
            qrcode_id: "qr-" + sid,
            qrcode_content: "https://ilinkai.weixin.qq.com/qr/demo-cycledock-" + sid,
            status: "wait",
            base_url: input.base_url || "https://ilinkai.weixin.qq.com",
            expires_at: new Date(Date.now() + 5 * 60_000).toISOString(),
            created_at: now(),
            updated_at: now(),
          };
          wxLoginSessions.set(sid, sess);
          return data(res, { session: sess }, 201);
        }
        const wxPollMatch = route.match(/^\/channels\/weixin\/login\/([^/]+)$/);
        if (wxPollMatch && req.method === "GET") {
          const sess = wxLoginSessions.get(wxPollMatch[1]);
          if (!sess) return failure(res);
          return data(res, { session: sess });
        }
        const chTestMatch = route.match(/^\/channels\/([^/]+)\/test$/);
        if (chTestMatch && req.method === "POST") {
          const item = remoteChannels.find((c) => c.id === chTestMatch[1]);
          if (!item) return failure(res);
          return data(res, { ok: true, message: "测试消息已成功发送", channel: item });
        }
        const chToggleMatch = route.match(/^\/channels\/([^/]+)\/enabled$/);
        if (chToggleMatch && req.method === "POST") {
          const item = remoteChannels.find((c) => c.id === chToggleMatch[1]);
          if (!item) return failure(res);
          const input = await body(req);
          item.enabled = Boolean(input.enabled);
          item.runtime_status = item.enabled ? "connected" : "disabled";
          item.updated_at = now();
          return data(res, { channel: item });
        }
        const chMatch = route.match(/^\/channels\/([^/]+)$/);
        if (chMatch) {
          const idx = remoteChannels.findIndex((c) => c.id === chMatch[1]);
          if (idx === -1) return failure(res);
          if (req.method === "DELETE") {
            remoteChannels.splice(idx, 1);
            return data(res, { deleted: true, id: chMatch[1] });
          }
          if (req.method === "PUT") {
            const input = await body(req);
            Object.assign(remoteChannels[idx], {
              name: input.name,
              role: input.role,
              transport: input.transport,
              enabled: input.enabled ?? remoteChannels[idx].enabled,
              project_id: input.project_id,
              target_card_id: input.target_card_id || null,
              push_filter: input.push_filter || "all",
              mirror_web_messages: Boolean(input.mirror_web_messages),
              group_policy: input.group_policy || "mention_only",
              allow_from: input.allow_from || [],
              config: input.config || {},
              updated_at: now(),
            });
            return data(res, { channel: remoteChannels[idx] });
          }
        }
        if (route === "/notifications/push" && req.method === "GET")
          return data(res, { public_key: "" });
        if (route === "/triggers" && req.method === "GET") {
          return data(res, { triggers });
        }
        if (route === "/triggers" && req.method === "POST") {
          const input = await body(req);
          const id = `trig-${Date.now().toString(36)}`;
          const secret =
            input.secret ||
            `whsec_demo_${Math.random().toString(36).slice(2, 12)}_${Date.now().toString(36)}`;
          const targetProject = projects.find(
            (pr) => pr.id === input.target_project_id,
          );
          const targetCard = cards.find((c) => c.id === input.target_card_id);
          const created = {
            id,
            name: input.name,
            provider: input.provider || "github",
            auth_mode: input.auth_mode || "github_hmac_sha256",
            signature_header: input.signature_header || "",
            secret_hint: `${secret.slice(0, 6)}...${secret.slice(-4)}`,
            webhook_path: `/api/v1/webhooks/${id}`,
            enabled: input.enabled ?? true,
            routing_mode: input.routing_mode || "match_watchers",
            target_project_id: input.target_project_id || undefined,
            target_project_name: targetProject?.name,
            target_card_id: input.target_card_id || undefined,
            target_card_project_id: targetCard?.project_id,
            target_card_kind: targetCard?.kind,
            target_card_title: targetCard?.title,
            dispatch_mode: input.dispatch_mode || "enqueue",
            filter_config: input.filter_config || {},
            cooldown_seconds: input.cooldown_seconds || 0,
            created_at: now(),
            updated_at: now(),
          };
          triggers = [created, ...triggers];
          return data(res, { trigger: created, secret }, 201);
        }
        if (route === "/triggers/deliveries" && req.method === "GET") {
          return data(res, { deliveries: triggerDeliveries });
        }
        const trigMatch = route.match(/^\/triggers\/([^/]+)$/);
        if (trigMatch) {
          const id = decodeURIComponent(trigMatch[1]);
          const idx = triggers.findIndex((t) => t.id === id);
          if (idx === -1) return failure(res, 404);
          if (req.method === "PATCH") {
            const input = await body(req);
            const cur = triggers[idx];
            const next = {
              ...cur,
              ...input,
              updated_at: now(),
            };
            const targetProject = projects.find(
              (pr) => pr.id === next.target_project_id,
            );
            const targetCard = cards.find((c) => c.id === next.target_card_id);
            next.target_project_name = targetProject?.name;
            next.target_card_project_id = targetCard?.project_id;
            next.target_card_kind = targetCard?.kind;
            next.target_card_title = targetCard?.title;
            triggers[idx] = next;
            return data(res, { trigger: next });
          }
          if (req.method === "DELETE") {
            triggers.splice(idx, 1);
            triggerDeliveries = triggerDeliveries.filter(
              (d) => d.trigger_id !== id,
            );
            return data(res, { deleted: true });
          }
        }
        const trigRotateMatch = route.match(
          /^\/triggers\/([^/]+)\/rotate-secret$/,
        );
        if (trigRotateMatch && req.method === "POST") {
          const id = decodeURIComponent(trigRotateMatch[1]);
          const item = triggers.find((t) => t.id === id);
          if (!item) return failure(res, 404);
          const secret = `whsec_rot_${Math.random().toString(36).slice(2, 12)}_${Date.now().toString(36)}`;
          item.secret_hint = `${secret.slice(0, 6)}...${secret.slice(-4)}`;
          item.updated_at = now();
          return data(res, { trigger: item, secret });
        }
        const trigDelivMatch = route.match(/^\/triggers\/([^/]+)\/deliveries$/);
        if (trigDelivMatch && req.method === "GET") {
          const id = decodeURIComponent(trigDelivMatch[1]);
          return data(res, {
            deliveries: triggerDeliveries.filter((d) => d.trigger_id === id),
          });
        }
        if (route === "/watchers" && req.method === "GET") {
          const armedOnly = url.searchParams.get("armed_only") !== "false";
          const projID = url.searchParams.get("project_id") || "";
          const filtered = cardWatchers.filter((w) => {
            if (armedOnly && w.status !== "armed") return false;
            if (projID && w.project_id !== projID) return false;
            return true;
          });
          return data(res, { watchers: filtered });
        }
        const cardWatchersMatch = route.match(/^\/cards\/([^/]+)\/watchers$/);
        if (cardWatchersMatch && req.method === "GET") {
          const cid = decodeURIComponent(cardWatchersMatch[1]);
          const armedOnly = url.searchParams.get("armed_only") === "true";
          const filtered = cardWatchers.filter(
            (w) => w.card_id === cid && (!armedOnly || w.status === "armed"),
          );
          return data(res, { watchers: filtered });
        }
        const cancelWatcherMatch = route.match(
          /^\/cards\/([^/]+)\/watchers\/([^/]+)\/cancel$/,
        );
        if (cancelWatcherMatch && req.method === "POST") {
          const cid = decodeURIComponent(cancelWatcherMatch[1]);
          const wid = decodeURIComponent(cancelWatcherMatch[2]);
          const item = cardWatchers.find(
            (w) => w.card_id === cid && w.id === wid,
          );
          if (!item) return failure(res, 404);
          item.status = "cancelled";
          item.resolved_reason = "manual_cancelled";
          item.resolved_at = now();
          item.updated_at = now();
          return data(res, { watcher: item });
        }
        if (
          (route === "/notifications/push/subscription" &&
            (req.method === "PUT" || req.method === "DELETE")) ||
          ((route === "/notifications/push/presence" ||
            route === "/notifications/push/inactive" ||
            route === "/notifications/push/seen") &&
            req.method === "POST")
        ) {
          await body(req).catch(() => ({}));
          return data(res, {}, 204);
        }
        return failure(res);
      }
      const dest = path.resolve(root, "." + (p === "/" ? "/index.html" : p));
      let file = dest.startsWith(root + path.sep)
        ? dest
        : path.join(root, "index.html");
      let result;
      try {
        result = await fs.readFile(file);
      } catch {
        file = path.join(root, "index.html");
        result = await fs.readFile(file);
      }
      const type = file.endsWith(".js")
        ? "text/javascript"
        : file.endsWith(".css")
          ? "text/css"
          : file.endsWith(".svg")
            ? "image/svg+xml"
            : "text/html";
      res.writeHead(200, { "Content-Type": type + "; charset=utf-8" });
      res.end(result);
    } catch (e) {
      console.error("Fixture error", e);
      data(res, { error: { code: "fixture_error", message: String(e) } }, 500);
    }
  })
  .listen(Number(process.env.MOCK_PORT || 4188), "127.0.0.1", () =>
    console.log(
      "CycleDock smoke fixture: http://127.0.0.1:" +
        (process.env.MOCK_PORT || 4188),
    ),
  );
