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
    schedules:
      card.kind === "ongoing"
        ? [
            {
              id: "schedule-demo",
              enabled: true,
              frequency: "daily",
              hour: 9,
              minute: 0,
              timezone: "Asia/Shanghai",
              next_trigger_at: new Date(Date.now() + 86400000).toISOString(),
            },
          ]
        : [],
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
            todo_state: "todo",
            created_at: now(),
            updated_at: now(),
          };
          cards.unshift(card);
          events[card.id] = [];
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
            card.unread = false;
            return data(res, { last_read_message_sequence: latestSeq });
          }
          if (rest === "ongoing-state" && req.method === "POST") {
            card.ongoing_state = (await body(req)).state;
            return data(res, card);
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
          if (rest === "schedules" && req.method === "POST")
            return data(
              res,
              {
                id: "schedule-new",
                ...(await body(req)),
                next_trigger_at: now(),
              },
              201,
            );
          if (rest?.startsWith("schedules/"))
            return data(
              res,
              req.method === "DELETE" ? undefined : await body(req),
              req.method === "DELETE" ? 204 : 200,
            );
          if (rest?.includes("/cancel")) return data(res, {});
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
                    skills: [
                      {
                        name: "docker-ops",
                        description: "巡检与管理容器运行状态",
                        source_type: "managed",
                      },
                    ],
                    dynamic_mcp: [],
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
        if (route === "/notifications/push" && req.method === "GET")
          return data(res, { public_key: "" });
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
