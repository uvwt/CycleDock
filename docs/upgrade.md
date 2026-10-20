# 升级指南

## 1. 原则

生产升级采用 **先备份、再 migration、再切应用** 的顺序。

发布验证覆盖：

- CI clean database up 与 latest down / up。
- Phase 5 release verification 的带历史 Event / Trigger / Run 数据 v4 → v5 → v4 → v5 往返。
- CI Docker Compose 从 migration service 启动到 app ready。

生产环境仍推荐 forward-only 升级；不要把 `down` 当成常规发布回滚手段。

## 2. 升级前

1. 阅读目标版本 release notes / migration。
2. 备份 PostgreSQL，并验证备份可以恢复。
3. 保留当前镜像 digest / 二进制。
4. 保留当前部署环境变量与 secret。
5. 确认磁盘容量和数据库健康。

## 3. Docker Compose 升级

获取目标版本代码后：

```bash
docker compose build
docker compose run --rm migrate
docker compose up -d app
```

也可以在维护窗口直接：

```bash
docker compose up -d --build
```

因为 app 依赖 migrate service 成功退出，migration 失败时 app 不应被当作升级成功。

升级后至少验证：

```bash
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/healthz
docker compose ps
docker compose logs --no-color app
```

同时检查 Human 登录、Project/Card 读取和 Remote MCP metadata。

## 4. Phase 5 migration

`000005_runtime_recovery.sql` 为 Trigger / AgentRun 增加 retry / cancel / recovery 所需的生命周期字段和约束。

Up 不重写旧 Event、Trigger 或 Run 历史；旧行的新字段使用兼容默认值。

Down 会删除 Phase 5 新增的 retry/cancel metadata 列。因此：

- 开发/验证环境可以执行 down/up。
- 生产已经产生 Phase 5 cancel/retry 历史后，不建议用 down migration 回滚。
- 生产回滚优先恢复升级前数据库备份，再切回旧应用。

## 5. 应用回滚

如果新应用异常但 migration 与旧应用仍兼容，可以先停止新 app 并重新运行旧镜像。

如果不能证明 schema 向后兼容：

1. 停止写流量。
2. 恢复升级前数据库备份。
3. 重新运行旧应用镜像。
4. 验证 `/readyz`、Human 登录和关键 Card 数据。

不要在未知兼容性下直接让旧二进制写新 schema。

## 6. Built-in Agent 升级检查

升级不会把 secret 移入数据库。请确认：

- Settings 中的 `api_key_env` / `bearer_token_env` 名称仍对应部署环境。
- Provider Test 成功。
- MCP Test 成功并显示预期工具数量。
- 当前 waiting_human Run 仍保持 waiting，不会被 lease recovery 误过期。
- expired recovery 会形成新的 `system_followup` Trigger，不会复活旧 Run。

## 7. 发布验收

发布前后建议保持同一套 gate：

- `go test ./...`
- `go vet ./...`
- 核心包 `go test -race`
- Web lint / production build / production dependency audit
- migration smoke
- OAuth / MCP security tests
- cross-workspace isolation tests
- production binary smoke
- Linux Docker Compose smoke
