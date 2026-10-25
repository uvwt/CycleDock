# AI Connect

当前实例配置 `CYCLEDOCK_PUBLIC_URL` 后，会公开：

- `/docs/ai-connect/`：面向 AI 助手的实例化接入指南。
- `/skills/cycledock/SKILL.md`：官方 Skill。
- `/skills/cycledock.zip`：可安装 Skill 包。
- `/mcp`：Remote Streamable HTTP MCP。

推荐用户给 AI 的提示词由运行实例动态生成，核心语义是：

> 请阅读当前 CycleDock 的 `/docs/ai-connect/`，按照官方指南帮我接入 CycleDock。尽可能使用当前客户端原生的 Skill、MCP 和 OAuth 能力自动完成；只有登录、授权确认或客户端不允许 AI 修改连接设置时再让我操作。接入后必须确认工具真正注入当前客户端，并实际调用 `list_projects` 和 `list_cards` 验证。

## AI 助手必须遵守

- 优先使用客户端原生 Remote MCP / Connector 功能。
- OAuth 登录、验证码、Workspace 授权确认由 Human 完成。
- 不向用户索取 Access Token、Refresh Token、Cookie 或密码。
- 不用 curl/Python/临时代理成功来冒充“当前客户端已经接入 MCP”。
- OAuth 完成后必须检查工具发现，再真实调用工具。
- 支持原生 Skill 时读取官方 Skill；不支持 Skill 不影响 MCP 使用。

## 接入成功判定

只有以下两点同时成立才算成功：

1. 当前 AI 客户端能发现 CycleDock MCP 工具。
2. `list_projects` 与 `list_cards` 能读取 OAuth 授权 Workspace 的真实数据。

网页可打开、OAuth 页面可打开、`/mcp` 返回 401 challenge、或者脚本能直接请求服务，都不足以单独证明客户端接入成功。

## 周期工作建议

CycleDock 自带 Ongoing Schedule，它只创建 Schedule Turn，不直接执行 Agent。

如果当前 AI 客户端还要使用自己的定时任务：

- 先通过 `get_card` 读取 Card 与 `pending_turns / current_attempt`。
- 不要重复创建已经存在的 Turn。
- 周期巡检完成后用 `check_in` 写 Health 事实。
- 真正需要另一个独立工作方向时使用 `create_turn`；要执行某个 Card 的 pending Turn 时再调用 `claim_attempt`。
