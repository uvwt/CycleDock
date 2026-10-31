# NexusDock 对话接入与设备展示

## 当前实现：对话统一使用 MCP

CycleDock 第一方连接使用 NexusDock MCP OAuth，Token 的 resource 为 `/mcp`。每次 Attempt 建立授权 MCP Session：

1. 使用 MCP 工具目录获取名称、说明和参数 Schema；多设备的同名工具契约由 NexusDock 聚合和校验。
2. 通过 MCP 调用内部 `agentdock_context`，将设备、在线状态和能力摘要作为不可信元数据注入本轮上下文。摘要最多保留 20 台设备，不作为执行权限保证。
3. 设备工具和 Cloud 自有 Recall / Workflow 工具均通过同一 MCP Session 调用，使用 NexusDock 原生工具名称。设备路由参数由 NexusDock 校验和处理。
4. 返回结果经过统一脱敏，持久化为 ADK Session FunctionResponse；大结果进入 Artifact。

`agentdock_context` 在 bootstrap 后不重复暴露给模型。后续按任务需要调用 MCP 提供的工作区、Skill、动态 MCP 等工具。对话运行时不请求 REST 能力目录或详情，不在 CycleDock 合并另一份 Schema，也不通过 Execution API 分流设备执行。

OAuth discovery、授权、refresh 和 Workspace scope 继续由 CycleDock 管理。认证凭据不交给模型或浏览器。普通第三方 MCP 连接与第一方连接分别管理。

## 当前实现：前端通过 API 展示和选择设备

聊天、卡片、工作、计划、活动和设置页面右上角提供设备菜单。按钮显示在线数量，展开后按在线优先列出设备名称、系统和架构，并提供刷新和连接设置入口。菜单展开时每 10 秒刷新，收起时每 30 秒更新，后台标签页不轮询；错误不伪装成零台在线。未启用、未连接、空列表和授权过期分别提示。

前端请求 `GET /api/v1/agent/nexusdock/devices`，后端复用认证 Workspace 的 OAuth 授权调用同 origin 的 `GET /v1/integrations/devices`。只返回 `available`、`connected` 和设备摘要，Bearer 不进入浏览器；请求上限 12 秒、响应上限 2 MiB，拒绝重定向。401/403 映射为重新授权提示。成员可只读设备列表，Owner 才能管理连接。

用户可以选择在线设备，或保持默认的“不指定设备”。选择在当前 Workspace 的页面间共享，切换 Workspace 或刷新页面后重置。断开连接时也清除选择；已选设备暂时不可用时保留偏好并提示，不静默切换目标。

发送消息时，`device_target` 的节点 ID 和名称快照随 Turn 保存为 `device_node_id` / `device_name`，纳入请求幂等校验。选择只影响之后发送的输入，不修改当前 Attempt 或已排队 Turn；编辑排队正文不改变目标，技术重试沿用原 Turn 的选择。新讨论首次发送失败后的重试也保留原目标，排队列表显示已选设备。

Built-in Agent 在 Claim 后才将该选择作为本轮用户输入的一句上下文追加到 ADK Session；不指定时明确清除历史设备偏好。外部 Agent 可在 Attempt / Pending Turn 响应中读取目标。选择仅表达用户意图，不授予设备权限或强制隔离工具；实际执行继续由 MCP 校验授权。

前端不建立 MCP Session，不直接持有 NexusDock Token。页面刷新和设备选择不触发模型工具调用，不制造新的业务 Turn。能力详情展示仍未接入，有实际需求时再扩展。
