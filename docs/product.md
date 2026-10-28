# 产品定义

## 1. 一句话定位

CycleDock 是一个以 Project 和 Card 为核心的人机协作工作台。每个 Card 同时是一个持久 Agent Session：Human、Schedule 与 AI 围绕同一个工作对象持续对话和执行，CycleDock 负责业务状态、队列、权限与审核，Agent Runtime 负责连续上下文、工具调用、压缩与产物。

完整决策见 session-first-adk.md。

## 2. 用户心智

用户看到的是项目、讨论和任务，不需要理解 Session、Invocation 或 Agent Trace。

~~~text
创建 Project
→ 新建 Discussion
→ 持续讨论 / 调研 / 工具执行
→ 生成任务
→ Todo 或 Ongoing 获得自己的 Card Session
→ 多轮执行 / 追问
→ Review / Done，或继续下一次 Ongoing Cycle
~~~

Project 本身就是看板数据范围；Board 只是 UI View，不建立 Board 领域实体。

## 3. Card = 工作对象 + Agent Session

Card 只有三类。

### Discussion

用于探索、澄清和收集信息。首次发送消息时才真正创建 Card；首条输入持久化后，由后端独立异步调用模型生成标题与摘要；不依赖 Agent 工具调用，不阻塞主对话。

生命周期为 Active → Archived。

Discussion 不能转换成 Todo / Ongoing。生成任务时创建新 Card / 新 Session，并传递 Handoff Package，而不是共享 Discussion 的完整 Session History。

### Todo

用于有明确完成终点的一次性工作。

~~~text
Todo → Working → Review → Done
~~~

Agent 可以推进到 Review；Review → Done 只允许 Human。

Todo 可以来自 Discussion，也可以由 Human 在 Work 视图直接创建。Todo 自身不再派生其他 Card。

### Ongoing

用于长期维护、周期巡检和持续观察。

~~~text
Active / Paused / Archived
~~~

Ongoing 不进入 Done。Schedule 每次触发产生一个新的 Turn，但继续使用同一个 Card Session，因此近期执行上下文天然连续。

Health 由最近一次 Check-in 派生：healthy / warning / critical / unknown。

## 4. 连续对话与执行

同一个 Card 内，用户的后续消息是同一 Session 的新 Turn。

~~~text
Turn 1: 检查服务器上的容器
Agent: tool_call docker_ps
Tool: 返回 20 个容器
Agent: 汇报检查结果

Turn 2: 刚才第 8 个容器是什么镜像？
~~~

Turn 2 应能使用 Session 中已经持久化的上一轮 tool response，不应因为换了一个 Run 而重复调用工具。

如果用户问“现在第 8 个容器还是这个镜像吗？”，这是 freshness 要求，Agent 应重新查询真实环境。CycleDock 要区分“记忆缺失”与“需要最新事实”。

## 5. Turn / Attempt

Turn 是一次新的工作输入：Human message、Schedule occurrence 或 System continuation。技术 retry / recovery 是同一 Turn 的新 Attempt，不是新的业务 Turn。

Attempt 是执行某个 Turn 的一次真实 Agent invocation。

~~~text
Turn 42
├── Attempt 1 failed
└── Attempt 2 completed
~~~

技术重试不制造新的业务 Turn。

排队 Turn 在真正开始执行以前不能写入 ADK Session History，否则当前正在执行的 Attempt 可能看到未来消息。

## 6. Queue / Interrupt

当前没有 Attempt 时，发送消息立即创建并执行 Turn。

当前 Attempt 正在运行时：

- 加入队列：保存新 Turn，但暂不送进 Session Runtime。
- 立即转向：终止 / supersede 当前 Attempt，新 Turn 优先执行。

waiting-human 场景在 Session-first Runtime 中通过 Human Input / Attempt 状态表达，用户回复后继续同一 Card Session。

旧 Attempt 即使晚返回，也不得追加 Session Event 或修改 Card 当前业务状态。

## 7. Context / Artifact / Memory

CycleDock 不再要求 Agent 把所有工具结果提炼成 Finding 才能跨轮次记住。

上下文分层：

1. Recent Session Events：近期原始 user / model / tool event。
2. ADK Context Compaction：压缩较早历史。
3. Session History Retrieval：按需读取被压缩的精确旧事件。
4. Artifact：大型输出、文件、截图、日志、报告。
5. Memory：跨 Card / 跨 Session 的长期知识。

Memory 不用于替代同一 Card 的连续 Session。

## 8. Discussion 生成工作

Discussion 只有一个“生成任务”入口。Agent 判断：

- 有明确终点 → Todo。
- 长期 / 周期工作 → Ongoing。

生成时创建新的 Card Session，并传递 Handoff Package：goal、constraints、acceptance criteria、confirmed decisions、relevant findings、relevant artifact refs、source_card_id。

这样新任务继承必要知识，但不会继承 Discussion 中数百轮无关上下文。

## 9. Human Review

- Agent 可以输出结果并建议进入 Review。
- 服务端根据业务规则推进 Todo。
- Review → Done 只允许 Human。
- Human 可以重新打开已完成 Todo。
- Agent Session 历史不能绕过业务权限。

## 10. 产品不做什么

首次公开发布前不做：

- 脱离 Project / Card 的通用 Chat。
- Agent Marketplace。
- 通用 DAG / Workflow Builder。
- 面向用户的完整 raw trace 浏览器。
- Redis / Kafka / 微服务化。
- SQLite 多数据库兼容。
- 复杂 RBAC。
- 多 Board 领域模型。
- 为跨 Card Memory 过早引入向量数据库。

Session Event 是 Runtime 数据，不等于产品要把所有内部事件原样展示给 Human。

## 11. 当前重构状态

Session-first 重构正在 feat/session-first-adk-20261025 开发：

- 架构决策已确定。
- Google ADK Go v2.6 已接入。
- PostgreSQL-backed ADK SessionService 已建立并通过官方 conformance。
- Built-in 与 External MCP Agent 已完成统一 Session-first Runtime：Turn / Attempt 调度、stale-write/ownership guard、ADK Runner、Web Human input/control、Schedule Turn、Model Route/MCP Adapter、Context Compaction、Artifact/history retrieval 均已通过真实 PostgreSQL 验证。
- Phase F 已删除旧执行 Runtime / Context Builder，并把首次发布 schema 重整为单一 `000001_initial.sql`；SSE 也已统一使用 Attempt 语义。

首次公开发布不迁移旧 DEV Runtime 数据，也不保留旧执行 API 或数据库兼容层。
