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

## Hosted 账号登录与独立设备授权

NexusDock OIDC 只负责登录 CycleDock 并创建 Human Session，成功后返回原始目标页面。**不**在登录时自动重定向到设备 MCP OAuth。
在设置的「NexusDock 远程设备」中，Workspace Owner 主动点击「连接 NexusDock 设备」才触发独立 MCP OAuth Consent。授权时用户可以选择与 OIDC 登录账号不同的 NexusDock 账号 B：OIDC A 决定工作空间归属，OAuth B 决定可访问的设备资源；B 的 Token 只绑定发起授权的 Workspace，不替换 A 的登录会话。
OAuth state 绑定 Workspace + Human + Source，授权码使用 PKCE，所有设备权限必须经资源账号明确同意。NexusDock OAuth 的 token 响应附加资源账号 sub，服务端保存为 authorized_subject、连接状态展示该账号；无 sub 不落库。刷新 token 时若已知资源账号 sub 变化，拒绝继续使用并要求重新授权。旧连接记录的 sub 初始为空，重新连接后显示。
用户拒绝设备授权或断开连接不影响 OIDC 登录；断开时撤销已授权的 refresh token。一个 Workspace 目前只保留一条 NexusDock 连接，重新授权会替换原连接；多资源账号并存是独立需求，不在本次变更中。

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

内置 Agent Attempt 使用 tenant-scoped Cycle Agent Source。

外部 MCP Attempt 使用该 OAuth Client 对应的 oauth_client Source。`claim_attempt`、heartbeat、progress、finding、check-in、finish 都校验 Attempt 的 `runner_source_id`；知道另一个 Attempt UUID 也不能越权写入。

这样 Timeline / Attempt history 可以知道“谁做的”，但不需要建立复杂 Agent 角色系统。

## 7. Secret

Model API Key、MCP token、OAuth client secret 等敏感信息：

- 不通过普通 GET Settings 回显。
- 日志中不得输出。
- DB 中如需持久化，必须与普通展示配置分离，并采用明确的 secret storage / encryption 方案。
- Built-in Agent 的 Model API Key 仍采用 `api_key_env` 环境变量引用。
- 普通 MCP 连接可由 Workspace owner 在前端填写真实 `bearer_token`；数据库只存储 `bearer_token_ciphertext`（AES-256-GCM），不保存明文或环境变量名称。
- MCP 加密使用部署侧 `CYCLEDOCK_CONNECTION_ENCRYPTION_KEY`（32 字节 base64 或 hex）；随机 nonce，AEAD AAD 绑定 Workspace ID 与连接 ID，密钥只在服务端。
- MCP GET / POST / PUT 响应只有 `token_configured`，从不回显明文或密文。PUT 留空保留已配置 Token，`clear_bearer_token: true` 显式清除。
- 保存与测试带 Token 连接必须有可用密钥；密钥丢失或密文损坏时安全失败。Test 可以使用新输入的 Token，或通过同 Workspace 的 `connection_id` 测试已保存的 Token。
- 只有 Workspace owner 可以读取或修改 Agent settings / MCP connections。
- Provider / MCP connection test 同样要求 Workspace owner；测试结果只返回分类状态，不回显 secret 或上游原始错误体。
- MCP connection test 只做 initialize + ListTools，不执行任何有副作用 Tool。

### Model account OAuth

- ChatGPT 使用 OpenAI 官方 Sign in with ChatGPT。授权码流使用 PKCE + state + nonce；state 只保存 SHA-256 digest，flow payload 加密保存。
- ChatGPT plan inference 只请求公开 api.openai.com/v1/responses，不调用 ChatGPT backend-api。
- Hosted / remotely hosted CycleDock 正式使用时必须配置 OpenAI 批准的 CYCLEDOCK_CHATGPT_CLIENT_ID；loopback Self-hosted 开发可直接使用 dynamic_agent_client。
- CYCLEDOCK_CHATGPT_ALLOW_HOSTED_DYNAMIC_REGISTRATION=true 是**临时测试逃生口**：仅允许受控 Hosted DEV 在尚未取得正式 Client ID 时验证流程，默认关闭，不能作为生产方案。该模式使用 OpenAI 接受的 `http://localhost:1455/auth/callback` loopback redirect，不发送 `ext_agent_host_id`；授权完成后用户把浏览器地址栏里的完整 localhost callback URL 粘回 CycleDock，由 owner-authenticated API 在原 Workspace 内完成 PKCE token exchange。取得正式 Client ID 后必须删除/关闭该开关。
- Hosted 未配置正式 Client ID 且未开启上述临时测试开关时，Web 不展示 ChatGPT Plan 入口。
- Grok 使用 auth.x.ai 的 OAuth2 device-code flow；token endpoint 必须来自 xAI OIDC discovery 且 host 限定为 x.ai / *.x.ai。
- Grok subscription inference 固定发送到 cli-chat-proxy.grok.com/v1，OAuth token 不允许被用户配置转发到任意 endpoint。
- ChatGPT / Grok access token、refresh token 与 flow payload 存入独立表，并使用 CYCLEDOCK_MODEL_SECRET_KEY 提供的 32-byte key 做 AES-256-GCM 加密；AAD 绑定 Workspace + Provider。
- 同一 Workspace + Provider 可以连接多个账号。credential 使用独立 UUID；ChatGPT 用已验证 ID token 的 sub，Grok 优先使用 user id、缺失时使用 email 作为稳定 account_subject。重新授权同一 subject 更新原 credential。
- Model Route 只保存 credential ID；数据库复合 FK 阻止跨 Workspace 引用，仍被 Route 使用的 credential 不能断开。
- Refresh token 轮换按具体 credential row lock 串行完成，避免同一账号的并发 Run 同时消费 rotating refresh token；不同账号互不覆盖。

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

## 10. Hosted NexusDock 设备授权

Hosted 登录身份和设备控制授权是两个独立安全边界：

- `issuer + sub` 只回答“谁登录了 CycleDock”。
- NexusDock MCP OAuth grant 回答“这个 CycleDock Workspace 是否被用户允许调用对应 NexusDock Workspace 的 MCP/device 能力”。

第一方设备连接复用 NexusDock 标准 MCP OAuth：

1. CycleDock 自动读取 NexusDock Protected Resource / Authorization Server Metadata。
2. CycleDock 自动注册或复用一个 public OAuth client。
3. 用户从 Agent Connections 点击“连接 NexusDock 远程设备”。
4. CycleDock 使用 Authorization Code + S256 PKCE 跳转到 NexusDock consent。
5. NexusDock 从自己的登录 Session / Membership 决定 grant 的 Tenant；CycleDock 不提交 `tenant_id`。
6. callback 换取 access/refresh token 后，CycleDock 才把该 Workspace 标记为已连接。

Token 存储：

- NexusDock access/refresh token 不写入普通 Settings，也不回显给 Web。
- 两者使用 `CYCLEDOCK_CONNECTION_ENCRYPTION_KEY` 的 AES-256-GCM 密文持久化；AEAD associated data 绑定 CycleDock Workspace 与 token 用途。
- refresh token 轮换由 PostgreSQL advisory lock 按 Workspace 串行化；拿锁后必须重新读取当前密文，再决定是否刷新，避免两个进程同时使用同一 refresh token 触发 NexusDock replay-revocation。
- 断开连接先调用 NexusDock revoke；远端撤销成功后才删除本地连接记录。
- 日志与 API 错误永远不包含 token、authorization code、PKCE verifier 或上游原始敏感响应。

Hosted 第一方连接由部署层配置：

- `CYCLEDOCK_NEXUSDOCK_MCP_URL`
- `CYCLEDOCK_NEXUSDOCK_WEB_URL`
- `CYCLEDOCK_CONNECTION_ENCRYPTION_KEY`

这些是服务端部署配置，不是终端用户填写的 MCP 设置。未配置第一方 NexusDock 参数时设备卡片不可用；普通 MCP 无 Token 连接仍能使用，但要保存或调用带 Bearer Token 的普通 MCP，必须配置加密密钥。

### NexusDock DEV 环境配置

RN DEV 的 OIDC issuer 为 https://dev.nexusdock.co，第一方 MCP URL 为 https://dev.nexusdock.co/mcp，Web URL 为 https://dev.nexusdock.co。三项都应指向 DEV 环境，避免把 DEV 的授权错误发送给生产 OAuth 服务。已设置的受控 agentdock-ops Broker 仅允许在 RN DEV 修改固定的三个字段；不开放通用 root/任意配置写入。区分 A/B 账号合法授权与错误混用 DEV/生产环境。
