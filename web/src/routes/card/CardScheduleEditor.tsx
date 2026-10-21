import { useTranslation } from "react-i18next";
import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Clock, Plus } from "lucide-react";
import { toast } from "sonner";
import { z } from "zod";
import { api } from "../../shared/api/client";
import type {
  Schedule,
  ScheduleInput,
  TimelineEvent,
} from "../../shared/api/types";
import {
  Button,
  Input,
  LoadingButton,
  Switch,
  Typography,
} from "../../components/ui/controls";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogTrigger,
} from "../../components/ui/primitives";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../../components/ui/overlays";

const scheduleSchema = z.object({
  enabled: z.boolean(),
  frequency: z.enum(["daily", "weekly", "monthly"]),
  day_of_week: z.number().int().min(0).max(6),
  day_of_month: z.number().int().min(1).max(28),
  hour: z.number().int().min(0).max(23),
  minute: z.number().int().min(0).max(59),
  timezone: z
    .string()
    .trim()
    .min(1, "必须填写时区")
    .refine((value) => {
      try {
        new Intl.DateTimeFormat("en", { timeZone: value });
        return true;
      } catch {
        return false;
      }
    }, "请输入有效 IANA 时区"),
});
type ScheduleForm = z.infer<typeof scheduleSchema>;

const weekDays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

function asForm(schedule?: Schedule): ScheduleForm {
  return {
    enabled: schedule?.enabled ?? true,
    frequency: schedule?.frequency ?? "daily",
    day_of_week: schedule?.day_of_week ?? 1,
    day_of_month: schedule?.day_of_month ?? 1,
    hour: schedule?.hour ?? 9,
    minute: schedule?.minute ?? 0,
    timezone:
      schedule?.timezone ??
      Intl.DateTimeFormat().resolvedOptions().timeZone ??
      "Asia/Shanghai",
  };
}

function toPayload(form: ScheduleForm): ScheduleInput {
  const { frequency, enabled, hour, minute, timezone } = form;
  return {
    enabled,
    frequency,
    hour,
    minute,
    timezone,
    ...(frequency === "weekly" ? { day_of_week: form.day_of_week } : {}),
    ...(frequency === "monthly" ? { day_of_month: form.day_of_month } : {}),
  };
}

function labelOf(
  schedule: Schedule,
  t: (key: string) => string,
  language: string,
) {
  const day =
    schedule.frequency === "daily"
      ? t("每天")
      : schedule.frequency === "weekly"
        ? `${t("每周")}${language.startsWith("zh") ? "" : " "}${t(weekDays[schedule.day_of_week ?? 1])}`
        : `${t("每月")} ${schedule.day_of_month ?? 1} ${t("日")}`;
  return `${day} ${String(schedule.hour).padStart(2, "0")}:${String(schedule.minute).padStart(2, "0")} · ${schedule.timezone}`;
}

export function CardScheduleEditor({
  cardID,
  schedules,
  lastCheckin,
  onChanged,
}: {
  cardID: string;
  schedules: Schedule[];
  lastCheckin: TimelineEvent | null;
  onChanged: () => void;
}) {
  const { t, i18n } = useTranslation();
  const [editingID, setEditingID] = useState<string | null>(null);
  const form = useForm<ScheduleForm>({
    resolver: zodResolver(scheduleSchema),
    defaultValues: asForm(),
  });
  const frequency = useWatch({ control: form.control, name: "frequency" });
  const enabled = useWatch({ control: form.control, name: "enabled" });
  const changeEdit = (schedule?: Schedule) => {
    form.reset(asForm(schedule));
    setEditingID(schedule?.id ?? "new");
  };
  const save = useMutation({
    mutationFn: (values: ScheduleForm) =>
      editingID && editingID !== "new"
        ? api.updateSchedule(cardID, editingID, toPayload(values))
        : api.createSchedule(cardID, toPayload(values)),
    onSuccess: () => {
      onChanged();
      setEditingID(null);
      toast.success(t("定时计划已保存"));
    },
    onError: (error) => toast.error(String(error)),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.deleteSchedule(cardID, id),
    onSuccess: () => {
      onChanged();
      toast.success(t("计划已删除"));
    },
    onError: (error) => toast.error(String(error)),
  });
  return (
    <section className="space-y-3 border-t border-[var(--v2-border)] pt-3">
      <Typography as="h3" scale="section">
        {t("定时计划")}
      </Typography>
      {schedules.length === 0 && (
        <Typography scale="control" className="text-[var(--v2-muted)]">
          {t("暂无定时计划")}
        </Typography>
      )}
      {schedules.map((schedule) => (
        <div
          key={schedule.id}
          className="flex min-w-0 flex-wrap items-center gap-2 border-b border-[var(--v2-border)] pb-2"
        >
          <Clock size={14} color="var(--v2-muted)" aria-hidden="true" />
          <span className="min-w-0 flex-1 break-words">
            {labelOf(schedule, t, i18n.language)}{" "}
            {!schedule.enabled && t("· 已禁用")}
          </span>
          <Button size="compact" onClick={() => changeEdit(schedule)}>
            {t("编辑")}
          </Button>
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button size="compact" variant="ghost">
                {t("删除")}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent
              title={t("删除定时计划")}
              description={t(
                "确认删除此计划？如果已生成过历史触发，服务端可能拒绝删除；届时请编辑并禁用计划。",
              )}
              danger
              confirmLabel={t("确认删除")}
              disabled={remove.isPending}
              onConfirm={() => remove.mutate(schedule.id)}
            />
          </AlertDialog>
        </div>
      ))}
      {editingID === null ? (
        <Button size="compact" onClick={() => changeEdit()}>
          <Plus size={14} />
          {t("新增日程")}
        </Button>
      ) : (
        <form
          className="space-y-3 rounded-[var(--v2-radius)] bg-[var(--v2-subtle)] p-3"
          onSubmit={form.handleSubmit((values) => save.mutate(values))}
        >
          <Typography scale="control" className="font-semibold">
            {editingID === "new" ? t("新增日程") : t("编辑日程")}
          </Typography>
          <label className="block space-y-1">
            <Typography scale="meta">{t("周期")}</Typography>
            <Select
              value={frequency}
              onValueChange={(value) =>
                form.setValue("frequency", value as ScheduleForm["frequency"], {
                  shouldDirty: true,
                })
              }
            >
              <SelectTrigger aria-label={t("计划周期")}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="daily">{t("每天")}</SelectItem>
                <SelectItem value="weekly">{t("每周")}</SelectItem>
                <SelectItem value="monthly">{t("每月")}</SelectItem>
              </SelectContent>
            </Select>
          </label>
          {frequency === "weekly" && (
            <label className="block space-y-1">
              <Typography scale="meta">{t("星期")}</Typography>
              <Select
                value={String(form.getValues("day_of_week"))}
                onValueChange={(value) =>
                  form.setValue("day_of_week", Number(value), {
                    shouldDirty: true,
                  })
                }
              >
                <SelectTrigger aria-label={t("星期")}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {weekDays.map((day, index) => (
                    <SelectItem key={day} value={String(index)}>
                      {day}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </label>
          )}
          {frequency === "monthly" && (
            <label className="block space-y-1">
              <Typography scale="meta">{t("每月日期（1–28）")}</Typography>
              <Input
                type="number"
                min={1}
                max={28}
                {...form.register("day_of_month", { valueAsNumber: true })}
              />
            </label>
          )}
          <div className="grid grid-cols-2 gap-2">
            <label className="space-y-1">
              <Typography scale="meta">{t("小时")}</Typography>
              <Input
                type="number"
                min={0}
                max={23}
                {...form.register("hour", { valueAsNumber: true })}
              />
            </label>
            <label className="space-y-1">
              <Typography scale="meta">{t("分钟")}</Typography>
              <Input
                type="number"
                min={0}
                max={59}
                {...form.register("minute", { valueAsNumber: true })}
              />
            </label>
          </div>
          <label className="block space-y-1">
            <Typography scale="meta">{t("时区")}</Typography>
            <Input {...form.register("timezone")} placeholder="Asia/Shanghai" />
          </label>
          <label className="flex items-center gap-2">
            <Switch
              checked={enabled}
              onCheckedChange={(value) =>
                form.setValue("enabled", value, { shouldDirty: true })
              }
            />
            {t("启用计划")}
          </label>
          {Object.keys(form.formState.errors).length > 0 && (
            <p role="alert" className="v2-text-meta text-[var(--v2-danger)]">
              {Object.values(form.formState.errors)
                .map((error) => error.message && t(error.message))
                .filter(Boolean)
                .join("；")}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <LoadingButton
              type="submit"
              variant="primary"
              size="compact"
              loading={save.isPending}
            >
              {editingID === "new" ? t("新增日程") : t("保存日程")}
            </LoadingButton>
            <Button
              type="button"
              size="compact"
              onClick={() => setEditingID(null)}
              disabled={save.isPending}
            >
              {t("取消")}
            </Button>
          </div>
        </form>
      )}
      {lastCheckin && (
        <div>
          <Typography scale="meta">{t("最近检查")}</Typography>
          <p className="break-words">{lastCheckin.content}</p>
        </div>
      )}
    </section>
  );
}
