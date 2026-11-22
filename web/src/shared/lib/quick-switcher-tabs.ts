/**
 * 快速切换器分类 Tab 固定顺序：
 * 将最常用的「焦点 → 讨论 → 定时」置前，其余任务状态按原相对顺序紧随其后。
 */
export const QUICK_SWITCHER_TAB_ORDER = [
  "focus",
  "discussion",
  "ongoing",
  "todo",
  "working",
  "review",
  "done",
] as const;

export type SwitcherTab = (typeof QUICK_SWITCHER_TAB_ORDER)[number];
