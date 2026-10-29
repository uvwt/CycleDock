# 开发路线

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

Model credential、ordered routes、failover 与普通 MCP/NexusDock 继续通过 ADK adapter 保留；只有在 ADK 原生实现达到同等可靠性时才删除现有 transport/client，不为了框架纯度重写成熟能力。

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
