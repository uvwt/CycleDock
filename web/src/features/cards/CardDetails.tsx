import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  Archive,
  Pause,
  Play,
  RotateCcw,
  Trash2,
  XCircle,
  Save,
  CalendarClock,
} from "lucide-react";
import { api } from "../../shared/api/client";
import type {
  CardDetail,
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
  onDeleted,
}: {
  detail: CardDetail;
  onDeleted: () => void;
}) {
  const c = detail.card;
  const [title, setTitle] = useState(c.title),
    [description, setDescription] = useState(c.description);
  const sync = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.card(c.id) }),
      queryClient.invalidateQueries({ queryKey: keys.cards(c.project_id) }),
    ]);
  };
  const update = useMutation({
    mutationFn: () => api.updateCard(c.id, title.trim(), description.trim()),
    onSuccess: () => {
      toast.success("已保存");
      void sync();
    },
    onError: showError,
  });
  const move = useMutation({
    mutationFn: (state: TodoState) => api.moveTodo(c.id, state),
    onSuccess: sync,
    onError: showError,
  });
  const ongoing = useMutation({
    mutationFn: (state: string) => api.ongoingState(c.id, state),
    onSuccess: sync,
    onError: showError,
  });
  const archive = useMutation({
    mutationFn: () => api.archiveDiscussion(c.id),
    onSuccess: sync,
    onError: showError,
  });
  const del = useMutation({
    mutationFn: () => api.deleteDiscussion(c.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: keys.cards(c.project_id),
      });
      onDeleted();
    },
    onError: showError,
  });
  const stop = useMutation({
    mutationFn: () => api.cancelRun(c.id, detail.current_run!.id),
    onSuccess: sync,
    onError: showError,
  });
  const cancel = useMutation({
    mutationFn: (id: string) => api.cancelTrigger(c.id, id),
    onSuccess: sync,
    onError: showError,
  });
  const retry = useMutation({
    mutationFn: (id: string) => api.retry(c.id, id),
    onSuccess: sync,
    onError: showError,
  });
  return (
    <div className="space-y-4 pb-10">
      <Section
        title="任务信息"
        description="描述是持久化工作目标；完整交流过程在消息时间线中。"
      >
        <div className="space-y-4">
          <Field label="标题">
            <TextInput
              value={title}
              readOnly={c.kind === "discussion"}
              onChange={(e) => setTitle(e.target.value)}
              placeholder={
                c.kind === "discussion" ? "由 AI 自动生成标题" : "任务标题"
              }
            />
          </Field>
          <Field label="任务描述">
            <TextArea
              value={description}
              readOnly={c.kind === "discussion"}
              onChange={(e) => setDescription(e.target.value)}
              rows={4}
              placeholder="长期任务上下文"
            />
          </Field>
          {c.kind !== "discussion" && (
            <Button
              variant="primary"
              disabled={!title.trim() || update.isPending}
              onClick={() => update.mutate()}
            >
              <Save className="size-4" />
              保存修改
            </Button>
          )}
        </div>
      </Section>
      {c.kind === "todo" && (
        <Section title="任务状态">
          <div className="flex flex-wrap gap-2">
            {todoStates.map((state) => (
              <Button
                key={state}
                variant={state === c.todo_state ? "primary" : "secondary"}
                disabled={
                  move.isPending ||
                  state === c.todo_state ||
                  (state === "done" && c.todo_state !== "review")
                }
                onClick={() => move.mutate(state)}
              >
                {stateLabels[state]}
              </Button>
            ))}
          </div>
          <p className="mt-3 text-xs text-slate-500">
            只有人类可以最终标记 Done；Agent 负责提交 Review。
          </p>
        </Section>
      )}
      {c.kind === "discussion" && (
        <Section title="讨论管理">
          <div className="flex flex-wrap gap-2">
            {c.discussion_state === "active" && (
              <Button
                onClick={() => archive.mutate()}
                disabled={archive.isPending}
              >
                <Archive className="size-4" />
                归档讨论
              </Button>
            )}
            <Button
              variant="danger"
              onClick={() => {
                if (
                  window.confirm("确定删除这个讨论？已生成的任务不会被删除。")
                )
                  del.mutate();
              }}
              disabled={del.isPending}
            >
              <Trash2 className="size-4" />
              删除讨论
            </Button>
          </div>
        </Section>
      )}
      {c.kind === "ongoing" && (
        <>
          <Section title="持续事项">
            <div className="flex flex-wrap gap-2">
              {(["active", "paused", "archived"] as const).map((state) => (
                <Button
                  key={state}
                  disabled={ongoing.isPending || c.ongoing_state === state}
                  variant={c.ongoing_state === state ? "primary" : "secondary"}
                  onClick={() => ongoing.mutate(state)}
                >
                  {state === "active" ? (
                    <Play className="size-4" />
                  ) : state === "paused" ? (
                    <Pause className="size-4" />
                  ) : (
                    <Archive className="size-4" />
                  )}
                  {state}
                </Button>
              ))}
            </div>
            <p className="mt-3 text-sm text-slate-500">
              健康状态：{detail.health || "unknown"}
            </p>
          </Section>
          <ScheduleEditor
            cardID={c.id}
            schedules={detail.schedules}
            onChange={sync}
          />
        </>
      )}
      {detail.current_run && (
        <Section title="当前执行">
          <p className="text-sm font-semibold">{detail.current_run.status}</p>
          <p className="mt-2 text-sm text-slate-500">
            {detail.current_run.current_stage ||
              detail.current_run.progress_summary ||
              "等待阶段更新"}
          </p>
          <Button
            variant="danger"
            className="mt-3"
            disabled={stop.isPending}
            onClick={() => stop.mutate()}
          >
            <XCircle className="size-4" />
            取消当前执行
          </Button>
        </Section>
      )}
      <Section title={`执行队列 · ${detail.queue.length}`}>
        <div className="space-y-3">
          {detail.queue.length ? (
            detail.queue.map((q) => (
              <div
                className="rounded-xl border border-slate-200 p-3"
                key={q.id}
              >
                <p className="text-sm">{q.content || q.cause}</p>
                <div className="mt-2 flex items-center justify-between">
                  <span className="text-xs text-slate-400">
                    #{q.sequence} · {q.dispatch_mode}
                  </span>
                  <Button
                    variant="ghost"
                    onClick={() => cancel.mutate(q.id)}
                    disabled={cancel.isPending}
                  >
                    取消
                  </Button>
                </div>
              </div>
            ))
          ) : (
            <p className="text-sm text-slate-400">当前没有等待执行的任务</p>
          )}
        </div>
      </Section>
      <Section title="历史执行">
        <div className="space-y-2">
          {detail.work_history.length ? (
            detail.work_history.map((w) => (
              <div
                key={w.trigger_id}
                className="rounded-xl border border-slate-200 p-3"
              >
                <p className="text-sm text-slate-700">{w.content || w.cause}</p>
                <p className="mt-1 text-xs text-slate-400">
                  {w.status} · {new Date(w.created_at).toLocaleString()}
                </p>
                {w.error_summary && (
                  <p className="mt-2 text-xs text-rose-600">
                    {w.error_summary}
                  </p>
                )}
                {w.can_retry && (
                  <Button
                    variant="ghost"
                    className="mt-2"
                    disabled={retry.isPending}
                    onClick={() => retry.mutate(w.trigger_id)}
                  >
                    <RotateCcw className="size-4" />
                    重新执行
                  </Button>
                )}
              </div>
            ))
          ) : (
            <p className="text-sm text-slate-400">暂无历史</p>
          )}
        </div>
      </Section>
    </div>
  );
}
function ScheduleEditor({
  cardID,
  schedules,
  onChange,
}: {
  cardID: string;
  schedules: CardDetail["schedules"];
  onChange: () => Promise<void>;
}) {
  const [frequency, setFrequency] =
    useState<ScheduleInput["frequency"]>("daily");
  const [weekday, setWeekday] = useState(1),
    [monthday, setMonthday] = useState(1);
  const [time, setTime] = useState("09:00");
  const [timezone, setTimezone] = useState(
    Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Shanghai",
  );
  const create = useMutation({
    mutationFn: () => {
      const [hour, minute] = time.split(":").map(Number);
      return api.createSchedule(cardID, {
        frequency,
        hour,
        minute,
        timezone,
        enabled: true,
        ...(frequency === "weekly"
          ? { day_of_week: weekday }
          : frequency === "monthly"
            ? { day_of_month: monthday }
            : {}),
      });
    },
    onSuccess: () => {
      toast.success("日程已添加");
      void onChange();
    },
    onError: showError,
  });
  const modify = useMutation({
    mutationFn: (schedule: CardDetail["schedules"][number]) =>
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
    onSuccess: onChange,
    onError: showError,
  });
  return (
    <Section
      title="定时触发"
      description="日程创建 Trigger，不会打断当前任务。"
    >
      <div className="space-y-2">
        {schedules.map((s) => (
          <div
            key={s.id}
            className="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-slate-50 p-3 text-sm"
          >
            <div>
              <p className="font-semibold">
                {s.frequency} · {String(s.hour).padStart(2, "0")}:
                {String(s.minute).padStart(2, "0")}
              </p>
              <p className="text-xs text-slate-500">
                下次 {new Date(s.next_trigger_at).toLocaleString()} ·{" "}
                {s.timezone}
              </p>
            </div>
            <div className="flex gap-1">
              <Button
                onClick={() => modify.mutate(s)}
                disabled={modify.isPending}
              >
                {s.enabled ? "暂停" : "启用"}
              </Button>
              <Button
                variant="danger"
                onClick={() => del.mutate(s.id)}
                disabled={del.isPending}
              >
                <Trash2 className="size-4" />
              </Button>
            </div>
          </div>
        ))}
      </div>
      <div className="mt-4 space-y-3 rounded-xl border border-dashed border-slate-200 p-3">
        <p className="flex items-center gap-2 text-sm font-semibold">
          <CalendarClock className="size-4" />
          添加日程
        </p>
        <label className="block text-xs">
          频率
          <select
            aria-label="频率"
            value={frequency}
            onChange={(e) =>
              setFrequency(e.target.value as ScheduleInput["frequency"])
            }
            className="mt-1 w-full rounded-lg border border-slate-200 bg-white p-2"
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
              onChange={(e) => setWeekday(Number(e.target.value))}
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
              onChange={(e) => setMonthday(Number(e.target.value))}
            />
          </Field>
        )}
        <Field label="时间">
          <TextInput
            type="time"
            value={time}
            onChange={(e) => setTime(e.target.value)}
          />
        </Field>
        <Field label="IANA 时区">
          <TextInput
            value={timezone}
            onChange={(e) => setTimezone(e.target.value)}
          />
        </Field>
        <Button
          variant="primary"
          disabled={create.isPending || !timezone.trim()}
          onClick={() => create.mutate()}
        >
          添加日程
        </Button>
      </div>
    </Section>
  );
}
function showError(error: unknown) {
  toast.error(error instanceof Error ? error.message : "操作失败");
}
