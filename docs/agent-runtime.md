# Agent Runtime 与上下文

## 1. 核心原则

> Card retains knowledge; Run retains execution state.

中文：

> Card 保存经过提炼的长期工作知识；Run 保存短生命周期执行状态。

Discussion 可以在产品上表现为持续会话，但 CycleDock 仍不保存一个永不结束的模型 Conversation。每一轮执行仍由新的 Trigger / Run 驱动。

每个 Run 都独立构建模型上下文。

### Discussion 首轮元数据

新 Discussion 的首轮 Run 会从第一条用户消息生成标题与摘要，但这条 metadata 请求与主回复并行：

- 主回复不等待标题/摘要生成，也不因为 metadata 失败而失败。
- metadata 使用独立模型请求，不进入用户可见的流式输出。
- metadata 只在 Discussion 仍为 Active 且 title 仍为空时写入，避免覆盖用户后续手工修改。
- 产生 metadata 的 Run 即使已经 completed / failed 仍可完成这次旁路写回；superseded / expired / cancelled Run 不允许回写。
- 写回后通过 `card.updated` SSE 通知前端刷新 Card 与 Discussion 列表。

因此标题生成延迟或偶发失败不会增加首条 Agent 回复的等待时间。

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

当前实现使用最近最多 50 条 meaningful Event 作为 bounded recent window，事件类型只包含：

- comment。
- progress。
- finding。
- result。
- checkin。
- confirm / reject。
- status_change。

tool call、tool arguments、raw tool result 从不进入该查询。

## 5. 长历史压缩

Card 历史超过 recent window 后采用两层策略：

1. **历史 durable facts 原样保留**：窗口以前的 Finding / Result / Check-in / Confirm / Reject / Status change 仍从原始 Timeline 读取并进入上下文。
2. **旧 Comment / Progress 派生压缩**：记录窗口以前 Comment / Progress 的数量，并最多保留最近 8 条 excerpt 作为 compacted context。

这套 compaction 不写回 Event 表，也不替代 Timeline：

- 原始 Timeline 永远是审计事实源。
- compaction 是每次 Run 构建时的派生 context。
- Finding / Result / Check-in 不会因为窗口滚动而丢失。
- raw model messages / tool trace 从不因为 summary 被重新持久化。

后续只有当事件规模真实达到需要缓存时，才考虑单独持久化可重建的 derived summary；第一版不需要向量数据库。

## 6. Queue

如果：

```text
Trigger A → Run A running
Trigger B → no Run
Trigger C → no Run
```

则 B、C 就是队列。

Run A 完成后，Dispatcher 按 sequence Claim 最早的 pending Trigger。

Human Web 展示队列/执行状态时，必须沿稳定的执行链关联：Human Comment Event 的 `id` → Trigger 的 `cause_event_id` → Run 的 `trigger_id`。manual retry 通过 `retry_of_trigger_id` 追溯到原 Trigger，并由 API 派生 `source_event_id` 指向最初 Comment Event。Schedule / system follow-up 等没有用户 Comment 来源的 Trigger 不强行归属到任意消息。

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
- error_summary 写可供用户理解、且不包含 secret / 原始上游响应的失败摘要。
- provider timeout、malformed response、MCP initialize/tool transport failure、上下文预算耗尽、异常长工具循环都有明确失败分类。
- retry 创建新的 `manual_retry` Trigger，旧 Run/Trigger 不复活。
- `manual_retry` 保存 `retry_of_trigger_id`；旧失败记录继续保留。展示层可以把多个尝试附着到同一个 `source_event_id`，但不得覆盖旧状态，也不得按消息内容、时间或位置推断关联。

过期：

- queued / running lease 到期后 Run → expired。
- 原 Trigger 不重新 Claim。
- Built-in `cycle_agent` 在仍可执行时创建新的 `system_followup` Trigger；并发 maintenance 只能创建一条恢复方向。
- waiting_human 不维护 lease，也不会被 restart recovery 当成 expired。

取消：

- pending Trigger cancel 只记录 Trigger 取消元数据，不伪造 Run。
- queued / running / waiting_human current Run 可由 Human 终结为 cancelled。
- cancel 不自动创建新方向；需要继续时显式 retry。

被替代：

- Run → superseded。
- 迟到 Finding / Result / finish 不得修改当前 Card 状态。

## 12. 当前 Built-in Runtime

Workspace owner 可以配置：

- 零个或多个有序 Model Route；每条 Route 绑定 provider、model，以及订阅账号或 API endpoint。
- Route 可以独立启用/停用，数组顺序即故障转移优先级。
- OpenAI-compatible Route 保存 Provider Base URL / API Key 环境变量名。
- ChatGPT / Grok 可以连接多个 model account；订阅 Route 显式绑定一个 credential。
- Workspace Agent instructions。
- 零个或多个 outbound MCP connection。

MCP connection 保存 URL、timeout、enabled 与 Bearer Token 环境变量名。

Cycle Agent 本身没有 Workspace 级启用开关。只要存在至少一条 enabled Model Route，Dispatcher 就把该 Workspace 视为可执行；没有启用 Route 时不 Claim 新工作。订阅 Route 的 credential 由数据库外键保证属于同一 Workspace，且被 Route 引用时不能先断开账号。

模型请求 timeout、上下文预算与异常工具循环保护属于 Runtime 内部策略，不进入 Settings API / PostgreSQL，也不要求用户调参。默认模型请求 timeout 为 120 秒；工具链正常情况下依靠自动上下文压缩持续运行，仅保留一个较高的隐藏式异常循环上限作为安全保险。

Model API Key 与 MCP Bearer Token 本身不进入 Settings API / PostgreSQL；Runner 开始 Run 时从 CycleDock 进程环境读取。

ChatGPT / Grok OAuth token 不进入 Settings API 或普通日志，但需要跨 Run 刷新，因此存放在独立 credential 表并用 CYCLEDOCK_MODEL_SECRET_KEY 做 AES-256-GCM 加密。每个 credential 有独立 ID，同一 Provider 可以连接多个账号；重新授权同一稳定 account subject 会更新原 credential。Workspace Agent settings 只保存 instructions，模型选择由 Model Route 保存 credential ID，不复制 OAuth secret。

模型协议：

- openai_compatible：保持非流式 /chat/completions tool-call loop。
- chatgpt：官方 SIWC OAuth token + api.openai.com/v1/responses，固定 store=false + stream=true。
- grok：xAI OAuth token + cli-chat-proxy.grok.com/v1/responses，附带 Grok proxy 所需认证/模型 header。
- subscription /models 与 inference 使用同一账号 access token。

Dispatcher：

- 周期处理所有 Workspace 的 due Schedule 与 lease expiry。
- 只为存在至少一条 enabled Model Route 的 Workspace 自动执行 Trigger。
- 优先启动已经绑定 `cycle_agent` 的 queued Run。
- 没有 current Run 时，再原子 Claim 最早 pending Trigger。
- waiting_human 仍占 current Run 槽位，不会被自动越过。

Connection test：

- 每条 Model Route 可以独立测试。
- OpenAI-compatible Route Test 对配置 endpoint / model 发最小 /chat/completions 请求。
- ChatGPT / Grok Route Test 使用该 Route 绑定的具体账号与正式 Responses transport 发最小请求。
- MCP Test 复用正式 MCP Client，只做 initialize + ListTools 并返回工具数量，不执行 Tool。
- endpoint / auth / timeout / malformed / initialize / ListTools 错误只返回安全分类消息。

Runner：

- Claim Todo 后把 Todo 推进 Working（如果尚未进入 Working）。
- Claim Discussion 不改变 Discussion 生命周期状态；Discussion 永远不会因为 Agent 开始执行而进入 Working。
- Discussion 第一次运行且 title 仍为空时，Runner 启动独立 metadata 请求，与正式工作轮次并行；metadata 完成后按当前 Card/Run 状态原子写回，不阻塞用户可见回复。
- Run 开始时重新构建最新 durable context。
- 每个 Run 重新 discovery 已启用 outbound MCP tools。
- 同一 Run 的 system / Workspace / Project / Card / Trigger 前缀只在 Run 开始时构建一次；工具轮次只在后方追加，尽量保持供应商 prompt-cache 可复用的稳定前缀。
- 每次模型请求前按估算 Token 预算检查当前 Run；旧的已完成工具轮次会被确定性压缩成执行摘要，最近一轮 tool call / tool result 配对保留。
- 单个最新工具结果过大时可在保持 tool_call_id 配对的前提下缩成明确 preview；raw tool result 仍不会写入 Card Timeline。
- 如果供应商仍明确返回 context overflow，Runner 自动切换到更保守的输入预算并重试一次，后续工具轮次继续沿用该预算。
- 每轮模型推理从当前 active Route 开始；429、5xx、timeout/网络失败、账号需重连或 refresh 失败、空或 malformed model response 等可恢复模型故障才按 Route 顺序向后 failover。普通 400、Run cancellation、MCP 失败与任务状态冲突不切模型。
- 一旦备用 Route 成功，本 Run 后续 tool round 从该 Route 继续，不重新撞已失败的高优先级 Route，也不回绕。
- 上游已流出部分文本后才失败时先发送 ephemeral run.text.reset 清空当前未完成文本，再切备用 Route；partial text 不进入 durable Timeline。
- 自动压缩负责控制上下文增长；独立的隐藏式异常工具循环保护只用于阻止失控模型无限调用工具，不作为用户配置。
- heartbeat 只更新 lease，不写 Timeline。
- MCP raw result 只追加到当前内存 model message。
- 最终 assistant response 作为 Result 通过统一 Store 完成 Run。
- Human interrupt / supersede 后，旧 Runner 即使模型晚返回，`FinishRun` 仍因 current Run 校验失败而不能污染 Card。

## 13. Discussion 生成工作

Discussion 是唯一允许生成新工作 Card 的来源。

Built-in Agent 提供一个内部“生成工作”能力，输入只表达结构化结果：

- kind: todo | ongoing
- title
- description
- Todo 的 required confirmations，或 Ongoing 的可选 Schedule 建议。

用户界面只有“生成任务”一个动作；Todo / Ongoing 的判断由模型完成。

Store 在创建时重新检查：

- 当前来源 Card 仍是 active Discussion。
- 新 Card 与来源 Discussion 属于同一 Workspace / Project。
- kind 只能是 todo 或 ongoing。

Todo / Ongoing 不获得这一能力，避免形成 Todo → Todo 或 Ongoing → Todo 的派生树。

## 14. Artifact

如果执行产生真正需要后续继续使用的文件，例如 patch、诊断报告、截图或配置产物，不应通过继承完整 tool response 传递。

后续可以增加 Card Artifact / Resource 概念，让 Event 只保存资源引用，Agent 按需读取。

Artifact 不属于第一阶段必须 schema，避免提前扩展。

## 15. NexusDock First-party Runtime Bootstrap

如果当前 Workspace 已授权 NexusDock 远程设备，Runner 在第一次模型请求前完成以下动作：

1. 获取当前有效的 NexusDock MCP access token；必要时安全刷新。
2. 把 NexusDock 作为 first-party MCP session 自动连接，不读取 `workspace_mcp_connections`。
3. NexusDock 节点工具使用原始工具名；普通用户 MCP 连接继续使用 `mcp_<connection>_<tool>` 命名空间。
4. `agentdock_context` 保留在 MCP session 内，但从模型 Tool Definitions 隐藏。
5. Runtime 内部调用一次 `agentdock_context`，只提取设备名、`node_id`、online、OS/arch/version、主要 capabilities，以及受预算限制的 Skill / dynamic MCP 名称。
6. 精简后的 `NexusDock execution targets` 追加到本次 Run 的 system context。
7. 模型从第一轮起即可直接选择 `node_id` 并调用节点工具；无需先做一次 `agentdock_context` 检索。

完整 node-local rules、Workspace context、Skill 正文和 dynamic MCP schema 不在启动时全部注入。它们仍通过 `workspace_context`、Skill/MCP 相关工具按需读取，避免设备增多后 system prompt 无界增长。

第一方 bootstrap 的原始 MCP result 仍属于当前 Run 的临时执行材料，不写入 Card Timeline。
