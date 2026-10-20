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
- `POST /api/v1/projects/{projectID}/cards`

Project 即 Board，不存在独立 Board API。

## Card

- `GET /api/v1/cards/{cardID}`：返回 Card、current Run、Timeline、pending Trigger queue、最近终态 work history、Schedule、Health / Last Check-in。
- `PATCH /api/v1/cards/{cardID}`
- `POST /api/v1/cards/{cardID}/move`
- `POST /api/v1/cards/{cardID}/ongoing-state`

Normal Card 的 Review → Done 仍由 Store 的 Human-only 规则保证，HTTP Handler 不复制状态机。

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

Provider Test 使用当前 endpoint / model / API key env 做最小 `/chat/completions` 请求。

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
