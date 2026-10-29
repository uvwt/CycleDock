# Agent Runtime 与上下文

完整决策见 session-first-adk.md。

## 1. 核心原则

Card owns the session; Turn owns the intent; Attempt owns the execution.

中文：Card 保存连续 Agent Session；Turn 保存一次工作输入；Attempt 保存一次执行状态。

旧原则“每个 Run 独立构建上下文、raw tool result 不跨 Run”已经废弃。

## 2. 一次正常交互

~~~text
Human message
→ create Turn
→ Dispatcher claims Turn
→ create Attempt / ADK invocation
→ ADK Runner.Run(card session, message)
→ Session Events:
   user input
   model response
   tool call
   tool response
   ...
→ final response
→ Attempt completed
~~~

下一次 Human message 进入同一个 Card Session。

## 3. Session continuity

ADK Session Event 是连续上下文事实源。近期 tool response 可以自然进入下一 Turn 的 prompt。

~~~text
Turn 1
  user: 检查容器
  assistant: function_call docker_ps
  tool: 20 containers
  assistant: ...

Turn 2
  user: 刚才第 8 个容器是什么镜像？
~~~

Turn 2 不应该因为缺少历史 tool response 而再次调用 docker ps。

## 4. Freshness

复用历史与重新查询不是一回事。

- “刚才 / 上一次 / 当时” → 优先历史 Session / Artifact。
- “现在 / 最新 / 目前是否仍然” → 重新调用真实工具。

后续给历史事件与 Artifact 加来源时间 / freshness metadata，帮助模型稳定判断。

## 5. Context Compaction

Session 可以长期存在，但 prompt 不能无限增长。

生产 ADK Runner 使用 Tail Retention Context Compaction：

- 默认 `TokenThreshold = 24000`，在 prompt 继续膨胀前触发。
- 默认 `EventRetentionSize = 20`，保留近期指代与 function call / response 配对所需 raw tail。
- summarizer 使用同一 ordered model routes，但不发布内部 summary SSE。
- summarizer 对单个工具内容与 transcript 本身也设置上限，避免“为了压缩而先构造无限大压缩请求”。

Compaction record 是 Session EventActions 的一部分，由 SessionService 完整持久化。它走独立 bookkeeping ownership guard：只允许当前 running、已绑定 invocation 的 Attempt 对当前 Card Session 已存在事件范围追加 summary；普通 Session Event 的 stale invocation guard 不放宽。

原始 Session Event 不因为 compaction 被物理删除；压缩只影响 prompt assembly。

## 6. History Retrieval

当用户询问已经被 compaction 覆盖的精确旧数据时，Agent 需要按需检索历史。

当前内部工具：

~~~text
cycledock_search_session_history
~~~

它只搜索当前 Workspace/Card identity 对应的原始 PostgreSQL Session Event，跳过 compaction summary，自身限制 query 长度、返回条数和单条内容大小。历史检索优先解决精确旧事实，不是用来再次执行真实设备操作。

## 7. Artifact

大型工具输出不适合永久内联在 Event / prompt：日志、大 JSON、文件、图片、patch、build output、报告。

当前流程：

~~~text
large MCP tool result (> 12k chars)
→ recursively redact structured credential fields
→ persist full sanitized payload in PostgreSQL agent_artifacts
→ keep <= 4k preview + artifact_ref + version in FunctionResponse
→ later cycledock_read_artifact on demand (<= 20k chars/chunk)
~~~

Artifact 使用 ADK artifact.Service 兼容的 versioned PostgreSQL 实现，scope 固定由当前 Workspace/Card Session identity 推导；Agent 不能通过参数切换到其他 Card。MIME、metadata、version 与 canonical URI 一起持久化。

## 8. Turn Queue

排队 Turn 不能提前送入 ADK Session。

~~~text
Attempt A running
Turn B enqueue
~~~

此时 B 存在于 card_turns，UI 可以显示，但 Session history 暂时没有 B。

A 完成后才 claim Turn B、创建 Attempt B 并调用 ADK Runner；此时 ADK 才 append B 的 user message。

## 9. Interrupt

立即转向：

1. 创建 interrupt Turn。
2. current Attempt 变 terminal。
3. 新 Attempt 获得执行权。
4. 新 input 进入 Session。

旧 Attempt 的网络请求可能晚返回，因此 SessionService 后续要用 invocation/Attempt ownership 阻止 late AppendEvent。

## 10. Retry

技术失败后重试同一个 Turn：

~~~text
Turn 42
Attempt 1 failed
Attempt 2 running
~~~

不再通过创建新的 Trigger 来表达一次技术 retry。

如果用户改变了任务意图，应创建新 Turn。

## 11. waiting_human

Human-in-the-loop 属于同一 Card Session。

ADK v2 Event 已有 Human Input / requested input 能力；CycleDock 仍需要把产品状态映射到 Attempt waiting_human，并保证：

- waiting_human 占用 current Attempt。
- 不维护 lease。
- Schedule 不越过它。
- Human 回复后恢复/续跑同一 Session。
- stale activation 不能写回。

具体采用 ADK workflow pause/resume 还是新 invocation 延续 Session，在 Runner cutover 阶段以最少状态重复为准。

## 12. SessionService

CycleDock 自定义 PostgreSQL session.Service，当前实现位于 internal/agentsession/postgres.go。

当前已经支持：

- Create / Get / List / Delete。
- AppendEvent。
- app / user / session / temp state。
- event ID。
- partial event 忽略。
- event filters。
- compaction round-trip。
- ADK 全字段 JSON round-trip。
- PostgreSQL row lock / stale session 检查。

产品 identity：

~~~text
app_name = cycledock
user_id = workspace_id
session_id = card_id
~~~

## 13. Model Runtime

ADK LLM Agent 负责 agent/tool loop。

CycleDock model adapter 仍负责 OpenAI-compatible、ChatGPT SIWC、Grok OAuth、多账号 credential、ordered routes、retryability classification、failover、partial stream reset。

ADK experimental model routing 可以辅助，但不替代已有更强的产品语义。

## 14. MCP

普通 MCP 迁移到 ADK McpToolset。

每次 invocation 按需建立 MCP connection；Session 恢复不意味着复用失效 socket。

NexusDock first-party integration 继续负责 runtime OAuth、bootstrap execution targets、hidden bootstrap tool、tool filtering、native names。

## 15. Security

Session-first 允许持久化 tool response，但不是无限制 trace dump。

必须阻止 chain-of-thought、credential / Cookie / token、无必要 secret、未授权跨 Workspace 内容。

工具层应在返回给 Agent 以前完成最小化和脱敏；大敏感材料优先使用受控 Artifact，而不是长期 Event 文本。

## 16. 实时输出

产品 SSE 与 ADK Session persistence 分开：

- partial token/event 可以实时推给浏览器。
- partial Session Event 不持久化。
- final / durable ADK Event 才进入 SessionService。

因此浏览器可以保持低延迟，但恢复后不会出现重复半截消息。

## 17. 当前重构阶段

已经完成：

- ADK Go v2.6 依赖。
- Session-first identity。
- PostgreSQL SessionService。
- 官方 conformance。
- 跨 Turn tool response persistence 测试。

Phase B 已完成并验证：

- card_turns / turn_attempts。
- Human / Schedule / System Turn。
- enqueue / interrupt / waiting_human reply。
- Attempt retry / recovery / heartbeat / lease。
- current Attempt 唯一约束。
- stale invocation write guard。
- 真实 PostgreSQL 并发与 -race 测试。

Phase C 已完成 Built-in Runtime cutover：

- Dispatcher 正式以 Turn / Attempt 为 Built-in Agent 调度入口。
- ADK Runner 以 Card ID 恢复同一个持久 Session。
- CycleDock model routes / ChatGPT / Grok / OpenAI-compatible 通过 ADK model.LLM adapter 接入，并保留 ordered failover / streaming reset。
- 普通 MCP 与 NexusDock 工具通过 ADK FunctionTool 参与 tool loop；tool call / FunctionResponse 持久化到 Session Events。
- Human Comment / reply / cancel / retry HTTP API 已切到 Turn / Attempt。
- Web Card detail 已切到 current_attempt / pending_turns / attempt_history，并用 source_event_id 稳定关联 Human Comment。
- Schedule occurrence 已改为创建 Turn，同一 Ongoing Card 继续使用同一 Session。
- waiting_human 使用 ADK SkipSummarization 在保存 FunctionResponse 后终止当前 invocation。
- MCP transport fatal、64 轮 runaway tool-loop guard、stale invocation protection 已恢复并验证。
- 真实 PostgreSQL 集成测试已证明跨 Turn 可以复用上一轮 tool response，第二 Turn 不需要重复调用只读 MCP 工具。

Phase D 已完成 Context / Artifact / History：

- Tail Retention 生产配置与真实长 Session 验收完成。
- PostgreSQL `artifact.Service`、大 MCP 结果外置和按需分块读取完成。
- compacted-away 原始 Session Event history retrieval 完成。
- Thought / ThoughtSignature 不持久化；FunctionCall / FunctionResponse / JSON 文本中的结构化 credential key 在持久化与 Artifact 外置前递归脱敏。
- 真实 PostgreSQL 已验证：compaction 后 normal prompt 不再包含旧长原文，但 history tool 能找回精确旧值；Artifact 能找回 preview 之外的精确尾部值且不会重复调用真实 MCP 工具。

Phase E 已完成 External MCP cutover：

- OAuth MCP `create_turn / claim_attempt / heartbeat_attempt / reply_message / check_in / finish_attempt` 全部使用同一 Turn / Attempt Store。
- `get_card / list_cards` 返回 `pending_turns / current_attempt / attempt_history` 与 `current_attempt_status / pending_turn_count`。
- External OAuth Source 只能写自己拥有的 Attempt；stale / cross-source heartbeat、progress、finding、check-in、finish 均被拒绝。
- Human reply 与 Schedule 创建的 Turn 可由 External Agent 按 Card Claim；同 Source 网络重试复用当前 Attempt。
- 旧 `create_followup / claim_work / heartbeat_run / finish_run` 不再作为 MCP 兼容别名暴露。

Phase F cleanup 已完成：

- 删除旧执行 loop、Context Builder / context_budget、旧 Store API/query 和对应历史测试。
- 删除旧执行 schema；final baseline 只保留 `card_turns / turn_attempts`。产品 `events` 不再保存旧 Run 外键，只为 Attempt 产生的 durable Event 保留 nullable `attempt_id`，用于稳定关联 `Attempt → Turn → source_event_id`。
- SSE 临时协议统一为 `attempt.started / attempt.stage / attempt.text.* / attempt.finished / attempt.failed`，payload 使用 `attempt_id`。
- 首次发布 migration 已重整为单一 `000001_initial.sql`，并通过 fresh `up → down 0 → up`、自定义 `search_path` 和连续 `sqlc generate` 稳定性验证。

项目不提供旧 Runtime 长期兼容；最终发布前只剩 release verification。

## Discussion 标题与摘要

Dispatcher 启动独立后台任务，每 5 秒扫描已有首条 Turn、标题与描述均为空的 active Discussion，使用 Workspace 的模型路由生成结构化标题与描述。生成请求不提供工具，不创建 Turn / Attempt，不写入 ADK Session，也不发布临时模型输出。每次请求最多 20 秒；失败保留空白元数据供后续扫描重试，重启后同样可以恢复。Workspace 尚未配置启用的模型路由时等待配置。

保存使用数据库条件更新：仅当卡片仍为 active Discussion 且标题与描述均为空时写入，避免覆盖用户在生成期间的编辑。多个实例可能重复发起生成，但只有一次保存成功。保存后发送 `card.updated` 通知浏览器刷新。

标题生成使用 `ResponseJsonSchema` 定义必填的 `title` / `description` 字符串并禁止额外字段。模型适配层向 Chat Completions 发送严格的 `response_format.json_schema`，向 Responses 发送严格的 `text.format`；普通对话不附加输出格式。仍在本地拒绝非法 JSON、额外字段、尾随内容及空白必填字段；不支持 JSON Schema 的模型路由会报错，不自动降级为仅提示词约定。

## 浏览器卡片更新通知

登录后，尚未决定通知权限的浏览器会主动显示开启提示；用户点击「开启通知」才请求浏览器原生授权，允许后自动开启通知。选择「暂不开启」会在当前浏览器记住关闭选择，不重复打扰；已拒绝授权时不再主动提示。也可随时在「设置 → 常规 → 浏览器通知」开启或关闭，通知偏好仅保存在当前浏览器。需要安全上下文、Notifications / Service Worker 支持和用户授权。

开启后，登录页面保留工作空间级 `/api/v1/cards/updates/stream` SSE 连接，不随 Card 路由切换卸载。服务端每 3 秒从 PostgreSQL 比较当前工作空间的 Card 元数据、持久 Event、最新 Attempt 身份与状态、Turn 新建/取消及 Schedule 变化；仅在版本变化时发送快照。新消息通知以 Card 标题为标题，正文展示产品时间线中最新消息的前 180 个字符摘要；仅执行状态变化时显示对应状态提示；标题生成等元数据更新保留最新消息摘要，避免合并通知覆盖回答正文。通知不包含 token、执行阶段文案或 heartbeat，也不使用 ADK Session 原始工具上下文。

首次快照只建立基线，不为历史内容弹窗；SSE 重连保留客户端基线，断线期间的变化合并成每张卡片一次更新提示。通知由各标签页共享的 Service Worker 串行处理：任一标签页正在前台查看该 Card 时不通知，同一版本去重，同一卡片使用相同通知 tag 合并展示。点击通知聚焦已打开的对应页面，或打开该 Card。

这是网页在线期间的更新通知，不是离线 Web Push。网页关闭、浏览器冻结后台页面或系统禁止通知时不能保证送达；尚未实现推送订阅和服务端离线推送。多次变化发生在同一个采样窗口时合并通知，不逐条投递。

## 产品消息时间线

同一次 Built-in Agent Attempt 可以通过 `reply_message` 产生多条面向用户的 Message 气泡；最终答复不再生成 Result 或 Attempt 结果摘要。选择消息与 `waiting_human` 状态在同一事务中保存，人工回复后问题仍保留。Agent 消息不会创建新的业务 Turn。

前端按事件 `sequence` 展示人工消息与持久 Message 气泡。等待回复、取消、替代和完成状态与耗时显示在对应消息组的名称旁。模型自由文本不自动成为气泡，正常结束且未调用回复工具时才补发最终 Message。思考内容、上下文压缩摘要及原始工具参数和返回值不作为聊天消息展示。

已有执行若仅在 `progress_summary` 中保存了提问，前端会在等待回复或人工已回复时补充展示问题气泡。同一 Attempt 已有相同正文的 Agent 消息时不重复展示；其他 Attempt 或 Human 的同文消息不会隐藏该问题。该展示回退不写入虚构的持久事件。


`reply_message` 流式生成时，只提取面向用户的顶层 `content` 正文，先写 PostgreSQL Message 快照再推送 SSE。快照的 `message.preview_id` 用于稳定关联，`message.preview=true` 表示工具尚未提交。完整工具调用在同一事务中原地更新该 Message 并写入幂等键；气泡 ID、sequence、创建时间保持不变。取消、替代或进程重启不会删除已保存正文，刷新直接从产品 Timeline 恢复。预览不是已执行的工具响应，不写入 ADK Session。所有片段保存与取消、替代共用 Card 行锁并校验当前 Attempt / Runner，迟到片段不得继续修改正文。
