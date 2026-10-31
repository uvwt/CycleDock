# External MCP

## 定位

CycleDock Remote MCP 是外部 AI / Agent 的标准 Adapter。外部客户端通过 Streamable HTTP MCP 访问与 Human Web、Built-in Cycle Agent 相同的 Project / Card / Turn / Attempt Domain。

Card 是长期业务容器，同时对应一个持久 Agent Session；Turn 是新的工作方向；Attempt 是一次真实执行。

MCP Handler 不复制业务状态机，不能绕过：

- Workspace / OAuth Source scope。
- Card 单 current Attempt。
- Attempt runner ownership。
- stale / superseded Attempt 写入保护。
- Human-only Review → Done。
- Turn / Event 幂等与并发约束。

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

当前 MCP Implementation version：`0.4.0`。

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

所有 External MCP Event / Attempt 都使用这个 OAuth Client Source。

Refresh token 每次使用都会轮换 access + refresh。已经轮换过的旧 refresh token 被再次使用时，CycleDock 撤销整个 token family。

## Workspace / Source scope

MCP tool input 不接受 `workspace_id` 或 `source_id`。

真实 scope 来自：

`Bearer access token → OAuth Grant → Workspace + oauth_client Source`

客户端不能通过工具参数切换租户，也不能拿另一个 Source 的 `attempt_id` heartbeat / progress / finding / check-in / finish。

## 当前工具

### 读取

- `list_projects`
- `list_cards`
- `get_card`

`list_cards` 返回每个 Card 的 `current_attempt_status`、`pending_turn_count` 和 Ongoing health。

`get_card` 返回：

- Card。
- bounded durable Timeline。
- `pending_turns`。
- `current_attempt`。
- `attempt_history`。

产品 Timeline 不包含 raw model messages、tool args、tool trace 或 chain-of-thought。

### Card / Context

- `create_discussion`
- `generate_work`
- `update_card`
- `move_card`
- `add_comment`
- `create_turn`

`create_discussion` 创建新的 Discussion。Discussion 是新工作的入口，不要求调用方预先选择 Todo / Ongoing。

`generate_work` 只能以 active Discussion 为来源创建 Todo 或 Ongoing，并写入 `source_discussion_id`。Todo / Ongoing 不能继续生成更多 Card。

`add_comment` 只增加 durable Card context，不创建 Turn。

`create_turn` 原子创建 Comment Event + queued Turn：

- 默认 enqueue。
- 不 interrupt current Attempt。
- 不自动 Claim。
- 使用 OAuth Source + `idempotency_key` 保证网络重试安全。
- Turn 的 `source_event_id` 稳定指向对应 Comment Event。

External MCP 不提供 interrupt tool；立即转向仍是 Human Web 的显式人工决策。

### 定时计划工具

- list_schedules(card_id)：读取 Ongoing Card 的周期规则、启用状态与下一次触发时间。
- create_schedule(card_id, rule)：创建启用的每日、每周或每月周期规则。
- update_schedule(card_id, schedule_id, rule, enabled)：完整替换规则，或将 enabled 设置为 false 暂停。
- delete_schedule(card_id, schedule_id)：只有未产生 Schedule Turn 历史的规则可删除；已有历史应暂停。

rule 包含 frequency（daily/weekly/monthly）、hour、minute、timezone（IANA 名称，如 Asia/Shanghai）。weekly 另需 day_of_week（周日=0、周六=6），monthly 另需 day_of_month（1~28）。不支持 Cron 或一次性定时。

修改前先 list_schedules；更新须携带完整规则和 enabled，避免意外改变其他字段。创建没有重试幂等键；调用结果不明时先列出日程再决定是否重试。所有读写都使用 OAuth Workspace scope。

### Attempt

- `claim_attempt`
- `heartbeat_attempt`
- `reply_message`
- `check_in`
- `finish_attempt`

`claim_attempt(card_id)` 在目标 Card 行锁中原子执行：

1. 若 Card 已有同一 OAuth Source 拥有的 queued / running / waiting_human Attempt，则复用同一 Attempt。
2. 若 current Attempt 由另一个 Source 持有，返回 conflict。
3. 否则只在该 Card 内选择最早 pending Turn。
4. 创建 running Attempt，并记录当前 OAuth Source 为 `runner_source_id`。

因此网络重试不会静默领取下一条 Turn，也不会因为 Workspace FIFO 抢到别的 Card。

`heartbeat_attempt` 只延长 running Attempt lease，不写 Timeline。

`reply_message` 每调用一次生成独立用户消息气泡；`end_turn=true` 的文字/附件结束 Attempt，选择卡会原子进入 `waiting_human`，详情见 `docs/reply-message.md`。

`check_in` 仍记录 Ongoing 结构化健康事实；`confirm_card` 仍负责 Todo 审核。原始工具调用和 chain-of-thought 不写入产品 Timeline。

`finish_attempt` 支持：

- `completed`：只将 Attempt 置为 completed，不接收结果正文、不生成气泡；用户回复应使用 `reply_message`。
- `failed`：保存 failure summary，并将 Attempt 置为 failed。
- `waiting_human`：保存 stage / summary，释放 lease，等待 Human reply。

同一 Source 重试 completed 状态结算，或重试相同 failed / waiting_human 信息，均为幂等操作。

superseded / expired / cancelled Attempt，或另一个 Source 拥有的 Attempt，后续写入都会返回 `ATTEMPT_NOT_CURRENT`。

### Human reply 与 Schedule

Human Web reply 会：

1. supersede 原 waiting_human Attempt；
2. 在同一个 Card 创建新的 Human Turn；
3. 保持 Card Session 的连续上下文。

External Agent 随后可以再次 `claim_attempt(card_id)`，领取这个 reply Turn。

Ongoing Schedule 只创建 Schedule Turn，不直接运行模型。External Agent 同样通过 `claim_attempt(card_id)` 领取该 Turn。

### Review

- `confirm_card`

Agent 对 Todo 使用 `move_card` 只能进入 `todo / working`。Agent 不允许直接移动到 Review，必须通过 `confirm_card` 留下确认事实。

达到 `required_confirmations` 后，CycleDock 自动将 Working → Review。Review → Done 始终 Human-only。

## 幂等

以下写操作使用显式 `idempotency_key`：

- `create_discussion`。
- `generate_work`。
- `add_comment`。
- `create_turn`。
- 每条 `reply_message` 的 Message Event。
- `check_in`。
- `confirm_card`。

Attempt lifecycle 本身按 Attempt ID + owner + terminal value 提供幂等：

- 重复 `claim_attempt`：同 owner 复用 current Attempt。
- 重复 `finish_attempt(completed)`：复用 completed 终态，不写 Timeline Event。
- 重复 `finish_attempt(failed)`：相同 summary 复用终态。
- 重复 `finish_attempt(waiting_human)`：相同 stage / summary 复用 waiting 状态。

## 不兼容旧协议

CycleDock 尚未首次公开发布，因此 v0.4 不保留旧 External MCP Runtime 兼容别名。

以下工具已删除：

- `create_followup`
- `claim_work`
- `heartbeat_run`
- `finish_run`

对应 `trigger_id / run_id / current_run / pending Trigger` 字段也不再属于 External MCP API。

## 官方 Skill

正式 Skill 单一事实源：

- `skills/cycledock/SKILL.md`
- `skills/cycledock/references/tools.md`

运行时公开：

- `/skills/cycledock/SKILL.md`
- `/skills/cycledock.zip`

ZIP 由同一份嵌入源动态生成，不维护第二份 Skill 内容。

## 验证基线

External MCP cutover 至少验证：

- OAuth PKCE / code 单次消费。
- access / refresh digest storage。
- refresh rotation / replay family revoke。
- 并发 Workspace approval 只生成一个 oauth_client Source。
- 官方 MCP SDK Streamable HTTP 完整连接与 tool discovery。
- 旧 Trigger / Run 工具名不再暴露。
- 两个 OAuth Source 并发 Claim 同 Card 只有一个成功。
- 非 owner heartbeat / progress / finish 被拒。
- 同 owner claim / finish / waiting_human 网络重试幂等。
- Human reply Turn 与 Schedule Turn 可被 External Agent Claim。
- Workspace / Card tenant isolation。
