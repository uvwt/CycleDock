# 产品定义

## 1. 一句话定位

CycleDock 是一个以项目看板和任务为核心的人机协作系统：人类和 AI Agent 围绕同一个 Card 持续推进工作，CycleDock 负责保存任务状态、长期上下文、执行队列、进度、结果与 Review。

## 2. 用户心智

用户不需要先创建 Workspace、Project、Board 三层容器。

核心路径保持简单：

```text
创建 Project
→ 创建 Card
→ 在 Card 中持续讨论和下达方向
→ Agent 执行
→ 查看进度 / 结果
→ Review / Done
```

Project 本身就是一张项目看板。Board 只作为 UI 视图概念存在，不作为数据库实体。

未来如果需要 Kanban、List、My Tasks、Review、Critical Ongoing 等不同展示方式，应优先设计 View / Saved View，让不同 View 读取同一批 Card，而不是在 Project 下创建多个 Board。

## 3. Card 类型

### 3.1 Normal Card

用于有明确完成终点的工作。

固定业务列：

```text
Discussion → Todo → Working → Review → Done
```

含义：

- Discussion：仍在澄清、讨论或收集信息。
- Todo：方向明确，等待处理。
- Working：正在执行。
- Review：Agent 已认为工作达到提交条件，等待人类确认。
- Done：由人类最终确认完成。

AI / 外部 MCP 客户端不能直接把 Card 标记为 Done。

### 3.2 Ongoing Card

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

Health 不作为独立可写状态，而由最近一次 Check-in 派生：

- healthy
- warning
- critical
- 没有 Check-in 时为 unknown

## 4. Card Detail 是主要协作界面

第一版不提供独立通用 Chat 页面。

人与 Agent 的交互直接发生在 Card Detail：

- 标题和描述
- 当前业务状态
- 当前 Run
- 当前阶段 / Progress
- 待执行队列
- Timeline
- 评论输入框
- Ongoing 的 Schedule / Health / Last Check-in / Next Run

Card 上的“聊天”服务于任务，而不是形成另一套独立 Conversation / Session 世界。

## 5. 用户发送新方向

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

## 6. Review 与人类控制

CycleDock 允许 Agent 持续推进任务，但最终完成权保留给人类。

推荐规则：

- Agent 可以输出 Result。
- Agent 可以 Confirm 工作已经达到 Review 条件。
- 服务端根据规则把 Working 推进到 Review。
- 只有 Human 可以把 Review 变成 Done。
- Human 可以根据实际需要把 Done 重新打开。

## 7. 产品不做什么

第一版明确不做：

- 通用 Chat / Conversation 产品。
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

## 8. 产品设计目标

CycleDock 应做到：

1. 用户第一眼看到的是任务和看板，而不是 Agent 技术概念。
2. Agent 执行过程可观察，但只展示有意义的进度，不暴露内部思维链。
3. 新方向、排队、转向、等待人类等行为有稳定可解释的状态模型。
4. 同一个 Card 可以跨多次 Run 长期积累工作知识。
5. Ongoing Card 可以自然形成一轮又一轮 Cycle。
