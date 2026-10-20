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

Built-in Cycle Agent 的 secret 值由进程环境注入。默认 Compose 已转发两个常用变量：

```env
CYCLEDOCK_MODEL_API_KEY=<secret>
CYCLEDOCK_MCP_TOKEN=<secret>
```

Settings 页面保存的是环境变量**名称**，不是 secret 明文。更多 Provider / MCP connection 使用其他 env 名称时，需要在部署层显式把对应变量传入 app container。

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
2. 配置 Provider endpoint、model 与 API key env 名称。
3. 使用 **Test provider** 做最小模型请求。
4. MCP connection 使用 **Test**；测试只做 initialize + ListTools，不执行 Tool。
5. 测试通过后再启用 Agent。

## 7. 备份

至少备份 PostgreSQL volume / database。升级前必须先做可恢复备份。

CycleDock 不把 Model API Key / MCP Bearer Token 明文存入数据库，因此同时要保留部署侧 secret 配置。

不要把临时测试数据库、raw model/tool trace 或日志当作业务备份。
