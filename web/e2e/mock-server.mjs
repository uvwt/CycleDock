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
  instructions: "保障线上服务稳定",
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};
const secondProject = {
  id: "demo-project-b",
  name: "产品发布",
  description: "发布计划与上线检查",
  instructions: "按发布清单推进",
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};
const projects = [project, secondProject];
const now = () => new Date().toISOString();
const hoursAgo = (hours) =>
  new Date(Date.now() - hours * 60 * 60 * 1000).toISOString();
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
    current_run_status: "running",
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
let grokConnected = false;
const failFirstComment = new Set();
let failNextDiscussionCreate = false;
let settings = {
  configured: true,
  enabled: true,
  provider_type: "openai_compatible",
  provider_base_url: "https://api.example.com/v1",
  model: "example-model",
  api_key_env: "CYCLEDOCK_MODEL_API_KEY",
  request_timeout_ms: 120000,
  max_tool_rounds: 16,
  system_instructions: "先检查现场，再确认结果",
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
    bearer_token_env: "",
    timeout_ms: 30000,
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
// 使用与真实 Schedule API 同步的夹具状态，覆盖新增、修改和删除闭环。
const schedulesByCard = {
  "ongoing-1": [{
    id: "schedule-demo", enabled: true, frequency: "daily",
    hour: 9, minute: 0, timezone: "Asia/Shanghai",
    next_trigger_at: new Date(Date.now() + 86400000).toISOString(),
  }],
};
function details(id) {
  const card = cards.find((x) => x.id === id);
  if (!card) return null;
  return {
    card,
    timeline: events[id] || [],
    current_run: card.current_run_status
      ? {
          id: "run-demo",
          trigger_id: "trigger-running",
          source_event_id: "event-3",
          status: card.current_run_status,
          current_stage: "分析错误日志",
          created_at: now(),
          started_at: new Date(Date.now() - 32000).toISOString(),
        }
      : null,
    queue:
      id === "todo-1"
        ? [
            {
              id: "trigger-queued",
              trigger_id: "trigger-queued",
              sequence: 6,
              cause: "comment",
              dispatch_mode: "enqueue",
              cause_event_id: "event-7",
              source_event_id: "event-7",
              content: "顺便检查磁盘空间。",
              created_at: now(),
            },
          ]
        : [],
    work_history:
      id === "todo-1"
        ? [
            {
              trigger_id: "trigger-failed",
              cause_event_id: "event-8",
              source_event_id: "event-8",
              status: "failed",
              sequence: 1,
              content: "先检查昨天的日志。",
              cause: "comment",
              error_summary: "远程节点暂时不可用",
              can_retry: true,
              created_at: hoursAgo(5),
              finished_at: hoursAgo(4.9),
            },
            {
              trigger_id: "trigger-schedule-history",
              status: "completed",
              sequence: 2,
              content: "定时巡检触发",
              cause: "schedule",
              result_summary: "巡检完成",
              can_retry: false,
              created_at: hoursAgo(2),
              finished_at: hoursAgo(1.9),
            },
          ]
        : [],
    schedules: schedulesByCard[id] || [],
    health: card.health || "unknown",
    last_checkin: id === "ongoing-1" ? events[id][0] : null,
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
          return data(res, session.workspace.id === "demo-workspace-2" ? [secondProject] : projects);
        if (route === "/projects" && req.method === "POST") {
          const created = {
            ...(await body(req)),
            id: "project-" + Date.now(),
            created_at: now(),
            updated_at: now(),
          };
          projects.push(created);
          return data(res, created, 201);
        }
        const projectUpdate = route.match(/^\/projects\/([^/]+)$/);
        if (projectUpdate && req.method === "PATCH") {
          const target = projects.find((item) => item.id === projectUpdate[1]);
          if (!target) return failure(res);
          Object.assign(target, await body(req), { updated_at: now() });
          return data(res, target);
        }
        const projectCards = route.match(/^\/projects\/([^/]+)\/cards$/);
        if (projectCards && req.method === "GET") {
          return data(
            res,
            cards.filter((card) => card.project_id === projectCards[1]),
          );
        }
        const discussionCreate = route.match(/^\/projects\/([^/]+)\/discussions$/);
        if (discussionCreate && req.method === "POST") {
          const target = projects.find((item) => item.id === discussionCreate[1]);
          if (!target) return failure(res);
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
            project_id: target.id,
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
        const todoCreate = route.match(/^\/projects\/([^/]+)\/todos$/);
        if (todoCreate && req.method === "POST") {
          const target = projects.find((item) => item.id === todoCreate[1]);
          if (!target) return failure(res);
          const input = await body(req);
          const card = {
            id: "todo-" + Date.now(),
            project_id: target.id,
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
                    send("run.started", {
                      run_id: "run-demo",
                      started_at: new Date(Date.now() - 35000).toISOString(),
                    }),
                  500,
                ),
                setTimeout(
                  () =>
                    send("run.text.delta", {
                      run_id: "run-demo",
                      text: "正在读取 RN 服务器日志。",
                    }),
                  1100,
                ),
                setTimeout(
                  () =>
                    send("run.text.delta", {
                      run_id: "run-demo",
                      text: "\n\n已定位 Docker 容器的错误记录。",
                    }),
                  1600,
                ),
              ];
              req.on("close", () => timers.forEach(clearTimeout));
            }
            return;
          }
          if (!rest && req.method === "GET") return data(res, details(id));
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
              { event_id: "event-" + seq, trigger_id: "trigger-demo" },
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
              { event_id: "event-" + seq, trigger_id: "trigger-demo" },
              201,
            );
          }
          if (rest === "schedules" && req.method === "POST") {
            const schedule = {
              ...(await body(req)), id: "schedule-" + Date.now(), next_trigger_at: now(),
            };
            if (!schedulesByCard[id]) schedulesByCard[id] = [];
            schedulesByCard[id].push(schedule);
            return data(res, schedule, 201);
          }
          if (rest?.startsWith("schedules/")) {
            const scheduleID = rest.slice("schedules/".length);
            const plans = schedulesByCard[id] || [];
            const existing = plans.find(item => item.id === scheduleID);
            if (!existing) return failure(res);
            if (req.method === "DELETE") {
              plans.splice(plans.indexOf(existing), 1);
              return data(res, undefined, 204);
            }
            if (req.method === "PUT") {
              Object.assign(existing, await body(req));
              return data(res, existing);
            }
          }
          if (rest?.includes("/cancel") || rest?.includes("/retry"))
            return data(res, { trigger_id: "retry-demo" });
        }
        if (route === "/agent/settings") {
          if (req.method === "PUT") {
            settings = { ...settings, ...(await body(req)) };
            return data(res, settings);
          }
          return data(res, settings);
        }
        // 仅测试夹具使用：模拟已经授权的账号及其实际模型目录。
        if (route === "/__fixture/connect-grok" && req.method === "POST") {
          grokConnected = true;
          return data(res, { connected: true });
        }
        if (route === "/agent/model-auth")
          return data(res, [
            { provider: "chatgpt", available: false, connected: false },
            { provider: "grok", available: true, connected: grokConnected },
          ]);
        if (route === "/agent/models" && url.searchParams.get("provider") === "grok")
          return grokConnected
            ? data(res, [
                { id: "grok-3-mini", name: "Grok Mini (测试目录)" },
                { id: "grok-4", name: "Grok 4 (测试目录)" },
              ])
            : data(res, { error: { code: "not_connected", message: "Not connected" } }, 403);
        if (route === "/agent/nexusdock")
          return data(res, { available: true, connected: false });
        if (route === "/agent/mcp-connections") {
          if (req.method === "GET") return data(res, mcp);
          if (req.method === "POST") {
            const item = { id: "mcp-" + Date.now(), ...(await body(req)) };
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
            Object.assign(item, await body(req));
            return data(res, item);
          }
        }
        if (route === "/agent/mcp-test") {
          const input = await body(req);
          // Match Go decodeJSON's strict contract; passing MCP.enabled would fail in production.
          const allowed = ["name", "url", "bearer_token_env", "timeout_ms"];
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
