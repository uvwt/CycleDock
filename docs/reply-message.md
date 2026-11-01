# 统一 Agent 用户消息（reply_message）

## 产品语义

Agent 对用户的每一条可见气泡都由 `reply_message` 创建；一次工具调用对应一条持久消息，同一个 Turn 可以多次调用。模型内部文本、tool args、raw tool response 和上下文压缩摘要不自动变为用户气泡。

参数：

- `type`：`text` / `attachment` / `choice`。
- `content`：Markdown 正文，`text` 时必填。
- `url` / `alt`：`attachment` 的 HTTPS 下载地址和说明。
- `images`：可选 HTTPS 图片 URL 数组，最多 8 张，显示在正文下方。
- `widget`：`choice` 的 `question` 和 2~8 条 `{value,label}` 选项。
- `end_turn`：默认 false。true 表示当前 Turn 的 Agent 回复已结束。
- `idempotency_key`：一条逻辑消息一个唯一键；重试同键只复用已有消息。

一次调用最多写一条 `events(event_type='message')`，其结构化正文存储在 `message jsonb`。Timeline 通过 `event.id/sequence/attempt_id` 负责恢复、排序和去重；SSE 的 `event.created` 只负责通知刷新，PostgreSQL 是事实源。

## 结束与人工回复

- `text/attachment + end_turn=false`：只产生气泡，Agent 可以继续调用工具或再次发消息。
- `text/attachment + end_turn=true`：先持久化消息，Built-in Agent 终止 ADK loop，再结算 Attempt。External MCP 在同一个事务中结算 Attempt。不得再次写一个 Result 气泡，也不保存 Attempt 结果摘要。
- `choice + end_turn=true`：在发布选择卡的同一事务中切换 Attempt 为 `waiting_human`，释放 lease。用户选择后使用现有 Human `/reply` 创建新 Turn，继承原 Card Session。
- `choice + end_turn=false` 拒绝，因为还未准备接收人工选择。
- 不能把 `end_turn` 解释为 Card 的 Done；Todo 依然需要 `confirm_card` 与 Human Review。
- 上游模型没有调用 reply tool 却正常返回自由文本时，Built-in Runner 用同一个持久消息 API 补发一次最终气泡，避免空白回复。模型原始 token 不再直接成为用户气泡。

Attempt 只保存执行状态、时间及必要的失败信息，不保存 `result_summary`。活动页的 Attempt 条目展示执行状态和失败信息，不把用户输入或消息正文作为结果摘要；点击条目进入 Card 查看消息。`finish_attempt(completed)` 只结算状态，不接收结果正文、不写 Timeline Event。

内置 ADK 同一响应中的工具调用按模型给出的顺序执行，保证多个气泡顺序稳定；`end_turn=true` 之后的同批工具只保存拒绝响应，不再执行。当前选择串行工具执行来保证消息顺序与结束边界，不保留默认并发工具执行。

Human `/reply` 必须携带所回答的 `attempt_id`；选择按钮额外携带 `message_id`，`content` 使用选项的 `value`。服务端在创建 Turn 的同一事务中校验 waiting Attempt、消息归属和选项值；自由文字回复只绑定 Attempt，仍允许用户自行补充说明。幂等键包含回复目标，同键不能改答其他问题。

回滚消息迁移时，完整消息 JSON 转为旧结构可读取的 Progress 文本，保留事件 ID、顺序和 Attempt 关联。重新升级后，这些记录仍以历史 Progress 文本读取。

## 边界与安全

- 数据库使用 Card 行锁 + current Attempt / owner 校验，防止跨 Workspace、其他 Source、superseded/cancelled/expired Attempt 污染 Timeline。
- 同一个 Source 的消息幂等键必须唯一；相同键不同内容拒绝。
- HTTPS URL 不允许 userinfo、fragment、控制字符、非 HTTPS Scheme；文本、附件说明、选项和图片数量均有上限。
- 不执行前端任意 widget JSON 或脚本；choice 只有 schema 固定的问题与选项。
- 内置 Agent 删除 `cycledock_report_progress`、`cycledock_add_finding`、`cycledock_wait_for_human` 工具入口；外部 MCP 不再注册 `report_progress` / `add_finding`，统一改为 `reply_message`。
- `check_in`、`confirm_card`、`generate_work` 是真实业务操作，不能由文字气泡隐式替代。
- 原有历史 `progress/finding/result` 数据仍然可以读取，但不再成为新的内置 Agent 回复来源。

## 已知取舍

现阶段附件和图片 URL 仅接受 HTTPS；远程资源在前端渲染时可能触发外部请求。后续如需私有或本地文件，可复用 CycleDock Artifact 的授权读取接口。当前没有引入通用 UI 组件渲染器，choice 是唯一支持的交互 widget。

## Built-in Agent 正文流式预览

Built-in Runtime 在模型生成 `reply_message` 参数时，只预览顶层 `content` 字符串。Chat Completions 和 Responses 传输均可接收工具参数增量；未支持增量或返回普通 JSON 的服务仍在工具成功后展示完整消息。附件、图片和选择按钮等结构化字段只有在正式消息持久化后才展示，其他工具参数、自由模型文本及压缩摘要均不进入预览。

Runtime 使用 ADK `StreamingModeSSE`。模型适配器把已解码的正文增量转换成 `LLMResponse{Partial:true}` 与 `FunctionCall.PartialArgs`（路径 `$.content`），通过 ADK Runner 的事件迭代器转发。模型适配器不持有 SSE Hub，也不直接发布预览。生成批次和显示顺序通过临时 `CustomMetadata` 关联；Runner 转发时仍只允许回复正文，不转发其他工具参数或模型内部文本。

ADK 仅在完整响应到达后执行工具，Partial 事件不保存到 Session。工具持久化成功后，其 FunctionResponse 经 ADK Runner 返回，Runner 从响应中的 `event_id` 发布提交关联。模型路由重试和失败提示也通过 Partial 事件传递；调用方停止消费流时，模型请求立即取消。压缩摘要、标题生成使用非流式调用，不产生气泡预览。

- `reply.preview`：以当前调用的完整正文快照更新临时气泡，按模型请求和工具调用位置排序；正文尚未提交；已有正文时不显示呼吸圆点，仅在尚无正文的执行等待阶段使用圆点占位。
- `reply.committed`：工具事务成功后，用预览 ID 和正式 `event_id` 关联同一个气泡，并通知前端刷新 REST；PostgreSQL 仍是事实源，不持久化半截参数。
- `reply.discarded`：工具拒绝或失败时，标记临时气泡“消息未发送”。Attempt 结束时，未提交预览也标记为未发送。
- `reply.reset`：模型路由重试或进入下一次模型请求时，移除未提交预览；已提交消息的关联保留，避免与 REST 消息重复展示。
- `attempt.snapshot`：SSE 重连时恢复当前 Attempt 的预览、提交关联和开始时间。临时数据只存在于内存，单 Card 最多 128 个预览、总正文最多 256 KiB；正式消息仍通过 REST 恢复。

预览身份包含独立生成批次和工具调用 ID；同一调用的参数增量只更新自己的正文，路由重试不会复用上一批临时气泡。旧 Attempt 的预览、重置、提交和结束通知不能覆盖当前 Attempt。已经显示过预览的消息在 REST 到达后立即接替，普通批量新消息仍按既定节奏依次入场。
