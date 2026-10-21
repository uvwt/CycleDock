# CycleDock 前端从零重建 · 产品与实现基线

> 状态：2026-10-21 用户确认。本文是本次新前端的唯一设计基线；旧 `web/src` 代码不作为实现参考。对接后端以最新 `main`（Discussion/Todo/Ongoing 已分离）为准。

## 1. 目标与边界

- 前端代码从零开发；不复用旧页面、旧组件、旧样式或旧交互。保留 Go + PostgreSQL、OIDC、Model Provider、MCP 与 Card / Event / Trigger / Run 后端业务域。
- 核心用户路径：Discussion 中澄清 → 唯一 Generate task 操作由 AI 识别派生 Todo 或 Ongoing → 独立任务页继续协作 → Review（人类决定 Done）。
- Discussion 与 Todo / Ongoing 都是 Card，统一使用 Card Timeline、Trigger、Run；绝不另建聊天 Session 或另一套 Chat Message 数据模型。
- Project 仍是看板领域实体；Board 只是界面。
- PC / 移动端响应式，普通白、灰中性色，色彩只辅助交互/状态；默认可读与低视觉噪音。
- 数据隔离、Human-only Done、waiting_human、enqueue/interrupt 等领域规则由后端执行，前端只表达它们。

## 2. 页面与路由

顶层是 Chat、Board、Settings 三个界面域。顶栏左侧品牌/返回，右上角 Chat / Board 快速切换及菜单/抽屉入口。默认不显示永久全局侧边栏，导航抽屉按需开启。所有卡片进入独立 URL 页面，绝非 Dialog。

- `/chat`：Discussion 列表、搜索/打开、新建 Discussion；空白 Discussion 首条消息即会话开始，标题与描述由 Agent 生成。
- `/chat/:projectID/:cardID`：Discussion 的独立会话页，唯一 Generate task 操作，归档/删除。
- `/board/:projectID`：看板（Todo、Working、Review、Done；Ongoing 单独区域）；拖拽遵守后端状态机。
- `/cards/:projectID/:cardID`：Todo/Ongoing 独立工作会话页。
- `/settings/*`：完整设置中心及二级导航。
- 根路由跳转 Chat；现有旧 /projects 路由可作为不影响新架构的跳转兼容入口（非复用旧页面）。
- 项目范围通过当前选择项目决定；不会设计新的 Board 数据模型。

## 3. 会话页（最高优先级）

- 主要篇幅是统一对话流，左侧 AI / Agent 消息、右侧 Human 消息，系统事件在中间居中灰字显示。
- 相同后端 `events` 表 / `timeline` 返回：comment(Human)=右，comment(Agent)、finding、result=左；status_change=居中小字；progress/checkin=简短阶段节点；confirm/reject 按重要性增强。
- 事件按服务端 `sequence` 顺序排布，保留事件 ID 以去重；绝不可打乱用户消息、Agent 结果及任务状态的交错次序。
- 标题与状态放在顶部；Description、当前 Run、Queue、Work History、Ongoing Schedule 放在可展开的 **任务详情抽屉**，不长期占据消息区。
- 底部固定输入框，正常为“发送并执行”；running/queued 提供“加入队列”及“立即转向”；waiting_human 提供“回复并继续”；归档 Discussion 禁止提交。
- Discussion 仅能生成任务而非转换状态，一个 Discussion 可生成多个工作；Todo/Ongoing 不再产生工作。
- 已到消息底部时自动跟随增量；用户阅读旧消息时禁止强制滚动到底；显示跳到最新按钮。
- Markdown 安全渲染，代码块复制、可读排版，减少不必要的阴影和浮层。

## 4. 实时架构

不能通过 15 秒轮询模拟流式。必须完成 Go 模型层 → Agent Runner → 认证后 SSE → React 的真正增量传输。

- 传输：原生 EventSource，同源 HttpOnly Cookie；每个 Card 的只读 SSE 订阅基于后端鉴权范围。
- 短暂文本增量仅保存在 Run 的临时流中；最终有意义的 Result/Finding/Progress/Comment 入原有 events 表，不把原始工具参数/结果/CoT 持久化。
- SSE 合约建议：`run.started`、`run.stage`、`run.text.delta`、`event.created`、`run.finished`、`run.failed`；`id`/游标应可重连、幂等消重。
- Event / Card 最新持久状态的权威来源是 REST；流事件只负责低延迟和暂态 UI。重连时重新同步，避免丢消息/重复渲染。
- 加载中提供轻量动态点或 Spinner 和真实阶段。运行耗时从 `started_at` 至 `finished_at` / 当前服务端时间计算；Queued 不计为运行时间。
- 单次 SSE 网络中断不能取消后台 Agent Run，失败/等待人工/取消必须准确表达。
- OpenAI-compatible Transport 也要支持增量输出；不能只对 ChatGPT/Grok 实现。
- 对不支持推流的外部 Agent，按它生成的 Event 实时呈现，不虚构模型 Token。

## 5. 设置中心（7 个二级页面）

| 页面 | 功能 |
| --- | --- |
| 常规 | 语言、中性主题及后续显示偏好 |
| 账户 | 当前 Human Identity / Workspace、退出登录；Hosted 身份在 NexusDock 管理 |
| 模型服务 | ChatGPT、Grok、OpenAI-compatible；OAuth 授权/断开、模型选择、测试 |
| Agent 设置 | Agent 开关、模型超时、最大 Tool Rounds、Workspace Agent Instructions |
| NexusDock | 第一方设备授权状态、连接、断开及跳转 NexusDock 管理页 |
| MCP 服务 | 自定义 MCP 列表、新建、修改、删除、测试；环境变量 Token 引用 |
| 项目设置 | 当前 Project 名称、描述、Project Instructions |

Agent/连接设置保留 owner 权限。不要把模型账号和 CycleDock 登录身份混淆，不要将 Workspace 级和 Project 级指令混淆。没有实现过的 Skill 管理、订阅账单、通知等不放空菜单。

## 6. 技术选择

React + TypeScript + Vite + React Router + TanStack Query + Tailwind CSS 4。基础设计采用 shadcn/ui 风格的源代码自持组件和 Radix Accessible primitives；Lucide 图标保持统一；Board 使用 dnd-kit；React Hook Form / Zod 表单验证；react-markdown + remark-gfm、代码高亮按需加载；Sonner Toast；date-fns 时间。推荐未来 i18next + react-i18next、Motion、Vitest、Testing Library、Playwright，但新增包必须先测试可用且按需装入。不使用 Redux/Next.js/GraphQL/Socket.IO，不在浏览器直接使用模型/MCP Secret。

代码按职责组织：`app/`、`layouts/`、`features/chat/`、`features/board/`、`features/cards/`、`features/timeline/`、`features/settings/`、`shared/api/`、`shared/realtime/`、`shared/ui/`。Chat 和 Task 复用 Timeline 组件，SSE 客户端统一提供运行事件/断线恢复。

## 7. 质量与验收

- 移除旧 `web/src` 的组件代码，只依据产品基线、后端 HTTP Contract 和规范重新实现；不要粘贴或改造旧 JSX。
- 真实 API/鉴权与作用域不变。Chat、Board、Card Page、Settings 7 页、登录、OAuth 回调和所有主要写操作可用。
- 真实验证：TypeScript build、lint、前端主要交互测试、Go tests，SSE E2E 需覆盖流增量、重连消重、权限隔离、interruption、terminal。
- 断线、异步回调、失败、移动端、滚动位置和无数据状态都要验证。
- 产品设计以本文为准；未完成的能力须明确标记，禁止以视觉占位冒充实现。

## 8. 当前开发实现与未关闭事项（2026-10-21）

本次 worktree `feat/frontend-rebuild-20261021` 从已 squash 合并 Discussion 模型的 `origin/main` 提交 `89fabf9` 创建。旧 `web/src` 整体删除并新建目录；`docs/frontend-rebuild.md` 是设计约束。

**已实现：** Chat、Board、Discussion/Todo/Ongoing 独立详情页、统一 Timeline 消息排版、可展开任务详情/队列/历史/日程、七项 Settings 页面、同源认证 SSE、Go Runner 模型文本增量中转、Chat Completions 与 Responses Stream 解析、EventSource 重连后 REST 恢复、Run 起止时间字段。设计系统基于 Tailwind + 自持 UI 组件 + Radix Sheet，Lucide 统一图标。

**已验证：** React/TypeScript 生产构建、Oxlint、Go 编译/vet/单元测试；TianYi Neo 使用测试夹具验证桌面与 390px 移动端 Chat/Board/Card/Settings，卡片 URL 页面而非弹窗，模拟 SSE 文本增量和运行时计时可见。新增 `web/e2e/smoke.py` 并使用本机 Python Playwright 对测试夹具执行三条自动化 E2E（Chat 创建和消息、Card 详情与流式、Settings 与移动端），3/3 通过。Mock 服务为 `web/e2e/mock-server.mjs`，测试夹具不进入生产包。

**尚未通过生产级验收：**
1. `TEST_DATABASE_URL` 未配置，TianYi 没有 Docker CLI；本机未执行 PostgreSQL 集成测试以及正式 SSE 网络/断线竞态 E2E。
2. OpenAI-compatible、ChatGPT、Grok 真实外部账号及完整模型流需要授权环境再次验证；测试夹具不代表真实模型调用。
3. 模型流多进程部署的即时增量采用当前单实例内存 Hub；跨实例一致的实时传输需要扩展。持久 Event 用服务端每 1.5 秒检查序列变化并推送 SSE，客户端没有继续使用 15 秒轮询。
4. i18next 双语完整覆盖、主题切换、Shiki 语法高亮、Motion 高级动效、Vitest/Playwright 完整自动化套件尚未纳入；首次开发基线先锁定已安装并可构建的依赖，避免未验证的新增 SDK。
5. 未合并/部署。正式发布前还须验证实际 OIDC/OAuth 回调、反代 SSE 缓冲、权限隔离、断线后结果同步与移动端输入法体验。
