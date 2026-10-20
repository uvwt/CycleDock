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
- 第一阶段在 Built-in Agent 尚未落地前，不提前设计复杂 secrets framework。

## 8. 当前 Human Web 配置

Self-hosted 未配置 OIDC 时：

- `CYCLEDOCK_ADMIN_PASSWORD`：至少 12 个字符。

Hosted NexusDock OIDC：

- `CYCLEDOCK_PUBLIC_URL`
- `CYCLEDOCK_OIDC_ISSUER`
- `CYCLEDOCK_OIDC_CLIENT_ID`

Hosted 三项必须一起配置；配置 OIDC 后本地 Administrator password 登录不会暴露。

`CYCLEDOCK_PUBLIC_URL` 为 HTTPS 时 Web Session 与 OIDC state Cookie 自动启用 Secure。
特殊反代环境可通过 `CYCLEDOCK_SECURE_COOKIE` 显式覆盖。

OIDC Authorization Code 流程使用 S256 PKCE + nonce；state 与 Web Session token 都只以 SHA-256 digest 形式落库。
