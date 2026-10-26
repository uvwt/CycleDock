# 升级指南

## 1. 首次公开发布前

当前 Session-first 重构发生在首次公开发布之前，因此**不提供旧 DEV Runtime 数据升级路径**。

仓库已经重整为单一首次发布 baseline：

- 正式 migration 只有 `db/migrations/000001_initial.sql`。
- 执行模型只有 Card → Turn → Attempt。
- 不存在旧执行兼容表、兼容 API 或旧 MCP 工具别名。
- 开发环境中的旧数据库应重新创建；不要尝试把旧 DEV Runtime 数据迁入 final baseline。

final baseline 已验证：

- clean database `up`。
- `down-to 0 → up`。
- PostgreSQL 自定义 `search_path`。
- 连续 `sqlc generate` 稳定。

## 2. 首次公开发布后的升级原则

首次公开发布后，数据库升级采用 **先备份、再 migration、再切应用**：

1. 阅读目标版本 release notes / migration。
2. 备份 PostgreSQL，并验证备份可以恢复。
3. 保留当前镜像 digest / 二进制和部署环境变量。
4. 执行 forward migration。
5. 启动新应用并验证 readiness、登录、Card、Agent Runtime 与 MCP。

生产环境默认 forward-only。不要把 `down` 当作常规生产回滚机制。

## 3. Docker Compose 升级

```bash
docker compose build
docker compose run --rm migrate
docker compose up -d app
```

也可以在维护窗口：

```bash
docker compose up -d --build
```

app 依赖 migrate service 成功退出；migration 失败时不能把应用启动视为升级成功。

升级后至少验证：

```bash
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/healthz
docker compose ps
docker compose logs --no-color app
```

同时检查 Human 登录、Project/Card 读取、Built-in Agent、Remote MCP metadata 与 OAuth MCP tool discovery。

## 4. 应用回滚

如果新应用异常但已经执行了 schema migration，不要假设旧二进制可以安全写入新 schema。

生产回滚优先：

1. 停止写流量。
2. 恢复升级前数据库备份。
3. 切回旧应用镜像。
4. 验证 `/readyz`、Human 登录、关键 Card 数据与 MCP。

只有明确证明 schema 向后兼容时，才允许跳过数据库恢复。

## 5. Built-in Agent 升级检查

升级不会把 secret 移入普通 Session/Event 数据。请确认：

- 模型 `api_key_env` 名称仍对应部署环境。普通 MCP 已由 `bearer_token_env` 迁移到加密存储：旧记录只保存环境变量名，**不会自动读取原有环境变量并迁移密钥**；升级后请在前端重新填写真实 Bearer Token。
- 配置并安全备份固定的 `CYCLEDOCK_CONNECTION_ENCRYPTION_KEY`，否则无法保存或使用带 Token 的普通 MCP 连接。已配置第一方 NexusDock 时可沿用同一个部署密钥。
- Provider Test 成功。
- MCP Test 成功并显示预期工具数量。
- waiting_human Attempt 仍保持 waiting，不会被 lease recovery 误过期。
- retry / recovery 创建新的 Attempt，不复活旧 execution identity。
- Context Compaction、Artifact 与 Session history retrieval 正常。

## 6. 发布 Gate

发布前后保持同一套验证：

- `go test ./...`
- `go vet ./...`
- 核心包 `go test -race`
- Web lint / production build
- migration fresh / round-trip smoke
- OAuth / MCP security 与官方 SDK E2E
- cross-workspace isolation
- production binary smoke
- Linux Docker Compose smoke
