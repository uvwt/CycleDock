# CycleDock

CycleDock 是一个以 Project / Card 为核心的人机协作工作台。每个 Card 同时是一个持久 Agent Session：Human、Schedule 与 AI Agent 围绕同一个工作对象持续讨论和执行，CycleDock 负责业务状态、队列、权限、Schedule 与 Review，Agent Runtime 负责连续上下文、工具调用、压缩和产物。

核心关系：

~~~text
Workspace
└── Project
    └── Card = persistent Agent Session
        ├── Turn
        │   └── Attempt
        ├── Session Events
        ├── Artifact
        └── Schedule / Business State
~~~

关键原则：

- Project 本身就是项目看板，不建立独立 Board 领域实体。
- Card 分为 Discussion / Todo / Ongoing。
- 一个 Card 对应一个持久 Agent Session。
- Card ID 直接作为 ADK Session ID；Workspace ID 作为 ADK user_id。
- 同一 Card 后续追问可以使用前面 Turn 的 tool call / tool response。
- 长历史通过 Context Compaction、History Retrieval 与 Artifact 管理，而不是跨 Turn 丢弃工具证据。
- Turn 是一次新工作输入；Attempt 是一次真实 invocation。
- 排队 Turn 只有真正开始执行时才进入 Agent Session。
- Todo 保持 Todo → Working → Review → Done；最终 Done 由 Human 控制。
- Ongoing 的每次 Schedule 进入同一 Card Session。
- Discussion 派生 Todo / Ongoing 时创建新 Session，并通过 Handoff Package 传递必要知识。
- Google ADK Go v2 是 Built-in Agent Runtime 基础；CycleDock 保留业务调度、认证、Model Routes、NexusDock 与 PostgreSQL ownership。

## 文档

本地开发：首次运行 `make setup`，配置 `.env.local`，之后运行 `make dev`。
默认访问 http://127.0.0.1:5173，详见 [开发环境](docs/development.md#本地开发)。

- Session-first / ADK 架构决策：docs/session-first-adk.md
- 产品定义：docs/product.md
- 系统架构：docs/architecture.md
- 数据模型：docs/data-model.md
- Agent Runtime 与上下文：docs/agent-runtime.md
- Ongoing 调度：docs/scheduling.md
- MCP 设计：docs/mcp.md
- 认证与租户：docs/auth.md
- Human Web HTTP API：docs/http-api.md
- AI Connect：docs/ai-connect.md
- Self-host 安装：docs/install.md
- 技术栈：docs/stack.md
- 开发路线：docs/development.md

## 当前阶段

CycleDock 尚未首次公开发布，因此当前 Session-first 重构不迁移旧 DEV Runtime 数据。

当前开发分支已经完成统一 Session-first Runtime：Human Web、Schedule、Built-in ADK 与 OAuth MCP 全部使用同一 Card → Turn → Attempt 模型；长 Session 的 Tail Retention Context Compaction、PostgreSQL Artifact、大 MCP 结果外置、Artifact 按需读取和 Session history retrieval 也已完成。

Phase F cleanup 已删除旧执行 Runtime 与旧 Context Builder，并把数据库重整为单一首次发布 baseline：schema 中只保留 Turn / Attempt，`events` 不再携带旧执行外键。当前进入最终 release verification，不保留旧 Runtime API、MCP 工具或数据库兼容层。
