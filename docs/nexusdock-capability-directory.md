# NexusDock 能力目录接入

CycleDock 第一方连接仍使用 NexusDock MCP OAuth，Token 的 resource 为 `/mcp`。Agent 每次建立 MCP Session 后，优先用同一 Bearer Token 请求 NexusDock Cloud 的只读 HTTP 目录：

1. `GET /v1/integrations/devices` 获取设备和实时在线标记。
2. `GET /v1/integrations/capabilities` 获取服务端已缓存的 Skill、MCP、Plugin、Tool 名称。
3. 将设备、能力索引（最多 20 台设备，每类最多 14 项）作为非可信元数据追加到本轮上下文；真实执行仍通过已连接的 MCP Session。

Cloud 老版本没有该 API（404）或目录服务暂时不可用时，沿用现有 `agentdock_context` 发现方法。目录明确标注“元数据不等于可执行权限”；在线状态由设备接口决定。拒绝 HTTP 重定向（防止 Bearer 泄漏），响应大小上限 2 MiB；401/403 等授权错误不会静默回退。

新接口支持的 REST 发现流程不再主动调用 `agentdock_context`，但该 MCP 工具保留为旧版本兼容入口。未来若需要增量更新，可另行扩展，不在 CycleDock 自建第二套设备注册表。
