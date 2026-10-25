# 系统架构

## 1. 总体结构

~~~text
React Web
    │ HTTP / SSE
    ▼
CycleDock Go Server
├── HTTP / Auth / MCP Server
├── Product Domain + PostgreSQL
│   └── Project / Card / Turn / Attempt / Schedule / Review
└── Agent Runtime
    └── Google ADK Go v2
        ├── CycleDock SessionService → PostgreSQL
        ├── Context Compaction
        ├── Tool / Artifact
        ├── Model Route Adapter
        └── MCP / NexusDock
~~~

第一版仍保持单 Go 服务 + PostgreSQL。没有真实负载证明以前，不引入 Redis、Kafka 或独立 worker 集群。

## 2. 核心领域

~~~text
Workspace
└── Project
    └── Card = persistent Agent Session
        ├── Turn
        │   └── Attempt
        ├── Session Event
        ├── CardSchedule
        └── Business Activity / Review
~~~

Project 是项目与默认看板数据范围，不建立 Board 表。

Card 是长期业务对象，也是 Agent Session 的产品边界。Card 保存业务状态；Attempt status 不写入 Card。

Turn 是一次新的工作输入，来源可以是 Human、Schedule 或 System。技术 retry / recovery 属于同一 Turn 的新 Attempt，不创建新 Turn。未 Claim 的 Turn 不进入 ADK Session History。

Attempt 是某个 Turn 的一次真实执行，对应一个 ADK invocation。一个 Card 同一时间最多一个 current Attempt。

Session Event 是 Agent Runtime 连续上下文事实源，包括 user input、model response、tool call、tool response、state delta、compaction record、artifact delta、sub-agent branch / isolation metadata。

## 3. Session identity

CycleDock 使用固定映射：

~~~text
ADK app_name   = cycledock
ADK user_id    = workspace_id
ADK session_id = card_id
~~~

Workspace ID 而不是 Human ID 作为 ADK user_id，因为 Card 是 Workspace 协作资源。

所有 Workspace scope 仍从认证上下文获得，绝不信任客户端传入的 ADK user_id。

## 4. PostgreSQL SessionService

不使用 ADK 自带 GORM DatabaseSessionService。

CycleDock 自己实现 session.Service 的 Create / Get / List / Delete / AppendEvent。

原因：

- 全项目统一 pgx / sqlc / Goose。
- 不引入第二套 ORM 与 AutoMigrate。
- 可以在 AppendEvent 中加入 Attempt ownership / stale invocation 防护。
- 可以统一 Workspace / Card 的产品隔离规则。
- 可以对持久 Event / Artifact 做 CycleDock 自己的安全治理。

ADK 官方 conformance suite 作为 SessionService 的兼容性回归基线。

## 5. Turn 调度

Dispatcher 的目标职责：

~~~text
pending Turn
→ lock Card
→ ensure no current Attempt
→ create Attempt
→ invoke ADK Runner with Card Session
~~~

Turn 本身就是队列，不额外创建 queue_items。

enqueue 只写 Turn，当前 Attempt 不受影响。

interrupt 在同一事务中创建新 Turn并终止 current Attempt；commit 后 Dispatcher 优先 Claim 该 interrupt Turn，再创建新的 Attempt / invocation。

## 6. stale event 防护

仅取消 goroutine 不足以保护 Session。旧 Attempt 可能已经发出网络请求并晚返回。

任何 AppendEvent 最终都必须能确认 invocation 属于当前 Card、Attempt 未 terminal、Attempt 仍有写 Session 的资格。

失去资格的 invocation 不得 append Session Event、修改 Session state、修改 Card 业务状态或写当前结果。

## 7. Context Architecture

~~~text
Recent raw Session Events
          +
ADK Compaction summary
          +
on-demand History Retrieval
          +
Artifact references
          +
optional cross-Card Memory
~~~

Compaction 影响 prompt assembly，不删除作为事实源的原始事件。当前生产使用 ADK Tail Retention：约 24k token 触发并保留最近 20 个 raw Event；原始事件继续留在 PostgreSQL，精确旧值通过 Card-scoped history tool 按需检索。

超过 12k 字符的大 MCP 输出在结构化 credential 脱敏后外置到 versioned PostgreSQL Artifact，Session 只保留 preview/ref；模型需要精确旧 payload 时通过 Card-scoped artifact reader 分块读取，而不是重新操作真实设备。

## 8. Discussion handoff

Discussion 生成工作不是 Session fork，也不是共享 Session。

~~~text
Discussion Session
      │
      ├─ extract Handoff Package
      ▼
New Todo / Ongoing Card
= New Session
~~~

Handoff Package 是显式、可审计的跨 Card 知识传递边界。

## 9. Model boundary

ADK 负责 Agent Runtime；CycleDock 继续拥有 ChatGPT SIWC credential、Grok OAuth credential、OpenAI-compatible endpoint、多账号、ordered model routes、failover / streaming-reset policy。

ADK experimental Model Routing 可以复用，但不是 CycleDock 可靠性的唯一实现。

## 10. MCP / NexusDock

普通 outbound MCP 优先迁移到 ADK McpToolset。

NexusDock 保持 first-party Adapter：OAuth grant、token refresh、agentdock_context bootstrap、设备目录、tool filtering、native tool names。

Session 恢复只恢复上下文，不恢复旧网络连接；每次 invocation 按需重新建立 MCP session。

## 11. 依赖方向

~~~text
HTTP / MCP Adapter
        │
        ▼
Product Domain / Store
        │
        ├── PostgreSQL
        │
        └── Agent Runtime Adapter
             └── ADK Runner / SessionService / Toolsets
~~~

业务状态机不能复制到 Web、MCP 和 Agent 三套实现中。

## 12. 当前实施边界

当前分支已经完成 Built-in 与 External Agent 的 Session-first cutover：Human Web / Schedule / OAuth MCP → Turn → Attempt；Built-in Attempt 进入 ADK Runner + PostgreSQL SessionService，External Attempt 通过同一 Store ownership / stale-write guard 执行。Card detail、Web、SSE 与 MCP 执行视图都使用 Turn / Attempt。

Phase F 已删除旧执行 Runtime、Context Builder、旧 query/schema 与对应测试夹具，并把首次发布数据库重整为单一 baseline。当前代码只有一套执行状态机，不存在 Trigger/Run 兼容 Adapter。
