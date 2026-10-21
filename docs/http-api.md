# Human Web HTTP API

Phase 2 的 HTTP API 只服务 Human Web。所有业务路由的 Workspace 与 Human Source 都来自服务端 Web Session，不接受客户端通过 `workspace_id` / `source_id` 切换租户。

## Auth

- `GET /api/v1/auth/methods`
- `POST /api/v1/auth/login`：Self-hosted Administrator password。
- `POST /api/v1/auth/logout`
- `GET /auth/nexusdock/start`
- `GET /auth/nexusdock/callback`
- `GET /api/v1/me`

Web Session Cookie 名为 `cycledock_session`，HttpOnly + SameSite=Lax；Hosted HTTPS 同时启用 Secure。数据库只保存随机 token 的 SHA-256 digest。

## Projects / Board

- `GET /api/v1/projects`
- `POST /api/v1/projects`
- `GET /api/v1/projects/{projectID}`
- `PATCH /api/v1/projects/{projectID}`
- `GET /api/v1/projects/{projectID}/cards`
- `POST /api/v1/projects/{projectID}/discussions`：创建空白 active Discussion；首轮会话由 Built-in Agent 生成标题与摘要。

Project 即 Board，不存在独立 Board API。

## Card

- `GET /api/v1/cards/{cardID}`：返回 Card、current Run、Timeline、pending Trigger queue、最近终态 work history、Schedule、Health / Last Check-in。
- `PATCH /api/v1/cards/{cardID}`
- `POST /api/v1/cards/{cardID}/move`：仅 Todo 状态移动。
- `POST /api/v1/cards/{cardID}/discussion-state`：当前只允许 Human 将 Discussion 归档。
- `DELETE /api/v1/cards/{cardID}`：当前只允许 Human 删除 Discussion；已生成的 Todo / Ongoing 保留，来源引用置空。
- `POST /api/v1/cards/{cardID}/ongoing-state`

Discussion 不能转换为 Todo / Ongoing。Todo 的 Review → Done 仍由 Store 的 Human-only 规则保证，HTTP Handler 不复制状态机。

## Direction / Queue

- `POST /api/v1/cards/{cardID}/comments`
- `POST /api/v1/cards/{cardID}/reply`
- `POST /api/v1/cards/{cardID}/triggers/{triggerID}/cancel`
- `POST /api/v1/cards/{cardID}/triggers/{triggerID}/retry`
- `POST /api/v1/cards/{cardID}/runs/{runID}/cancel`

Comment 请求要求 `Idempotency-Key` Header。

`comments` 的 `dispatch_mode` 支持：

- `enqueue`
- `interrupt`

Human Web 不提交 runner source。interrupt 与 waiting_human reply 在 Store 的 Card 锁内沿用 current Run 的 Runner，避免 Handler “先查再写”造成并发竞态。

没有 AgentRun 的 Trigger 即 pending queue item。

取消与 retry 不复写旧历史：

- pending Trigger cancel 只记录该 Trigger 的取消元数据，不伪造 AgentRun。
- current queued / running / waiting_human Run cancel 后进入 `cancelled`。
- retry failed / expired / cancelled work 时创建新的 `manual_retry` Trigger。
- 同一个原 Trigger 只允许一条 retry lineage；幂等请求复用已创建的新 Trigger。
- `interrupt` 表达“立即换方向继续”，`cancel` 表达“停止当前工作”，两者不混用。

## Built-in Agent settings / connection test

Workspace owner：

- `GET /api/v1/agent/settings`
- `PUT /api/v1/agent/settings`
- `POST /api/v1/agent/provider-test`
- `GET /api/v1/agent/mcp-connections`
- `POST /api/v1/agent/mcp-connections`
- `PUT /api/v1/agent/mcp-connections/{connectionID}`
- `DELETE /api/v1/agent/mcp-connections/{connectionID}`
- `POST /api/v1/agent/mcp-test`

- GET /api/v1/agent/model-auth：只返回 available / connected / expiry，不返回 token。
- POST /api/v1/agent/model-auth/chatgpt/start：返回官方 authorization URL；临时 Hosted dynamic-registration 测试模式还会返回 `manual_callback=true`。
- POST /api/v1/agent/model-auth/chatgpt/complete：仅供上述临时 Hosted 测试模式使用；Workspace owner 粘贴完整 localhost callback URL，由服务端校验 loopback URL、Workspace-scoped state 并完成 PKCE token exchange。
- POST /api/v1/agent/model-auth/grok/start：返回 xAI verification URL / user code / flow ID。
- POST /api/v1/agent/model-auth/grok/poll：完成 device-code 连接。
- DELETE /api/v1/agent/model-auth/{provider}：断开账号。
- GET /api/v1/agent/models?provider=chatgpt|grok：读取当前账号模型目录。

ChatGPT OAuth callback 为 GET /auth/model/chatgpt/callback，完成后重定向回 Web。

Provider Test 使用正式 provider transport：OpenAI-compatible 为 /chat/completions，ChatGPT / Grok 为 Responses SSE。

MCP Test 只执行 connect / initialize / ListTools，返回工具数量，**不执行任何 Tool**。

测试响应只返回分类后的状态、消息、耗时与工具数量，不回显 secret 或上游原始响应体。

## Ongoing Schedule

- `GET /api/v1/cards/{cardID}/schedules`
- `POST /api/v1/cards/{cardID}/schedules`
- `PUT /api/v1/cards/{cardID}/schedules/{scheduleID}`
- `DELETE /api/v1/cards/{cardID}/schedules/{scheduleID}`

规则仅支持 daily / weekly / monthly + IANA timezone；missed-run policy 固定为 skip。

已经产生 Trigger 历史的 Schedule 不能硬删除；应改为 disabled，以保留 Trigger / Run 审计链。尚无 Trigger 历史的 Schedule 可以删除。

## 写请求安全

POST / PUT / PATCH 必须使用 `Content-Type: application/json`。
浏览器跨站写请求会被拒绝；再结合 SameSite=Lax Session Cookie，避免普通跨站表单触发状态变化。

## NexusDock First-party Agent Connection

以下接口只允许当前 Workspace owner 调用：

- `GET /api/v1/agent/nexusdock`：读取第一方连接是否可用、是否已连接与 NexusDock 管理页 URL；不返回 token。
- `POST /api/v1/agent/nexusdock/connect`：创建一次 PKCE 授权流程，返回 NexusDock authorization URL。
- `DELETE /api/v1/agent/nexusdock`：撤销远端 OAuth grant 后删除本地连接。
- `GET /auth/nexusdock-device/callback`：要求已有 CycleDock Human Session；校验 state 与 Workspace/Human 绑定后兑换 code，并重定向回 Agent Connections。

该连接不是普通 MCP Connection；终端用户不会提交 NexusDock MCP URL、PAT、Bearer Token 或 `tenant_id`。
