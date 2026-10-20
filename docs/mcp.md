# External MCP

## 定位

CycleDock Remote MCP 是外部 AI / Agent 的 Adapter。外部客户端通过标准 Streamable HTTP MCP 访问与 Human Web、未来 Built-in Cycle Agent 相同的 Domain / Store。

MCP Handler 不复制业务状态机，不能绕过：

- Workspace scope。
- Trigger 单 Claim。
- Card 单 current Run。
- runner ownership。
- supersede stale-write 防护。
- Human-only Review → Done。
- 幂等与并发约束。

## 接入地址

配置 `CYCLEDOCK_PUBLIC_URL` 后启用：

- MCP Resource：`{PUBLIC_URL}/mcp`
- Protected Resource Metadata：`{PUBLIC_URL}/.well-known/oauth-protected-resource/mcp`
- Authorization Server Metadata：`{PUBLIC_URL}/.well-known/oauth-authorization-server`
- DCR：`{PUBLIC_URL}/oauth/register`
- Authorization：`{PUBLIC_URL}/oauth/authorize`
- Token：`{PUBLIC_URL}/oauth/token`
- AI Connect：`{PUBLIC_URL}/docs/ai-connect/`
- Skill：`{PUBLIC_URL}/skills/cycledock/SKILL.md`

远程 Public URL 必须 HTTPS；localhost / loopback 开发环境允许 HTTP。

## OAuth

Remote MCP 使用：

- Protected Resource Metadata。
- Authorization Server Metadata。
- Dynamic Client Registration。
- Authorization Code。
- S256 PKCE。
- `resource` 参数绑定 MCP audience。
- public client（`token_endpoint_auth_method=none`）。
- opaque access token。
- opaque refresh token。
- refresh rotation。

数据库只保存 Authorization Code、Access Token、Refresh Token 的 SHA-256 digest，不保存 raw secret。

Human 必须先通过 Human Web 身份认证，再在 OAuth consent 页面批准当前 Workspace。批准后：

`Human Membership → OAuth Grant → oauth_client Source`

所有 MCP Event / Run 都使用这个 OAuth Client Source。

Refresh token 每次使用都会轮换 access + refresh。已经轮换过的旧 refresh token 被再次使用时，CycleDock 撤销整个 token family，防止合法客户端与泄漏者继续竞速。

## Workspace / Source scope

MCP tool input 中没有 `workspace_id` 或 `source_id`。

实际 scope 来自：

`Bearer access token → OAuth Grant → Workspace + oauth_client Source`

因此客户端不能通过工具参数切换租户，也不能用另一个 Source 的 `run_id` heartbeat / progress / finding / check-in / finish。

## 当前工具

### 读取

- `list_projects`
- `list_cards`
- `get_card`

`get_card` 返回 Card、最近 durable Timeline、pending Trigger queue、current Run；不会返回 raw model messages / tool trace。

### Card / Context

- `create_card`
- `update_card`
- `move_card`
- `add_comment`
- `create_followup`

`add_comment` 只增加长期上下文，不创建 Trigger。

`create_followup` 显式创建新的 Comment Event + pending `system_followup` Trigger；默认 enqueue，不 interrupt，也不自动 Claim。

External MCP 第一版不提供 interrupt tool。立即转向是 Human Web 的显式人工决策。

### Run

- `claim_work`
- `heartbeat_run`
- `report_progress`
- `add_finding`
- `check_in`
- `finish_run`

`claim_work` 在同一个 Card 行锁中原子执行：

1. 检查 current Run。
2. 若当前 Run 已由同一 Source 持有，则作为网络重试返回同一 Run。
3. 若由其他 Source 持有，返回 conflict。
4. 否则选择最早 pending Trigger。
5. 直接创建 running Run。

因此网络重试不会静默领取下一条 Trigger。

Heartbeat 只续 lease，不写 Timeline。

Progress 可更新 `current_stage` / `progress_summary` 并续 lease；只有 `event_content` 非空时才生成 durable progress Event。

Finding / Result / Check-in 是长期上下文。Raw tool output、临时日志、tool args、chain-of-thought 不应写入 CycleDock。

`finish_run` 支持：

- `completed`：写 durable Result。
- `failed`：写 failure summary。
- `waiting_human`：记录 stage / summary 并等待 Human。

superseded / expired / cancelled Run，或另一个 Source 拥有的 Run，后续写入都会被拒绝。

### Review

- `confirm_card`

Agent 不允许直接 `move_card → review`，必须通过 `confirm_card` 留下确认事实。

达到 `required_confirmations` 后，CycleDock 自动将 Working → Review。Review → Done 始终 Human-only。

## 幂等

以下写操作使用 `idempotency_key`：

- create_card。
- add_comment。
- create_followup。
- meaningful progress Event。
- add_finding。
- check_in。
- completed / failed finish_run。
- confirm_card。

同一逻辑操作网络重试必须复用同一个 key；同一个 key 配不同请求内容会返回 idempotency conflict。

## Ongoing

CycleDock 原生 Ongoing Schedule 支持 daily / weekly / monthly + IANA timezone。

Schedule 只创建 `scheduled_followup` Trigger，不直接执行模型。外部 AI 客户端如果同时使用自己的定时任务，应先通过 `get_card` / `list_cards` 检查 pending queue，避免重复制造相同工作。

## 官方 Skill

正式 Skill 单一事实源：

- `skills/cycledock/SKILL.md`
- `skills/cycledock/references/tools.md`

运行时公开：

- `/skills/cycledock/SKILL.md`
- `/skills/cycledock.zip`

ZIP 由同一份嵌入源动态生成，不维护第二份 Skill 内容。

## 验证基线

Phase 3 必须同时验证：

- OAuth PKCE / code 单次消费。
- access / refresh digest storage。
- refresh rotation / replay family revoke。
- 并发 Workspace approval 只生成一个 oauth_client Source。
- 两 Agent 并发 Claim 只有一个成功。
- bearer Source 不能写另一个 Source 的 Run。
- superseded Run late finish 被拒绝。
- Confirm threshold 与 Human-only Done。
- 官方 Go MCP SDK 通过真实 HTTP 完成 discovery / DCR / OAuth / Streamable HTTP / tool discovery / tool calls。
