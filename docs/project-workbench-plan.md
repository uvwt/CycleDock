# CycleDock 项目工作台优化方案与实施交接

> 状态：待实现、待验收的设计方案，不代表当前产品已具备这些能力。
> 整理日期：2026-10-21。代码审查基线：`feat/chat-workbench-20261021` / `9e3888a`。
> 历史版本说明：本文第 1–7 节记录了 2026-10-21 早期统一工作台方案；当天后续确定的双模式信息架构见第 8 节，若冲突，以第 8 节为准。

## 1. 与已有文档的关系

本文是 `docs/frontend-rebuild.md` 的后续布局与交互修订。冲突时，在本轮优化范围内以本文为准：移除旧全局导航侧栏、默认进入看板、共享讨论列表、任务信息所有尺寸默认折叠、复用现有组件，而非再次从零重写前端。旧文档中未被本方案替代的要求继续适用。

领域模型及执行语义仍以 AGENTS.md、data-model.md、agent-runtime.md、scheduling.md 为基线；本文不改变状态机，不增加聊天 Session 或另一套 Message 模型。后续实现时同步更新相关产品、HTTP API 和运行时文档，不能把计划接口写成已实现。

## 2. 目标：一个项目、一个工作台

CycleDock 是人与 Agent 推进工作的看板，不是聊天产品旁边外挂任务管理。保留 Discussion / Todo / Ongoing 的区别，但不再让用户在两个独立模块之间来回找工作。

```text
顶栏：☰ CycleDock                         [当前项目 ▾] [账户菜单]
      ├─ 项目讨论列表（可折叠） ─┬─ 首页：工作看板
      │  搜索、讨论标题、关联工作 │   Todo / Working / Review / Done
      │                           │   Ongoing 独立区域
      │                           │   右下角：新讨论输入框
      │                           └─ 二级页：Discussion / Todo / Ongoing 详情
      │                               标题、状态、摘要、现有时间线、回复框
      │                               任务信息：按钮按需打开，默认关闭
```

### 2.1 顶栏与项目上下文

- 去掉旧全局侧栏中的重复邮箱、聊天 / 看板 / 设置导航、工作空间及退出登录常驻区块。
- 项目不占整列侧栏；右上角使用带项目名的下拉按钮。项目列表、搜索和新建入口置于选择器内。
- 账户菜单内保留邮箱（仅一次）、工作空间切换、设置、退出登录。工作空间与项目是不同层级，不混成一个选择器。
- 切换项目时，讨论列表、看板及搜索范围一起变化；详情按其所属项目展示。URL 直达、刷新、浏览器前进后退、返回看板都不能掉回第一个项目。
- 无项目、项目不可访问、工作空间切换后的失效选择要有明确空态或回退，不显示上一个项目的残留数据。

### 2.2 左侧讨论列表

- 从 DiscussionSidebar 提炼共享导航，首页、Discussion、Todo、Ongoing 详情均可使用；不能继续用 isDiscussion 限制整个侧栏。
- 宽屏默认展开，窄屏默认折叠并以抽屉打开；允许手动收起，合理记忆选择，手机不强行多栏。
- 左栏始终表示同一项目的 Discussion，不在任务详情里偷偷换成另一份聊天历史。
- “新建讨论”只进入当前 Project 的草稿页，不立即创建 Card；首次发送有效内容时才真正创建 Discussion。
- 标题加轻量的关联工作信息即可；关联数量和状态来自真实数据，不另造讨论进度。
- 搜索无匹配与真正没有讨论分开；无匹配提供清空搜索，空项目才引导新建。

### 2.3 首页以看板为主体

- 默认首页不再是欢迎语加中央聊天输入，不用「讨论 / 看板」平级页签代替旧导航。
- Todo 保留现有四列；Ongoing 单独显示运行情况与最新 Check-in 派生健康状态。
- 看板右下角放紧凑的「新讨论」输入框，聚焦后展开，明确当前项目；留出布局空间，不能挡住最后一张卡或关键操作。
- 发送后创建 Discussion 并提交首条 Comment，再进入该讨论。不是直接创建 Todo / Ongoing；用户直接创建独立 Todo 的现有能力保留。
- 创建成功、首条发送失败：保留原文和已创建 Card 的身份，提供重试；不能清空草稿，也不能重试时重复创建讨论。网络失败和项目切换过程也要保护草稿。

### 2.4 详情页共享外壳

- 第一版继续使用有独立 URL 的整页详情，顶部和讨论侧栏保持连续，只切换中间内容。
- 不要求先做「看板旁详情面板 + 展开全屏」双模式；这是后续可选增强，不是本轮前提。
- 返回看板保留原项目、筛选与浏览位置。详情不显示首页的新讨论悬浮框，避免两个输入框职责不清。
- Discussion：以现有时间线与回复为主，显示真实派生工作及状态，可直达工作。
- Todo / Ongoing：主区顶部增加紧凑工作摘要，包括目标摘要、业务状态、Agent 状态、当前阶段 / 进展、最新结果入口、来源讨论（存在才展示）。详细说明不必重复铺满首屏。
- 复用 ConversationView 的持久 Timeline：Comment / Finding / Progress / Result / Check-in 等，不能替换成原始模型历史；事件 ID 与 `sequence` 继续作为审计事实和去重依据。会话视觉顺序在存在稳定 `source_event_id / run_id` 关联时按因果轮次组织，不能因为后续消息先入队就把前一轮 Result 排到它们后面。
- 来源关系用 source_discussion_id；独立创建的 Todo 无来源，不伪造。验收标准当前没有独立前端字段，可从 description 展示，不假设结构化字段已存在。
- 回复、继续、Human 验收等推进操作位于主区；最终 Review → Done 仍只由 Human 完成。

## 3. 右侧任务信息：默认折叠，打开后只读

- 手机、桌面均默认关闭，不常驻占宽；进入或切换卡片时不能意外沿用上一张卡打开的编辑态。
- 基线 CardPage 的 detailsOpen=false 只控制 Sheet；桌面 aside 的 lg:block 和固定 320/360px 网格列绕过了它。需一起移除常驻 aside / 预留列，让信息按钮在桌面也可见，并复用 Sheet 或统一按需面板。
- 默认阅读标题、目标、来源关系、必要元数据；不展示输入框、文本域与「保存修改」。按卡片类型选择内容，缺少来源或不适用日程时不放空占位。
- Todo / Ongoing 点击「编辑」进入编辑态，明确保存 / 取消，保存失败保留输入；取消或关闭不得悄悄保存。Discussion 保留 AI 生成标题 / 摘要的只读规则。
- Ongoing 日程先显示可读安排与最近 Check-in，点击新增 / 编辑才出现表单；保留原有管理能力。
- 不默认堆运行 ID、完整队列和执行历史。取消排队、重试迁入主时间线后，不能因删右栏区块而丢失能力。
- 视觉采用清晰标题、弱标签、正文段落与留白，避免每项一层框、默认表单的粗糙感。

## 4. 排队、取消与重试放回对应消息下

### 4.1 展示与行为

```text
你：先调整右侧任务信息，默认折叠。
    等待处理 · Agent 正在执行上一项要求                 [取消排队]

开始后同一位置更新：正在执行 · 调整任务信息布局
失败且可重试：执行失败 · 失败原因                        [重试]
取消后：已取消执行（用户原话保留）
```

- 这是系统执行状态，不伪装成 Agent 真实回复，不调用模型生成排队提示。
- 随真实状态更新附属状态区，不每次刷新或状态变化都新增持久气泡。结果正文只由现有 Agent Result 展示；完成后的 `work_history.result_summary` 不再作为第二份消息正文渲染，避免与同一 Run 的持久 Result 重复。
- 取消只针对尚可取消的排队 Trigger；已开始执行不再显示「取消排队」，不能误当作取消当前 Run。
- 重试继续按后端 can_retry 判定，创建新 Trigger 并保留旧执行，不能覆盖历史或复用同一 Trigger 创建第二个 Run。
- 重试可沿同一来源消息展示多个执行尝试；原失败记录可追溯，避免新旧状态互相覆盖。
- 定时检查等没有用户消息的触发，显示独立执行条目；旧数据缺少稳定关联时也安全回退，不能强挂到最近一句话下。
- 输入框附近可显示「Agent 正在执行 · 另有 N 条待处理」轻量汇总；按钮区分发送、加入队列、立即转向、回复并继续，遵守 enqueue / interrupt / waiting_human 语义。

### 4.2 代码与数据边界

- 基线 ConversationView 只接 events / live / cardKind，尚未接 queue / work_history 及取消、重试回调。
- CardDetails 已有 queue.id 取消、work_history.trigger_id 重试、can_retry 判断，应复用后端规则，不在前端复制状态机。
- 基线 TimelineEvent 有 run_id，未暴露 trigger_id / cause_event_id；QueueItem / WorkItem 也缺稳定的来源事件关联。不得按文字相同、时间相近或数组位置猜测。
- 后端已有 triggers.cause_event_id、retry_of_trigger_id 基础；实施前核对实际查询、DTO、运行关联和 API 文档，补齐稳定的 Comment Event → Trigger → Run / 重试链关联，再接 UI。这是待实现工作，不代表接口已支持。
- 刷新、实时更新、取消与 Claim 竞态、重试并发必须以服务端结果为准；失败有提示，提交期间防重复点击。跨工作空间 / 项目的读取与操作仍由服务端认证 scope 和数据库约束保证。

## 5. 建议实施顺序与范围

1. 读取当前 AGENTS.md、本文和真实分支差异；确认新版 worktree 与 main 的关系，不盲目覆盖或回到旧 main 开发。
2. 统一 Shell、项目选择和路由上下文，把 DiscussionSidebar 提升为共享导航；默认以看板为首页。
3. 接入看板新讨论输入，修复创建成功但首发失败丢草稿的问题；补来源 / 派生工作关联与任务摘要。
4. 任务信息全尺寸默认关闭，默认只读、显式编辑；保留 Ongoing 日程及权限约束。
5. 补 API 稳定关联与测试，把排队 / 取消 / 重试放入时间线；确认替代能力完备后移除右栏重复列表。
6. 统一用户可读状态与中英文文案，验证完整闭环，同步实现涉及的文档。

本轮不新增 Board 实体、独立聊天存储、复杂总览页、双模式详情系统或第二套可写 health。不要为纯视觉调整重写后端；但消息执行关联确有数据缺口，不能用前端猜测代替正确接口。

## 6. 验收清单

- [ ] 首页主要内容为看板；无旧全局侧栏、重复邮箱或平级聊天 / 看板导航，账户及工作空间管理仍可访问。
- [ ] A/B 两项目切换、直达详情、刷新、前进后退、返回看板均保留正确上下文，无跨项目数据残留；筛选与位置恢复。
- [ ] 首页及三类 Card 详情可用同一讨论侧栏；宽屏 / 窄屏折叠行为正确，搜索无匹配不冒充空项目。
- [ ] 新讨论首条发送成功、创建失败、创建成功但发送失败、重试、切换项目场景均验证，无丢草稿或重复创建。
- [ ] 桌面及手机任务信息初始关闭且无空白预留列；打开只读，编辑保存 / 取消 / 失败行为正确；Discussion 不被开放编辑。
- [ ] Ongoing 日程新增 / 编辑与最新健康展示可用；Todo / Ongoing 摘要与来源、Discussion 派生工作链接正确；详情只保留当前对象回复框。
- [ ] 排队、执行、取消、失败、重试准确关联；同文重复消息、多条排队、多次重试不会错挂。无用户来源触发及旧数据有独立回退展示。
- [ ] 取消 / Claim 竞态、重复点击、刷新及实时事件不会出现假成功或重复条目；can_retry=false 不误放重试入口，服务端越权操作被拒绝。
- [ ] enqueue 不打断、interrupt 正确转向、waiting_human 回复接续；Human-only Done 等领域约束不退化。
- [ ] 状态码转换为用户可读文案，中英文一致；键盘焦点、抽屉关闭、按钮可访问名称可用；悬浮框不遮挡卡片，移动端无横向挤压。
- [ ] 真实执行前端 build / lint 及相关自动化测试；涉及接口时运行相应后端测试。浏览器验证桌面 / 手机视口与上述关键流程，记录证据和未验证项，不能只用编译通过代替产品验收。

## 7. 交接说明

本文是本次交付的文档，不是功能完成声明。实施任务应遵守短期分支 + 独立 worktree 约束，先检查真实 HEAD 和未提交改动。保留现有可用组件，不覆盖他人工作；未经明确要求不部署、不推送、不合并。完成后报告改动文件、接口变化、验证结果、剩余风险及提交位置。

## 8. 2026-10-21 后续产品决策：Project 下双模式、单侧栏

> 本节是对上述信息架构、导航布局和首页结构的**后续修订**，覆盖第 2–3 节中与此冲突的约定；第 4 节关于执行关联与领域约束继续有效。当前仅在独立 worktree 快速预览，未经正式测试和发布。

- Project 是共同数据范围，但有两个不同的一级工作模式：**Discussion（讨论）**与 **Work（工作）**；共享源 Card、Trigger、Run、Event 状态机，不增加独立会话或 Board 领域实体。
- 整个应用不再常驻全局顶栏，也不恢复旧的 64px 图标主导航 + 第二块聊天侧栏。桌面左侧仅一块导航区域，顶部为 Discussion / Work 模式切换，底部为项目切换与用户/Workspace/设置菜单。移动端使用单个导航抽屉。
- 讨论模式：左侧是当前 Project 的 Discussion 搜索与列表；主区是独立聊天首页或 Discussion 详情。新讨论首条消息继续复用具备幂等重试与草稿保护的创建组件；关联 Todo/Ongoing 仅作为轻量入口按需展开，不常驻额外「派生工作」横条。
- Discussion 的信息入口与 Todo / Ongoing 统一：会话标题右侧一个信息图标，点击在按钮下方展示**不占页面布局宽度的紧凑浮层**，点击外部或 Esc 关闭，不使用独立右侧固定栏或原右侧抽屉。讨论摘要在浮层显示，不重复标题、不显示维护规则说明；归档/删除收进 `···` 操作菜单，保留删除确认及服务端权限校验。任务浮层中来源 Discussion 链接用更小的次要文字（11px）呈现，避免抢占主体视觉。
- 工作模式：左侧改成全部工作 / 待审核 / 持续事项等工作视图；主区为四列 Todo + Ongoing 看板或任务详情。**不常驻 Discussion 列表**。Todo/Ongoing 详情改为单栏会话：标题旁使用紧凑状态切换器（Todo: 待办 / 进行中 / 待审核 / 已完成；Ongoing: 运行中 / 已暂停 / 已归档），状态操作仍通过原后端和 Human-only Done 权限约束。原右侧整列信息面板取消，在标题栏右侧使用 `!` 信息按钮打开锚定浮层显示目标、来源及显式编辑；Ongoing 日程和 Check-in 管理功能留在同一浮层，不能因为去掉侧栏而丢失。移动端同样优先轻量浮层，不再为任务占用固定信息列。
- 会话中 Human 评论始终为右侧独立气泡；关联的 Run / Trigger 的排队、运行、失败、重试和结果作为左侧带 Agent 头像的独立消息卡片展现，不塞进 Human 气泡。视觉上以来源 Human 消息为轮次锚点：例如用户连续发送 A、B，A 正在执行而 B 入队，则展示应为 `A → A 的 Agent 状态/结果 → B → B 的 Agent 状态/结果`，而不是 `A → B → Agent A → Agent B`；底层 Event/Trigger/Run 的真实发生顺序不改写。执行时长属于单个正在运行的 Run，放在其对应 Agent 消息头的右侧（使用该 Run 的 `started_at`），不能放在整个 Card 的标题旁；SSE 临时输出与最终 Result 必须合并到相同 Run 的消息位置，避免重复呈现。
- 视觉基线统一为克制的白 / 灰中性色 + CycleDock 蓝青品牌色：页面主标题约 22–24px、区块标题 15–16px、正文 13–14px、导航与表单控件优先 12–13px、辅助信息约 12px，避免同屏出现明显失衡的大字和微字；卡片和输入框以 8–12px 圆角为主，表单下拉框保持约 13px 字号与紧凑高度，弹层使用紧凑阴影，避免大面积 pill、过度圆角和玻璃拟态。品牌入口、登录页和浏览器 favicon 统一使用 CycleDock 三色几何 C Logo。移动端根布局必须保持 viewport 宽度，横向滚动仅允许发生在明确的局部容器（如看板列、设置标签）。
- Board 可直接创建独立 Todo；Discussion 生成的工作保留来源 Discussion 的深链接。两种模式分别做适合自身的交互，但无信息孤岛。
- 切换项目或新建项目都必须保持当前一级模式（Discussion / Work）：Discussion 中完成项目操作仍停留在 Discussion，Work 中仍停留在 Work，不得因为项目选择器复用 Board 路由而隐式切换模式。切换 Workspace 必须清理旧 Workspace 查询范围，所有 URL 深链接继续使用服务端鉴权。设置为共享入口，不引入另一条全局顶栏。
- 不回退整个仓库到 `092f521`。只参考其 Chat / Board 分离边界，当前 `53b0d86` 的后端、OAuth、安全性和 SSE/执行关联改进保留。
- 本轮属于用户明确要求的**临时快速渲染调整**：只允许轻量页面打开与 HMR 检查，不代替发布前必须执行的 lint / build / 回归 / 真实桌面与移动端验收；未经用户确认不合并或部署。

## 9. 2026-10-22 最终产品决策：恢复 092f521 外壳，能力按当前后端重建

> 本节覆盖第 8 节中“不要恢复 64px 图标主导航”、单侧栏与任务信息浮层等冲突约定。领域约束、稳定执行关联与可靠首轮 Discussion 语义继续有效。

- 桌面端恢复 `092f521` 的 **64px 全局图标 rail**。一级工作入口只保留 **Chat** 与 **Work** 两个图标；顶部使用正式 CycleDock Logo，rail 底部只保留 Settings。Account / Workspace 快捷侧栏删除，账户、Workspace 与退出登录统一留在 Settings → Account。
- Chat 与 Work 是同一 Project 数据上的两种工作入口，不建立新的 Session / Board 领域模型。Chat 使用 Discussion 列表 + 会话主区；Work 使用 Todo 四列 + Ongoing 区域。移动端使用紧凑顶栏与抽屉。
- 不把 `web/` 简单整体回退到 `092f521`。后端、API 契约及安全边界保持当前 `main`：Workspace membership/switch、Trigger lineage、OAuth、SSE、Human-only Done、enqueue / interrupt / waiting_human 等全部保留。
- Card 详情基于 `092f521` 页面结构重新开发。Timeline 继续是对话式：Human 消息在右，Agent / Result 在左，系统事件居中；相邻持久事件间隔严格超过 2 小时才显示时间分隔。
- SSE 继续使用 Card stream：`run.started` / `run.stage` / `run.text.delta` / `run.text.snapshot` / terminal / `event.created`。SSE 只表示暂态和低延迟输出，REST Timeline 是最终事实源。
- Queue / current Run / retry / cancel 通过服务端稳定的 `source_event_id / trigger_id / retry_of_trigger_id` 关联到原始 Human Comment；无稳定来源的 Schedule/System Trigger 独立展示，不允许按文字或时间猜测。
- 新 Discussion 首页保留 `092f521` 的收起 / 聚焦展开两态输入框，但补回可靠首轮语义：Card 创建成功后立即保存原始草稿、Card ID 与幂等键；首条 Comment 失败只允许重试同一 Card。
- Todo/Ongoing 继续使用对话式工作详情；取消右侧常驻任务信息栏，标题旁保留紧凑状态切换器，信息按钮固定在标题栏最右侧。桌面左侧新增 Work 二级列表，视觉与 DiscussionSidebar 共用同一基础组件，但内容按 Todo / Working / Review / Done / Ongoing 折叠分组；默认只展开当前任务所在分组。移动端从左抽屉打开同一 Work 列表。任务目标、来源 Discussion、Ongoing Health/Schedule 和显式编辑继续保留在信息浮层中；Human 手工 Todo 与 Human-only Done 规则不变。
- Workspace membership/switch API 与服务端 Session scope 继续保留，但 Shell 不再暴露 Workspace 快捷切换侧栏；账户与当前 Workspace 信息统一进入 Settings → Account。若未来重新提供 Workspace 切换入口，仍必须先清理旧租户 Query cache 再写入新 Identity。所有真实租户权限继续由服务端 Session 与数据库约束保证。
- 验收必须包含 lint/build、浏览器 mock E2E、相关 Go 测试、桌面与 375px 移动端宽度检查；未经用户明确要求，不推送、不合并、不部署。
- 本轮独立分支已实际验证：前端 lint 0 warning / 0 error、production build 通过、mock Playwright 14/14 通过；最新收敛回归覆盖正式 Logo、Shell 无 Account / Workspace 快捷侧栏、标题旁状态切换、任务 / 讨论信息浮层、Human-only Done disabled、Work 二级侧栏状态折叠、信息按钮右对齐、移动端 Work / Discussion 抽屉和原有 SSE / Queue / Retry 流程。1440px 桌面与 375px 手机均无全局横向溢出；相对当前 `main` 的后端源码 / migration / Agent / MCP / OAuth diff 仍为 0。此前同分支 Go `test ./...` 与 `vet ./...` 已通过，本轮未修改后端。

## 10. 2026-10-22 Work 详情二级列表修订

- Discussion 与 Work 详情统一采用同一套 Context Sidebar 基础骨架：项目选择、侧栏容器、滚动区、选中态列表项共用；Discussion 保持扁平会话列表，Work 保持独立的业务分组，不做“万能侧栏”。
- Work 列表分为待办、进行中、待审核、已完成、持续事项五组。进入 Todo/Ongoing 详情时默认只展开当前任务所在组，其余组保持折叠；用户手动展开另一组时使用单组手风琴行为。
- 切换 Work 列表中的任务后继续使用独立 Card URL，新的当前任务自动高亮，其所在分组自动成为默认展开组。Project 切换返回对应 Work 看板，不跨 Project 保留旧任务。
- 桌面端 Work 列表与 Discussion 列表使用相同宽度和边界；移动端都用左侧 Sheet，不额外挤占会话主区。
- 标题栏信息图标从标题/状态组中移出，固定在最右侧；标题与状态区域可收缩，信息入口位置不再随标题长度漂移。
- 本修订已通过前端 lint 0 warning / 0 error、production build 和 mock Playwright 14/14；新增回归覆盖 Work 分组展开、当前任务高亮、分组切换、信息按钮右对齐与 375×812 移动端 Work 抽屉。

## 11. 2026-10-22 全局 Project 上下文与选择器修订

> 本节是 Project 入口与前端上下文的最新决策，覆盖第 2.1、8、9、10 节中关于 Project 选择器位置、页面级项目下拉和 Context Sidebar 内项目选择的冲突约定；其他领域规则不变。

- Project 是 Chat / Work / Card 共用的全局数据范围。桌面端在 64px 全局 rail 底部、Settings 正上方放置唯一的 Project 入口；移动端在紧凑顶栏提供同一入口。Discussion / Work 二级侧栏和 Board 页面不再各自放一套项目下拉。
- Project 选择统一复用同一个 `ProjectSwitcher` 组件，不再使用原生 `<select>` 或页面各自手写一套选择框。桌面 compact 入口打开 rail 右侧、贴近底部的轻量浮层，不遮暗页面；移动端 compact 入口与设置页完整选择器继续使用可访问的 Dialog 交互。
- 当前 Project 由应用级 Project Context 统一管理并持久化。URL 中明确指定的 Project 优先；直达、刷新、前进后退后仍应回到正确 Project，失效 ID 才回退到可访问项目，不能继续显示上一个 Project 的 Card 数据。
- 切换 Project 时保留当前一级工作模式：Chat 切换后仍进入该 Project 的 Chat；Work / Work Card 切换后回到该 Project 的 Work。Card 详情以 Card 的真实 `project_id` 纠正 URL 与全局 Project 上下文，避免跨项目错配。
- Settings → Projects 复用相同 Project 选择器；新建项目继续复用现有项目创建表单与服务端 API，不另造第二套项目实体或状态。

## 12. 2026-10-22 四个一级入口：Chat / Work / Plan / Activity

> 本节覆盖第 9 节中“一级入口只保留 Chat 与 Work 两个图标”的约定；第 11 节的全局 Project Context 与唯一 ProjectSwitcher 继续有效。

- 当前 Project 下的一级入口固定为 **Chat / Work / Plan / Activity**。四者只是同一 Project 数据的不同 View，不增加 Conversation、Activity、ScheduledTask 等新领域实体。
- **Plan** 直接聚合现有 Ongoing Card + CardSchedule，回答“接下来哪些事情会自动发生”。第一版仅展示启停状态、日/周/月规则、时区、下一次触发、最近触发与最近运行，点击回到对应 Ongoing Card；不要求日历视图，也不建立第二套计划状态机。
- **Activity** 直接聚合现有 Event / Trigger / AgentRun，回答“Agent 正在做什么、最近发生了什么”。只展示有产品意义的排队、运行、等待、完成、失败和 durable Event；raw model message、tool call、tool arguments、raw tool result 与 chain-of-thought 不进入产品活动流。
- Project 切换继续复用应用级 Project Context：在 Plan 中切换项目仍停留 Plan，在 Activity 中切换项目仍停留 Activity；失效 Project ID 按第 11 节规则回退。
- Plan / Activity 列表点击后进入现有 Card 详情，不复制任务详情、运行操作或状态机。Schedule 编辑仍在 Ongoing Card 中完成，Activity 不增加修改 Run / Trigger 的第二套写接口。
