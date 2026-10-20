# 系统架构

## 1. 总体结构

```text
                    ┌──────────────────────┐
                    │ React Web            │
                    └──────────┬───────────┘
                               │ HTTP
                               ▼
┌──────────────────────────────────────────────────┐
│ CycleDock Go Server                              │
│                                                  │
│ HTTP API   MCP Server   Auth/OAuth/OIDC          │
│      \         |          /                     │
│       \        |         /                      │
│        ───── Domain + Store ─────                 │
│                 ▲          │                      │
│                 │          ▼                      │
│          Built-in Agent   PostgreSQL              │
│                 │                                 │
│        Model Client + MCP Client                  │
└──────────────────────────────────────────────────┘
                               ▲
                               │ Remote MCP
                     External AI / AgentDock
```

第一阶段保持单体 Go 服务 + PostgreSQL。不要提前引入 Redis、Kafka、独立 worker 集群或微服务。

## 2. 核心领域

```text
Workspace
└── Project
    └── Card
        ├── Event
        ├── Trigger
        │   └── AgentRun
        └── CardSchedule
```

### Project

Project 是项目与默认看板的统一领域对象。

不存在 Board 表。前端 Kanban 只是 Project Cards 的一种 View。

### Card

Card 是长期业务对象，保存任务标题、描述和业务状态。

Card 不保存：

- 当前 Agent。
- claimed 布尔值。
- Agent status。
- 当前执行百分比。

这些信息属于 AgentRun。

### Event

Event 是用户长期可见的工作事实，例如：

- comment
- progress
- finding
- result
- checkin
- confirm
- reject
- status_change

系统不是完整 Event Sourcing。Card / Run 仍直接保存当前状态，Event 用于记录重要事实与历史。

### Trigger

Trigger 表示一次不可变的新工作方向。

来源：

- human_comment
- scheduled_followup
- manual_retry
- system_followup

同一个 Card 可以在生命周期中产生任意多个 Trigger。

Trigger 没有 AgentRun 时，天然处于待执行队列。

### AgentRun

AgentRun 表示某个 Agent 对某个 Trigger 的一次具体执行。

创建 AgentRun = Claim Trigger。

不建立独立 Claims 表。

## 3. 依赖方向

内置 Agent 与外部 MCP Agent 最终必须进入同一套领域状态机：

```text
Web Handler ─┐
MCP Server ──┼──> Domain / Store ──> PostgreSQL
Agent Runner ┘
```

禁止形成：

```text
Web 一套状态机
MCP 一套状态机
内置 Agent 又一套状态机
```

内置 Agent 不调用 CycleDock 自己的 MCP；它直接调用内部领域/存储能力。MCP 是给外部客户端的 Adapter。

## 4. 一次人类评论的事务边界

### 无 current Run / 直接执行

单事务：

1. 写入 comment Event。
2. 写入 human_comment Trigger。
3. 如果内置 Agent 可执行，创建 queued AgentRun。
4. commit。
5. commit 后通知本地 dispatcher。

### 加入队列

单事务：

1. 写入 comment Event。
2. 写入 human_comment Trigger，dispatch_mode=enqueue。
3. 不修改 current Run。
4. 不创建新 Run。
5. commit。

### 立即转向

单事务：

1. 写入 comment Event。
2. 写入 human_comment Trigger，dispatch_mode=interrupt。
3. 将当前 queued/running/waiting_human Run 变为 superseded。
4. 创建新 queued Run。
5. commit。

不能用“先查询 current Run，再分两次写”的方式实现；并发安全由事务 + 数据库约束共同保证。

## 5. current Run 规则

一个 Card 同一时间最多存在一个：

- queued
- running
- waiting_human

的 Run。

用 PostgreSQL partial unique index 强制。

terminal Run 包括：

- completed
- failed
- superseded
- expired
- cancelled

以后如出现明确业务需要，可以增加 skipped。

## 6. stale result 防护

Run 被 superseded 后可能仍有外部调用晚到。

因此任何由 Run 发起、会改变 Card 当前业务状态或写入“当前结果”的操作，都必须验证：

- Run 仍属于该 Card。
- Run 未 terminal。
- Run 仍是当前 Run。
- Trigger / runner source 与请求身份匹配。

旧 Run 的迟到结果不能覆盖新方向。

## 7. Dispatcher

Dispatcher 只负责把“可执行 Trigger”转换成 AgentRun。

一个 current Run 结束后：

1. 锁定 Card。
2. 检查是否仍有 current Run。
3. 按 Trigger sequence 找最早的“没有 AgentRun”的 Trigger。
4. 创建 queued AgentRun。
5. commit 后交给 Runner。

Trigger 自身就是队列，不额外建立 task_queue / queue_items 表。

## 8. Heartbeat 与 lease

Heartbeat 只更新：

- heartbeat_at
- lease_expires_at

Heartbeat 不写 Timeline。

lease 超时：

- running / queued Run 可进入 expired。
- 原 Trigger 已被消费，不重新 Claim。
- 如需重试，创建 manual_retry / system_followup Trigger + 新 Run。

waiting_human 不继续 heartbeat，也不应该被 Scheduler 自动抢占。

## 9. 扩展原则

优先扩展稳定概念，而不是增加平行系统。

例如：

- 多种看板展示 → View，而不是 Board。
- Agent 重试 → 新 Trigger / Run，而不是复活旧 Run。
- 定时执行 → Schedule 生成 Trigger，而不是另一套 Job Runtime。
- 外部 Agent → MCP Adapter，而不是新的 Card 状态机。

## 10. Hosted 第一方 NexusDock 设备连接

CycleDock Hosted 把 NexusDock 远程设备作为 **First-party Agent Connection**，而不是普通 `workspace_mcp_connections`。

边界如下：

```text
CycleDock OIDC login
  -> 只建立 NexusDock account identity (`issuer + sub`)

用户显式连接 NexusDock 远程设备
  -> NexusDock MCP OAuth consent
  -> Workspace-scoped MCP grant
  -> CycleDock 加密保存 grant token

Built-in Agent Run
  -> CycleDock runtime 读取并刷新第一方 grant
  -> 自动连接 NexusDock `/mcp`
  -> 内部 bootstrap `agentdock_context`
  -> 只把精简设备目录加入 Run context
  -> 节点工具以原生 tool name 提供给模型
```

约束：

- OIDC token 永远不能直接作为 NexusDock MCP/device credential。
- NexusDock 授权不写入 `workspace_mcp_connections`，也不要求用户填写 URL、PAT 或 Token 环境变量。
- 首次授权必须经过 NexusDock 自己的 consent；CycleDock 不复制 NexusDock 的设备权限模型。
- `agentdock_context` 是第一方 runtime bootstrap 细节，正常不暴露给模型。设备存在性、`node_id`、在线状态与主要能力属于启动上下文；具体 Workspace/Skill/MCP 内容仍按需读取。
- CycleDock 不复制 NexusDock 的设备管理页；“在 NexusDock 中管理设备”跳转到 NexusDock Web。
- 当前传输继续复用 NexusDock 标准 MCP Gateway 与既有 AgentDock WebSocket。只有真实测量证明协议层成为瓶颈时才考虑额外 RPC。
