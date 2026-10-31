# Self-host 安装

## 1. 适用范围

默认发布形态是单个 CycleDock 应用 + PostgreSQL 17。仓库中的 `docker-compose.yml` 会依次启动：

1. PostgreSQL。
2. 一次性 migration service。
3. CycleDock app。

生产环境必须由 HTTPS 反向代理暴露应用；Compose 默认只用于单机部署基线。

## 2. 前置条件

- Docker Engine。
- Docker Compose v2。
- 可持久化 Docker volume 的磁盘。
- 一个随机且至少 12 个字符的 Administrator password，或完整的 Hosted OIDC 配置。

## 3. 环境变量

复制 `.env.example` 为部署环境的 `.env`，至少配置一种 Human 登录方式。

Self-hosted Administrator：

```env
CYCLEDOCK_ADMIN_PASSWORD=<strong-random-password>
```

要启用 Remote MCP OAuth，再配置外部可访问的 HTTPS URL：

```env
CYCLEDOCK_PUBLIC_URL=https://cycledock.example.com
```

Hosted NexusDock OIDC 需要同时设置：

```env
CYCLEDOCK_PUBLIC_URL=https://cycledock.example.com
CYCLEDOCK_OIDC_ISSUER=https://<issuer>
CYCLEDOCK_OIDC_CLIENT_ID=<client-id>
```

Built-in Cycle Agent 的**模型 API Key** 仍由进程环境注入。默认 Compose 转发常用变量：

```env
CYCLEDOCK_MODEL_API_KEY=<secret>
```

模型设置保存的是环境变量**名称**。普通 MCP 设置直接填写真实 Bearer Token，由后端加密写入 PostgreSQL，不再依赖单独注入 MCP Token 环境变量；无 Token 的 MCP 连接可以直接配置。

若要保存带 Bearer Token 的普通 MCP 连接，在服务端配置固定的 `CYCLEDOCK_CONNECTION_ENCRYPTION_KEY`（32 字节随机值，base64 或 hex），并确保所有副本使用同一密钥。该密钥仅由服务端读取，不能写入 PostgreSQL、前端或 Git。

要允许 Workspace 连接 ChatGPT / Grok subscription account，还必须配置固定部署主密钥 CYCLEDOCK_MODEL_SECRET_KEY。该值应由 32-byte 随机数据做 base64 编码，例如可用 openssl rand -base64 32 生成。

该 key 用于 AES-256-GCM 加密 OAuth access / refresh token。生产部署必须把它与数据库备份一起安全保存；丢失后现有 subscription account connection 无法解密，需要用户重新登录。

ChatGPT Plan 的远程 / Hosted 正式接入需要 OpenAI 批准的 CYCLEDOCK_CHATGPT_CLIENT_ID。仅 loopback Self-hosted 开发默认可以留空并走 OpenAI dynamic_agent_client 流程。

如果只是小规模 Hosted DEV 验证，可临时设置 CYCLEDOCK_CHATGPT_ALLOW_HOSTED_DYNAMIC_REGISTRATION=true。这是明确的测试逃生口，默认关闭，不应带入生产。开启后 CycleDock 使用 `http://localhost:1455/auth/callback` 作为 OpenAI dynamic client 的 loopback redirect：完成 OpenAI 授权后，localhost 页面可能显示无法连接；把浏览器地址栏里的完整 callback URL 粘回 CycleDock 即可由服务端完成 PKCE token exchange。这个流程不依赖本地模型 CLI/runtime。取得正式 CYCLEDOCK_CHATGPT_CLIENT_ID 后必须删除/关闭。未配置正式 Client ID 且未启用临时开关时，ChatGPT Plan 入口会隐藏。

Grok device-code 使用 xAI Grok Build 的公开 OAuth client，不需要本地 Grok CLI。

关闭页面后仍接收卡片通知时，配置一对 Web Push VAPID 密钥。留空则只在网页打开时通知。Subject 填 `mailto:` 邮箱；留空且 `CYCLEDOCK_PUBLIC_URL` 为 https 时，用该 URL 作为 Subject。iPhone 和 iPad 还要用户把站点添加到主屏幕，并用那个图标打开后才能订阅。

```env
CYCLEDOCK_VAPID_PUBLIC_KEY=<base64url-public-key>
CYCLEDOCK_VAPID_PRIVATE_KEY=<base64url-private-key>
CYCLEDOCK_VAPID_SUBJECT=mailto:ops@example.com
```

## 4. 启动

```bash
docker compose up -d --build
```

Compose 会等待 PostgreSQL ready，先执行全部 Goose migrations，再启动 app。

检查：

```bash
docker compose ps
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/healthz
```

`/readyz` 用于确认数据库可用并且应用可以接收业务请求；`/healthz` 用于进程存活检查。

## 5. HTTPS / Cookie

推荐让 TLS 在可信反向代理终止，并把 `CYCLEDOCK_PUBLIC_URL` 设置为真实 HTTPS 地址。HTTPS Public URL 会自动启用 Secure Cookie。

如果 Self-hosted 不配置 Public URL，但外层反向代理实际使用 HTTPS，可设置：

```env
CYCLEDOCK_SECURE_COOKIE=true
```

不要用该变量尝试把 HTTPS Public URL 降级为非 Secure Cookie。

## 6. 首次验证

Human Web：

1. 打开 CycleDock。
2. 使用 Administrator password 或 NexusDock OIDC 登录。
3. 创建 Project / Card。
4. 确认 Card Detail 可以加载 Timeline 与 Queue。

Remote MCP（配置 Public URL 后）：

- `/.well-known/oauth-protected-resource/mcp` 应返回 metadata。
- 未授权请求 `/mcp` 应返回 401。
- 外部 Client 应按 `docs/ai-connect.md` 完成 OAuth。

Built-in Agent：

1. Workspace owner 打开 Agent Settings。
2. 在模型服务中连接一个或多个 ChatGPT / Grok 账号；同一 Provider 可以连接多个账号。
3. 添加 Model Route，并为每条 Route 选择 provider、model，以及订阅账号或 API endpoint/env。
4. 按希望的故障转移优先级排序 Route；不参与路由的条目可以单独禁用。
5. 对每条 Route 使用 **Test** 做最小模型请求。
6. MCP connection 使用 **Test**；测试只做 initialize + ListTools，不执行 Tool。
7. 至少保留一条启用 Route，再启用 Cycle Agent。

## 7. 备份

至少备份 PostgreSQL volume / database。升级前必须先做可恢复备份。

CycleDock 不把 Model API Key / MCP Bearer Token 明文存入数据库。备份时须分别保留模型的环境配置与 MCP 加密主密钥；只恢复 PostgreSQL 而丢失密钥将无法使用原有 MCP Token。

ChatGPT / Grok OAuth credential 是加密后的业务配置；备份数据库时还必须安全备份 CYCLEDOCK_MODEL_SECRET_KEY。

不要把临时测试数据库、raw model/tool trace 或日志当作业务备份。

### Hosted NexusDock 远程设备连接

Hosted 部署若要启用 Agent Connections 中的 NexusDock 第一方设备能力，额外配置：

```env
CYCLEDOCK_NEXUSDOCK_MCP_URL=https://mcp.nexusdock.co/mcp
CYCLEDOCK_NEXUSDOCK_WEB_URL=https://www.nexusdock.co
CYCLEDOCK_CONNECTION_ENCRYPTION_KEY=<32-byte random key, base64>
```

`CYCLEDOCK_CONNECTION_ENCRYPTION_KEY` 用于服务端加密第一方 OAuth token 和普通 MCP Bearer Token，不发送给浏览器或 NexusDock。变更该 key 前必须先完成密钥迁移；直接替换会让已有连接无法解密，需要重新授权或重新输入 Token。
