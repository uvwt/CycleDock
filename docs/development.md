# 开发路线

## 本地开发

需要 Go 1.26.6（见 go.mod）、Node.js 24（见 .node-version）、npm、Make 和可连接的 PostgreSQL 17。
开发入口支持 macOS / Linux，使用进程组管理前后端生命周期。

首次在仓库根目录运行：

```bash
make setup
```

该命令下载 Go 依赖、安装固定版本 Air v1.67.4 到被 Git 忽略的 bin/，并使用 npm ci 安装前端依赖。
仅在不存在时从 .env.local.example 创建 .env.local，不覆盖已有本机配置。
根据本机数据库修改 DATABASE_URL；setup 不启动或新建数据库，不执行迁移。
配置文件使用 Node.js 的 env 文件格式，由启动入口显式加载；已有进程环境变量优先。

日常启动：

```bash
make dev
```

- 先检查依赖和前后端端口，再运行 Goose migration。连接或迁移失败时不会启动开发服务器。
- 数据迁移会修改 DATABASE_URL 指向的数据库；使用专用开发库，不要指向生产或测试库。
- Go 后端默认监听 127.0.0.1:8080，Air 在后端 Go 源文件变化后重新编译并重启。
- 浏览器访问 http://127.0.0.1:5173；Vite 提供前端热更新，并将 API、SSE、OAuth/MCP 和回调请求代理到 Go。
- HTTP_ADDR 可设为其他本机 host:port；DEV_WEB_PORT 可调整前端端口。端口被占用时直接报错，不自动换端口或终止已有服务。
- Ctrl+C 停止本次启动的前后端及其子进程；任一开发进程退出也会停止另一进程。
- 修改 .env.local、go.mod、go.sum、Air 配置或数据库迁移后，重新运行 make dev；Go 依赖变化或 lockfile / Air 版本更新后重新运行 make setup。
- 配置本机 OAuth 时，CYCLEDOCK_PUBLIC_URL 使用浏览器入口（例如 http://127.0.0.1:5173）。本机密码登录无需配置。

单独执行迁移：

```bash
make migrate
```

模型 API Key 等可按需加入 .env.local；该文件已被 Git 忽略。
如需订阅模型账号或带 Token 的 MCP，分别配置 CYCLEDOCK_MODEL_SECRET_KEY 和 CYCLEDOCK_CONNECTION_ENCRYPTION_KEY，
每个密钥独立使用 `openssl rand -base64 32` 生成并持久保留。完整变量说明见 .env.example 与 install.md。
日常开发不需要设置 WEB_DIR，也不需要执行前端 build；生产构建仍按 Dockerfile 执行。

## 1. 当前目标

CycleDock 在首次公开发布前完成 Session-first / Google ADK Go v2 Runtime 重构。

不迁移旧 DEV Runtime 数据；当前只保留一套 Card → Turn → Attempt Runtime。

完整决策见 session-first-adk.md。

## 2. 开发原则

1. Card = persistent Agent Session。
2. 先把 Session persistence / Turn / Attempt 并发语义做正确，再切 Runner。
3. PostgreSQL 是唯一正式数据库；pgx + sqlc + Goose。
4. 不因为 ADK 自带 DatabaseSessionService 就引入 GORM。
5. 每阶段必须有真实 PostgreSQL 验证。
6. 不提供旧 Runtime Adapter 兼容层。
7. 首次公开发布使用已经重整完成的单一 `000001_initial.sql`。

## 3. Phase A — Session Foundation

内容：

- 锁定 Google ADK Go v2。
- Session-first 架构文档。
- Card ID = session_id。
- Workspace ID = user_id。
- PostgreSQL session.Service。
- Session state。
- Event persistence。
- Compaction metadata。
- ADK official session conformance。
- 跨 Turn tool result persistence。

当前状态：已完成并形成 Phase A 提交；真实 PostgreSQL、ADK conformance、race 与 migration round-trip 均通过。

## 4. Phase B — Turn / Attempt

删除 Trigger / AgentRun 的目标语义，建立：

- card_turns。
- turn_attempts。
- Human / Schedule / System Turn source。
- initial / retry / recovery Attempt reason。
- enqueue。
- interrupt。
- current Attempt 唯一。
- FIFO pending Turn。
- heartbeat / lease。
- waiting_human。
- retry = new Attempt。
- stale invocation write guard。

必须有真实并发测试：

- 两个 dispatcher 只能 claim 一次。
- enqueue 不污染 current Session。
- interrupt 与旧 Attempt event append 并发时旧写入失败。
- Schedule occurrence 去重。
- restart recovery 不复活旧 execution identity。

Phase B 已完成并提交，真实 PostgreSQL 并发测试、全仓 test/vet、核心 package -race 与 migration up/down/up 均通过。

## 5. Phase C — ADK Runner Cutover

Built-in Runtime cutover 已完成：LlmAgent / Runner、Card Session resume、Turn/Attempt Dispatcher、产品 SSE 状态源、Todo integration、Human input/control、Discussion metadata、Generate Work、Schedule Turn、Model Route adapter 与 MCP/NexusDock FunctionTool adapter 已进入正式执行路径。

旧执行 loop / Context Builder 已在 Phase F 删除；Built-in Agent 只有 ADK Runner 执行路径。

Phase C 的验收包括真实 PostgreSQL 跨 Turn tool-response continuity：第一 Turn 的 MCP FunctionResponse 能直接进入第二 Turn prompt，且第二 Turn 在不要求 freshness 时不重复调用只读工具。

## 6. Phase D — Context / Artifact / History

已完成：

- ADK Tail Retention Context Compaction：生产默认约 24k token 阈值、20 raw Event tail，并使用 bounded summarizer input。
- PostgreSQL ADK ArtifactService：versioned payload / MIME / metadata / Card Session scope，并通过 advisory lock 保证并发版本唯一。
- >12k MCP 结果结构化脱敏后外置 Artifact；FunctionResponse 只留 preview/ref，Agent 可通过 `cycledock_read_artifact` 分块读回。
- `cycledock_search_session_history`：从同 Card 原始 PostgreSQL Session Event 检索被 compaction 覆盖的精确旧证据。
- Thought / ThoughtSignature 丢弃、结构化 credential 递归脱敏，以及 Compaction bookkeeping ownership guard。
- 真实 PostgreSQL 验证：Artifact/History 精确值恢复、不重复真实 MCP 调用、raw Event 不被 compaction 删除、五个核心包 `-race`、migration v12 up/down/up。

Model credential、ordered routes、failover 与普通 MCP 继续通过 ADK adapter 保留；只有在 ADK 原生实现达到同等可靠性时才删除现有 transport/client。NexusDock 第一方对话运行时统一通过 MCP 获取设备上下文、工具定义并执行设备及 Cloud 自有工具，不使用 REST 目录或 Execution API。前端已通过独立认证 API 展示设备在线列表并支持可选设备偏好，偏好随 Turn 固定，见 [NexusDock 对话接入与设备展示](nexusdock-capability-directory.md)。

## 7. Phase E — External MCP / Memory

External MCP Agent cutover 已完成：

- MCP Server v0.4 使用 `create_turn / claim_attempt / heartbeat_attempt / reply_message / check_in / finish_attempt`。
- External OAuth Source 与 Built-in Agent 共用 `card_turns / turn_attempts`、Card 锁、ownership 与 stale-write guard。
- `get_card / list_cards` 已切到 `pending_turns / current_attempt / attempt_history`。
- Card-scoped Claim 保证外部客户端不会因为 Workspace FIFO 抢到别的 Card；同 Source 网络重试复用当前 Attempt。
- Human reply / Schedule Turn 可由 External Agent Claim；cross-source heartbeat/progress/finish 被拒。
- 官方 OAuth + Streamable HTTP MCP SDK E2E 与真实 PostgreSQL 并发/race 已通过。
- 旧 `create_followup / claim_work / heartbeat_run / finish_run` 不提供兼容别名。

Memory 仍只在出现跨 Card 知识召回需求后实现；Self-hosted 默认不依赖 Vertex Memory Bank，不提前引入向量数据库。

## 8. Phase F — Cleanup / Release

Cleanup 已完成：

- 删除旧执行 Runtime Store/API/query/schema 与旧 Context Builder / context_budget。
- 保留仍承担 credential、ordered failover、OpenAI-compatible / ChatGPT / Grok transport、MCP transport 的 modelclient / mcpclient；它们是 ADK adapter 基础设施，不是第二套 Agent loop。
- HTTP / MCP / Web / SSE 全面使用 Turn / Attempt 语义；SSE 使用 `attempt.*` + `attempt_id`。
- Migration 重整为单一首次发布 `000001_initial.sql`，无旧执行表/列。
- final baseline 已通过普通数据库与自定义 `search_path` 的 migration round-trip，以及 sqlc 稳定生成。

当前只剩 Release Gate 的最终全量验证。

## 9. Release Gate

发布前至少：

- go test ./...
- go vet ./...
- core packages -race
- ADK session conformance on real PostgreSQL
- Session compaction round-trip
- cross-Turn tool response continuity
- stale invocation rejection
- Turn claim concurrency
- enqueue / interrupt concurrency
- Schedule duplicate prevention
- Workspace isolation
- migration empty DB up/down/up
- Web lint/build/audit
- Docker Linux smoke
- OIDC / MCP OAuth E2E
- Human-only Done
- 文档与实现一致

## 10. Git

- main 保持稳定。
- 短期分支 + 独立 worktree。
- 当前重构分支：feat/session-first-adk-20261025。
- 不直接在主工作区改代码。
- 验证后提交。
- 不 force push 已共享分支。
