# CycleDock 开发约束

## 产品定位

CycleDock 是人与 AI Agent 共用的项目工作台。产品核心仍是 Project / Card，但 Agent Runtime 采用 **Session-first**：一个 Card 就是一个持久 Agent Session。

权威架构基线见 `docs/session-first-adk.md`。

核心闭环：

```text
Project
→ Card (persistent Agent Session)
→ Turn
→ Attempt / ADK invocation
→ Session Events / Tools / Artifacts
→ Review / 下一轮 Turn / 下一次 Schedule
```

## 领域硬约束

- Project 本身就是项目看板，不重新引入 Board 表。
- Card 只有 Discussion / Todo / Ongoing 三类。
- Card ID 直接作为 Agent Session ID；不要增加一对一 CardSession 领域实体。
- ADK `user_id` 使用 Workspace ID；Session 不属于某一个 Human。
- Discussion 是探索/澄清型 Session；只能派生 Todo / Ongoing，不能转换成 Todo / Ongoing。
- Discussion 派生工作时创建新 Card / 新 Session，只传 Handoff Package，不共享完整 Session。
- Todo 状态固定为 Todo → Working → Review → Done；Review → Done 只允许 Human。
- Ongoing 生命周期为 Active / Paused / Archived；Schedule 的每次执行进入同一 Card Session。
- Turn 表示一次新的工作输入；排队 Turn 在真正 Claim 前不得 append 到 ADK Session。
- Attempt 表示一次真实 invocation；技术重试创建新 Attempt，不制造新的业务 Turn。
- 一个 Card 同时最多一个 current Attempt。
- enqueue 不打断 current Attempt；interrupt 终止/替代 current Attempt 后优先执行新 Turn。
- superseded/cancelled Attempt 的迟到 Event 不得污染 Session 或 Card 状态。
- Session Event 可以持久保存 tool call / tool response；不再要求 raw tool context 只能存在于单次 Run 内存。
- 长 Session 通过 ADK Context Compaction + history retrieval + Artifact 控制上下文，不通过丢弃跨 Turn 工具证据解决。
- 不保存 chain-of-thought、明文 credential、Cookie、access token 或无必要 secret。
- Card 保存业务状态；Attempt 保存执行状态；ADK Session 保存 Agent 连续上下文，三者职责不得混淆。
- AI 可以推进 Todo 到 Review，最终 Done 由 Human 决定。

## Agent Runtime

- Google ADK Go v2 是 Built-in Agent Runtime 基础。
- CycleDock 实现自己的 ADK `session.Service`，使用 PostgreSQL + pgx/sqlc/Goose；禁止为 ADK Session 引入 GORM AutoMigrate。
- 普通 MCP 优先使用 ADK McpToolset；NexusDock first-party 授权/bootstrap 仍由 CycleDock 管理。
- Model credentials、ordered routes、多账号和 failover policy 属于 CycleDock；不要把产品可靠性完全绑定到 ADK experimental model routing。
- Memory 用于跨 Card 知识；同一 Card 连续上下文首先依赖 Session，不要过早引入向量数据库。

## 工程原则

- 长期可维护性优先，不做补丁式设计。
- 本分支不兼容旧 Trigger / AgentRun / Context Builder 数据语义；首次公开发布前允许重整 `000001_initial.sql`。
- 开发过程中允许按可验证阶段切换，但最终不得保留两套并行 Runtime。
- Go 代码保持直接、可读，避免 Java 式机械分层。
- 不提前定义无真实多实现需求的 interface；ADK 官方接口等外部契约除外。
- 动态 JSON 只停留在明确边界；业务状态使用明确类型。
- 关键状态变化必须由数据库约束和事务兜底。
- 多租户 scope 必须来自认证上下文。
- 数据库、HTTP、MCP、Built-in Agent 不允许复制平行状态机。
- 不保存 chain-of-thought。

## Git

- 稳定分支为 `main`。
- 默认使用短期分支 + 独立 Git worktree，不直接修改主工作区。
- 提交格式：`type(scope): 中文说明`。
- 修改前确认现状，修改后必须真实验证。
- 不 force push 已共享分支。

## 数据库

- PostgreSQL 是唯一正式数据库。
- 使用 pgx + sqlc + Goose。
- 首次公开发布前不兼容旧 DEV 数据，可重写 initial migration。
- Session、Turn、Attempt、Workspace/Card scope 必须由数据库约束和事务共同保证。
- current Attempt 唯一性、Turn FIFO、Schedule 去重、stale invocation 写保护必须有真实 PostgreSQL 测试。

## 文档

- `docs/session-first-adk.md`：Session-first / ADK 总体决策。
- `docs/data-model.md`：目标数据结构。
- `docs/agent-runtime.md`：Session / Turn / Attempt / Context 行为。
- `docs/scheduling.md`：Ongoing 调度。
- `docs/stack.md`：依赖选型。
- 尚未实现的能力必须明确标为目标态，不写成已经可用。
