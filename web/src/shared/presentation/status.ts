import type { Card } from "../api/types";

type Translate = (key: string) => string;

const todoStateKeys: Record<string, string> = {
  todo: "待办",
  working: "进行中",
  review: "待审核",
  done: "已完成",
};

const ongoingStateKeys: Record<string, string> = {
  active: "运行中",
  paused: "已暂停",
  archived: "已归档",
};

const discussionStateKeys: Record<string, string> = {
  active: "讨论中",
  archived: "已归档",
};

const healthKeys: Record<string, string> = {
  healthy: "健康",
  warning: "警告",
  critical: "严重",
  unknown: "未知",
};

const runStateKeys: Record<string, string> = {
  queued: "等待执行",
  running: "正在执行",
  waiting_human: "等待你的回复",
  completed: "执行完成",
  failed: "执行失败",
  superseded: "已被新要求替代",
  expired: "执行已过期",
  cancelled: "已取消执行",
};

export function cardStateLabel(t: Translate, card: Card) {
  const state =
    card.kind === "todo"
      ? card.todo_state
      : card.kind === "ongoing"
        ? card.ongoing_state
        : card.discussion_state;
  const keys =
    card.kind === "todo"
      ? todoStateKeys
      : card.kind === "ongoing"
        ? ongoingStateKeys
        : discussionStateKeys;
  return state ? t(keys[state] || state) : "—";
}

export function healthLabel(t: Translate, health?: string) {
  const value = health || "unknown";
  return t(healthKeys[value] || value);
}

export function runStateLabel(t: Translate, status?: string) {
  if (!status) return "—";
  return t(runStateKeys[status] || status);
}
