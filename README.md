# CycleDock

CycleDock 是一个以项目看板和任务为核心的人机协作系统。

它不是通用聊天产品，也不是 Agent tracing 平台。CycleDock 让人类与 AI Agent 围绕同一个任务持续协作：任务长期存在于 Card 中，每一个新的工作方向形成 Trigger，每一次具体执行形成 Run；系统记录工作的进展、发现与结果，而不是保存模型内部完整对话和工具调用轨迹。

核心关系：

```text
Workspace
└── Project
    └── Card
        ├── Event
        ├── Trigger
        │   └── AgentRun
        └── Schedule   # 仅 Ongoing Card
```

关键原则：

- Project 本身就是一个项目看板，不存在独立 Board 领域实体。
- Card 是长期任务与上下文载体。
- Trigger 是一次新的工作方向，也是天然的待执行队列项。
- 创建 AgentRun 即表示成功 Claim 一个 Trigger，不单独建立 Claims 表。
- Run 只保存执行状态；旧 Run 的原始模型消息、工具参数和工具结果不会自动传给后续 Run。
- Tool Result 是原材料，Finding / Result 才是任务记忆。
- 用户可选择把新方向“加入队列”或“立即转向”。
- Ongoing Card 可按日、周、月自动产生 Trigger。
- 内置 Agent 与外部 MCP Agent 共用同一套领域状态机。
- 最终 Review / Done 保留人类控制。

## 文档

- [产品定义](docs/product.md)
- [系统架构](docs/architecture.md)
- [数据模型](docs/data-model.md)
- [Agent Runtime 与上下文](docs/agent-runtime.md)
- [Ongoing 调度](docs/scheduling.md)
- [MCP 设计](docs/mcp.md)
- [认证与租户](docs/auth.md)
- [Human Web HTTP API](docs/http-api.md)
- [AI Connect](docs/ai-connect.md)
- [技术栈](docs/stack.md)
- [开发路线](docs/development.md)
- [旧项目参考边界](docs/legacy-reference.md)

## 旧项目

本仓库是全新一代 CycleDock，从零开始设计，不继承旧仓库 migration 和领域结构。

- 旧 CycleDock：<https://github.com/uvwt/CycleDock-old>
- ChatDock：<https://github.com/uvwt/chatdock>

旧项目只作为经过验证的实现参考。新代码应按当前文档重新判断职责和模型，不能直接把旧结构整体复制进来。

## 当前阶段

Phase 0 Foundation、Phase 1 Work Core、Phase 2 Human Web、Phase 3 External MCP 与 Phase 4 Built-in Cycle Agent 已形成可运行基线：

- 干净的 `000001_initial.sql` 与 PostgreSQL / pgx / sqlc / Goose 数据层。
- Trigger Claim、current Run、enqueue / interrupt / supersede、waiting_human、heartbeat / lease。
- Ongoing Schedule / Check-in / Health 与 Human-only Done。
- 真实 PostgreSQL 并发 integration tests。
- Go + React 单镜像、Docker Compose migration 流程与 CI 骨架。
- Hosted NexusDock OIDC / Self-hosted Administrator fallback 与 opaque Web Session。
- Project Board、Card Detail、Timeline、Queue、Human Review/Done 与 Ongoing Schedule Human Web。
- 标准 Remote Streamable HTTP MCP、OAuth metadata / DCR / S256 PKCE / refresh rotation。
- Workspace-scoped MCP tools、原子 Claim、Progress/Finding/Check-in/Finish/Confirm 与官方 CycleDock Skill。
- Workspace-scoped Built-in Cycle Agent settings、Dispatcher、Runner 与 Context Builder。
- OpenAI-compatible Model Client 与 outbound MCP Client；模型/tool raw trace 只存在于当前 Run 内存。
- Built-in Agent secret 配置只保存环境变量名，API/数据库不保存 Model API Key 或 MCP Bearer Token 明文。

下一阶段进入 [Phase 5 — Product Polish](docs/development.md#7-phase-5--product-polish)，重点做运行恢复、配置体验、可访问性与长历史上下文治理。
