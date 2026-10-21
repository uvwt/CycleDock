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
- Phase 5 Product Polish / Release Preparation：已实现运行恢复、retry/cancel、连接测试、长历史治理、基础 i18n/mobile/accessibility 与发布前强化验证。

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

- Discussion Active / Archived 生命周期与 Human-only Archive/Delete。
- Discussion → Todo / Ongoing 的 AI 派生约束（不是状态转换）；Human Web 另允许在 Board 直接创建无 source Discussion 的 Todo。
- Todo → Working → Review → Done 状态机。
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
- provider type / model / timeout / max tool rounds / Workspace Agent instructions 持久化到 PostgreSQL。
- Model API Key 与 MCP Bearer Token 不写数据库；配置只保存 `api_key_env` / `bearer_token_env`，运行时从进程环境解析。
- Dispatcher 同时负责 Schedule / lease maintenance，并只自动 Claim 启用了 Built-in Agent 的 Workspace。
- queued Run（例如 interrupt / waiting_human reply）优先恢复；普通 pending Trigger 按 sequence 原子 Claim。
- Context Builder 每个 Run 从最新 Project / Card / bounded meaningful Timeline / 当前 Trigger 重建上下文。
- OpenAI-compatible Client 使用非流式 `/chat/completions` tool-call loop，具备 timeout 与响应体上限。
- ChatGPT Plan 使用官方 Sign in with ChatGPT + public Responses API；Grok Subscription 使用 xAI OAuth + Grok inference proxy；两者都不依赖本地 CLI/runtime。
- subscription transport 使用 store=false / stream=true Responses SSE，并把 current Run 的 tool-call loop 映射为 function call items。
- outbound MCP Client 使用官方 MCP Go SDK Streamable HTTP；Bearer 只发送到配置 origin，跨 origin redirect 被拒绝，响应有大小上限。
- raw model messages / tool args / raw MCP results 只存在于当前 Run 内存；只有 Finding / Result / Check-in / meaningful Progress 进入 Timeline。
- waiting_human 停止本 Run；Human reply 创建新 Trigger / Run。superseded Run 的迟到模型返回无法写 Result。
- 两 Dispatcher 并发、Context 边界、waiting_human、late model response、Model/MCP HTTP 协议均有 integration tests。

## 7. Phase 5 — Product Polish

- expired queued/running lease 不复活旧 Run；Built-in Agent 通过新的 `system_followup` Trigger 恢复。
- Human cancel pending Trigger / current Run；failed / expired / cancelled work 通过新的 `manual_retry` Trigger retry。
- Provider / MCP connection test action 与状态反馈；测试不执行任何 MCP Tool。
- provider timeout、malformed response、MCP initialize/tool transport failure、max tool rounds 都进入明确 failed Run。
- API Key / MCP token 继续使用部署环境注入；需要跨 Run refresh 的 subscription OAuth token 已使用独立 encrypted credential storage。
- 基础中英 i18n、mobile Board、loading/error/empty states、键盘拖拽、focus-visible、reduced-motion。
- 长历史 context 使用 bounded recent Timeline；窗口外 durable facts 原样保留，旧 Comment / Progress 只形成派生 compaction。
- DST gap / overlap 有明确 occurrence 语义。
- migration 带数据 up/down/up、production binary、race、OAuth/MCP security 与 cross-workspace isolation 均纳入发布验证。

Human Web 的实时更新 SSE 目前没有加入；Responses Provider 的 SSE 仅是上游模型 transport，不新增产品侧实时通道。

只在真实需求出现后考虑 Card Artifact。

## 8. Release Gate

发布前必须：

- 全量 Go test / vet。
- 核心 Store / Agent / OAuth / HTTP `-race`。
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
- `npm audit --omit=dev --audit-level=high`。
- release flags 构建的 production binary 真实启动 smoke。

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
