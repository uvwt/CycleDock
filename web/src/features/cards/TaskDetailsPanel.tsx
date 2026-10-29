import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  scheduleFormDefaults,
  scheduleFormInput,
  scheduleFormSchema,
  type ScheduleFormValues,
} from "./schedule-form";
import { useMemo, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import {
  CalendarClock,
  CalendarPlus,
  Link2,
  Pencil,
  Save,
  Trash2,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import type {
  Card,
  CardDetail,
  Schedule,
  ScheduleInput,
} from "../../shared/api/types";
import {
  Button,
  Field,
  Section,
  Select,
  TextInput,
} from "../../shared/ui/components";

export function TaskDetailsPanel({
  detail,
  projectCards,
  onChange,
}: {
  detail: CardDetail;
  projectCards: Card[];
  onChange: () => Promise<void>;
}) {
  const navigate = useNavigate();
  const card = detail.card;
  const source = useMemo(
    () =>
      card.source_discussion_id
        ? projectCards.find((item) => item.id === card.source_discussion_id)
        : undefined,
    [card.source_discussion_id, projectCards],
  );

  return (
    <div className="max-h-[68vh] space-y-4 overflow-y-auto pr-1">
      <Section title="任务目标">
        <p className="whitespace-pre-wrap text-sm leading-6 text-secondary-foreground">
          {card.description || "暂无描述"}
        </p>
        {source && (
          <button
            type="button"
            onClick={() => navigate(`/chat/${source.project_id}/${source.id}`)}
            className="mt-3 inline-flex max-w-full items-center gap-1.5 text-left text-xs text-muted-foreground hover:text-sky-700 hover:underline"
          >
            <Link2 className="size-3.5 shrink-0 text-faint" />
            <span className="shrink-0 text-faint">来源 ·</span>
            <span className="truncate">{source.title}</span>
          </button>
        )}
      </Section>

      {card.kind === "ongoing" && (
        <>
          <Section title="持续事项">
            <p className="text-sm text-secondary-foreground">
              健康状态：{detail.health || "unknown"}
            </p>
            {detail.last_checkin && (
              <p className="mt-2 text-xs leading-5 text-faint">
                最近巡检：{detail.last_checkin.content || "已记录"}
              </p>
            )}
          </Section>

          <ScheduleEditor
            cardID={card.id}
            schedules={detail.schedules}
            onChange={onChange}
          />
        </>
      )}
    </div>
  );
}

function ScheduleEditor({
  cardID,
  schedules,
  onChange,
}: {
  cardID: string;
  schedules: Schedule[];
  onChange: () => Promise<void>;
}) {
  const [editingID, setEditingID] = useState<string | "new" | null>(null);
  const editing = schedules.find((schedule) => schedule.id === editingID);

  const save = useMutation({
    mutationFn: (input: ScheduleInput) =>
      editing
        ? api.updateSchedule(cardID, editing.id, input)
        : api.createSchedule(cardID, input),
    onSuccess: async () => {
      await onChange();
      setEditingID(null);
      toast.success(editing ? "日程已更新" : "日程已添加");
    },
    // 保存失败时保留表单，用户修正后可以直接重试。
    onError: showError,
  });

  const toggle = useMutation({
    mutationFn: (schedule: Schedule) =>
      api.updateSchedule(cardID, schedule.id, {
        frequency: schedule.frequency,
        hour: schedule.hour,
        minute: schedule.minute,
        timezone: schedule.timezone,
        enabled: !schedule.enabled,
        ...(schedule.day_of_week !== undefined
          ? { day_of_week: schedule.day_of_week }
          : {}),
        ...(schedule.day_of_month !== undefined
          ? { day_of_month: schedule.day_of_month }
          : {}),
      }),
    onSuccess: onChange,
    onError: showError,
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteSchedule(cardID, id),
    onSuccess: async () => {
      await onChange();
      setEditingID(null);
    },
    onError: showError,
  });

  return (
    <Section
      title="定时触发"
      description="日程只负责创建新的 Turn，不会绕过当前执行和排队规则。"
    >
      <div className="space-y-2">
        {schedules.length ? (
          schedules.map((schedule) => (
            <div key={schedule.id} className="rounded-xl bg-canvas p-3 text-sm">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="font-semibold text-foreground">
                    {describeSchedule(schedule)}
                  </p>
                  <p className="mt-1 text-xs leading-5 text-muted-foreground">
                    下次 {new Date(schedule.next_trigger_at).toLocaleString()}
                    {" · "}
                    {schedule.timezone}
                    {!schedule.enabled ? " · 已暂停" : ""}
                  </p>
                </div>
                <div className="flex shrink-0 gap-1">
                  <Button
                    variant="ghost"
                    disabled={save.isPending}
                    onClick={() => {
                      setEditingID(schedule.id);
                    }}
                  >
                    <Pencil className="size-3.5" />
                    编辑
                  </Button>
                  <Button
                    variant="ghost"
                    disabled={toggle.isPending}
                    onClick={() => toggle.mutate(schedule)}
                  >
                    {schedule.enabled ? "暂停" : "启用"}
                  </Button>
                  <Button
                    variant="danger"
                    title="删除日程"
                    aria-label="删除日程"
                    disabled={remove.isPending}
                    onClick={() => remove.mutate(schedule.id)}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
              </div>
            </div>
          ))
        ) : (
          <p className="text-sm text-faint">暂无定时日程</p>
        )}
      </div>

      {editingID ? (
        <ScheduleForm
          key={editingID}
          schedule={editing}
          pending={save.isPending}
          onSave={(input) => save.mutate(input)}
          onCancel={() => setEditingID(null)}
        />
      ) : (
        <Button
          className="mt-4"
          onClick={() => {
            setEditingID("new");
          }}
        >
          <CalendarPlus className="size-4" />
          新增日程
        </Button>
      )}
    </Section>
  );
}

function ScheduleForm({
  schedule,
  pending,
  onSave,
  onCancel,
}: {
  schedule?: Schedule;
  pending: boolean;
  onSave: (input: ScheduleInput) => void;
  onCancel: () => void;
}) {
  const {
    register,
    control,
    handleSubmit,
    formState: { errors },
  } = useForm<ScheduleFormValues>({
    resolver: zodResolver(scheduleFormSchema),
    defaultValues: scheduleFormDefaults(schedule),
    // 频率切换后移除无关日期，避免隐藏字段阻止提交或泄漏到请求。
    shouldUnregister: true,
  });
  const frequency = useWatch({ control, name: "frequency" });

  return (
    <form
      noValidate
      onSubmit={handleSubmit((value) => {
        if (!pending)
          onSave(scheduleFormInput(value, schedule?.enabled ?? true));
      })}
      className="mt-4 space-y-3 rounded-xl border border-dashed border-line p-3"
    >
      <p className="flex items-center gap-2 text-sm font-semibold text-foreground">
        <CalendarClock className="size-4" />
        {schedule ? "编辑日程" : "新增日程"}
      </p>
      <fieldset disabled={pending} className="space-y-3">
        <Field label="频率" error={errors.frequency?.message}>
          <Select
            aria-label="频率"
            {...register("frequency")}
            aria-invalid={!!errors.frequency}
          >
            <option value="daily">每天</option>
            <option value="weekly">每周</option>
            <option value="monthly">每月</option>
          </Select>
        </Field>
        {frequency === "weekly" && (
          <Field label="星期（0=周日）" error={errors.weekday?.message}>
            <TextInput
              type="number"
              min={0}
              max={6}
              {...register("weekday", { valueAsNumber: true })}
              aria-invalid={!!errors.weekday}
            />
          </Field>
        )}
        {frequency === "monthly" && (
          <Field label="日期（1–28）" error={errors.monthday?.message}>
            <TextInput
              type="number"
              min={1}
              max={28}
              {...register("monthday", { valueAsNumber: true })}
              aria-invalid={!!errors.monthday}
            />
          </Field>
        )}
        <Field label="时间" error={errors.time?.message}>
          <TextInput
            type="time"
            {...register("time")}
            aria-invalid={!!errors.time}
          />
        </Field>
        <Field label="IANA 时区" error={errors.timezone?.message}>
          <TextInput
            {...register("timezone")}
            aria-invalid={!!errors.timezone}
          />
        </Field>
        <div className="flex flex-wrap gap-2">
          <Button type="submit" variant="primary" disabled={pending}>
            <Save className="size-4" />
            保存
          </Button>
          <Button disabled={pending} onClick={onCancel}>
            <X className="size-4" />
            取消
          </Button>
        </div>
      </fieldset>
    </form>
  );
}

function describeSchedule(schedule: Schedule) {
  const clock = `${String(schedule.hour).padStart(2, "0")}:${String(
    schedule.minute,
  ).padStart(2, "0")}`;
  if (schedule.frequency === "weekly") {
    const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
    return `每周${weekdays[schedule.day_of_week ?? 0]} ${clock}`;
  }
  if (schedule.frequency === "monthly") {
    return `每月 ${schedule.day_of_month ?? 1} 日 ${clock}`;
  }
  return `每天 ${clock}`;
}

function showError(error: unknown) {
  toast.error(error instanceof Error ? error.message : "操作失败");
}
