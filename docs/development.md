# 开发路线

## 1. 总体策略

这是一个全新项目，不在旧 CycleDock 上做 v2 patch。

旧 CycleDock 与 ChatDock 只作为参考实现。

第一原则：

> 先把数据模型与并发规则做正确，再做 Agent Runtime 和 UI 丰富度。

当前实现进度：

- Phase 0 Foundation：已建立工程、数据库、Web、Docker Compose、CI 与 migration 基线。
- Phase 1 Work Core：已实现核心 Domain / Store，并以真实 PostgreSQL integration tests 覆盖关键并发一致性。
- Phase 2 Human Web：已实现 Human 身份、NexusDock OIDC / Administrator fallback、Web Session、HTTP API 与 Project/Card Web 工作台。
- Phase 3 External MCP：已实现标准 Streamable HTTP MCP、OAuth metadata/DCR/S256 PKCE/token rotation、Workspace approval、统一 Store 工具面、官方 Skill 与 AI Connect。
- Phase 4 Built-in Cycle Agent：已实现 Workspace Agent settings、Dispatcher、Runner、Context Builder、OpenAI-compatible Model Client 与 outbound MCP Client。
- 下一阶段：Phase 5 Product Polish。

## 2. Phase 0 — Foundation

目标：得到干净、可运行、可测试的工程骨架。

内容：

- Go module。
- chi server。
- React + TypeScript + Vite。
- PostgreSQL。
- pgx + sqlc。
- Goose。
- Docker Compose。
- CI。
- `000001_initial.sql`。
- `/healthz`。

完成条件：

- 空 PostgreSQL 可以 migration up。
- migration 可 down/up 验证。
- Go tests / vet 通过。
- Web build / lint 通过。
- Docker Compose 真启动。

## 3. Phase 1 — Work Core

实现：

- Workspace。
- Project。
- Card。
- Event。
- Trigger。
- AgentRun。
- CardSchedule。

重点领域行为：

- Normal 状态机。
- Ongoing 状态机。
- Comment → Trigger。
- enqueue。
- interrupt / supersede。
- Claim。
- one-current-run-per-card。
- FIFO pending Trigger。
- waiting_human。
- heartbeat / lease expiry。
- stale result protection。
- Ongoing health 派生。
- Schedule → Trigger。
- missed schedule skip。
- Human-only Done。

必须使用真实 PostgreSQL integration tests。

重点并发测试：

- 两个 Agent 同时 Claim 同 Trigger，只能一个成功。
- 两个请求同时创建 current Run，只能一个成功。
- interrupt 与旧 Run finish 并发时，旧 Run 不能覆盖新方向。
- Scheduler 重复扫描同一 occurrence，只生成一个 Trigger。

## 4. Phase 2 — Human Web

实现：

- Hosted NexusDock OIDC。
- Self-hosted Administrator fallback。
- Web Session。
- Projects。
- Project Board。
- Card create/edit/move。
- Card Detail。
- Timeline。
- Comment。
- Queue UI。
- “加入队列 / 立即转向”。
- waiting_human “回复并继续”。
- Human Review / Done。
- Ongoing Schedule UI。
- Health / Last Check-in / Next Run。

此阶段即使没有内置 Agent，也应能作为完整人工任务看板运行。

## 5. Phase 3 — External MCP

实现：

- Remote Streamable HTTP MCP。
- OAuth metadata。
- DCR。
- Authorization Code + S256 PKCE。
- Access / Refresh token。
- Workspace approval。
- MCP tools。
- Claim / Progress / Finish。
- Confirm。
- 幂等与并发。
- 官方 CycleDock Skill。
- AI connect docs。

完成条件：

- 官方 MCP Go SDK 通过真实 HTTP 完成 Protected Resource Metadata、DCR、OAuth、Streamable HTTP Connect / ListTools。
- 真正调用 list/create/followup/claim/progress/finish/get。
- 并发 Claim 与 token rotation 验证。

## 6. Phase 4 — Built-in Cycle Agent

从 ChatDock 选择性参考：

- OpenAI-compatible Model Client。
- MCP Client。

新增：

- Workspace Cycle Agent settings。
- Agent Dispatcher。
- Agent Runner。
- Context Builder。
- Heartbeat。
- waiting_human。
- Run lifecycle。

完整链路：

```text
Human Comment
→ Trigger
→ Queue / Interrupt
→ Run
→ Context Builder
→ Model
→ MCP Tools
→ Finding / Progress / Result
→ Review
```

必须验证：

- 新 Run 不继承旧 Run raw tool context。
- 新 Run 能看到旧 Finding / Result。
- 排队 Trigger 开始时读取最新 Card Context。
- superseded Run 晚返回不污染当前 Card。

当前实现：

- 每个 Workspace 最多一条专用 `cycle_agent` Source，只有 Workspace owner 可以读取/修改 Built-in Agent settings。
- provider / model / timeout / max tool rounds / Workspace Agent instructions 持久化到 PostgreSQL。
- Model API Key 与 MCP Bearer Token 不写数据库；配置只保存 `api_key_env` / `bearer_token_env`，运行时从进程环境解析。
- Dispatcher 同时负责 Schedule / lease maintenance，并只自动 Claim 启用了 Built-in Agent 的 Workspace。
- queued Run（例如 interrupt / waiting_human reply）优先恢复；普通 pending Trigger 按 sequence 原子 Claim。
- Context Builder 每个 Run 从最新 Project / Card / bounded meaningful Timeline / 当前 Trigger 重建上下文。
- OpenAI-compatible Client 使用非流式 `/chat/completions` tool-call loop，具备 timeout 与响应体上限。
- outbound MCP Client 使用官方 MCP Go SDK Streamable HTTP；Bearer 只发送到配置 origin，跨 origin redirect 被拒绝，响应有大小上限。
- raw model messages / tool args / raw MCP results 只存在于当前 Run 内存；只有 Finding / Result / Check-in / meaningful Progress 进入 Timeline。
- waiting_human 停止本 Run；Human reply 创建新 Trigger / Run。superseded Run 的迟到模型返回无法写 Result。
- 两 Dispatcher 并发、Context 边界、waiting_human、late model response、Model/MCP HTTP 协议均有 integration tests。

## 7. Phase 5 — Product Polish

- better failure recovery。
- retry / cancel queue。
- Provider / MCP connection test action 与状态反馈。
- 如真实部署需要在数据库内托管 secret，再设计独立 encrypted secret storage；现阶段继续使用部署环境注入。
- i18n。
- mobile。
- empty states。
- accessibility。
- SSE（如果 polling 体验不足）。
- 长历史 context summary。

只在真实需求出现后考虑 Card Artifact。

## 8. Phase 6 — Release

发布前必须：

- 全量 Go test / vet。
- sqlc regenerate clean。
- Web build / lint / audit。
- migration empty DB smoke。
- Docker linux/amd64 smoke。
- OIDC E2E。
- MCP OAuth E2E。
- 两 Agent 并发 Claim。
- interrupt / supersede race。
- heartbeat timeout。
- Schedule duplicate prevention。
- Schedule timezone / DST 测试。
- Human-only Done 权限测试。
- 跨 Workspace 访问隔离测试。
- 文档与实际工具一致。

## 9. Git 工作方式

仓库首次初始化提交后：

- `main` 只保持稳定。
- 每一阶段使用独立分支 + worktree。
- 尽量按稳定能力形成小而完整的提交。
- 完成真实验证后再推送 / PR。
- 不在主工作区直接开发。

## 10. 第一阶段明确不做

- 兼容旧 CycleDock migration。
- 数据迁移旧 DEV 测试数据。
- 独立 Board 表。
- 通用聊天 Session。
- raw Agent trace。
- Agent Marketplace。
- 多 Agent DAG。
- Workflow Builder。
- Redis / MQ / microservices。
- SQLite。
- 复杂 RBAC。
- 自定义 Cron。
- 向量数据库。
