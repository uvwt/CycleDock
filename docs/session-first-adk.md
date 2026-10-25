# CycleDock Session-first / ADK 架构

> 状态：已实现的架构基线。首次公开发布使用统一 Card → Turn → Attempt Runtime，不迁移旧 DEV Runtime 数据。

## 1. 决策

CycleDock 继续是人与 AI Agent 共用的项目工作台，不变成通用 Chat 产品；但 Agent Runtime 改为 **Session-first**：

- 一个 Card 对应一个持久 Agent Session。
- Card ID 直接作为 ADK Session ID，不增加一对一的 CardSession 领域实体。
- 用户评论、Schedule、系统继续执行形成 Turn。
- 一个 Turn 可以有多个 Attempt；Attempt 是一次真实 Agent invocation。
- ADK Session Event 保存连续会话历史，包括用户输入、模型回复、tool call、tool response、state delta、compaction 等。
- 旧设计“每个 Run 独立上下文、raw tool result 不跨 Run”的规则废弃。
- CycleDock 业务状态、队列、并发、权限、Schedule、Review、Model Route、NexusDock 授权仍由 CycleDock 管理。
- Google ADK Go v2 负责 Agent loop、Session/Event、Context Compaction、Artifact 接口和未来 multi-agent 能力。
- PostgreSQL 仍是唯一正式数据库；ADK SessionService 由 CycleDock 用 pgx/sqlc/Goose 实现，不引入 GORM/AutoMigrate。

核心关系：

```text
Workspace
└── Project
    └── Card = persistent Agent Session
        ├── Turn
        │   └── Attempt
        ├── ADK Session Events
        ├── Artifact
        ├── Schedule
        └── Business State
```

## 2. 为什么重构

旧 Runtime 把 raw model/tool context 限制在单个 Run 内。用户在同一个 Card 追问时，新 Run 只能看到 Comment / Finding / Result 等提炼后的 durable facts，看不到上一轮工具调用的精确结果。

这会造成：

- “刚才第 8 个容器是什么镜像”无法直接回答；
- Agent 重复执行 docker ps / journalctl / 文件读取等只读工具；
- 指代词（“它”“刚才那个”“上一条错误”）跨 Run 失效；
- Context Builder / Context Budget / Finding 提炼承担了本应由 Session Runtime 承担的职责。

Session-first 的目标不是永久把所有 raw output 塞给模型，而是把“持久历史”和“当前模型上下文”分开。

## 3. 上下文分层

### 3.1 Recent Session Events

最近的用户消息、模型回复、tool call、tool response 保持原始事件形态，直接参与模型上下文。

它解决正常连续追问与指代解析。

### 3.2 Context Compaction

Session 变长后由 ADK Tail Retention 压缩较老事件。当前生产默认在约 24k prompt tokens 时触发，并保留最近 20 个 raw Event；这提供真正的上下文增长上限，而不是只按固定窗口做比例缩减。

Compaction 使用与当前 Agent 相同的 ordered model routes，但内部 summary 不进入产品 SSE。压缩只改变“下一次发给模型什么”；原始 PostgreSQL Session Event 不被物理删除，compaction record 作为 Session EventActions 持久化。

### 3.3 Session History Retrieval

被压缩掉的旧事件仍可按需读取。内置 `cycledock_search_session_history` 只在当前 Workspace/Card Session 的原始持久 Event 中检索，返回受限数量的精确历史证据；Agent 应优先使用它回答“很久以前某次工具输出中的精确值”，而不是为了恢复旧证据重新操作真实设备。

### 3.4 Artifact

大结果和文件不应长期占据事件正文：

- 日志文件；
- docker inspect / API 大响应；
- patch / diff；
- 截图；
- CSV / 报告；
- 构建产物。

当前实现对超过 12k 字符的 MCP 结果先做结构化 credential 脱敏，再把完整脱敏结果保存到 PostgreSQL `agent_artifacts`；Session FunctionResponse 只保留最多约 4k 字符 preview、`artifact_ref`、version 与长度。Agent 通过 `cycledock_read_artifact` 按需分块读取，每次最多 20k 字符，不需要重新调用真实 MCP 工具。

### 3.5 Memory

Memory 用于跨 Card / 跨 Session 的长期知识，不用于替代同一 Card 的 Session history。

第一阶段不引入向量数据库。先解决 Card 内持续上下文；出现明确跨 Card 召回需求后，再实现 CycleDock MemoryService。

## 4. Card 仍是产品核心

Session-first 不取消 Card。

### Discussion

探索、讨论、澄清型 Session。可以派生 Todo / Ongoing。

### Todo

有明确完成终点的任务型 Session。业务状态仍为：

```text
Todo -> Working -> Review -> Done
```

Review -> Done 仍只允许 Human。

### Ongoing

长期/周期执行 Session。业务状态仍为：

```text
Active / Paused / Archived
```

同一 Ongoing 的每次定时执行进入同一 Session，使 Agent 能看到近期巡检上下文；陈旧历史由 compaction / retrieval / artifact 控制。

## 5. Discussion 派生工作

Discussion 生成 Todo / Ongoing 时 **不共享同一个 Session**。

流程：

```text
Discussion Session
    |
    | Generate Work
    v
Handoff Package
    |
    v
New Todo/Ongoing Card
= New Agent Session
```

Handoff Package 只包含新工作真正需要的内容：

- goal；
- constraints；
- acceptance criteria；
- confirmed decisions；
- relevant findings；
- 必要 Artifact refs；
- source_card_id。

这样派生任务继承关键知识，但不会继承 Discussion 的全部噪声历史。

## 6. Turn 与 Attempt

旧 Trigger / AgentRun 语义重构为 Turn / Attempt。

### Turn

Turn 表示 Card Session 的一次新输入方向：

- human；
- schedule；
- system。

技术 retry / recovery 不创建新 Turn，而是在同一 Turn 下创建新的 Attempt。

Turn 保存用户/系统原始意图和 dispatch mode。

排队的 Turn **不能提前 append 到 ADK Session**。只有 Turn 被 Claim 开始执行时，才把该输入交给 ADK Runner，避免正在运行的上一 Turn 看到未来排队消息。

### Attempt

Attempt 表示一次真实 invocation：

```text
Turn 42
├── Attempt 1 failed
└── Attempt 2 completed
```

技术重试不需要制造新的业务 Turn。

Attempt 保存：

- ADK invocation ID；
- status；
- model route；
- heartbeat / lease；
- started / finished；
- safe error summary。

## 7. Queue / Interrupt

CycleDock 继续掌握调度权。

### Enqueue

当前 Attempt 不受影响；新 Turn 保存在队列，直到它成为 current Turn 才 append user input 到 Session。

### Interrupt

当前 Attempt 被 supersede/cancel，新 Turn 优先启动。

Session persistence 必须防止旧 Attempt 的迟到事件污染当前 Session。AppendEvent 层需要依据 invocation/attempt ownership 做 stale-write protection；不能只依赖上层 goroutine cancellation。

## 8. ADK 边界

### 交给 ADK

- Session / Event 模型；
- Agent loop；
- tool call loop；
- context assembly 基础能力；
- context compaction；
- artifact interface；
- sub-agent / isolation / branch 语义；
- future multi-agent workflows。

### CycleDock 保留

- Workspace / Project / Card；
- Todo / Ongoing 状态机；
- Turn queue / interrupt；
- Attempt lease / cancellation / stale-write guard；
- Schedule；
- Human Review / Done；
- auth / tenant isolation；
- model credentials；
- ordered model routes / failover policy；
- NexusDock first-party OAuth / bootstrap；
- PostgreSQL schema 与 migration。

## 9. Session 身份

产品约束：

```text
app_name   = "cycledock"
user_id    = workspace_id
session_id = card_id
```

使用 Workspace ID 作为 ADK user_id，而不是登录 Human ID，因为一个 Card 可以由多个 Workspace 成员、Schedule 和 Agent 共同推进。

所有对外请求仍从认证上下文取得 Workspace scope，不能信任客户端传入的 user_id/session owner。

## 10. SessionService

不使用 ADK 自带 GORM DatabaseSessionService。

CycleDock 实现 `session.Service`：

```go
Create
Get
List
Delete
AppendEvent
```

持久层继续使用 pgx / sqlc / Goose。

第一阶段表：

```text
agent_sessions
agent_session_events
agent_app_state
agent_user_state
```

事件正文以 ADK Event JSON 持久化，保留 compaction metadata、tool calls、tool responses、branch/isolation 等 ADK 语义。

SessionService 必须通过真实 PostgreSQL 并发测试；后续优先跑 ADK 官方 session conformance suite。

## 11. Model Runtime

ADK Go 原生 OpenAI model 支持可以替换大部分通用 model loop。

但 CycleDock 仍保留自定义 provider 层：

- ChatGPT Sign in with ChatGPT credential；
- Grok OAuth credential；
- OpenAI-compatible endpoint；
- 多账号；
- ordered route；
- CycleDock 自己的 failover / streaming reset policy。

ADK experimental model routing 不作为唯一故障转移保证。

## 12. MCP / NexusDock

普通 MCP 优先迁移到 ADK McpToolset。

NexusDock 继续作为 first-party integration：

- CycleDock 管授权；
- runtime bootstrap agentdock_context；
- 隐藏内部 bootstrap tool；
- 设备目录注入；
- native tool names；
- 按 Workspace 权限过滤。

MCP 连接恢复属于每次 invocation/runtime 的技术职责；Session 持久化不意味着持久保存活动网络连接。

## 13. 数据保留与安全

Session-first 不等于“保存所有秘密”。

必须禁止持久化：

- chain-of-thought；
- 明文 credential / Cookie / access token；
- 无必要的环境 secret；
- 用户未授权的敏感材料。

工具返回如包含 secret，应在进入持久 Session Event 前做安全过滤或改为受控 Artifact 引用。

## 14. 开发顺序

### Phase A — Foundation

- 锁定 ADK Go v2；
- 建立 Session-first 文档基线；
- 实现 PostgreSQL SessionService；
- Card ID / Workspace ID Session identity；
- Session Event 跨 invocation 持久化；
- compaction metadata round-trip。

### Phase B — Turn / Attempt

- 新建 card_turns / turn_attempts；
- Dispatcher 改为 Turn -> Attempt；
- enqueue / interrupt / retry；
- current Attempt 唯一约束；
- lease / recovery；
- stale invocation event guard。

### Phase C — Runner cutover

- ADK Runner 驱动 Card Session；
- 接 Model Routes；
- 接 MCP / NexusDock；
- SSE 映射 ADK Events；
- waiting_human / confirmation。

### Phase D — Context & Artifact

- Context Compaction；
- Session history retrieval；
- Artifact service；
- 大工具结果 externalization；
- freshness policy。

### Phase E — External MCP Cutover

- External OAuth MCP Agent 全面使用 Turn / Attempt Store。
- Card-scoped Claim、Attempt ownership、network retry idempotency。
- Human reply / Schedule Turn 可由 External Agent Claim。
- 删除旧 External MCP 工具名和旧字段。

### Phase F — Cleanup / Release Baseline

- 删除旧执行 loop、Context Builder / context_budget、旧 Store/query/schema 与对应测试。
- 保留 modelclient / mcpclient 作为 ADK adapter 的 credential、provider failover 与 transport 基础设施，不保留第二套 Agent loop。
- SSE 统一为 `attempt.*` + `attempt_id`。
- 重整为单一 `000001_initial.sql`，无旧执行表/列。
- final baseline 通过 ordinary DB、自定义 `search_path`、migration round-trip 与稳定 sqlc generation。

## 15. 验收场景

至少覆盖：

1. 同一 Card Turn 1 查询 20 个容器，Turn 2 询问“第 8 个容器是什么镜像”，不得再次调用 docker ps。
2. 用户问“现在第 8 个容器还是这个镜像吗”，Agent 能识别 freshness 要求并重新查询。
3. Session 超过 compaction threshold 后，近期指代仍正确。
4. 被压缩的旧工具结果可以通过 history/artifact 重新读取。
5. Enqueue 的未来 Turn 不得提前污染 current invocation。
6. Interrupt 后旧 Attempt 晚返回不得 append Session Event 或改变业务状态。
7. Ongoing 多次 Schedule 使用同一 Card Session。
8. Discussion 派生 Todo 时只传 Handoff Package，不共享完整 Session。
9. 不同 Workspace 的 Session / Artifact / Memory 严格隔离。
10. 进程重启后 Session、state、events、compaction 仍可恢复。
