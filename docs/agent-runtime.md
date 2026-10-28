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

- OAuth MCP `create_turn / claim_attempt / heartbeat_attempt / report_progress / add_finding / check_in / finish_attempt` 全部使用同一 Turn / Attempt Store。
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
