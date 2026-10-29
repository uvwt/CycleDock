# UI 组件

通用 UI 使用 shadcn/ui New York / Tailwind v4 官方 registry 源码，2026-10-29 接入。组件源码由项目维护，不会随某个 shadcn 运行时包自动升级。

## 目录与使用

- `web/components.json`：shadcn CLI 配置；Vite 与 TypeScript 的 `@/` 均指向 `web/src/`。
- `web/src/shared/ui/`：Button、Input、Textarea、Label、NativeSelect、Dialog、AlertDialog、Sheet、DropdownMenu 的源码。
- `web/src/shared/lib/utils.ts`：官方常用的 `clsx` + `tailwind-merge` 类名合并函数。
- `web/src/shared/ui/components.tsx`：CycleDock 业务组合入口，保留页面使用的按钮命名、表单标签、通用错误和确认框；不重复实现基础组件。

通用样式修改在对应组件或主题变量中完成。业务页面只组合内容与请求行为，不再单独编写弹窗定位、遮罩、关闭按钮或菜单键盘交互。项目选择器仍有适配导航栏的位置规则，外壳复用 Dialog。

NativeSelect 保留 HTML select 与 option 的值契约，可直接注册到 React Hook Form。不要为了换外观引入另一套表单状态。

## 与官方源码的差异

- 采用已有的 `@radix-ui/react-*` 独立包，避免同时引入聚合包。
- import 统一改为本项目别名；移除 React SPA 不需要的 `use client` 指令。
- Dialog / Sheet 的关闭标签默认中文，允许业务传入翻译；Dialog 允许覆盖遮罩样式，供项目选择器使用。
- NativeSelect 常规尺寸填满表单宽度，小尺寸用于消息发送方式；Textarea 保留页面已有的 rows 高度契约，不启用内容自适应高度。
- Button 的危险操作前景通过主题变量适配深色模式，仅导出实际使用的组件。

`index.css` 映射 shadcn 的 background / primary / accent / border / input / ring 等语义颜色到项目主题。`dark:` 使用已有的 `data-theme="dark"`，不额外维护 `.dark` 状态。动画使用 `tw-animate-css`；减少动态效果的系统偏好继续生效。

## 异步确认

ConfirmDialog 使用 AlertDialog。默认焦点在取消按钮，点击遮罩不确认也不关闭。确认操作阻止 Action 默认立即关闭，由业务调用方成功后关闭；失败时保留弹窗。请求进行中禁用操作与 Esc 关闭，避免结果未知时重复提交。

## 来源与升级

官方文档：[Vite 安装](https://ui.shadcn.com/docs/installation/vite)、[Dialog](https://ui.shadcn.com/docs/components/radix/dialog)、[AlertDialog](https://ui.shadcn.com/docs/components/radix/alert-dialog)。

本次源码来源为 `https://ui.shadcn.com/r/styles/new-york-v4/{组件名}.json`，组件名为 button、input、textarea、label、native-select、dialog、alert-dialog、sheet、dropdown-menu。上游 MIT 许可保留在 `web/SHADCN_LICENSE`。

以后按需通过 shadcn CLI 添加组件；更新现有组件时审查差异并保留上述项目适配，不直接批量覆盖。验证包含构建、Lint、Node 测试，以及 `web/e2e/smoke.py` 的桌面、移动端与主题交互回归。
