# NexusDock 对话接入与设备展示

## 当前实现：对话统一使用 MCP

CycleDock 第一方连接使用 NexusDock MCP OAuth，Token 的 resource 为 `/mcp`。每次 Attempt 建立授权 MCP Session：

1. 使用 MCP 工具目录获取名称、说明和参数 Schema；多设备的同名工具契约由 NexusDock 聚合和校验。
2. 通过 MCP 调用内部 `agentdock_context`，将设备、在线状态和能力摘要作为不可信元数据注入本轮上下文。摘要最多保留 20 台设备，不作为执行权限保证。
3. 设备工具和 Cloud 自有 Recall / Workflow 工具均通过同一 MCP Session 调用，使用 NexusDock 原生工具名称。设备路由参数由 NexusDock 校验和处理。
4. 返回结果经过统一脱敏，持久化为 ADK Session FunctionResponse；大结果进入 Artifact。

`agentdock_context` 在 bootstrap 后不重复暴露给模型。后续按任务需要调用 MCP 提供的工作区、Skill、动态 MCP 等工具。对话运行时不请求 REST 能力目录或详情，不在 CycleDock 合并另一份 Schema，也不通过 Execution API 分流设备执行。

OAuth discovery、授权、refresh 和 Workspace scope 继续由 CycleDock 管理。认证凭据不交给模型或浏览器。普通第三方 MCP 连接与第一方连接分别管理。

## 目标态：前端通过 API 展示设备

目前设置页只展示 NexusDock 连接状态，尚未实现设备在线列表。

前端需要展示设备在线状态时，应请求 CycleDock 自己的认证 API，由后端复用当前 Workspace 的 NexusDock 授权调用 `GET /v1/integrations/devices`，只返回展示所需的设备摘要。能力摘要有实际展示需求时再接入 `GET /v1/integrations/capabilities`。

前端不建立 MCP Session，不直接持有 NexusDock Token。展示数据与对话工具调用独立，页面刷新或轮询不触发模型工具调用，也不修改 Card / Turn / Attempt 状态。
