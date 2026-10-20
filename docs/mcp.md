# MCP 设计

## 1. 定位

CycleDock MCP 是给外部 AI / Agent 的 Adapter。

典型客户端：

- ChatGPT
- Claude
- Codex
- AgentDock
- 其他支持 Remote MCP 的 Agent

内置 Cycle Agent 不调用自己的 MCP，而直接调用内部 Domain / Store。

## 2. MCP 必须遵守领域规则

MCP 不能绕过：

- Workspace scope。
- Card 状态机。
- Human-only Done。
- Trigger 唯一 Claim。
- current Run 唯一性。
- waiting_human。
- superseded stale result 防护。
- 幂等规则。

MCP handler 不复制这些规则，只调用统一领域能力。

## 3. 第一版能力方向

最终 JSON schema 在 Phase 3 实现时确定，当前只固定能力边界。

建议能力：

- list_projects
- list_cards
- get_card
- create_card
- update_card
- move_card
- add_comment
- add_event
- claim_card / claim_trigger
- report_progress
- finish_run
- confirm_card

是否合并某些工具，以正式 MCP schema 的可读性和幂等性为准，不追求工具数量。

## 4. 外部执行流程

```text
list_cards / get_card
→ 找到可执行工作
→ claim
→ 执行外部工具
→ report_progress / add finding
→ finish_run
→ confirm / 等待 Human Review
```

外部 Agent 不应该仅因为“看到了 Card”就直接执行而不 Claim。

## 5. Claim 语义

优先 Claim 当前最早的 pending Trigger。

创建 AgentRun 成功即 Claim 成功。

数据库 `UNIQUE(trigger_id)` 防止两个 Agent 同时 Claim 同一个 Trigger。

同 Card current Run 的 partial unique index 防止多个 current Run 并行出现。

如果外部 Scheduler 希望在没有 pending Trigger 时发起一次新的 follow-up，应通过显式、带幂等键的“创建 follow-up + Claim”语义完成；不要让普通 claim 在网络重试时静默制造无限新 Trigger。

该接口在 Phase 3 设计具体 schema 时必须专门做并发/重试测试。

## 6. Progress

Progress 是用户有意义的阶段变化，而不是 tool trace。

外部 Agent 可报告：

- current_stage
- progress_summary
- optional progress Event

不要逐个写：

- 每次 shell 命令。
- 每次 HTTP request。
- 每次 tool call 参数。
- heartbeat。

## 7. Finish

finish_run 需要明确区分：

- completed
- failed
- waiting_human

如果目标 Run 已 superseded / expired / cancelled，finish 必须拒绝其继续修改当前 Card。

## 8. OAuth

Remote MCP 使用标准 OAuth：

- Dynamic Client Registration。
- Authorization Code。
- S256 PKCE。
- Protected Resource Metadata。
- Bearer Access Token。
- Refresh Token rotation。

Human 在浏览器中批准当前 Workspace 授权。

OAuth Client 在 Workspace 内对应一个 oauth_client Source，所有 MCP Event / Run 都可追溯到该 Source。

## 9. Skill

正式 CycleDock Skill 只在 MCP 行为实际存在后编写。

不要在代码尚未实现时继续沿用旧版 CycleDock Skill 中“CycleDock 不运行 Agent / 不内置 Scheduler”等已经过时的规则。

新 Skill 应围绕：

- 如何寻找工作。
- 如何 Claim。
- 如何报告 Progress / Finding / Result。
- 如何处理 Ongoing Check-in。
- 如何进入 waiting_human。
- 如何 Confirm。
- 为什么不能 Human-only Done。
- 如何避免重复 Claim / 重复定时任务。
