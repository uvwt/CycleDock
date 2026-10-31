# 技术栈

## Backend

- Go
- chi
- PostgreSQL
- pgx
- sqlc
- Goose
- Google Agent Development Kit (ADK) Go v2
- 官方 Model Context Protocol Go SDK

原则：

- PostgreSQL 是唯一正式数据库。
- 不使用 GORM 作为 CycleDock 数据层。
- ADK 自带 GORM DatabaseSessionService 不进入正式架构。
- CycleDock 自己实现 ADK session.Service。
- 不提前引入 Redis / Kafka / worker 微服务。

## Frontend

- React
- TypeScript
- Vite
- React Router
- TanStack Query
- Tailwind CSS
- shadcn/ui（源码自持的通用组件，基于 Radix UI；接入约定见 [UI 组件](ui-components.md)）
- Lucide React
- dnd-kit
- React Hook Form
- Zod
- Sonner
- react-markdown + remark-gfm
- date-fns

产品实时更新继续优先 SSE。

## Agent Runtime

### Google ADK Go v2

ADK 负责 Agent Runner / event loop、Session / Event、state、context compaction、tool loop、artifact interface、sub-agent / branch / isolation，以及 future multi-agent workflow。

当前锁定基线：google.golang.org/adk/v2 v2.6.0。

### Session persistence

CycleDock 使用 PostgreSQL 自定义实现 internal/agentsession。

正式实现必须满足 ADK session.Service conformance，并继续使用 pgx / Goose。

~~~text
app_name = cycledock
user_id = workspace_id
session_id = card_id
~~~

### Model

ADK Go 的 OpenAI model adapter 可作为通用协议基础，但 CycleDock 保留自己的认证和 route policy：OpenAI-compatible、ChatGPT Sign in with ChatGPT、Grok OAuth、multiple credentials、ordered routes、failover、partial stream reset。

ADK experimental Model Routing 不作为唯一生产故障转移实现。

### MCP

普通 outbound MCP 优先使用 ADK McpToolset。

NexusDock first-party connection 由 CycleDock 管 OAuth、refresh 和 Workspace scope；对话中的 bootstrap、工具定义和工具执行统一使用 MCP，工具通过 ADK FunctionTool 进入 tool loop。前端设备在线状态按需使用独立 API（尚未实现设备列表）。

### Context

~~~text
recent Session Events
+ ADK Context Compaction
+ history retrieval
+ Artifact
+ optional cross-Card Memory
~~~

不再使用“每个 Run 丢掉旧 raw tool context”的设计。

### Memory

第一阶段不引入外部向量数据库。

ADK MemoryService 只在跨 Card / 跨 Session 的长期知识需求出现后实现；Self-hosted 不依赖 Google Cloud Memory Bank 才能工作。

## Runtime / Deployment

第一版继续单 Go 服务 + PostgreSQL + React build；Docker Compose 作为 Self-hosted / DEV 基线。

只有真实负载证明需要时再引入 Redis、外部队列、独立 Agent worker、多服务拆分。

## Observability

需要 structured logs、request/card/turn/attempt/invocation correlation、agent event metrics、scheduler/dispatcher 错误、context/compaction 基础指标。

产品层不默认暴露完整 raw trace 或 chain-of-thought。
