import type { OngoingState, ProjectSchedule } from "../../shared/api/types";

export type ScheduleSummaryInput = Pick<
  ProjectSchedule,
  | "enabled"
  | "frequency"
  | "day_of_week"
  | "day_of_month"
  | "interval_minutes"
  | "hour"
  | "minute"
  | "next_trigger_at"
  | "last_attempt_status"
>;

export type TranslateFn = (key: string) => string;

export type PlanScheduleSummary = {
  /** 合并后的周期规则描述；未配置任何规则时为空字符串 */
  rulesText: string;
  /** 最早且合法的唯一一次「下次」触发文案（如 "下次 今天 13:17"）；不展示时为 null */
  nextTriggerText: string | null;
  /** 卡片已暂停/已归档，或全部规则均已停用时的简短状态提示 */
  statusHint: string | null;
  /** 任意已启用规则最近一次执行失败 */
  hasFailedAttempt: boolean;
  /** 卡片是否尚未配置任何排程规则 */
  isEmpty: boolean;
};

const WEEKDAYS = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"] as const;
const MS_PER_DAY = 86_400_000;
const identityTranslate: TranslateFn = (key) => key;

function pad2(value: number): string {
  return String(value).padStart(2, "0");
}

function normalizeClock(hour: number, minute: number): {
  minutes: number;
  text: string;
} {
  const safeHour = Number.isFinite(hour)
    ? Math.max(0, Math.min(23, Math.trunc(hour)))
    : 0;
  const safeMinute = Number.isFinite(minute)
    ? Math.max(0, Math.min(59, Math.trunc(minute)))
    : 0;
  return {
    minutes: safeHour * 60 + safeMinute,
    text: `${pad2(safeHour)}:${pad2(safeMinute)}`,
  };
}

function normalizeWeekday(day?: number): number {
  const raw = typeof day === "number" && Number.isFinite(day) ? Math.trunc(day) : 0;
  return raw >= 0 && raw <= 6 ? raw : 0;
}

// 中文习惯按周一到周日排序（周一=1..周六=6, 周日=7），避免“周日、周六”这类倒置展示。
function weekdaySortOrder(day: number): number {
  return day === 0 ? 7 : day;
}

function normalizeMonthDay(day?: number): number {
  const raw = typeof day === "number" && Number.isFinite(day) ? Math.trunc(day) : 1;
  return raw >= 1 && raw <= 31 ? raw : 1;
}

function normalizeIntervalMinutes(minutes?: number): number {
  const raw =
    typeof minutes === "number" && Number.isFinite(minutes) ? Math.trunc(minutes) : 5;
  return raw >= 1 ? raw : 5;
}

/**
 * 将单个定时卡片下的多条周期规则按频率自然合并并排序，
 * 避免列表页逐行堆叠相似规则。
 */
function mergeScheduleRules(rules: readonly ScheduleSummaryInput[]): string {
  const intervals = new Set<number>();
  const dailyTimes = new Map<number, string>();
  const weeklyByTime = new Map<number, { timeText: string; days: Set<number> }>();
  const monthlyByTime = new Map<number, { timeText: string; days: Set<number> }>();

  for (const rule of rules) {
    if (rule.frequency === "interval") {
      intervals.add(normalizeIntervalMinutes(rule.interval_minutes));
      continue;
    }

    const clock = normalizeClock(rule.hour, rule.minute);
    if (rule.frequency === "daily") {
      dailyTimes.set(clock.minutes, clock.text);
      continue;
    }

    if (rule.frequency === "weekly") {
      const weekday = normalizeWeekday(rule.day_of_week);
      const group = weeklyByTime.get(clock.minutes);
      if (group) {
        group.days.add(weekday);
      } else {
        weeklyByTime.set(clock.minutes, {
          timeText: clock.text,
          days: new Set([weekday]),
        });
      }
      continue;
    }

    const monthDay = normalizeMonthDay(rule.day_of_month);
    const group = monthlyByTime.get(clock.minutes);
    if (group) {
      group.days.add(monthDay);
    } else {
      monthlyByTime.set(clock.minutes, {
        timeText: clock.text,
        days: new Set([monthDay]),
      });
    }
  }

  const segments: string[] = [];

  for (const minutes of [...intervals].sort((a, b) => a - b)) {
    segments.push(`每 ${minutes} 分钟`);
  }

  if (dailyTimes.size > 0) {
    const orderedTimes = [...dailyTimes.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([, text]) => text);
    segments.push(`每天 ${orderedTimes.join("、")}`);
  }

  if (weeklyByTime.size > 0) {
    const weeklyGroups = [...weeklyByTime.entries()]
      .map(([timeMinutes, group]) => ({
        timeMinutes,
        timeText: group.timeText,
        days: [...group.days].sort((a, b) => weekdaySortOrder(a) - weekdaySortOrder(b)),
      }))
      .sort(
        (a, b) =>
          weekdaySortOrder(a.days[0]) - weekdaySortOrder(b.days[0]) ||
          a.timeMinutes - b.timeMinutes,
      );

    for (const group of weeklyGroups) {
      const dayText = group.days.map((day) => WEEKDAYS[day]).join("、");
      segments.push(`每${dayText} ${group.timeText}`);
    }
  }

  if (monthlyByTime.size > 0) {
    const monthlyGroups = [...monthlyByTime.entries()]
      .map(([timeMinutes, group]) => ({
        timeMinutes,
        timeText: group.timeText,
        days: [...group.days].sort((a, b) => a - b),
      }))
      .sort((a, b) => a.days[0] - b.days[0] || a.timeMinutes - b.timeMinutes);

    for (const group of monthlyGroups) {
      segments.push(`每月 ${group.days.join("、")} 日 ${group.timeText}`);
    }
  }

  return segments.join(" · ");
}

/**
 * 格式化单次触发时间：今天/明天直接展示相对日期，其余展示 MM/DD HH:mm。
 */
export function formatNextTriggerTime(
  value?: string,
  now: Date = new Date(),
  t: TranslateFn = identityTranslate,
): string | null {
  if (!value || !value.trim()) {
    return null;
  }
  const target = new Date(value);
  if (Number.isNaN(target.getTime())) {
    return null;
  }

  // 按本地日历零点计算相差天数，跨午夜或夏令时切换时仍能准确区分“今天/明天”。
  const todayStart = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate(),
  ).getTime();
  const targetStart = new Date(
    target.getFullYear(),
    target.getMonth(),
    target.getDate(),
  ).getTime();
  const dayDiff = Math.round((targetStart - todayStart) / MS_PER_DAY);
  const hhmm = `${pad2(target.getHours())}:${pad2(target.getMinutes())}`;

  if (dayDiff === 0) {
    return `${t("下次")} ${t("今天")} ${hhmm}`;
  }
  if (dayDiff === 1) {
    return `${t("下次")} ${t("明天")} ${hhmm}`;
  }
  return `${t("下次")} ${pad2(target.getMonth() + 1)}/${pad2(target.getDate())} ${hhmm}`;
}

export type SummarizePlanSchedulesOptions = {
  ongoingState?: OngoingState;
  now?: Date;
  t?: TranslateFn;
};

/**
 * 汇总单张定时卡片的排程摘要：
 * - 有启用规则时仅汇总启用规则，忽略已停用的历史规则；
 * - 全部规则停用时仍保留合并后的周期文本，并标明“已停用”；
 * - 卡片运行中时仅展示所有启用规则里最早且合法的 1 个“下次”时间。
 */
export function summarizePlanSchedules(
  schedules: readonly ScheduleSummaryInput[],
  options: SummarizePlanSchedulesOptions = {},
): PlanScheduleSummary {
  const {
    ongoingState = "active",
    now = new Date(),
    t = identityTranslate,
  } = options;
  if (schedules.length === 0) {
    return {
      rulesText: "",
      nextTriggerText: null,
      statusHint: null,
      hasFailedAttempt: false,
      isEmpty: true,
    };
  }

  const enabledSchedules = schedules.filter((item) => item.enabled);
  const displayRules =
    enabledSchedules.length > 0 ? enabledSchedules : schedules;
  const rulesText = mergeScheduleRules(displayRules);
  const hasFailedAttempt = enabledSchedules.some(
    (item) => item.last_attempt_status === "failed",
  );

  if (ongoingState === "paused") {
    return {
      rulesText,
      nextTriggerText: null,
      statusHint: t("已暂停"),
      hasFailedAttempt,
      isEmpty: false,
    };
  }

  if (ongoingState === "archived") {
    return {
      rulesText,
      nextTriggerText: null,
      statusHint: t("已归档"),
      hasFailedAttempt,
      isEmpty: false,
    };
  }

  if (enabledSchedules.length === 0) {
    return {
      rulesText,
      nextTriggerText: null,
      statusHint: t("已停用"),
      hasFailedAttempt: false,
      isEmpty: false,
    };
  }

  let earliestISO: string | undefined;
  let earliestMs = Number.POSITIVE_INFINITY;
  for (const item of enabledSchedules) {
    if (!item.next_trigger_at || !item.next_trigger_at.trim()) {
      continue;
    }
    const ms = new Date(item.next_trigger_at).getTime();
    if (!Number.isNaN(ms) && ms < earliestMs) {
      earliestMs = ms;
      earliestISO = item.next_trigger_at;
    }
  }

  return {
    rulesText,
    nextTriggerText: formatNextTriggerTime(earliestISO, now, t),
    statusHint: null,
    hasFailedAttempt,
    isEmpty: false,
  };
}
