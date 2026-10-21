# 产品定义

## 1. 一句话定位

CycleDock 是一个以项目看板和任务为核心的人机协作系统：人类和 AI Agent 围绕同一个 Card 持续推进工作，CycleDock 负责保存任务状态、长期上下文、执行队列、进度、结果与 Review。

## 2. 用户心智

用户不需要先创建 Workspace、Project、Board 三层容器。

核心路径保持简单：

```text
创建 Project
→ 新建 Discussion
→ 在 Discussion 中持续讨论、澄清和收集信息
→ “生成任务”
→ AI 根据语义生成 Todo 或 Ongoing
→ Todo 执行 / Review / Done，或 Ongoing 持续运行
```

Human 也可以在 Project Board 直接填写并创建 Todo。直接创建用于用户已经知道“要做什么”的场景，不要求先建 Discussion，也不调用模型；AI 生成 Todo / Ongoing 的入口仍然只存在于 Discussion。

Project 本身就是一张项目看板。Board 只作为 UI 视图概念存在，不作为数据库实体。

未来如果需要 Kanban、List、My Tasks、Review、Critical Ongoing 等不同展示方式，应优先设计 View / Saved View，让不同 View 读取同一批 Card，而不是在 Project 下创建多个 Board。

## 3. Card 类型

### 3.1 Discussion Card

Discussion 是所有新工作的默认入口，也是一个真正的 AI 会话对象。它负责“把事情想清楚”，不属于 Todo 的状态机。

新建 Discussion 时不要求用户填写标题或描述；第一次会话运行时由 Built-in Agent 根据首轮上下文自动生成标题与描述。生成前 UI 使用“New discussion / 新讨论”作为展示占位，不把占位文本写成业务标题。

Discussion 生命周期：

```text
Active → Archived
```

规则：

- Discussion 永远不能通过状态转换变成 Todo 或 Ongoing。
- Discussion 不能进入 Working / Review / Done。
- Discussion 可以继续对话、归档或由 Human 删除。
- Discussion 是唯一允许“生成任务”的 Card 类型。
- 一个 Discussion 可以生成多个后续工作。

### 3.2 Todo Card

Todo 用于有明确完成终点的一次性工作。

固定业务状态：

```text
Todo → Working → Review → Done
```

规则：

- Todo 可以由 Discussion 的“生成任务”动作派生，也可以由 Human 在 Board 直接创建；两者都不是 Discussion 状态转换。
- Human 直接创建的 Todo 不设置 `source_discussion_id`，标题与描述以用户输入为准，不调用模型生成。
- Todo 自身不能再生成 Todo 或 Ongoing，避免任务树无限分叉。
- Agent 可以开始执行并推进到 Working。
- Agent 完成验证后通过 Confirm 进入 Review。
- Review → Done 只允许 Human。

### 3.3 Ongoing Card

用于长期持续事项，例如：

- 每日服务器健康巡检。
- 每周备份检查。
- 每月账单或资源检查。
- 长期 SEO / 内容巡检。
- 持续跟踪某个系统状态。

生命周期：

```text
Active / Paused / Archived
```

Ongoing Card 不进入 Done。

Ongoing 与 Todo 一样由 Discussion 派生。用户不需要在“生成任务”前先选择类型；AI 根据 Discussion 上下文判断：

- 有明确完成终点 → Todo。
- 长期维护、周期执行、持续观察 → Ongoing。

如果是 Ongoing，Agent 可以根据上下文给出并创建合适的 Schedule；Schedule 仍遵守现有 daily / weekly / monthly + IANA timezone 规则。

Health 不作为独立可写状态，而由最近一次 Check-in 派生：

- healthy
- warning
- critical
- 没有 Check-in 时为 unknown

## 4. Discussion 与工作详情

Discussion Detail 是会话界面；Todo / Ongoing Detail 是工作执行界面。它们都继续使用 Card + Event + Trigger + Run 基础设施，不建立另一套通用 Conversation / Session 数据模型。

人与 Agent 的交互仍直接发生在 Card Detail：

- 标题和描述
- 当前业务状态
- 当前 Run
- 当前阶段 / Progress
- 待执行队列
- Timeline
- 评论输入框
- Ongoing 的 Schedule / Health / Last Check-in / Next Run

Discussion 上的“聊天”负责澄清与产生工作；Todo / Ongoing 上的对话负责推进已经生成的工作。模型原始 message/tool trace 仍不作为长期业务数据保存。

### 4.1 Project Workbench 交互

项目默认进入 Board，而不是独立 Chat 首页。工作台外壳由三部分组成：

- 顶栏：Project 选择器 + Account 菜单；Account 菜单保留 Workspace 切换、设置和退出登录。
- 左侧：当前 Project 的共享 Discussion 列表；Board、Discussion、Todo、Ongoing 详情都复用同一列表。桌面可折叠，移动端使用抽屉。
- 主区：Board 或当前 Card 详情。Board 右下角可以直接开始新 Discussion；详情只保留当前对象的回复框。

新 Discussion 的首条消息属于一个原子用户意图，但网络请求分成“创建 Discussion Card”和“提交首条 Comment”。因此 UI 在 Card 创建成功后立即持久保存 `Card ID + 原始草稿 + Comment 幂等键`；若第二步失败，用户重试同一 Card/同一幂等键，不能创建重复 Discussion，也不能清空原文。普通创建失败或切换 Project 时，草稿也按 Project 保留。

Todo / Ongoing 详情顶部只放紧凑工作摘要；详细任务信息在桌面和手机都默认关闭且不预留空列。打开后先阅读标题、目标、来源和适用元数据，Human 主动点击“编辑”后才显示表单。Discussion 的标题/摘要保持 AI 管理的只读规则。Ongoing Schedule 同样先阅读，再显式新增/编辑。

执行状态属于用户 Comment 的附属系统状态，不伪装成 Agent 消息。Human Comment Event → Trigger → Run/retry 通过稳定 ID 关联；等待、执行、取消、失败和 retry 显示在对应用户消息下。Schedule/System 等没有用户来源的 Trigger，以及无法建立稳定来源的旧数据，独立显示，不挂到“最近一句话”。

## 5. 生成任务

Discussion 只有一个面向 AI 的“生成任务”动作，不提供“生成 Todo / 生成 Ongoing”两个按钮。

流程：

```text
Discussion
→ Human 点击“生成任务”或在会话中明确要求生成工作
→ Agent 阅读当前 Discussion durable context
→ Agent 判断 kind = todo | ongoing
→ 创建新的 Card，并记录 source_discussion_id
```

“生成任务”是派生，不是类型转换。生成出的 Todo / Ongoing 立即成为独立工作对象，后续生命周期不会反向改变来源 Discussion。

Todo / Ongoing 不暴露生成其他 Card 的能力。

该规则只约束 **AI 派生工作**。Human 在 Board 的“新建待办”是直接创建独立 Todo，不需要来源 Discussion；第一版不提供 Human 直接创建 Ongoing，避免把长期事项的 Schedule / Health 语义拆散。

## 6. 用户发送新方向

### 没有 current Run

默认动作：

`发送并执行`

后端创建 Comment Event + Trigger，并在可执行条件满足时创建 Run。

### 当前 Run 为 queued / running

输入框提供两个动作：

#### 加入队列

默认、安全动作。

当前 Run 不受影响，新评论产生新的 Trigger；该 Trigger 没有 Run，因此天然处于待执行队列。

#### 立即转向

表示用户明确希望新的方向优先。

当前 Run 进入 superseded；新评论产生 Trigger，并优先创建新的 Run。

已经排队的旧 Trigger 默认保留，不因为一次转向被自动删除。用户如果明确希望取消旧队列，应执行显式取消操作。

### 当前 Run 为 waiting_human

UI 不再显示“加入队列 / 立即转向”，而显示：

`回复并继续`

底层仍通过新 Comment → 新 Trigger → 新 Run 接续；旧 waiting_human Run 进入 superseded。

## 7. Review 与人类控制

CycleDock 允许 Agent 持续推进任务，但最终完成权保留给人类。

推荐规则：

- Agent 可以输出 Result。
- Agent 可以 Confirm 工作已经达到 Review 条件。
- 服务端根据规则把 Working 推进到 Review。
- 只有 Human 可以把 Review 变成 Done。
- Human 可以根据实际需要把 Done 重新打开。

## 8. 产品不做什么

第一版明确不做：

- 脱离 Project / Card 的通用 Chat 产品。
- Agent Marketplace。
- 多 Agent DAG / Workflow Builder。
- 完整模型 trace 产品。
- raw tool call / raw tool result 浏览器。
- 虚假的百分比进度。
- Redis / 消息队列 / 微服务化。
- SQLite / 多数据库兼容。
- 复杂 RBAC。
- 多 Board 体系。
- 通用 if/else 自动化编排。

## 9. 产品设计目标

CycleDock 应做到：

1. 用户第一眼看到的是任务和看板，而不是 Agent 技术概念。
2. Agent 执行过程可观察，但只展示有意义的进度，不暴露内部思维链。
3. 新方向、排队、转向、等待人类等行为有稳定可解释的状态模型。
4. 同一个 Card 可以跨多次 Run 长期积累工作知识。
5. Ongoing Card 可以自然形成一轮又一轮 Cycle。
