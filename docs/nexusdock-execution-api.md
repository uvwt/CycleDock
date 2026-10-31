# NexusDock Execution API 接入

CycleDock 对 NexusDock 的第一方连接分成三个稳定职责：

1. **MCP**：继续提供模型可见 Tool 定义、Fleet Schema 合并与 NexusDock 自有工具。
2. **设备/能力目录 REST**：提供设备元数据以及每个节点 Tool 的 capability_id / contract_hash。
3. **Execution REST**：当模型实际调用能够精确映射到 `(node_id, tool_name)` 的 AgentDock Tool 时，创建持久 Execution 并通过 SSE 等待终态。

这样 CycleDock 不复制 NexusDock 的 Fleet Schema 合并逻辑，也不把目录缓存当成执行保证。真正执行仍由 Cloud 对在线 AgentDock 的当前 Hello.Tools 再校验。

## 调用路由

普通 MCP 与 NexusDock 自有工具始终通过现有 MCP Session。

NexusDock 第一方 MCP Tool 满足以下条件时改走 Execution API：

- Tool 来自第一方 NexusDock MCP server。
- 参数包含目标 `node_id`；兼容入口 `node_tool_call` 则使用其 `node_id + name + arguments`。
- 同一 Attempt 启动时读取的能力目录中存在在线、非 stale 且带 contract_hash 的对应 Tool capability。

直接 Tool 的 `node_id` 只用于 Cloud 路由，发送给 AgentDock Tool 的 input 会去掉它。`node_tool_call` 会解包为真实 Tool name / arguments。

## 幂等与故障语义

CycleDock 使用 `Attempt ID + ADK FunctionCallID + node_id + tool_name` 计算固定 SHA-256 idempotency key。原始 Card/Attempt 内容不进入 key。对同一个 ADK FunctionCall 的 HTTP 重试始终复用同一 key。

- POST 网络错误或 5xx：只以同一个 idempotency key 重试 REST；**绝不切回 MCP**，因为第一次请求可能已经创建甚至执行。
- 旧 Cloud 对 Execution 路由明确返回普通 404：本 Attempt 缓存为 unsupported，继续使用既有 MCP 调用。
- `CAPABILITY_SCHEMA_CHANGED`：Cloud 明确保证它发生在 Execution 创建/发送之前，因此只对这个错误安全退回 MCP 的实时 Fleet/节点 Schema 校验路径。MCP 不会直接用旧 Schema 参数执行新版本工具。
- Execution 返回 `unknown`：作为 Tool error 交给模型，并明确 `retry_safe=false`；不能自动重复有副作用的操作。
- Attempt / FunctionCall context 取消：CycleDock 使用独立短超时请求 `POST .../cancel`，不依赖已取消的原 context。

## SSE 与结果

创建成功后，CycleDock 使用 `GET /executions/{id}/events` 等待状态变化，携带 `Last-Event-ID` 断线重连。事件只决定状态；进入终态后再 GET 详情读取最终结果。

成功结果保持 AgentDock/MCP Tool Result JSON 形状，继续走 CycleDock 现有 Sanitizer、Session Event 和大结果 Artifact 外置逻辑。失败/取消/unknown 转成 `isError=true` 的 Tool Result，而不是伪装成成功文本。

Bearer Token 只发送到与配置 `/mcp` 相同的 origin；REST 客户端拒绝所有 HTTP 重定向。生产只允许 HTTPS，本机 loopback 测试可用 HTTP。
