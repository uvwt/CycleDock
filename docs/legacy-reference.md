# 旧项目参考边界

## 1. 旧 CycleDock

GitHub：

<https://github.com/uvwt/CycleDock-old>

旧项目已经验证过大量基础设施，但其最初核心假设是：

> CycleDock 是外部 AI 共用的状态层，自身不运行 Agent / Scheduler。

新 CycleDock 已经改变该假设，因此不能在旧代码上继续堆 v2。

### 可以参考

- Go + chi 服务组织。
- PostgreSQL / pgx / sqlc / Goose。
- Tenant / Workspace scope。
- Web Session。
- NexusDock OIDC。
- human_accounts / oidc_identities / memberships 的经验。
- Remote MCP Server。
- OAuth DCR / PKCE / token rotation。
- Human-only Done。
- Ongoing Check-in / health 派生。
- React Board / Card Detail。
- Docker / CI / DEV 部署经验。

### 不直接继承

- Project → Board → Card 层级；新项目没有 Board 实体。
- “CycleDock 不运行 Agent”的旧产品边界。
- 旧固定 7 个 MCP 工具契约。
- 外部 Scheduler 是唯一周期执行方式的旧假设。
- 旧 migration 历史。

## 2. ChatDock

GitHub：

<https://github.com/uvwt/chatdock>

ChatDock 的核心假设是：

> 以 Session / Conversation 为中心的轻量 AI 对话中控台。

新 CycleDock 不是聊天产品，因此只抽取已经验证的运行能力。

### 可以参考

- OpenAI-compatible client。
- streaming / timeout / cancellation。
- provider error handling。
- /models。
- MCP Client。
- tool schema / tool call。
- MCP response size / timeout 防护。

### 不迁移

- SQLite。
- Session / Message。
- Prompt Space。
- Chat history。
- Chat Job。
- raw tool trace。
- ChatDock Scheduled Task 数据模型。
- session-bound 上下文策略。

## 3. 复用原则

每次准备复用旧代码时先回答：

1. 这段代码服务的是稳定基础设施还是旧产品模型？
2. 它的输入输出是否符合新领域模型？
3. 能否直接复用而不把 Session / Board / 旧 Scheduler 假设带进来？
4. 是否应该仅参考测试和协议处理，再按新职责重写？

默认优先“参考实现并按新边界重写”，而不是复制整个 package。
