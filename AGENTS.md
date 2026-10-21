# CycleDock 开发约束

## 产品定位

CycleDock 是人与 AI Agent 共用的任务看板。产品核心是 Project / Card / Trigger / Run，而不是 Conversation / Session。

任何新功能都先判断是否直接服务以下闭环：

```text
Project Board
→ Discussion
→ 对话 / 澄清
→ Generate work（Todo / Ongoing）
→ Human / Schedule 产生 Trigger
→ Claim
→ Run
→ Progress / Finding / Result / Check-in
→ Human Review 或下一轮 Cycle
```

不能直接服务该闭环的功能默认不进入第一版。

## 领域硬约束

- Project 即一个项目看板；不要重新引入 Board 表或 Board 领域实体。
- Card 只有 Discussion / Todo / Ongoing 三类；不要重新引入 Normal Card 抽象。
- Discussion 是新工作的会话入口，生命周期只有 Active / Archived；不能转换成 Todo / Ongoing。
- 只有 Discussion 可以通过单一 Generate work 动作派生 Todo / Ongoing；AI 根据有限任务或长期/周期事项判断类型；Todo / Ongoing 不再派生其他 Card。
- Todo 状态机固定为 Todo → Working → Review → Done；Review → Done 只允许 Human。
- Discussion 首轮会话负责生成标题与摘要；归档后禁止继续产生 Trigger/Run，删除 Discussion 不级联删除其派生工作。
- Card 保存业务状态，不保存 Agent 当前运行状态。
- Trigger 是不可变工作方向；不要通过修改旧 Trigger 表达“换方向”。
- 创建 AgentRun 即 Claim Trigger；不要增加 Claims 表。
- 一个 Trigger 最多对应一个 AgentRun。
- 一个 Card 同时最多有一个 current Run，current 状态包括 queued / running / waiting_human。
- waiting_human 占用 current Run 槽位；用户回复时由新 Trigger 接续，旧 Run 进入 superseded。
- enqueue 不打断当前 Run；interrupt 先 supersede 当前 Run，再优先启动新 Trigger。
- 旧 Run 在 superseded 后即使返回，也不得修改 Card 当前业务状态。
- Card 保存长期任务知识；Run 的原始模型消息、工具参数、工具结果只属于当前执行，不自动持久化和继承。
- 有长期价值的信息必须提升为 Comment / Finding / Progress / Result / Check-in 等 Event。
- Ongoing health 由最新 Check-in 派生，不维护第二套可写 health 字段。
- Ongoing Schedule 只负责生成 Trigger，不直接执行 Agent。
- 定时触发默认不打断当前 Run。
- AI 可以推动工作进入 Review，但最终 Done 由 Human 决定。

## 工程原则

- 长期可维护性优先，不做补丁式设计。
- Go 代码保持直接、可读，避免 Java 式机械分层。
- 不提前定义无真实多实现需求的 interface。
- 不为了测试污染生产 API。
- 动态 JSON 只停留在 HTTP / MCP 等边界，进入领域逻辑前转换为明确类型。
- 关键状态变化必须由数据库约束和事务兜底，不能依赖前端约定或“先查再写”。
- 多租户 scope 必须来自认证上下文，不能信任客户端传入 workspace_id / source_id。
- 数据库、HTTP、MCP、内置 Agent 不允许各自复制一套状态机。
- 不保存 chain-of-thought。
- 不把 raw tool trace 当成产品数据模型。

## Git

- 稳定分支为 `main`。
- 除仓库首次初始化外，默认使用短期分支 + 独立 Git worktree 开发，不直接在主工作区修改代码。
- 提交格式：`type(scope): 中文说明`。
- 修改前确认现状，修改后必须真实验证。
- 不 force push 已共享分支。
- 合入前确认工作区干净、测试/文档通过、远端状态符合预期。

## 数据库

- PostgreSQL 是唯一正式数据库。
- 使用 pgx + sqlc + Goose。
- 第一版从 `000001_initial.sql` 开始，不兼容旧 CycleDock 开发数据库。
- Tenant/Workspace scope 应尽量由数据库 FK、唯一约束和查询条件共同保证。
- 并发 Claim、current Run 唯一性、Schedule 去重必须由 PostgreSQL 约束参与保证。

## 文档

- 产品规则变化必须同步更新对应 docs。
- `docs/data-model.md` 是领域数据结构基线。
- `docs/agent-runtime.md` 是 Trigger / Queue / Run / Context 行为基线。
- `docs/scheduling.md` 是 Ongoing 定时行为基线。
- `docs/stack.md` 是依赖选型基线。
- 尚未真正实现的 MCP 工具或 API 不要写成已经可用。

## 旧代码参考

旧 CycleDock 与 ChatDock 只能按能力选择性参考，不允许整体迁移。

- 旧 CycleDock 可参考 OIDC、OAuth、MCP Server、PostgreSQL/sqlc、Web Session、前端交互和多租户实现。
- ChatDock 可参考 OpenAI-compatible Model Client、MCP Client、tool schema、streaming、timeout 和 provider error handling。
- 不迁移 ChatDock 的 SQLite、Session/Message、Prompt Space、完整聊天历史、raw tool trace、旧 Scheduler 数据模型。
