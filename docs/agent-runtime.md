# Agent Runtime 与上下文

## 1. 核心原则

> Card retains knowledge; Run retains execution state.

中文：

> Card 保存经过提炼的长期任务知识；Run 保存短生命周期执行状态。

CycleDock 不把一个 Card 实现成永不结束的模型 Conversation。

每个 Run 都独立构建模型上下文。

## 2. 上下文分层

### Project Context

长期项目背景：

- Project 名称 / 描述。
- Project instructions。
- Workspace Agent instructions。
- 可用 Skills / MCP Connections 的说明。

### Card Context

长期任务上下文：

- Card 标题 / 描述。
- 当前业务状态。
- 用户评论。
- 重要 Finding。
- Result。
- Check-in。
- 有意义的 Progress。
- 状态变化。
- 必要的资源引用。

### Trigger Context

本轮明确方向：

- Trigger cause。
- 对应 human comment / schedule。
- 原始工作意图。

### Run Context

只属于当前执行：

- model messages。
- tool calls。
- tool arguments。
- raw tool results。
- 临时日志、命令输出。
- 本轮中间推理。

Run Context 默认只存在于运行时内存，不作为 Card 长期历史。

## 3. 工具上下文不继承

前一个 Run 可能执行：

```text
ssh
docker ps
journalctl
curl
```

下一个 Run 不需要继承这些调用的完整参数和原始输出。

真正有价值的信息必须“晋升”为长期事实，例如：

```text
Finding:
nginx upstream 当前指向 127.0.0.1:8080，配置正常。

Result:
问题范围已经缩小到 session cookie 的 SameSite 配置。
```

因此：

> Tool Result 是原材料，Finding / Result 才是任务记忆。

## 4. Context Builder

正式实现放在类似：

`internal/agent/context.go`

输入：

- Workspace / Project。
- Card。
- 当前 Trigger。
- 当前 Run。

输出给模型的上下文建议：

1. 系统安全与产品约束。
2. Project instructions。
3. Card title / description。
4. Card 当前状态。
5. 已确认的重要 Finding / Result。
6. 最近有意义的 Timeline。
7. 必要的队列概况。
8. 当前 Trigger。

不要简单把完整 Event 表全部拼进 Prompt。

当前实现使用最近最多 50 条 meaningful Event，事件类型只包含：

- comment。
- progress。
- finding。
- result。
- checkin。
- confirm / reject。
- status_change。

tool call、tool arguments、raw tool result 从不进入该查询。

## 5. 长历史压缩

Card 使用几个月后可能积累大量 Event。

第一版先用简单策略：

- Project Context。
- Card title / description。
- 关键长期事实。
- 最近 N 条 meaningful Event。
- 当前 Trigger。

当历史明显变长后，再引入 Card durable summary / context summary。

summary 只能保存：

- 已确认事实。
- 已完成结论。
- 未解决问题。
- 当前约束。

不能保存 chain-of-thought 或 raw tool trace。

第一版不需要向量数据库。

## 6. Queue

如果：

```text
Trigger A → Run A running
Trigger B → no Run
Trigger C → no Run
```

则 B、C 就是队列。

Run A 完成后，Dispatcher 按 sequence Claim 最早的 pending Trigger。

## 7. 队列执行时使用最新上下文

Trigger 创建时只冻结原始意图。

它真正开始 Run 时，重新读取 Card 最新上下文。

例如：

```text
09:00 Trigger B: 检查磁盘
09:05 其他 Run 得出新的 Finding
09:20 Trigger B 才开始
```

Run B 应看到 09:20 时 Card 已经拥有的最新知识，而不是 09:00 冻结的一整份 Prompt Snapshot。

## 8. Interrupt

“立即转向”改变的是调度优先级，不清除任务历史。

旧 Run：

`running → superseded`

新 Trigger：

`human_comment + interrupt`

新 Run 继承 Card 已经产生的有效 Finding / Result，但不继承旧 Run 的 raw tool context。

旧 Run 的已确认事实仍属于 Card；旧 Run 只是失去继续控制当前任务的权利。

## 9. waiting_human

waiting_human 表示：

> 当前执行需要用户输入后才能继续。

规则：

- waiting_human 占用 current Run 槽位。
- 不持续 heartbeat。
- Scheduler 不自动接管。
- 用户回复后创建新 Trigger。
- 旧 waiting_human Run 进入 superseded。
- 新 Run 基于最新 Card Context + 用户回复继续。

## 10. Progress / Finding / Result

### Progress

只记录有意义的阶段变化，例如：

- 正在读取部署配置。
- 构建完成，开始启动服务。
- 新版本已启动，正在验证 WebSocket。

不要使用没有事实基础的百分比。

### Finding

从执行过程中提炼、值得后续继承的事实。

### Result

一次 Run 对当前 Trigger 的最终结果摘要。

### Heartbeat

纯执行存活信号，不写 Timeline。

## 11. Run 结束

成功：

- 写必要的 Finding / Result / Check-in。
- Run → completed。
- Dispatcher 尝试 Claim 下一个 pending Trigger。

失败：

- Run → failed。
- error_summary 写可供用户理解的失败摘要。
- 是否重试由显式 manual_retry / system_followup Trigger 决定。

过期：

- Run → expired。
- 原 Trigger 不重新 Claim。
- 需要恢复则创建新 Trigger。

被替代：

- Run → superseded。
- 迟到结果不得修改当前 Card 状态。

## 12. 当前 Built-in Runtime

Workspace owner 可以配置：

- OpenAI-compatible Provider Base URL。
- Model。
- Model API Key 对应的环境变量名。
- request timeout。
- max tool rounds。
- Workspace Agent instructions。
- 零个或多个 outbound MCP connection。

MCP connection 保存 URL、timeout、enabled 与 Bearer Token 环境变量名。

敏感值本身不进入 Settings API、PostgreSQL 或普通日志。Runner 开始 Run 时才从 CycleDock 进程环境读取。

Dispatcher：

- 周期处理所有 Workspace 的 due Schedule 与 lease expiry。
- 只为启用 Built-in Agent 的 Workspace 自动执行 Trigger。
- 优先启动已经绑定 `cycle_agent` 的 queued Run。
- 没有 current Run 时，再原子 Claim 最早 pending Trigger。
- waiting_human 仍占 current Run 槽位，不会被自动越过。

Runner：

- Claim 后先把 Normal Card 推进 Working（如果尚未进入 Working）。
- Run 开始时重新构建最新 durable context。
- 每个 Run 重新 discovery 已启用 outbound MCP tools。
- heartbeat 只更新 lease，不写 Timeline。
- MCP raw result 只追加到当前内存 model message。
- 最终 assistant response 作为 Result 通过统一 Store 完成 Run。
- Human interrupt / supersede 后，旧 Runner 即使模型晚返回，`FinishRun` 仍因 current Run 校验失败而不能污染 Card。

## 13. Artifact

如果执行产生真正需要后续继续使用的文件，例如 patch、诊断报告、截图或配置产物，不应通过继承完整 tool response 传递。

后续可以增加 Card Artifact / Resource 概念，让 Event 只保存资源引用，Agent 按需读取。

Artifact 不属于第一阶段必须 schema，避免提前扩展。
