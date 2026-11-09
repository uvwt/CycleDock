package agent

import "google.golang.org/adk/v2/session/compaction"

// compactionPromptTemplate 定义 ADK Tail Retention 上下文压缩的结构化摘要提示词。
//
// 在 Tail Retention 策略下，每次压缩会将上一次生成的摘要作为 seed 事件与新增事件一起重新压缩。
// 因此模板必须同时约束“工作阶段状态机（Completed / Active / Blocked / Next Move）”与“跨轮精确事实防退化”，
// 避免模型在压缩后丢失执行进度、反复分析相同代码或将未执行的计划误判为已完成。
const compactionPromptTemplate = `You are compacting the conversation history of a persistent CycleDock Card Session.
The history below may start from an earlier compaction summary followed by newer user turns, model responses, tool calls, and tool responses.

Update and produce a single structured checkpoint summary that lets the agent resume execution immediately without repeating completed read-only exploration.

CRITICAL RULES:
1. Explicitly state the primary language used by the user at the very top (e.g., "Conversation Language: 中文"). Write the summary content in that language while keeping the exact Markdown section headings below.
2. Latest execution results supersede older summaries: once an issue or blocker is resolved, move it out of Active/Blocked and never list resolved items as unfinished.
3. Only operations that were actually executed and confirmed by tool responses or verifiable outputs may be listed under "### Completed". If the agent only analyzed code, proposed a plan, or stated an intention without executing it, place that work under "### Active" or "## Next Move" — never under "### Completed".
4. Preserve raw failure evidence when repeated technical failures occur; never summarize a failed command, build, or test as succeeded.
5. Carry forward exact identifiers verbatim from both the previous summary and new events: file paths, line numbers, function/type/symbol names, test failure messages, Git branch names and commit hashes, device node_id, card_id, MCP tool_id, and externalized artifact references (artifact_ref and version). Never generalize an exact identifier into a vague label.
6. "## Next Move" must specify the concrete next action to execute (e.g., the exact file edit, command, or verification step when transitioning from analysis to implementation) so the agent advances instead of re-reading the same files. Do not state unverified hypotheses as established facts.
7. Never include hidden chain-of-thought, plaintext credentials, API keys, cookies, access tokens, private keys, or raw sensitive secrets.

Use this exact Markdown structure:

Conversation Language: <language>

## Objective
当前任务及最终验收目标。

## Requirements
用户明确要求、技术约束、不能违反的规则。

## Decisions
已经确定的技术方案、原因以及关键取舍。

## Work State
### Completed
已经完成的工作，以及能够验证的证据（工具结果、提交记录或 artifact_ref）。

### Active
当前正在进行但尚未完成的工作。

### Blocked
尚未解决的阻塞、失败原因与原始报错摘要；无阻塞时写“无”。

## Next Move
下一步应该立即执行的具体操作（明确指出应继续的实现或验证步骤，避免重复已完成的只读探索）。

## Relevant Files
继续任务需要的关键文件、路径、关键符号及用途。

## Important Context
不能丢失的技术结论、精确标识符（card_id、node_id、tool_id、artifact_ref、配置值等）和必要信息。

` + compaction.ConversationHistoryPlaceholder
