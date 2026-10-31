# 数据模型

本文描述 Session-first 目标模型。首次公开发布前不兼容旧 CycleDock DEV 数据，最终会重整 000001_initial.sql。当前开发分支允许用后续 migration 验证新模型，再在 cutover 后收拢。

## 1. 总体关系

~~~text
workspaces
  ├── workspace_memberships
  ├── sources
  └── projects
       └── cards
            ├── card_turns
            │    └── turn_attempts
            ├── card_schedules
            ├── card_activity
            └── agent session
                 ├── agent_session_events
                 └── artifacts
~~~

ADK Session 的产品身份不是额外领域实体：

~~~text
app_name   = cycledock
user_id    = workspace_id
session_id = card_id
~~~

## 2. workspaces / projects / cards

Workspace / Project 语义保持不变。

Card 仍保存三类业务状态：

- discussion: active / archived
- todo: todo / working / review / done
- ongoing: active / paused / archived

Card 是长期工作对象；Attempt status 不写入 Card。

Discussion 派生 Todo / Ongoing 时新 Card 保存 source_discussion_id，并通过显式 Handoff Package 传递必要上下文。

## 3. card_turns

目标字段：

~~~text
id uuid
sequence bigint identity
workspace_id uuid
card_id uuid
source human | schedule | system
created_by_source_id nullable
content text
dispatch_mode enqueue | interrupt
schedule_id nullable
scheduled_for nullable
idempotency_key nullable
request_hash nullable
cancelled_at nullable
created_at
~~~

约束：

- Turn 是不可变输入方向。
- Human Turn 由消息产生。
- Schedule occurrence 唯一。
- enqueue Turn 可以 pending。
- pending Turn 不提前进入 ADK Session。
- 技术 retry / recovery 不创建新 Turn；它们在 turn_attempts 中形成同一 Turn 的后继 Attempt。

人工 Turn 可保存 `device_node_id` / `device_name`，表示用户发送该输入时选定的外部设备偏好；空字符串表示未指定。该字段不授予设备权限，不影响 Workspace scope，技术重试复用原 Turn 的值。人工 Comment 正文保持原样，设备提示在实际 Claim 后才进入 ADK 用户上下文。

## 4. turn_attempts

目标字段：

~~~text
id uuid
workspace_id uuid
card_id uuid
turn_id uuid
attempt_number integer
reason initial | retry | recovery
retry_of_attempt_id uuid nullable
invocation_id text nullable
runner_source_id uuid
model_route_id nullable
status queued | running | waiting_human |
       completed | failed | superseded | expired | cancelled
current_stage text
error_summary text
started_at nullable
heartbeat_at nullable
lease_expires_at nullable
finished_at nullable
created_at
updated_at
~~~

一个 Turn 可以有多个 Attempt；技术 retry 创建新 Attempt。

一个 Card 同时最多一个 queued / running / waiting_human Attempt。

invocation_id 必须能用于 Session Event stale-write 校验。

## 5. agent_sessions

ADK session.Service 的持久化对象：

~~~text
app_name text
user_id text
id text
state jsonb
created_at
updated_at

PRIMARY KEY(app_name, user_id, id)
~~~

CycleDock 正常业务中 app_name=cycledock、user_id=Workspace UUID string、id=Card UUID string。

Session state 只保存可序列化、确实属于 Agent runtime 的动态数据。业务状态仍在 Card / Turn / Attempt 表中。

## 6. agent_session_events

当前基础字段：

~~~text
sequence bigint identity
app_name text
user_id text
session_id text
id text
invocation_id text
occurred_at timestamptz
event jsonb
~~~

event 保存 ADK Session Event 的可恢复表示，包括 LLM content、function call、function response、EventActions、state delta、artifact delta、compaction metadata、branch / isolation scope 与 long-running tool metadata。

约束：

- partial streaming chunk 不持久化。
- temp state delta 不持久化。
- Event 缺少 ID 时 storage 必须分配。
- compaction record 必须完整 round-trip。
- 不保存 chain-of-thought / secret。
- terminal / stale Attempt 的迟到 Event 后续必须在写入层拒绝。

当前实现已满足 ADK session.Service conformance，并已接入 Attempt / invocation ownership guard；running Attempt 首个 Event 绑定 invocation_id，waiting_human 仅允许已绑定的同 invocation 尾部 Event，terminal / superseded Attempt 的迟到写入会被拒绝。

## 7. scoped ADK state

为满足 ADK state scope：

~~~text
agent_app_state
agent_user_state
agent_sessions.state
~~~

前缀语义：

- app: app scope。
- user: Workspace scope，因为 ADK user_id = workspace_id。
- 无前缀：Card Session scope。
- temp: 单 invocation 临时状态，不持久化。

## 8. agent_artifacts

Phase D 已接入 ADK `artifact.Service` 的 PostgreSQL 实现。`agent_artifacts` 以 `(app_name, user_id, session_id, file_name, version)` 为主键，并保存 `genai.Part` JSON、MIME、metadata 与创建时间；外键绑定 `agent_sessions`，因此 Artifact scope 与 Card Session 一致。

自动版本分配使用 PostgreSQL advisory transaction lock，保证同一 logical file 的并发保存得到唯一递增版本。

当前大型 MCP 结果阈值为 12k 字符：完整脱敏 payload 存 Artifact，Session Event 只保留最多约 4k preview、artifact ref/version/length。Artifact reader 的 Workspace/Card identity 从当前 Session 推导，调用方不能传入其他租户 identity。

## 9. card_activity

产品 Timeline 与 ADK Session Event 分开。

card_activity 只记录 Human 可理解的业务事实，例如 card status change、review、schedule change、archive/reopen、handoff created。

它不复制完整 model/tool trace。

## 10. schedules

Ongoing Schedule 继续属于 Card。

~~~text
Schedule occurrence
→ card_turns(source=schedule)
→ Dispatcher
→ Attempt
→ same Card Session
~~~

Scheduler 不直接调用 Agent。

## 11. credentials / model routes / MCP

模型账号、Route、MCP、NexusDock OAuth 的 Workspace scope 继续独立存储。

Session event 绝不能把 access token、refresh token、Cookie、API key、bearer token 作为普通上下文持久化。

## 12. 并发不变量

数据库与事务必须共同保证：

- 一个 Card 最多一个 current Attempt。
- pending Turn FIFO。
- Schedule occurrence 去重。
- interrupt 与旧 Attempt finish 并发时旧 Attempt 不能回写。
- Session AppendEvent 按 Session 串行化。
- stale invocation 不得写 Session。
- Workspace / Card tenant scope 不可跨越。

## 13. 当前迁移状态

首次发布 baseline 已完成重整：数据库只保留当前 Session-first 模型，正式 migration 只有 `000001_initial.sql`。

- `card_turns / turn_attempts` 是唯一执行调度模型。
- `agent_sessions / agent_session_events / agent_artifacts` 保存持久 Agent Session、原始 Event 与大结果 Artifact。
- `events` 只保存产品 Timeline 事实；旧 Run 外键已删除。Attempt 产生的 Message/Check-in 等事件使用 nullable `attempt_id` 关联当前 Attempt，UI 可由 `attempt_id → turn_attempts.turn_id → card_turns.source_event_id` 稳定组织因果轮次，同时 `sequence` 始终保留真实审计顺序。
- 不保留旧 DEV Runtime 数据迁移或兼容表。
- final baseline 已验证 fresh `up → down 0 → up`、自定义 PostgreSQL `search_path` 与稳定 `sqlc generate`。
