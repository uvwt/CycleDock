# 数据模型

本文定义第一版数据库的领域基线。真正建表时允许根据 PostgreSQL / sqlc 细节调整字段名，但不能改变核心语义而不更新本文。

## 1. 总体关系

```text
human_accounts
  └── oidc_identities

workspaces
  ├── workspace_memberships
  ├── sources
  └── projects
       └── cards
            ├── events
            ├── triggers
            │    └── agent_runs
            └── card_schedules
```

另有 Web Session 与 MCP OAuth 基础设施表。

## 2. 身份与 Workspace

### workspaces

建议字段：

- id
- name
- created_at
- updated_at

第一版 Tenant 边界即 Workspace。

### human_accounts

表示 CycleDock 内部的人类主体。

建议字段：

- id
- display_name
- email_snapshot（仅展示用途，不能作为稳定主键）
- created_at
- updated_at

### oidc_identities

Hosted 使用稳定外部身份：

`issuer + subject`

建议唯一约束：

`UNIQUE (issuer, subject)`

### workspace_memberships

把 Human Account 加入 Workspace。

第一版只需要 owner/member 等非常克制的角色；不要提前做复杂 RBAC。

### sources

统一表示动作来源。

kind 第一版：

- human
- cycle_agent
- oauth_client

Event actor 与 AgentRun runner 都引用 Source。

## 3. projects

Project 本身就是项目看板。

建议字段：

- id
- workspace_id
- name
- description
- instructions
- created_at
- updated_at

不要增加 board_id。

未来多种展示方式使用 View 体系，不改变 Card 归属。

## 4. cards

建议字段：

- id
- workspace_id
- project_id
- kind: normal / ongoing
- title
- description
- normal_column
- ongoing_state
- required_confirmations
- created_by_source_id
- created_at
- updated_at

约束：

- normal Card 必须有 normal_column，不能有 ongoing_state。
- ongoing Card 必须有 ongoing_state，不能有 normal_column。
- normal_column 限定 Discussion / Todo / Working / Review / Done。
- ongoing_state 限定 Active / Paused / Archived。
- workspace_id 与 project_id 必须 tenant-consistent。

Card 不保存 agent_status、claimed、progress、current_agent。

## 5. events

Event 是 Card Timeline 的长期工作事实。

建议字段：

- id
- sequence
- workspace_id
- card_id
- event_type
- actor_source_id
- run_id nullable
- content
- checkin_status nullable
- created_at

event_type 第一版：

- comment
- progress
- finding
- result
- checkin
- confirm
- reject
- status_change

原则：

- comment 是人类/Agent 可读交流。
- progress 是有意义的阶段变化，不是 heartbeat。
- finding 是从执行中提炼出来、值得后续继承的事实。
- result 是本轮工作结果摘要。
- checkin 用于 Ongoing 健康结果。
- 不存 chain-of-thought。
- 不存 raw tool args/result。

人类写 comment 的 HTTP/MCP 请求应支持幂等键，避免网络重试重复创建 Event。

## 6. triggers

Trigger 是不可变工作方向，也是队列项。

建议字段：

- id
- sequence
- workspace_id
- card_id
- cause
- cause_event_id nullable
- created_by_source_id nullable
- dispatch_mode
- schedule_id nullable
- scheduled_for nullable
- idempotency_key nullable
- request_hash nullable
- created_at

cause：

- human_comment
- scheduled_followup
- manual_retry
- system_followup

dispatch_mode：

- enqueue
- interrupt

对于没有 current Run 的“发送并执行”，可以记录 enqueue 语义后立即创建 Run；dispatch_mode 只表达创建时用户/系统的调度意图，不作为 mutable status。

关键约束：

- human_comment Trigger 必须引用对应 comment Event。
- 同一个 comment Event 最多产生一个 Trigger：`UNIQUE (cause_event_id)`（对非空值）。
- schedule 触发应通过 `schedule_id + scheduled_for` 唯一约束避免重复。
- Trigger 一旦创建，不修改其原始意图。

“待执行”不需要 status：

> 没有任何 AgentRun 的 Trigger = pending queue item。

## 7. agent_runs

AgentRun 表示一次具体 Claim / 执行。

建议字段：

- id
- workspace_id
- card_id
- trigger_id
- runner_source_id
- status
- current_stage
- progress_summary
- result_summary
- error_summary
- started_at nullable
- heartbeat_at nullable
- lease_expires_at nullable
- finished_at nullable
- created_at
- updated_at

status：

- queued
- running
- waiting_human
- completed
- failed
- superseded
- expired
- cancelled

关键约束：

### 一个 Trigger 只能被 Claim 一次

`UNIQUE (trigger_id)`

### 一个 Card 最多一个 current Run

PostgreSQL partial unique index：

```sql
UNIQUE (card_id)
WHERE status IN ('queued', 'running', 'waiting_human')
```

实际建表时还应确保 workspace/card/trigger/source 的 tenant scope 一致，必要时使用 composite FK / unique key。

### 时间约束

- queued 可以没有 started_at。
- running 应有 started_at / heartbeat / lease。
- waiting_human 不要求持续 heartbeat。
- terminal Run 应有 finished_at。

具体 CHECK 在 migration 实现时落地。

## 8. card_schedules

第一版仅 Ongoing Card 可配置。

建议字段：

- id
- workspace_id
- card_id
- enabled
- frequency: daily / weekly / monthly
- day_of_week nullable
- day_of_month nullable
- time_of_day
- timezone
- next_trigger_at
- last_triggered_at nullable
- created_at
- updated_at

约束：

- 只能绑定 ongoing Card。
- monthly 的 day_of_month 必须有确定规则；第一版建议只允许 1-28，避免月末歧义，后续若需要“月末”再显式增加语义。
- weekly 需要 day_of_week。
- daily 不需要 day 字段。
- timezone 使用 IANA timezone。

第一版 missed-run policy 固定为 skip，不需要字段。

## 9. Web Session

Hosted / Self-hosted Human Web 都使用服务端 Session。

浏览器只持有随机 opaque Cookie；数据库保存 token digest，不保存明文。

Session 必须绑定：

- workspace
- human source
- expires_at

HTTPS Hosted 使用 Secure + HttpOnly + SameSite=Lax。

## 10. MCP OAuth

保留标准 OAuth 基础设施：

- oauth_clients
- oauth_grants
- oauth_authorization_codes
- oauth_tokens

继续遵守：

- Authorization Code + S256 PKCE。
- opaque code/token。
- 数据库保存 SHA-256 digest。
- Refresh Token rotation。
- grant 绑定 workspace + human approval + oauth client source。

## 11. Agent 配置

Model Provider / MCP Connection 不强行塞进 initial core schema。

在 Built-in Agent Phase 再增加：

- model_providers
- mcp_connections
- workspace_agent_settings

Secret 与普通展示配置分离，不能通过普通 Settings API 回显。

## 12. 初始 migration

新项目没有兼容旧 CycleDock DEV 数据的要求。

开发初期可以使用：

`db/migrations/000001_initial.sql`

在首次公开发布前仍可根据实际实现重整 initial migration，确保新安装从空 PostgreSQL 一次得到干净 schema。
