# CycleDock 前端从零重建 · 产品与实现基线

> 2026-10-22 最终修订：前端以 `092f521` 的 64px 图标导航与 Chat / Board 分离结构为产品基线重新开发；用户可见名称统一为 **Chat / Work**。本次不是整仓回退：后端继续以当前 `main` 为准，保留 Workspace 切换、Trigger lineage、OAuth、安全性与 SSE 等能力。后续章节与此修订冲突时，以本段和 `docs/product.md` 为准。

> 后续修订：本轮项目工作台优化以 [项目工作台优化方案与实施交接](project-workbench-plan.md) 为准。该方案已在 `feat/project-workbench-impl-20261021` 实现并进入验收收尾；其覆盖范围内的布局与交互决策替代本文早期的平级 Chat / Board 导航、桌面右侧详情常驻及从零重写要求，未涉及要求继续适用。

> 状态：2026-10-21 用户确认。本文是本次新前端的唯一设计基线；旧 `web/src` 代码不作为实现参考。对接后端以最新 `main`（Discussion/Todo/Ongoing 已分离）为准。

## 1. 目标与边界

- 前端代码从零开发；不复用旧页面、旧组件、旧样式或旧交互。保留 Go + PostgreSQL、OIDC、Model Provider、MCP 与 Card / Event / Turn / Attempt 后端业务域。
- 核心用户路径：Discussion 中澄清 → 唯一 Generate task 操作由 AI 识别派生 Todo 或 Ongoing → 独立任务页继续协作 → Review（人类决定 Done）。用户已经明确任务时，也可在 Board 直接手填 Todo，不调用模型。
- Discussion 与 Todo / Ongoing 都是 Card；一个 Card 对应一个持久 Agent Session，产品 Timeline 展示 durable Event，执行调度使用 Turn / Attempt，不另建第二套聊天业务模型。
- Project 仍是看板领域实体；Board 只是界面。
- PC / 移动端响应式，普通白、灰中性色，色彩只辅助交互/状态；默认可读与低视觉噪音。
- 数据隔离、Human-only Done、waiting_human、enqueue/interrupt 等领域规则由后端执行，前端只表达它们。

## 2. 页面与路由

顶层采用 `092f521` 风格的 **64px 图标 rail + Chat / Work 双入口**。桌面 rail 只把 Chat / Work 作为一级工作模式，底部只保留 Settings；使用正式 `cycledock-logo.png` 作为品牌入口，不再提供 Account / Workspace 快捷侧栏。账户、当前 Workspace 与退出登录统一放在 Settings → Account。移动端使用紧凑顶栏。Project 选择由各模式内部 UI 与 URL 驱动，Workspace scope 继续由服务端 Web Session 保证。

- `/`：跳转到 `/chat`。
- `/chat`：Chat 首页；当前 Project Discussion 列表 + 新讨论两态输入框。
- `/chat/:projectID/:cardID`：Discussion 对话详情；桌面保留 Discussion 列表，移动端从左抽屉打开。
- `/board` / `/board/:projectID`：Work 首页；Todo 四列 + Ongoing 区域，可直接创建 Human Todo。
- `/cards/:projectID/:cardID`：Todo / Ongoing 对话式工作详情；桌面左侧使用与 Discussion 同一视觉骨架的 Work 二级侧栏，按 Todo / Working / Review / Done / Ongoing 分组折叠，默认只展开当前任务所在分组；移动端从左抽屉打开。标题旁保留紧凑状态切换器，任务信息按钮固定在标题栏最右侧并使用锚定浮层按需打开，不保留右侧常驻栏或移动端详情抽屉。
- `/settings/*`：完整设置中心及二级导航。
- 旧 `/projects` 路由仅保留兼容跳转。Card URL 的 Project 与实际 Card 不一致时校正到真实 Project。
- Project 切换保持当前 Chat / Work 模式。Workspace membership/switch 后端能力继续保留，但本轮移除 Shell 的快捷切换侧栏；账户与当前 Workspace 信息统一从 Settings → Account 查看。
- 不引入新的 Board 实体或独立聊天 Session；Project 仍是数据范围，Board 只是 Work 模式的一种 UI。

## 3. 会话页（最高优先级）

- Discussion、Todo、Ongoing 的核心区域都是统一对话流：左侧 Agent / Result，右侧 Human，系统事件在中间居中灰字显示。Discussion 详情使用 Discussion 二级列表；Todo / Ongoing 使用 Work 二级列表，二者复用同一 Context Sidebar 基础组件，但业务数据与分组逻辑独立。
- 相同后端 `events` 表 / `timeline` 返回：comment(Human)=右，comment(Agent)、finding、result=左；status_change=居中小字；progress/checkin=简短阶段节点；confirm/reject 按重要性增强。
- 服务端 `sequence` 始终保留为真实审计顺序和去重依据；会话展示优先按稳定因果关系组织轮次。Session-first Runtime 使用 `Comment Event ← source_event_id ← Turn ← Attempt ← attempt_id ← Agent Event` 稳定关联；Attempt 产生的 Result/Finding 等产品 Event 携带 `attempt_id`，同一 Attempt 的执行状态与 durable Agent Event 应紧跟触发它的来源消息展示。缺少用户来源的 Schedule / System Turn 独立展示，不得按文本、时间或数组位置猜测归属。
- Todo/Ongoing 详情不保留右侧常驻栏。业务状态以标题旁紧凑状态切换器呈现；任务目标、来源 Discussion、Health / Schedule 等 durable 信息通过标题栏最右侧的信息按钮打开紧凑浮层。浮层默认阅读，Human 显式点击编辑后才显示保存/取消表单，移动端同样使用浮层而不是独立详情抽屉。
- 底部输入框有“收起态 / 展开聚合态”两种状态。收起时保持单行、尽量不占聊天高度；获得焦点后展开，running/queued 的“加入队列 / 立即转向”、waiting_human 的“回复并继续”、取消 Attempt 等控制全部聚合在输入框内部的底部控制区，不再另起一整行遮挡正文。
- Discussion 的 Generate task 只在输入框右上角显示 Sparkles 图标，通过 `title / aria-label` 保留可访问名称，不显示额外文字按钮。跳到最新同样只显示向下图标。
- Discussion 仅能生成任务而非转换状态，一个 Discussion 可生成多个工作；Todo/Ongoing 不再产生工作。
- Human 可以在 Board 直接打开“新建待办”表单创建 Todo；AI Generate work 仍然只来自 Discussion。
- 消息下方不逐条展示时间；只有相邻持久事件的时间差 **严格超过 2 小时** 时，才在两条事件之间插入居中的系统式绝对时间分隔。
- 已到消息底部时自动跟随增量；用户阅读旧消息时禁止强制滚动到底；显示仅图标的跳到最新按钮。
- 移动端不同时挤入左右栏：Discussion 详情从左抽屉打开 Discussion 列表，Todo / Ongoing 详情从左抽屉打开 Work 分组列表；Card / Discussion 信息继续由标题栏最右侧锚定浮层进入，中间聊天始终占满可用宽度；会话页不得再显示 Shell 全局移动顶栏。
- Markdown 安全渲染，代码块复制、可读排版，减少不必要的阴影和浮层。

## 4. 实时架构

不能通过 15 秒轮询模拟流式。必须完成 Go 模型层 → Agent Runner → 认证后 SSE → React 的真正增量传输。

- 传输：原生 EventSource，同源 HttpOnly Cookie；每个 Card 的只读 SSE 订阅基于后端鉴权范围。
- 短暂文本增量只存在于 Attempt 的实时流中；最终有意义的 Result/Finding/Progress/Comment 进入产品 `events`。其中 Attempt 产生的 durable Event 保存 nullable `attempt_id` 作为因果关联，不把它当作另一套执行状态。ADK Session Events 另行持久化模型消息、tool call 与 FunctionResponse 作为连续 Agent 上下文，但不保存 chain-of-thought 或 secret，也不把原始工具事件直接当作产品 Timeline 气泡。
- SSE 合约：`attempt.started`、`attempt.stage`、`attempt.text.delta`、`attempt.text.reset`、`attempt.text.snapshot`、`event.created`、`attempt.finished`、`attempt.failed`；执行标识统一为 `attempt_id`。
- Event / Card 最新持久状态的权威来源是 REST；流事件只负责低延迟和暂态 UI。重连时重新同步，避免丢消息/重复渲染。Agent 回复只展示持久 Message 气泡；Attempt 不保存结果摘要，SSE 只通知刷新和执行状态变化。
- 加载中提供轻量动态点或 Spinner 和真实阶段。运行耗时从 `started_at` 至 `finished_at` / 当前服务端时间计算；Queued 不计为运行时间。
- 单次 SSE 网络中断不能取消后台 Attempt，失败/等待人工/取消必须准确表达。
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

前端从零重建已进入 `main`。2026-10-21 的项目工作台优化以 `feat/chat-workbench-20261021@12966fb` 为实施基线，并在独立 worktree / 短期分支 `feat/project-workbench-impl-20261021` 完成；未推送、未合并、未部署。布局与交互以 `docs/project-workbench-plan.md` 为当前约束。

**已实现：** 根路由默认进入 Project Board；旧全局侧栏移除，顶栏使用 Project 选择器 + Account 菜单，并提供受 Membership 校验的 Workspace Session 切换。Discussion 详情使用 Discussion 二级列表，Todo/Ongoing 详情使用同一基础骨架的可折叠 Work 分组列表；Board 右下角提供新 Discussion Composer，首条 Comment 失败时持久保留原文、已创建 Card ID 与幂等键并安全重试。Card 详情保留独立 URL 与持久 Timeline；Todo/Ongoing 增加工作摘要、来源 Discussion / 派生工作入口。任务信息在桌面和手机均默认关闭且不预留右列，打开后默认只读，Todo/Ongoing 需显式编辑；Ongoing Schedule 也先阅读、后新增/编辑。Session-first cutover 后，pending Turn / cancel / retry / current Attempt 按稳定的 `source_event_id / turn_id / retry_of_attempt_id` 关联到用户 Comment，下游 UI 不再依赖 Trigger / AgentRun；无用户来源的 Schedule/System Turn 独立展示。

**本轮验证：** `npm run lint` 为 0 warning / 0 error；`npm run build` production build 通过。更新后的 `web/e2e/smoke.py` 对 mock server 执行 14 条 Python Playwright 浏览器 E2E，覆盖 Board 主工作台、无旧全局导航、A/B Project 数据隔离、Human 手工 Todo、项目 URL 直达/刷新/前进后退、375×812 移动端 Discussion/任务信息双抽屉、新 Discussion 创建失败草稿保留/跨 Project 草稿恢复/首发失败后同 Card 重试、编辑保存失败保留输入、Ongoing Schedule 阅读→编辑、语言/主题回归、Timeline 执行状态稳定挂载、无用户来源 Trigger 独立展示及 Workspace 切换，14/14 通过；新增覆盖 Work 二级侧栏状态折叠、信息按钮右对齐和 375×812 Work 抽屉。`sqlc generate`、`go test ./...`、`go vet ./...` 均通过；另外使用 TianYi 本机 PostgreSQL 17.11 测试实例真实执行 `TestHumanWorkControlHTTPFlowAndWorkspaceIsolation` 与 `TestHumanWorkspaceSwitchUsesMembershipScope`，每次均从 migration 000001 跑到 000007 后 PASS，确认并非因缺少 `TEST_DATABASE_URL` 而 skip。TianYi Neo 对 1408×757 桌面真实构建确认 Board/共享 Discussion/按需任务信息与时间线执行状态无控制台错误；375×812 手机布局另由 Playwright 实际 viewport E2E 验证，Neo 当前自动附着外部 Edge 时无法覆盖已有 viewport，因此不把其桌面尺寸冒充手机证据。

**尚未通过生产级验收：**
1. 关键新增 HTTP 路径已在真实 PostgreSQL 17 上运行并通过；正式部署环境的 SSE 网络断线/重连、跨实例事件广播与长时间运行竞态仍需要持续验证。
2. OpenAI-compatible、ChatGPT、Grok 真实外部账号及完整模型流需要授权环境再次验证；测试夹具不代表真实模型调用。
3. 模型流多进程部署的即时增量采用当前单实例内存 Hub；跨实例一致的实时传输需要扩展。持久 Event 用服务端每 1.5 秒检查序列变化并推送 SSE，客户端没有继续使用 15 秒轮询。
4. i18next 与主题切换已经落地并纳入 E2E，但高级设置仍未做到完整双语；Shiki、Motion、Vitest 等增强项仍按实际价值后续引入，不为了技术清单强行加依赖。
5. 前端从零重建基线已于 2026-10-21 经 PR #7 squash 合并（7afb749）并部署 RN DEV；**本节描述的项目工作台优化仍只在本地短期分支 `feat/project-workbench-impl-20261021`，未经用户要求未推送、未合并、未部署。**

## 9. 语言与主题偏好（后续独立 PR）

- 设置 → 常规：可切换中文/English、浅色/深色/跟随系统。默认中性白灰色浅色主题；配色与语言偏好只存在浏览器 localStorage，不写入 Workspace 或用户账号。
- 技术实现：i18next + react-i18next，Tailwind v4 颜色 CSS Variables 支持夜间模式，System 跟随媒体查询变化。另有 localStorage storage 事件同步多标签偏好。
- 当前翻译覆盖导航、登录、Chat/Board 页面、任务页常用动作、设置导航和 Agent 设置主要操作；Model/MCP 高级配置、任务抽屉等仍含中文，不应宣传为完整双语。
- 浏览器 E2E 新增语言切换、主题变量生效、刷新后的偏好恢复、原有页面交互回归。系统/用户实际模型文本及错误消息不自动翻译。
