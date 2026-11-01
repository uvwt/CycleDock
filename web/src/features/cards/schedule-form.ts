import { z } from "zod";
import type { Schedule, ScheduleInput } from "../../shared/api/types";

export const scheduleFormSchema = z
  .object({
    frequency: z.enum(["daily", "weekly", "monthly", "interval"]),
    weekday: z
      .number()
      .int()
      .min(0, "星期应为 0–6")
      .max(6, "星期应为 0–6")
      .optional(),
    monthday: z
      .number()
      .int()
      .min(1, "日期应为 1–28")
      .max(28, "日期应为 1–28")
      .optional(),
    interval_minutes: z
      .number()
      .int()
      .min(1, "间隔应为 1–1440 分钟")
      .max(1440, "间隔应为 1–1440 分钟")
      .optional(),
    time: z.string().optional(),
    timezone: z
      .string()
      .trim()
      .min(1, "请输入时区")
      .refine((value) => {
        try {
          new Intl.DateTimeFormat("en", { timeZone: value });
          return true;
        } catch {
          return false;
        }
      }, "请输入有效的 IANA 时区，如 Asia/Shanghai"),
  })
  .superRefine((value, context) => {
    if (value.frequency === "interval") {
      if (
        value.interval_minutes === undefined ||
        Number.isNaN(value.interval_minutes)
      ) {
        context.addIssue({
          code: "custom",
          path: ["interval_minutes"],
          message: "请输入间隔分钟",
        });
      }
      return;
    }
    if (!value.time || !/^([01]\d|2[0-3]):[0-5]\d$/.test(value.time)) {
      context.addIssue({
        code: "custom",
        path: ["time"],
        message: "请输入有效时间",
      });
    }
    if (value.frequency === "weekly" && value.weekday === undefined) {
      context.addIssue({
        code: "custom",
        path: ["weekday"],
        message: "请选择星期",
      });
    }
    if (value.frequency === "monthly" && value.monthday === undefined) {
      context.addIssue({
        code: "custom",
        path: ["monthday"],
        message: "请输入日期",
      });
    }
  });

export type ScheduleFormValues = z.infer<typeof scheduleFormSchema>;

export function scheduleFormDefaults(schedule?: Schedule): ScheduleFormValues {
  return {
    frequency: schedule?.frequency ?? "daily",
    weekday: schedule?.day_of_week ?? 1,
    monthday: schedule?.day_of_month ?? 1,
    interval_minutes: schedule?.interval_minutes ?? 5,
    time:
      schedule && schedule.frequency !== "interval"
        ? `${String(schedule.hour).padStart(2, "0")}:${String(schedule.minute).padStart(2, "0")}`
        : "09:00",
    timezone:
      schedule?.timezone ||
      Intl.DateTimeFormat().resolvedOptions().timeZone ||
      "Asia/Shanghai",
  };
}

export function scheduleFormInput(
  value: ScheduleFormValues,
  enabled: boolean,
): ScheduleInput {
  if (value.frequency === "interval") {
    return {
      frequency: "interval",
      hour: 0,
      minute: 0,
      interval_minutes: value.interval_minutes,
      timezone: value.timezone,
      enabled,
    };
  }
  const [hour, minute] = (value.time ?? "09:00").split(":").map(Number);
  return {
    frequency: value.frequency,
    hour,
    minute,
    timezone: value.timezone,
    enabled,
    ...(value.frequency === "weekly" ? { day_of_week: value.weekday } : {}),
    ...(value.frequency === "monthly" ? { day_of_month: value.monthday } : {}),
  };
}
