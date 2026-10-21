# CycleDock 前端从零重建 · 产品与实现基线

> 状态：2026-10-21 用户确认。本文是本次新前端的唯一设计基线；旧 `web/src` 代码不作为实现参考。对接后端以最新 `main`（Discussion/Todo/Ongoing 已分离）为准。

## 1. 目标与边界

- 前端代码从零开发；不复用旧页面、旧组件、旧样式或旧交互。保留 Go + PostgreSQL、OIDC、Model Provider、MCP 与 Card / Event / Trigger / Run 后端业务域。
- 核心用户路径：Discussion 中澄清 → 唯一 Generate task 操作由 AI 识别派生 Todo 或 Ongoing → 独立任务页继续协作 → Review（人类决定 Done）。用户已经明确任务时，也可在 Board 直接手填 Todo，不调用模型。
- Discussion 与 Todo / Ongoing 都是 Card，统一使用 Card Timeline、Trigger、Run；绝不另建聊天 Session 或另一套 Chat Message 数据模型。
- Project 仍是看板领域实体；Board 只是界面。
- PC / 移动端响应式，普通白、灰中性色，色彩只辅助交互/状态；默认可读与低视觉噪音。
- 数据隔离、Human-only Done、waiting_human、enqueue/interrupt 等领域规则由后端执行，前端只表达它们。

## 2. 页面与路由

顶层是 Chat、Board、Settings 三个界面域。**桌面端只保留一条窄全局侧轨**承载 Chat / Board / Settings，不再叠加第二条全局顶栏；会话页自己的顶部只负责当前 Card 标题、Run 状态和局部面板入口，不重复全局导航。移动端的全局顶栏只出现在顶层页面，进入 Card 会话后隐藏，由会话自身单一工具栏接管。所有卡片进入独立 URL 页面，绝非 Dialog。

- `/chat`：桌面左侧固定 Discussion 列表，中间为大模型聊天式欢迎态和输入框；移动端列表按需从左侧抽屉打开。欢迎态直接输入第一句话会自动创建 Discussion、提交首条 Comment 并进入会话，标题与描述仍由 Agent 生成。
- `/chat/:projectID/:cardID`：Discussion 的独立会话页，唯一 Generate task 操作，归档/删除。
- `/board/:projectID`：看板（Todo、Working、Review、Done；Ongoing 单独区域）；拖拽遵守后端状态机。
- `/cards/:projectID/:cardID`：Todo/Ongoing 独立工作会话页。
- `/settings/*`：完整设置中心及二级导航。
- 根路由跳转 Chat；现有旧 /projects 路由可作为不影响新架构的跳转兼容入口（非复用旧页面）。
- 项目范围通过当前选择项目决定；不会设计新的 Board 数据模型。

## 3. 会话页（最高优先级）

- 主要篇幅是统一对话流，左侧 AI / Agent 消息、右侧 Human 消息，系统事件在中间居中灰字显示。Discussion 桌面布局为“左 Discussion 列表 / 中聊天 / 右 Card 详情”，Todo / Ongoing 为“中聊天 / 右 Card 详情”；右侧详情默认常驻，不要求先点按钮。
- 相同后端 `events` 表 / `timeline` 返回：comment(Human)=右，comment(Agent)、finding、result=左；status_change=居中小字；progress/checkin=简短阶段节点；confirm/reject 按重要性增强。
- 事件按服务端 `sequence` 顺序排布，保留事件 ID 以去重；绝不可打乱用户消息、Agent 结果及任务状态的交错次序。
- 标题与状态放在会话顶部；Description、当前 Run、Queue、Work History、Ongoing Schedule 在桌面右侧详情栏常驻，在窄屏/移动端才进入 **任务详情抽屉**。
- 底部输入框有“收起态 / 展开聚合态”两种状态。收起时保持单行、尽量不占聊天高度；获得焦点后展开，running/queued 的“加入队列 / 立即转向”、waiting_human 的“回复并继续”、取消 Run 等控制全部聚合在输入框内部的底部控制区，不再另起一整行遮挡正文。
- Discussion 的 Generate task 只在输入框右上角显示 Sparkles 图标，通过 `title / aria-label` 保留可访问名称，不显示额外文字按钮。跳到最新同样只显示向下图标。
- Discussion 仅能生成任务而非转换状态，一个 Discussion 可生成多个工作；Todo/Ongoing 不再产生工作。
- Human 可以在 Board 直接打开“新建待办”表单创建 Todo；AI Generate work 仍然只来自 Discussion。
- 消息下方不逐条展示时间；只有相邻持久事件的时间差 **严格超过 2 小时** 时，才在两条事件之间插入居中的系统式绝对时间分隔。
- 已到消息底部时自动跟随增量；用户阅读旧消息时禁止强制滚动到底；显示仅图标的跳到最新按钮。
- 移动端不同时挤入左右栏：Discussion 列表从左抽屉进入，Card 详情从右抽屉进入，中间聊天始终占满可用宽度；会话页不得再显示 Shell 全局移动顶栏。
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

前端从零重建已进入 `main`。2026-10-21 的会话工作台修订在独立 worktree / 分支 `feat/chat-workbench-20261021` 上继续演进；`docs/frontend-rebuild.md` 继续作为当前设计约束，不回退到旧版双顶栏、Discussion 卡片宫格或桌面 Drawer-only 详情。

**已实现：** ChatGPT 类 Discussion 工作台、桌面 Discussion 左列表 / 中聊天 / 右详情、Todo/Ongoing 桌面右详情常驻、移动端双抽屉、两态聚合输入框、图标式 Generate / Jump latest、超过 2 小时才显示的时间分隔、Human Board 手工创建 Todo，以及原有统一 Timeline、七项 Settings、同源认证 SSE、Go Runner 模型文本增量中转、Responses/Chat Completions 流解析、EventSource 重连 REST 恢复。设计系统继续基于 Tailwind + 自持 UI 组件 + Radix Sheet，Lucide 统一图标。

**本轮验证：** React/TypeScript production build 与 Oxlint 已通过；`web/e2e/smoke.py` 对 `web/e2e/mock-server.mjs` 执行 6 条 Python Playwright E2E，覆盖 Discussion 三栏、两态输入框、2 小时时间分隔、首轮消息自动建 Discussion、Human 手工 Todo、桌面常驻详情、SSE 增量、移动端单导航 + 左右抽屉、语言和主题，6/6 通过。Go 全量 `go test ./...` 与 `go vet ./...` 通过；另使用 TianYi 本机 PostgreSQL 17 二进制创建独立临时数据库，执行真实 `TestHumanWebHTTPFlowAndTenantIsolation`，迁移 1–7 与 Human 手工 Todo HTTP 路径通过，测试后实例和数据目录已清理。TianYi Neo 对 1408px 桌面实际构建核验为：左 Discussion 约 279px、中间聊天约 704px、右详情 360px，`scrollWidth == viewport width`，无横向覆盖；Generate 只保留图标和可访问名称。

**尚未通过生产级验收：**
1. 本轮已跑新增 Human Todo 所在的真实 PostgreSQL HTTP 集成路径，但没有为所有包配置持久 `TEST_DATABASE_URL` 后重跑整套数据库集成矩阵；正式 SSE 网络断线/重连竞态仍需要在部署环境持续验证。
2. OpenAI-compatible、ChatGPT、Grok 真实外部账号及完整模型流需要授权环境再次验证；测试夹具不代表真实模型调用。
3. 模型流多进程部署的即时增量采用当前单实例内存 Hub；跨实例一致的实时传输需要扩展。持久 Event 用服务端每 1.5 秒检查序列变化并推送 SSE，客户端没有继续使用 15 秒轮询。
4. i18next 与主题切换已经落地并纳入 E2E，但高级设置仍未做到完整双语；Shiki、Motion、Vitest 等增强项仍按实际价值后续引入，不为了技术清单强行加依赖。
5. 前端从零重建基线已于 2026-10-21 经 PR #7 squash 合并（7afb749）并部署 RN DEV；**本节描述的会话工作台修订仍在 `feat/chat-workbench-20261021`，尚未合并或部署。**

## 9. 语言与主题偏好（后续独立 PR）

- 设置 → 常规：可切换中文/English、浅色/深色/跟随系统。默认中性白灰色浅色主题；配色与语言偏好只存在浏览器 localStorage，不写入 Workspace 或用户账号。
- 技术实现：i18next + react-i18next，Tailwind v4 颜色 CSS Variables 支持夜间模式，System 跟随媒体查询变化。另有 localStorage storage 事件同步多标签偏好。
- 当前翻译覆盖导航、登录、Chat/Board 页面、任务页常用动作、设置导航和 Agent 设置主要操作；Model/MCP 高级配置、任务抽屉等仍含中文，不应宣传为完整双语。
- 浏览器 E2E 新增语言切换、主题变量生效、刷新后的偏好恢复、原有页面交互回归。系统/用户实际模型文本及错误消息不自动翻译。
