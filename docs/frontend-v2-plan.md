# CycleDock Frontend V2 · 从零重构方案

> 状态：**正在实现与验收：Phase 1 Showcase 已通过；Phase 2–5 主界面与业务链路已落地；Phase 6 正在回归。具体实现记录见第 19 节。**
>
> 基线：`main@3c9c024`（2026-10-21 quick-adjust 已合入本地 main）。
>
> 开发分支：`feat/frontend-v2-20261021`。
>
> 独立 worktree：`C:\Users\Administrator\CycleDock-frontend-v2`。
>
> 本文是下一轮 Frontend V2 的实施基线。V2 不继续在当前 UI 上修修补补，而是在保留后端领域模型、API 契约和已经确认的产品布局方向的前提下，**从 UI / 页面层重新实现**。

## 1. 为什么要重做

当前前端底层技术并不落后，问题主要出在 UI 层和页面组织：

- 页面大量直接书写 Tailwind class，缺少强约束的 Design System。
- `shared/ui/components.tsx` 太薄，Button / Select / Dialog / Popover / Menu / Typography 等没有形成统一规范。
- 字号、控件高度、圆角、阴影、辅助色在不同页面各自决定，导致同屏视觉层级不一致。
- 原生 `<select>` 在不同浏览器 / 系统缩放下视觉差异大。
- Discussion / Work / Settings 的页面结构经过多轮迭代后出现较多局部补丁。
- Loading / Empty / Error / Mutation Pending 没有统一表达方式。
- 当前页面已经验证了核心产品方向，适合把这些产品决策保留，但不适合继续复用现有页面组件和样式实现。

因此 V2 的原则是：

> **保留业务，保留成熟 React 基础设施；重做 Design System、组件层、路由组织和所有业务页面。**

## 2. 不变的领域模型与产品硬约束

V2 不改变后端领域，也不为了前端重构发明第二套聊天模型。

必须继续遵守：

- Project 即项目，不增加 Board 领域实体。
- Card 只有 Discussion / Todo / Ongoing。
- Discussion 不能转换为 Todo / Ongoing，只能通过 Generate work 派生工作。
- Todo 状态固定为 `Todo → Working → Review → Done`，Review → Done 仍只允许 Human。
- Trigger 表示一次不可变工作方向。
- AgentRun 表示一次具体执行；一个 Card 同时最多一个 current Run。
- waiting_human、enqueue、interrupt、superseded 等语义以服务端状态机为准。
- Timeline 展示的是持久 Event / Result / Finding / Progress / Check-in 等产品事实，不展示 raw tool trace 或 chain-of-thought。
- Ongoing health 继续由最新 Check-in 派生。
- 所有 Workspace / Project scope 与权限由服务端保证，前端不复制安全边界。

V2 的聊天/协作 UI 必须适配 `Card / Event / Trigger / Run`，不能反过来把领域模型改造成普通 `Thread / Message / Generation`。

## 3. 当前功能面：V2 必须完整保留

### 3.1 Workspace / Project

- Workspace 切换。
- Project 列表与项目切换。
- 新建 Project。
- Project Settings。
- 切换 / 新建 Project 时保持当前一级模式（Discussion / Work），不能隐式跳到 Work。
- URL 直达、刷新、前进后退保持正确 Project。

### 3.2 Discussion

- Discussion 列表与搜索。
- 新建 Discussion。
- 首条消息失败后的草稿保护和安全重试。
- Discussion Timeline。
- Human / Agent 消息。
- 系统事件。
- 关联工作。
- 归档 / 删除。
- Generate work。
- SSE 实时增量。

### 3.3 Work

- Todo 四列：Todo / Working / Review / Done。
- Human 手工创建 Todo。
- dnd-kit 拖拽状态。
- 待我审核。
- Ongoing 列表 / 状态 / Check-in / Health。
- Ongoing Schedule。
- Todo / Ongoing 独立详情页。
- 来源 Discussion。

### 3.4 Trigger / Run

- queued / running / waiting_human。
- enqueue / interrupt。
- cancel queue / cancel current run。
- retry。
- superseded。
- Agent 执行耗时属于具体 Run，显示在对应 Agent 执行消息上，不放在 Card 标题旁。
- 执行失败 / 运行 / 排队等状态作为独立 Agent 消息呈现，不塞入 Human 气泡。

### 3.5 Settings

- 常规：语言、主题。
- 账户 / Workspace。
- Model Provider / OAuth。
- Agent Settings。
- NexusDock。
- MCP Connections。
- Project Settings。
- 权限限制与错误提示。

## 4. V2 技术栈

### 4.1 保留

以下技术继续使用，不做无价值迁移：

- React 19
- TypeScript
- Vite
- React Router
- TanStack Query
- Tailwind CSS v4
- dnd-kit
- Lucide React
- React Hook Form
- Zod
- Sonner
- react-markdown + remark-gfm
- i18next + react-i18next
- date-fns

### 4.2 新增 / 正式采用

#### shadcn/ui + Radix primitives

作为 V2 的主 UI 组件体系。

shadcn/ui 不是不可修改的黑盒 npm 主题，而是把组件源码放入项目；CycleDock 继续拥有自己的视觉规范。

优先建立以下基础组件：

- Button
- IconButton
- Input
- Textarea
- Select
- Checkbox
- RadioGroup
- Switch
- Dialog
- AlertDialog
- Sheet
- Popover
- DropdownMenu
- Tooltip
- Tabs / Segmented control
- Avatar
- Badge / StatusDot
- Separator
- ScrollArea
- Skeleton
- EmptyState
- ErrorState
- LoadingButton

#### class-variance-authority

用于 Button / Badge / Input size / variant 等明确变体，避免页面拼接大量 class。

#### clsx + tailwind-merge

统一 `cn()`，避免 Tailwind class 冲突。

#### @tanstack/react-virtual

用于长 Timeline、Discussion 长列表等场景，避免长期使用后 DOM 数量无限增长。

### 4.3 不采用

V2 不引入：

- Ant Design
- MUI
- Chakra UI
- Mantine
- Redux
- Zustand（当前没有必要）
- Next.js
- GraphQL
- 重型富文本编辑器
- 大型 Kanban 框架

原因：CycleDock 已经有稳定的 Vite + React + Query 基础，以上方案要么设计语言过强，要么引入与当前问题无关的架构复杂度。

### 4.4 assistant-ui 的决定

assistant-ui 可参考其 Chat UX，但 **不作为 CycleDock 的核心 runtime**。

原因：

- CycleDock 的真实模型是 Card / Event / Trigger / Run。
- Timeline 包含 Comment / Progress / Finding / Result / Check-in / status_change / queued Trigger / Run history。
- assistant-ui 天然更偏 Thread / Message / Generation。
- 为适配 assistant-ui 而反向扭曲领域模型不值得。

可以借鉴它的 Message / Composer / Streaming UX，但核心 Timeline、执行状态和后端同步由 CycleDock 自己实现。

## 5. Design System 先于业务页面

V2 第一阶段必须先完成 Design System 和 UI Showcase。**Showcase 未确认前，不大规模实现业务页面。**

### 5.1 字体层级

默认使用系统 UI 字体栈，中文优先系统字体。

建议限制为少数语义规格：

- 12px：辅助信息、时间、弱元数据。
- 13px：导航、表单控件、次级正文。
- 14px：普通正文、消息正文。
- 16px：区块标题、Card 标题。
- 20–22px：页面标题。

避免页面自行引入 11 / 18 / 24 / 30px 等额外层级，除非有明确产品理由。

### 5.2 控件高度

只保留少量规格，例如：

- compact：32px
- default：36px
- prominent：40px

Select / Input / Button 必须共享高度体系。

### 5.3 圆角

只保留少量 token：

- small：6px
- default：8px
- large：12px

避免每个页面随手使用 rounded-md / lg / xl / 2xl / 3xl。

### 5.4 色彩

- 页面主体：白 / 灰中性色。
- 品牌强调：CycleDock 蓝 / 青。
- 成功 / 警告 / 错误 / 运行中：只在有真实语义时使用。
- 不把每个状态都做成彩色 Badge。
- 不使用大面积玻璃拟态或高饱和渐变作为常规容器。

### 5.5 品牌

继续使用已经确认的 CycleDock 三色几何 C Logo：

- 左上角品牌入口。
- 登录页。
- favicon。
- apple-touch-icon / PWA 图标（若启用）。
- 其他品牌空状态。

当前资产：`web/public/cycledock-logo.png`。

## 6. 整体布局

V2 保留最近已经确认的产品布局概念，但不复用当前 JSX / CSS。

### 6.1 桌面

仅保留一个左侧栏：

```text
┌───────────────┬─────────────────────────────────┐
│ CycleDock     │                                 │
│               │                                 │
│ 讨论 | 工作   │            主内容区              │
│               │                                 │
│ 当前模式内容   │                                 │
│               │                                 │
│               │                                 │
│ Project       │                                 │
│ Account       │                                 │
└───────────────┴─────────────────────────────────┘
```

左侧栏顶部：

- CycleDock Logo。
- Discussion / Work 一级模式切换。

左侧栏底部：

- Project selector。
- Account / Workspace / Settings / Logout。

### 6.2 Discussion 模式

左栏模式内容：

- 搜索框。
- 搜索框右侧“新建 Discussion”图标按钮。
- Discussion 列表。

右侧：

- Discussion 首页或具体 Discussion Thread。
- Human 在右。
- Agent 在左，并显示 Agent Avatar。
- Agent 内容使用统一消息容器。
- 系统事件居中弱化显示。
- Composer 固定在底部但不遮挡正文。

Discussion / Task 信息不再常驻右侧占一列：

- Desktop：优先 Popover。
- Mobile：空间不足时使用 Sheet / Drawer。
- 点击外部或 Esc 关闭。
- 不重复标题和无价值说明。

### 6.3 Work 模式

左栏模式内容：

- 全部工作。
- 待我审核。
- 持续事项。

右侧：

- Todo / Working / Review / Done。
- Ongoing 独立区域。
- 详情仍使用统一 Thread / Timeline，而不是另一套详情系统。

### 6.4 新建 Project

必须使用居中 Dialog。

- 不从右边弹出。
- 不通过跳转 Board 才能打开。
- Discussion 中新建成功后仍处于 Discussion。
- Work 中新建成功后仍处于 Work。

## 7. 路由建议

V2 建议把 Project 和 mode 都明确放入 URL。

目标路由：

```text
/projects/:projectID/discussions
/projects/:projectID/discussions/:cardID

/projects/:projectID/work
/projects/:projectID/work/:cardID

/settings/:section
```

旧路由保留 redirect：

```text
/chat
/chat/:projectID/:cardID
/board/:projectID
/cards/:projectID/:cardID
```

规则：

- 切换 Project 只替换 `:projectID`，不改变 mode。
- 切换 Discussion / Work 只改变 mode，不丢 Project。
- Card 直达 URL 与实际 Project 不一致时按服务端事实纠正。
- Filter 尽量放 URL query，保证刷新 / 前进后退可恢复。

## 8. Timeline / Agent 消息设计

Timeline 继续以服务端 Event sequence 为唯一顺序依据。

### Human

- 右侧消息。
- 不把 Agent 执行状态直接塞在 Human 消息框里。

### Agent

- 左侧 Agent Avatar。
- Agent 消息框。
- Finding / Result / Check-in 根据语义做轻量差异，不发明多套卡片。

### Run / Trigger

- queued / running / failed / completed / retry / cancel 使用 Agent 执行消息表达。
- Run duration 属于具体 Run，显示在该 Agent 执行消息的右侧。
- 实时 SSE 内容与同一个 Run 的消息合并，避免重复 Agent 气泡。
- 没有用户来源的 Schedule Trigger 独立展示。
- 不能按文本、时间接近或数组位置猜测 Trigger 与 Event 关联。

## 9. Board

继续使用 dnd-kit，不引入大型 Kanban 库。

重点保证：

- Todo → Working → Review → Done。
- Done 仍只允许 Human。
- 拖拽失败按服务端结果回滚 / 刷新。
- 移动端横向列滚动可用。
- Ongoing 不伪装成 Todo 第五列。

## 10. Settings V2

Settings 需要明显重做。

Desktop 使用 Settings 内部自己的纵向二级导航，而不是把大量入口横向铺在页面顶部：

```text
设置

个人
  常规
  账户

AI
  模型服务
  Agent 设置

外部连接
  NexusDock
  MCP

工作空间
  项目设置
```

Mobile 使用紧凑入口 / Sheet，不把桌面导航硬压缩。

所有复杂表单统一使用：

- React Hook Form
- Zod
- shadcn Form primitives

不再每个设置页手写大量 `useState + validate + save`。

## 11. Skeleton / Loading / Empty / Error

V2 必须系统性加入骨架屏。

基础组件：

- Skeleton
- EmptyState
- ErrorState
- LoadingButton

业务骨架：

- SidebarSkeleton
- ProjectSwitcherSkeleton
- DiscussionListSkeleton
- ThreadSkeleton
- BoardSkeleton
- CardDetailSkeleton
- SettingsSkeleton

规则：

1. Skeleton 用于“结构已知、数据未返回”的首次加载。
2. Mutation 使用按钮内部 loading，不用 Skeleton。
3. TanStack Query refetch 默认保留旧内容，不把页面重新变成骨架。
4. Skeleton 延迟约 150–200ms 再展示，避免快速请求造成闪烁。
5. ThreadSkeleton 模拟真实左右消息结构，降低内容回来时的布局跳动。
6. BoardSkeleton 模拟列头和卡片，而不是整页一个 Spinner。
7. ErrorState 必须提供明确错误和可重试动作。
8. EmptyState 与 loading / error 明确区分。

## 12. 状态管理原则

不新增全局状态框架。

### TanStack Query

管理服务端状态：

- Projects
- Cards
- Timeline
- Run / Queue
- Settings
- Connection status

### URL

管理可导航状态：

- project
- mode
- card
- filter / view

### React local state

管理短生命周期 UI：

- Dialog / Popover / Sheet
- 草稿输入
- 编辑态
- hover / focus
- 本地 disclosure

不要把服务端事实复制到一个额外 global store。

## 13. 推荐代码结构

```text
web/src/
├─ app/
│  ├─ router.tsx
│  ├─ providers.tsx
│  └─ auth.tsx
│
├─ routes/
│  ├─ discussion/
│  ├─ work/
│  ├─ card/
│  └─ settings/
│
├─ components/
│  ├─ ui/              # shadcn/Radix 基础组件
│  └─ layout/          # AppShell / Sidebar / PageHeader
│
├─ features/
│  ├─ projects/
│  ├─ discussions/
│  ├─ work-board/
│  ├─ card-thread/
│  ├─ execution/
│  ├─ schedules/
│  └─ settings/
│
├─ lib/
│  ├─ api/
│  ├─ query/
│  ├─ realtime/
│  ├─ i18n/
│  └─ utils/
│
└─ styles/
   ├─ tokens.css
   └─ globals.css
```

API 不再长期堆在一个大 `client.ts`：

```text
lib/api/
├─ client.ts
├─ auth.ts
├─ projects.ts
├─ cards.ts
├─ execution.ts
├─ agent.ts
└─ settings.ts
```

HTTP transport 仍只有一份；拆文件只是按业务域组织，不建立无意义 Repository 层。

## 14. 从零实现边界

V2 开发时：

### 可以保留 / 参考

- 后端 API 契约。
- TypeScript 领域类型的含义。
- mock server / fixtures。
- 当前 E2E 覆盖的业务场景。
- 已确认的产品布局方向。
- i18n 文案含义。
- CycleDock Logo。
- 本文及 AGENTS.md 中的领域规则。

### 不直接复用

- 当前页面 JSX。
- 当前 `shared/ui/components.tsx`。
- 当前页面 Tailwind class。
- 当前 Sidebar / Settings / CardPage 的组件实现。
- 当前页面布局细节。

目标是“保留产品规则，不复制旧 UI”。

## 15. 实施阶段

### Phase 1 — Design System / UI Showcase

先完成：

- tokens.css。
- Typography。
- Button / IconButton。
- Input / Textarea / Select。
- Dialog / Sheet / Popover / DropdownMenu。
- Tabs / Tooltip / Avatar / Badge。
- Skeleton / Empty / Error / LoadingButton。
- Message Bubble / Agent Avatar / System Event。
- Sidebar / PageHeader。

建立只用于开发验收的 UI Showcase。

**Showcase 未确认，不开始批量写业务页面。**

### Phase 2 — App Shell / Router

- 新 URL 结构。
- Project context。
- Discussion / Work mode。
- Project selector。
- Account / Workspace。
- 新建 Project Dialog。
- Desktop / Mobile navigation。

### Phase 3 — Discussion / Thread

- Discussion Sidebar。
- Search / create。
- Timeline。
- Human / Agent / System event。
- Composer。
- SSE。
- Run / Queue / retry / cancel。
- Skeleton / empty / error。

### Phase 4 — Work

- Board。
- dnd-kit。
- Human Todo create。
- Review / Done。
- Ongoing。
- Card detail。
- Info Popover / mobile Sheet。
- Schedule。

### Phase 5 — Settings / Login

- Settings 纵向二级导航。
- RHF + Zod。
- Model / Agent / NexusDock / MCP / Project。
- Login。
- Logo / favicon。
- i18n / theme。

### Phase 6 — Cleanup / Acceptance

- 删除旧 UI 代码。
- 删除不再使用的依赖。
- 旧 URL redirects。
- lint。
- production build。
- Browser E2E。
- Desktop + Mobile visual pass。
- 真实 SSE / provider / MCP 环境验收按可用授权继续做。

## 16. 验收标准

### 工程

- `npm run lint`：0 warning / 0 error。
- `npm run build`：通过。
- 不引入无使用场景的依赖。
- 页面代码不绕过 UI primitives 随意重新定义常用控件。
- 现有关键 smoke / E2E 场景迁移并通过。

### Desktop

至少真实验证：

- 1440×900。
- Discussion 首页。
- Discussion detail。
- Todo detail。
- Ongoing detail。
- Work Board。
- Settings。
- Project / Account menus。

### Mobile

至少真实验证：

- 390×844 或 375×812。
- 无根级横向溢出。
- Navigation Drawer。
- Thread / Composer。
- Info Sheet / Popover fallback。
- Board 横向列滚动。
- Settings navigation。

### 关键流程

- 新建 / 切换 Project 保持当前 Discussion / Work mode。
- Discussion 创建失败不丢草稿。
- 首条 Comment 失败重试不重复建 Card。
- Agent message / Run duration 位置正确。
- enqueue / interrupt / waiting_human / retry / cancel 正确。
- Review → Done 仍只有 Human。
- Ongoing Schedule 正常。
- Workspace scope 不串数据。
- Loading / Skeleton / Empty / Error 不混淆。

## 17. 明确的非目标

本轮 Frontend V2 不做：

- 后端领域重构。
- 新 Board 实体。
- 新 Chat Session / Message 存储。
- raw model/tool trace UI。
- Redux / Zustand 架构迁移。
- Next.js 迁移。
- Ant Design / MUI 风格化后台。
- 复杂富文本编辑器。
- 为技术清单而强行加入动画框架。

## 18. 开发纪律

- 必须在当前独立 worktree / 分支开发，不直接修改 main。
- 开始前读取 `AGENTS.md`、本文以及相关领域文档。
- 修改前检查真实 HEAD / status / 与 main 差异。
- 不覆盖其他 worktree 的未合并修改。
- 每个 Phase 都要真实浏览器验证，不以 TypeScript 编译通过代替产品验收。
- 产品规则变化同步文档。
- 未实现能力不能在文档或 UI 中冒充完成。

## 19. Frontend V2 实施记录（2026-10-21，待发布）

本节记录本地开发分支上的真实代码与验证状态，不表示已经进入 `main` 或 DEV。

### 已落地

- **Phase 1：** 在 `src/styles/tokens.css` 统一白灰中性色、品牌蓝青、12/13/14/16/22px 字体规格、32/36/40px 控件高度和 6/8/12px 圆角。新增独立 `/__ui` 开发 Showcase（生产 bundle 不包含）。基于 Radix 原语、CVA、`cn()` 建立按钮/输入/Select/弹层/导航/消息/骨架/反馈等基础组件；骨架延迟 180ms，Refetch 保留旧数据。
- **Phase 2：** 新建 V2 App、Project 单左侧栏及 Discussion/Work 路由、居中 Project Dialog、Project/Workspace 切换、Account 菜单与旧 URL Redirect。新建/切换项目保持当前一级模式。
- **Phase 3：** Discussion 主页、首条消息失败的会话草稿保护及重试、统一 Card Event Timeline、Human 右侧 / Agent 左侧 / System 居中、Run Duration 所属关系、Trigger 排队和重试，以及保留的 REST + SSE 增量契约。长 Timeline 接入 TanStack Virtual。
- **Phase 4：** dnd-kit 四列 Todo 看板、独立 Human 创建 Todo、待审核/Ongoing 视图、Card 信息桌面 Popover / 移动 Sheet，以及 Ongoing Schedule 只读到显式编辑的日/周/月配置。
- **Phase 5：** 独立的七个 Settings 二级页面、React Hook Form + Zod 的 Project/Agent/MCP 表单、模型连接/断开、手动 ChatGPT OAuth 回跳与 Grok 设备授权、Provider/MCP 测试、登录页与统一 Logo。
- **Phase 6（进行中）：** 清理旧 `src/app/`、`src/features/`、`src/layouts/` 的页面代码以及旧共享 UI/CSS；保留 `shared/api`、`shared/realtime`、`shared/preferences`；将浏览器 Smoke 迁移回 CI 使用的 `web/e2e/smoke.py`。
- **本轮职责拆分与修复：** 把设置表单按 Project、Agent、模型授权、MCP 四个稳定领域职责拆分；项目选择/创建与 Workspace 账户操作从 ProjectFrame 中抽离。Agent 模型目录选择改为订阅 React Hook Form 字段变化，选择、测试、保存与刷新恢复已纳入浏览器回归。
- **中英双语体验：** 将 V2 Project/Account、Discussion、Work、Card、Schedule、Login 和 Settings 的主要导航、表单、按钮、验证提示与空状态统一接入既有 i18next；语言仍由浏览器偏好保存，服务端 Event/模型输出保持原文。新增 `e2e/i18n_settings.py` 并纳入 CI，覆盖英文界面刷新持久化、七个设置二级页面、讨论/看板/卡片/定时计划、390px 移动端设置抽屉。

### 本地测试记录

- `npm run lint`：0 warnings / 0 errors（如修改代码应重新运行）。
- `npm run build`：TypeScript + Vite production build 通过，页面按路由拆包。
- `python e2e/showcase.py`：真实 Chromium 桌面/390×844 手机视口测试 **5/5**，覆盖 Select、Dialog、Popover、Sheet、Menu、控件规格、消息位置、Skeleton 与根级无横向溢出。
- `python e2e/smoke.py`：mock REST/SSE fixture 浏览器业务回归 **15/15**，覆盖新建/切换 Project 模式保持、工作看板、手工 Todo、首发失败/草稿跨项目、已授权模型目录选取与保存、Ongoing Schedule、旧 URL、设置表单、SSE Run 文本与 Workspace 切换。
- `python e2e/sse_reconnect.py`：真实 Chromium 中替换浏览器 EventSource 的受控时序测试 **2/2**。覆盖断线重连保留临时文本、快照覆盖后继续追加、旧 Run 增量/终态不覆盖新 Run，以及 REST 尚为旧 current Run 时优先展示新 Run；隔离构建输出避免与其他进程争抢 dist。
- `python e2e/i18n_settings.py`：真实 Chromium 英文语言、设置及 Discussion/Work/Card/Schedule 的桌面与 390px 视口回归 **4/4**，测试已加入 CI 浏览器 job。
- 隔离 PostgreSQL 17：新建本地临时测试集群、仅在 loopback 使用独立测试库，Goose migration `up → down → up` 均成功至版本 7；设置 `TEST_DATABASE_URL` 后 `go test -count=1 ./...` 和 `go vet ./...` 全部通过，实际执行 store/auth/httpapi/agent/oauth/modelauth 的数据库集成测试。
- 未使用老前端页面和 UI 组件替换 V2 实现。以上测试只证明隔离 mock / 浏览器侧链路，不冒充真实 OAuth、模型推理或生产 SSE 网络验证。

### 未关闭风险与下一轮验收

1. 已通过本机隔离 PostgreSQL 17 上的 Go 集成测试，但 V2 React 页面尚未与正式后端 + PostgreSQL 实际运行会话完成完整联调；NexusDock OIDC、ChatGPT/Grok 授权账号和真实 MCP 的端到端功能仍需在有权限的环境复验，并核验真实错误响应、Provider 额度与权限。
2. SSE 浏览器端重连与新旧 Run 交错时序已在受控模拟场景回归；真实网络断线、取消/转向并发、跨实例广播与长执行仍需在部署拓扑上压测，不能把模拟事件当生产通过。
3. 主要界面和高级设置已接入 i18next，并经中英浏览器回归覆盖；服务端中文错误/模型输出、动态事件内容仍按原文展示，少量执行耗时单位与边角提示需进一步完整审校，不能把目前覆盖宣称为所有字符串已翻译。
4. Browser Smoke 的移动视口无根级溢出已验证；真实触屏设备上的 dnd-kit 手势、屏幕阅读器与视觉细节仍值得增加专项验收。
5. 上述变更尚在 `feat/frontend-v2-20261021` worktree；**未 push、未合并、未部署**。有关本轮后端与数据库领域语义，仍以仓库 `AGENTS.md` 和领域文档为准。
