import { useMemo, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import {
  Archive,
  CalendarClock,
  CalendarPlus,
  Link2,
  MoreHorizontal,
  Pause,
  Pencil,
  Play,
  Save,
  Trash2,
  X,
} from "lucide-react";
import { api } from "../../shared/api/client";
import type {
  Card,
  CardDetail,
  Schedule,
  ScheduleInput,
  TodoState,
} from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  Field,
  Section,
  TextArea,
  TextInput,
} from "../../shared/ui/components";

const todoStates: TodoState[] = ["todo", "working", "review", "done"];
const stateLabels: Record<TodoState, string> = {
  todo: "待办",
  working: "进行中",
  review: "待审核",
  done: "已完成",
};

export function CardDetails({
  detail,
  projectCards,
  onDeleted,
  hideHeading = false,
  compact = false,
}: {
  detail: CardDetail;
  projectCards: Card[];
  onDeleted: () => void;
  hideHeading?: boolean;
  compact?: boolean;
}) {
  const card = detail.card;
  const navigate = useNavigate();
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(card.title);
  const [description, setDescription] = useState(card.description);
  const source = useMemo(
    () =>
      card.source_discussion_id
        ? projectCards.find((item) => item.id === card.source_discussion_id)
        : undefined,
    [card.source_discussion_id, projectCards],
  );

  const sync = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.card(card.id) }),
      queryClient.invalidateQueries({
        queryKey: keys.cards(card.project_id),
      }),
    ]);
  };

  const update = useMutation({
    mutationFn: () => api.updateCard(card.id, title.trim(), description.trim()),
    onSuccess: async () => {
      await sync();
      setEditing(false);
      toast.success("已保存");
    },
    // 保存失败时不退出编辑态，也不覆盖输入值。
    onError: showError,
  });

  const move = useMutation({
    mutationFn: (state: TodoState) => api.moveTodo(card.id, state),
    onSuccess: sync,
    onError: showError,
  });

  const ongoing = useMutation({
    mutationFn: (state: string) => api.ongoingState(card.id, state),
    onSuccess: sync,
    onError: showError,
  });

  const archive = useMutation({
    mutationFn: () => api.archiveDiscussion(card.id),
    onSuccess: sync,
    onError: showError,
  });

  const del = useMutation({
    mutationFn: () => api.deleteDiscussion(card.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: keys.cards(card.project_id),
      });
      onDeleted();
    },
    onError: showError,
  });

  function cancelEdit() {
    setTitle(card.title);
    setDescription(card.description);
    setEditing(false);
  }

  function openSource() {
    if (!source) return;
    navigate("/chat/" + source.project_id + "/" + source.id);
  }

  return (
    <div className={compact ? "space-y-3" : "space-y-4 pb-10"}>
      <section className={compact ? "bg-white" : "rounded-2xl border border-slate-200 bg-white p-4"}>
        <div className="flex items-center justify-between gap-2">
          {!hideHeading && (
            <h2 className="text-sm font-semibold text-slate-900">
              {card.kind === "discussion" ? "讨论信息" : "任务信息"}
            </h2>
          )}
          {!editing && card.kind !== "discussion" && (
            <button
              type="button"
              onClick={() => setEditing(true)}
              title="编辑任务信息"
              aria-label="编辑任务信息"
              className="ml-auto grid size-8 shrink-0 place-items-center rounded-lg text-slate-400 hover:bg-slate-100 hover:text-slate-700"
            >
              <Pencil className="size-4" />
            </button>
          )}
          {card.kind === "discussion" && (
            <details className="relative ml-auto">
              <summary
                title="讨论操作"
                aria-label="讨论操作"
                className="grid size-8 cursor-pointer list-none place-items-center rounded-lg text-slate-500 hover:bg-slate-100 hover:text-slate-700"
              >
                <MoreHorizontal className="size-4" />
              </summary>
              <div className="absolute top-9 right-0 z-30 w-36 rounded-xl border border-slate-200 bg-white p-1 shadow-lg">
                {card.discussion_state === "active" && (
                  <button
                    type="button"
                    disabled={archive.isPending}
                    onClick={(event) => {
                      event.currentTarget.closest("details")?.removeAttribute("open");
                      archive.mutate();
                    }}
                    className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-slate-700 hover:bg-slate-50 disabled:opacity-50"
                  >
                    <Archive className="size-3.5" />
                    归档讨论
                  </button>
                )}
                <button
                  type="button"
                  disabled={del.isPending}
                  onClick={(event) => {
                    event.currentTarget.closest("details")?.removeAttribute("open");
                    if (window.confirm("确定删除这个讨论？已生成的任务不会被删除。")) {
                      del.mutate();
                    }
                  }}
                  className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs text-rose-700 hover:bg-rose-50 disabled:opacity-50"
                >
                  <Trash2 className="size-3.5" />
                  删除讨论
                </button>
              </div>
            </details>
          )}
        </div>
        {editing && card.kind !== "discussion" ? (
          <div className="mt-3 space-y-4">
            <Field label="标题">
              <TextInput
                autoFocus
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                placeholder="任务标题"
              />
            </Field>
            <Field label="任务描述">
              <TextArea
                value={description}
                onChange={(event) => setDescription(event.target.value)}
                rows={5}
                placeholder="目标、范围与完成条件"
              />
            </Field>
            <div className="flex flex-wrap gap-2">
              <Button
                variant="primary"
                disabled={!title.trim() || update.isPending}
                onClick={() => update.mutate()}
              >
                <Save className="size-4" />
                保存
              </Button>
              <Button disabled={update.isPending} onClick={cancelEdit}>
                <X className="size-4" />
                取消
              </Button>
            </div>
          </div>
        ) : (
          <div className="mt-3 space-y-3">
            <p className="whitespace-pre-wrap text-sm leading-6 text-slate-600">
              {card.description || "暂无描述"}
            </p>
            {source && (
              <button
                type="button"
                onClick={openSource}
                className="inline-flex max-w-full items-center gap-1 text-left text-[11px] leading-4 text-slate-500 hover:text-sky-700 hover:underline"
              >
                <Link2 className="size-3 shrink-0 text-slate-400" />
                <span className="shrink-0 text-slate-400">来源 ·</span>
                <span className="truncate">{source.title}</span>
              </button>
            )}
          </div>
        )}
      </section>

      {card.kind === "todo" && !compact && (
        <Section title="任务状态">
          <div className="flex flex-wrap gap-2">
            {todoStates.map((state) => (
              <Button
                key={state}
                variant={state === card.todo_state ? "primary" : "secondary"}
                disabled={
                  move.isPending ||
                  state === card.todo_state ||
                  (state === "done" && card.todo_state !== "review")
                }
                onClick={() => move.mutate(state)}
              >
                {stateLabels[state]}
              </Button>
            ))}
          </div>
          <p className="mt-3 text-xs leading-5 text-slate-500">
            最终 Done 仍由人类确认；Agent 只负责把完成工作提交到 Review。
          </p>
        </Section>
      )}

      {card.kind === "ongoing" && (
        <>
          <Section title="持续事项">
            {!compact && <div className="flex flex-wrap gap-2">
              {(["active", "paused", "archived"] as const).map((state) => (
                <Button
                  key={state}
                  disabled={ongoing.isPending || card.ongoing_state === state}
                  variant={
                    card.ongoing_state === state ? "primary" : "secondary"
                  }
                  onClick={() => ongoing.mutate(state)}
                >
                  {state === "active" ? (
                    <Play className="size-4" />
                  ) : state === "paused" ? (
                    <Pause className="size-4" />
                  ) : (
                    <Archive className="size-4" />
                  )}
                  {state === "active"
                    ? "运行中"
                    : state === "paused"
                      ? "已暂停"
                      : "已归档"}
                </Button>
              ))}
            </div>}
            <p className="mt-3 text-sm text-slate-500">
              健康状态：{detail.health || "unknown"}
            </p>
            {detail.last_checkin && (
              <p className="mt-1 text-xs leading-5 text-slate-400">
                最近巡检：{detail.last_checkin.content || "已记录"}
              </p>
            )}
          </Section>

          <ScheduleEditor
            cardID={card.id}
            schedules={detail.schedules}
            onChange={sync}
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
  const [frequency, setFrequency] =
    useState<ScheduleInput["frequency"]>("daily");
  const [weekday, setWeekday] = useState(1);
  const [monthday, setMonthday] = useState(1);
  const [time, setTime] = useState("09:00");
  const [timezone, setTimezone] = useState(
    Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Shanghai",
  );
  const editing = schedules.find((schedule) => schedule.id === editingID);

  function resetForm(schedule?: Schedule) {
    setFrequency(schedule?.frequency || "daily");
    setWeekday(schedule?.day_of_week ?? 1);
    setMonthday(schedule?.day_of_month ?? 1);
    setTime(
      schedule
        ? String(schedule.hour).padStart(2, "0") +
            ":" +
            String(schedule.minute).padStart(2, "0")
        : "09:00",
    );
    setTimezone(
      schedule?.timezone ||
        Intl.DateTimeFormat().resolvedOptions().timeZone ||
        "Asia/Shanghai",
    );
  }

  function input(enabled: boolean): ScheduleInput {
    const [hour, minute] = time.split(":").map(Number);
    return {
      frequency,
      hour,
      minute,
      timezone: timezone.trim(),
      enabled,
      ...(frequency === "weekly" ? { day_of_week: weekday } : {}),
      ...(frequency === "monthly" ? { day_of_month: monthday } : {}),
    };
  }

  const save = useMutation({
    mutationFn: () =>
      editing
        ? api.updateSchedule(cardID, editing.id, input(editing.enabled))
        : api.createSchedule(cardID, input(true)),
    onSuccess: async () => {
      await onChange();
      setEditingID(null);
      toast.success(editing ? "日程已更新" : "日程已添加");
    },
    // 失败时保留当前表单和输入，便于修正后重试。
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

  const del = useMutation({
    mutationFn: (id: string) => api.deleteSchedule(cardID, id),
    onSuccess: async () => {
      await onChange();
      setEditingID(null);
    },
    onError: showError,
  });

  function openNew() {
    resetForm();
    setEditingID("new");
  }

  function openEdit(schedule: Schedule) {
    resetForm(schedule);
    setEditingID(schedule.id);
  }

  return (
    <Section
      title="定时触发"
      description="日程只负责创建 Trigger，不会绕过当前运行和排队规则。"
    >
      <div className="space-y-2">
        {schedules.length ? (
          schedules.map((schedule) => (
            <div
              key={schedule.id}
              className="rounded-xl bg-slate-50 p-3 text-sm"
            >
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="font-semibold text-slate-700">
                    {describeSchedule(schedule)}
                  </p>
                  <p className="mt-1 text-xs leading-5 text-slate-500">
                    下次 {new Date(schedule.next_trigger_at).toLocaleString()}
                    {" · "}
                    {schedule.timezone}
                    {!schedule.enabled ? " · 已暂停" : ""}
                  </p>
                </div>
                <div className="flex shrink-0 gap-1">
                  <Button
                    variant="ghost"
                    onClick={() => openEdit(schedule)}
                    disabled={save.isPending}
                  >
                    <Pencil className="size-3.5" />
                    编辑
                  </Button>
                  <Button
                    variant="ghost"
                    onClick={() => toggle.mutate(schedule)}
                    disabled={toggle.isPending}
                  >
                    {schedule.enabled ? "暂停" : "启用"}
                  </Button>
                  <Button
                    variant="danger"
                    title="删除日程"
                    aria-label="删除日程"
                    onClick={() => del.mutate(schedule.id)}
                    disabled={del.isPending}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
              </div>
            </div>
          ))
        ) : (
          <p className="text-sm text-slate-400">暂无定时日程</p>
        )}
      </div>

      {editingID ? (
        <ScheduleForm
          frequency={frequency}
          setFrequency={setFrequency}
          weekday={weekday}
          setWeekday={setWeekday}
          monthday={monthday}
          setMonthday={setMonthday}
          time={time}
          setTime={setTime}
          timezone={timezone}
          setTimezone={setTimezone}
          editing={Boolean(editing)}
          pending={save.isPending}
          onSave={() => save.mutate()}
          onCancel={() => setEditingID(null)}
        />
      ) : (
        <Button className="mt-4" onClick={openNew}>
          <CalendarPlus className="size-4" />
          新增日程
        </Button>
      )}
    </Section>
  );
}

function ScheduleForm({
  frequency,
  setFrequency,
  weekday,
  setWeekday,
  monthday,
  setMonthday,
  time,
  setTime,
  timezone,
  setTimezone,
  editing,
  pending,
  onSave,
  onCancel,
}: {
  frequency: ScheduleInput["frequency"];
  setFrequency: (value: ScheduleInput["frequency"]) => void;
  weekday: number;
  setWeekday: (value: number) => void;
  monthday: number;
  setMonthday: (value: number) => void;
  time: string;
  setTime: (value: string) => void;
  timezone: string;
  setTimezone: (value: string) => void;
  editing: boolean;
  pending: boolean;
  onSave: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="mt-4 space-y-3 rounded-xl border border-dashed border-slate-200 p-3">
      <p className="flex items-center gap-2 text-sm font-semibold">
        <CalendarClock className="size-4" />
        {editing ? "编辑日程" : "新增日程"}
      </p>
      <label className="block text-xs font-semibold text-slate-600">
        频率
        <select
          aria-label="频率"
          value={frequency}
          onChange={(event) =>
            setFrequency(event.target.value as ScheduleInput["frequency"])
          }
          className="mt-1.5 w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-[13px] font-normal leading-5"
        >
          <option value="daily">每天</option>
          <option value="weekly">每周</option>
          <option value="monthly">每月</option>
        </select>
      </label>
      {frequency === "weekly" && (
        <Field label="星期（0=周日）">
          <TextInput
            type="number"
            min={0}
            max={6}
            value={weekday}
            onChange={(event) => setWeekday(Number(event.target.value))}
          />
        </Field>
      )}
      {frequency === "monthly" && (
        <Field label="日期（1–28）">
          <TextInput
            type="number"
            min={1}
            max={28}
            value={monthday}
            onChange={(event) => setMonthday(Number(event.target.value))}
          />
        </Field>
      )}
      <Field label="时间">
        <TextInput
          type="time"
          value={time}
          onChange={(event) => setTime(event.target.value)}
        />
      </Field>
      <Field label="IANA 时区">
        <TextInput
          value={timezone}
          onChange={(event) => setTimezone(event.target.value)}
        />
      </Field>
      <div className="flex flex-wrap gap-2">
        <Button
          variant="primary"
          disabled={pending || !timezone.trim()}
          onClick={onSave}
        >
          <Save className="size-4" />
          保存
        </Button>
        <Button disabled={pending} onClick={onCancel}>
          <X className="size-4" />
          取消
        </Button>
      </div>
    </div>
  );
}

function describeSchedule(schedule: Schedule) {
  const clock =
    String(schedule.hour).padStart(2, "0") +
    ":" +
    String(schedule.minute).padStart(2, "0");
  if (schedule.frequency === "weekly") {
    const weekdays = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
    return "每周" + weekdays[schedule.day_of_week ?? 0] + " " + clock;
  }
  if (schedule.frequency === "monthly") {
    return "每月 " + (schedule.day_of_month ?? 1) + " 日 " + clock;
  }
  return "每天 " + clock;
}

function showError(error: unknown) {
  toast.error(error instanceof Error ? error.message : "操作失败");
}
