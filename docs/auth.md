# 认证、身份与租户

## 1. Hosted

CycleDock Hosted 统一使用 NexusDock 账号体系。

NexusDock 作为 OIDC Provider；Google / GitHub / 邮箱等登录方式由 NexusDock 自己管理。

CycleDock 只信任标准 OIDC 身份：

`issuer + sub`

邮箱只作为展示属性，不作为稳定身份主键。

## 2. Hosted 首次登录

首次 OIDC 登录应原子完成：

1. 查找/创建 Human Account。
2. 建立 oidc_identity。
3. 创建个人 Workspace（若没有）。
4. 创建 Human Source。
5. 创建 owner Membership。
6. 可选创建默认 Project，具体产品 onboarding 再决定。
7. 创建 Web Session。

重复登录相同 `issuer + sub` 必须复用同一身份。

未来团队 Workspace 通过 Membership 扩展，不改变 OIDC 主身份模型。

## 3. Self-hosted

未配置 OIDC 时，保留单 Workspace Administrator password fallback。

Self-hosted password 只用于 Human Web，不作为 MCP OAuth 的替代品。

不要为了 Self-hosted 引入另一套 Card / Workspace / Agent 模型。

## 4. Web Session

- 随机 opaque token。
- 浏览器 Cookie：HttpOnly。
- Hosted HTTPS：Secure。
- SameSite=Lax。
- 数据库只存 token SHA-256 digest。
- Session 绑定 Workspace + Human Source + expires_at。

业务请求中的 workspace_id / source_id 不可信；由 Session 决定。

## 5. MCP OAuth

外部 MCP Client 必须独立完成 OAuth 授权。

授权后 Grant 绑定：

- Workspace。
- Human approval。
- OAuth Client。
- OAuth Client Source。
- resource / scope。

MCP tool input 不接受用于越权切换租户的 workspace_id / source_id。

## 6. Source

统一来源：

- human
- cycle_agent
- oauth_client

Human Web Event 使用 Human Source。

内置 AgentRun 使用 tenant-scoped Cycle Agent Source。

外部 MCP Run 使用该 OAuth Client 对应的 oauth_client Source。

这样 Timeline / Run history 可以知道“谁做的”，但不需要建立复杂 Agent 角色系统。

## 7. Secret

Model API Key、MCP token、OAuth client secret 等敏感信息：

- 不通过普通 GET Settings 回显。
- 日志中不得输出。
- DB 中如需持久化，必须与普通展示配置分离，并采用明确的 secret storage / encryption 方案。
- 当前 Built-in Agent **不把 Model API Key / MCP Bearer Token 写入数据库**。
- Workspace Agent settings 只保存 `api_key_env`；MCP connection 只保存 `bearer_token_env`。
- Runner 在每个 Run 开始时从 CycleDock 进程环境解析对应变量；变量缺失时 Run 以脱敏摘要失败。
- 默认 Docker Compose 提供 `CYCLEDOCK_MODEL_API_KEY` 与 `CYCLEDOCK_MCP_TOKEN` 两个可直接转发的环境变量。更多连接需要由部署层显式把对应变量注入 app container。
- 只有 Workspace owner 可以读取或修改 Agent settings / MCP connections。

## 8. 当前 Human Web 配置

Self-hosted 未配置 OIDC 时：

- `CYCLEDOCK_ADMIN_PASSWORD`：至少 12 个字符。

Hosted NexusDock OIDC：

- `CYCLEDOCK_PUBLIC_URL`
- `CYCLEDOCK_OIDC_ISSUER`
- `CYCLEDOCK_OIDC_CLIENT_ID`

Hosted 三项必须一起配置；配置 OIDC 后本地 Administrator password 登录不会暴露。

`CYCLEDOCK_PUBLIC_URL` 为 HTTPS 时 Web Session 与 OIDC state Cookie 自动启用 Secure。
OIDC 的远程 issuer 与 Public URL 都必须使用 HTTPS；仅 localhost / loopback 开发环境允许 HTTP。
Self-hosted 未配置 Public URL 但由 HTTPS 反代暴露时，可通过 `CYCLEDOCK_SECURE_COOKIE=true` 强制启用 Secure。
该开关不能关闭 HTTPS Public URL 自动启用的 Secure。

OIDC Authorization Code 流程使用 S256 PKCE + nonce；state 与 Web Session token 都只以 SHA-256 digest 形式落库。

## 9. 当前 Remote MCP OAuth

配置 `CYCLEDOCK_PUBLIC_URL` 后启用 Remote MCP OAuth 与 `/mcp`。Self-hosted 可以只配置 Public URL + Administrator password，不需要为了 Remote MCP 同时开启 NexusDock OIDC。

Remote MCP 使用 Protected Resource Metadata、Authorization Server Metadata、Dynamic Client Registration、Authorization Code + S256 PKCE、`resource` audience 绑定、opaque access / refresh token 与 refresh rotation。

Human 在浏览器中登录 CycleDock 后批准当前 Workspace。批准记录创建或复用一个 Workspace-scoped `oauth_client` Source；MCP 的 Workspace / Source 都从 Bearer grant 派生，不接受工具参数覆盖。

Authorization Code、Access Token 与 Refresh Token 在数据库中只保存 SHA-256 digest。旧 refresh token 在轮换后被重放时会撤销整个 token family。

远程 `CYCLEDOCK_PUBLIC_URL` 必须使用 HTTPS；localhost / loopback 开发允许 HTTP。
