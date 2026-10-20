# 技术栈

新 CycleDock 沿用已经在旧 CycleDock / ChatDock 中验证过的技术方向，但重新实现领域模型。

## Backend

- Go
- chi
- PostgreSQL
- pgx
- sqlc
- Goose
- 官方 Model Context Protocol Go SDK（实际版本开发时锁定）

原则：

- 不使用 GORM 作为核心数据层。
- 不为 Self-hosted 平行支持 SQLite。
- 不提前建立 Repository interface 抽象。
- SQL 中 tenant/workspace scope 必须显式。

## Frontend

- React
- TypeScript
- Vite
- React Router
- TanStack Query
- Tailwind CSS
- Radix UI
- Lucide React
- dnd-kit
- React Hook Form
- Zod
- Sonner
- react-markdown + remark-gfm
- date-fns

第一版不引入：

- Next.js
- Redux
- GraphQL
- 重型富文本编辑器
- 复杂实时框架

需要实时感时先评估 polling / SSE。

## Agent Runtime

### Model Client

第一版只需要 OpenAI-compatible provider 边界。

可以参考 ChatDock 已验证实现中的：

- request/response。
- streaming。
- timeout。
- context cancellation。
- provider error mapping。
- /models 获取。

不要一开始做大型 multi-provider abstraction。

### MCP Client

参考 ChatDock 已验证的 MCP Client：

- MCP protocol/session。
- tool list。
- tool schema。
- tool call。
- timeout / response limit。

不要迁移 ChatDock 的 Session / Message / Prompt Space。

## Runtime / Deployment

第一版继续：

- 单 Go 服务。
- PostgreSQL。
- React build 嵌入 Go binary 或在同一镜像内静态托管。
- Docker Compose 作为 Self-hosted / DEV 基线。

只有真实负载证明需要时再引入：

- Redis。
- 外部队列。
- 独立 Agent worker。
- 多服务拆分。

## Observability

需要：

- structured logs。
- request / run correlation id。
- 基础运行指标。
- scheduler / dispatcher 错误可定位。

但产品层不提供 raw model/tool tracing。
