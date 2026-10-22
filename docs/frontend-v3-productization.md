# CycleDock Frontend V3 · 产品化视觉重做

> 状态：**已实现并完成本地验收；待用户确认后再 push / merge / 部署 DEV。**
>
> 基线：`main@51d305a`。
>
> 开发分支：`feat/frontend-v3-productization-20261021`。
>
> 独立 worktree：`C:\Users\Administrator\CycleDock-frontend-v3`。

## 1. 目标

Frontend V3 不改变 CycleDock 的后端领域模型、路由契约、Query/SSE 数据流或认证方式。本轮只重做用户实际看到和操作的产品层，让 CycleDock 从“可用的后台管理界面”变成有明确品牌气质、信息层级和工作节奏的产品。

原则：

- 保留已经通过验收的 Project / Discussion / Todo / Ongoing / Trigger / Run 行为。
- 保留 React Router、TanStack Query、SSE、i18n、表单和 API Client。
- 核心页面 JSX、布局、视觉组件和样式按产品目标重新设计，不把 V2 外观当基线继续微调。
- 不为了视觉重做后端，不增加第二套 Conversation / Message 模型。
- 不引入新的重型 UI 框架，不依赖大面积渐变、玻璃拟态、发光和卡片套卡片制造“设计感”。

## 2. 视觉方向

CycleDock 与 AgentDock / NexusDock 保持品牌家族感，但不是它们的复制品。

### 2.1 基础气质

- 导航与工作画布都必须跟随主题：浅色主题使用浅色中性导航，深色主题使用深墨色导航；通过边界、空间和层级区分“控制区 / 工作区”，不依赖固定黑色侧栏制造反差。
- 继续使用现有青蓝 accent，不另起一套品牌主色。
- 主画布采用带轻微冷暖差的中性底色，不使用纯白铺满整个屏幕。
- 强调色只用于当前状态、主操作、运行中提示和关键链接。
- 层级主要依赖空间、字号、字重和表面关系，不依赖 badge / border / shadow 堆叠。

### 2.2 表面层级

只保留四种产品表面：

1. Navigation：主题感知、稳定、承载项目/模式/账户；桌面侧栏始终与工作区等高。
2. Canvas：页面主背景。
3. Work Surface：看板列、Composer、设置内容等真实工作容器。
4. Floating Surface：Dialog / Popover / Sheet。

普通文本区不自动套 Card。卡片只在它确实代表一个可点击业务对象时使用。

## 3. Composer 是核心产品交互

Discussion 首页、Discussion 详情、Todo/Ongoing 详情统一使用同一套 Composer 行为。

### 默认聚合态

- 单行高度，弱存在感，不抢正文。
- 保留 placeholder、必要快捷入口和圆形发送按钮。
- 不提前展示队列模式、运行状态、取消执行等次级控件。

### Focus 展开态

- 原地向上展开到 3–4 行，不打开新弹窗。
- 显示 Generate work、enqueue / interrupt、当前 Run 状态、取消等上下文操作。
- Focus 外框和工作表面明显增强，但不使用夸张 glow。

### 收起规则

- 空内容且焦点离开整个 Composer 时可收起。
- 已输入内容时保持展开，避免误触导致布局抖动。
- 有 current Run / waiting_human 时可保持展开。
- 发送成功后恢复聚合态。
- Enter 发送，Shift+Enter 换行；IME composing 时不能误发送。

## 4. 页面方向

### 4.1 Shell

- Logo / CycleDock / 当前模式形成稳定品牌头。
- 模式切换不做普通灰色 segmented control，要像产品导航。
- Discussion 列表或 Work 过滤属于项目内导航，视觉上低于品牌和项目层。
- 项目与账户留在导航底部，仍保持现有信息架构和 Workspace 边界。

### 4.2 Work

- 看板列不再是四个同质灰块。
- 列头使用真实状态语义和轻量 accent marker，Card 本身是主要视觉对象。
- Todo Card 优先展示标题、最多两行说明和当前执行状态；不把长描述完整铺在看板。
- Ongoing 不使用普通三列卡片墙，改为更接近“持续运行事项”的横向工作条目，突出状态、health 和最近上下文。

### 4.3 Discussion

- 首页是工作发起入口，不做“欢迎页”。
- Composer 是第一视觉焦点；最近 Discussion 是次级工作历史。
- 最近 Discussion 使用列表式信息结构，不做一组同权重按钮卡片。

### 4.4 Card Detail

- 标题 / Card 类型 / 业务状态形成一个明确的工作头部。
- Timeline 是主内容，消息表面减少“每条一个白卡”。
- Human 与 Agent 仍有清晰角色差异，但不做普通聊天 App 气泡复制。
- Card 信息继续按需打开，不恢复常驻右栏。

### 4.5 Settings

- Settings 保持克制，但与主产品共享同一视觉语言。
- 桌面设置导航是独立 rail，内容区不是“一个大白卡里再放表单”。
- 中英文分组标题必须一起翻译，不允许 English 模式残留“个人 / 外部连接 / 工作空间”。

## 5. 验收

- [x] Compact Composer 与 Focused Composer 在 Discussion 首页和 Card Detail 都能真实切换。
- [x] 有内容时失焦不收起；空内容可收起；发送成功后收起。
- [x] Desktop / Mobile 无根级横向滚动；Kanban 仅自身保留横向滚动。
- [x] Discussion / Work / Card / Settings 使用同一产品视觉层级。
- [x] Todo / Ongoing 的原有业务操作、路由、SSE、queue / interrupt / waiting_human 行为未回归。
- [x] 中英文和浅色 / 深色主题可用；设置分组标题已补齐英文翻译。
- [x] Browser E2E、lint、build、dependency audit 全绿。
- [x] 使用 TianYi Neo 做桌面与 390×844 移动端真实浏览器布局验收。

## 6. 2026-10-21 实施与验证记录

本轮以 `main@51d305a` 为基线完成第一轮产品化实现：

- Shell 改为深墨色 Navigation + 明亮工作 Canvas，保留 CycleDock 现有青蓝 accent。
- Discussion 首页从欢迎式卡片页面调整为工作发起入口，最近讨论改为列表结构。
- Work 看板改为有明确状态语义的四列工作区；Todo Card 收敛为标题、两行说明和当前执行状态。
- Ongoing 从普通三列卡片墙改为横向工作条目，`unknown` health 不再暴露给用户。
- Card Timeline 降低“聊天 App 白卡片”感，Human / Agent / System 三类内容重新分层。
- 新增 `AdaptiveComposer`，恢复并统一 Compact / Focused 两态行为。
- Settings 去掉“大白卡包住所有表单”的结构，桌面改为 rail + content；英文模式分组标题已修复。
- CI 新增 `e2e/composer_states.py`，把 Composer 两态作为长期回归契约。

真实验证：

- `npm run lint`：0 warnings / 0 errors。
- `npm run build`：通过。
- `npm audit --omit=dev --audit-level=high`：0 vulnerabilities。
- `e2e/sse_reconnect.py`：2/2。
- `e2e/i18n_settings.py`：4/4。
- `e2e/composer_states.py`：4/4。
- `e2e/smoke.py`：15/15。
- 上述四套 Browser E2E 已按 CI 顺序在同一个全新 fixture 上连续通过。
- TianYi Neo 桌面结构验收：Work 根宽 1408/1408，导航、看板与 Ongoing 信息层级正常。
- TianYi 本机 Playwright 像素验收：Desktop 1408×900 与 Mobile 390×844 均无根级横向溢出；Discussion / Card 的 Compact / Focused Composer 已实际截图比对。
- Card Focused Composer 视觉验收中发现并修复工具栏状态被挤成竖排，以及展开后最后一条 Timeline 被裁住的问题；回归测试要求原本贴底时展开后底部余量不超过 12px。
- kill-ai-slop 扫描剩余命中仅为语义链接下划线、必要圆形控件/状态点以及 Composer 高度 transition 误报，未发现需要继续清理的装饰性 AI slop。

当前 V3 仍只在独立 worktree/开发分支中，尚未 push、merge 或部署。
